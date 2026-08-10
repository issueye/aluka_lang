package builtin

// node:worker_threads 内置模块（开发计划 3.9）。
//
// 架构（3-R1 应对：独立堆 + 消息传递，无 SharedArrayBuffer）：
//   - Worker 在独立 Go goroutine 运行一个完整 VM（interpreter.NewVMEngine），
//     loader 加载模块文件。
//   - 消息经 JSON 序列化跨 VM 传递（valueToJSON/jsonToEngine，复用 v8.go）。
//   - worker 内注入 parentPort 全局（postMessage/on('message')）与 workerData。
//   - 主线程 Worker 对象是 EventEmitter：postMessage/on('message')/threadId/
//     terminate。

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// workerState 是 Worker 的内部状态（跨 VM 消息通道）。
type workerState struct {
	toWorker      chan string // 主线程 → worker
	toMain        chan string // worker → 主线程
	closed        chan struct{}
	mu            sync.Mutex
	stopFn        func() // 终止 worker 的事件循环
	releaseWorker func() // 释放 worker 端 parentPort 的活跃度
}

func (s *workerState) setStop(fn func()) {
	s.mu.Lock()
	s.stopFn = fn
	s.mu.Unlock()
}

func (s *workerState) stop() {
	s.mu.Lock()
	fn := s.stopFn
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *workerState) setWorkerRelease(fn func()) {
	s.mu.Lock()
	s.releaseWorker = fn
	s.mu.Unlock()
}

