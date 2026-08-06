package builtin

// node:domain 内置模块——废弃（DEP0003）的 legacy 错误路由模块。
//
// 对齐 Node 22 的公开面：
//   - 导出 create / createDomain / Domain / active / _stack
//   - Domain 实例：run / bind / intercept / enter / exit / add / remove /
//     members + 一组 EventEmitter 方法（on/once/emit/removeListener/off/
//     removeAllListeners/listeners/rawListeners/listenerCount/eventNames/
//     setMaxListeners/getMaxListeners）
//   - process.domain / exports.active 随 enter/exit 更新（初始 null，exit 后
//     undefined——与 Node 一致）
//   - 错误路由基础：intercept 首参 Error 路由到 domain 'error' 事件；
//     domain.add(emitter) 后 emitter 的 'error' 事件路由到 domain。
//   - run/bind 回调抛错时【不】自动 exit（复刻 Node 语义：错误继续向调用方
//     传播，domain 保持 enter 状态——共享 stack 的已知污染行为）。
//
// 与 Node 的差异（knownDifference）：
//   - Node 通过 hook EventEmitter.prototype.emit 实现"任意已绑定 emitter 的
//     'error' 事件自动路由"，并让"domain 激活期间 new 的 emitter 自动绑定"。
//     aluka 不改 events.go（模块所有权），改为在 domain.add 时为 emitter 注册
//     内部 'error' 转发监听器，因此：非 add 绑定的 emitter 不会路由；
//     emitter 自身另有 'error' 监听器时 Node 跳过 domain，aluka 仍会转发；
//     listenerCount('error') 会多计 1。
//   - 顶层未捕获异常拦截（Node 的 uncaughtExceptionCaptureCallback 把同步
//     throw 在顶层路由到活动 domain）需改 engine 层，aluka 不做：run 内同步
//     throw 且 domain 有 'error' 监听器时，Node 会先调监听器再退出进程，
//     aluka 直接按未捕获异常处理。异步错误路由（setImmediate 内 throw）同样
//     依赖事件循环拦截，aluka 不实现。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// domainActiveState 承载模块级共享状态（Node 的模块级 stack / active）。
type domainActiveState struct {
	stack  []engine.Value // 已 enter 的 domain 栈
	active engine.Value   // 当前活动 domain（初始 null）
}

// domainEmitter 是 domain 实例的内部 EventEmitter 状态。
type domainEmitter struct {
	listeners   map[string][]engine.Value
	maxListeners int
}

// domainInstance 封装一个 Domain 实例的全部状态与方法。
type domainInstance struct {
	ctx    engine.Context
	self   engine.Value // 实例对象
	em     *domainEmitter
	st     *domainActiveState
	module engine.Object // 模块导出对象（更新 active 用）
	members []engine.Value
	// emitter -> 转发监听器（domain.add 注册的内部 'error' 转发）。
	forwarders map[engine.Value]engine.Value
}

// NewDomain 构造 node:domain 模块导出对象（DEP0003 废弃）。
func NewDomain(ctx engine.Context) (engine.Value, error) {
	emitDeprecation("DEP0003", "The domain module is deprecated. Use alternative error handling solutions instead.")

	st := &domainActiveState{active: engine.Null()}
	m := engine.NewObject()

	// create / createDomain：Node 中二者为同一函数（createDomain 别名）。
	createFn := engine.NewFunction("create", func(args []engine.Value) (engine.Value, error) {
		return newDomainInstance(ctx, st, m), nil
	})
	_ = m.Set("create", createFn)
	_ = m.Set("createDomain", createFn)

	// Domain 类构造器：new Domain() / Domain() 均返回新实例。
	proto := engine.NewObject()
	domainCtor := engine.NewFunction("Domain", func(args []engine.Value) (engine.Value, error) {
		inst := newDomainInstance(ctx, st, m)
		// 实例原型链接到 Domain.prototype（支持 instanceof，Node 的
		// domain.add 用 ee instanceof Domain 防循环）。
		engine.SetProto(inst, proto)
		return inst, nil
	})
	ctorObj, _ := domainCtor.AsObject()
	_ = proto.Set("constructor", domainCtor)
	_ = ctorObj.Set("prototype", proto)
	_ = m.Set("Domain", domainCtor)

	// active：初始 null（Node 语义）。
	_ = m.Set("active", st.active)
	// _stack：内部调试用（Node 暴露 exports._stack）。
	_ = m.Set("_stack", engine.NewArray(nil))

	return m, nil
}

