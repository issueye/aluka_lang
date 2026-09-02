package nodeevents

// EventEmitter.prototype 上的 this 感知方法（供 mixin 使用，如 express 的 app）。
//
// 这些方法通过 `this` 定位实例状态（stored under emitterStateKey），因此可被
// `mixin(app, EventEmitter.prototype)` 拷贝到任意对象使用。状态以 JS 对象组织，
// 随实例参与 GC。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// getProtoState 返回实例的事件状态对象，不存在时惰性创建并挂到实例上。
func getProtoState(this engine.Value) (engine.Object, error) {
	o, ok := this.AsObject()
	if !ok {
		return nil, fmt.Errorf("EventEmitter method called on non-object")
	}
	if st, err := o.Get(emitterStateKey); err == nil {
		if sobj, ok := st.(engine.Object); ok {
			return sobj, nil
		}
	}
	st := engine.NewObject()
	_ = st.Set("__list", engine.NewObject())
	_ = st.Set("__max", engine.IntValue(defaultMaxListeners))
	_ = o.Set(emitterStateKey, st)
	return st, nil
}

// protoList 返回事件状态中的监听器表对象（event → array）。
func protoList(st engine.Object) engine.Object {
	if l, _ := st.Get("__list"); l != nil {
		if lo, ok := l.(engine.Object); ok {
			return lo
		}
	}
	lo := engine.NewObject()
	_ = st.Set("__list", lo)
	return lo
}

// protoListeners 返回某事件当前的监听器切片。
func protoListeners(st engine.Object, event string) []engine.Value {
	list := protoList(st)
	if v, _ := list.Get(event); v != nil {
		if arr, ok := v.(*engine.ArrayValue); ok {
			return arr.Elems()
		}
	}
	return nil
}

// protoSetListeners 覆盖某事件的监听器数组。
func protoSetListeners(st engine.Object, event string, l []engine.Value) {
	_ = protoList(st).Set(event, engine.NewArray(l))
}

// protoMaxListeners 返回实例的最大监听器数。
func protoMaxListeners(st engine.Object) int {
	if v, _ := st.Get("__max"); v != nil {
		if n, ok := v.Int(); ok {
			return n
		}
	}
	return defaultMaxListeners
}