func (s *workerState) releaseWorkerRef() {
	s.mu.Lock()
	fn := s.releaseWorker
	s.releaseWorker = nil
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

var workerThreadCounter int64

// NewWorkerThreads 构造 node:worker_threads 模块导出对象。
// parentPort/workerData/isMainThread 从全局读取（worker 内注入对应值）。
func NewWorkerThreads(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	// Node uses these markers to reject selected objects during structured
	// cloning/transfer. Aluka's worker transport already only serializes the
	// supported JSON-compatible subset, so marking is currently a no-op.
	_ = m.Set("markAsUncloneable", engine.NewFunction("markAsUncloneable", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("markAsUntransferable", engine.NewFunction("markAsUntransferable", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("isMarkedAsUntransferable", engine.NewFunction("isMarkedAsUntransferable", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	_ = m.Set("Worker", engine.NewFunction("Worker", func(args []engine.Value) (engine.Value, error) {
		return newWorkerInstance(ctx, args), nil
	}))
	// MessageChannel / MessagePort（同线程链接端口 + 消息缓冲）。
	_ = m.Set("MessageChannel", engine.NewFunction("MessageChannel", func(args []engine.Value) (engine.Value, error) {
		return makeMessageChannel(ctx), nil
	}))
	_ = m.Set("MessagePort", engine.NewFunction("MessagePort", func(args []engine.Value) (engine.Value, error) {
		// 无连接端口（Node 语义：可手动 start；此处返回独立端口）。
		p := makeMessagePort(ctx)
		_ = p.Set("_peer", engine.Null())
		return p, nil
	}))
	// BroadcastChannel：同/跨上下文广播（全局注册表按 name）。
	_ = m.Set("BroadcastChannel", engine.NewFunction("BroadcastChannel", func(args []engine.Value) (engine.Value, error) {
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		return makeBroadcastChannel(ctx, name), nil
	}))
	// getEnvironmentData / setEnvironmentData：跨 worker 共享环境数据。
	_ = m.Set("setEnvironmentData", engine.NewFunction("setEnvironmentData", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		key := workerDataKey(args[0])
		if len(args) > 1 {
			envDataMap.Store(key, args[1])
		} else {
			envDataMap.Delete(key)
		}
		return engine.Undefined(), nil
	}))
	_ = m.Set("getEnvironmentData", engine.NewFunction("getEnvironmentData", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		if v, ok := envDataMap.Load(workerDataKey(args[0])); ok {
			return v.(engine.Value), nil
		}
		return engine.Undefined(), nil
	}))
	// receiveMessageOnPort(port)：同步取缓冲消息（无则 undefined，Node 实测）。
	_ = m.Set("receiveMessageOnPort", engine.NewFunction("receiveMessageOnPort", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		if ps, ok := portStates.Load(args[0]); ok {
			if p := ps.(*msgPortState); p.pop() {
				obj := engine.NewObject()
				_ = obj.Set("message", p.lastMsg)
				return obj, nil
			}
		}
		return engine.Undefined(), nil
	}))
	// postMessageToThread(threadId, value[, transferList])：向指定 worker 发消息。
	_ = m.Set("postMessageToThread", engine.NewFunction("postMessageToThread", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		threadId := 0
		if n, ok := args[0].Int(); ok {
			threadId = n
		}
		if w, ok := threadWorkers.Load(threadId); ok {
			w.(*threadWorkerInfo).post(args[1])
		}
		return engine.Undefined(), nil
	}))
	// moveMessagePortToContext(port, contextifiedObject)：跨 context 移动端口。
	_ = m.Set("moveMessagePortToContext", engine.NewFunction("moveMessagePortToContext", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		// 近似：返回原端口（Aluka 的消息传输已按 JSON 序列化，跨 context
		// 语义由 postMessage 端点保证）。
		return args[0], nil
	}))
	// 主线程默认值。
	isMain := true
	if v, err := ctx.Global().Get("isMainThread"); err == nil && !v.IsUndefined() && !v.IsNull() {
		if b, ok := v.Bool(); ok {
			isMain = b
		}
	}
	_ = m.Set("isMainThread", engine.Boolean(isMain))
	_ = m.Set("threadId", engine.IntValue(0))
	_ = m.Set("isInternalThread", engine.Boolean(false))
	_ = m.Set("threadName", engine.Str(""))
	_ = m.Set("resourceLimits", engine.NewObject())
	_ = m.Set("SHARE_ENV", engine.NewSymbol("node:worker_threads:SHARE_ENV"))

	var ppVal engine.Value = engine.Null()
	if v, err := ctx.Global().Get("parentPort"); err == nil && !v.IsUndefined() && !v.IsNull() {
		ppVal = v
	}
	_ = m.Set("parentPort", ppVal)

	var wdVal engine.Value = engine.Null()
	if v, err := ctx.Global().Get("workerData"); err == nil && !v.IsUndefined() && !v.IsNull() {
		wdVal = v
	}
	_ = m.Set("workerData", wdVal)
	return m, nil
}

// newWorkerInstance 创建 Worker 对象并启动 worker goroutine。
func newWorkerInstance(mainCtx engine.Context, args []engine.Value) engine.Value {
	worker := newEmitterInstance().(engine.Object)
	filename := ""
	if len(args) > 0 {
		filename = args[0].String()
	}
	// workerData（JSON 序列化传给 worker）。
	var workerDataJSON []byte
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("workerData"); err == nil && !v.IsUndefined() {
				if dj, err := valueToJSON(v, make(map[engine.Object]bool)); err == nil {
					workerDataJSON, _ = json.Marshal(dj)
				}
			}
		}
	}

	state := &workerState{
		toWorker: make(chan string, 16),
		toMain:   make(chan string, 16),
		closed:   make(chan struct{}),
	}
	threadID := int(atomic.AddInt64(&workerThreadCounter, 1))
	_ = worker.Set("threadId", engine.IntValue(threadID))
	// 登记 threadId → worker（postMessageToThread 用）。
	threadWorkers.Store(threadID, &threadWorkerInfo{worker: worker, toChan: state.toWorker})

	// worker 计入事件循环活跃度（运行期间保持主进程存活）。
	release := mainCtx.AddRef()

	// worker.postMessage(data[, transferList]) → worker 端 parentPort 'message'。
	// transferList 中的 ArrayBuffer/Buffer 转移（detach 源，Node 语义）。
	_ = worker.Set("postMessage", engine.NewFunction("postMessage", func(pa []engine.Value) (engine.Value, error) {
		var msg engine.Value
		if len(pa) > 0 {
			msg = pa[0]
		}
		data, _ := json.Marshal(mustValueToJSON(msg))
		if len(pa) > 1 {
			detachTransferList(pa[1])
		}
		select {
		case state.toWorker <- string(data):
		case <-state.closed:
		}
		return engine.Undefined(), nil
	}))

	// worker.terminate()：关闭通道并停止 worker 事件循环。
	_ = worker.Set("terminate", engine.NewFunction("terminate", func(pa []engine.Value) (engine.Value, error) {
		select {
		case <-state.closed:
		default:
			close(state.closed)
		}
		state.stop()
		return engine.Undefined(), nil
	}))

	// 主线程接收 worker 消息 → worker 'message' 事件。
	go func() {
		for {
			select {
			case msg := <-state.toMain:
				mv := jsonToEngine(jsonDecode(msg))
				mainCtx.PostTask(func() {
					emitEvent(worker, "message", mv)
				})
			case <-state.closed:
				return
			}
		}
	}()

	// worker goroutine：独立 VM + loader。
	go func() {
		defer release()
		eng := interpreter.NewVMEngine()
		wctx, err := eng.NewContext()
		if err != nil {
			mainCtx.PostTask(func() {
				emitEvent(worker, "error", engine.Str(err.Error()))
				emitEvent(worker, "exit", engine.IntValue(1))
			})
			return
		}
		defer wctx.Close()
		_ = globals.NewConsole(wctx, globals.ConsoleConfig{})
		_ = globals.NewProcess(wctx, globals.ProcessConfig{})
		_ = globals.NewBuffer(wctx, globals.BufferConfig{})
		_ = globals.NewTimers(wctx, globals.TimerConfig{})
		_ = wctx.Global().Set("globalThis", wctx.Global())

		// parentPort：worker 端消息端口。
		// Node 语义：parentPort 默认不保持 worker 存活；添加 'message'/
		// 'messageerror' 监听器后才 ref（worker 脚本只 postMessage 则自然退出）。
		pp := newEmitterInstance().(engine.Object)
		_ = pp.Set("postMessage", engine.NewFunction("postMessage", func(pa []engine.Value) (engine.Value, error) {
			var msg engine.Value
			if len(pa) > 0 {
				msg = pa[0]
			}
			if len(pa) > 1 {
				detachTransferList(pa[1])
			}
			data, _ := json.Marshal(mustValueToJSON(msg))
			mv := jsonToEngine(jsonDecode(string(data)))
			// 直接 PostTask 到主线程：保证 'message' 先于后续 'exit' 投递
			// （Node 语义：消息一定在退出事件之前送达）。
			mainCtx.PostTask(func() {
				emitEvent(worker, "message", mv)
			})
			return engine.Undefined(), nil
		}))

		// parentPort keep-alive：监听器计数驱动（ref/unref）。
		keep := &ppKeepAlive{}
		origOn, _ := pp.Get("on")
		origOnce, _ := pp.Get("once")
		origOff, _ := pp.Get("off")
		origRemoveAll, _ := pp.Get("removeAllListeners")
		wrapAdd := func(fn engine.Value) engine.Value {
			return engine.NewFunction("wrapAdd", func(ca []engine.Value) (engine.Value, error) {
				if len(ca) > 0 {
					ev := ca[0].String()
					if ev == "message" || ev == "messageerror" {
						keep.add(wctx.AddRef)
					}
				}
				if f, ok := fn.AsFunction(); ok {
					return f.Call(ca)
				}
				return pp, nil
			})
		}
		wrapRemove := func(fn engine.Value) engine.Value {
			return engine.NewFunction("wrapRemove", func(ca []engine.Value) (engine.Value, error) {
				if len(ca) > 0 {
					ev := ca[0].String()
					if ev == "message" || ev == "messageerror" {
						// removeAllListeners 或 removeListener 移除最后一个
						// message 监听器后释放 keep-alive。
						if lc, lerr := pp.Get("listenerCount"); lerr == nil && lc.IsFunction() {
							if lf, ok := lc.AsFunction(); ok {
								if rv, cerr := lf.Call([]engine.Value{ca[0]}); cerr == nil {
									if n, ok2 := rv.Int(); ok2 && n <= 1 {
										keep.remove()
									}
								}
							}
						}
					}
				}
				if f, ok := fn.AsFunction(); ok {
					return f.Call(ca)
				}
				return pp, nil
			})
		}
		_ = pp.Set("on", wrapAdd(origOn))
		_ = pp.Set("addListener", wrapAdd(origOn))
		_ = pp.Set("once", wrapAdd(origOnce))
		_ = pp.Set("off", wrapRemove(origOff))
		_ = pp.Set("removeListener", wrapRemove(origOff))
		_ = pp.Set("removeAllListeners", wrapRemove(origRemoveAll))

		_ = pp.Set("close", engine.NewFunction("close", func(pa []engine.Value) (engine.Value, error) {
			keep.remove()
			select {
			case <-state.closed:
			default:
				close(state.closed)
			}
			return engine.Undefined(), nil
		}))
		_ = wctx.Global().Set("parentPort", pp)
		_ = wctx.Global().Set("isMainThread", engine.Boolean(false))
		if workerDataJSON != nil {
			_ = wctx.Global().Set("workerData", jsonToEngine(jsonDecode(string(workerDataJSON))))
		}

		// 主线程消息 → worker 端 parentPort 'message'。
		go func() {
			for {
				select {
				case msg := <-state.toWorker:
					mv := jsonToEngine(jsonDecode(msg))
					wctx.PostTask(func() {
						emitEvent(pp, "message", mv)
					})
				case <-state.closed:
					return
				}
			}
		}()

		// 加载并运行 worker 模块。
		loader := modmodule.NewLoader(wctx)
		RegisterAll(loader)
		// eval: true —— 首个参数为源码而非文件路径。
		evalMode := false
		if len(args) > 1 {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("eval"); err == nil && !v.IsUndefined() {
					if b, ok := v.Bool(); ok {
						evalMode = b
					}
				}
			}
		}
		var runErr error
		if evalMode {
			// eval 模式：注入 require（worker_threads 等内置模块可加载）。
			_ = wctx.Global().Set("require", loader.MakeRequireFunc("[worker eval]"))
			_, runErr = wctx.Eval(filename, "[worker eval]")
		} else {
			runErr = loader.Run(filename)
		}
		if runErr != nil {
			mainCtx.PostTask(func() {
				emitEvent(worker, "error", engine.Str(fmt.Sprintf("worker: %v", runErr)))
				emitEvent(worker, "exit", engine.IntValue(1))
			})
			return
		}
		// 主线程 terminate 时停止 worker 事件循环。
		state.setStop(func() {
			if vm, ok := wctx.(interface{ Stop() }); ok {
				vm.Stop()
			}
		})
		if vm, ok := wctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		// 冲刷残留消息：确保 'message' 先于 'exit' 到达主线程（Node 语义）。
		for {
			select {
			case msg := <-state.toMain:
				mv := jsonToEngine(jsonDecode(msg))
				mainCtx.PostTask(func() {
					emitEvent(worker, "message", mv)
				})
				continue
			default:
			}
			break
		}
		// worker 事件循环结束 → 主线程 'exit'。
		mainCtx.PostTask(func() {
			emitEvent(worker, "exit", engine.IntValue(0))
		})
	}()

	return worker
}

