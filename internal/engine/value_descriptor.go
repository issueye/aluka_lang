// 属性描述符语义：DefineOwnProperty（含 Array 分支）、描述符兼容性校验与 seal/freeze 完整性级别。

package engine

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// embeddedObjectValue 从嵌入 objectValue 的类型中取出其 *objectValue。
func embeddedObjectValue(obj Object) *objectValue {
	switch v := obj.(type) {
	case *functionValue:
		return &v.objectValue
	case *ArrayValue:
		return &v.objectValue
	case *BufferValue:
		return v.objectValue
	}
	return nil
}

// PropAttrs 是属性标志（[[Writable]]/[[Enumerable]]/[[Configurable]]）。
// objectValue.attrs 中仅存偏离默认（全 true）的条目；无条目即默认。
type PropAttrs struct {
	Writable     bool
	Enumerable   bool
	Configurable bool
}

var defaultPropAttrs = PropAttrs{Writable: true, Enumerable: true, Configurable: true}

// ObjectUnwrapper 由"包装型"对象实现（如解释器的 Closure/NativeMethod——
// 持有一个底层 engine.Object 承载属性存储，自身仅做委托）。engine 侧的
// 描述符操作（DefineOwnProperty/AttrsOf/AllOwnKeys/GetOwnSlot）经它取到
// 真实存储，避免包装类型走退化路径丢失标志语义。
type ObjectUnwrapper interface {
	UnwrapObject() Object
}

// unwrapObjectValue 解析对象的真实属性存储：直接 objectValue、嵌入类型
// （functionValue/ArrayValue/BufferValue）或经 ObjectUnwrapper 解包（一层）。
func unwrapObjectValue(obj Object) *objectValue {
	if ov, ok := obj.(*objectValue); ok {
		return ov
	}
	if ov := embeddedObjectValue(obj); ov != nil {
		return ov
	}
	if uw, ok := obj.(ObjectUnwrapper); ok {
		if inner := uw.UnwrapObject(); inner != nil && inner != obj {
			return unwrapObjectValue(inner)
		}
	}
	return nil
}

// AttrsOf 返回对象自有属性的生效标志（无法解析存储时恒为默认）。
func AttrsOf(obj Object, key string) PropAttrs {
	// 数组优先：length 标志由 lengthWritable 承载，不在嵌入 objectValue
	// 的 attrs map 中，必须经 ArrayValue.attrOf 覆写读取。
	if a, ok := obj.(*ArrayValue); ok {
		return a.attrOf(key)
	}
	if ov := unwrapObjectValue(obj); ov != nil {
		return ov.attrOf(key)
	}
	return defaultPropAttrs
}

