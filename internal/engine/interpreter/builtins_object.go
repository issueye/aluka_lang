// 内置对象：Object.prototype 与 Object 构造器（含描述符/原型链方法族）、ordinaryHasInstance。

package interpreter

import (
	"fmt"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

func (interp *Interpreter) setupObjectProto() {
	p := interp.objectProto
	_ = p.Set("hasOwnProperty", interp.nativeMethod("hasOwnProperty", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		key := propertyKeyOf(args[0])
		if p, ok := this.(*ProxyValue); ok {
			desc, err := p.proxyGetOwnPropertyDescriptor(key)
			return engine.Boolean(err == nil && desc != nil && !desc.IsUndefined()), err
		}
		o, ok := this.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(engine.HasOwnProperty(o, key)), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var builtinTag string
		switch this.Type() {
		case engine.TypeUndefined:
			return engine.Str("[object Undefined]"), nil
		case engine.TypeNull:
			return engine.Str("[object Null]"), nil
		case engine.TypeBoolean:
			builtinTag = "Boolean"
		case engine.TypeNumber:
			builtinTag = "Number"
		case engine.TypeString:
			builtinTag = "String"
		case engine.TypeFunction:
			builtinTag = "Function"
		case engine.TypeBigInt:
			builtinTag = "BigInt"
		default:
			builtinTag = "Object"
		}
		// Symbol.toStringTag 协议（ES2020 20.1.3.6 step 5）：沿原型链查
		// @@toStringTag，字符串值覆盖 builtin tag；否则保持内建标签。
		if o, ok := this.AsObject(); ok {
			key := engine.SymbolToStringTag.SymbolKey()
			for cur := engine.Value(o); cur != nil; {
				co, ok := cur.AsObject()
				if !ok {
					break
				}
				if tag, err := co.Get(key); err == nil && tag != nil && !tag.IsUndefined() {
					if tag.Type() == engine.TypeString {
						builtinTag = tag.String()
					}
					break
				}
				cur = engine.GetProto(co)
			}
		}
		return engine.Str("[object " + builtinTag + "]"), nil
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return this, nil
	}))
	_ = p.Set("isPrototypeOf", interp.nativeMethod("isPrototypeOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		proto := engine.GetProto(args[0])
		thisObj, ok := this.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		for proto != nil {
			if proto == thisObj {
				return engine.Boolean(true), nil
			}
			proto = engine.GetProto(proto)
		}
		return engine.Boolean(false), nil
	}))
	_ = p.Set("propertyIsEnumerable", interp.nativeMethod("propertyIsEnumerable", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		o, ok := this.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		for _, k := range o.Keys() {
			if k == args[0].String() {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))
	_ = p.Set("toLocaleString", interp.nativeMethod("toLocaleString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		// 默认实现等价 Invoke(this, "toString")。
		if o, ok := this.AsObject(); ok {
			if ts, err := o.Get("toString"); err == nil && ts.IsFunction() {
				if f, ok := ts.AsFunction(); ok {
					return f.Call([]engine.Value{this})
				}
			}
		}
		return engine.Str(this.String()), nil
	}))

	// --- Legacy accessor helpers（Annex B）---
	legacyDefineAccessor := func(isGetter bool) engine.Value {
		name := "__defineSetter__"
		if isGetter {
			name = "__defineGetter__"
		}
		return interp.nativeMethod(name, func(this engine.Value, args []engine.Value) (engine.Value, error) {
			o, ok := this.AsObject()
			if !ok {
				return engine.Undefined(), fmt.Errorf("%w: Object.prototype.%s called on non-object", engine.ErrTypeError, name)
			}
			if len(args) < 2 || !args[1].IsFunction() {
				return engine.Undefined(), fmt.Errorf("%w: %s requires a function", engine.ErrTypeError, name)
			}
			key := propertyKeyOf(args[0])
			d := engine.Descriptor{HasEnumerable: true, Enumerable: true, HasConfigurable: true, Configurable: true}
			if isGetter {
				d.HasGet, d.Get = true, args[1]
			} else {
				d.HasSet, d.Set = true, args[1]
			}
			if err := engine.DefineOwnProperty(o, key, d); err != nil {
				return engine.Undefined(), err
			}
			return engine.Undefined(), nil
		})
	}
	_ = p.Set("__defineGetter__", legacyDefineAccessor(true))
	_ = p.Set("__defineSetter__", legacyDefineAccessor(false))

	legacyLookupAccessor := func(isGetter bool) engine.Value {
		name := "__lookupSetter__"
		if isGetter {
			name = "__lookupGetter__"
		}
		return interp.nativeMethod(name, func(this engine.Value, args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), nil
			}
			key := propertyKeyOf(args[0])
			for cur := this; cur != nil; {
				co, ok := cur.AsObject()
				if !ok {
					break
				}
				if v, exists := engine.GetOwnSlot(co, key); exists {
					if acc, ok := v.(*engine.AccessorValue); ok {
						if isGetter {
							return orUndefinedValue(acc.Getter), nil
						}
						return orUndefinedValue(acc.Setter), nil
					}
					return engine.Undefined(), nil
				}
				cur = engine.GetProto(co)
			}
			return engine.Undefined(), nil
		})
	}
	_ = p.Set("__lookupGetter__", legacyLookupAccessor(true))
	_ = p.Set("__lookupSetter__", legacyLookupAccessor(false))

	// __proto__ 访问器（Annex B）：get = getPrototypeOf；set 走 [[SetPrototypeOf]]
	// 语义（非对象/环静默失败）。
	engine.SetAccessor(p, "__proto__",
		interp.nativeMethod("get __proto__", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			proto := engine.GetProto(this)
			if proto == nil {
				return engine.Null(), nil
			}
			return proto, nil
		}),
		interp.nativeMethod("set __proto__", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), nil
			}
			if _, ok := this.AsObject(); !ok {
				return engine.Undefined(), nil
			}
			if args[0].IsNull() {
				engine.SetProto(this, nil)
				return engine.Undefined(), nil
			}
			proto, ok := args[0].AsObject()
			if !ok {
				return engine.Undefined(), nil
			}
			engine.TrySetProto(this, proto)
			return engine.Undefined(), nil
		}),
	)
	// 成员不可枚举统一在 setupBuiltins 末尾的 sweepBuiltinEnumerability 处理
	// （constructor 亦经 Object 构造器的 prototype 链接被覆盖）。
}

