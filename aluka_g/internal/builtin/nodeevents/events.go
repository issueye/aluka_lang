package nodeevents

// node:events 内置模块——提供 EventEmitter。
//
// 实现方案：EventEmitter 是一个构造器（支持 new EventEmitter()）。
// 构造时在闭包中创建 listeners map，并构造一个含 on/emit/off 等方法的对象。
// 方法不依赖 JS this 绑定（绕过 engine.Func 无 this 参数的限制），
// 而是通过闭包捕获实例状态。
//
// M2 补齐的 Node 语义：
//   - Symbol 事件名（symbolListeners 按 Symbol 身份独立存储）
//   - emit('error') 无监听器时抛出原值；errorMonitor 先于常规监听器调用
//   - newListener / removeListener 事件
//   - maxListeners 警告（process.emitWarning 或 stderr）
//   - captureRejections（async 监听器 rejection → 'error'）
//   - 模块级事件 API：events.on/once/getEventListeners/addAbortListener

import (
	"fmt"
	"os"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// defaultMaxListeners 是 EventEmitter 的默认最大监听器数。
const defaultMaxListeners = 10

// emitterStateKey 是挂在实例对象上的隐藏属性名，用于存放该实例的事件状态
// （JS 对象组织结构，天然参与引擎 GC，不会泄漏）。
const emitterStateKey = "\x00<aluka>emitterState"

// eventsErrorMonitor 对应 Node 的 EventEmitter.errorMonitor（Symbol 键）。
var eventsErrorMonitor = engine.NewSymbol("events.errorMonitor")

// eventsCaptureRejectionSymbol 对应 Node 的 EventEmitter.captureRejectionSymbol。
var eventsCaptureRejectionSymbol = engine.NewSymbol("events.captureRejection")

// eventsCtx 供 maxListeners 警告时读取 process.emitWarning（NewEvents 时设置）。
var eventsCtx engine.Context

// NewEvents 构造 node:events 模块导出。Node.js 的 events 模块本身就是
// EventEmitter 构造器，同时也通过 .EventEmitter 暴露同一个构造器。
func NewEvents(ctx engine.Context) (engine.Value, error) {
	eventsCtx = ctx
	// EventEmitter 构造器：new EventEmitter() 返回一个 emitter 实例对象。
	ctor := engine.NewFunction("EventEmitter", func(args []engine.Value) (engine.Value, error) {
		return newEmitterInstanceOpts(args), nil
	})
	ctorObj, _ := ctor.AsObject()
	// 设置 prototype 属性（支持 instanceof 与继承）。
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	_ = ctorObj.Set("prototype", proto)
	_ = ctorObj.Set("defaultMaxListeners", engine.IntValue(defaultMaxListeners))

	// 在 EventEmitter.prototype 上注册标准方法。这些方法通过 `this` 定位
	// 实例状态（挂在 emitterStateKey 隐藏属性上），因此可被 `mixin(app,
	// EventEmitter.prototype)` 拷贝到任意对象（如 express 的 app）使用。
	registerEmitterPrototype(proto)

	// 静态导出（Node 22 全集，注意：Node 没有静态 off）。
	_ = ctorObj.Set("on", engine.NewFunction("on", eventsOnModule))
	_ = ctorObj.Set("once", engine.NewFunction("once", eventsOnceModule))
	_ = ctorObj.Set("listenerCount", engine.NewFunction("listenerCount", makeStaticEmitterMethod("listenerCount")))
	_ = ctorObj.Set("getMaxListeners", engine.NewFunction("getMaxListeners", makeStaticEmitterMethod("getMaxListeners")))
	_ = ctorObj.Set("setMaxListeners", engine.NewFunction("setMaxListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				if n, err := o.Get("setMaxListeners"); err == nil && n.IsFunction() {
					if f, ok := n.AsFunction(); ok {
						return f.Call(args[1:])
					}
				}
			}
		}
		return engine.Undefined(), nil
	}))
	// getEventListeners(emitter, event)：返回监听器数组。
	_ = ctorObj.Set("getEventListeners", engine.NewFunction("getEventListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		return CallEmitterMethod(args[0], "listeners", args[1:])
	}))
	// addAbortListener(signal, listener)：监听 signal 的 abort 事件，
	// 返回 { unref, ref } 对象。
	_ = ctorObj.Set("addAbortListener", engine.NewFunction("addAbortListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		signal := args[0]
		listener := args[1]
		if o, ok := signal.AsObject(); ok {
			if onFn, err := o.Get("addEventListener"); err == nil && onFn.IsFunction() {
				if f, ok := onFn.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{engine.Str("abort"), listener}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		}
		h := engine.NewObject()
		_ = h.Set("unref", engine.NewFunction("unref", func(a []engine.Value) (engine.Value, error) { return engine.Undefined(), nil }))
		_ = h.Set("ref", engine.NewFunction("ref", func(a []engine.Value) (engine.Value, error) { return engine.Undefined(), nil }))
		return h, nil
	}))
	// init：Node 内部钩子，暴露为 no-op 函数。
	_ = ctorObj.Set("init", engine.NewFunction("init", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// EventEmitterAsyncResource：AsyncResource + EventEmitter 组合（API 面）。
	_ = ctorObj.Set("EventEmitterAsyncResource", makeEventEmitterAsyncResource(ctx))

	// 静态属性。
	_ = ctorObj.Set("errorMonitor", eventsErrorMonitor)
	_ = ctorObj.Set("captureRejectionSymbol", eventsCaptureRejectionSymbol)
	_ = ctorObj.Set("captureRejections", engine.IntValue(0))
	_ = ctorObj.Set("usingDomains", engine.Boolean(false))
	_ = ctorObj.Set("defaultMaxListeners", engine.IntValue(defaultMaxListeners))

	_ = ctorObj.Set("EventEmitter", ctor)

	return ctor, nil
}

// makeStaticEmitterMethod 构造模块级静态方法的 engine.Func
// （接受 emitter 作为第一个参数，转发到实例方法）。
func makeStaticEmitterMethod(method string) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("EventEmitter.%s: requires emitter as first argument", method)
		}
		return CallEmitterMethod(args[0], method, args[1:])
	}
}

