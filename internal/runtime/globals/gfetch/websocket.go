package gfetch

// Web API：WebSocket 客户端（开发计划 3.2）。
//
// 实现要点：
//   - new WebSocket(url)：基于 gorilla/websocket 在 goroutine 连接，
//     PostTask 回 JS 线程触发 open/message/close/error 事件。
//   - 事件分发：支持 onopen/onmessage/onclose/onerror 属性 + addEventListener
//     （复用 EventTarget 的事件能力 + 额外调 onxxx 属性）。
//   - send(data) 发送文本；close() 关闭连接。

import (
	"fmt"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gevent"
	"github.com/gorilla/websocket"
)

// WebSocketConfig 配置 WebSocket 全局。
type WebSocketConfig struct{}

// NewWebSocket 注册全局 WebSocket。
func NewWebSocket(ctx engine.Context, cfg WebSocketConfig) error {
	_ = ctx.Global().Set("WebSocket", engine.NewFunction("WebSocket", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("WebSocket: URL required")
		}
		return newWebSocketInstance(ctx, args[0].String()), nil
	}))
	return nil
}

// wsConnState 是 WebSocket 连接状态（conn 在 goroutine 中赋值）。
type wsConnState struct {
	mu         sync.Mutex
	conn       *websocket.Conn
	closeFired bool
}

func (s *wsConnState) setConn(c *websocket.Conn) {
	s.mu.Lock()
	s.conn = c
	s.mu.Unlock()
}

func (s *wsConnState) getConn() *websocket.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

// finalizeClose 触发一次 close 事件（连接终态统一入口）。
func (s *wsConnState) finalizeClose(ctx engine.Context, ws engine.Object) {
	s.mu.Lock()
	if s.closeFired {
		s.mu.Unlock()
		return
	}
	s.closeFired = true
	s.mu.Unlock()
	_ = ws.Set("readyState", engine.IntValue(3))
	wsDispatch(ctx, ws, "close", engine.Undefined())
}

// newWebSocketInstance 构造 WebSocket 对象并异步连接。
func newWebSocketInstance(ctx engine.Context, url string) engine.Value {
	ws := gevent.NewEventTargetInstance().(engine.Object)
	state := &wsConnState{}
	_ = ws.Set("readyState", engine.IntValue(0)) // CONNECTING
	_ = ws.Set("url", engine.Str(url))
	_ = ws.Set("onopen", engine.Undefined())
	_ = ws.Set("onmessage", engine.Undefined())
	_ = ws.Set("onclose", engine.Undefined())
	_ = ws.Set("onerror", engine.Undefined())
	// 表面属性（Node 语义默认值）。
	_ = ws.Set("binaryType", engine.Str("blob"))
	_ = ws.Set("bufferedAmount", engine.IntValue(0))
	_ = ws.Set("extensions", engine.Str(""))
	_ = ws.Set("protocol", engine.Str(""))

	// 常量。
	_ = ws.Set("CONNECTING", engine.IntValue(0))
	_ = ws.Set("OPEN", engine.IntValue(1))
	_ = ws.Set("CLOSING", engine.IntValue(2))
	_ = ws.Set("CLOSED", engine.IntValue(3))

	// send(data)
	_ = ws.Set("send", engine.NewFunction("send", func(args []engine.Value) (engine.Value, error) {
		msg := ""
		if len(args) > 0 {
			msg = args[0].String()
		}
		conn := state.getConn()
		if conn != nil {
			err := conn.WriteMessage(websocket.TextMessage, []byte(msg))
			return engine.Boolean(err == nil), err
		}
		return engine.Boolean(false), nil
	}))

	// close()：关闭底层连接；close 事件由读循环终态统一触发一次。
	_ = ws.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		conn := state.getConn()
		if conn != nil {
			_ = conn.Close()
		}
		state.finalizeClose(ctx, ws)
		return engine.Undefined(), nil
	}))

	// 异步连接。
	release := ctx.AddRef()
	go func() {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			ctx.PostTask(func() {
				defer release()
				wsDispatch(ctx, ws, "error", engine.Str(err.Error()))
				state.finalizeClose(ctx, ws)
			})
			return
		}
		state.setConn(conn)
		// 连接成功。
		ctx.PostTask(func() {
			_ = ws.Set("readyState", engine.IntValue(1))
			wsDispatch(ctx, ws, "open", engine.Undefined())
		})
		// 读循环。
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			msg := string(data)
			ctx.PostTask(func() {
				wsDispatch(ctx, ws, "message", engine.Str(msg))
			})
		}
		// 连接关闭（终态）。
		ctx.PostTask(func() {
			defer release()
			state.finalizeClose(ctx, ws)
		})
	}()
	return ws
}

// wsDispatch 在 WebSocket 对象上触发事件：调 on<event> 属性 + dispatchEvent。
func wsDispatch(ctx engine.Context, ws engine.Object, event string, data engine.Value) {
	// on<event> 属性。
	if v, err := ws.Get("on" + event); err == nil && v.IsFunction() {
		if f, ok := v.AsFunction(); ok {
			ev := engine.NewObject()
			_ = ev.Set("type", engine.Str(event))
			if data != nil && !data.IsUndefined() {
				_ = ev.Set("data", data)
			}
			if _, err := f.Call([]engine.Value{ev}); err != nil {
				interpreter.ReportUncaught(ctx, err)
			}
		}
	}
	// EventTarget dispatchEvent（addEventListener 监听器）。
	ev, _ := gevent.NewEventInstance([]engine.Value{engine.Str(event)}).AsObject()
	if data != nil && !data.IsUndefined() {
		_ = ev.Set("data", data)
	}
	gevent.Dispatch(ws, ev)
}
