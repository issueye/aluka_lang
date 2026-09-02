// VM 属性访问：数组索引快路径、带 receiver 的 get/set、原型链查找与 in/instanceof。

package interpreter

import (
	"fmt"
	"math"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

// propertyKeyOf converts a value to a property key string. Symbols use their
// unique SymbolKey(); other values use their string representation.
func propertyKeyOf(key engine.Value) string {
	if sym, ok := key.(*engine.SymbolValue); ok {
		return sym.SymbolKey()
	}
	return key.String()
}

// tryArrayIndexGet 处理数组的数值下标读快路径（M1-2）：obj 为 ArrayValue
// 且 key 为非负整数 number 时，直接读元素并返回 (值, true)；否则返回
// (Undefined, false)，调用方回退完整 getProperty 路径。
// 语义精确对齐 getProperty 的数组分支：key 为非负整数且 ≤ 2^53-1（该范围
// 内 formatNumber 输出纯十进制整数串，strconv.Atoi 必成功）时，n < len 返回
// 元素、n ≥ len 返回 undefined 且不查原型链（JS 数组越界索引语义）；负数 /
// 非整数 / NaN / ±Inf / 超出范围一律返回 false 走完整路径。
func (v *VM) tryArrayIndexGet(obj, key engine.Value) (engine.Value, bool) {
	if key.Type() != engine.TypeNumber {
		return engine.Undefined(), false
	}
	arr, ok := obj.(*engine.ArrayValue)
	if !ok {
		return engine.Undefined(), false
	}
	f, _ := key.Float()
	if f < 0 || f != math.Trunc(f) || f > float64(uint64(1)<<32-2) {
		return engine.Undefined(), false
	}
	n := int(f)
	elems := arr.Elems()
	if n < len(elems) {
		if value, exists := engine.GetOwnSlot(arr, strconv.Itoa(n)); exists {
			if _, accessor := value.(*engine.AccessorValue); accessor {
				return engine.Undefined(), false
			}
			return value, true
		}
		return engine.Undefined(), false
	}
	return engine.Undefined(), false
}

// tryArrayIndexSet 处理数组的数值下标写快路径（M1-2 写侧）：obj 为
// ArrayValue 且 key 为非负整数 number 时，直写元素（越界自动稀疏填充并
// 同步 length）并返回 true；否则返回 false，调用方回退完整 setProperty 路径。
//
// 语义精确对齐 setProperty 的数组分支（ArrayValue.Set 的数值索引路径）：
// 非负整数且 ≤ 2^53-1 走元素写；负数 / 非整数 / NaN / ±Inf / 超范围回退。
// Proxy 非 *ArrayValue 自动排除；数值索引 accessor 在本引擎不可构造
// （defineProperty 为简化 Set），故无需 FindAccessor 拦截。
func (v *VM) tryArrayIndexSet(obj, key, val engine.Value) bool {
	if key.Type() != engine.TypeNumber {
		return false
	}
	arr, ok := obj.(*engine.ArrayValue)
	if !ok {
		return false
	}
	keyString := propertyKeyOf(key)
	if desc, exists := engine.OwnPropertyDescriptor(arr, keyString); exists {
		if desc.HasGet || !desc.Writable {
			return false
		}
	} else if !engine.IsExtensible(arr) {
		return false
	}
	f, _ := key.Float()
	if f < 0 || f != math.Trunc(f) || f > float64(uint64(1)<<32-2) {
		return false
	}
	arr.SetIndex(int(f), val)
	return true
}

// getProperty reads a property from a value, handling primitives via prototypes.
func (v *VM) getProperty(obj engine.Value, key string) (engine.Value, error) {
	return v.getPropertyWithReceiver(obj, key, obj)
}

// getPropertyWithReceiver reads a property from obj, using receiver as 'this' for getters.
func (v *VM) getPropertyWithReceiver(obj engine.Value, key string, receiver engine.Value) (engine.Value, error) {
	if receiver == nil {
		receiver = obj
	}
	// O2-D2 快速路径：隐藏类对象 IC 命中直接返回（仅在 receiver == obj 时生效以保证 getter this 正确）。
	if receiver == obj {
		if cv, hit := v.ic.GetCached(obj, key); hit {
			if acc, isAcc := cv.(*engine.AccessorValue); isAcc {
				if acc.Getter != nil && !acc.Getter.IsUndefined() {
					return v.invoke(acc.Getter, receiver, nil, false)
				}
				return engine.Undefined(), nil
			}
			return cv, nil
		}
	}
	if obj.IsNull() || obj.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: Cannot read properties of %s (reading '%s')", engine.ErrTypeError, obj.String(), key)
	}
	// Proxy interception: dispatch to the get trap if defined.
	if p, ok := obj.(*ProxyValue); ok {
		return p.proxyGet(key, receiver)
	}
	// String primitives: handle length + indexed access + string proto methods.
	if obj.Type() == engine.TypeString {
		if key == "length" {
			n, _ := engine.StringLen(obj)
			return engine.IntValue(n), nil
		}
		// Numeric index → character.
		if n, err := strconv.Atoi(key); err == nil {
			if unit, ok := jsStringUnitAt(obj.String(), n); ok {
				return engine.Str(unit), nil
			}
			return engine.Undefined(), nil
		}
		if v.interp.stringProto != nil {
			return v.interp.stringProto.Get(key)
		}
		return engine.Undefined(), nil
	}
	// Array length.
	if arr, ok := obj.(*engine.ArrayValue); ok {
		if key == "length" {
			return engine.IntValue(len(arr.Elems())), nil
		}
		if own, exists := engine.GetOwnSlot(arr, key); exists {
			if acc, ok := own.(*engine.AccessorValue); ok {
				if !orUndefinedValue(acc.Getter).IsUndefined() {
					return v.invoke(acc.Getter, receiver, nil, false)
				}
				return engine.Undefined(), nil
			}
			return own, nil
		}
		// hole 或越界索引须继续查原型链。
		if _, err := strconv.ParseUint(key, 10, 32); err == nil {
			if proto := engine.GetProto(arr); proto != nil {
				return v.getPropertyWithReceiver(proto, key, receiver)
			}
			return engine.Undefined(), nil
		}
		// 数组 own 属性 miss：先查显式原型链（绑定了 arrayProto 的数组），
		// 再回退到 interp.arrayProto（Go 侧创建、未绑定原型的数组）。
		if o, ok := arr.AsObject(); ok {
			if val, _ := o.Get(key); !val.IsUndefined() {
				return val, nil
			}
		}
		if v.interp.arrayProto != nil {
			return v.interp.arrayProto.Get(key)
		}
		return engine.Undefined(), nil
	}
	// Number/boolean primitives: look up on prototype.
	switch obj.Type() {
	case engine.TypeNumber:
		if v.interp.numberProto != nil {
			return v.interp.numberProto.Get(key)
		}
	case engine.TypeBoolean:
		if v.interp.booleanProto != nil {
			return v.interp.booleanProto.Get(key)
		}
	case engine.TypeBigInt:
		if v.interp.bigintProto != nil {
			return v.interp.bigintProto.Get(key)
		}
	case engine.TypeFunction:
		// 函数对象：先查 own（name/length 等），miss 后回退 Function.prototype
		// （native 函数的 [[Prototype]] 未链接 functionProto）。
		if o, ok := obj.(engine.Object); ok {
			if val, _ := o.Get(key); !val.IsUndefined() {
				// 访问器属性：调用 getter（this=receiver）。
				if acc, ok := val.(*engine.AccessorValue); ok && !acc.Getter.IsUndefined() {
					return v.invoke(acc.Getter, receiver, nil, false)
				}
				return val, nil
			}
		}
		if v.interp.functionProto != nil {
			return v.interp.functionProto.Get(key)
		}
	}
	// 如果对象本身或原型链上有 Proxy，分派到 Proxy 的 proxyGet（携带正确的 receiver）
	cur := obj
	for cur != nil {
		if p, ok := cur.(*ProxyValue); ok {
			return p.proxyGet(key, receiver)
		}
		proto := engine.GetProto(cur)
		if proto == nil {
			break
		}
		cur = proto
	}

	backing := v.backingObj(obj)
	if acc, ok := engine.FindAccessor(backing, key); ok {
		if acc.Getter != nil && !acc.Getter.IsUndefined() {
			return v.invoke(acc.Getter, receiver, nil, false)
		}
		return engine.Undefined(), nil
	}
	if o, ok := obj.AsObject(); ok {
		val, err := o.Get(key)
		if receiver == obj {
			v.ic.CachePut(obj, key)
		}
		return val, err
	}
	return engine.Undefined(), nil
}

