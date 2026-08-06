package builtin

// node:async_hooks 内置模块（开发计划 3.11）。
//
// 提供：
//   - createHook / AsyncHook：生命周期钩子（init/before/after/destroy/
//     promiseResolve）。本引擎不创建 Node 内部异步资源，因此钩子回调仅在
//     AsyncResource 生命周期与 runInAsyncScope 时触发。
//   - executionAsyncId / triggerAsyncId / executionAsyncResource。
//   - AsyncResource：异步资源句柄（runInAsyncScope/emitDestroy/asyncId/
//     triggerAsyncId/bind）。
//   - AsyncLocalStorage：跨异步资源存取的 store（run/getStore/enterWith/
//     exit/disable）。传播模型：JS 闭包在创建时捕获当前 ALS 上下文，事件循环
//     外（定时器/微任务/IO 回调）调用时恢复——见 interpreter.AsyncContext
//     Capture/Restore 钩子。同步调用不恢复，保持 Node run()/enterWith 语义。
//   - asyncWrapProviders：Provider 类型名 → 数值 id（Node 对齐）。

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// functionInvoker 由 interpreter.VM 实现，支持带 this 的函数调用。
type functionInvoker interface {
	InvokeFn(fn, this engine.Value, args []engine.Value) (engine.Value, error)
}

// --- 全局异步上下文状态 -----------------------------------------------------

var nextAsyncID int64 = 1

// execMu 保护执行链状态（单线程 JS 执行，锁仅为防御）。
var execMu sync.Mutex

// execStack 是当前执行链的 asyncId 栈；triggerStack 是 triggerAsyncId 栈；
// resourceStack 是 executionAsyncResource 栈。
var (
	execStack     []int64
	triggerStack  []int64
	resourceStack []engine.Value
)

func currentExecID() int64 {
	execMu.Lock()
	defer execMu.Unlock()
	if len(execStack) > 0 {
		return execStack[len(execStack)-1]
	}
	return 1 // Node 顶层 executionAsyncId = 1
}

func currentTriggerID() int64 {
	execMu.Lock()
	defer execMu.Unlock()
	if len(triggerStack) > 0 {
		return triggerStack[len(triggerStack)-1]
	}
	return 0 // Node 顶层 triggerAsyncId = 0
}

// --- AsyncHook --------------------------------------------------------------

// hookState 表示一个 createHook 返回的 AsyncHook 实例。
type hookState struct {
	mu             sync.Mutex
	enabled        bool
	init           engine.Value
	before         engine.Value
	after          engine.Value
	destroy        engine.Value
	promiseResolve engine.Value
}

var (
	hooksMu sync.Mutex
	hooks   []*hookState
)

func (h *hookState) isEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled
}

func (h *hookState) setEnabled(on bool) {
	h.mu.Lock()
	h.enabled = on
	h.mu.Unlock()
}

// fireHook 向所有启用中的 hook 派发回调。
func fireHook(name string, args []engine.Value) {
	hooksMu.Lock()
	targets := make([]*hookState, 0, len(hooks))
	for _, h := range hooks {
		if h.isEnabled() {
			targets = append(targets, h)
		}
	}
	hooksMu.Unlock()
	for _, h := range targets {
		var cb engine.Value
		switch name {
		case "init":
			cb = h.init
		case "before":
			cb = h.before
		case "after":
			cb = h.after
		case "destroy":
			cb = h.destroy
		case "promiseResolve":
			cb = h.promiseResolve
		}
		if cb == nil || !cb.IsFunction() {
			continue
		}
		if f, ok := cb.AsFunction(); ok {
			_, _ = f.Call(args)
		}
	}
}

// --- AsyncLocalStorage ------------------------------------------------------

// alsEntry 是异步上下文快照中的一项：某个 AsyncLocalStorage 的 store 栈。
type alsEntry struct {
	als   *alsState
	stack []engine.Value
}