// newDomainInstance 创建 Domain 实例对象。
func newDomainInstance(ctx engine.Context, st *domainActiveState, module engine.Object) engine.Value {
	obj := engine.NewObject()
	inst := &domainInstance{
		ctx:        ctx,
		self:       obj,
		em:         &domainEmitter{listeners: make(map[string][]engine.Value), maxListeners: 10},
		st:         st,
		module:     module,
		forwarders: make(map[engine.Value]engine.Value),
	}

	// members：Node 中为构造时初始化的自有数组属性。
	membersArr := engine.NewArray(nil)
	_ = obj.Set("members", membersArr)

	// domain 属性：Node 中 EventEmitter 初始化时设为 null（非 Domain 才绑定
	// 活动 domain）。这里直接设为 null。
	_ = obj.Set("domain", engine.Null())

	// --- EventEmitter 方法面 ---
	registerDomainEmitter(obj, inst)

	// enter()：压栈并设为活动 domain。
	_ = obj.Set("enter", engine.NewFunction("enter", func(args []engine.Value) (engine.Value, error) {
		inst.enter()
		return engine.Undefined(), nil
	}))
	// exit()：出栈（仅当本 domain 在栈中）。
	_ = obj.Set("exit", engine.NewFunction("exit", func(args []engine.Value) (engine.Value, error) {
		inst.exit()
		return engine.Undefined(), nil
	}))
	// run(fn, ...args)：enter 后调用 fn；成功时 exit 并返回 fn 结果，
	// 失败时【不】exit（错误向调用方传播，Node 语义）。
	_ = obj.Set("run", engine.NewFunction("run", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: fn must be a function", engine.ErrTypeError)
		}
		fn, _ := args[0].AsFunction()
		inst.enter()
		ret, err := fn.Call(args[1:])
		if err != nil {
			return engine.Undefined(), err
		}
		inst.exit()
		return ret, nil
	}))
	// bind(fn)：返回包装函数；调用时 enter/调用/exit，抛错向调用方传播。
	_ = obj.Set("bind", engine.NewFunction("bind", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: fn must be a function", engine.ErrTypeError)
		}
		cb, _ := args[0].AsFunction()
		wrapper := engine.NewFunction("runBound", func(callArgs []engine.Value) (engine.Value, error) {
			inst.enter()
			ret, err := cb.Call(callArgs)
			if err != nil {
				return engine.Undefined(), err
			}
			inst.exit()
			return ret, nil
		})
		if wo, ok := wrapper.AsObject(); ok {
			_ = wo.Set("domain", inst.self)
		}
		return wrapper, nil
	}))
	// intercept(cb)：首参为 Error 时路由到 domain 'error' 事件（cb 不调用）；
	// 否则 enter/调用（去掉首参）/exit。
	_ = obj.Set("intercept", engine.NewFunction("intercept", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: fn must be a function", engine.ErrTypeError)
		}
		cb, _ := args[0].AsFunction()
		return engine.NewFunction("runIntercepted", func(callArgs []engine.Value) (engine.Value, error) {
			if len(callArgs) > 0 && isErrorLike(callArgs[0]) {
				er := callArgs[0]
				if o, ok := er.AsObject(); ok {
					_ = o.Set("domainBound", cb)
					_ = o.Set("domainThrown", engine.Boolean(false))
					_ = o.Set("domain", inst.self)
				}
				// Node：self.emit('error', er)；无监听器时抛原值。
				return inst.emitError(er)
			}
			inst.enter()
			ret, err := cb.Call(callArgs[1:])
			if err != nil {
				return engine.Undefined(), err
			}
			inst.exit()
			return ret, nil
		}), nil
	}))
	// add(emitter)：绑定 emitter，'error' 事件路由到本 domain。
	_ = obj.Set("add", engine.NewFunction("add", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		inst.add(args[0])
		return engine.Undefined(), nil
	}))
	// remove(emitter)：解除绑定。
	_ = obj.Set("remove", engine.NewFunction("remove", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		inst.remove(args[0])
		return engine.Undefined(), nil
	}))
	// _errorHandler：Node 中暴露在原型上（引擎层未捕获异常拦截使用）。
	// aluka 不做顶层拦截，提供方法面（可直接调用：路由一个错误值）。
	_ = obj.Set("_errorHandler", engine.NewFunction("_errorHandler", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return inst.routeError(args[0])
	}))

	return obj
}

