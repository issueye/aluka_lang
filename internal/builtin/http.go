package builtin

// node:http 内置模块——HTTP 服务器与客户端。
//
// 架构：
//   - 服务器用 Go net/http.Server 在独立 goroutine 监听端口。
//   - 请求到达时，在 net/http handler goroutine 构造 JS 的 IncomingMessage
//     / ServerResponse 对象，然后通过 ctx.PostTask 投递到 JS 线程调用用户 handler。
//   - 客户端用 Go net/http.Client 发请求，响应到达时同样 PostTask 回调 JS。
//
// 注意：handler 回调（engine.Function.Call）只能在 JS 线程执行，因此所有
// 从 Go goroutine 出发的 JS 调用必须经 PostTask。

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewHTTP 构造 node:http 模块的导出对象。
func NewHTTP(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// http.Agent + globalAgent（keepAlive 连接复用）。
	registerHttpAgent(ctx, m)

	// http.createServer([handler]) → Server 对象。
	_ = m.Set("createServer", engine.NewFunction("createServer", func(args []engine.Value) (engine.Value, error) {
		var handler engine.Value
		if len(args) > 0 && args[0].IsFunction() {
			handler = args[0]
		}
		return newHTTPServer(ctx, handler), nil
	}))

	// http.request(options, callback) → ClientRequest 对象。
	_ = m.Set("request", engine.NewFunction("request", func(args []engine.Value) (engine.Value, error) {
		return newClientRequest(ctx, args), nil
	}))

	// http.get(options, callback) → ClientRequest 对象（自动 end）。
	_ = m.Set("get", engine.NewFunction("get", func(args []engine.Value) (engine.Value, error) {
		req := newClientRequest(ctx, args)
		if o, ok := req.AsObject(); ok {
			if endFn, err := o.Get("end"); err == nil && endFn.IsFunction() {
				if f, ok := endFn.AsFunction(); ok {
					_, _ = f.Call(nil)
				}
			}
		}
		return req, nil
	}))

	// 状态码常量（常用子集）。
	_ = m.Set("STATUS_CODES", httpStatusCodes())

	// HTTP 方法名列表（大写）。express 的 utils 依赖 `METHODS.map(...)`
	// 生成小写方法名集合，缺失时 METHODS 为 undefined 导致 "reading 'map'"。
	_ = m.Set("METHODS", httpMethods())

	// http.IncomingMessage：express 在模块加载时 `Object.create(http.
	// IncomingMessage.prototype)` 构造请求原型对象，缺失时读取 'prototype'
	// 报 TypeError。实际请求对象由 newIncomingMessage 构造，且在 handler
	// 派发时被 express 以 Object.setPrototypeOf 重新挂到 app.request 上。
	_ = m.Set("IncomingMessage", newIncomingMessageCtor())

	// http.ServerResponse：express 在 response.js 里 `Object.create(http.
	// ServerResponse.prototype)` 构造响应原型对象，缺失时读取 'prototype'
	// 报 TypeError。实际响应对象由 newServerResponse 构造，且在 handler
	// 派发时被 express 以 Object.setPrototypeOf 重新挂到 app.response 上。
	_ = m.Set("ServerResponse", newServerResponseCtor(ctx))

	// http.validateHeaderName/validateHeaderValue：低层校验（no-op 返回 undefined）。
	_ = m.Set("validateHeaderName", engine.NewFunction("validateHeaderName", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("validateHeaderValue", engine.NewFunction("validateHeaderValue", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	return m, nil
}

// newIncomingMessageCtor 构造 IncomingMessage 构造器（含可继承的 prototype）。
func newIncomingMessageCtor() engine.Value {
	ctor := engine.NewFunction("IncomingMessage", func(args []engine.Value) (engine.Value, error) {
		return newIncomingMessage(nil, nil), nil
	})
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	if co, ok := ctor.AsObject(); ok {
		_ = co.Set("prototype", proto)
	}
	return ctor
}

// --- 服务器 --------------------------------------------------------------

// httpServerState 是 HTTP 服务器的内部状态。
type httpServerState struct {
	ctx           engine.Context
	server        engine.Value // 服务器对象（EventEmitter）
	handler       engine.Value // 用户 handler
	httpSrv       *http.Server
	tlsConfig     *tls.Config // 非 nil 时启用 HTTPS
	mu            sync.Mutex
	listening     bool
	addr          string
	ln            net.Listener // 实际监听器（用于获取动态端口）
	releaseHandle func()       // 事件循环活跃度释放（server.close 时调用）
	closed        bool
}

// newHTTPServer 创建明文 HTTP Server 对象（基于 EventEmitter）。
func newHTTPServer(ctx engine.Context, handler engine.Value) engine.Value {
	return newHTTPServerWithTLS(ctx, handler, nil)
}

// newHTTPServerWithTLS 创建 Server 对象；tlsConfig 非 nil 时启用 HTTPS。
func newHTTPServerWithTLS(ctx engine.Context, handler engine.Value, tlsConfig *tls.Config) engine.Value {
	server := newEmitterInstance().(engine.Object)
	state := &httpServerState{ctx: ctx, handler: handler, tlsConfig: tlsConfig, server: server}

	// Node 表面属性（server.timeout/keepAliveTimeout 等）。
	_ = server.Set("listening", engine.Boolean(false))
	_ = server.Set("timeout", engine.IntValue(0))
	_ = server.Set("keepAliveTimeout", engine.IntValue(5000))
	_ = server.Set("maxHeadersCount", engine.Null())
	_ = server.Set("headersTimeout", engine.IntValue(60000))
	_ = server.Set("requestTimeout", engine.IntValue(300000))
	_ = server.Set("maxRequestsPerSocket", engine.IntValue(0))

	// server.setTimeout([msecs][, callback])：超时默认 0（禁），no-op 兼容。
	_ = server.Set("setTimeout", engine.NewFunction("setTimeout", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && args[0].Type() == engine.TypeNumber {
			if n, ok := args[0].Int(); ok {
				_ = server.Set("timeout", engine.IntValue(n))
			}
		}
		if len(args) > 1 && args[1].IsFunction() {
			if f, ok := args[1].AsFunction(); ok {
				_, _ = f.Call(nil)
			}
		}
		return server, nil
	}))
	_ = server.Set("closeAllConnections", engine.NewFunction("closeAllConnections", func(args []engine.Value) (engine.Value, error) {
		return server, nil
	}))
	_ = server.Set("closeIdleConnections", engine.NewFunction("closeIdleConnections", func(args []engine.Value) (engine.Value, error) {
		return server, nil
	}))
	_ = server.Set("getConnections", engine.NewFunction("getConnections", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && args[0].IsFunction() {
			if f, ok := args[0].AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.Null(), engine.IntValue(0)})
			}
		}
		return server, nil
	}))

	// server.listen(port[, hostname][, callback])
	_ = server.Set("listen", engine.NewFunction("listen", func(args []engine.Value) (engine.Value, error) {
		port := 0
		if len(args) > 0 {
			port = intArg(args, 0, 0)
		}
		host := ""
		var callback engine.Value
		if len(args) > 1 {
			if args[1].IsFunction() {
				callback = args[1]
			} else {
				host = args[1].String()
				if len(args) > 2 && args[2].IsFunction() {
					callback = args[2]
				}
			}
		}
		if port <= 0 {
			port = 0 // 自动分配
		}
		addr := fmt.Sprintf("%s:%d", host, port)
		// 服务器计入事件循环活跃度：RunLoop 会等待服务器运行，直到 close 释放。
		releaseHandle := ctx.AddRef()
		state.mu.Lock()
		state.addr = addr
		state.releaseHandle = releaseHandle
		state.mu.Unlock()

		// 启动 Go HTTP 服务器。
		srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state.handleRequest(w, r)
		})}
		// 连接建立 → 'connection' 事件（Node 语义：server.on('connection')）。
		srv.ConnState = func(c net.Conn, cs http.ConnState) {
			if cs == http.StateNew {
				ctx.PostTask(func() {
					emitEvent(server, "connection", engine.Undefined())
				})
			}
		}
		state.mu.Lock()
		state.httpSrv = srv
		state.listening = true
		state.mu.Unlock()
		_ = server.Set("listening", engine.Boolean(true))

		// 在 goroutine 监听（不阻塞 JS）。
		go func() {
			// 用 net.Listen 手动监听，捕获错误。
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				ctx.PostTask(func() {
					emitEvent(server, "error", engine.Str(err.Error()))
				})
				return
			}
			state.mu.Lock()
			state.ln = ln
			state.mu.Unlock()
			// 监听就绪后触发 listen 回调（此时 address() 返回真实端口）。
			if callback != nil {
				ctx.PostTask(func() {
					if f, ok := callback.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				})
			}
			ctx.PostTask(func() {
				emitEvent(server, "listening")
			})
			if state.tlsConfig != nil {
				// HTTPS：用 TLS listener 包装。
				err = srv.Serve(tls.NewListener(ln, state.tlsConfig))
			} else {
				err = srv.Serve(ln)
			}
			if err != nil && err != http.ErrServerClosed {
				ctx.PostTask(func() {
					emitEvent(server, "error", engine.Str(err.Error()))
				})
			}
		}()
		return server, nil
	}))

	// server.close([callback])
	_ = server.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		var callback engine.Value
		if len(args) > 0 && args[0].IsFunction() {
			callback = args[0]
		}
		state.mu.Lock()
		srv := state.httpSrv
		state.listening = false
		_ = server.Set("listening", engine.Boolean(false))
		var release func()
		if !state.closed {
			state.closed = true
			release = state.releaseHandle
			state.releaseHandle = nil
		}
		state.mu.Unlock()

		if srv != nil {
			// 在 goroutine 关闭服务器。释放事件循环活跃度必须延迟到
			// close 回调投递并执行之后——否则 RunLoop 可能在回调前退出。
			go func() {
				_ = srv.Close()
				if callback != nil {
					ctx.PostTask(func() {
						if f, ok := callback.AsFunction(); ok {
							_, _ = f.Call(nil)
						}
						if release != nil {
							release()
						}
					})
				} else if release != nil {
					release()
				}
			}()
		} else {
			// 从未监听：同步回调并立即释放。
			if callback != nil {
				if f, ok := callback.AsFunction(); ok {
					_, _ = f.Call(nil)
				}
			}
			if release != nil {
				release()
			}
		}
		return server, nil
	}))

	// server.address() → {address, family, port}
	_ = server.Set("address", engine.NewFunction("address", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if !state.listening {
			return engine.Null(), nil
		}
		port := 0
		host := "127.0.0.1"
		if state.ln != nil {
			if tcpAddr, ok := state.ln.Addr().(*net.TCPAddr); ok {
				port = tcpAddr.Port
				if tcpAddr.IP != nil {
					host = tcpAddr.IP.String()
				}
			}
		}
		addr := engine.NewObject()
		_ = addr.Set("address", engine.Str(host))
		_ = addr.Set("family", engine.Str("IPv4"))
		_ = addr.Set("port", engine.IntValue(port))
		return addr, nil
	}))

	return server
}