// registerEmitterPrototype 在 EventEmitter.prototype 上注册标准事件方法。
func registerEmitterPrototype(proto engine.Object) {
	// on(event, listener) / addListener：注册监听器。
	onFn := interpreter.NewNativeMethod("on", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) >= 2 {
			ev := args[0].String()
			protoSetListeners(st, ev, append(protoListeners(st, ev), args[1]))
		}
		return this, nil
	})
	_ = proto.Set("on", onFn)
	_ = proto.Set("addListener", onFn)

	// once(event, listener)：注册一次性监听器（触发后自删）。
	_ = proto.Set("once", interpreter.NewNativeMethod("once", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) >= 2 {
			ev := args[0].String()
			original := args[1]
			var wrapper engine.Value
			wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
				if f, ok := original.AsFunction(); ok {
					if _, err := f.Call(callArgs); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
				l := protoListeners(st, ev)
				for i, x := range l {
					if x == wrapper {
						protoSetListeners(st, ev, append(append([]engine.Value{}, l[:i]...), l[i+1:]...))
						break
					}
				}
				return engine.Undefined(), nil
			})
			protoSetListeners(st, ev, append(protoListeners(st, ev), wrapper))
		}
		return this, nil
	}))

	// emit(event, ...args)：触发事件。
	_ = proto.Set("emit", interpreter.NewNativeMethod("emit", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := args[0].String()
		snapshot := protoListeners(st, ev)
		if len(snapshot) == 0 {
			return engine.Boolean(false), nil
		}
		callArgs := args[1:]
		for _, fn := range snapshot {
			if f, ok := fn.AsFunction(); ok {
				if _, err := f.Call(callArgs); err != nil {
					return engine.Undefined(), err
				}
			}
		}
		return engine.Boolean(true), nil
	}))

	// off / removeListener / removeEventListener(event, listener)
	offFn := interpreter.NewNativeMethod("off", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return this, nil
		}
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := args[0].String()
		target := args[1]
		l := protoListeners(st, ev)
		for i, x := range l {
			if x == target {
				protoSetListeners(st, ev, append(append([]engine.Value{}, l[:i]...), l[i+1:]...))
				break
			}
		}
		return this, nil
	})
	_ = proto.Set("off", offFn)
	_ = proto.Set("removeListener", offFn)
	_ = proto.Set("removeEventListener", offFn)

	// listeners(event)：返回监听器数组拷贝。
	_ = proto.Set("listeners", interpreter.NewNativeMethod("listeners", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := ""
		if len(args) > 0 {
			ev = args[0].String()
		}
		return engine.NewArray(append([]engine.Value{}, protoListeners(st, ev)...)), nil
	}))

	// rawListeners(event)：同 listeners（简化）。
	_ = proto.Set("rawListeners", interpreter.NewNativeMethod("rawListeners", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := ""
		if len(args) > 0 {
			ev = args[0].String()
		}
		return engine.NewArray(append([]engine.Value{}, protoListeners(st, ev)...)), nil
	}))

	// listenerCount(event)：返回监听器数量。
	_ = proto.Set("listenerCount", interpreter.NewNativeMethod("listenerCount", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := ""
		if len(args) > 0 {
			ev = args[0].String()
		}
		return engine.IntValue(len(protoListeners(st, ev))), nil
	}))

	// eventNames()：返回所有已注册事件名数组。
	_ = proto.Set("eventNames", interpreter.NewNativeMethod("eventNames", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		list := protoList(st)
		var names []engine.Value
		for _, k := range list.Keys() {
			names = append(names, engine.Str(k))
		}
		return engine.NewArray(names), nil
	}))

	// removeAllListeners([event])
	_ = proto.Set("removeAllListeners", interpreter.NewNativeMethod("removeAllListeners", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) == 0 || args[0].IsUndefined() {
			_ = st.Set("__list", engine.NewObject())
		} else {
			_ = protoList(st).Delete(args[0].String())
		}
		return this, nil
	}))

	// setMaxListeners(n)
	_ = proto.Set("setMaxListeners", interpreter.NewNativeMethod("setMaxListeners", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		n := defaultMaxListeners
		if len(args) > 0 {
			if v, ok := args[0].Int(); ok {
				n = v
			}
		}
		_ = st.Set("__max", engine.IntValue(n))
		return this, nil
	}))

	// getMaxListeners()
	_ = proto.Set("getMaxListeners", interpreter.NewNativeMethod("getMaxListeners", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(protoMaxListeners(st)), nil
	}))

	// prependListener(event, listener)：在头部插入。
	prependFn := interpreter.NewNativeMethod("prependListener", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return this, nil
		}
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := args[0].String()
		protoSetListeners(st, ev, append([]engine.Value{args[1]}, protoListeners(st, ev)...))
		return this, nil
	})
	_ = proto.Set("prependListener", prependFn)

	// prependOnceListener(event, listener)
	_ = proto.Set("prependOnceListener", interpreter.NewNativeMethod("prependOnceListener", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return this, nil
		}
		st, err := getProtoState(this)
		if err != nil {
			return engine.Undefined(), err
		}
		ev := args[0].String()
		original := args[1]
		var wrapper engine.Value
		wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
			if f, ok := original.AsFunction(); ok {
				if _, err := f.Call(callArgs); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			l := protoListeners(st, ev)
			for i, x := range l {
				if x == wrapper {
					protoSetListeners(st, ev, append(append([]engine.Value{}, l[:i]...), l[i+1:]...))
					break
				}
			}
			return engine.Undefined(), nil
		})
		protoSetListeners(st, ev, append([]engine.Value{wrapper}, protoListeners(st, ev)...))
		return this, nil
	}))
}