// alsState 表示一个 AsyncLocalStorage 实例。
type alsState struct {
	mu       sync.Mutex
	stack    []engine.Value // store 栈（栈顶 = 当前 store）
	disabled bool
}

var (
	alsMu  sync.Mutex
	allALS []*alsState
)

// alsCapture 实现 interpreter.AsyncContextCapture：快照所有启用的 ALS 栈。
func alsCapture() interface{} {
	alsMu.Lock()
	defer alsMu.Unlock()
	entries := make([]alsEntry, 0, len(allALS))
	for _, als := range allALS {
		als.mu.Lock()
		if !als.disabled {
			stack := append([]engine.Value(nil), als.stack...)
			entries = append(entries, alsEntry{als: als, stack: stack})
		}
		als.mu.Unlock()
	}
	return entries
}

// alsRestore 实现 interpreter.AsyncContextRestore：恢复 ALS 栈快照
// （已 disable 的 ALS 保持禁用，不恢复旧栈）。
func alsRestore(token interface{}) {
	entries, _ := token.([]alsEntry)
	for _, e := range entries {
		e.als.mu.Lock()
		if !e.als.disabled {
			e.als.stack = e.stack
		}
		e.als.mu.Unlock()
	}
}

func (a *alsState) getStore() engine.Value {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.disabled || len(a.stack) == 0 {
		return engine.Undefined()
	}
	return a.stack[len(a.stack)-1]
}

func (a *alsState) run(store, callback engine.Value, args []engine.Value) (engine.Value, error) {
	if !callback.IsFunction() {
		return engine.Undefined(), fmt.Errorf("async_hooks: AsyncLocalStorage.run callback must be a function")
	}
	f, _ := callback.AsFunction()
	a.mu.Lock()
	if a.disabled {
		a.mu.Unlock()
		return f.Call(args)
	}
	a.stack = append(a.stack, store)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if len(a.stack) > 0 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		a.mu.Unlock()
	}()
	return f.Call(args)
}

func (a *alsState) enterWith(store engine.Value) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.disabled {
		return
	}
	a.stack = append(a.stack, store)
}

func (a *alsState) exit(callback engine.Value, args []engine.Value) (engine.Value, error) {
	if !callback.IsFunction() {
		return engine.Undefined(), fmt.Errorf("async_hooks: AsyncLocalStorage.exit callback must be a function")
	}
	f, _ := callback.AsFunction()
	a.mu.Lock()
	a.stack = append(a.stack, engine.Undefined())
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if len(a.stack) > 0 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		a.mu.Unlock()
	}()
	return f.Call(args)
}

func (a *alsState) disable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.disabled = true
	a.stack = nil
}

// --- AsyncResource ----------------------------------------------------------

// resourceData 保存 AsyncResource 实例的状态。
type resourceData struct {
	uid       int64
	trigger   int64
	typ       string
	obj       engine.Value
	destroyed bool
}

var resources = struct {
	sync.Mutex
	m map[engine.Object]*resourceData
}{m: make(map[engine.Object]*resourceData)}

var hookInstances = struct {
	sync.Mutex
	m map[engine.Object]*hookState
}{m: make(map[engine.Object]*hookState)}

func lookupHook(inst engine.Value) *hookState {
	if inst == nil || !inst.IsObject() {
		return nil
	}
	o, _ := inst.AsObject()
	hookInstances.Lock()
	defer hookInstances.Unlock()
	return hookInstances.m[o]
}

func lookupResource(inst engine.Value) *resourceData {
	if inst == nil || !inst.IsObject() {
		return nil
	}
	o, _ := inst.AsObject()
	resources.Lock()
	defer resources.Unlock()
	return resources.m[o]
}

// invokeWithThis 以指定 this 调用 JS 函数。
func invokeWithThis(ctx engine.Context, fn, thisArg engine.Value, args []engine.Value) (engine.Value, error) {
	if invoker, ok := ctx.(functionInvoker); ok {
		return invoker.InvokeFn(fn, thisArg, args)
	}
	f, ok := fn.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("async_hooks: callback must be a function")
	}
	return f.Call(args)
}