// handleRequest 处理一个 HTTP 请求（在 net/http goroutine 调用）。
// 构造 JS 请求/响应对象，经 PostTask 投递到 JS 线程执行用户 handler，
// 并阻塞等待 handler 完成——确保响应体在 Go handler 返回前写入
// （否则 Go net/http 会先发送空响应）。
func (s *httpServerState) handleRequest(w http.ResponseWriter, r *http.Request) {
	// HTTP Upgrade / CONNECT 请求：Node 语义为触发 server 'upgrade'/'connect'
	// 事件并把原始 socket 交给用户，而不是走普通 handler。用 Go http.Hijacker
	// 劫持连接后构造 net.Socket 投递给 JS 线程。
	if isUpgradeRequest(r) {
		s.handleUpgradeRequest(w, r)
		return
	}
	if s.handler == nil || !s.handler.IsFunction() {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("no handler"))
		return
	}
	// 读取请求体（同步，阻塞 net/http goroutine 但可接受）。
	body, _ := io.ReadAll(r.Body)

	// 在 JS 线程构造并调用 handler；回调必须尽快返回，把等待放在
	// http goroutine 上——否则阻塞 JS 线程导致定时器/IO 回调无法执行。
	resCh := make(chan *respState, 1)
	s.ctx.PostTask(func() {
		req := newIncomingMessage(r, body)
		res, resState := newServerResponse(s.ctx, w)
		resCh <- resState
		if f, ok := s.handler.AsFunction(); ok {
			_, _ = f.Call([]engine.Value{req, res})
		}
		// 驱动已就绪的微任务（async/await 链）；定时器/IO 触发的 res.end
		// 由事件循环的后续任务自然驱动。
		s.ctx.FlushMicrotasks()
		// handler 同步部分已注册监听器，发射请求体事件（'data'/'end'）。
		emitIncomingData(req, body)
	})

	// http goroutine 等待响应完成（res.end → done 关闭）。
	resState := <-resCh
	select {
	case <-resState.done:
	case <-time.After(30 * time.Second):
	}
	// 兜底：异常路径仍未 end，提交已缓冲内容（避免空响应）。
	if !resState.finished {
		resState.flushHeadersOnce()
		resState.writeBuffered()
		resState.sendTrailers()
		resState.finished = true
	}
}