// enter 压栈并设置活动 domain 与 process.domain。
func (d *domainInstance) enter() {
	d.st.active = d.self
	d.setProcessDomain(d.self)
	d.st.stack = append(d.st.stack, d.self)
	_ = d.module.Set("active", d.st.active)
}

// exit 从栈中弹出本 domain（Node：仅当在栈中才处理）。
func (d *domainInstance) exit() {
	idx := -1
	for i := len(d.st.stack) - 1; i >= 0; i-- {
		if d.st.stack[i] == d.self {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}
	d.st.stack = d.st.stack[:idx]
	if len(d.st.stack) == 0 {
		d.st.active = engine.Undefined()
	} else {
		d.st.active = d.st.stack[len(d.st.stack)-1]
	}
	d.setProcessDomain(d.st.active)
	_ = d.module.Set("active", d.st.active)
}

// add 绑定 emitter：设置 ee.domain 并注册内部 'error' 转发监听器。
func (d *domainInstance) add(ee engine.Value) {
	// 已绑定本 domain：直接返回。
	if o, ok := ee.AsObject(); ok {
		if v, err := o.Get("domain"); err == nil && v == d.self {
			return
		}
	}
	// 已有 domain：先解除旧绑定。
	if o, ok := ee.AsObject(); ok {
		if v, err := o.Get("domain"); err == nil && !v.IsUndefined() && !v.IsNull() {
			if old, ok2 := v.AsObject(); ok2 && old != o {
				if rm, err3 := old.Get("remove"); err3 == nil && rm.IsFunction() {
					if f, ok3 := rm.AsFunction(); ok3 {
						_, _ = f.Call([]engine.Value{ee})
					}
				}
			}
		}
	}
	if o, ok := ee.AsObject(); ok {
		_ = o.Set("domain", d.self)
	}
	d.members = append(d.members, ee)
	d.refreshMembersArray()

	// 注册内部 'error' 转发监听器：ee.emit('error', er) → 本 domain 错误路由。
	if o, ok := ee.AsObject(); ok {
		if onV, err := o.Get("on"); err == nil && onV.IsFunction() {
			if f, ok := onV.AsFunction(); ok {
				forwarder := engine.NewFunction("domainErrorForwarder", func(a []engine.Value) (engine.Value, error) {
					if len(a) == 0 {
						return engine.Undefined(), nil
					}
					er := a[0]
					if eo, ok := er.AsObject(); ok {
						_ = eo.Set("domainEmitter", ee)
						_ = eo.Set("domain", d.self)
						_ = eo.Set("domainThrown", engine.Boolean(false))
					}
					// 无 'error' 监听器时抛原值（模拟 Node 的
					// "Unhandled 'error' event" 语义）。
					return d.emitError(er)
				})
				_, _ = f.Call([]engine.Value{engine.Str("error"), forwarder})
				d.forwarders[ee] = forwarder
			}
		}
	}
}

// remove 解除 emitter 绑定。
func (d *domainInstance) remove(ee engine.Value) {
	if o, ok := ee.AsObject(); ok {
		_ = o.Set("domain", engine.Null())
	}
	for i, m := range d.members {
		if m == ee {
			d.members = append(d.members[:i], d.members[i+1:]...)
			break
		}
	}
	d.refreshMembersArray()
	// 移除转发监听器。
	if fwd, ok := d.forwarders[ee]; ok {
		if o, ok2 := ee.AsObject(); ok2 {
			if offV, err := o.Get("removeListener"); err == nil && offV.IsFunction() {
				if f, ok3 := offV.AsFunction(); ok3 {
					_, _ = f.Call([]engine.Value{engine.Str("error"), fwd})
				}
			}
		}
		delete(d.forwarders, ee)
	}
}

// refreshMembersArray 同步 members JS 数组内容。
func (d *domainInstance) refreshMembersArray() {
	if o, ok := d.self.AsObject(); ok {
		if v, err := o.Get("members"); err == nil {
			if arr, ok2 := v.(*engine.ArrayValue); ok2 {
				_ = arr.Set("length", engine.IntValue(0))
				for _, m := range d.members {
					arr.Append(m)
				}
			}
		}
	}
}

// emitError 触发 domain 的 'error' 事件；无监听器时抛原错误值。
func (d *domainInstance) emitError(er engine.Value) (engine.Value, error) {
	ls := d.em.listeners["error"]
	if len(ls) == 0 {
		return engine.Undefined(), interpreter.ThrowJSValue(er)
	}
	for _, fn := range ls {
		if f, ok := fn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{er}); err != nil {
				return engine.Undefined(), err
			}
		}
	}
	return engine.Boolean(true), nil
}

