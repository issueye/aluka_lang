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
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewHTTP 构造 node:http 模块的导出对象。
func NewHTTP(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

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
	_ = m.Set("ServerResponse", newServerResponseCtor())

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
	state := &httpServerState{ctx: ctx, handler: handler, tlsConfig: tlsConfig}

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
		state.mu.Lock()
		state.httpSrv = srv
		state.listening = true
		state.mu.Unlock()

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
	if s.handler == nil || !s.handler.IsFunction() {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("no handler"))
		return
	}
	// 读取请求体（同步，阻塞 net/http goroutine 但可接受）。
	body, _ := io.ReadAll(r.Body)

	// 在 JS 线程构造并调用 handler，完成后才返回（响应已写入）。
	done := make(chan struct{})
	s.ctx.PostTask(func() {
		req := newIncomingMessage(r, body)
		res := newServerResponse(w)
		if f, ok := s.handler.AsFunction(); ok {
			_, _ = f.Call([]engine.Value{req, res})
		}
		// 异步 handler（Promise/async 链式 res.end）：同步返回后响应尚未
		// 完成，需在 JS 线程持续排空微任务，直到 res.end 被调用（finished）。
		// 否则 Go net/http 会在本次返回后发送空响应，丢失异步写入的 body。
		for i := 0; i < 1000000 && !resFinished(res); i++ {
			if !s.ctx.FlushMicrotasks() {
				break // 无待执行微任务：handler 依赖定时器/IO 等，超出本层处理
			}
		}
		// handler 注册完监听器后发射请求体事件（'data'/'end'）。
		emitIncomingData(req, body)
		close(done)
	})
	<-done
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
func newServerResponseCtor() engine.Value {
	ctor := engine.NewFunction("ServerResponse", func(args []engine.Value) (engine.Value, error) {
		return newServerResponse(nil), nil
	})
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	if co, ok := ctor.AsObject(); ok {
		_ = co.Set("prototype", proto)
	}
	return ctor
}

// newServerResponse 构造 ServerResponse 对象（响应）。
func newServerResponse(w http.ResponseWriter) engine.Value {
	res := newEmitterInstance().(engine.Object)

	state := &respState{
		w:        w,
		status:   200,
		headers:  make(map[string]string),
		finished: false,
	}

	// res.statusCode
	_ = res.Set("statusCode", engine.IntValue(200))

	// res.writeHead(statusCode[, headers])：立即写入状态码与 headers。
	_ = res.Set("writeHead", engine.NewFunction("writeHead", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			state.status = intArg(args, 0, 200)
			_ = res.Set("statusCode", engine.IntValue(state.status))
		}
		if len(args) > 1 {
			if h, ok := args[1].AsObject(); ok {
				state.applyHeaders(h)
			}
		}
		state.flushHeadersOnce() // 立即提交 headers
		return res, nil
	}))

	// res.write(chunk)：首次 write 前 flush headers。
	_ = res.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && !state.finished {
			state.flushHeadersOnce()
			_, _ = state.w.Write([]byte(args[0].String()))
		}
		return engine.Boolean(true), nil
	}))

	// res.end([chunk])
	_ = res.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		if state.finished {
			return res, nil
		}
		// 先 flush headers（含状态码），再写 body，避免重复 WriteHeader。
		state.flushHeadersOnce()
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() && !args[0].IsFunction() {
			_, _ = state.w.Write([]byte(args[0].String()))
		}
		state.finished = true
		_ = res.Set(resFinishedKey, engine.Boolean(true))
		emitEvent(res, "finish")
		emitEvent(res, "close")
		return res, nil
	}))

	// res.setHeader(name, value)
	_ = res.Set("setHeader", engine.NewFunction("setHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			state.headers[args[0].String()] = args[1].String()
		}
		return res, nil
	}))

	// res.getHeader(name)
	_ = res.Set("getHeader", engine.NewFunction("getHeader", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if v, ok := state.headers[args[0].String()]; ok {
				return engine.Str(v), nil
			}
		}
		return engine.Undefined(), nil
	}))

	// res.statusCode 设置器（setter 简化：直接写）
	_ = res.Set("setStatusCode", engine.NewFunction("setStatusCode", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			state.status = intArg(args, 0, 200)
			_ = res.Set("statusCode", engine.IntValue(state.status))
		}
		return res, nil
	}))

	return res
}

// respState 是 ServerResponse 的内部状态。
type respState struct {
	w              http.ResponseWriter
	status         int
	headers        map[string]string
	finished       bool
	headersWritten bool
}

// flushHeadersOnce 只写入一次 headers/状态码（避免 Go 重复 WriteHeader 警告）。
func (rs *respState) flushHeadersOnce() {
	if rs.headersWritten {
		return
	}
	rs.headersWritten = true
	hasHeaders := len(rs.headers) > 0
	for k, v := range rs.headers {
		rs.w.Header().Set(k, v)
	}
	if hasHeaders || rs.status != 200 {
		rs.w.WriteHeader(rs.status)
	}
}

// applyHeaders 应用 header 对象到响应。
func (rs *respState) applyHeaders(h engine.Object) {
	for _, k := range h.Keys() {
		if v, err := h.Get(k); err == nil {
			rs.headers[k] = v.String()
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
	method      string
	url         string
	headers     map[string]string
	body        strings.Builder
	callback    engine.Value // 响应回调
	ended       bool
	insecureTLS bool // rejectUnauthorized: false（跳过自签名证书校验）
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

	return req
}

// send 发送 HTTP 请求（在 JS 线程调用）。
func (s *clientReqState) send() {
	if s.ended {
		return
	}
	s.ended = true

	// 在 Go goroutine 发请求（不阻塞 JS），完成后 PostTask 回调。
	go func() {
		var bodyReader io.Reader
		if s.body.Len() > 0 {
			bodyReader = strings.NewReader(s.body.String())
		}
		req, err := http.NewRequest(s.method, s.url, bodyReader)
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
		if s.insecureTLS {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 自签名证书（本地开发）
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			s.ctx.PostTask(func() {
				if f, ok := s.callback.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{engine.Undefined(), engine.Str(err.Error())})
				}
			})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		// 构造响应对象并回调 JS。
		s.ctx.PostTask(func() {
			resMsg := newIncomingMessage(&http.Request{}, body).(engine.Object)
			// 覆盖 statusCode。
			_ = resMsg.Set("statusCode", engine.IntValue(resp.StatusCode))
			_ = resMsg.Set("statusMessage", engine.Str(resp.Status))
			_ = resMsg.Set("headers", headersToObj(resp.Header))
			if f, ok := s.callback.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{resMsg})
			}
			// 回调注册完监听器后发射响应体事件（'data'/'end'）。
			emitIncomingData(resMsg, body)
		})
	}()
}