func (interp *Interpreter) setupObjectCtor() {
	// Object is both a constructor (new Object() / Object()) and a namespace
	// for static methods (Object.keys, Object.values, ...). Use makeFunc so it
	// is callable; static methods are attached as properties on the function.
	obj := interp.makeFunc("Object", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || args[0].IsUndefined() || args[0].IsNull() {
			o := engine.NewObject()
			engine.SetProto(o, interp.objectProto)
			return o, nil
		}
		if ao, ok := args[0].AsObject(); ok {
			return ao, nil
		}
		o := engine.NewObject()
		engine.SetProto(o, interp.objectProto)
		return o, nil
	})
	_ = obj.Set("keys", interp.makeFunc("keys", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		// Proxy interception: use the ownKeys trap.
		if p, ok := args[0].(*ProxyValue); ok {
			keys, err := p.proxyOwnKeys()
			if err != nil {
				return interp.newArray(nil), err
			}
			values := make([]engine.Value, 0, len(keys))
			for _, key := range keys {
				if engine.IsSymbolKey(key) {
					continue
				}
				desc, err := interp.getOwnPropertyDescriptor(p, key)
				if err != nil {
					return interp.newArray(nil), err
				}
				if d, ok := desc.AsObject(); ok {
					enumerable, _ := d.Get("enumerable")
					if b, _ := enumerable.Bool(); b {
						values = append(values, engine.Str(key))
					}
				}
			}
			return interp.newArray(values), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		return interp.newArray(toValues(o.Keys())), nil
	}))
	// Object.hasOwn(obj, key) → bool（ES2022，N22-C3 核对补全）。
	_ = obj.Set("hasOwn", interp.makeFunc("hasOwn", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		if p, ok := args[0].(*ProxyValue); ok {
			desc, err := p.proxyGetOwnPropertyDescriptor(propertyKeyOf(args[1]))
			return engine.Boolean(err == nil && desc != nil && !desc.IsUndefined()), err
		}
		o, ok := args[0].AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(hasOwn(o, propertyKeyOf(args[1]))), nil
	}))
	_ = obj.Set("values", interp.makeFunc("values", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		var vals []engine.Value
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			vals = append(vals, v)
		}
		return interp.newArray(vals), nil
	}))
	_ = obj.Set("entries", interp.makeFunc("entries", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		var entries []engine.Value
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			entry := interp.newArray([]engine.Value{engine.Str(k), v})
			entries = append(entries, entry)
		}
		return interp.newArray(entries), nil
	}))
	// Object.groupBy(items, callbackfn) → null-prototype 对象（ES2024，N22-C2）。
	// 分组键经 ToPropertyKey（propertyKeyOf）。
	_ = obj.Set("groupBy", interp.makeFunc("groupBy", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: Object.groupBy requires an iterable and callback", engine.ErrTypeError)
		}
		fn, err := asCallable(args[1])
		if err != nil {
			return nil, err
		}
		groups := engine.NewObject() // proto nil = null-prototype（规范语义）
		err = forEachIterable(interp, args[0], func(item engine.Value) error {
			k, err := fn.callWith(engine.Undefined(), []engine.Value{item})
			if err != nil {
				return err
			}
			key := propertyKeyOf(k)
			var arr *engine.ArrayValue
			if v, err := groups.Get(key); err == nil && !v.IsUndefined() {
				if a, ok := v.(*engine.ArrayValue); ok {
					arr = a
				}
			}
			if arr == nil {
				arr = engine.NewArray(nil)
				engine.SetProto(arr, interp.arrayProto)
				_ = groups.Set(key, arr)
			}
			elems := arr.Elems()
			_ = arr.Set(strconv.Itoa(len(elems)), item)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return groups, nil
	}))
	_ = obj.Set("assign", interp.makeFunc("assign", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewObject(), nil
		}
		target, ok := args[0].AsObject()
		if !ok {
			return nil, fmt.Errorf("%w: Object.assign target must be object", engine.ErrTypeError)
		}
		for i := 1; i < len(args); i++ {
			src, ok := args[i].AsObject()
			if !ok {
				continue
			}
			for _, k := range src.Keys() {
				// 必须走 getProperty：ESM 命名导出可能是 getter 活绑定，
				// Object.Get 会把 AccessorValue 当数据值拷走。
				var val engine.Value
				if vm := interp.currentVM; vm != nil {
					val, _ = vm.getProperty(args[i], k)
				} else {
					val, _ = src.Get(k)
				}
				_ = target.Set(k, val)
			}
		}
		return target, nil
	}))
	_ = obj.Set("freeze", interp.makeFunc("freeze", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		if o, ok := args[0].AsObject(); ok {
			if err := engine.SetIntegrityLevel(o, true); err != nil {
				return nil, err
			}
		}
		return args[0], nil
	}))
	_ = obj.Set("getPrototypeOf", interp.makeFunc("getPrototypeOf", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Null(), nil
		}
		// Proxy interception: use the VM's getProto so the getPrototypeOf
		// trap fires for Proxy targets.
		if vm := interp.currentVM; vm != nil {
			proto := vm.getProto(args[0])
			if proto == nil {
				return engine.Null(), nil
			}
			return proto, nil
		}
		proto := engine.GetProto(args[0])
		if proto == nil {
			return engine.Null(), nil
		}
		return proto, nil
	}))
	// Object 静态方法扩展（见 object_methods.go）。
	interp.setupObjectCtorExt(obj)
	_ = obj.Set("prototype", interp.objectProto)
	_ = obj.Set("name", engine.Str("Object"))
	engine.SetProto(obj, interp.functionProto)
	_ = interp.objectProto.Set("constructor", obj)
	interp.globalObj.Set("Object", obj)
	interp.constructors["Object"] = obj
}

