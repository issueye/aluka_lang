package builtin

// node:http2 内置模块——HTTP/2 协议。
// 基于 Go net/http（Go 1.26 内置 HTTP/2 支持：http.Server 自动协商 h2，
// http.Client via h2c/ALPN）。
// 提供：connect/createServer/constants/getPackedSettings/getUnpackedSettings
// /getDefaultSettings。客户端 Http2Session.request 返回 ClientHttp2Stream；
// 服务端 stream.respond/respondWithFD。

import (
	"crypto/tls"
	"fmt"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewHTTP2 构造 node:http2 模块导出对象。
func NewHTTP2(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// http2.constants：HTTP/2 帧类型、错误码、设置项、伪头常量。
	constants := engine.NewObject()
	// 伪头部名称（RFC 7540）。
	_ = constants.Set("HTTP2_HEADER_METHOD", engine.Str(":method"))
	_ = constants.Set("HTTP2_HEADER_PATH", engine.Str(":path"))
	_ = constants.Set("HTTP2_HEADER_SCHEME", engine.Str(":scheme"))
	_ = constants.Set("HTTP2_HEADER_AUTHORITY", engine.Str(":authority"))
	_ = constants.Set("HTTP2_HEADER_STATUS", engine.Str(":status"))
	_ = constants.Set("HTTP2_HEADER_PROTOCOL", engine.Str(":protocol"))
	// nghttp2 错误码（Node 命名：NGHTTP2_*）。
	_ = constants.Set("NGHTTP2_NO_ERROR", engine.IntValue(0x0))
	_ = constants.Set("NGHTTP2_PROTOCOL_ERROR", engine.IntValue(0x1))
	_ = constants.Set("NGHTTP2_INTERNAL_ERROR", engine.IntValue(0x2))
	_ = constants.Set("NGHTTP2_FLOW_CONTROL_ERROR", engine.IntValue(0x3))
	_ = constants.Set("NGHTTP2_SETTINGS_TIMEOUT", engine.IntValue(0x4))
	_ = constants.Set("NGHTTP2_STREAM_CLOSED", engine.IntValue(0x5))
	_ = constants.Set("NGHTTP2_FRAME_SIZE_ERROR", engine.IntValue(0x6))
	_ = constants.Set("NGHTTP2_REFUSED_STREAM", engine.IntValue(0x7))
	_ = constants.Set("NGHTTP2_CANCEL", engine.IntValue(0x8))
	_ = constants.Set("NGHTTP2_COMPRESSION_ERROR", engine.IntValue(0x9))
	_ = constants.Set("NGHTTP2_CONNECT_ERROR", engine.IntValue(0xa))
	_ = constants.Set("NGHTTP2_ENHANCE_YOUR_CALM", engine.IntValue(0xb))
	_ = constants.Set("NGHTTP2_INADEQUATE_SECURITY", engine.IntValue(0xc))
	_ = constants.Set("NGHTTP2_HTTP_1_1_REQUIRED", engine.IntValue(0xd))
	// 帧类型（Node 也导出，值对齐 RFC 7540）。
	_ = constants.Set("HTTP2_FRAME_HEADERS", engine.IntValue(0x1))
	_ = constants.Set("HTTP2_FRAME_SETTINGS", engine.IntValue(0x4))
	_ = constants.Set("HTTP2_FRAME_PING", engine.IntValue(0x6))
	_ = constants.Set("HTTP2_FRAME_GOAWAY", engine.IntValue(0x7))
	// 设置项名。
	_ = constants.Set("HTTP2_SETTINGS_HEADER_TABLE_SIZE", engine.IntValue(0x1))
	_ = constants.Set("HTTP2_SETTINGS_ENABLE_PUSH", engine.IntValue(0x2))
	_ = constants.Set("HTTP2_SETTINGS_MAX_CONCURRENT_STREAMS", engine.IntValue(0x3))
	_ = constants.Set("HTTP2_SETTINGS_INITIAL_WINDOW_SIZE", engine.IntValue(0x4))
	_ = constants.Set("HTTP2_SETTINGS_MAX_FRAME_SIZE", engine.IntValue(0x5))
	_ = constants.Set("HTTP2_SETTINGS_MAX_HEADER_LIST_SIZE", engine.IntValue(0x6))
	_ = constants.Set("HTTP2_SETTINGS_ENABLE_CONNECT_PROTOCOL", engine.IntValue(0x8))
	_ = constants.Set("NGHTTP2_ERR_NOMEM", engine.IntValue(-1))
	_ = m.Set("constants", constants)

	// http2.getDefaultSettings()：默认设置对象（Node 字段名与键序）。
	_ = m.Set("getDefaultSettings", engine.NewFunction("getDefaultSettings", func(args []engine.Value) (engine.Value, error) {
		s := engine.NewObject()
		_ = s.Set("headerTableSize", engine.IntValue(4096))
		_ = s.Set("enablePush", engine.Boolean(true))
		_ = s.Set("initialWindowSize", engine.IntValue(65535))
		_ = s.Set("maxFrameSize", engine.IntValue(16384))
		_ = s.Set("maxConcurrentStreams", engine.Number(4294967295))
		_ = s.Set("maxHeaderSize", engine.IntValue(65535))
		_ = s.Set("maxHeaderListSize", engine.IntValue(65535))
		_ = s.Set("enableConnectProtocol", engine.Boolean(false))
		return s, nil
	}))

	// http2.getPackedSettings() / getUnpackedSettings：返回空 Buffer / 空对象（占位）。
	_ = m.Set("getPackedSettings", engine.NewFunction("getPackedSettings", func(args []engine.Value) (engine.Value, error) {
		return globals.NewBufferInstance(make([]byte, 0)), nil
	}))
	_ = m.Set("getUnpackedSettings", engine.NewFunction("getUnpackedSettings", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))

	// http2.sensitiveHeaders：Symbol 标记敏感头（Node 导出为 Symbol）。
	_ = m.Set("sensitiveHeaders", engine.NewSymbol("sensitiveHeaders"))

	// http2.connect(authority[, options][, listener]) → ClientHttp2Session
	_ = m.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		authority := ""
		if len(args) > 0 {
			authority = args[0].String()
		}
		var listener engine.Value
		if len(args) > 1 {
			for _, a := range args[1:] {
				if a.IsFunction() {
					listener = a
				}
			}
		}
		return newHTTP2ClientSession(ctx, authority, listener), nil
	}))

	// http2.createServer([options][, onRequestHandler]) → Http2Server
	_ = m.Set("createServer", engine.NewFunction("createServer", func(args []engine.Value) (engine.Value, error) {
		var handler engine.Value
		for _, a := range args {
			if a.IsFunction() {
				handler = a
			}
		}
		return newHTTP2Server(ctx, handler), nil
	}))

	// http2.createSecureServer([options][, onRequestHandler])：TLS 版本。
	_ = m.Set("createSecureServer", engine.NewFunction("createSecureServer", func(args []engine.Value) (engine.Value, error) {
		var handler engine.Value
		var options engine.Value
		for _, a := range args {
			if a.IsFunction() {
				handler = a
			} else if a.IsObject() {
				options = a
			}
		}
		tlsCfg, err := tlsConfigFromOptions(options)
		if err != nil {
			// Node 允许无 key/cert（自签名缺省）——此处沿用 https 语义。
			return engine.Undefined(), err
		}
		return newHTTPServerWithTLS(ctx, handler, tlsCfg), nil
	}))

	// Node 不导出 Http2Server/Http2Session 等类（仅通过 createServer/connect
	// 实例获得），这里不注册多余表面。

	return m, nil
}

