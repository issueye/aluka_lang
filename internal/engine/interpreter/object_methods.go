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
							if err := engine.DefineOwnProperty(newObj, k, descriptorFrom(descObj)); err != nil {
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
		o, ok := args[0].AsObject()
		if !ok {
			return nil, fmt.Errorf("%w: Object.defineProperty target must be object", engine.ErrTypeError)
		}
		// Symbol 键必须用 SymbolKey()（内部唯一键），不能 String() 化。
		key := propertyKeyOf(args[1])
		desc, ok := args[2].AsObject()
		if !ok {
			return nil, fmt.Errorf("%w: Property descriptor must be an object", engine.ErrTypeError)
		}
		if err := engine.DefineOwnProperty(o, key, descriptorFrom(desc)); err != nil {
			return nil, err
		}
		return args[0], nil
	}))

	// defineProperties(obj, descriptors) 批量定义属性（复用 defineProperty 语义）。
	_ = obj.Set("defineProperties", interp.makeFunc("defineProperties", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("%w: Object.defineProperties requires (obj, descriptors)", engine.ErrTypeError)
		}
		o, ok := args[0].AsObject()
		if !ok {
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
			if err := engine.DefineOwnProperty(o, k, descriptorFrom(desc)); err != nil {
				return nil, err
			}
		}
		return args[0], nil
	}))

	// getOwnPropertyDescriptor(obj, prop) 返回自有属性描述符（反映生效标志）。
	_ = obj.Set("getOwnPropertyDescriptor", interp.makeFunc("getOwnPropertyDescriptor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), nil
		}
		key := propertyKeyOf(args[1])
		desc := interp.ownPropertyDescriptor(o, key)
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
			if desc := interp.ownPropertyDescriptor(o, k); desc != nil {
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
		return interp.newArray(toValues(engine.AllOwnKeys(o))), nil
	}))

	// getOwnPropertySymbols(obj) 返回自有 Symbol 属性（当前未建模，返回空数组）。
	_ = obj.Set("getOwnPropertySymbols", interp.makeFunc("getOwnPropertySymbols", func(args []engine.Value) (engine.Value, error) {
		return interp.newArray(nil), nil
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
		o, ok := args[0].AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(hasOwn(o, propertyKeyOf(args[1]))), nil
	}))

	// 以下为常用但当前缺失的辅助静态方法（冻结/密封族，简化实现）。

	// isFrozen(obj) 简化：恒返回 false（未建模不可变性）。
	_ = obj.Set("isFrozen", interp.makeFunc("isFrozen", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))

	// isSealed(obj) 简化：恒返回 false。
	_ = obj.Set("isSealed", interp.makeFunc("isSealed", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))

	// isExtensible(obj) 简化：对象恒返回 true。
	_ = obj.Set("isExtensible", interp.makeFunc("isExtensible", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		if _, ok := args[0].AsObject(); ok {
			return engine.Boolean(true), nil
		}
		return engine.Boolean(false), nil
	}))

	// seal(obj) / preventExtensions(obj) 简化为返回原对象。
	_ = obj.Set("seal", interp.makeFunc("seal", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			return args[0], nil
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("preventExtensions", interp.makeFunc("preventExtensions", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			return args[0], nil
		}
		return engine.Undefined(), nil
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
			engine.SetProto(o, nil)
		} else if p, ok := args[1].AsObject(); ok {
			engine.SetProto(o, p)
		}
		return args[0], nil
	}))
}

// --- 辅助函数 ------------------------------------------------------------

// hasOwn 报告对象是否拥有某自有属性（不走原型链）。
func hasOwn(o engine.Object, key string) bool {
	for _, k := range o.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

// descriptorFrom 从描述符对象提取 engine.Descriptor（Has* 标记字段出现性）。
func descriptorFrom(desc engine.Object) engine.Descriptor {
	var d engine.Descriptor
	if hasOwn(desc, "value") {
		d.HasValue = true
		d.Value, _ = desc.Get("value")
	}
	if hasOwn(desc, "writable") {
		d.HasWritable = true
		w, _ := desc.Get("writable")
		d.Writable, _ = w.Bool()
	}
	if hasOwn(desc, "enumerable") {
		d.HasEnumerable = true
		e, _ := desc.Get("enumerable")
		d.Enumerable, _ = e.Bool()
	}
	if hasOwn(desc, "configurable") {
		d.HasConfigurable = true
		c, _ := desc.Get("configurable")
		d.Configurable, _ = c.Bool()
	}
	if hasOwn(desc, "get") {
		d.HasGet = true
		d.Get, _ = desc.Get("get")
	}
	if hasOwn(desc, "set") {
		d.HasSet = true
		d.Set, _ = desc.Get("set")
	}
	return d
}

// ownPropertyDescriptor 构造反映生效状态的属性描述符对象
// （value/writable 或 get/set + enumerable/configurable）；属性不存在返回 nil。
// get-intrinsic 等 npm 包依赖 gOPD 区分访问器与数据属性：若访问器被伪装成
// 数据属性（只有 value），它们会直接读取 prototype 上的属性值，从而以错误
// 的 this 调用 getter（如 Map.prototype.size 报 "called on non-Map"）。
func (interp *Interpreter) ownPropertyDescriptor(o engine.Object, key string) engine.Value {
	v, exists := engine.GetOwnSlot(o, key)
	if !exists {
		return nil
	}
	attrs := engine.AttrsOf(o, key)
	desc := engine.NewObject()
	engine.SetProto(desc, interp.objectProto)
	if acc, ok := v.(*engine.AccessorValue); ok {
		_ = desc.Set("get", orUndefinedValue(acc.Getter))
		_ = desc.Set("set", orUndefinedValue(acc.Setter))
	} else {
		_ = desc.Set("value", v)
		_ = desc.Set("writable", engine.Boolean(attrs.Writable))
	}
	_ = desc.Set("enumerable", engine.Boolean(attrs.Enumerable))
	_ = desc.Set("configurable", engine.Boolean(attrs.Configurable))
	return desc
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
