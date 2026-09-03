package interpreter

import (
	"fmt"
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
)

// 本文件存放 Object 构造器静态方法的补全实现，从 builtins.go 拆出以遵守
// "单文件 ≤ 500 行"规范。在 setupObjectCtor() 中通过 setupObjectCtorExt()
// 注册到 Object 函数对象上。
//
// 属性描述符：defineProperty/defineProperties 经 engine.DefineOwnProperty
// 实现规范子集（部分描述符合并、标志执行、accessor/data 互斥校验、非可配置
// 重定义限制）；getOwnPropertyDescriptor(s) 反映生效状态。对象级 attrs 惰性
// 字典只记录偏离默认（w/e/c 全 true）的属性，普通对象热路径零开销。

// setupObjectCtorExt 注册 Object 构造器上的扩展静态方法。
func (interp *Interpreter) setupObjectCtorExt(obj engine.Object) {
	// create(proto, propertiesObject?) 以指定原型创建新对象。
	_ = obj.Set("create", interp.makeFunc("create", func(args []engine.Value) (engine.Value, error) {
		var proto engine.Object
		if len(args) == 0 || args[0].IsNull() {
			proto = nil
		} else {
			po, ok := args[0].AsObject()
			if !ok {
				return nil, fmt.Errorf("%w: Object prototype may only be an Object or null", engine.ErrTypeError)
			}
			proto = po
		}
		newObj := engine.NewObject()
		if proto != nil {
			engine.SetProto(newObj, proto)
		}
		// 第二参数 propertiesObject：对每个属性执行 defineProperty 语义。
		if len(args) > 1 && !args[1].IsUndefined() {
			if props, ok := args[1].AsObject(); ok {
				for _, k := range props.Keys() {
					if desc, err := props.Get(k); err == nil {
						if descObj, ok := desc.AsObject(); ok {
							d, err := descriptorFrom(descObj, interp.currentVM)
							if err != nil {
								return nil, err
							}
							if err := engine.DefineOwnProperty(newObj, k, d); err != nil {
								return nil, err
							}
						}
					}
				}
			}
		}
		return newObj, nil
	}))

	// defineProperty(obj, prop, descriptor) 定义/修改一个属性。
	// 完整描述符语义（部分描述符合并/标志执行/校验）见 engine.DefineOwnProperty。
	_ = obj.Set("defineProperty", interp.makeFunc("defineProperty", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("%w: Object.defineProperty requires (obj, prop, descriptor)", engine.ErrTypeError)
		}
		if _, ok := args[0].AsObject(); !ok {
			return nil, fmt.Errorf("%w: Object.defineProperty target must be object", engine.ErrTypeError)
		}
		// Symbol 键必须用 SymbolKey()（内部唯一键），不能 String() 化。
		key := propertyKeyOf(args[1])
		desc, ok := args[2].AsObject()
		if !ok {
			return nil, fmt.Errorf("%w: Property descriptor must be an object", engine.ErrTypeError)
		}
		d, err := descriptorFrom(desc, interp.currentVM)
		if err != nil {
			return nil, err
		}
		if ok, err := interp.defineOwnProperty(args[0], key, d); err != nil {
			return nil, err
		} else if !ok {
			return nil, fmt.Errorf("%w: Proxy defineProperty trap returned false", engine.ErrTypeError)
		}
		return args[0], nil
	}))

	// defineProperties(obj, descriptors) 批量定义属性（复用 defineProperty 语义）。
	_ = obj.Set("defineProperties", interp.makeFunc("defineProperties", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("%w: Object.defineProperties requires (obj, descriptors)", engine.ErrTypeError)
		}
		if _, ok := args[0].AsObject(); !ok {
			return nil, fmt.Errorf("%w: Object.defineProperties target must be object", engine.ErrTypeError)
		}
		descs, ok := args[1].AsObject()
		if !ok {
			return nil, fmt.Errorf("%w: Property descriptors must be an object", engine.ErrTypeError)
		}
		for _, k := range descs.Keys() {
			dv, _ := descs.Get(k)
			desc, ok := dv.AsObject()
			if !ok {
				continue
			}
			d, err := descriptorFrom(desc, interp.currentVM)
			if err != nil {
				return nil, err
			}
			if ok, err := interp.defineOwnProperty(args[0], k, d); err != nil {
				return nil, err
			} else if !ok {
				return nil, fmt.Errorf("%w: Proxy defineProperty trap returned false", engine.ErrTypeError)
			}
		}
		return args[0], nil
	}))

	// getOwnPropertyDescriptor(obj, prop) 返回自有属性描述符（反映生效标志）。
	_ = obj.Set("getOwnPropertyDescriptor", interp.makeFunc("getOwnPropertyDescriptor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		if _, ok := args[0].AsObject(); !ok {
			return engine.Undefined(), nil
		}
		key := propertyKeyOf(args[1])
		desc, err := interp.getOwnPropertyDescriptor(args[0], key)
		if err != nil {
			return nil, err
		}
		if desc == nil {
			return engine.Undefined(), nil
		}
		return desc, nil
	}))

	// getOwnPropertyDescriptors(obj) 返回全部自有属性描述符。
	_ = obj.Set("getOwnPropertyDescriptors", interp.makeFunc("getOwnPropertyDescriptors", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewObject(), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return engine.NewObject(), nil
		}
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)
		for _, k := range engine.AllOwnKeys(o) {
			if engine.IsSymbolKey(k) {
				continue
			}
			if desc, err := interp.getOwnPropertyDescriptor(args[0], k); err != nil {
				return nil, err
			} else if desc != nil {
				_ = result.Set(k, desc)
			}
		}
		return result, nil
	}))

	// getOwnPropertyNames(obj) 返回全部自有属性名（含不可枚举）。
	_ = obj.Set("getOwnPropertyNames", interp.makeFunc("getOwnPropertyNames", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		keys := make([]string, 0)
		for _, key := range engine.AllOwnKeys(o) {
			if !engine.IsSymbolKey(key) {
				keys = append(keys, key)
			}
		}
		return interp.newArray(toValues(keys)), nil
	}))

	// getOwnPropertySymbols(obj) 返回自有 Symbol 属性。
	_ = obj.Set("getOwnPropertySymbols", interp.makeFunc("getOwnPropertySymbols", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		values := make([]engine.Value, 0)
		for _, key := range engine.AllOwnKeys(o) {
			if sym, ok := engine.SymbolFromKey(key); ok {
				values = append(values, sym)
			}
		}
		return interp.newArray(values), nil
	}))

	// is(value1, value2) 同值相等（Object.is）。
	// 与 === 的区别：+0 !== -0 但 Object.is(+0,-0)=false；NaN === NaN 为 false 但 Object.is 为 true。
	_ = obj.Set("is", interp.makeFunc("is", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(objectIs(args[0], args[1])), nil
	}))

	// fromEntries(iterable) 将 [key, value] 对的可迭代对象转为对象（ES2019）。
	_ = obj.Set("fromEntries", interp.makeFunc("fromEntries", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewObject(), nil
		}
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)

		// 路径 1：数组（含数组元素为 [k,v] 对）。
		if arr, ok := args[0].(*engine.ArrayValue); ok {
			for _, e := range arr.Elems() {
				k, v := entryToKV(e)
				_ = result.Set(k, v)
			}
			return result, nil
		}

		// 路径 2：Map（直接复用其内部条目）。
		if m, ok := args[0].(*MapValue); ok {
			for _, kv := range m.entries {
				_ = result.Set(kv.key.String(), kv.value)
			}
			return result, nil
		}

		// 路径 3：通用可迭代对象——通过迭代器协议消费。
		if vm := interp.currentVM; vm != nil {
			iter, err := vm.getIterator(args[0])
			if err == nil && iter != nil {
				for {
					nextFn, err := vm.getProperty(iter, "next")
					if err != nil {
						break
					}
					res, err := vm.invoke(nextFn, iter, nil, false)
					if err != nil {
						break
					}
					doneVal, _ := vm.getProperty(res, "done")
					done, _ := doneVal.Bool()
					if done {
						break
					}
					val, _ := vm.getProperty(res, "value")
					k, v := entryToKV(val)
					_ = result.Set(k, v)
				}
				return result, nil
			}
		}

		return result, nil
	}))

	// hasOwn(obj, prop) 判断是否存在自有属性（ES2022）。
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

	// isFrozen(obj) / isSealed(obj) 查询完整性级别。
	_ = obj.Set("isFrozen", interp.makeFunc("isFrozen", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(true), nil
		}
		o, ok := args[0].AsObject()
		return engine.Boolean(!ok || engine.TestIntegrityLevel(o, true)), nil
	}))
	_ = obj.Set("isSealed", interp.makeFunc("isSealed", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(true), nil
		}
		o, ok := args[0].AsObject()
		return engine.Boolean(!ok || engine.TestIntegrityLevel(o, false)), nil
	}))

	_ = obj.Set("isExtensible", interp.makeFunc("isExtensible", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		o, ok := args[0].AsObject()
		return engine.Boolean(ok && engine.IsExtensible(o)), nil
	}))

	_ = obj.Set("seal", interp.makeFunc("seal", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		if o, ok := args[0].AsObject(); ok {
			if err := engine.SetIntegrityLevel(o, false); err != nil {
				return nil, err
			}
		}
		return args[0], nil
	}))
	_ = obj.Set("preventExtensions", interp.makeFunc("preventExtensions", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		if o, ok := args[0].AsObject(); ok {
			engine.PreventExtensions(o)
		}
		return args[0], nil
	}))

	// setPrototypeOf(obj, proto) 设置原型。
	_ = obj.Set("setPrototypeOf", interp.makeFunc("setPrototypeOf", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return args[0], nil
		}
		if args[1].IsNull() {
			if !engine.TrySetProto(o, nil) {
				return nil, fmt.Errorf("%w: object is not extensible", engine.ErrTypeError)
			}
		} else if p, ok := args[1].AsObject(); ok {
			if !engine.TrySetProto(o, p) {
				return nil, fmt.Errorf("%w: object is not extensible", engine.ErrTypeError)
			}
		}
		return args[0], nil
	}))
}