// emitterState 是一个 EventEmitter 实例的内部状态。
type emitterState struct {
	listeners         map[string][]engine.Value              // string 事件
	symbolListeners   map[*engine.SymbolValue][]engine.Value // Symbol 事件
	maxListeners      int
	captureRejections bool
}

// getListeners 按事件值（string 或 Symbol）取监听器切片。
func (st *emitterState) getListeners(ev engine.Value) []engine.Value {
	if ev.Type() == engine.TypeSymbol {
		if s, ok := ev.(*engine.SymbolValue); ok {
			return st.symbolListeners[s]
		}
		return nil
	}
	return st.listeners[ev.String()]
}

// appendListener 追加监听器（string 或 Symbol）。
func (st *emitterState) appendListener(ev engine.Value, l engine.Value) {
	if ev.Type() == engine.TypeSymbol {
		if s, ok := ev.(*engine.SymbolValue); ok {
			st.symbolListeners[s] = append(st.symbolListeners[s], l)
		}
		return
	}
	name := ev.String()
	st.listeners[name] = append(st.listeners[name], l)
}

// prependListener 在头部插入监听器。
func (st *emitterState) prependListener(ev engine.Value, l engine.Value) {
	if ev.Type() == engine.TypeSymbol {
		if s, ok := ev.(*engine.SymbolValue); ok {
			st.symbolListeners[s] = append([]engine.Value{l}, st.symbolListeners[s]...)
		}
		return
	}
	name := ev.String()
	st.listeners[name] = append([]engine.Value{l}, st.listeners[name]...)
}

// removeListenerValue 移除监听器，返回是否实际移除。
func (st *emitterState) removeListenerValue(ev engine.Value, target engine.Value) bool {
	if ev.Type() == engine.TypeSymbol {
		s, ok := ev.(*engine.SymbolValue)
		if !ok {
			return false
		}
		l := st.symbolListeners[s]
		for i, x := range l {
			if x == target {
				st.symbolListeners[s] = append(append([]engine.Value{}, l[:i]...), l[i+1:]...)
				return true
			}
		}
		return false
	}
	name := ev.String()
	l := st.listeners[name]
	for i, x := range l {
		if x == target {
			st.listeners[name] = append(append([]engine.Value{}, l[:i]...), l[i+1:]...)
			return true
		}
	}
	return false
}

