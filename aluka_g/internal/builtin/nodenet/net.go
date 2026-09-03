package nodenet

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
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/builtin/nodeevents"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"

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

	// net.isIP(input)：IPv4 → 4，IPv6 → 6，否则 0。
	_ = m.Set("isIP", engine.NewFunction("isIP", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(0), nil
		}
		ip := net.ParseIP(args[0].String())
		if ip == nil {
			return engine.IntValue(0), nil
		}
		if ip.To4() != nil {
			return engine.IntValue(4), nil
		}
		return engine.IntValue(6), nil
	}))

	// net.isIPv4 / net.isIPv6。
	_ = m.Set("isIPv4", engine.NewFunction("isIPv4", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		ip := net.ParseIP(args[0].String())
		return engine.Boolean(ip != nil && ip.To4() != nil), nil
	}))
	_ = m.Set("isIPv6", engine.NewFunction("isIPv6", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		ip := net.ParseIP(args[0].String())
		return engine.Boolean(ip != nil && ip.To4() == nil), nil
	}))

	// net.BlockList：IP 黑名单（addAddress/addSubnet/check）。
	registerBlockList(m)

	// net.SocketAddress：地址描述对象。
	registerSocketAddress(m)

	return m, nil
}

// --- BlockList -------------------------------------------------------------

// blockListState 保存 BlockList 的规则。
type blockListState struct {
	mu     sync.Mutex
	ipSet  map[string]bool // 精确 IP
	subnet []blockSubnet   // 子网（cidr）
	ranges []blockRange    // 地址区间
}

type blockSubnet struct {
	ipNet *net.IPNet
}

type blockRange struct {
	from net.IP
	to   net.IP
}