// --- 辅助函数 ------------------------------------------------------------

// hasOwn 报告对象是否拥有某自有属性（不走原型链）。
func hasOwn(o engine.Object, key string) bool {
	return engine.HasOwnProperty(o, key)
}

// descriptorFrom 实现 ToPropertyDescriptor：字段存在性包含继承和不可枚举属性。
func descriptorFrom(desc engine.Object, vm *VM) (engine.Descriptor, error) {
	var d engine.Descriptor
	has := func(key string) (bool, error) {
		if vm != nil {
			return vm.hasProperty(engine.Str(key), desc)
		}
		for cur := desc; cur != nil; cur = engine.GetProto(cur) {
			if engine.HasOwnProperty(cur, key) {
				return true, nil
			}
		}
		return false, nil
	}
	get := func(key string) (engine.Value, error) {
		v, err := desc.Get(key)
		if err != nil {
			return nil, err
		}
		return v, nil
	}
	if ok, err := has("value"); err != nil {
		return d, err
	} else if ok {
		d.HasValue = true
		d.Value, err = get("value")
		if err != nil {
			return d, err
		}
	}
	if ok, err := has("writable"); err != nil {
		return d, err
	} else if ok {
		d.HasWritable = true
		w, err := get("writable")
		if err != nil {
			return d, err
		}
		d.Writable, _ = w.Bool()
	}
	if ok, err := has("enumerable"); err != nil {
		return d, err
	} else if ok {
		d.HasEnumerable = true
		e, err := get("enumerable")
		if err != nil {
			return d, err
		}
		d.Enumerable, _ = e.Bool()
	}
	if ok, err := has("configurable"); err != nil {
		return d, err
	} else if ok {
		d.HasConfigurable = true
		c, err := get("configurable")
		if err != nil {
			return d, err
		}
		d.Configurable, _ = c.Bool()
	}
	if ok, err := has("get"); err != nil {
		return d, err
	} else if ok {
		d.HasGet = true
		d.Get, err = get("get")
		if err != nil {
			return d, err
		}
	}
	if ok, err := has("set"); err != nil {
		return d, err
	} else if ok {
		d.HasSet = true
		d.Set, err = get("set")
		if err != nil {
			return d, err
		}
	}
	return d, nil
}

