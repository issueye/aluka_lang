// 普通对象：Shape/slots 表示、构造入口、原型链读写与 for-in 键枚举。

package engine

import (
	"strconv"
	"strings"
)

// objectValue 是 JS Object 的实现：隐藏类（Shape）+ 槽位数组。
// 同类对象共享 Shape，属性访问经 shape.index 映射 O(1)。
type objectValue struct {
	shape   *Shape
	slots   []Value
	deleted map[string]bool // 对象级已删除属性（避免污染共享 Shape）
	proto   Object          // [[Prototype]]
	// attrs 记录经 defineProperty 约束过、偏离默认标志的属性
	// （writable/enumerable/configurable）。惰性分配：普通对象无此 map，
	// 热路径（IC 直写/Keys）只多一次 nil 判断。
	attrs map[string]PropAttrs
	// nonExtensible models [[Extensible]] without penalizing the default case.
	nonExtensible bool
	// small 是小对象（≤4 属性）的内嵌槽位后备：slots 指向它即可省去
	// 一次独立 slice 分配（字面量/短生命周期对象分配热路径的主力开销）。
	// 超过 4 属性时 append 自动迁移到独立堆数组，语义与纯 slice 一致。
	small [4]Value
}

// initSlots 惰性把 slots 指向内嵌后备（首次添加属性时调用）。
func (o *objectValue) initSlots() {
	if o.slots == nil {
		o.slots = o.small[:0]
	}
}

// NewObject creates an empty JS object.
func NewObject() Object {
	o := &objectValue{shape: rootShape}
	register(o)
	return o
}

// NewObjectFromPairs builds a plain object from alternating string-key/value
// entries. It resolves the final shape first and allocates the slots exactly
// once, which avoids repeated slice growth for object literals.
func NewObjectFromPairs(pairs []Value) Object {
	shape := rootShape
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i].String()
		if _, exists := shape.lookup(key); !exists {
			shape = shape.transition(key)
		}
	}

	o := &objectValue{shape: shape}
	n := shape.NumProps()
	if n <= len(o.small) {
		o.slots = o.small[:n]
	} else {
		o.slots = make([]Value, n)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		idx, _ := shape.lookup(pairs[i].String())
		o.slots[idx] = pairs[i+1]
	}
	register(o)
	return o
}

// ResolveLiteralShape 解析对象字面量的最终 shape 与 pair→slot 索引。
// 供 VM 字面量站点缓存使用：首次解析后缓存，后续经 NewObjectFromShape
// 免哈希/免 transition 行走直接构建。
func ResolveLiteralShape(pairs []Value) (*Shape, []int32) {
	shape := rootShape
	idxs := make([]int32, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i].String()
		if idx, exists := shape.lookup(key); exists {
			idxs = append(idxs, int32(idx))
			continue
		}
		idxs = append(idxs, int32(shape.NumProps()))
		shape = shape.transition(key)
	}
	return shape, idxs
}

// NewObjectWithProto creates an empty JS object with the given prototype.
func NewObjectWithProto(proto Object) Object {
	o := &objectValue{shape: rootShape, proto: proto}
	register(o)
	return o
}

// NewObjectFromShape 以预解析的 shape 与 pair→slot 索引构建对象
// （字面量站点缓存命中路径）。
func NewObjectFromShape(shape *Shape, idxs []int32, pairs []Value) Object {
	return NewObjectFromShapeWithProto(shape, idxs, pairs, nil)
}

// NewObjectFromShapeWithProto 以预解析的 shape、pair→slot 索引和 prototype 构建对象
// （字面量站点缓存命中路径）。
func NewObjectFromShapeWithProto(shape *Shape, idxs []int32, pairs []Value, proto Object) Object {
	o := &objectValue{shape: shape, proto: proto}
	n := shape.NumProps()
	if n <= len(o.small) {
		o.slots = o.small[:n]
	} else {
		o.slots = make([]Value, n)
	}
	for j, idx := range idxs {
		if int(idx) < n {
			o.slots[idx] = pairs[2*j+1]
		}
	}
	register(o)
	return o
}

