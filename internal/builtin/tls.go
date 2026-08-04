package builtin

// node:tls 内置模块——TLS 服务器与客户端（开发计划 2.24）。
//
// 实现要点：
//   - TLS 连接（tls.Conn）实现 net.Conn 接口，因此 socket 复用 node:net 的
//     newNetSocket（写/读/事件循环全套）。
//   - tls.createServer：net.Listen + tls.NewListener + accept 循环（复用
//     handleNetConn 分发连接）。
//   - tls.connect：net.Dial 后 tls.Client + Handshake，成功后触发 'connect'
//     与回调。
//   - 证书校验：rejectUnauthorized:false 时跳过（自签名/本地开发）。

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTLS 构造 node:tls 模块的导出对象。
func NewTLS(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// tls.createServer([options], [secureConnectionListener])
	_ = m.Set("createServer", engine.NewFunction("createServer", func(args []engine.Value) (engine.Value, error) {
		var listener engine.Value
		var options engine.Value
		for _, a := range args {
			if a.IsFunction() {
				listener = a
			} else if a.IsObject() {
				options = a
			}
		}
		tlsCfg, err := tlsConfigFromOptions(options)
		if err != nil {
			return engine.Undefined(), err
		}
		return newTLSServer(ctx, listener, tlsCfg), nil
	}))

	// tls.connect(options[, connectListener])
	_ = m.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		return tlsConnect(ctx, args), nil
	}))

	_ = m.Set("TLSSocket", engine.NewFunction("TLSSocket", func(args []engine.Value) (engine.Value, error) {
		// 简化：构造一个未连接的 socket 占位。
		socket, _ := newNetSocket(ctx, nil)
		return socket, nil
	}))

	return m, nil
}

// tlsConfigFromOptions 从 JS options 对象构造 tls.Config。
func tlsConfigFromOptions(options engine.Value) (*tls.Config, error) {
	if options == nil || options.IsUndefined() {
		return nil, fmt.Errorf("tls: createServer requires { key, cert } options")
	}
	keyPEM, certPEM := "", ""
	if o, ok := options.AsObject(); ok {
		if v, err := o.Get("key"); err == nil && !v.IsUndefined() {
			keyPEM = v.String()
		}
		if v, err := o.Get("cert"); err == nil && !v.IsUndefined() {
			certPEM = v.String()
		}
	}
	if keyPEM == "" || certPEM == "" {
		return nil, fmt.Errorf("tls: createServer requires { key, cert } PEM options")
	}
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("tls: invalid key/cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	}, nil
}

// --- TLS 服务器 -----------------------------------------------------------

// tlsServerState 是 TLS 服务器的内部状态。
type tlsServerState struct {
	ctx           engine.Context
	listener      net.Listener
	mu            sync.Mutex
	releaseHandle func()
	closed        bool
}

// newTLSServer 创建 TLS Server 对象（复用 handleNetConn 分发连接）。
func newTLSServer(ctx engine.Context, listener engine.Value, tlsCfg *tls.Config) engine.Value {
	server := newEmitterInstance().(engine.Object)
	state := &tlsServerState{ctx: ctx}

	// server.listen(port[, host][, callback])
	_ = server.Set("listen", engine.NewFunction("listen", func(args []engine.Value) (engine.Value, error) {
		port := 0
		host := ""
		var callback engine.Value
		if len(args) > 0 {
			port = intArg(args, 0, 0)
		}
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
		addr := fmt.Sprintf("%s:%d", host, port)

		releaseHandle := ctx.AddRef()
		state.mu.Lock()
		state.releaseHandle = releaseHandle
		state.mu.Unlock()

		go func() {
			raw, err := net.Listen("tcp", addr)
			if err != nil {
				ctx.PostTask(func() { emitEvent(server, "error", engine.Str(err.Error())) })
				state.release()
				return
			}
			ln := tls.NewListener(raw, tlsCfg)
			state.mu.Lock()
			state.listener = ln
			state.mu.Unlock()
			ctx.PostTask(func() {
				if callback != nil {
					if f, ok := callback.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				}
				emitEvent(server, "secureConnection")
				emitEvent(server, "listening")
			})
			for {
				conn, err := ln.Accept()
				if err != nil {
					break
				}
				go handleNetConn(ctx, conn, server, listener)
			}
			state.release()
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
		ln := state.listener
		var release func()
		if !state.closed {
			state.closed = true
			release = state.releaseHandle
			state.releaseHandle = nil
		}
		state.mu.Unlock()
		if ln != nil {
			// 在 goroutine 关闭监听器；活跃度延迟到 close 回调执行后释放。
			go func() {
				_ = ln.Close()
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

	// server.address()
	_ = server.Set("address", engine.NewFunction("address", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.listener == nil {
			return engine.Null(), nil
		}
		addr := engine.NewObject()
		if tcpAddr, ok := state.listener.Addr().(*net.TCPAddr); ok {
			_ = addr.Set("address", engine.Str(tcpAddr.IP.String()))
			_ = addr.Set("port", engine.IntValue(tcpAddr.Port))
			_ = addr.Set("family", engine.Str("IPv4"))
		}
		return addr, nil
	}))

	return server
}

// release 释放服务器活跃度（幂等）。
func (s *tlsServerState) release() {
	s.mu.Lock()
	if !s.closed && s.releaseHandle != nil {
		s.closed = true
		r := s.releaseHandle
		s.releaseHandle = nil
		s.mu.Unlock()
		r()
		return
	}
	s.mu.Unlock()
}

// --- TLS 客户端 -----------------------------------------------------------

// tlsConnect 创建 TLS 客户端 socket 并异步握手。
func tlsConnect(ctx engine.Context, args []engine.Value) engine.Value {
	host := "127.0.0.1"
	port := 0
	servername := host
	rejectUnauthorized := true
	if len(args) > 0 {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("host"); err == nil && !v.IsUndefined() && !v.IsNull() && v.String() != "" {
				host = v.String()
			}
			if v, err := o.Get("servername"); err == nil && !v.IsUndefined() && v.String() != "" {
				servername = v.String()
			}
			if v, err := o.Get("port"); err == nil {
				if n, ok := v.Int(); ok {
					port = n
				}
			}
			if v, err := o.Get("rejectUnauthorized"); err == nil {
				if b, ok := v.Bool(); ok {
					rejectUnauthorized = b
				}
			}
		}
	}
	var connectListener engine.Value
	if len(args) > 1 && args[1].IsFunction() {
		connectListener = args[1]
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	socket, state := newNetSocket(ctx, nil)
	go func() {
		raw, err := net.Dial("tcp", addr)
		if err != nil {
			ctx.PostTask(func() {
				emitEvent(socket, "error", engine.Str(err.Error()))
				state.close()
			})
			return
		}
		tconn := tls.Client(raw, &tls.Config{
			ServerName:         servername,
			InsecureSkipVerify: !rejectUnauthorized,
			NextProtos:         []string{"http/1.1"},
		})
		if err := tconn.Handshake(); err != nil {
			_ = raw.Close()
			ctx.PostTask(func() {
				emitEvent(socket, "error", engine.Str("tls: handshake failed: "+err.Error()))
				state.close()
			})
			return
		}
		state.mu.Lock()
		state.conn = tconn
		state.mu.Unlock()
		ctx.PostTask(func() {
			setAddrProps(socket.(engine.Object), tconn)
			if connectListener != nil && connectListener.IsFunction() {
				if f, ok := connectListener.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{socket})
				}
			}
			emitEvent(socket, "connect")
			emitEvent(socket, "secureConnect")
		})
		go startNetReader(ctx, socket, state, tconn)
	}()
	return socket
}