// countListeners 返回事件监听器数。
func (st *emitterState) countListeners(ev engine.Value) int {
	return len(st.getListeners(ev))
}

// eventNames 返回全部事件名（string 或 Symbol 对象）。
func (st *emitterState) eventNames() []engine.Value {
	names := make([]engine.Value, 0, len(st.listeners)+len(st.symbolListeners))
	for name := range st.listeners {
		names = append(names, engine.Str(name))
	}
	for sym := range st.symbolListeners {
		names = append(names, sym)
	}
	return names
}

// callListeners 依次调用监听器快照；监听器抛错则传播。
func callListeners(listeners []engine.Value, args []engine.Value) error {
	for _, fn := range listeners {
		if f, ok := fn.AsFunction(); ok {
			if _, err := f.Call(args); err != nil {
				return err
			}
		}
	}
	return nil
}

// warnMaxListeners 输出 Node 风格 maxListeners 警告（process.emitWarning 或 stderr）。
func warnMaxListeners(event string, count, max int) {
	msg := fmt.Sprintf("MaxListenersExceededWarning: Possible EventEmitter memory leak detected. %d %s listeners added to [EventEmitter]. Use emitter.setMaxListeners() to increase limit", count, event)
	if eventsCtx != nil {
		if procV, err := eventsCtx.Global().Get("process"); err == nil {
			if po, ok := procV.AsObject(); ok {
				if ew, err := po.Get("emitWarning"); err == nil && ew.IsFunction() {
					if f, ok := ew.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{engine.Str(msg)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
						return
					}
				}
			}
		}
	}
	fmt.Fprintln(os.Stderr, msg)
}

// newEmitterInstance 构造一个 EventEmitter 实例对象（默认选项）。
func NewEmitterInstance() engine.Value {
	return newEmitterInstanceOpts(nil)
}

