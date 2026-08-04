package builtin

// node:net 内置模块——TCP 服务器与客户端。
//
// 架构（复用事件循环）：
//   - Server：net.Listen + 独立 goroutine accept 循环；每个连接构造 JS Socket
//     （EventEmitter），经 ctx.PostTask 触发 server 的 'connection' 事件。
//   - Socket：写操作同步 conn.Write（JS 线程），读在 goroutine 经 PostTask
//     触发 'data'/'end'/'close'。
//   - 活跃度：服务器与每个 socket 创建时 AddRef，close/destroy 时释放，
//     保证事件循环在服务器/连接存活期间不退出。

import (
	"fmt"
	"net"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewNet 构造 node:net 模块的导出对象。
func NewNet(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// net.createServer([options][, connectionListener])
	_ = m.Set("createServer", engine.NewFunction("createServer", func(args []engine.Value) (engine.Value, error) {
		var listener engine.Value
		for _, a := range args {
			if a.IsFunction() {
				listener = a
			}
		}
		return newNetServer(ctx, listener), nil
	}))

	// net.connect(options[, connectListener]) / net.createConnection
	connectFn := engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		return newNetSocketClient(ctx, args), nil
	})
	_ = m.Set("connect", connectFn)
	_ = m.Set("createConnection", connectFn)

	return m, nil
}

// --- 服务器 --------------------------------------------------------------

// netServerState 是 TCP 服务器的内部状态。
type netServerState struct {
	ctx           engine.Context
	listener      net.Listener
	mu            sync.Mutex
	releaseHandle func()
	closed        bool
}

// newNetServer 创建 TCP Server 对象（基于 EventEmitter）。
func newNetServer(ctx engine.Context, listener engine.Value) engine.Value {
	server := newEmitterInstance().(engine.Object)
	state := &netServerState{ctx: ctx}

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

		// 服务器计入事件循环活跃度。
		releaseHandle := ctx.AddRef()
		state.mu.Lock()
		state.releaseHandle = releaseHandle
		state.mu.Unlock()

		go func() {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				ctx.PostTask(func() { emitEvent(server, "error", engine.Str(err.Error())) })
				state.releaseLocked()
				return
			}
			state.mu.Lock()
			state.listener = ln
			state.mu.Unlock()
			ctx.PostTask(func() {
				if callback != nil {
					if f, ok := callback.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				}
				emitEvent(server, "listening")
			})
			// accept 循环（阻塞直到关闭）。
			for {
				conn, err := ln.Accept()
				if err != nil {
					break
				}
				go handleNetConn(ctx, conn, server, listener)
			}
			state.releaseLocked()
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
			// 在 goroutine 关闭监听器；活跃度延迟到 close 回调执行后释放
			// （否则事件循环可能在回调投递前退出）。
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

	// server.address() → {address, family, port}
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

// releaseLocked 释放服务器活跃度句柄（在 accept 退出或监听失败时）。
func (s *netServerState) releaseLocked() {
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

// handleNetConn 处理一个 TCP 连接（accept goroutine 调用）。
func handleNetConn(ctx engine.Context, conn net.Conn, server engine.Value, listener engine.Value) {
	socket, _ := newNetSocket(ctx, conn)
	ctx.PostTask(func() {
		emitEvent(server, "connection", socket)
		if listener != nil && listener.IsFunction() {
			if f, ok := listener.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{socket})
			}
		}
	})
}

// --- Socket ---------------------------------------------------------------

// netSocketState 是 Socket 实例的内部状态。
type netSocketState struct {
	ctx           engine.Context
	conn          net.Conn
	mu            sync.Mutex
	closed        bool
	releaseHandle func()
}