// AllOwnKeys 返回全部自有属性名（含不可枚举；不含已删除）。
// 与 Object.Keys()（仅可枚举）互补，供 getOwnPropertyNames 等使用。
func AllOwnKeys(obj Object) []string {
	if a, ok := obj.(*ArrayValue); ok {
		out := make([]string, 0, len(a.elems)+len(a.shape.names))
		for i := range a.elems {
			if a.isPresent(i) {
				out = append(out, strconv.Itoa(i))
			}
		}
		out = append(out, "length")
		for _, name := range a.shape.names {
			if name == "length" {
				continue
			}
			if a.isDeleted(name) {
				continue
			}
			out = append(out, name)
		}
		return out
	}
	if uw, ok := obj.(ObjectUnwrapper); ok {
		if inner := uw.UnwrapObject(); inner != nil && inner != obj {
			return AllOwnKeys(inner)
		}
	}
	ov := unwrapObjectValue(obj)
	if ov == nil {
		return obj.Keys()
	}
	out := make([]string, 0, len(ov.shape.names))
	for _, name := range ov.shape.names {
		if ov.isDeleted(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Descriptor 是 defineProperty 的属性描述符。Has* 标记字段是否出现
// （部分描述符语义：未出现的字段保留现值）。
type Descriptor struct {
	HasValue, HasWritable, HasEnumerable, HasConfigurable bool
	HasGet, HasSet                                        bool
	Value                                                 Value
	Writable, Enumerable, Configurable                    bool
	Get, Set                                              Value
}

// sameValue 是 SameValue 的窄实现（数值含 NaN 相等；其余按值/指针相等）。
// numberValue 是包着 *numberBox 的值类型（接口存值形态），必须按数值比较，
// 指针/结构体相等性在这里都不可靠。
func sameValue(a, b Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	numOf := func(v Value) (float64, bool) {
		switch n := v.(type) {
		case numberValue:
			f, _ := n.Float()
			return f, true
		case *numberValue:
			f, _ := n.Float()
			return f, true
		}
		return 0, false
	}
	af, aNum := numOf(a)
	bf, bNum := numOf(b)
	if aNum || bNum {
		if !aNum || !bNum {
			return false
		}
		if af == bf {
			if af == 0 {
				return math.Signbit(af) == math.Signbit(bf)
			}
			return true
		}
		return math.IsNaN(af) && math.IsNaN(bf)
	}
	if a.Type() != b.Type() {
		return false
	}
	return a == b
}

// ErrDefineRejected marks an ordinary [[DefineOwnProperty]] rejection. Object.defineProperty
// converts it to TypeError, while Reflect.defineProperty reports false.
var ErrDefineRejected = errors.New("property definition rejected")

// IsDefineRejected reports whether err represents an ordinary descriptor rejection.
func IsDefineRejected(err error) bool { return errors.Is(err, ErrDefineRejected) }

func defineRejected(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDefineRejected, fmt.Sprintf(format, args...))
}

// SameValue reports ECMAScript SameValue equality for descriptor invariant checks.
func SameValue(a, b Value) bool { return sameValue(a, b) }

// IsCompatibleDescriptor checks whether d may describe key without changing target.
func IsCompatibleDescriptor(obj Object, key string, d Descriptor) bool {
	current, exists := OwnPropertyDescriptor(obj, key)
	if !exists {
		return IsExtensible(obj)
	}
	if current.Configurable {
		return true
	}
	if d.HasConfigurable && d.Configurable || d.HasEnumerable && d.Enumerable != current.Enumerable {
		return false
	}
	currentAccessor := current.HasGet || current.HasSet
	descAccessor := d.HasGet || d.HasSet
	descData := d.HasValue || d.HasWritable
	if descAccessor && !currentAccessor || descData && currentAccessor {
		return false
	}
	if currentAccessor {
		if d.HasGet && !sameValue(orUndefined(d.Get), orUndefined(current.Get)) || d.HasSet && !sameValue(orUndefined(d.Set), orUndefined(current.Set)) {
			return false
		}
		return true
	}
	if !current.Writable {
		if d.HasWritable && d.Writable || d.HasValue && !sameValue(d.Value, current.Value) {
			return false
		}
	}
	return true
}

// DefineOwnProperty 实现 [[DefineOwnProperty]] 的规范子集：
// 部分描述符合并、accessor/data 互斥校验、非可配置重定义限制、
// 新属性缺省标志为 false。返回 nil 表示成功。
func DefineOwnProperty(obj Object, key string, d Descriptor) error {
	if a, ok := obj.(*ArrayValue); ok {
		return defineArrayOwnProperty(a, key, d)
	}
	if uw, ok := obj.(ObjectUnwrapper); ok {
		if inner := uw.UnwrapObject(); inner != nil && inner != obj {
			return DefineOwnProperty(inner, key, d)
		}
	}
	ov := unwrapObjectValue(obj)
	if ov == nil {
		// 非 objectValue 存储：退化为纯数据写入（保持既有兼容）。
		if d.HasValue {
			return obj.Set(key, d.Value)
		}
		return nil
	}

	// 校验：accessor 与 data 字段互斥。
	if (d.HasGet || d.HasSet) && (d.HasValue || d.HasWritable) {
		return fmt.Errorf("%w: Invalid property descriptor. Cannot both specify accessors and a value or writable attribute", ErrTypeError)
	}
	if d.HasGet && !d.Get.IsUndefined() && !d.Get.IsFunction() {
		return fmt.Errorf("%w: Getter must be a function", ErrTypeError)
	}
	if d.HasSet && !d.Set.IsUndefined() && !d.Set.IsFunction() {
		return fmt.Errorf("%w: Setter must be a function", ErrTypeError)
	}

	curVal, exists := ov.getSlot(key)
	if !exists && ov.isNonExtensible() {
		return defineRejected("Cannot define property %s, object is not extensible", key)
	}
	curIsAcc := exists && IsAccessorValue(curVal)
	cur := ov.attrOf(key)

	// 非可配置属性的重定义限制。
	if exists && !cur.Configurable {
		if d.HasConfigurable && d.Configurable {
			return defineRejected("Cannot redefine property: %s is not configurable", key)
		}
		if d.HasEnumerable && d.Enumerable != cur.Enumerable {
			return defineRejected("Cannot redefine property: %s is not configurable", key)
		}
		if (d.HasGet || d.HasSet) && !curIsAcc {
			return defineRejected("Cannot redefine property: %s is not configurable", key)
		}
		if (d.HasValue || d.HasWritable) && curIsAcc {
			return defineRejected("Cannot redefine property: %s is not configurable", key)
		}
		if curIsAcc {
			acc := curVal.(*AccessorValue)
			if d.HasGet && !sameValue(orUndefined(d.Get), orUndefined(acc.Getter)) {
				return defineRejected("Cannot redefine property: %s is not configurable", key)
			}
			if d.HasSet && !sameValue(orUndefined(d.Set), orUndefined(acc.Setter)) {
				return defineRejected("Cannot redefine property: %s is not configurable", key)
			}
		} else {
			if !cur.Writable {
				if d.HasWritable && d.Writable {
					return defineRejected("Cannot redefine property: %s is not configurable", key)
				}
				if d.HasValue && !sameValue(d.Value, curVal) {
					return defineRejected("Cannot redefine property: %s is not configurable", key)
				}
			}
		}
	}

	// 合成生效标志：新属性未指定的字段缺省 false。
	eff := cur
	if !exists {
		eff = PropAttrs{}
	}
	// 属性种类转换时，未指定的种类特有标志使用新种类缺省值。
	if exists && curIsAcc && (d.HasValue || d.HasWritable) {
		eff.Writable = false
	}
	if d.HasWritable {
		eff.Writable = d.Writable
	}
	if d.HasEnumerable {
		eff.Enumerable = d.Enumerable
	}
	if d.HasConfigurable {
		eff.Configurable = d.Configurable
	}

	// 应用属性体。
	if d.HasGet || d.HasSet {
		g, s := Undefined(), Undefined()
		if curIsAcc {
			acc := curVal.(*AccessorValue)
			g, s = acc.Getter, acc.Setter
		}
		if d.HasGet {
			g = orUndefined(d.Get)
		}
		if d.HasSet {
			s = orUndefined(d.Set)
		}
		ov.setSlot(key, NewAccessor(g, s))
	} else if d.HasValue {
		ov.setSlot(key, d.Value)
	} else if !exists {
		// 新属性且描述符无 value（纯标志约束）：值为 undefined。
		ov.setSlot(key, Undefined())
	}

	// attrs 收敛：全默认则移除条目，保持热路径零开销。
	ov.setAttr(key, eff)
	return nil
}

func defineArrayOwnProperty(a *ArrayValue, key string, d Descriptor) error {
	if key != "length" {
		idx, isIndex := arrayIndex(key)
		if !isIndex {
			return DefineOwnProperty(&a.objectValue, key, d)
		}
		if (d.HasGet || d.HasSet) && (d.HasValue || d.HasWritable) {
			return fmt.Errorf("%w: Invalid property descriptor", ErrTypeError)
		}
		if d.HasGet && !orUndefined(d.Get).IsUndefined() && !d.Get.IsFunction() || d.HasSet && !orUndefined(d.Set).IsUndefined() && !d.Set.IsFunction() {
			return fmt.Errorf("%w: accessor must be a function or undefined", ErrTypeError)
		}
		value, exists := GetOwnSlot(a, key)
		if !exists && (a.isNonExtensible() || idx >= len(a.elems) && !a.attrOf("length").Writable) {
			return defineRejected("Cannot define property %s", key)
		}
		cur := a.attrOf(key)
		if !exists {
			cur = PropAttrs{}
		}
		curIsAcc := exists && IsAccessorValue(value)
		if exists && !cur.Configurable {
			if d.HasConfigurable && d.Configurable || d.HasEnumerable && d.Enumerable != cur.Enumerable ||
				(d.HasGet || d.HasSet) && !curIsAcc || (d.HasValue || d.HasWritable) && curIsAcc {
				return defineRejected("Cannot redefine property: %s is not configurable", key)
			}
			if curIsAcc {
				acc := value.(*AccessorValue)
				if d.HasGet && !sameValue(orUndefined(d.Get), orUndefined(acc.Getter)) || d.HasSet && !sameValue(orUndefined(d.Set), orUndefined(acc.Setter)) {
					return defineRejected("Cannot redefine property: %s is not configurable", key)
				}
			} else if !cur.Writable && (d.HasWritable && d.Writable || d.HasValue && !sameValue(d.Value, value)) {
				return defineRejected("Cannot redefine property: %s is not configurable", key)
			}
		}
		eff := cur
		if curIsAcc && (d.HasValue || d.HasWritable) {
			eff.Writable = false
		}
		if d.HasWritable {
			eff.Writable = d.Writable
		}
		if d.HasEnumerable {
			eff.Enumerable = d.Enumerable
		}
		if d.HasConfigurable {
			eff.Configurable = d.Configurable
		}
		newValue := value
		if d.HasGet || d.HasSet {
			getter, setter := Undefined(), Undefined()
			if curIsAcc {
				acc := value.(*AccessorValue)
				getter, setter = acc.Getter, acc.Setter
			}
			if d.HasGet {
				getter = orUndefined(d.Get)
			}
			if d.HasSet {
				setter = orUndefined(d.Set)
			}
			newValue = NewAccessor(getter, setter)
		} else if d.HasValue {
			newValue = d.Value
		} else if !exists {
			newValue = Undefined()
		}
		if len(a.elems) <= idx {
			a.materializePresent()
			for len(a.elems) <= idx {
				a.elems = append(a.elems, Undefined())
				a.present = append(a.present, false)
			}
		}
		a.elems[idx] = newValue
		if a.present != nil {
			a.present[idx] = true
		}
		// attrs 收敛：全默认仅移除条目；length 不在 map 中，索引约束才物化 map。
		a.setAttr(key, eff)
		a.objectValue.setSlot("length", IntValue(len(a.elems)))
		return nil
	}

	if d.HasGet || d.HasSet || d.HasEnumerable && d.Enumerable || d.HasConfigurable && d.Configurable {
		return defineRejected("Cannot redefine property: length")
	}
	cur := a.attrOf("length")
	if !cur.Writable && d.HasWritable && d.Writable {
		return defineRejected("Cannot redefine property: length")
	}
	if d.HasValue {
		f, ok := d.Value.Float()
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 || f > float64(uint64(1)<<32-1) {
			return fmt.Errorf("%w: invalid array length", ErrRangeError)
		}
		n := int(f)
		if !cur.Writable && n != len(a.elems) {
			return defineRejected("Cannot redefine property: length")
		}
		if n < len(a.elems) {
			for i := len(a.elems) - 1; i >= n; i-- {
				if a.isPresent(i) {
					if attrs, ok := a.getAttr(strconv.Itoa(i)); ok && !attrs.Configurable {
						return defineRejected("Cannot delete non-configurable array index %d", i)
					}
				}
			}
			a.elems = a.elems[:n]
			if a.present != nil {
				a.present = a.present[:n]
			}
		} else {
			a.materializePresent()
			for len(a.elems) < n {
				a.elems = append(a.elems, Undefined())
				a.present = append(a.present, false)
			}
		}
		a.objectValue.setSlot("length", IntValue(n))
	}
	if d.HasWritable {
		a.lengthWritable = d.Writable
	}
	return nil
}

// OwnPropertyDescriptor returns an effective own property descriptor.
func OwnPropertyDescriptor(obj Object, key string) (Descriptor, bool) {
	value, exists := GetOwnSlot(obj, key)
	if !exists {
		return Descriptor{}, false
	}
	attrs := AttrsOf(obj, key)
	d := Descriptor{
		HasEnumerable: true, HasConfigurable: true,
		Enumerable: attrs.Enumerable, Configurable: attrs.Configurable,
	}
	if acc, ok := value.(*AccessorValue); ok {
		d.HasGet, d.HasSet = true, true
		d.Get, d.Set = orUndefined(acc.Getter), orUndefined(acc.Setter)
	} else {
		d.HasValue, d.HasWritable = true, true
		d.Value, d.Writable = value, attrs.Writable
	}
	return d, true
}

// HasOwnProperty reports own-property existence independent of enumerability.
func HasOwnProperty(obj Object, key string) bool {
	_, ok := GetOwnSlot(obj, key)
	return ok
}

// IsExtensible reports the object's [[Extensible]] state.
func IsExtensible(obj Object) bool {
	if uw, ok := obj.(ObjectUnwrapper); ok {
		if inner := uw.UnwrapObject(); inner != nil && inner != obj {
			return IsExtensible(inner)
		}
	}
	if ov := unwrapObjectValue(obj); ov != nil {
		return !ov.isNonExtensible()
	}
	return true
}

// PreventExtensions sets [[Extensible]] to false when the backing storage is known.
func PreventExtensions(obj Object) bool {
	if uw, ok := obj.(ObjectUnwrapper); ok {
		if inner := uw.UnwrapObject(); inner != nil && inner != obj {
			return PreventExtensions(inner)
		}
	}
	if ov := unwrapObjectValue(obj); ov != nil {
		ov.setNonExtensible()
		return true
	}
	return false
}

// SetIntegrityLevel implements the shared Object.seal/Object.freeze operation.
func SetIntegrityLevel(obj Object, frozen bool) error {
	if !PreventExtensions(obj) {
		return fmt.Errorf("%w: Cannot prevent extensions", ErrTypeError)
	}
	for _, key := range AllOwnKeys(obj) {
		d := Descriptor{HasConfigurable: true, Configurable: false}
		if frozen {
			if current, ok := OwnPropertyDescriptor(obj, key); ok && current.HasValue {
				d.HasWritable = true
				d.Writable = false
			}
		}
		if err := DefineOwnProperty(obj, key, d); err != nil {
			return err
		}
	}
	return nil
}

// TestIntegrityLevel implements Object.isSealed/Object.isFrozen.
func TestIntegrityLevel(obj Object, frozen bool) bool {
	if IsExtensible(obj) {
		return false
	}
	for _, key := range AllOwnKeys(obj) {
		d, ok := OwnPropertyDescriptor(obj, key)
		if !ok || d.Configurable || frozen && d.HasValue && d.Writable {
			return false
		}
	}
	return true
}

// orUndefined 把 nil 归一为 Undefined。
func orUndefined(v Value) Value {
	if v == nil {
		return Undefined()
	}
	return v
}