// backingObj returns the underlying property storage for delegated wrappers.
func (v *VM) backingObj(val engine.Value) engine.Value {
	if wrapper, ok := val.(engine.ObjectUnwrapper); ok {
		if inner := wrapper.UnwrapObject(); inner != nil {
			return inner
		}
	}
	return val
}

// setProperty writes a property on a value.
func (v *VM) setProperty(obj engine.Value, key string, val engine.Value) error {
	// Proxy interception: dispatch to the set trap if defined.
	if p, ok := obj.(*ProxyValue); ok {
		return p.proxySet(key, val)
	}
	// 写入 IC 前置（M4 后续）：own 数据属性命中时直写槽位，跳过 FindAccessor
	// 的原型链 shape 查找（own 数据属性按 JS 语义遮蔽原型链 accessor）。
	// accessor 槽位由 SetCached 内部拒绝（返回 false），回退到下方
	// FindAccessor 拦截调 setter。数组（*ArrayValue）类型断言失败自然回退。
	if v.ic.SetCached(obj, key, val) {
		return nil
	}
	// Accessor (getter/setter) interception: if an accessor is found on the
	// prototype chain for this key, invoke the setter with this = obj.
	// For custom value types, search from the backing obj.
	backing := v.backingObj(obj)
	if acc, ok := engine.FindAccessor(backing, key); ok {
		if acc.Setter != nil && !acc.Setter.IsUndefined() {
			_, err := v.invoke(acc.Setter, obj, []engine.Value{val}, false)
			return err
		}
		// Read-only accessor: silently ignore (strict mode would throw).
		return nil
	}
	// Array indexed assignment：委托给 ArrayValue.Set（正确处理追加索引与 length 同步）。
	if arr, ok := obj.(*engine.ArrayValue); ok {
		return arr.Set(key, val)
	}
	if o, ok := obj.AsObject(); ok {
		err := o.Set(key, val)
		v.ic.SetPut(obj, key)
		return err
	}
	// Primitives: silently ignore (strict mode would throw, but we don't enforce).
	return nil
}