// isUpgradeRequest 判断请求是否为 HTTP Upgrade（Connection: Upgrade 等）或
// CONNECT 方法（Node 分别触发 server 的 'upgrade' 与 'connect' 事件）。
func isUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method == "CONNECT" {
		return true
	}
	for _, v := range r.Header.Values("Connection") {
		if strings.Contains(strings.ToLower(v), "upgrade") {
			return true
		}
	}
	return strings.TrimSpace(r.Header.Get("Upgrade")) != ""
}

// handleUpgradeRequest 劫持连接并触发 server 'upgrade'/'connect' 事件。
func (s *httpServerState) handleUpgradeRequest(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(500)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	// head：hijack 后可能已缓冲的请求头之后的数据。
	var head []byte
	if rw != nil && rw.Reader != nil && rw.Reader.Buffered() > 0 {
		head, _ = rw.Reader.Peek(rw.Reader.Buffered())
	}
	event := "upgrade"
	if r.Method == "CONNECT" {
		event = "connect"
	}
	serverObj := s.server
	// 构造 net.Socket（立即启动读循环）。
	socket, _ := newNetSocket(s.ctx, conn)
	s.ctx.PostTask(func() {
		req := newIncomingMessage(r, nil)
		emitEvent(serverObj, event, req, socket, globals.NewBufferInstance(head))
	})
}

// resFinishedKey 记录响应是否已结束（res.end 调用）的隐藏属性。
const resFinishedKey = "\x00<aluka>resFinished"