func newBlockListInstance() engine.Value {
	bl := engine.NewObject()
	state := &blockListState{ipSet: make(map[string]bool)}

	_ = bl.Set("addAddress", engine.NewFunction("addAddress", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			state.mu.Lock()
			state.ipSet[args[0].String()] = true
			state.mu.Unlock()
		}
		return bl, nil
	}))
	_ = bl.Set("addSubnet", engine.NewFunction("addSubnet", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			prefix, ok := args[1].Int()
			if ok {
				_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", args[0].String(), prefix))
				if err == nil {
					state.mu.Lock()
					state.subnet = append(state.subnet, blockSubnet{ipNet: ipNet})
					state.mu.Unlock()
				}
			}
		}
		return bl, nil
	}))
	_ = bl.Set("addRange", engine.NewFunction("addRange", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			from := net.ParseIP(args[0].String())
			to := net.ParseIP(args[1].String())
			if from != nil && to != nil {
				state.mu.Lock()
				state.ranges = append(state.ranges, blockRange{from: from, to: to})
				state.mu.Unlock()
			}
		}
		return bl, nil
	}))
	_ = bl.Set("check", engine.NewFunction("check", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		ip := net.ParseIP(args[0].String())
		if ip == nil {
			return engine.Boolean(false), nil
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.ipSet[ip.String()] {
			return engine.Boolean(true), nil
		}
		for _, s := range state.subnet {
			if s.ipNet.Contains(ip) {
				return engine.Boolean(true), nil
			}
		}
		for _, r := range state.ranges {
			if ipRangeContains(r.from, r.to, ip) {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))

	// rules getter：Node 格式字符串数组（逆序：Range/Subnet/Address）。
	rulesGet := engine.NewFunction("rulesGet", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		var rules []string
		for _, r := range state.ranges {
			rules = append(rules, fmt.Sprintf("Range: IPv4 %s-%s", r.from.String(), r.to.String()))
		}
		for _, s := range state.subnet {
			rules = append(rules, fmt.Sprintf("Subnet: IPv4 %s", s.ipNet.String()))
		}
		for addr := range state.ipSet {
			rules = append(rules, fmt.Sprintf("Address: IPv4 %s", addr))
		}
		// Node 返回顺序：Range, Subnet, Address。
		vals := make([]engine.Value, 0, len(rules))
		for _, r := range rules {
			vals = append(vals, engine.Str(r))
		}
		return engine.NewArray(vals), nil
	})
	engine.SetAccessor(bl, "rules", rulesGet, nil)
	return bl
}

func registerBlockList(m engine.Object) {
	ctor := engine.NewFunction("BlockList", func(args []engine.Value) (engine.Value, error) {
		return newBlockListInstance(), nil
	})
	_ = m.Set("BlockList", ctor)
}

func ipRangeContains(from, to, ip net.IP) bool {
	// 仅比较 IPv4（简化）。
	af, bf, cf := from.To4(), to.To4(), ip.To4()
	if af == nil || bf == nil || cf == nil {
		return false
	}
	fa, fb, fc := ipToUint32(af), ipToUint32(bf), ipToUint32(cf)
	return fc >= fa && fc <= fb
}

func ipToUint32(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// --- SocketAddress ---------------------------------------------------------

// newSocketAddress 构造 SocketAddress 实例。
func newSocketAddress(args []engine.Value) engine.Value {
	sa := engine.NewObject()
	address := ""
	port := 0
	family := "ipv4"
	flowlabel := 0
	if len(args) > 0 {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("address"); err == nil && !v.IsUndefined() {
				address = v.String()
			}
			if v, err := o.Get("port"); err == nil {
				if n, ok := v.Int(); ok {
					port = n
				}
			}
			if v, err := o.Get("family"); err == nil && !v.IsUndefined() {
				// Node 规范化为小写（'ipv4'/'ipv6'）。
				family = strings.ToLower(v.String())
			}
			if v, err := o.Get("flowlabel"); err == nil {
				if n, ok := v.Int(); ok {
					flowlabel = n
				}
			}
		}
	}
	_ = sa.Set("address", engine.Str(address))
	_ = sa.Set("port", engine.IntValue(port))
	_ = sa.Set("family", engine.Str(family))
	_ = sa.Set("flowlabel", engine.IntValue(flowlabel))
	return sa
}

func registerSocketAddress(m engine.Object) {
	ctor := engine.NewFunction("SocketAddress", func(args []engine.Value) (engine.Value, error) {
		return newSocketAddress(args), nil
	})
	_ = m.Set("SocketAddress", ctor)
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
	server := nodeevents.NewEmitterInstance().(engine.Object)
	state := &netServerState{ctx: ctx}

	// server.listen(port[, host][, callback])
	_ = server.Set("listen", engine.NewFunction("listen", func(args []engine.Value) (engine.Value, error) {
		port := 0
		host := ""
		var callback engine.Value
		if len(args) > 0 {
			port = nodebase.IntArg(args, 0, 0)
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
				ctx.PostTask(func() { nodebase.EmitEvent(server, "error", engine.Str(err.Error())) })
				state.releaseLocked()
				return
			}
			state.mu.Lock()
			state.listener = ln
			state.mu.Unlock()
			ctx.PostTask(func() {
				_ = server.Set("listening", engine.Boolean(true))
				if callback != nil {
					if f, ok := callback.AsFunction(); ok {
						if _, err := f.Call(nil); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
				nodebase.EmitEvent(server, "listening")
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
				_ = server.Set("listening", engine.Boolean(false))
				if callback != nil {
					ctx.PostTask(func() {
						nodebase.EmitEvent(server, "close")
						if f, ok := callback.AsFunction(); ok {
							if _, err := f.Call(nil); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
						if release != nil {
							release()
						}
					})
				} else {
					ctx.PostTask(func() {
						nodebase.EmitEvent(server, "close")
					})
					if release != nil {
						release()
					}
				}
			}()
		} else {
			if callback != nil {
				if f, ok := callback.AsFunction(); ok {
					if _, err := f.Call(nil); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
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

	// server.getConnections(cb)：当前连接数（简化恒 0）。
	_ = server.Set("getConnections", engine.NewFunction("getConnections", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && args[0].IsFunction() {
			if f, ok := args[0].AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.Null(), engine.IntValue(0)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return server, nil
	}))
	_ = server.Set("ref", engine.NewFunction("ref", func(args []engine.Value) (engine.Value, error) {
		return server, nil
	}))
	_ = server.Set("unref", engine.NewFunction("unref", func(args []engine.Value) (engine.Value, error) {
		return server, nil
	}))

	// server 表面属性。
	_ = server.Set("listening", engine.Boolean(false))
	_ = server.Set("maxConnections", engine.Number(0))

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
	socket, _ := NewSocket(ctx, conn)
	ctx.PostTask(func() {
		nodebase.EmitEvent(server, "connection", socket)
		if listener != nil && listener.IsFunction() {
			if f, ok := listener.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{socket}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
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
func NewSocket(ctx engine.Context, conn net.Conn) (engine.Value, *netSocketState) {
	socket := nodeevents.NewEmitterInstance().(engine.Object)
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
		var writeCb engine.Value
		for _, a := range args[1:] {
			if a.IsFunction() {
				writeCb = a
			}
		}
		state.mu.Lock()
		conn := state.conn
		closed := state.closed
		state.mu.Unlock()
		if closed || conn == nil {
			return engine.Boolean(false), nil
		}
		_, err := conn.Write([]byte(args[0].String()))
		if writeCb != nil && writeCb.IsFunction() {
			if f, ok := writeCb.AsFunction(); ok {
				// Node 语义：write 回调在数据提交到 OS 后异步触发。
				ctx.PostTask(func() {
					if _, err := f.Call(nil); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				})
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

	// socket.setTimeout(timeout[, cb])：no-op 兼容（读循环天然无超时）。
	_ = socket.Set("setTimeout", engine.NewFunction("setTimeout", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))
	_ = socket.Set("setKeepAlive", engine.NewFunction("setKeepAlive", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))
	// ref/unref：活跃度已由 socket 自身持有，no-op 返回自身。
	_ = socket.Set("ref", engine.NewFunction("ref", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))
	_ = socket.Set("unref", engine.NewFunction("unref", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))
	// pause/resume：读循环持续运行（简化），no-op。
	_ = socket.Set("pause", engine.NewFunction("pause", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))
	_ = socket.Set("resume", engine.NewFunction("resume", func(args []engine.Value) (engine.Value, error) {
		return socket, nil
	}))

	// socket.pipe(dest)：把 'data' 转发到可写目标（简化：write 透传）。
	_ = socket.Set("pipe", engine.NewFunction("pipe", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			dest, ok := args[0].AsObject()
			if ok {
				onFn, _ := socket.Get("on")
				if f, ok := onFn.AsFunction(); ok {
					pipeFn := engine.NewFunction("pipeData", func(callArgs []engine.Value) (engine.Value, error) {
						if len(callArgs) > 0 {
							if wf, err := dest.Get("write"); err == nil && wf.IsFunction() {
								if w, ok := wf.AsFunction(); ok {
									if _, err := w.Call([]engine.Value{callArgs[0]}); err != nil {
										interpreter.ReportUncaught(nil, err)
									}
								}
							}
						}
						return engine.Undefined(), nil
					})
					if _, err := f.Call([]engine.Value{engine.Str("data"), pipeFn}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		}
		return socket, nil
	}))

	// socket.bytesRead / bytesWritten（统计，简化恒 0/已写量）。
	_ = socket.Set("bytesRead", engine.IntValue(0))

	// socket.address()
	_ = socket.Set("address", engine.NewFunction("address", func(args []engine.Value) (engine.Value, error) {
		state.mu.Lock()
		conn := state.conn
		state.mu.Unlock()
		addr := engine.NewObject()
		if conn != nil {
			if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
				_ = addr.Set("address", engine.Str(tcpAddr.IP.String()))
				_ = addr.Set("port", engine.IntValue(tcpAddr.Port))
			}
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
// 'end'/'close' 并释放活跃度。data 以 Buffer 传递（Node 语义）。
func startNetReader(ctx engine.Context, socket engine.Value, state *netSocketState, conn net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			ctx.PostTask(func() {
				nodebase.EmitEvent(socket, "data", gbuffer.NewBufferInstance(chunk))
			})
		}
		if err != nil {
			break
		}
	}
	state.close()
	ctx.PostTask(func() {
		nodebase.EmitEvent(socket, "end")
		nodebase.EmitEvent(socket, "close")
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
// 支持两种签名：
//   - net.connect(options[, connectListener])：options = {host, port, ...}
//   - net.connect(port[, host][, connectListener])
func newNetSocketClient(ctx engine.Context, args []engine.Value) engine.Value {
	host := "127.0.0.1"
	port := 0
	var connectListener engine.Value
	for _, a := range args {
		if a.IsFunction() {
			connectListener = a
		} else if o, ok := a.AsObject(); ok {
			if v, err := o.Get("host"); err == nil && !v.IsUndefined() && !v.IsNull() && v.String() != "" {
				host = v.String()
			}
			if v, err := o.Get("port"); err == nil {
				if n, ok := v.Int(); ok {
					port = n
				}
			}
		} else if a.Type() == engine.TypeNumber {
			if n, ok := a.Int(); ok {
				port = n
			}
		} else if a.Type() == engine.TypeString {
			host = a.String()
		}
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	socket, state := NewSocket(ctx, nil) // conn 延迟到 Dial 成功后设置

	go func() {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			ctx.PostTask(func() {
				nodebase.EmitEvent(socket, "error", engine.Str(err.Error()))
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
					if _, err := f.Call([]engine.Value{socket}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			nodebase.EmitEvent(socket, "connect")
		})
		go startNetReader(ctx, socket, state, conn)
	}()
	return socket
}
