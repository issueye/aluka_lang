package nodenet

// node:dgram 内置模块——UDP 数据报。
// 基于 Go net 标准库（net.ListenUDP/net.ResolveUDPAddr）。
// 支持 createSocket('udp4'/'udp6')、bind/send/message/close/address。

import (
	"fmt"
	"net"
	"sync"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/builtin/nodeevents"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewDgram 构造 node:dgram 模块导出对象。
func NewDgram(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// dgram.createSocket(type[, callback]) → Socket
	_ = m.Set("createSocket", engine.NewFunction("createSocket", func(args []engine.Value) (engine.Value, error) {
		netType := "udp4"
		if len(args) > 0 && args[0].Type() == engine.TypeString {
			netType = args[0].String()
		}
		var messageCb engine.Value
		if len(args) > 1 && args[1].IsFunction() {
			messageCb = args[1]
		}
		return newDgramSocket(ctx, netType, messageCb), nil
	}))

	// Socket 类（构造器，供 instanceof 检测）。
	socketProto := engine.NewObject()
	_ = m.Set("Socket", engine.NewFunction("Socket", func(args []engine.Value) (engine.Value, error) {
		return newDgramSocket(ctx, "udp4", engine.Undefined()), nil
	}))

	_ = socketProto.Set("test", nil)
	_ = m.Set("_socketProto", socketProto)

	return m, nil
}

// dgramSocketState UDP 套接字内部状态。
type dgramSocketState struct {
	ctx           engine.Context
	netType       string
	conn          *net.UDPConn
	boundAddr     *net.UDPAddr
	connectedAddr *net.UDPAddr // socket.connect() 设置的默认目标
	mu            sync.Mutex
	bound         bool
	closed        bool
	release       func()
}

// newDgramSocket 构造 UDP Socket 对象（EventEmitter 风格）。
func newDgramSocket(ctx engine.Context, netType string, messageCb engine.Value) engine.Value {
	sock := nodeevents.NewEmitterInstance().(engine.Object)
	state := &dgramSocketState{ctx: ctx, netType: netType}

	// 默认 'message' 监听器（createSocket 回调）。
	if messageCb != nil && messageCb.IsFunction() {
		if onFn, err := sock.Get("on"); err == nil && onFn.IsFunction() {
			if f, ok := onFn.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.Str("message"), messageCb}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
	}

	// socket.bind([port][, address][, callback])
	_ = sock.Set("bind", engine.NewFunction("bind", func(args []engine.Value) (engine.Value, error) {
		port := 0
		address := ""
		var cb engine.Value
		for _, a := range args {
			if a.IsFunction() {
				cb = a
			} else if a.Type() == engine.TypeNumber {
				if n, ok := a.Int(); ok {
					port = n
				}
			} else if a.Type() == engine.TypeString {
				address = a.String()
			}
		}
		if address == "" {
			if netType == "udp6" {
				address = "::"
			} else {
				address = "0.0.0.0"
			}
		}

		addr, err := net.ResolveUDPAddr(netType, fmt.Sprintf("%s:%d", address, port))
		if err != nil {
			nodebase.EmitEvent(sock, "error", engine.Str(err.Error()))
			return sock, nil
		}
		conn, err := net.ListenUDP(netType, addr)
		if err != nil {
			nodebase.EmitEvent(sock, "error", engine.Str(err.Error()))
			return sock, nil
		}
		state.mu.Lock()
		state.conn = conn
		state.boundAddr = addr
		state.bound = true
		state.release = ctx.AddRef()
		state.mu.Unlock()

		// 启动接收 goroutine。
		go dgramRecvLoop(ctx, sock, state)

		// 触发 'listening' 事件。
		ctx.PostTask(func() {
			nodebase.EmitEvent(sock, "listening")
			if cb != nil && cb.IsFunction() {
				if f, ok := cb.AsFunction(); ok {
					if _, err := f.Call(nil); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		})
		return sock, nil
	}))

	// socket.send(msg[, offset, length][, port][, address][, callback])
	_ = sock.Set("send", engine.NewFunction("send", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return sock, fmt.Errorf("send: message required")
		}
		data, _ := nodebase.CryptoBytes(args[0])
		// 解析 port/address：从尾部找 number + string。
		var port int
		var address string
		var cb engine.Value
		for _, a := range args[1:] {
			if a.IsFunction() {
				cb = a
			} else if a.Type() == engine.TypeNumber {
				if n, ok := a.Int(); ok {
					port = n
				}
			} else if a.Type() == engine.TypeString {
				address = a.String()
			}
		}

		state.mu.Lock()
		conn := state.conn
		boundAddr := state.boundAddr
		connectedAddr := state.connectedAddr
		state.mu.Unlock()

		// 隐式 bind：Node 语义允许未 bind 的 socket 直接 send（自动绑定
		// 临时端口）。
		if conn == nil {
			bindIP := "0.0.0.0"
			if state.netType == "udp6" {
				bindIP = "::"
			}
			implicitAddr, rerr := net.ResolveUDPAddr(state.netType, fmt.Sprintf("%s:0", bindIP))
			if rerr != nil {
				implicitAddr = nil
			}
			implicit, lerr := net.ListenUDP(state.netType, implicitAddr)
			if lerr != nil {
				return sock, lerr
			}
			state.mu.Lock()
			state.conn = implicit
			state.boundAddr = implicitAddr
			state.bound = true
			if state.release == nil {
				state.release = ctx.AddRef()
			}
			state.mu.Unlock()
			go dgramRecvLoop(ctx, sock, state)
			conn = implicit
		}

		var dest *net.UDPAddr
		var err error
		if address != "" && port > 0 {
			dest, err = net.ResolveUDPAddr(state.netType, fmt.Sprintf("%s:%d", address, port))
		} else if port > 0 {
			// 缺省地址：用 bound 的 IP。
			ip := "127.0.0.1"
			if boundAddr != nil && boundAddr.IP != nil {
				ip = boundAddr.IP.String()
			}
			dest, err = net.ResolveUDPAddr(state.netType, fmt.Sprintf("%s:%d", ip, port))
		} else if connectedAddr != nil {
			// connected socket：send(msg) 发往 connect 设置的目标。
			dest = connectedAddr
		} else if conn == nil {
			err = fmt.Errorf("socket not bound")
		}
		if err != nil {
			if cb != nil && cb.IsFunction() {
				if f, ok := cb.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			return sock, nil
		}
		_, err = conn.WriteToUDP(data, dest)
		if cb != nil && cb.IsFunction() {
			if f, ok := cb.AsFunction(); ok {
				if err != nil {
					if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				} else {
					if _, err := f.Call([]engine.Value{engine.Null()}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		}
		return sock, nil
	}))

	// socket.close([callback])
	_ = sock.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		conn := state.conn
		release := state.release
		alreadyClosed := state.closed
		state.closed = true
		state.mu.Unlock()
		if !alreadyClosed {
			if conn != nil {
				_ = conn.Close()
			}
			if release != nil {
				release()
			}
			if len(args) > 0 && args[0].IsFunction() {
				if f, ok := args[0].AsFunction(); ok {
					if _, err := f.Call(nil); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			nodebase.EmitEvent(sock, "close")
		}
		return sock, nil
	}))

	// socket.address()：返回 {address, family, port}。
	_ = sock.Set("address", engine.NewFunction("address", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		conn := state.conn
		state.mu.Unlock()
		o := engine.NewObject()
		if conn == nil {
			return o, fmt.Errorf("not bound")
		}
		laddr := conn.LocalAddr()
		if ua, ok := laddr.(*net.UDPAddr); ok {
			_ = o.Set("address", engine.Str(ua.IP.String()))
			_ = o.Set("port", engine.IntValue(ua.Port))
			fam := "IPv4"
			if ua.IP.To4() == nil {
				fam = "IPv6"
			}
			_ = o.Set("family", engine.Str(fam))
		}
		return o, nil
	}))

	// socket.setBroadcast(flag)（简化：无操作，返回自身）。
	_ = sock.Set("setBroadcast", engine.NewFunction("setBroadcast", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	_ = sock.Set("addMembership", engine.NewFunction("addMembership", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	_ = sock.Set("dropMembership", engine.NewFunction("dropMembership", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	_ = sock.Set("setTTL", engine.NewFunction("setTTL", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	_ = sock.Set("ref", engine.NewFunction("ref", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	_ = sock.Set("unref", engine.NewFunction("unref", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	// setMulticastLoopback/setMulticastTTL：no-op。
	_ = sock.Set("setMulticastLoopback", engine.NewFunction("setMulticastLoopback", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))
	_ = sock.Set("setMulticastTTL", engine.NewFunction("setMulticastTTL", func(args []engine.Value) (engine.Value, error) {
		return sock, nil
	}))

	// socket.connect(port[, address][, cb])：设置默认发送目标（connected socket）。
	_ = sock.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		var port int
		var address string
		var cb engine.Value
		for _, a := range args {
			if a.IsFunction() {
				cb = a
			} else if a.Type() == engine.TypeNumber {
				if n, ok := a.Int(); ok {
					port = n
				}
			} else if a.Type() == engine.TypeString {
				address = a.String()
			}
		}
		dest, err := net.ResolveUDPAddr(state.netType, fmt.Sprintf("%s:%d", address, port))
		if err != nil {
			if cb.IsFunction() {
				if f, ok := cb.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{nodebase.MakeErrorValue(ctx, err)}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			return sock, nil
		}
		state.mu.Lock()
		state.connectedAddr = dest
		state.mu.Unlock()
		if cb.IsFunction() {
			if f, ok := cb.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.Null()}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return sock, nil
	}))

	// socket.disconnect()：清除默认发送目标。
	_ = sock.Set("disconnect", engine.NewFunction("disconnect", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		state.connectedAddr = nil
		state.mu.Unlock()
		return sock, nil
	}))

	return sock
}

// dgramRecvLoop UDP 接收循环（goroutine）。
func dgramRecvLoop(ctx engine.Context, sock engine.Object, state *dgramSocketState) {
	buf := make([]byte, 65536)
	for {
		state.mu.Lock()
		conn := state.conn
		closed := state.closed
		state.mu.Unlock()
		if closed || conn == nil {
			return
		}
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			state.mu.Lock()
			if !state.closed {
				state.closed = true
				if state.release != nil {
					state.release()
				}
				ctx.PostTask(func() {
					nodebase.EmitEvent(sock, "close")
				})
			}
			state.mu.Unlock()
			return
		}
		// 复制有效数据，投递 'message' 事件。
		data := make([]byte, n)
		copy(data, buf[:n])
		msg := gbuffer.NewBufferInstance(data)
		rinfo := engine.NewObject()
		_ = rinfo.Set("address", engine.Str(raddr.IP.String()))
		_ = rinfo.Set("port", engine.IntValue(raddr.Port))
		fam := "IPv4"
		if raddr.IP.To4() == nil {
			fam = "IPv6"
		}
		_ = rinfo.Set("family", engine.Str(fam))
		_ = rinfo.Set("size", engine.IntValue(n))
		ctx.PostTask(func() {
			nodebase.EmitEvent(sock, "message", msg, rinfo)
		})
	}
}
