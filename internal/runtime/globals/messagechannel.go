package globals

// Web API：MessageChannel / MessagePort（开发计划 3.7）。
//
// 实现：port1.postMessage(data) 经 ctx.PostTask 投递到 JS 线程，触发
// port2 的 'message' 事件（onmessage 属性或 addEventListener 监听器）。
// 消息直接传递 engine.Value 引用（简化：不做结构化克隆）。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// MessageConfig 配置 MessageChannel 全局。
type MessageConfig struct{}

// NewMessageChannel 注册全局 MessageChannel。
func NewMessageChannel(ctx engine.Context, cfg MessageConfig) error {
	_ = ctx.Global().Set("MessageChannel", engine.NewFunction("MessageChannel", func(args []engine.Value) (engine.Value, error) {
		s1 := &msgPortState{ctx: ctx}
		s2 := &msgPortState{ctx: ctx}
		s1.other = s2
		s2.other = s1
		p1 := newMessagePortInstance(s1)
		p2 := newMessagePortInstance(s2)
		channel := engine.NewObject()
		_ = channel.Set("port1", p1)
		_ = channel.Set("port2", p2)
		return channel, nil
	}))
	return nil
}

// msgPortState 是 MessagePort 的内部状态。
type msgPortState struct {
	ctx       engine.Context
	other     *msgPortState
	self      engine.Value // port 对象（读 onmessage 属性）
	listeners []engine.Value
	started   bool
}

// newMessagePortInstance 构造 MessagePort 对象。
func newMessagePortInstance(state *msgPortState) engine.Value {
	port := engine.NewObject()

	_ = port.Set("postMessage", engine.NewFunction("postMessage", func(args []engine.Value) (engine.Value, error) {
		var msg engine.Value
		if len(args) > 0 {
			msg = args[0]
		}
		// 投递到另一端口（JS 线程）。
		other := state.other
		if other != nil {
			other.ctx.PostTask(func() {
				deliverMessage(other, msg)
			})
		}
		return engine.Undefined(), nil
	}))

	_ = port.Set("start", engine.NewFunction("start", func(args []engine.Value) (engine.Value, error) {
		state.started = true
		return engine.Undefined(), nil
	}))

	_ = port.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		state.listeners = nil
		return engine.Undefined(), nil
	}))

	_ = port.Set("addEventListener", engine.NewFunction("addEventListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		if args[0].String() == "message" && args[1].IsFunction() {
			state.listeners = append(state.listeners, args[1])
		}
		return engine.Undefined(), nil
	}))

	_ = port.Set("removeEventListener", engine.NewFunction("removeEventListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		target := args[1]
		out := state.listeners[:0]
		for _, l := range state.listeners {
			if l != target {
				out = append(out, l)
			}
		}
		state.listeners = out
		return engine.Undefined(), nil
	}))

	// onmessage 属性（可赋值）。
	_ = port.Set("onmessage", engine.Undefined())
	state.self = port

	return port
}

// deliverMessage 在端口上分发一条消息。
func deliverMessage(state *msgPortState, msg engine.Value) {
	// 构造 MessageEvent 简化对象 {data}。
	ev := engine.NewObject()
	_ = ev.Set("data", msg)

	// onmessage 回调（从 port 对象读取 JS 侧赋值）。
	if state.self != nil {
		if o, ok := state.self.AsObject(); ok {
			if om, err := o.Get("onmessage"); err == nil && om.IsFunction() {
				if f, ok := om.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{ev})
				}
			}
		}
	}
	// addEventListener 监听器。
	for _, l := range state.listeners {
		if f, ok := l.AsFunction(); ok {
			_, _ = f.Call([]engine.Value{ev})
		}
	}
}