// ownPropertyDescriptor 构造反映生效状态的属性描述符对象
// （value/writable 或 get/set + enumerable/configurable）；属性不存在返回 nil。
// get-intrinsic 等 npm 包依赖 gOPD 区分访问器与数据属性：若访问器被伪装成
// 数据属性（只有 value），它们会直接读取 prototype 上的属性值，从而以错误
// 的 this 调用 getter（如 Map.prototype.size 报 "called on non-Map"）。
func (interp *Interpreter) descriptorObject(d engine.Descriptor) engine.Value {
	desc := engine.NewObject()
	engine.SetProto(desc, interp.objectProto)
	if d.HasGet || d.HasSet {
		_ = desc.Set("get", orUndefinedValue(d.Get))
		_ = desc.Set("set", orUndefinedValue(d.Set))
	} else {
		_ = desc.Set("value", d.Value)
		_ = desc.Set("writable", engine.Boolean(d.Writable))
	}
	_ = desc.Set("enumerable", engine.Boolean(d.Enumerable))
	_ = desc.Set("configurable", engine.Boolean(d.Configurable))
	return desc
}

func (interp *Interpreter) ownPropertyDescriptor(o engine.Object, key string) engine.Value {
	d, exists := engine.OwnPropertyDescriptor(o, key)
	if !exists {
		return nil
	}
	return interp.descriptorObject(d)
}