// NewObjectFrom creates an object from a map (random order).
func NewObjectFrom(m map[string]Value) Object {
	o := NewObject()
	for k, v := range m {
		_ = o.Set(k, v)
	}
	return o
}

// Proto returns the [[Prototype]] of the object (may be nil).
func (o *objectValue) Proto() Object { return o.proto }

// SetProto sets the [[Prototype]] of the object.
func (o *objectValue) SetProto(p Object) { o.proto = p }

// SetProto sets the [[Prototype]] on any value whose concrete type supports it.
func SetProto(obj Value, proto Object) {
	if setter, ok := obj.(interface{ SetProto(Object) }); ok {
		setter.SetProto(proto)
	}
}

// TrySetProto applies [[SetPrototypeOf]] restrictions and reports ordinary rejection.
func TrySetProto(obj Value, proto Object) bool {
	current := GetProto(obj)
	if current == proto {
		return true
	}
	if o, ok := obj.AsObject(); ok && !IsExtensible(o) {
		return false
	}
	for cur := proto; cur != nil; cur = GetProto(cur) {
		if cur == obj {
			return false
		}
	}
	SetProto(obj, proto)
	return true
}

// GetProto returns the [[Prototype]] of the value, or nil if not available.
func GetProto(obj Value) Object {
	if getter, ok := obj.(interface{ Proto() Object }); ok {
		return getter.Proto()
	}
	return nil
}