// NewAsyncHooks 构造 node:async_hooks 模块导出对象。
func NewAsyncHooks(ctx engine.Context) (engine.Value, error) {
	// 安装异步上下文传播钩子（幂等）。
	if interpreter.AsyncContextCapture == nil {
		interpreter.AsyncContextCapture = alsCapture
		interpreter.AsyncContextRestore = alsRestore
	}

	mod := engine.NewObject()

	// --- createHook(hooks) → AsyncHook -----------------------------------
	hookProto := engine.NewObject()
	_ = hookProto.Set("enable", interpreter.NewNativeMethod("enable", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		h := lookupHook(this)
		if h != nil {
			h.setEnabled(true)
		}
		return engine.Undefined(), nil
	}))
	_ = hookProto.Set("disable", interpreter.NewNativeMethod("disable", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		h := lookupHook(this)
		if h != nil {
			h.setEnabled(false)
		}
		return engine.Undefined(), nil
	}))

	createHookFn := engine.NewFunction("createHook", func(args []engine.Value) (engine.Value, error) {
		h := &hookState{}
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				for _, k := range []string{"init", "before", "after", "destroy", "promiseResolve"} {
					if v, err := o.Get(k); err == nil {
						switch k {
						case "init":
							h.init = v
						case "before":
							h.before = v
						case "after":
							h.after = v
						case "destroy":
							h.destroy = v
						case "promiseResolve":
							h.promiseResolve = v
						}
					}
				}
			}
		}
		hooksMu.Lock()
		hooks = append(hooks, h)
		hooksMu.Unlock()

		inst := engine.NewObject()
		for _, k := range hookProto.Keys() {
			if v, err := hookProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		hookInstances.Lock()
		hookInstances.m[inst] = h
		hookInstances.Unlock()
		return inst, nil
	})
	_ = mod.Set("createHook", createHookFn)
	if co, ok := createHookFn.AsObject(); ok {
		_ = co.Set("prototype", hookProto)
	}

	// --- executionAsyncId / triggerAsyncId / executionAsyncResource -------
	_ = mod.Set("executionAsyncId", engine.NewFunction("executionAsyncId", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(int(currentExecID())), nil
	}))
	_ = mod.Set("triggerAsyncId", engine.NewFunction("triggerAsyncId", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(int(currentTriggerID())), nil
	}))
	_ = mod.Set("executionAsyncResource", engine.NewFunction("executionAsyncResource", func(args []engine.Value) (engine.Value, error) {
		execMu.Lock()
		defer execMu.Unlock()
		if len(resourceStack) > 0 {
			return resourceStack[len(resourceStack)-1], nil
		}
		return engine.NewObject(), nil
	}))

	// --- asyncWrapProviders ------------------------------------------------
	providers := engine.NewObject()
	for _, name := range asyncWrapProviderNames {
		_ = providers.Set(name, engine.IntValue(1))
	}
	_ = mod.Set("asyncWrapProviders", providers)

	// --- AsyncResource ------------------------------------------------------
	resourceProto := engine.NewObject()
	_ = resourceProto.Set("runInAsyncScope", interpreter.NewNativeMethod("runInAsyncScope", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("async_hooks: runInAsyncScope callback must be a function")
		}
		rd := lookupResource(this)
		if rd == nil {
			// 子类/引擎 super() 构造路径可能产生未注册实例：退化为直接调用
			// （不触发钩子），保持旧行为。
			fn, _ := args[0].AsFunction()
			thisArg := engine.Undefined()
			if len(args) > 1 {
				thisArg = args[1]
			}
			invokeArgs := []engine.Value{}
			if len(args) > 2 {
				invokeArgs = args[2:]
			}
			return invokeWithThis(ctx, fn, thisArg, invokeArgs)
		}
		fireHook("before", []engine.Value{engine.IntValue(int(rd.uid))})
		execMu.Lock()
		execStack = append(execStack, rd.uid)
		triggerStack = append(triggerStack, rd.trigger)
		resourceStack = append(resourceStack, rd.obj)
		execMu.Unlock()

		fn, _ := args[0].AsFunction()
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		invokeArgs := []engine.Value{}
		if len(args) > 2 {
			invokeArgs = args[2:]
		}
		result, err := invokeWithThis(ctx, fn, thisArg, invokeArgs)

		execMu.Lock()
		if len(execStack) > 0 {
			execStack = execStack[:len(execStack)-1]
		}
		if len(triggerStack) > 0 {
			triggerStack = triggerStack[:len(triggerStack)-1]
		}
		if len(resourceStack) > 0 {
			resourceStack = resourceStack[:len(resourceStack)-1]
		}
		execMu.Unlock()
		fireHook("after", []engine.Value{engine.IntValue(int(rd.uid))})
		return result, err
	}))
	_ = resourceProto.Set("emitDestroy", interpreter.NewNativeMethod("emitDestroy", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		rd := lookupResource(this)
		if rd == nil {
			return engine.Undefined(), nil
		}
		if !rd.destroyed {
			rd.destroyed = true
			fireHook("destroy", []engine.Value{engine.IntValue(int(rd.uid))})
		}
		return engine.Undefined(), nil
	}))
	_ = resourceProto.Set("asyncId", interpreter.NewNativeMethod("asyncId", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		rd := lookupResource(this)
		if rd == nil {
			return engine.IntValue(0), nil
		}
		return engine.IntValue(int(rd.uid)), nil
	}))
	_ = resourceProto.Set("triggerAsyncId", interpreter.NewNativeMethod("triggerAsyncId", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		rd := lookupResource(this)
		if rd == nil {
			return engine.IntValue(0), nil
		}
		return engine.IntValue(int(rd.trigger)), nil
	}))
	_ = resourceProto.Set("bind", interpreter.NewNativeMethod("bind", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("async_hooks: bind requires a function")
		}
		fn := args[0]
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		return engine.NewFunction("bound", func(callArgs []engine.Value) (engine.Value, error) {
			return invokeWithThis(ctx, fn, thisArg, callArgs)
		}), nil
	}))

	resourceCtor := engine.NewFunction("AsyncResource", func(args []engine.Value) (engine.Value, error) {
		typ := "async_hooks.AsyncResource"
		if len(args) > 0 {
			typ = args[0].String()
		}
		uid := atomic.AddInt64(&nextAsyncID, 1)
		trig := currentExecID()
		inst := engine.NewObject()
		for _, k := range resourceProto.Keys() {
			if v, err := resourceProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		rd := &resourceData{uid: uid, trigger: trig, typ: typ, obj: inst}
		resources.Lock()
		resources.m[inst] = rd
		resources.Unlock()
		fireHook("init", []engine.Value{engine.IntValue(int(uid)), engine.Str(typ), engine.IntValue(int(trig)), inst})
		return inst, nil
	})
	if co, ok := resourceCtor.AsObject(); ok {
		_ = co.Set("prototype", resourceProto)
		_ = co.Set("bind", engine.NewFunction("bind", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 || !args[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("async_hooks: AsyncResource.bind requires a function")
			}
			fn := args[0]
			thisArg := engine.Undefined()
			if len(args) > 2 {
				thisArg = args[2]
			}
			return engine.NewFunction("bound", func(callArgs []engine.Value) (engine.Value, error) {
				return invokeWithThis(ctx, fn, thisArg, callArgs)
			}), nil
		}))
	}
	_ = mod.Set("AsyncResource", resourceCtor)

	// --- AsyncLocalStorage --------------------------------------------------
	alsCtor := engine.NewFunction("AsyncLocalStorage", func(args []engine.Value) (engine.Value, error) {
		a := &alsState{}
		alsMu.Lock()
		allALS = append(allALS, a)
		alsMu.Unlock()

		inst := engine.NewObject()
		_ = inst.Set("run", interpreter.NewNativeMethod("run", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			if len(args) < 2 {
				return engine.Undefined(), fmt.Errorf("async_hooks: AsyncLocalStorage.run requires store and callback")
			}
			return a.run(args[0], args[1], args[2:])
		}))
		_ = inst.Set("getStore", interpreter.NewNativeMethod("getStore", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			return a.getStore(), nil
		}))
		_ = inst.Set("enterWith", interpreter.NewNativeMethod("enterWith", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			store := engine.Undefined()
			if len(args) > 0 {
				store = args[0]
			}
			a.enterWith(store)
			return engine.Undefined(), nil
		}))
		_ = inst.Set("exit", interpreter.NewNativeMethod("exit", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			if len(args) < 1 {
				return engine.Undefined(), fmt.Errorf("async_hooks: AsyncLocalStorage.exit requires a callback")
			}
			return a.exit(args[0], args[1:])
		}))
		_ = inst.Set("disable", interpreter.NewNativeMethod("disable", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			a.disable()
			return engine.Undefined(), nil
		}))
		return inst, nil
	})
	if co, ok := alsCtor.AsObject(); ok {
		proto := engine.NewObject()
		_ = proto.Set("constructor", alsCtor)
		_ = co.Set("prototype", proto)
	}
	_ = mod.Set("AsyncLocalStorage", alsCtor)

	return mod, nil
}