// routeError 执行 Node _errorHandler 的核心语义：设置错误属性、弹出活动
// domain、有 'error' 监听器则调用（否则重新抛出原值）。
func (d *domainInstance) routeError(er engine.Value) (engine.Value, error) {
	if o, ok := er.AsObject(); ok {
		_ = o.Set("domain", d.self)
		_ = o.Set("domainThrown", engine.Boolean(true))
	}
	// 弹出当前活动 domain（及其相邻重复）。
	for len(d.st.stack) > 0 && d.st.stack[len(d.st.stack)-1] == d.self {
		d.exit()
	}
	return d.emitError(er)
}

// setProcessDomain 更新全局 process.domain。
func (d *domainInstance) setProcessDomain(v engine.Value) {
	if proc, err := d.ctx.Global().Get("process"); err == nil {
		if po, ok := proc.AsObject(); ok {
			_ = po.Set("domain", v)
		}
	}
}

// isErrorLike 判断值是否可视为 Error（Node 用 instanceof Error）。
// aluka 以 Error 家族对象的特征字段近似判断：对象且带字符串 name/message。
func isErrorLike(v engine.Value) bool {
	o, ok := v.AsObject()
	if !ok {
		return false
	}
	name, err1 := o.Get("name")
	if err1 != nil || name.Type() != engine.TypeString {
		return false
	}
	msg, err2 := o.Get("message")
	if err2 != nil || msg.Type() != engine.TypeString {
		return false
	}
	return true
}