// EnumerateForInKeys 实现 for-in 头部的 EnumerateObjectProperties（ES2023
// 14.7.5.9）：沿 [[Prototype]] 链收集可枚举字符串键，派生对象键遮蔽同名
// 原型键。null/undefined 返回空；原始值中仅字符串产生索引键（UTF-16 单位
// 计）。链深上限防御原型环（超出按枚举完毕处理）。
func EnumerateForInKeys(v Value) []string {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return nil
	}
	if !v.IsObject() {
		if v.Type() == TypeString {
			// 索引键按 UTF-16 code unit 计（星面字符占 2 个）。
			units := 0
			for _, r := range v.String() {
				if r > 0xFFFF {
					units += 2
				} else {
					units++
				}
			}
			keys := make([]string, 0, units)
			for i := 0; i < units; i++ {
				keys = append(keys, strconv.Itoa(i))
			}
			return keys
		}
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for depth := 0; depth < 128 && v != nil; depth++ {
		o, ok := v.AsObject()
		if !ok {
			break
		}
		for _, k := range o.Keys() {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
		v = GetProto(o)
	}
	return out
}

func (o *objectValue) Type() ValueType { return TypeObject }

// String 返回对象的字符串表示（简化版，类似 Node util.inspect）。
func (o *objectValue) String() string {
	if !markStringify(o) {
		return "[Circular]"
	}
	defer unmarkStringify(o)
	names := o.shape.names
	if len(names) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{ ")
	first := true
	for i, name := range names {
		if o.deleted[name] {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(inspectValue(o.slots[i]))
	}
	b.WriteString(" }")
	return b.String()
}

func (o *objectValue) Int() (int, bool) { return 0, false }

func (o *objectValue) Float() (float64, bool) { return 0, false }

func (o *objectValue) Bool() (bool, bool) { return true, true } // ToBoolean(object) = true

func (o *objectValue) IsUndefined() bool { return false }

func (o *objectValue) IsNull() bool { return false }

func (o *objectValue) IsObject() bool { return true }

func (o *objectValue) IsFunction() bool { return false }

func (o *objectValue) AsObject() (Object, bool) { return o, true }

func (o *objectValue) AsFunction() (Function, bool) { return nil, false }

// getSlot 读取本对象 own 属性（含 deleted 检查）。
func (o *objectValue) getSlot(key string) (Value, bool) {
	if o.deleted != nil && o.deleted[key] {
		return Undefined(), false
	}
	idx, ok := o.shape.lookup(key)
	if !ok {
		return Undefined(), false
	}
	return o.slots[idx], true
}

// GetSlot 读取本对象 own 属性（供外部/VM 原型链遍历使用）。
func (o *objectValue) GetSlot(key string) (Value, bool) {
	return o.getSlot(key)
}

// GetOwnSlot 尝试从值中直接读取自有属性（不走原型链）。
func GetOwnSlot(val Value, key string) (Value, bool) {
	if a, ok := val.(*ArrayValue); ok {
		if key == "length" {
			return IntValue(len(a.elems)), true
		}
		if idx, isIndex := arrayIndex(key); isIndex && idx < len(a.elems) && a.isPresent(idx) {
			return a.elems[idx], true
		}
		return a.objectValue.getSlot(key)
	}
	if o, ok := val.(*objectValue); ok {
		return o.getSlot(key)
	}
	if f, ok := val.(*functionValue); ok {
		return f.objectValue.getSlot(key)
	}
	// 包装型对象（Closure/NativeMethod 等）：解包后读底层存储。
	if uw, ok := val.(ObjectUnwrapper); ok {
		if inner := uw.UnwrapObject(); inner != nil {
			return GetOwnSlot(inner, key)
		}
	}
	return Undefined(), false
}

// setSlot 写入本对象 own 属性；不存在时经 Shape transition 添加。
func (o *objectValue) setSlot(key string, value Value) {
	if o.deleted != nil && o.deleted[key] {
		// 复用原槽位。
		if idx, ok := o.shape.lookup(key); ok {
			delete(o.deleted, key)
			o.slots[idx] = value
			return
		}
	}
	if idx, ok := o.shape.lookup(key); ok {
		o.slots[idx] = value
		return
	}
	o.shape = o.shape.transition(key)
	o.initSlots()
	o.slots = append(o.slots, value)
}

func (o *objectValue) Get(key string) (Value, error) {
	// Walk own + prototype chain.
	cur := o
	for cur != nil {
		if v, ok := cur.getSlot(key); ok {
			return v, nil
		}
		if cur.proto == nil {
			break
		}
		if p, ok := cur.proto.(*objectValue); ok {
			cur = p
			continue
		}
		// 非 objectValue 原型（函数对象/闭包等作为原型，如 chalk 的
		// Object.setPrototypeOf(builder, proto)）：委托其 Get 继续沿链查找，
		// 否则原型链会在此断掉，导致访问器/方法无法解析。
		return cur.proto.Get(key)
	}
	return Undefined(), nil
}

func (o *objectValue) Set(key string, value Value) error {
	// writable:false 拦截（sloppy 语义：静默忽略；严格模式 TypeError 待
	// VM 严格性建模后接入）。IC 快路径（SetCached）有同款守卫。
	if a, ok := o.attrs[key]; ok && !a.Writable {
		return nil
	}
	if _, exists := o.getSlot(key); !exists && o.nonExtensible {
		return nil
	}
	o.setSlot(key, value)
	return nil
}

func (o *objectValue) Keys() []string {
	names := o.shape.names
	out := make([]string, 0, len(names))
	for _, name := range names {
		if o.deleted != nil && o.deleted[name] {
			continue
		}
		if IsSymbolKey(name) {
			continue
		}
		if a, ok := o.attrs[name]; ok && !a.Enumerable {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Delete removes an own property. Returns true if the property was removed or
// did not exist; false if the property is non-configurable.
func (o *objectValue) Delete(key string) bool {
	if _, ok := o.getSlot(key); !ok {
		return true // property doesn't exist — delete returns true
	}
	if a, ok := o.attrs[key]; ok && !a.Configurable {
		return false
	}
	if o.deleted == nil {
		o.deleted = make(map[string]bool)
	}
	o.deleted[key] = true
	delete(o.attrs, key)
	return true
}

// attrOf 返回属性当前生效标志（无约束条目时为默认全 true）。
func (o *objectValue) attrOf(key string) PropAttrs {
	if a, ok := o.attrs[key]; ok {
		return a
	}
	return defaultPropAttrs
}