// newEmitterInstanceOpts 构造 EventEmitter 实例，支持 { captureRejections } 选项。
func newEmitterInstanceOpts(args []engine.Value) engine.Value {
	obj := engine.NewObject()

	state := &emitterState{
		listeners:       make(map[string][]engine.Value),
		symbolListeners: make(map[*engine.SymbolValue][]engine.Value),
		maxListeners:    defaultMaxListeners,
	}
	if len(args) > 0 {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("captureRejections"); err == nil {
				if b, ok := v.Bool(); ok {
					state.captureRejections = b
				}
			}
		}
	}

	// on(event, listener)：注册监听器，返回 emitter（链式）。
	onFn := engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		addListenerState(state, args)
		return obj, nil
	})
	_ = obj.Set("on", onFn)
	_ = obj.Set("addListener", onFn)

	// once(event, listener)：注册一次性监听器（用 wrapper 在触发后自删）。
	onceFn := engine.NewFunction("once", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0]
			original := args[1]
			var wrapper engine.Value
			wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
				if f, ok := original.AsFunction(); ok {
					if _, err := f.Call(callArgs); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
				state.removeListenerValue(event, wrapper)
				return engine.Undefined(), nil
			})
			addListenerState(state, []engine.Value{event, wrapper})
		}
		return obj, nil
	})
	_ = obj.Set("once", onceFn)

	// off / removeListener / removeEventListener(event, listener)
	offFn := engine.NewFunction("off", func(args []engine.Value) (engine.Value, error) {
		removeListenerState(state, args)
		return obj, nil
	})
	_ = obj.Set("off", offFn)
	_ = obj.Set("removeListener", offFn)
	_ = obj.Set("removeEventListener", offFn)

	// emit(event, ...args)：触发事件，返回是否有监听器。
	// emitFn 用 var 前置声明（captureRejections 闭包递归引用自身）。
	var emitFn engine.Value
	emitFn = engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		event := args[0]
		callArgs := args[1:]

		// error 特殊路径：errorMonitor 先被调用；无常规 'error' 监听器时抛出原值。
		if event.Type() == engine.TypeString && event.String() == "error" {
			var mon []engine.Value
			if syms := state.symbolListeners[eventsErrorMonitor]; len(syms) > 0 {
				mon = syms
			}
			var reg []engine.Value
			if l := state.listeners["error"]; len(l) > 0 {
				reg = l
			}
			if len(mon) > 0 {
				if err := callListeners(mon, callArgs); err != nil {
					return engine.Undefined(), err
				}
			}
			if len(reg) == 0 {
				errVal := engine.Undefined()
				if len(callArgs) > 0 {
					errVal = callArgs[0]
				}
				return engine.Undefined(), interpreter.ThrowJSValue(errVal)
			}
			if err := callListeners(reg, callArgs); err != nil {
				return engine.Undefined(), err
			}
			return engine.Boolean(true), nil
		}

		listeners := state.getListeners(event)
		if len(listeners) == 0 {
			return engine.Boolean(false), nil
		}
		// 复制一份避免遍历时修改（once wrapper 会删除自身）。
		snapshot := make([]engine.Value, len(listeners))
		copy(snapshot, listeners)
		for _, fn := range snapshot {
			f, ok := fn.AsFunction()
			if !ok {
				continue
			}
			result, callErr := f.Call(callArgs)
			if callErr != nil {
				return engine.Undefined(), callErr
			}
			// captureRejections：async 监听器 rejection → emit('error')。
			if state.captureRejections && result != nil {
				if pv, ok := result.(*interpreter.PromiseValue); ok {
					noop := engine.NewFunction("noop", func(ca []engine.Value) (engine.Value, error) {
						return engine.Undefined(), nil
					})
					onRejected := engine.NewFunction("captureRejection", func(ca []engine.Value) (engine.Value, error) {
						reason := engine.Undefined()
						if len(ca) > 0 {
							reason = ca[0]
						}
						if f, ok := emitFn.AsFunction(); ok {
							if _, err := f.Call([]engine.Value{engine.Str("error"), reason}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
						return engine.Undefined(), nil
					})
					pv.Then(noop, onRejected)
				}
			}
		}
		return engine.Boolean(true), nil
	})
	_ = obj.Set("emit", emitFn)

	// listeners(event)：返回事件监听器数组。
	_ = obj.Set("listeners", engine.NewFunction("listeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		return engine.NewArray(append([]engine.Value{}, state.getListeners(args[0])...)), nil
	}))

	// listenerCount(event)：返回监听器数量。
	_ = obj.Set("listenerCount", engine.NewFunction("listenerCount", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(0), nil
		}
		return engine.IntValue(state.countListeners(args[0])), nil
	}))

	// eventNames()：返回所有已注册事件名数组（string 与 Symbol）。
	_ = obj.Set("eventNames", engine.NewFunction("eventNames", func(args []engine.Value) (engine.Value, error) {
		return engine.NewArray(state.eventNames()), nil
	}))

	// removeAllListeners([event])
	_ = obj.Set("removeAllListeners", engine.NewFunction("removeAllListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || args[0].IsUndefined() {
			state.listeners = make(map[string][]engine.Value)
			state.symbolListeners = make(map[*engine.SymbolValue][]engine.Value)
		} else {
			if args[0].Type() == engine.TypeSymbol {
				if s, ok := args[0].(*engine.SymbolValue); ok {
					delete(state.symbolListeners, s)
				}
			} else {
				delete(state.listeners, args[0].String())
			}
		}
		return obj, nil
	}))

	// setMaxListeners(n)
	_ = obj.Set("setMaxListeners", engine.NewFunction("setMaxListeners", func(args []engine.Value) (engine.Value, error) {
		state.maxListeners = nodebase.IntArg(args, 0, defaultMaxListeners)
		return obj, nil
	}))

	// getMaxListeners()
	_ = obj.Set("getMaxListeners", engine.NewFunction("getMaxListeners", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(state.maxListeners), nil
	}))

	// prependListener(event, listener)：在头部插入。
	prependFn := engine.NewFunction("prependListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			state.prependListener(args[0], args[1])
		}
		return obj, nil
	})
	_ = obj.Set("prependListener", prependFn)

	// prependOnceListener
	_ = obj.Set("prependOnceListener", engine.NewFunction("prependOnceListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0]
			original := args[1]
			var wrapper engine.Value
			wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
				if f, ok := original.AsFunction(); ok {
					if _, err := f.Call(callArgs); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
				state.removeListenerValue(event, wrapper)
				return engine.Undefined(), nil
			})
			state.prependListener(event, wrapper)
		}
		return obj, nil
	}))

	// rawListeners(event)：同 listeners（简化）。
	_ = obj.Set("rawListeners", engine.NewFunction("rawListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		return engine.NewArray(append([]engine.Value{}, state.getListeners(args[0])...)), nil
	}))

	// 事件状态挂到实例隐藏属性（供原型方法/诊断复用）。
	stObj := engine.NewObject()
	_ = stObj.Set("max", engine.IntValue(state.maxListeners))
	_ = obj.Set(emitterStateKey, stObj)

	return obj
}