// newNetSocket 创建 Socket 对象（绑定连接；conn 可为 nil 待客户端延迟设置）。
// 返回 (socket, state)，state 供客户端在 Dial 成功后填充连接。
func newNetSocket(ctx engine.Context, conn net.Conn) (engine.Value, *netSocketState) {
	socket := newEmitterInstance().(engine.Object)
	state := &netSocketState{ctx: ctx, conn: conn}
	// socket 计入活跃度（close 时释放）。
	state.releaseHandle = ctx.AddRef()

	// 地址属性（conn 就绪时填充）。
	setAddrProps(socket, conn)

	// socket.write(data[, encoding][, callback])
	_ = socket.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		state.mu.Lock()
		conn := state.conn
		closed := state.closed
		state.mu.Unlock()
		if closed || conn == nil {
			return engine.Boolean(false), nil
		}
		_, err := conn.Write([]byte(args[0].String()))
		if len(args) > 2 && args[2].IsFunction() {
			if f, ok := args[2].AsFunction(); ok {
				_, _ = f.Call(nil)
			}
		}
		return engine.Boolean(err == nil), err
	}))

	// socket.end([data])
	_ = socket.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		conn := state.conn
		closed := state.closed
		state.mu.Unlock()
		if closed || conn == nil {
			return socket, nil
		}
		if len(args) > 0 && !args[0].IsUndefined() {
			_, _ = conn.Write([]byte(args[0].String()))
		}
		state.close()
		return socket, nil
	}))

	// socket.destroy()
	_ = socket.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		state.close()
		return socket, nil
	}))

	// socket.setEncoding() / setNoDelay()：no-op（数据直接以字符串传递）。
	_ = socket.Set("setEncoding", engine.NewFunction("setEncoding", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))
	_ = socket.Set("setNoDelay", engine.NewFunction("setNoDelay", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))

	// socket.address()
	_ = socket.Set("address", engine.NewFunction("address", func(args []engine.Value) (engine.Value, error) {
		addr := engine.NewObject()
		if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
			_ = addr.Set("address", engine.Str(tcpAddr.IP.String()))
			_ = addr.Set("port", engine.IntValue(tcpAddr.Port))
		}
		return addr, nil
	}))

	// 连接就绪时启动读循环。
	if conn != nil {
		go startNetReader(ctx, socket, state, conn)
	}
	return socket, state
}

// setAddrProps 填充 socket 的地址属性。
func setAddrProps(socket engine.Object, conn net.Conn) {
	if conn == nil {
		return
	}
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		_ = socket.Set("remoteAddress", engine.Str(tcpAddr.IP.String()))
		_ = socket.Set("remotePort", engine.IntValue(tcpAddr.Port))
		_ = socket.Set("remoteFamily", engine.Str("IPv4"))
	}
	if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		_ = socket.Set("localAddress", engine.Str(tcpAddr.IP.String()))
		_ = socket.Set("localPort", engine.IntValue(tcpAddr.Port))
	}
}

// startNetReader 启动读循环：conn.Read → PostTask 触发 'data'；EOF 时
// 'end'/'close' 并释放活跃度。
func startNetReader(ctx engine.Context, socket engine.Value, state *netSocketState, conn net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			data := string(buf[:n])
			ctx.PostTask(func() {
				emitEvent(socket, "data", engine.Str(data))
			})
		}
		if err != nil {
			break
		}
	}
	state.close()
	ctx.PostTask(func() {
		emitEvent(socket, "end")
		emitEvent(socket, "close")
	})
}

// close 关闭连接并释放活跃度（幂等）。
func (s *netSocketState) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conn := s.conn
	release := s.releaseHandle
	s.releaseHandle = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if release != nil {
		release()
	}
}

// --- 客户端 --------------------------------------------------------------

// newNetSocketClient 创建客户端 Socket 并异步连接。
func newNetSocketClient(ctx engine.Context, args []engine.Value) engine.Value {
	host := "127.0.0.1"
	port := 0
	if len(args) > 0 {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("host"); err == nil && !v.IsUndefined() && !v.IsNull() && v.String() != "" {
				host = v.String()
			}
			if v, err := o.Get("port"); err == nil {
				if n, ok := v.Int(); ok {
					port = n
				}
			}
		}
	}
	var connectListener engine.Value
	if len(args) > 1 && args[1].IsFunction() {
		connectListener = args[1]
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	socket, state := newNetSocket(ctx, nil) // conn 延迟到 Dial 成功后设置

	go func() {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			ctx.PostTask(func() {
				emitEvent(socket, "error", engine.Str(err.Error()))
				state.close() // 释放活跃度
			})
			return
		}
		// 填充连接并启动读循环。
		state.mu.Lock()
		state.conn = conn
		state.mu.Unlock()
		ctx.PostTask(func() {
			setAddrProps(socket.(engine.Object), conn)
			if connectListener != nil {
				if f, ok := connectListener.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{socket})
				}
			}
			emitEvent(socket, "connect")
		})
		go startNetReader(ctx, socket, state, conn)
	}()
	return socket
}