// resFinished 判断响应是否已通过 res.end 结束。
func resFinished(res engine.Value) bool {
	if o, ok := res.AsObject(); ok {
		if v, err := o.Get(resFinishedKey); err == nil && v.Type() == engine.TypeBoolean {
			b, _ := v.Bool()
			return b
		}
	}
	return false
}

// newIncomingMessage 构造 IncomingMessage 对象（请求/响应消息）。
// 注意：不在构造时发射 'data'/'end'——监听器由 JS handler/回调在收到对象后
// 注册，过早发射会丢失事件。发射延迟到 emitIncomingData（handler 执行之后）。
func newIncomingMessage(r *http.Request, body []byte) engine.Value {
	msg := newEmitterInstance().(engine.Object)

	// 兼容 nil URL（客户端响应场景不传 URL）。
	urlStr := ""
	if r != nil && r.URL != nil {
		urlStr = r.URL.RequestURI()
	}
	method := ""
	if r != nil {
		method = r.Method
	}
	_ = msg.Set("method", engine.Str(method))
	_ = msg.Set("url", engine.Str(urlStr))
	_ = msg.Set("httpVersion", engine.Str("1.1"))
	// 流式方法（简化：aluka 响应一次性给出，resume/pause 为空操作）。
	_ = msg.Set("resume", engine.NewFunction("resume", func(args []engine.Value) (engine.Value, error) {
		return msg, nil
	}))
	_ = msg.Set("pause", engine.NewFunction("pause", func(args []engine.Value) (engine.Value, error) {
		return msg, nil
	}))
	_ = msg.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		return msg, nil
	}))
	if r != nil {
		h := headersToObj(r.Header)
		// Go 的 http.Request 把 content-length 存在 ContentLength 字段而非
		// Header 里。body-parser 等依赖 req.headers['content-length']（或
		// transfer-encoding）判断是否有请求体（typeis.hasBody），缺失会被
		// 当作无 body 而跳过解析，导致 req.body 为 null。这里补上小写键。
		if r.ContentLength > 0 {
			if ho, ok := h.AsObject(); ok {
				_ = ho.Set("content-length", engine.Str(strconv.FormatInt(r.ContentLength, 10)))
			}
		}
		_ = msg.Set("headers", h)
	} else {
		_ = msg.Set("headers", engine.NewObject())
	}

	return msg
}

// emitIncomingData 在消息对象上发射请求/响应体事件 'data'/'end'。
// 必须在 handler/回调返回之后调用，此时监听器已注册完成。
//
// chunk 按 Node 语义发射 Buffer 而非 string：依赖方（如 raw-body/body-parser）
// 用 Buffer.concat 合并 chunk，string chunk 会被静默丢弃导致请求体为空。
// Buffer 的 String() 返回内容，`body += chunk` 等字符串拼接路径行为不变。
func emitIncomingData(msg engine.Value, body []byte) {
	if len(body) > 0 {
		emitEvent(msg, "data", globals.NewBufferInstance(body))
	}
	emitEvent(msg, "end")
}

// newServerResponseCtor 构造 ServerResponse 构造器（含可继承的 prototype）。
func newServerResponseCtor(ctx engine.Context) engine.Value {
	ctor := engine.NewFunction("ServerResponse", func(args []engine.Value) (engine.Value, error) {
		v, _ := newServerResponse(ctx, nil)
		return v, nil
	})
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	if co, ok := ctor.AsObject(); ok {
		_ = co.Set("prototype", proto)
	}
	return ctor
}