// jsonDecode 把 JSON 字符串解码为 interface{}。
func jsonDecode(s string) interface{} {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return v
}

// ppKeepAlive 跟踪 parentPort 的 'message' 监听器数量（Node 语义：
// 有监听器时 worker 保持存活，无监听器时允许 worker 自然退出）。
// add 用工厂函数惰性创建 ref（wctx.AddRef）。
type ppKeepAlive struct {
	mu      sync.Mutex
	count   int
	release func()
}

func (k *ppKeepAlive) add(mkRef func() func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.count++
	if k.release == nil {
		k.release = mkRef()
	}
}

func (k *ppKeepAlive) remove() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.count > 0 {
		k.count--
	}
	if k.count == 0 && k.release != nil {
		r := k.release
		k.release = nil
		r()
	}
}

// --- MessagePort / MessageChannel / BroadcastChannel / env data -----------

// envDataMap：setEnvironmentData/getEnvironmentData 的共享存储
// （worker 与主线程共享，Node 语义）。
var envDataMap sync.Map // string → engine.Value

// portStates：端口对象 → 消息缓冲状态（receiveMessageOnPort 用）。
var portStates sync.Map // engine.Value → *msgPortState

// msgPortState 消息缓冲（receiveMessageOnPort 同步取；'message' 监听器
// 异步取）。JSON 序列化保证跨 context 可传输。
type msgPortState struct {
	mu      sync.Mutex
	queue   []engine.Value
	lastMsg engine.Value
	closed  bool
}

