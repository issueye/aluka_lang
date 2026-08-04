package builtin

// node:events 内置模块——提供 EventEmitter。
//
// 实现方案：EventEmitter 是一个构造器（支持 new EventEmitter()）。
// 构造时在闭包中创建 listeners map，并构造一个含 on/emit/off 等方法的对象。
// 方法不依赖 JS this 绑定（绕过 engine.Func 无 this 参数的限制），
// 而是通过闭包捕获实例状态。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// defaultMaxListeners 是 EventEmitter 的默认最大监听器数。
const defaultMaxListeners = 10

// NewEvents 构造 node:events 模块的导出对象。
func NewEvents(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// EventEmitter 构造器：new EventEmitter() 返回一个 emitter 实例对象。
	ctor := engine.NewFunction("EventEmitter", func(args []engine.Value) (engine.Value, error) {
		return newEmitterInstance(), nil
	})
	ctorObj, _ := ctor.AsObject()
	// 设置 prototype 属性（支持 instanceof 与继承）。
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	_ = ctorObj.Set("prototype", proto)
	_ = ctorObj.Set("defaultMaxListeners", engine.IntValue(defaultMaxListeners))

	// 模块级静态方法（emitter 作为第一个参数）。
	_ = ctorObj.Set("once", engine.NewFunction("once", makeStaticEmitterMethod("once")))
	_ = ctorObj.Set("on", engine.NewFunction("on", makeStaticEmitterMethod("on")))
	_ = ctorObj.Set("off", engine.NewFunction("off", makeStaticEmitterMethod("off")))
	_ = ctorObj.Set("listenerCount", engine.NewFunction("listenerCount", makeStaticEmitterMethod("listenerCount")))

	_ = m.Set("EventEmitter", ctor)
	return m, nil
}

// makeStaticEmitterMethod 构造模块级静态方法的 engine.Func
// （接受 emitter 作为第一个参数，转发到实例方法）。
func makeStaticEmitterMethod(method string) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("EventEmitter.%s: requires emitter as first argument", method)
		}
		return callEmitterMethod(args[0], method, args[1:])
	}
}

// emitterState 是一个 EventEmitter 实例的内部状态。
type emitterState struct {
	listeners     map[string][]engine.Value // event → listeners
	maxListeners  int
}