// newServerResponse 构造 ServerResponse 对象（响应）。
// 返回 (对象, 内部状态)，状态供 handleRequest 在 handler 未 end 时兜底 flush。
func newServerResponse(ctx engine.Context, w http.ResponseWriter) (engine.Value, *respState) {
	res := newEmitterInstance().(engine.Object)

	state := &respState{
		ctx:      ctx,
		w:        w,
		status:   200,
		headers:  make(map[string][]string),
		trailers: make(map[string][]string),
		finished: false,
		done:     make(chan struct{}),
	}

	// res.statusCode：accessor（Node 语义 res.statusCode = N 修改实际状态码）。
	statusGet := engine.NewFunction("statusCodeGet", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(state.status), nil
	})
	statusSet := engine.NewFunction("statusCodeSet", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			state.status = intArg(args, 0, 200)
		}
		return engine.Undefined(), nil
	})
	engine.SetAccessor(res, "statusCode", statusGet, statusSet)

	// res.writableEnded：response 是否已 end。
	_ = res.Set("writableEnded", engine.Boolean(false))

	// res.writeHead(statusCode[, statusMessage][, headers])：立即写入状态码与 headers。
	_ = res.Set("writeHead", engine.NewFunction("writeHead", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			state.status = intArg(args, 0, 200)
		}
		// (statusCode, statusMessage, headers) / (statusCode, headers) 两种形式。
		for i := 1; i < len(args); i++ {
			if args[i].IsObject() {
				if h, ok := args[i].AsObject(); ok {
					state.applyHeaders(h)
				}
				break
			}
		}
		state.flushHeadersOnce() // 立即提交 headers
		state.flushed = true     // 后续 write 直写（Node writeHead 语义）
		return res, nil
	}))

	// res.write(chunk)：缓冲到内存，end 时统一提交——保证 addTrailers 在
	// WriteHeader 前声明（Go 语义：Trailer 头必须在 WriteHeader 前设置）。
	// 超过阈值或显式 flushHeaders 后直写。
	_ = res.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && !state.finished {
			state.writeChunk([]byte(args[0].String()))
		}
		return engine.Boolean(true), nil
	}))

	// res.end([chunk])
	_ = res.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		if state.finished {
			return res, nil
		}
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() && !args[0].IsFunction() {
			state.writeChunk([]byte(args[0].String()))
		}
		// 先 flush headers（含状态码与 Trailer 声明），再写 body + trailers。
		state.flushHeadersOnce()
		state.writeBuffered()
		state.sendTrailers()
		state.finished = true
		_ = res.Set(resFinishedKey, engine.Boolean(true))
		_ = res.Set("writableEnded", engine.Boolean(true))
		state.signalDone() // 通知 handleRequest 等待循环
		// Node 语义：'finish'/'close' 在响应提交后异步触发（先于其注册的
		// 监听器也能收到）。用 PostTask 延迟到当前同步执行结束后发射。
		state.ctx.PostTask(func() {
			emitEvent(res, "finish")
			emitEvent(res, "close")
		})
		return res, nil
	}))

	// res.setHeader(name, value)
	_ = res.Set("setHeader", engine.NewFunction("setHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			state.headers[strings.ToLower(args[0].String())] = headerValues(args[1])
		}
		return res, nil
	}))

	// res.getHeader(name)
	_ = res.Set("getHeader", engine.NewFunction("getHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if vals, ok := state.headers[strings.ToLower(args[0].String())]; ok && len(vals) > 0 {
				return engine.Str(vals[0]), nil
			}
		}
		return engine.Undefined(), nil
	}))

	// res.getHeaders()：返回 {name: value|array}，键小写。
	_ = res.Set("getHeaders", engine.NewFunction("getHeaders", func(args []engine.Value) (engine.Value, error) {
		return state.headersObj(), nil
	}))

	// res.hasHeader(name)
	_ = res.Set("hasHeader", engine.NewFunction("hasHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			_, ok := state.headers[strings.ToLower(args[0].String())]
			return engine.Boolean(ok), nil
		}
		return engine.Boolean(false), nil
	}))

	// res.removeHeader(name)
	_ = res.Set("removeHeader", engine.NewFunction("removeHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			delete(state.headers, strings.ToLower(args[0].String()))
		}
		return res, nil
	}))

	// res.addTrailers(headers)：chunked 响应的 trailer 头。
	_ = res.Set("addTrailers", engine.NewFunction("addTrailers", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if h, ok := args[0].AsObject(); ok {
				for _, k := range h.Keys() {
					if v, err := h.Get(k); err == nil {
						state.trailers[strings.ToLower(k)] = headerValues(v)
					}
				}
			}
		}
		return res, nil
	}))

	// res.flushHeaders()：立即提交 headers 与已缓冲 body（后续 write 直写）。
	_ = res.Set("flushHeaders", engine.NewFunction("flushHeaders", func(args []engine.Value) (engine.Value, error) {
		state.flushHeadersOnce()
		state.writeBuffered()
		return res, nil
	}))

	// res.writeContinue()：发送 100 Continue（简化 no-op）。
	_ = res.Set("writeContinue", engine.NewFunction("writeContinue", func(args []engine.Value) (engine.Value, error) {
		return res, nil
	}))

	// res.cork() / res.uncork()：流式缓冲 no-op。
	_ = res.Set("cork", engine.NewFunction("cork", func(args []engine.Value) (engine.Value, error) {
		return res, nil
	}))
	_ = res.Set("uncork", engine.NewFunction("uncork", func(args []engine.Value) (engine.Value, error) {
		return res, nil
	}))

	// res.setTimeout([msecs][, callback])：no-op 兼容。
	_ = res.Set("setTimeout", engine.NewFunction("setTimeout", func(args []engine.Value) (engine.Value, error) {
		return res, nil
	}))

	return res, state
}

// respState 是 ServerResponse 的内部状态。
type respState struct {
	ctx            engine.Context
	w              http.ResponseWriter
	status         int
	headers        map[string][]string
	trailers       map[string][]string
	finished       bool
	headersWritten bool
	flushed        bool
	body           bytes.Buffer // 缓冲 body（end 时统一提交，保证 Trailer 声明时序）
	done           chan struct{}
	doneOnce       sync.Once
}

// signalDone 通知 handleRequest 的等待循环响应已结束。
func (rs *respState) signalDone() {
	rs.doneOnce.Do(func() { close(rs.done) })
}

// maxResponseBuffer 超过该阈值后 write 直写（避免大响应无限缓冲）。
const maxResponseBuffer = 1 << 20 // 1 MiB