// ordinaryHasInstance 实现 Function.prototype[Symbol.hasInstance] 的普通
// 原型链判断：V 的 [[Prototype]] 链上是否出现 ctor.prototype。忽略用户对
// @@hasInstance 的覆写（内置语义），且不调用 VM.instanceof 避免递归。
func (interp *Interpreter) ordinaryHasInstance(v, ctor engine.Value) bool {
	if v.IsNull() || v.IsUndefined() {
		return false
	}
	if !v.IsObject() {
		return false
	}
	ctorObj, ok := ctor.AsObject()
	if !ok {
		return false
	}
	protoVal, err := ctorObj.Get("prototype")
	if err != nil || protoVal.IsUndefined() {
		return false
	}
	protoObj, ok := protoVal.(engine.Object)
	if !ok {
		return false
	}
	// custom 值类型（Map/Set/Promise/Generator 等）的 [[Prototype]] 链在 backing
	// obj 上（与 vm.getProto 一致），否则 `new Map() instanceof Map` 恒 false。
	var cur engine.Object
	switch t := v.(type) {
	case *PromiseValue:
		cur = engine.GetProto(t.obj)
	case *GeneratorValue:
		cur = engine.GetProto(t.obj)
	case *MapValue:
		cur = engine.GetProto(t.obj)
	case *SetValue:
		cur = engine.GetProto(t.obj)
	case *WeakMapValue:
		cur = engine.GetProto(t.obj)
	case *WeakSetValue:
		cur = engine.GetProto(t.obj)
	default:
		cur = engine.GetProto(v)
	}
	for cur != nil {
		if cur == protoObj {
			return true
		}
		cur = engine.GetProto(cur)
	}
	return false
}
