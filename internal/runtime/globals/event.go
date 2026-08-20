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
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// EventConfig 配置 Event 全局（当前无可用选项）。
type EventConfig struct{}

// 各类实例原型（instanceof 支持）。
var (
	eventProto        engine.Object
	eventTargetProto  engine.Object
	eventTargetCtor   engine.Value
	customEventProto  engine.Object
	messageEventProto engine.Object
)

// eventTargetProtoOnce 保证 EventTarget 原型只构建一次（跨注册顺序可用）。
var eventTargetProtoOnce sync.Once

// ensureEventTargetProto 惰性构建 EventTarget 原型与构造器：AbortSignal /
// WebSocket 等在 NewEvent 之前注册时，newEventTargetInstance 也能挂到完整
// 原型。上下文相关部分（%Object.prototype% 链接、全局注册）在 NewEvent 补。
func ensureEventTargetProto() {
	eventTargetProtoOnce.Do(func() {
		proto := engine.NewObject()
		ctor := engine.NewFunction("EventTarget", func(args []engine.Value) (engine.Value, error) {
			return newEventTargetInstance(), nil
		})
		// WebIDL：constructor 不可枚举；ctor.prototype 不可写/枚举/配置。
		_ = proto.Set("constructor", ctor)
		_ = engine.DefineOwnProperty(proto, "constructor", engine.Descriptor{HasEnumerable: true, Enumerable: false})
		if co, ok := ctor.AsObject(); ok {
			_ = co.Set("prototype", proto)
			_ = engine.DefineOwnProperty(co, "prototype", engine.Descriptor{
				HasWritable: true, Writable: false,
				HasEnumerable: true, Enumerable: false,
				HasConfigurable: true, Configurable: false,
			})
		}
		installEventTargetMethods(proto)
		eventTargetProto = proto
		eventTargetCtor = ctor
	})
}

// installEventTargetMethods 把 add/remove/dispatchEvent 挂到原型（可枚举，
// WebIDL 成员语义）。内部 listeners 状态存 Symbol 键槽位（对外不可见），
// 方法经 this 读写。
func installEventTargetMethods(proto engine.Object) {
	_ = proto.Set("addEventListener", interpreter.NewNativeMethod("addEventListener", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		state := eventTargetStateOf(this)
		if state == nil {
			state = make(map[string][]eventListener)
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
		// WHATWG 语义：同一 (type, listener, capture) 去重——先移除再追加。
		var out []eventListener
		for _, old := range state[eventType] {
			if !sameListener(old, l) {
				out = append(out, old)
			}
		}
		state[eventType] = append(out, l)
		saveEventTargetListeners(this, state)
		return engine.Undefined(), nil
	}))
	_ = proto.Set("removeEventListener", interpreter.NewNativeMethod("removeEventListener", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		state := eventTargetStateOf(this)
		if state == nil || len(args) < 2 {
			return engine.Undefined(), nil
		}
		eventType := args[0].String()
		target := toEventListener(args[1])
		var out []eventListener
		for _, l := range state[eventType] {
			if !sameListener(l, target) {
				out = append(out, l)
			}
		}
		state[eventType] = out
		saveEventTargetListeners(this, state)
		return engine.Undefined(), nil
	}))
	_ = proto.Set("dispatchEvent", interpreter.NewNativeMethod("dispatchEvent", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		state := eventTargetStateOf(this)
		ev := args[0]
		if evObj, ok := ev.AsObject(); ok {
			_ = evObj.Set("target", this)
			_ = evObj.Set("currentTarget", this)
		}
		eventType := ""
		if evObj, ok := ev.AsObject(); ok {
			if t, err := evObj.Get("type"); err == nil {
				eventType = t.String()
			}
		}
		if state != nil {
			ls := state[eventType]
			snapshot := make([]eventListener, len(ls))
			copy(snapshot, ls)
			for _, l := range snapshot {
				if l.once {
					state = removeListenerOnceIn(state, eventType, l)
				}
				dispatchToListener(l, ev)
			}
			if len(ls) > 0 {
				saveEventTargetListeners(this, state)
			}
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
}

// NewEvent 注册全局 Event / EventTarget / CustomEvent / MessageEvent 构造器。
func NewEvent(ctx engine.Context, cfg EventConfig) error {
	// EventTarget 原型与方法惰性构建（ensureEventTargetProto）：AbortSignal /
	// WebSocket 等在 NewEvent 之前注册时也能得到完整原型。此处补上下文相关
	// 部分：原型链接 %Object.prototype% 并注册全局构造器。
	ensureEventTargetProto()
	engine.SetProto(eventTargetProto, ctx.ObjectPrototype())
	if err := ctx.Global().Set("EventTarget", eventTargetCtor); err != nil {
		return err
	}

	evCtor := engine.NewFunction("Event", func(args []engine.Value) (engine.Value, error) {
		return newEventInstance(args), nil
	})
	evObj, _ := evCtor.AsObject()
	eventProto = engine.NewObject()
	_ = eventProto.Set("constructor", evCtor)
	_ = evObj.Set("prototype", eventProto)

	customCtor := engine.NewFunction("CustomEvent", func(args []engine.Value) (engine.Value, error) {
		ev := newEventInstance(args)
		if customEventProto != nil {
			engine.SetProto(ev, customEventProto)
		}
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
	})
	customObj, _ := customCtor.AsObject()
	customEventProto = engine.NewObject()
	if eventProto != nil {
		engine.SetProto(customEventProto, eventProto)
	}
	_ = customEventProto.Set("constructor", customCtor)
	_ = customObj.Set("prototype", customEventProto)

	meCtor := engine.NewFunction("MessageEvent", func(args []engine.Value) (engine.Value, error) {
		return newMessageEventInstance(args), nil
	})
	meObj, _ := meCtor.AsObject()
	messageEventProto = engine.NewObject()
	if eventProto != nil {
		engine.SetProto(messageEventProto, eventProto)
	}
	_ = messageEventProto.Set("constructor", meCtor)
	_ = meObj.Set("prototype", messageEventProto)

	// EventTarget 构造器已由 RegisterInterface 注册为全局。
	if err := ctx.Global().Set("Event", evCtor); err != nil {
		return err
	}
	if err := ctx.Global().Set("CustomEvent", customCtor); err != nil {
		return err
	}
	return ctx.Global().Set("MessageEvent", meCtor)
}

// newMessageEventInstance 构造 MessageEvent(type[, init])。
func newMessageEventInstance(args []engine.Value) engine.Value {
	ev := newEventInstance(args)
	if messageEventProto != nil {
		engine.SetProto(ev, messageEventProto)
	}
	if evObj, ok := ev.AsObject(); ok {
		// 默认值（WHATWG 语义）。
		_ = evObj.Set("data", engine.Null())
		_ = evObj.Set("origin", engine.Str(""))
		_ = evObj.Set("lastEventId", engine.Str(""))
		_ = evObj.Set("source", engine.Null())
		_ = evObj.Set("ports", engine.NewArray(nil))
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				for _, k := range []string{"data", "origin", "lastEventId", "source", "ports"} {
					if v, err := o.Get(k); err == nil && !v.IsUndefined() {
						_ = evObj.Set(k, v)
					}
				}
			}
		}
	}
	return ev
}