// asyncWrapProviderNames 与 Node 22 的 asyncWrapProviders 键对齐。
var asyncWrapProviderNames = []string{
	"BLOBREADER", "CHECKPRIMEREQUEST", "CIPHERREQUEST", "DERIVEBITSREQUEST",
	"DIRHANDLE", "DNSCHANNEL", "ELDHISTOGRAM", "FILEHANDLE", "FILEHANDLECLOSEREQ",
	"FSEVENTWRAP", "FSREQCALLBACK", "FSREQPROMISE", "GETADDRINFOREQWRAP",
	"GETNAMEINFOREQWRAP", "HASHREQUEST", "HEAPSNAPSHOT", "HTTP2PING",
	"HTTP2SESSION", "HTTP2SETTINGS", "HTTP2STREAM", "HTTPCLIENTREQUEST",
	"HTTPINCOMINGMESSAGE", "JSSTREAM", "JSUDPWRAP", "KEYEXPORTREQUEST",
	"KEYGENREQUEST", "KEYPAIRGENREQUEST", "MESSAGEPORT", "NONE", "PBKDF2REQUEST",
	"PIPECONNECTWRAP", "PIPESERVERWRAP", "PIPEWRAP", "PROCESSWRAP", "PROMISE",
	"QUERYWRAP", "QUIC_ENDPOINT", "QUIC_LOGSTREAM", "QUIC_PACKET", "QUIC_SESSION",
	"QUIC_STREAM", "QUIC_UDP", "RANDOMBYTESREQUEST", "RANDOMPRIMEREQUEST",
	"SCRYPTREQUEST", "SHUTDOWNWRAP", "SIGINTWATCHDOG", "SIGNALWRAP",
	"SIGNREQUEST", "STATWATCHER", "STREAMPIPE", "TCPCONNECTWRAP",
	"TCPSERVERWRAP", "TCPWRAP", "TLSWRAP", "TTYWRAP", "UDPSENDWRAP", "UDPWRAP",
	"VERIFYREQUEST", "WORKER", "WORKERCPUPROFILE", "WORKERCPUUSAGE",
	"WORKERHEAPSNAPSHOT", "WORKERHEAPSTATISTICS", "WRITEWRAP", "ZLIB",
}