// writeChunk 写入一个响应 chunk（未提交前缓冲）。
func (rs *respState) writeChunk(chunk []byte) {
	if rs.flushed || rs.body.Len()+len(chunk) > maxResponseBuffer {
		rs.flushHeadersOnce()
		rs.flushed = true
		_, _ = rs.w.Write(chunk)
		return
	}
	rs.body.Write(chunk)
}

// writeBuffered 把缓冲的 body 写到底层 ResponseWriter。
func (rs *respState) writeBuffered() {
	if rs.flushed {
		return
	}
	rs.flushed = true
	if rs.body.Len() > 0 {
		_, _ = rs.w.Write(rs.body.Bytes())
	}
}

// headerValues 把 JS header 值（string 或 string[]）转为 []string。
func headerValues(v engine.Value) []string {
	if av, ok := v.(*engine.ArrayValue); ok {
		vals := make([]string, 0, len(av.Elems()))
		for _, e := range av.Elems() {
			if !e.IsUndefined() && !e.IsNull() {
				vals = append(vals, e.String())
			}
		}
		return vals
	}
	return []string{v.String()}
}

// headersObj 返回 getHeaders() 的结果对象（键小写，数组值保留数组）。
func (rs *respState) headersObj() engine.Value {
	obj := engine.NewObject()
	for k, vals := range rs.headers {
		lk := strings.ToLower(k)
		if len(vals) == 1 {
			_ = obj.Set(lk, engine.Str(vals[0]))
		} else {
			arr := make([]engine.Value, len(vals))
			for i, v := range vals {
				arr[i] = engine.Str(v)
			}
			_ = obj.Set(lk, engine.NewArray(arr))
		}
	}
	return obj
}

// flushHeadersOnce 只写入一次 headers/状态码（避免 Go 重复 WriteHeader 警告）。
func (rs *respState) flushHeadersOnce() {
	if rs.headersWritten {
		return
	}
	rs.headersWritten = true
	for k, vals := range rs.headers {
		for _, v := range vals {
			rs.w.Header().Add(k, v)
		}
	}
	// 声明 trailer 名称（Go 在 handler 返回时发送 trailer 值）。
	for name := range rs.trailers {
		rs.w.Header().Add("Trailer", name)
	}
	hasHeaders := len(rs.headers) > 0
	if hasHeaders || rs.status != 200 {
		rs.w.WriteHeader(rs.status)
	}
}

// sendTrailers 在 body 写完（res.end）后提交 trailer 值。
func (rs *respState) sendTrailers() {
	for name, vals := range rs.trailers {
		for _, v := range vals {
			rs.w.Header().Add(http.TrailerPrefix+name, v)
		}
	}
}

// applyHeaders 应用 header 对象到响应。
func (rs *respState) applyHeaders(h engine.Object) {
	for _, k := range h.Keys() {
		if v, err := h.Get(k); err == nil {
			rs.headers[strings.ToLower(k)] = headerValues(v)
		}
	}
}

// headersToObj 将 http.Header 转为 JS 对象。
func headersToObj(h http.Header) engine.Value {
	obj := engine.NewObject()
	for k, vals := range h {
		if len(vals) > 0 {
			// Node 约定 req.headers 键为小写；body-parser/type-is 等用
			// req.headers['content-type'] 等下划线访问，大小写敏感。
			_ = obj.Set(strings.ToLower(k), engine.Str(strings.Join(vals, ", ")))
		}
	}
	return obj
}

// httpStatusCodes 返回状态码常量对象。
func httpStatusCodes() engine.Value {
	obj := engine.NewObject()
	codes := map[int]string{
		200: "OK", 201: "Created", 202: "Accepted",
		301: "Moved Permanently", 302: "Found",
		400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found",
		500: "Internal Server Error", 502: "Bad Gateway", 503: "Service Unavailable",
	}
	for code, text := range codes {
		_ = obj.Set(fmt.Sprintf("%d", code), engine.Str(text))
	}
	return obj
}

// httpMethods 返回标准 HTTP 方法名数组（与 Node 的 http.METHODS 一致）。
func httpMethods() engine.Value {
	methods := []string{
		"ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD",
		"LINK", "LOCK", "M-SEARCH", "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL",
		"MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST", "PROPFIND", "PROPPATCH",
		"PURGE", "PUT", "REBIND", "REPORT", "SEARCH", "SOURCE", "SUBSCRIBE",
		"TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE",
	}
	vals := make([]engine.Value, len(methods))
	for i, m := range methods {
		vals[i] = engine.Str(m)
	}
	return engine.NewArray(vals)
}

// --- 客户端 --------------------------------------------------------------

// clientReqState 是 ClientRequest 的内部状态。
type clientReqState struct {
	ctx         engine.Context
	req         engine.Value // ClientRequest 对象
	method      string
	url         string
	headers     map[string]string
	body        strings.Builder
	callback    engine.Value // 响应回调
	ended       bool
	aborted     bool
	cancel      context.CancelFunc // 中止请求（req.abort）
	insecureTLS bool              // rejectUnauthorized: false（跳过自签名证书校验）
	agent       *http.Transport   // options.agent 的连接池（nil 时按 noAgent/全局）
	noAgent     bool              // agent: false → 每次请求新建连接
}