// registerDomainEmitter 在 domain 实例上注册 EventEmitter 方法面。
func registerDomainEmitter(obj engine.Object, d *domainInstance) {
	// on/addListener(event, listener)
	onFn := engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && args[1].IsFunction() {
			event := args[0].String()
			d.em.listeners[event] = append(d.em.listeners[event], args[1])
		}
		return obj, nil
	})
	_ = obj.Set("on", onFn)
	_ = obj.Set("addListener", onFn)

	// once(event, listener)
	_ = obj.Set("once", engine.NewFunction("once", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0].String()
			original := args[1]
			var wrapper engine.Value
			wrapper = engine.NewFunction("onceWrapper", func(callArgs []engine.Value) (engine.Value, error) {
				if f, ok := original.AsFunction(); ok {
					_, _ = f.Call(callArgs)
				}
				ls := d.em.listeners[event]
				for i, x := range ls {
					if x == wrapper {
						d.em.listeners[event] = append(ls[:i], ls[i+1:]...)
						break
					}
				}
				return engine.Undefined(), nil
			})
			d.em.listeners[event] = append(d.em.listeners[event], wrapper)
		}
		return obj, nil
	}))

	// removeListener/off(event, listener)
	offFn := engine.NewFunction("off", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0].String()
			ls := d.em.listeners[event]
			for i, x := range ls {
				if x == args[1] {
					d.em.listeners[event] = append(ls[:i], ls[i+1:]...)
					break
				}
			}
		}
		return obj, nil
	})
	_ = obj.Set("off", offFn)
	_ = obj.Set("removeListener", offFn)

	// removeAllListeners([event])
	_ = obj.Set("removeAllListeners", engine.NewFunction("removeAllListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			d.em.listeners = make(map[string][]engine.Value)
		} else {
			d.em.listeners[args[0].String()] = nil
		}
		return obj, nil
	}))

	// listeners(event)
	_ = obj.Set("listeners", engine.NewFunction("listeners", func(args []engine.Value) (engine.Value, error) {
		event := ""
		if len(args) > 0 {
			event = args[0].String()
		}
		return engine.NewArray(append([]engine.Value{}, d.em.listeners[event]...)), nil
	}))

	// rawListeners(event)：与 listeners 相同（无包装器差异）。
	_ = obj.Set("rawListeners", engine.NewFunction("rawListeners", func(args []engine.Value) (engine.Value, error) {
		event := ""
		if len(args) > 0 {
			event = args[0].String()
		}
		return engine.NewArray(append([]engine.Value{}, d.em.listeners[event]...)), nil
	}))

	// listenerCount([event])
	_ = obj.Set("listenerCount", engine.NewFunction("listenerCount", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			total := 0
			for _, ls := range d.em.listeners {
				total += len(ls)
			}
			return engine.IntValue(total), nil
		}
		return engine.IntValue(len(d.em.listeners[args[0].String()])), nil
	}))

	// eventNames()
	_ = obj.Set("eventNames", engine.NewFunction("eventNames", func(args []engine.Value) (engine.Value, error) {
		var names []engine.Value
		for name := range d.em.listeners {
			if len(d.em.listeners[name]) > 0 {
				names = append(names, engine.Str(name))
			}
		}
		return engine.NewArray(names), nil
	}))

	// emit(event, ...args)：'error' 无监听器时抛原值。
	_ = obj.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		event := args[0].String()
		if event == "error" {
			if len(d.em.listeners["error"]) == 0 {
				er := engine.Undefined()
				if len(args) > 1 {
					er = args[1]
				}
				return engine.Undefined(), interpreter.ThrowJSValue(er)
			}
			for _, fn := range append([]engine.Value{}, d.em.listeners["error"]...) {
				if f, ok := fn.AsFunction(); ok {
					if _, err := f.Call(args[1:]); err != nil {
						return engine.Undefined(), err
					}
				}
			}
			return engine.Boolean(true), nil
		}
		ls := append([]engine.Value{}, d.em.listeners[event]...)
		for _, fn := range ls {
			if f, ok := fn.AsFunction(); ok {
				if _, err := f.Call(args[1:]); err != nil {
					return engine.Undefined(), err
				}
			}
		}
		return engine.Boolean(len(ls) > 0), nil
	}))

	// setMaxListeners/getMaxListeners
	_ = obj.Set("setMaxListeners", engine.NewFunction("setMaxListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				d.em.maxListeners = n
			}
		}
		return obj, nil
	}))
	_ = obj.Set("getMaxListeners", engine.NewFunction("getMaxListeners", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(d.em.maxListeners), nil
	}))

	// prependListener / prependOnceListener：与 on/once 相同语义（简化）。
	_ = obj.Set("prependListener", onFn)
	_ = obj.Set("prependOnceListener", engine.NewFunction("prependOnceListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			event := args[0].String()
			d.em.listeners[event] = append([]engine.Value{args[1]}, d.em.listeners[event]...)
		}
		return obj, nil
	}))
}