// newEventInstance 构造 Event 对象。new Event(type[, options])。
func newEventInstance(args []engine.Value) engine.Value {
	ev := engine.NewObject()
	if eventProto != nil {
		engine.SetProto(ev, eventProto)
	}
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
				if v, err := o.Get(k); err == nil && !v.IsUndefined() {
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
	fn   engine.Value  // 函数监听器
	obj  engine.Object // handleEvent 对象监听器
	once bool
}

// eventTargetListenersSlot 是 EventTarget 内部监听状态的 Symbol 键槽位
// （own keys / for-in / getOwnPropertyNames 均不可见，对齐 Node）。槽位值是
// JS 对象 { "<type>": [{fn, obj, once}, ...] }，原型方法经 this 读写。
var eventTargetListenersSlot = engine.NewSymbol("aluka.EventTarget.listeners")

// eventTargetAddListener 是 addEventListener 的 Go 直调版（Go 侧内部触发
// 注册用；NativeMethod.Call 无 this 绑定，Go 调用方不能走 JS 方法路径）。
func eventTargetAddListener(target engine.Value, eventType string, l eventListener) {
	state := eventTargetStateOf(target)
	if state == nil {
		state = make(map[string][]eventListener)
	}
	var out []eventListener
	for _, old := range state[eventType] {
		if !sameListener(old, l) {
			out = append(out, old)
		}
	}
	state[eventType] = append(out, l)
	saveEventTargetListeners(target, state)
}

// eventTargetDispatch 是 dispatchEvent 的 Go 直调版（Go 侧触发事件用），
// 返回 !defaultPrevented。
func eventTargetDispatch(target engine.Value, ev engine.Value) bool {
	state := eventTargetStateOf(target)
	if evObj, ok := ev.AsObject(); ok {
		_ = evObj.Set("target", target)
		_ = evObj.Set("currentTarget", target)
	}
	eventType := ""
	if evObj, ok := ev.AsObject(); ok {
		if t, err := evObj.Get("type"); err == nil {
			eventType = t.String()
		}
	}
	if state != nil {
		ls := state[eventType]
		snapshot := make([]eventListener, len(ls))
		copy(snapshot, ls)
		for _, l := range snapshot {
			if l.once {
				state = removeListenerOnceIn(state, eventType, l)
			}
			dispatchToListener(l, ev)
		}
		if len(ls) > 0 {
			saveEventTargetListeners(target, state)
		}
	}
	if evObj, ok := ev.AsObject(); ok {
		if v, err := evObj.Get("defaultPrevented"); err == nil {
			if b, ok := v.Bool(); ok {
				return !b
			}
		}
	}
	return true
}

// eventTargetListenersOf 读取 this 的监听状态（无槽位返回 nil）。
func eventTargetListenersOf(this engine.Value) map[string][]eventListener {
	o, ok := this.AsObject()
	if !ok {
		return nil
	}
	v, err := o.Get(eventTargetListenersSlot.SymbolKey())
	if err != nil || !v.IsObject() {
		return nil
	}
	holder, _ := v.AsObject()
	out := make(map[string][]eventListener)
	for _, typ := range holder.Keys() {
		arrV, _ := holder.Get(typ)
		if arrV == nil || !arrV.IsObject() {
			continue
		}
		if a, ok := arrV.(*engine.ArrayValue); ok {
			for _, w := range a.Elems() {
				wo, ok := w.AsObject()
				if !ok {
					continue
				}
				var l eventListener
				if fn, err := wo.Get("fn"); err == nil && fn.IsFunction() {
					l.fn = fn
				}
				if obj, err := wo.Get("obj"); err == nil && obj.IsObject() {
					if oo, ok := obj.AsObject(); ok {
						l.obj = oo
					}
				}
				if once, err := wo.Get("once"); err == nil {
					if b, ok := once.Bool(); ok {
						l.once = b
					}
				}
				out[typ] = append(out[typ], l)
			}
		}
	}
	return out
}

// saveEventTargetListeners 把监听状态写回 this 的槽位（整体重写，读多写少
// 的低频路径；事件类型间无顺序语义）。
func saveEventTargetListeners(this engine.Value, m map[string][]eventListener) {
	o, ok := this.AsObject()
	if !ok {
		return
	}
	holder := engine.NewObject()
	for typ, ls := range m {
		vals := make([]engine.Value, len(ls))
		for i, l := range ls {
			w := engine.NewObject()
			if l.fn != nil {
				_ = w.Set("fn", l.fn)
			} else {
				_ = w.Set("fn", engine.Undefined())
			}
			if l.obj != nil {
				_ = w.Set("obj", l.obj)
			} else {
				_ = w.Set("obj", engine.Undefined())
			}
			_ = w.Set("once", engine.Boolean(l.once))
			vals[i] = w
		}
		_ = holder.Set(typ, engine.NewArray(vals))
	}
	_ = o.Set(eventTargetListenersSlot.SymbolKey(), holder)
}

// eventTargetStateOf 兼容别名：原型方法内以 map 形式取状态。
func eventTargetStateOf(this engine.Value) map[string][]eventListener {
	return eventTargetListenersOf(this)
}

// newEventTargetInstance 构造 EventTarget 实例：自有键为空，方法经
// EventTarget.prototype 继承，监听状态存 Symbol 键槽位。
func newEventTargetInstance() engine.Value {
	ensureEventTargetProto()
	et := engine.NewObject()
	engine.SetProto(et, eventTargetProto)
	_ = et.Set(eventTargetListenersSlot.SymbolKey(), engine.NewObject())
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

// dispatchToListener 调用监听器（函数或 handleEvent）。监听器抛错上报为
// uncaughtException（Node 语义：EventTarget 监听器异常即 uncaughtException）。
// ctx 传 nil：监听器抛的是 *jsThrow，ReportUncaught 直接透传其 JS 值。
func dispatchToListener(l eventListener, ev engine.Value) {
	if l.fn != nil {
		if f, ok := l.fn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{ev}); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
		return
	}
	if l.obj != nil {
		if h, err := l.obj.Get("handleEvent"); err == nil && h.IsFunction() {
			if f, ok := h.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{ev}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
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

// removeListenerOnceIn 是 removeListenerOnce 的 map 形态（原型方法路径）。
func removeListenerOnceIn(state map[string][]eventListener, eventType string, target eventListener) map[string][]eventListener {
	var out []eventListener
	for _, l := range state[eventType] {
		if !sameListener(l, target) {
			out = append(out, l)
		}
	}
	state[eventType] = out
	return state
}