// newClientRequest 创建 ClientRequest 对象（HTTP）。
func newClientRequest(ctx engine.Context, args []engine.Value) engine.Value {
	return newClientRequestProto(ctx, args, "http")
}

// newClientRequestProto 创建 ClientRequest 对象。
// proto 为 URL 协议前缀（"http"/"https"）。https 模块复用本函数。
// options: 字符串 URL 或对象 {host, port, path, method, headers}。
func newClientRequestProto(ctx engine.Context, args []engine.Value, proto string) engine.Value {
	req := newEmitterInstance().(engine.Object)

	state := &clientReqState{
		ctx:     ctx,
		method:  "GET",
		headers: make(map[string]string),
	}
	state.req = req

	// 解析 options。
	var callback engine.Value
	if len(args) > 0 {
		opt := args[0]
		switch {
		case opt.Type() == engine.TypeString:
			state.url = opt.String()
		default:
			if o, ok := opt.AsObject(); ok {
				if m, err := o.Get("method"); err == nil && !m.IsUndefined() && !m.IsNull() && m.String() != "" {
					state.method = m.String()
				}
				if h, err := o.Get("host"); err == nil && !h.IsUndefined() && !h.IsNull() && h.String() != "" {
					state.url = proto + "://" + h.String()
				}
				if p, err := o.Get("port"); err == nil && !p.IsUndefined() && !p.IsNull() && p.String() != "" {
					state.url = strings.TrimSuffix(state.url, "/") + ":" + p.String()
				}
				if pa, err := o.Get("path"); err == nil && !pa.IsUndefined() && !pa.IsNull() && pa.String() != "" {
					state.url = strings.TrimSuffix(state.url, "/") + pa.String()
				}
				if hdrs, err := o.Get("headers"); err == nil && !hdrs.IsUndefined() && !hdrs.IsNull() {
					if hObj, ok := hdrs.AsObject(); ok {
						for _, k := range hObj.Keys() {
							if v, err := hObj.Get(k); err == nil {
								state.headers[k] = v.String()
							}
						}
					}
				}
				if r, err := o.Get("rejectUnauthorized"); err == nil {
					if b, ok := r.Bool(); ok && !b {
						state.insecureTLS = true // 跳过自签名证书校验
					}
				}
				// agent：Agent 实例 → 连接池；false → 不复用；缺省 → 全局。
				if a, err := o.Get("agent"); err == nil {
					tr, noReuse := resolveAgentTransport(a)
					state.agent = tr
					state.noAgent = noReuse
				}
			}
		}
		if len(args) > 1 && args[1].IsFunction() {
			callback = args[1]
		}
	}
	state.callback = callback

	// req.write(chunk)
	_ = req.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			state.body.WriteString(args[0].String())
		}
		return engine.Boolean(true), nil
	}))

	// req.end([chunk])：发送请求。
	_ = req.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsFunction() {
			state.body.WriteString(args[0].String())
		}
		state.send()
		return req, nil
	}))

	// req.setHeader(name, value)
	_ = req.Set("setHeader", engine.NewFunction("setHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			state.headers[args[0].String()] = args[1].String()
		}
		return req, nil
	}))

	// req.getHeader(name)
	_ = req.Set("getHeader", engine.NewFunction("getHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if v, ok := state.headers[args[0].String()]; ok {
				return engine.Str(v), nil
			}
		}
		return engine.Undefined(), nil
	}))

	// req.getHeaders()：返回当前请求头对象。
	_ = req.Set("getHeaders", engine.NewFunction("getHeaders", func(args []engine.Value) (engine.Value, error) {
		obj := engine.NewObject()
		for k, v := range state.headers {
			_ = obj.Set(k, engine.Str(v))
		}
		return obj, nil
	}))

	// req.hasHeader(name)
	_ = req.Set("hasHeader", engine.NewFunction("hasHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			_, ok := state.headers[args[0].String()]
			return engine.Boolean(ok), nil
		}
		return engine.Boolean(false), nil
	}))

	// req.removeHeader(name)
	_ = req.Set("removeHeader", engine.NewFunction("removeHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			delete(state.headers, args[0].String())
		}
		return req, nil
	}))

	// req.flushHeaders()：no-op（发送在 end 时统一进行）。
	_ = req.Set("flushHeaders", engine.NewFunction("flushHeaders", func(args []engine.Value) (engine.Value, error) {
		return req, nil
	}))

	// req.setTimeout(timeout[, callback])：定时触发 'timeout' 事件。
	_ = req.Set("setTimeout", engine.NewFunction("setTimeout", func(args []engine.Value) (engine.Value, error) {
		timeout := intArg(args, 0, 0)
		var cb engine.Value
		if len(args) > 1 && args[1].IsFunction() {
			cb = args[1]
		}
		if timeout > 0 {
			if st, err := ctx.Global().Get("setTimeout"); err == nil && st.IsFunction() {
				if sf, ok := st.AsFunction(); ok {
					timerCb := engine.NewFunction("timeout", func(callArgs []engine.Value) (engine.Value, error) {
						// Node 语义：setTimeout 的 callback 是第一个 'timeout' 监听器，
						// 先触发 callback，再触发后续 on('timeout') 监听器。
						if cb.IsFunction() {
							if f, ok := cb.AsFunction(); ok {
								_, _ = f.Call(nil)
							}
						}
						emitEvent(req, "timeout")
						return engine.Undefined(), nil
					})
					_, _ = sf.Call([]engine.Value{timerCb, engine.Number(float64(timeout))})
				}
			}
		}
		return req, nil
	}))

	// req.setNoDelay([noDelay]) / req.setSocketKeepAlive([enable][, initialDelay])：no-op。
	_ = req.Set("setNoDelay", engine.NewFunction("setNoDelay", func(args []engine.Value) (engine.Value, error) {
		return req, nil
	}))
	_ = req.Set("setSocketKeepAlive", engine.NewFunction("setSocketKeepAlive", func(args []engine.Value) (engine.Value, error) {
		return req, nil
	}))

	// req.abort()：中止请求，触发 'abort'；底层取消后 'error'(ECONNRESET)+'close'。
	_ = req.Set("abort", engine.NewFunction("abort", func(args []engine.Value) (engine.Value, error) {
		if !state.aborted {
			state.aborted = true
			if state.cancel != nil {
				state.cancel()
			}
			emitEvent(req, "abort")
		}
		return req, nil
	}))

	// req.destroy([error])：销毁请求（等同 abort + 'close'）。
	_ = req.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		if !state.aborted {
			state.aborted = true
			if state.cancel != nil {
				state.cancel()
			}
		}
		emitEvent(req, "abort")
		emitEvent(req, "close")
		return req, nil
	}))

	return req
}