// addListenerState 添加监听器（Node 语义：先发 newListener，再追加，超限警告）。
func addListenerState(state *emitterState, args []engine.Value) {
	if len(args) < 2 {
		return
	}
	event := args[0]
	listener := args[1]
	// Node 语义：先发 'newListener' 事件（用当前快照，追加的监听器不参与本次分发，
	// 因此给 'newListener' 自身加监听器不会无限递归）。
	if nl := state.listeners["newListener"]; len(nl) > 0 {
		_ = callListeners(nl, []engine.Value{event, listener})
	}
	state.appendListener(event, listener)
	// maxListeners 警告。
	n := state.countListeners(event)
	if state.maxListeners > 0 && n > state.maxListeners {
		warnMaxListeners(eventNameString(event), n, state.maxListeners)
	}
}

// removeListenerState 移除监听器（Node 语义：移除后发 removeListener 事件）。
func removeListenerState(state *emitterState, args []engine.Value) {
	if len(args) < 2 {
		return
	}
	event := args[0]
	target := args[1]
	if state.removeListenerValue(event, target) {
		if rl := state.listeners["removeListener"]; len(rl) > 0 {
			_ = callListeners(rl, []engine.Value{event, target})
		}
	}
}

// eventNameString 事件名的字符串形式（Symbol 用描述）。
func eventNameString(ev engine.Value) string {
	if ev.Type() == engine.TypeSymbol {
		return ev.String()
	}
	return ev.String()
}

// callEmitterMethod 在一个 emitter 实例上调用指定方法。
func CallEmitterMethod(emitter engine.Value, method string, args []engine.Value) (engine.Value, error) {
	o, ok := emitter.AsObject()
	if !ok {
		return engine.Undefined(), fmt.Errorf("EventEmitter.%s: first argument must be an EventEmitter", method)
	}
	fn, err := o.Get(method)
	if err != nil || !fn.IsFunction() {
		return engine.Undefined(), fmt.Errorf("EventEmitter.%s: object has no method %q", method, method)
	}
	f, _ := fn.AsFunction()
	return f.Call(args)
}