func (interp *Interpreter) defineOwnProperty(target engine.Value, key string, d engine.Descriptor) (bool, error) {
	if proxy, ok := target.(*ProxyValue); ok {
		return proxy.proxyDefineProperty(key, d)
	}
	o, ok := target.AsObject()
	if !ok {
		return false, fmt.Errorf("%w: target must be object", engine.ErrTypeError)
	}
	if err := engine.DefineOwnProperty(o, key, d); err != nil {
		if engine.IsDefineRejected(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (interp *Interpreter) getOwnPropertyDescriptor(target engine.Value, key string) (engine.Value, error) {
	if proxy, ok := target.(*ProxyValue); ok {
		return proxy.proxyGetOwnPropertyDescriptor(key)
	}
	o, ok := target.AsObject()
	if !ok {
		return nil, nil
	}
	return interp.ownPropertyDescriptor(o, key), nil
}

// orUndefinedValue 把 nil 归一为 Undefined。
func orUndefinedValue(v engine.Value) engine.Value {
	if v == nil {
		return engine.Undefined()
	}
	return v
}

// entryToKV 从一个 [key, value] 对（数组或对象）提取键与值。
func entryToKV(e engine.Value) (string, engine.Value) {
	if arr, ok := e.(*engine.ArrayValue); ok {
		elems := arr.Elems()
		if len(elems) >= 2 {
			return elems[0].String(), elems[1]
		}
		if len(elems) == 1 {
			return elems[0].String(), engine.Undefined()
		}
	}
	if obj, ok := e.AsObject(); ok {
		k, _ := obj.Get("0")
		v, _ := obj.Get("1")
		return k.String(), v
	}
	return "", engine.Undefined()
}

// objectIs 实现 Object.is 的同值相等语义。
func objectIs(a, b engine.Value) bool {
	// 快速路径：strictEqual 处理大部分情况。
	if strictEqual(a, b) {
		// 排除 +0 === -0 的情况：Object.is(+0, -0) 应为 false。
		af, aok := a.Float()
		bf, bok := b.Float()
		if aok && bok && af == 0 && bf == 0 {
			return math.Signbit(af) == math.Signbit(bf)
		}
		return true
	}
	// NaN 路径：Object.is(NaN, NaN) 应为 true。
	af, aok := a.Float()
	bf, bok := b.Float()
	if aok && bok {
		return math.IsNaN(af) && math.IsNaN(bf)
	}
	return false
}