func (v *VM) binAdd(l, r engine.Value) engine.Value {
	if l.Type() == engine.TypeString || r.Type() == engine.TypeString {
		return engine.ConcatStrings(l, r)
	}
	// Non-Number operands follow JS ToNumber (undefined -> NaN, null -> 0,
	// string -> StringToNumber, boolean -> 0/1, object -> ToPrimitive);
	// the plain Float() path silently treated conversion failures as 0
	// (1 + undefined returned 1 instead of NaN).
	return v.num(jsToNumber(l) + jsToNumber(r))
}

func (v *VM) instanceof(l, r engine.Value) bool {
	ro, ok := r.AsObject()
	if !ok {
		return false
	}
	// Proxy interception: if r is a Proxy, get [Symbol.hasInstance] via the
	// get trap (passing the actual Symbol value so the handler can compare
	// `key === Symbol.hasInstance`). If found and callable, invoke it.
	if p, ok := r.(*ProxyValue); ok {
		hasInstanceVal, err := p.proxyGetSymbol(engine.SymbolHasInstance)
		if err == nil && !hasInstanceVal.IsUndefined() && isCallable(hasInstanceVal) {
			result, err := v.invoke(hasInstanceVal, r, []engine.Value{l}, false)
			if err != nil {
				return false
			}
			b, _ := result.Bool()
			return b
		}
		// No [Symbol.hasInstance]: fall through using the target's prototype.
		r = p.target
		ro, ok = r.AsObject()
		if !ok {
			return false
		}
	}
	// Symbol.hasInstance on the constructor takes precedence over [[Prototype]] walk.
	if hasInstanceVal, err := ro.Get(engine.SymbolHasInstance.SymbolKey()); err == nil && !hasInstanceVal.IsUndefined() && isCallable(hasInstanceVal) {
		result, err := v.invoke(hasInstanceVal, r, []engine.Value{l}, false)
		if err != nil {
			return false
		}
		b, _ := result.Bool()
		return b
	}
	proto, err := ro.Get("prototype")
	if err != nil || proto.IsUndefined() {
		return false
	}
	protoObj, ok := proto.(engine.Object)
	if !ok {
		return false
	}
	cur := v.getProto(l)
	for cur != nil {
		if cur == protoObj {
			return true
		}
		cur = v.getProto(cur)
	}
	return false
}

// getProto returns the [[Prototype]] of a value, handling custom value types
// (PromiseValue, GeneratorValue, MapValue, SetValue, WeakMapValue, WeakSetValue)
// whose proto lives on a backing object.
func (v *VM) getProto(val engine.Value) engine.Object {
	if p, ok := val.(*ProxyValue); ok {
		proto, err := p.proxyGetProto()
		if err != nil {
			return nil
		}
		return proto
	}
	if p, ok := val.(*PromiseValue); ok {
		return engine.GetProto(p.obj)
	}
	if g, ok := val.(*GeneratorValue); ok {
		return engine.GetProto(g.obj)
	}
	if m, ok := val.(*MapValue); ok {
		return engine.GetProto(m.obj)
	}
	if s, ok := val.(*SetValue); ok {
		return engine.GetProto(s.obj)
	}
	if w, ok := val.(*WeakMapValue); ok {
		return engine.GetProto(w.obj)
	}
	if w, ok := val.(*WeakSetValue); ok {
		return engine.GetProto(w.obj)
	}
	if r, ok := val.(*RegexpValue); ok {
		return engine.GetProto(r.obj)
	}
	return engine.GetProto(val)
}

func (v *VM) inOp(l, r engine.Value) bool {
	has, _ := v.hasProperty(l, r)
	return has
}

func (v *VM) hasProperty(l, r engine.Value) (bool, error) {
	// Proxy interception: dispatch to the has trap if defined.
	if p, ok := r.(*ProxyValue); ok {
		return p.proxyHas(propertyKeyOf(l))
	}
	o, ok := r.AsObject()
	if !ok {
		return false, nil
	}
	key := propertyKeyOf(l)
	cur := o
	for cur != nil {
		if engine.HasOwnProperty(cur, key) {
			return true, nil
		}
		cur = v.getProto(cur)
	}
	return false, nil
}