// http2ClientState HTTP/2 客户端会话状态。
type http2ClientState struct {
	ctx       engine.Context
	authority string
	client    *http.Client
	mu        sync.Mutex
	closed    bool
}

// newHTTP2ClientSession 构造 ClientHttp2Session。
func newHTTP2ClientSession(ctx engine.Context, authority string, listener engine.Value) engine.Value {
	sess := newEmitterInstance().(engine.Object)
	isHTTPS := strings.HasPrefix(authority, "https://")
	state := &http2ClientState{
		ctx:       ctx,
		authority: authority,
	}
	// 复用 Go net/http 内置的 HTTP/2 支持（http.Client 对 https:// 自动
	// 协商 h2；对 http:// 走 h2c 需特殊 Transport，此处简化仅支持 https）。
	if isHTTPS {
		state.client = &http.Client{Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{},
			ForceAttemptHTTP2: true,
		}}
	} else {
		state.client = &http.Client{Transport: &http.Transport{
			ForceAttemptHTTP2: true,
		}}
	}

	// session.request(headers[, options][, callback]) → ClientHttp2Stream
	_ = sess.Set("request", engine.NewFunction("request", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("request: headers required")
		}
		method := "GET"
		path := "/"
		var headers http.Header = http.Header{}
		if hdrs, ok := args[0].AsObject(); ok {
			for _, k := range hdrs.Keys() {
				v, _ := hdrs.Get(k)
				switch k {
				case ":method":
					method = v.String()
				case ":path":
					path = v.String()
				default:
					headers.Set(k, v.String())
				}
			}
		}
		var cb engine.Value
		for _, a := range args[1:] {
			if a.IsFunction() {
				cb = a
			}
		}

		stream := newEmitterInstance().(engine.Object)
		url := state.authority + path
		go func() {
			req, err := http.NewRequest(method, url, nil)
			if err != nil {
				ctx.PostTask(func() {
					emitEvent(stream, "error", engine.Str(err.Error()))
					if cb.IsFunction() {
						if f, ok := cb.AsFunction(); ok {
							if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				})
				return
			}
			req.Header = headers
			resp, err := state.client.Do(req)
			if err != nil {
				ctx.PostTask(func() {
					emitEvent(stream, "error", engine.Str(err.Error()))
				})
				return
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			ctx.PostTask(func() {
				_ = stream.Set("statusCode", engine.IntValue(resp.StatusCode))
				hdrObj := headersToObj(resp.Header)
				_ = stream.Set("headers", hdrObj)
				emitEvent(stream, "response", hdrObj)
				emitEvent(stream, "data", globals.NewBufferInstance(body))
				emitEvent(stream, "end")
				if cb.IsFunction() {
					if f, ok := cb.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{hdrObj}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			})
		}()
		return stream, nil
	}))

	// session.close([callback])
	_ = sess.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		if !state.closed {
			state.closed = true
		}
		state.mu.Unlock()
		emitEvent(sess, "close")
		if len(args) > 0 && args[0].IsFunction() {
			if f, ok := args[0].AsFunction(); ok {
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return sess, nil
	}))

	_ = sess.Set("ref", engine.NewFunction("ref", func(args []engine.Value) (engine.Value, error) {
		return sess, nil
	}))
	_ = sess.Set("unref", engine.NewFunction("unref", func(args []engine.Value) (engine.Value, error) {
		return sess, nil
	}))

	// connect 成功 → 触发 'connect' 事件（含 listener）。
	ctx.PostTask(func() {
		emitEvent(sess, "connect", sess, engine.Undefined())
		if listener.IsFunction() {
			if f, ok := listener.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{sess, engine.Undefined()}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
	})

	return sess
}

// newHTTP2Server 构造 HTTP/2 服务端（复用 node:http Server，Go 自动协商 h2）。
// 注意：明文 h2c 需特殊配置；此处简化为 HTTP/1.1 over TLS h2（Go http.Server
// 对 TLS 连接自动启用 HTTP/2 ALPN）。
func newHTTP2Server(ctx engine.Context, handler engine.Value) engine.Value {
	return newHTTPServerWithTLS(ctx, handler, nil)
}