// send 发送 HTTP 请求（在 JS 线程调用）。
func (s *clientReqState) send() {
	if s.ended {
		return
	}
	s.ended = true

	// 可取消的请求上下文（req.abort/destroy 时触发）。
	ctxCancel, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	// 在 Go goroutine 发请求（不阻塞 JS），完成后 PostTask 回调。
	go func() {
		var bodyReader io.Reader
		if s.body.Len() > 0 {
			// Node 语义：不显式设置 Content-Length 时用 chunked 编码。
			// io.NopCloser 包装使 Go 无法推断长度 → Transfer-Encoding: chunked。
			bodyReader = io.NopCloser(strings.NewReader(s.body.String()))
		}
		req, err := http.NewRequestWithContext(ctxCancel, s.method, s.url, bodyReader)
		if err != nil {
			s.ctx.PostTask(func() {
				if f, ok := s.callback.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{engine.Undefined(), engine.Str(err.Error())})
				}
			})
			return
		}
		for k, v := range s.headers {
			req.Header.Set(k, v)
		}
		client := &http.Client{}
		if s.agent != nil {
			// 显式 Agent（keepAlive 连接池）。
			client.Transport = s.agent
		} else if !s.noAgent {
			// 缺省：全局共享 Transport（连接复用，Node globalAgent 语义）。
			client.Transport = getHttpGlobalTransport()
		}
		if s.insecureTLS {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 自签名证书（本地开发）
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			s.ctx.PostTask(func() {
				// abort 取消：Node 语义依次触发 'error'(ECONNRESET) 与 'close'。
				if s.aborted {
					errObj := makeErrorValue(s.ctx, err)
					if eo, ok := errObj.AsObject(); ok {
						_ = eo.Set("code", engine.Str("ECONNRESET"))
						_ = eo.Set("message", engine.Str("socket hang up"))
					}
					emitEvent(s.req, "error", errObj)
					emitEvent(s.req, "close")
					return
				}
				if f, ok := s.callback.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{engine.Undefined(), engine.Str(err.Error())})
				}
			})
			return
		}
		body, _ := io.ReadAll(resp.Body)
		// 先归还连接（keep-alive 复用）再投递回调——否则回调（含下一个
		// 请求）可能在连接归还前执行，导致连接池为空而新建连接。
		_ = resp.Body.Close()

		// 构造响应对象并回调 JS。
		s.ctx.PostTask(func() {
			resMsg := newIncomingMessage(&http.Request{}, body).(engine.Object)
			// 覆盖 statusCode。
			_ = resMsg.Set("statusCode", engine.IntValue(resp.StatusCode))
			_ = resMsg.Set("statusMessage", engine.Str(resp.Status))
			_ = resMsg.Set("headers", headersToObj(resp.Header))
			// trailer 头（Go 在 body 读完时填充 resp.Trailer）。
			_ = resMsg.Set("trailers", headersToObj(resp.Trailer))
			if f, ok := s.callback.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{resMsg})
			}
			// 回调注册完监听器后发射响应体事件（'data'/'end'）。
			emitIncomingData(resMsg, body)
		})
	}()
}