func (s *msgPortState) push(v engine.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.queue = append(s.queue, v)
	s.lastMsg = v
}

func (s *msgPortState) pop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return false
	}
	s.lastMsg = s.queue[0]
	s.queue = s.queue[1:]
	return true
}

// makeMessageChannel 构造 {port1, port2} 链接端口对。
func makeMessageChannel(ctx engine.Context) engine.Value {
	p1 := makeMessagePort(ctx)
	p2 := makeMessagePort(ctx)
	_ = p1.Set("_peer", p2)
	_ = p2.Set("_peer", p1)
	ch := engine.NewObject()
	_ = ch.Set("port1", p1)
	_ = ch.Set("port2", p2)
	return ch
}

// portStateFor 取端口的消息缓冲状态（惰性创建并登记）。
func portStateFor(port engine.Object) *msgPortState {
	if v, ok := portStates.Load(port); ok {
		return v.(*msgPortState)
	}
	st := &msgPortState{}
	portStates.Store(port, st)
	return st
}

// makeMessagePort 构造单端口对象（postMessage/close/ref/unref/start/hasRef）。
// 通过 _peer 关联对端；无 _peer 的端口 postMessage 直接丢弃。
func makeMessagePort(ctx engine.Context) engine.Object {
	port := newEmitterInstance().(engine.Object)
	st := portStateFor(port)

	_ = port.Set("postMessage", engine.NewFunction("postMessage", func(pa []engine.Value) (engine.Value, error) {
		var msg engine.Value
		if len(pa) > 0 {
			msg = pa[0]
		}
		if len(pa) > 1 {
			detachTransferList(pa[1])
		}
		// JSON 序列化（跨 context 一致）。
		var data []byte
		if b, err := json.Marshal(mustValueToJSON(msg)); err == nil {
			data = b
		}
		var peerV engine.Value
		if pv, err := port.Get("_peer"); err == nil {
			peerV = pv
		}
		if peerV.IsNull() || peerV.IsUndefined() {
			return engine.Undefined(), nil
		}
		peerObj, _ := peerV.AsObject()
		if peerObj == nil {
			return engine.Undefined(), nil
		}
		peerSt := portStateFor(peerObj)
		peerSt.push(jsonToEngine(jsonDecode(string(data))))
		// 异步派发 'message'（有监听器时）。
		deliverPortMessage(ctx, peerObj, peerSt)
		return engine.Undefined(), nil
	}))

	_ = port.Set("close", engine.NewFunction("close", func(pa []engine.Value) (engine.Value, error) {
		st.mu.Lock()
		st.closed = true
		st.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = port.Set("ref", engine.NewFunction("ref", func(pa []engine.Value) (engine.Value, error) {
		return port, nil
	}))
	_ = port.Set("unref", engine.NewFunction("unref", func(pa []engine.Value) (engine.Value, error) {
		return port, nil
	}))
	_ = port.Set("start", engine.NewFunction("start", func(pa []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = port.Set("hasRef", engine.NewFunction("hasRef", func(pa []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	return port
}

// deliverPortMessage 把缓冲消息派发给端口的 'message' 监听器（异步）。
// 无监听器时消息留在缓冲（receiveMessageOnPort 可取）。
func deliverPortMessage(ctx engine.Context, port engine.Object, st *msgPortState) {
	if lc, err := port.Get("listenerCount"); err == nil && lc.IsFunction() {
		if f, ok := lc.AsFunction(); ok {
			if rv, cerr := f.Call([]engine.Value{engine.Str("message")}); cerr == nil {
				if n, ok2 := rv.Int(); ok2 && n > 0 {
					st.mu.Lock()
					msgs := append([]engine.Value{}, st.queue...)
					st.queue = nil
					st.mu.Unlock()
					ctx.PostTask(func() {
						for _, m := range msgs {
							emitEvent(port, "message", m)
						}
					})
					return
				}
			}
		}
	}
}

// broadcastRegistry：name → 打开的 BroadcastChannel 集合。
var broadcastRegistry struct {
	sync.Mutex
	channels map[string][]*broadcastChannel
}

type broadcastChannel struct {
	name  string
	ctx   engine.Context
	port  engine.Object
	state *msgPortState
}

// makeBroadcastChannel 构造 BroadcastChannel（同 name 通道互发）。
func makeBroadcastChannel(ctx engine.Context, name string) engine.Object {
	port := makeMessagePort(ctx)
	bc := &broadcastChannel{name: name, ctx: ctx, port: port, state: portStateFor(port)}
	_ = port.Set("name", engine.Str(name))
	// 覆盖 postMessage：广播给所有同 name 通道。
	_ = port.Set("postMessage", engine.NewFunction("postMessage", func(pa []engine.Value) (engine.Value, error) {
		var msg engine.Value
		if len(pa) > 0 {
			msg = pa[0]
		}
		if len(pa) > 1 {
			detachTransferList(pa[1])
		}
		data, _ := json.Marshal(mustValueToJSON(msg))
		decoded := jsonToEngine(jsonDecode(string(data)))
		broadcastRegistry.Lock()
		var targets []*broadcastChannel
		for _, c := range broadcastRegistry.channels[name] {
			if c != bc {
				targets = append(targets, c)
			}
		}
		broadcastRegistry.Unlock()
		for _, c := range targets {
			c.state.push(decoded)
			deliverPortMessage(c.ctx, c.port, c.state)
		}
		return engine.Undefined(), nil
	}))
	// 覆盖 close：从注册表移除。
	origClose, _ := port.Get("close")
	_ = port.Set("close", engine.NewFunction("close", func(pa []engine.Value) (engine.Value, error) {
		broadcastRegistry.Lock()
		chans := broadcastRegistry.channels[name]
		for i, c := range chans {
			if c == bc {
				chans = append(chans[:i], chans[i+1:]...)
				break
			}
		}
		if len(chans) == 0 {
			delete(broadcastRegistry.channels, name)
		} else {
			broadcastRegistry.channels[name] = chans
		}
		broadcastRegistry.Unlock()
		if f, ok := origClose.AsFunction(); ok {
			if _, err := f.Call(pa); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
		return engine.Undefined(), nil
	}))

	broadcastRegistry.Lock()
	if broadcastRegistry.channels == nil {
		broadcastRegistry.channels = map[string][]*broadcastChannel{}
	}
	broadcastRegistry.channels[name] = append(broadcastRegistry.channels[name], bc)
	broadcastRegistry.Unlock()
	return port
}

// workerDataKey 序列化 set/getEnvironmentData 的键（symbol 用 KeyFor/描述，
// 其他用 String/数字）。
func workerDataKey(v engine.Value) string {
	if s, ok := v.(*engine.SymbolValue); ok {
		if k, has := s.KeyFor(); has {
			return "symbol:" + k
		}
		return "symbol:" + s.String()
	}
	if n, ok := v.Float(); ok {
		return "num:" + fmt.Sprintf("%v", n)
	}
	return "str:" + v.String()
}

// threadWorkers：threadId → 主线程 Worker 信息（postMessageToThread 用）。
var threadWorkers sync.Map // int → *threadWorkerInfo

type threadWorkerInfo struct {
	worker engine.Object
	toChan chan string
}

func (tw *threadWorkerInfo) post(v engine.Value) {
	data, _ := json.Marshal(mustValueToJSON(v))
	select {
	case tw.toChan <- string(data):
	default:
	}
}

// detachTransferList 处理 postMessage 的 transferList：ArrayBuffer/Buffer
// 所有权转移——源内容置零（Node detach 语义近似：转移后源不可用）。
func detachTransferList(tl engine.Value) {
	if o, ok := tl.AsObject(); ok {
		lv, err := o.Get("length")
		if err != nil || lv.IsUndefined() {
			return
		}
		n, _ := lv.Int()
		for i := 0; i < n; i++ {
			bv, err := o.Get(strconv.Itoa(i))
			if err != nil {
				continue
			}
			if b, ok := engine.AsBuffer(bv); ok {
				clear(b) // 转移后源 Buffer 内容清零（长度保留，近似 detach）
			}
		}
	}
}