// newEmitterInstance 构造一个 EventEmitter 实例对象。
// 所有方法通过闭包捕获 state 实现状态隔离，不依赖 JS this 绑定。
func newEmitterInstance() engine.Value {
	obj := engine.NewObject()

	state := &emitterState{
		listeners:    make(map[string][]engine.Value),
		maxListeners: defaultMaxListeners,
	}

	// on(event, listener)：注册监听器，返回 emitter（链式）。
	onFn := engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		addListener(state, args, false)
		return obj, nil
	})
	_ = obj.Set("on", onFn)
	_ = obj.Set("addListener", onFn)

	// once(event, listener)：注册一次性监听器（用 wrapper 在触发后自删）。
	onceFn := engine.NewFunction("once", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0].String()
			original := args[1]
			// 创建 wrapper 并注册；wrapper 触发时调 original 并精确删除自身。
			var wrapper engine.Value
				wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
					// 调用原始监听器。
					if f, ok := original.AsFunction(); ok {
						_, _ = f.Call(callArgs)
					}
					// 精确删除 wrapper 自身。
					listeners := state.listeners[event]
					for i, l := range listeners {
						// 闭包捕获 wrapper 变量（var 声明在闭包外），触发后删除自身。
						if l == wrapper {
							state.listeners[event] = append(listeners[:i], listeners[i+1:]...)
							break
						}
					}
					return engine.Undefined(), nil
				})
				state.listeners[event] = append(state.listeners[event], wrapper)
		}
		return obj, nil
	})
	_ = obj.Set("once", onceFn)

	// off / removeListener / removeEventListener(event, listener)
	offFn := engine.NewFunction("off", func(args []engine.Value) (engine.Value, error) {
		removeListener(state, args)
		return obj, nil
	})
	_ = obj.Set("off", offFn)
	_ = obj.Set("removeListener", offFn)
	_ = obj.Set("removeEventListener", offFn)

	// emit(event, ...args)：触发事件，返回是否有监听器。
	emitFn := engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		event := args[0].String()
		listeners := state.listeners[event]
		if len(listeners) == 0 {
			return engine.Boolean(false), nil
		}
		// 复制一份避免遍历时修改（once wrapper 会删除自身）。
		snapshot := make([]engine.Value, len(listeners))
		copy(snapshot, listeners)
		callArgs := args[1:]
		for _, fn := range snapshot {
			if f, ok := fn.AsFunction(); ok {
				_, _ = f.Call(callArgs)
			}
		}
		return engine.Boolean(true), nil
	})
	_ = obj.Set("emit", emitFn)

	// listeners(event)：返回事件监听器数组。
	_ = obj.Set("listeners", engine.NewFunction("listeners", func(args []engine.Value) (engine.Value, error) {
		event := ""
		if len(args) > 0 {
			event = args[0].String()
		}
		return engine.NewArray(append([]engine.Value{}, state.listeners[event]...)), nil
	}))

	// listenerCount(event)：返回监听器数量。
	_ = obj.Set("listenerCount", engine.NewFunction("listenerCount", func(args []engine.Value) (engine.Value, error) {
		event := ""
		if len(args) > 0 {
			event = args[0].String()
		}
		return engine.IntValue(len(state.listeners[event])), nil
	}))

	// eventNames()：返回所有已注册事件名数组。
	_ = obj.Set("eventNames", engine.NewFunction("eventNames", func(args []engine.Value) (engine.Value, error) {
		names := make([]engine.Value, 0, len(state.listeners))
		for name := range state.listeners {
			names = append(names, engine.Str(name))
		}
		return engine.NewArray(names), nil
	}))

	// removeAllListeners([event])
	_ = obj.Set("removeAllListeners", engine.NewFunction("removeAllListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || args[0].IsUndefined() {
			state.listeners = make(map[string][]engine.Value)
		} else {
			delete(state.listeners, args[0].String())
		}
		return obj, nil
	}))

	// setMaxListeners(n)
	_ = obj.Set("setMaxListeners", engine.NewFunction("setMaxListeners", func(args []engine.Value) (engine.Value, error) {
		state.maxListeners = intArg(args, 0, defaultMaxListeners)
		return obj, nil
	}))

	// getMaxListeners()
	_ = obj.Set("getMaxListeners", engine.NewFunction("getMaxListeners", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(state.maxListeners), nil
	}))

	// prependListener(event, listener)：在头部插入。
	prependFn := engine.NewFunction("prependListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return obj, nil
		}
		event := args[0].String()
		state.listeners[event] = append([]engine.Value{args[1]}, state.listeners[event]...)
		return obj, nil
	})
	_ = obj.Set("prependListener", prependFn)

	// prependOnceListener
	_ = obj.Set("prependOnceListener", engine.NewFunction("prependOnceListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0].String()
			original := args[1]
			var wrapper engine.Value
			wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
				if f, ok := original.AsFunction(); ok {
					_, _ = f.Call(callArgs)
				}
				listeners := state.listeners[event]
				for i, l := range listeners {
					if l == wrapper {
						state.listeners[event] = append(listeners[:i], listeners[i+1:]...)
						break
					}
				}
				return engine.Undefined(), nil
			})
			state.listeners[event] = append([]engine.Value{wrapper}, state.listeners[event]...)
		}
		return obj, nil
	}))

	// rawListeners(event)：同 listeners（简化）。
	_ = obj.Set("rawListeners", engine.NewFunction("rawListeners", func(args []engine.Value) (engine.Value, error) {
		event := ""
		if len(args) > 0 {
			event = args[0].String()
		}
		return engine.NewArray(append([]engine.Value{}, state.listeners[event]...)), nil
	}))

	return obj
}

// addListener 添加监听器（公共逻辑）。
func addListener(state *emitterState, args []engine.Value, once bool) {
	if len(args) < 2 {
		return
	}
	event := args[0].String()
	state.listeners[event] = append(state.listeners[event], args[1])
}

// removeListener 从事件中移除指定监听器。
func removeListener(state *emitterState, args []engine.Value) {
	if len(args) < 2 {
		return
	}
	event := args[0].String()
	target := args[1]
	listeners := state.listeners[event]
	for i, l := range listeners {
		if l == target {
			state.listeners[event] = append(listeners[:i], listeners[i+1:]...)
			return
		}
	}
}

// callEmitterMethod 在一个 emitter 实例上调用指定方法。
func callEmitterMethod(emitter engine.Value, method string, args []engine.Value) (engine.Value, error) {
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