// makeEventEmitterAsyncResource 构造 EventEmitterAsyncResource 类（API 面）。
func makeEventEmitterAsyncResource(ctx engine.Context) engine.Value {
	proto := engine.NewObject()
	_ = proto.Set("emitDestroy", engine.NewFunction("emitDestroy", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	ctor := engine.NewFunction("EventEmitterAsyncResource", func(args []engine.Value) (engine.Value, error) {
		inst := newEmitterInstanceOpts(nil).(engine.Object)
		for _, k := range proto.Keys() {
			if v, err := proto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		return inst, nil
	})
	if co, ok := ctor.AsObject(); ok {
		_ = co.Set("prototype", proto)
	}
	return ctor
}

// eventsOnceModule 实现 events.once(emitter, name[, options]) → Promise<args[]>。
func eventsOnceModule(args []engine.Value) (engine.Value, error) {
	if len(args) < 2 {
		return engine.Undefined(), fmt.Errorf("events.once: requires emitter and event name")
	}
	emitter := args[0]
	event := args[1]
	promiseCtor, err := eventsCtx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("events.once: Promise not available")
	}
	executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
		if len(ea) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := ea[0], ea[1]
		var argsArr []engine.Value
		resolveCb := engine.NewFunction("onceResolve", func(ca []engine.Value) (engine.Value, error) {
			argsArr = append([]engine.Value{}, ca...)
			if f, ok := resolve.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.NewArray(argsArr)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			return engine.Undefined(), nil
		})
		errorCb := engine.NewFunction("onceError", func(ca []engine.Value) (engine.Value, error) {
			if len(ca) > 0 {
				if f, ok := reject.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{ca[0]}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			return engine.Undefined(), nil
		})
		// once 监听器：触发即 resolve。
		_, _ = CallEmitterMethod(emitter, "once", []engine.Value{event, resolveCb})
		// error 路径：error 事件 reject（仅当未被 resolve 消费）。
		if o, ok := emitter.AsObject(); ok {
			if _, err := o.Get("on"); err == nil {
				_, _ = CallEmitterMethod(emitter, "on", []engine.Value{engine.Str("error"), errorCb})
			}
		}
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("events.once: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// eventsOnModule 实现 events.on(emitter, name[, options]) → AsyncIterator。
// 支持 for await...of：事件经 'data' 队列缓冲，无缓冲时返回挂起 Promise；
// 源发 'end' 事件或调用 iterator.return() 时结束（done:true）。
func eventsOnModule(args []engine.Value) (engine.Value, error) {
	if len(args) < 2 {
		return engine.Undefined(), fmt.Errorf("events.on: requires emitter and event name")
	}
	emitter := args[0]
	event := args[1]

	iterObj := engine.NewObject()
	queue := make([]engine.Value, 0)
	waiters := make([]engine.Value, 0)
	started := false
	ended := false

	dataCb := engine.NewFunction("evData", func(ca []engine.Value) (engine.Value, error) {
		arr := engine.NewArray(append([]engine.Value{}, ca...))
		if len(waiters) > 0 {
			w := waiters[0]
			waiters = waiters[1:]
			if f, ok := w.AsFunction(); ok {
				res := engine.NewObject()
				_ = res.Set("done", engine.Boolean(false))
				_ = res.Set("value", arr)
				if _, err := f.Call([]engine.Value{res}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		} else {
			queue = append(queue, arr)
		}
		return engine.Undefined(), nil
	})
	endCb := engine.NewFunction("evEnd", func(ca []engine.Value) (engine.Value, error) {
		ended = true
		for len(waiters) > 0 {
			w := waiters[0]
			waiters = waiters[1:]
			if f, ok := w.AsFunction(); ok {
				res := engine.NewObject()
				_ = res.Set("done", engine.Boolean(true))
				if _, err := f.Call([]engine.Value{res}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return engine.Undefined(), nil
	})

	nextFn := engine.NewFunction("next", func(ca []engine.Value) (engine.Value, error) {
		if !started {
			started = true
			_, _ = CallEmitterMethod(emitter, "on", []engine.Value{event, dataCb})
			_, _ = CallEmitterMethod(emitter, "on", []engine.Value{engine.Str("end"), endCb})
		}
		if len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			res := engine.NewObject()
			_ = res.Set("done", engine.Boolean(false))
			_ = res.Set("value", v)
			return res, nil
		}
		if ended {
			res := engine.NewObject()
			_ = res.Set("done", engine.Boolean(true))
			return res, nil
		}
		// 挂起：返回 Promise，resolve 存入 waiters，事件到达时唤醒。
		return pendingNextPromise(eventsCtx, &waiters)
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set("return", engine.NewFunction("return", func(ca []engine.Value) (engine.Value, error) {
		ended = true
		_, _ = CallEmitterMethod(emitter, "off", []engine.Value{event, dataCb})
		res := engine.NewObject()
		_ = res.Set("done", engine.Boolean(true))
		return res, nil
	}))
	_ = iterObj.Set(engine.SymbolAsyncIterator.SymbolKey(), engine.NewFunction("__asyncIterator", func(ca []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj, nil
}

// pendingNextPromise 构造挂起的 next Promise，resolve 函数存入 waiters。
func pendingNextPromise(ctx engine.Context, waiters *[]engine.Value) (engine.Value, error) {
	if ctx == nil {
		return engine.Undefined(), fmt.Errorf("events.on: no context for pending next")
	}
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("events.on: Promise not available")
	}
	executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
		if len(ea) >= 2 {
			*waiters = append(*waiters, ea[0])
		}
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("events.on: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}
