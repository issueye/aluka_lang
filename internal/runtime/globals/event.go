package globals

// Web 事件 API：Event / EventTarget / CustomEvent（开发计划 2.8）。
//
// 实现要点：
//   - EventTarget 以闭包持有 listeners map（type → []listener），监听器可为
//     函数或含 handleEvent 方法的对象；dispatchEvent 同步遍历调用。
//   - Event 对象构造时写入 type/bubbles/cancelable/composed 字段；
//     preventDefault() 设置 defaultPrevented。
//   - CustomEvent 在 Event 基础上附加 detail 字段。
//   - 实例方法/状态均以闭包捕获（绕过 engine.Func 无 this 绑定限制）。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// EventConfig 配置 Event 全局（当前无可用选项）。
type EventConfig struct{}

// NewEvent 注册全局 Event / EventTarget / CustomEvent 构造器。
func NewEvent(ctx engine.Context, cfg EventConfig) error {
	_ = ctx.Global().Set("EventTarget", engine.NewFunction("EventTarget", func(args []engine.Value) (engine.Value, error) {
		return newEventTargetInstance(), nil
	}))
	_ = ctx.Global().Set("Event", engine.NewFunction("Event", func(args []engine.Value) (engine.Value, error) {
		return newEventInstance(args), nil
	}))
	_ = ctx.Global().Set("CustomEvent", engine.NewFunction("CustomEvent", func(args []engine.Value) (engine.Value, error) {
		ev := newEventInstance(args)
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if detail, err := o.Get("detail"); err == nil {
					if evObj, ok := ev.AsObject(); ok {
						_ = evObj.Set("detail", detail)
					}
				}
			}
		}
		return ev, nil
	}))
	return nil
}

// newEventInstance 构造 Event 对象。new Event(type[, options])。
func newEventInstance(args []engine.Value) engine.Value {
	ev := engine.NewObject()
	eventType := ""
	if len(args) > 0 {
		eventType = args[0].String()
	}
	_ = ev.Set("type", engine.Str(eventType))
	_ = ev.Set("bubbles", engine.Boolean(false))
	_ = ev.Set("cancelable", engine.Boolean(false))
	_ = ev.Set("composed", engine.Boolean(false))
	_ = ev.Set("defaultPrevented", engine.Boolean(false))
	_ = ev.Set("target", engine.Null())
	_ = ev.Set("currentTarget", engine.Null())
	_ = ev.Set("eventPhase", engine.IntValue(0))
	_ = ev.Set("timeStamp", engine.Number(0))
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			for _, k := range []string{"bubbles", "cancelable", "composed"} {
				if v, err := o.Get(k); err == nil {
					_ = ev.Set(k, v)
				}
			}
		}
	}

	// preventDefault()：设置 defaultPrevented。
	_ = ev.Set("preventDefault", engine.NewFunction("preventDefault", func(a []engine.Value) (engine.Value, error) {
		_ = ev.Set("defaultPrevented", engine.Boolean(true))
		return engine.Undefined(), nil
	}))
	// stopPropagation() / stopImmediatePropagation()：no-op（无冒泡模型）。
	noop := engine.NewFunction("stopPropagation", func(a []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	})
	_ = ev.Set("stopPropagation", noop)
	_ = ev.Set("stopImmediatePropagation", noop)

	return ev
}

// eventTargetState 是 EventTarget 实例的内部状态。
type eventTargetState struct {
	listeners map[string][]eventListener
}

// eventListener 是监听器包装（函数或 {handleEvent} 对象）。
type eventListener struct {
	fn  engine.Value  // 函数监听器
	obj engine.Object // handleEvent 对象监听器
	once bool
}

// newEventTargetInstance 构造 EventTarget 实例。
func newEventTargetInstance() engine.Value {
	et := engine.NewObject()
	state := &eventTargetState{listeners: make(map[string][]eventListener)}

	// addEventListener(type, listener[, options])
	_ = et.Set("addEventListener", engine.NewFunction("addEventListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		eventType := args[0].String()
		l := toEventListener(args[1])
		if l.fn == nil && l.obj == nil {
			return engine.Undefined(), nil
		}
		if len(args) > 2 && args[2].IsObject() {
			if o, ok := args[2].AsObject(); ok {
				if once, err := o.Get("once"); err == nil {
					if b, ok := once.Bool(); ok && b {
						l.once = true
					}
				}
			}
		}
		state.listeners[eventType] = append(state.listeners[eventType], l)
		return engine.Undefined(), nil
	}))

	// removeEventListener(type, listener[, options])
	_ = et.Set("removeEventListener", engine.NewFunction("removeEventListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		eventType := args[0].String()
		target := toEventListener(args[1])
		ls := state.listeners[eventType]
		out := ls[:0]
		for _, l := range ls {
			if !sameListener(l, target) {
				out = append(out, l)
			}
		}
		state.listeners[eventType] = out
		return engine.Undefined(), nil
	}))

	// dispatchEvent(event)：返回 false 若 preventDefault 被调用。
	_ = et.Set("dispatchEvent", engine.NewFunction("dispatchEvent", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		ev := args[0]
		if evObj, ok := ev.AsObject(); ok {
			_ = evObj.Set("target", et)
			_ = evObj.Set("currentTarget", et)
		}
		eventType := ""
		if evObj, ok := ev.AsObject(); ok {
			if t, err := evObj.Get("type"); err == nil {
				eventType = t.String()
			}
		}
		ls := state.listeners[eventType]
		snapshot := make([]eventListener, len(ls))
		copy(snapshot, ls)
		for _, l := range snapshot {
			if l.once {
				removeListenerOnce(state, eventType, l)
			}
			dispatchToListener(l, ev)
		}
		// 返回 !defaultPrevented。
		if evObj, ok := ev.AsObject(); ok {
			if v, err := evObj.Get("defaultPrevented"); err == nil {
				if b, ok := v.Bool(); ok {
					return engine.Boolean(!b), nil
				}
			}
		}
		return engine.Boolean(true), nil
	}))

	return et
}

// toEventListener 将 JS 值转为 eventListener（函数或 {handleEvent}）。
func toEventListener(v engine.Value) eventListener {
	if v.IsFunction() {
		return eventListener{fn: v}
	}
	if v.IsObject() {
		if o, ok := v.AsObject(); ok {
			return eventListener{obj: o}
		}
	}
	return eventListener{}
}

// sameListener 判断两个监听器是否同一（函数引用或对象引用）。
func sameListener(a, b eventListener) bool {
	if a.fn != nil && b.fn != nil {
		return a.fn == b.fn
	}
	return a.obj != nil && b.obj != nil && a.obj == b.obj
}

// dispatchToListener 调用监听器（函数或 handleEvent）。
func dispatchToListener(l eventListener, ev engine.Value) {
	if l.fn != nil {
		if f, ok := l.fn.AsFunction(); ok {
			_, _ = f.Call([]engine.Value{ev})
		}
		return
	}
	if l.obj != nil {
		if h, err := l.obj.Get("handleEvent"); err == nil && h.IsFunction() {
			if f, ok := h.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{ev})
			}
		}
	}
}

// removeListenerOnce 移除一次性监听器（dispatch 时自删）。
func removeListenerOnce(state *eventTargetState, eventType string, target eventListener) {
	ls := state.listeners[eventType]
	out := ls[:0]
	for _, l := range ls {
		if !sameListener(l, target) {
			out = append(out, l)
		}
	}
	state.listeners[eventType] = out
}
