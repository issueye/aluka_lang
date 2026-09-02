// Array exotic object：索引/length 语义、holes 与 attrs 惰性物化、批量写快路径。

package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ArrayValue 是 JS 数组的对象实现（带 length 属性）。
type ArrayValue struct {
	// objectValue 值嵌入（而非指针）：数组与其宿主对象合成一次分配，
	// 省去每个数组一次独立 malloc。经 ArrayValue 指针访问时选择器
	// 自动取址，既有 a.objectValue.xxx 调用点无需改动。
	objectValue
	elems []Value
	// present 为 nil 表示全部元素 present（无洞，绝大多数数组的稳态）；
	// 非 nil 时 false 表示 hole；值为 undefined 的自有属性仍为 true。
	// 首个洞出现时经 materializePresent 物化。
	present []bool
	// lengthWritable 承载 length 的 writable 标志（Enumerable=false、
	// Configurable=false 为数组固有语义，无需存储）；length 不再占用
	// attrs map——此前每个数组为 {length} 单条目 eager 建 map，多付
	// ~260B/2 allocs，是数组分配贵于普通对象 3 倍的主因。
	lengthWritable bool
	// smallElems 是小数组（≤4 元素）的内嵌元素后备：字面量 [a, b] 类
	// 高频短生命周期数组省去独立 elems 分配；更大数组走独立堆数组。
	smallElems [4]Value
}

// NewArray 创建数组对象。elems 长度 ≤4 时拷贝进内嵌后备（调用方可让
// 传入切片留在栈上避免逃逸）；更长时直接接管传入切片（零拷贝）。
// 新建数组无洞：present 保持 nil（全 present 的紧凑表示）。
func NewArray(elems []Value) *ArrayValue {
	a := &ArrayValue{lengthWritable: true}
	a.shape = rootShape
	if len(elems) <= len(a.smallElems) {
		copy(a.smallElems[:], elems)
		a.elems = a.smallElems[:len(elems)]
	} else {
		a.elems = elems
	}
	register(&a.objectValue)
	// 同步 length 属性
	a.setSlot("length", IntValue(len(elems)))
	return a
}

// NewArrayHoles creates an array with length n and no own indexed properties.
func NewArrayHoles(n int) *ArrayValue {
	if n < 0 {
		n = 0
	}
	a := &ArrayValue{lengthWritable: true}
	a.shape = rootShape
	a.elems = make([]Value, n)
	for i := range a.elems {
		a.elems[i] = Undefined()
	}
	a.present = make([]bool, n) // 零值即全 hole
	register(&a.objectValue)
	a.setSlot("length", IntValue(n))
	return a
}

// isPresent reports whether index idx holds an own property.
func (a *ArrayValue) isPresent(idx int) bool {
	return a.present == nil || a.present[idx]
}

// materializePresent 把 nil（全 present）物化为显位图，供写入 hole 或
// 收缩/扩张跨越洞语义前调用。
func (a *ArrayValue) materializePresent() {
	if a.present == nil {
		a.present = make([]bool, len(a.elems))
		for i := range a.present {
			a.present[i] = true
		}
	}
}

// attrOf 覆写嵌入 objectValue 的实现：length 的固有标志（仅 writable
// 可翻转）由 lengthWritable 字段承载，不进 attrs map。
func (a *ArrayValue) attrOf(key string) PropAttrs {
	if key == "length" {
		return PropAttrs{Writable: a.lengthWritable}
	}
	return a.objectValue.attrOf(key)
}

func (a *ArrayValue) Type() ValueType { return TypeObject }

// AsObject 重写以返回数组自身（而非嵌入的 *objectValue），
// 否则后续 Get/Set 会绕过 ArrayValue 的索引处理逻辑。
func (a *ArrayValue) AsObject() (Object, bool) { return a, true }

func (a *ArrayValue) String() string {
	if !markStringify(&a.objectValue) {
		return "[Circular]"
	}
	defer unmarkStringify(&a.objectValue)
	if len(a.elems) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[ ")
	for i, e := range a.elems {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(inspectValue(e))
	}
	b.WriteString(" ]")
	return b.String()
}

// Get 重写以支持数字索引访问（"0", "1", ...）。
func (a *ArrayValue) Get(key string) (Value, error) {
	if key == "length" {
		return IntValue(len(a.elems)), nil
	}
	// 尝试解析为索引
	if idx, ok := arrayIndex(key); ok && idx < len(a.elems) {
		if a.isPresent(idx) {
			return a.elems[idx], nil
		}
		if a.proto != nil {
			return a.proto.Get(key)
		}
		return Undefined(), nil
	}
	return a.objectValue.Get(key)
}

// Set 重写以支持数字索引写入与 length 同步。
func (a *ArrayValue) Set(key string, value Value) error {
	if key == "length" {
		if attrs := a.attrOf("length"); !attrs.Writable {
			return nil
		}
		f, ok := value.Float()
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 || f > float64(uint64(1)<<32-1) {
			return fmt.Errorf("%w: invalid length", ErrTypeError)
		}
		n := int(f)
		if n < len(a.elems) {
			for i := n; i < len(a.elems); i++ {
				if a.isPresent(i) {
					if attrs, ok := a.attrs[strconv.Itoa(i)]; ok && !attrs.Configurable {
						return fmt.Errorf("%w: cannot delete array index %d", ErrTypeError, i)
					}
				}
			}
			a.elems = a.elems[:n]
			if a.present != nil {
				a.present = a.present[:n]
			}
		} else {
			a.materializePresent()
			for i := len(a.elems); i < n; i++ {
				a.elems = append(a.elems, Undefined())
				a.present = append(a.present, false)
			}
		}
		a.objectValue.setSlot("length", IntValue(n))
		return nil
	}
	if idx, ok := arrayIndex(key); ok {
		if idx < len(a.elems) {
			if attrs, constrained := a.attrs[key]; constrained && !attrs.Writable {
				return nil
			}
		} else if a.nonExtensible || !a.attrOf("length").Writable {
			return nil
		}
		if len(a.elems) <= idx {
			a.materializePresent()
			for len(a.elems) <= idx {
				a.elems = append(a.elems, Undefined())
				a.present = append(a.present, false)
			}
		}
		a.elems[idx] = value
		if a.present != nil {
			a.present[idx] = true
		}
		a.objectValue.setSlot("length", IntValue(len(a.elems)))
		return nil
	}
	return a.objectValue.Set(key, value)
}

func (a *ArrayValue) Keys() []string {
	out := make([]string, 0, len(a.elems))
	for i := range a.elems {
		if !a.isPresent(i) {
			continue
		}
		key := strconv.Itoa(i)
		if attrs, ok := a.attrs[key]; !ok || attrs.Enumerable {
			out = append(out, key)
		}
	}
	// 合并对象自有属性（负索引/非规范键，如 jsdiff 的 bestPath[-1]）。
	// length 是数组固有非枚举属性，不再经 attrs map 过滤，这里显式排除；
	// seen 保持其余属性去重。
	seen := make(map[string]bool, len(out))
	for _, k := range out {
		seen[k] = true
	}
	for _, k := range a.objectValue.Keys() {
		if k == "length" {
			continue
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Delete removes an array index or delegates ordinary properties to the
// embedded object storage.
func (a *ArrayValue) Delete(key string) bool {
	if key == "length" {
		return false
	}
	if idx, ok := arrayIndex(key); ok && idx < len(a.elems) {
		if !a.isPresent(idx) {
			return true
		}
		if attrs, constrained := a.attrs[key]; constrained && !attrs.Configurable {
			return false
		}
		a.materializePresent()
		a.elems[idx] = Undefined()
		a.present[idx] = false
		delete(a.attrs, key)
		return true
	}
	return a.objectValue.Delete(key)
}

func arrayIndex(key string) (int, bool) {
	if key == "" || key == "-0" {
		return 0, false
	}
	idx64, err := strconv.ParseInt(key, 10, 64)
	if err != nil || idx64 < 0 || idx64 > int64(^uint32(0)-1) {
		return 0, false
	}
	idx := int(idx64)
	if strconv.Itoa(idx) != key {
		return 0, false
	}
	return idx, true
}

// Elems 返回数组元素切片（只读视图）。
func (a *ArrayValue) Elems() []Value { return a.elems }

// CanAppend reports whether Array.prototype.push/JIT may create count trailing indices.
func (a *ArrayValue) CanAppend(count int) bool {
	if count < 0 || uint64(len(a.elems))+uint64(count) > uint64(1)<<32-1 {
		return false
	}
	return !a.nonExtensible && a.attrOf("length").Writable
}

// HasTrailingIndexAttrs reports whether any of the next count indices already
// have own property attributes (Object.defineProperty). push 快路径仅在无
// 自定义描述符时才能 Append；否则必须走 Set 以遵守 writable/accessor。
func (a *ArrayValue) HasTrailingIndexAttrs(count int) bool {
	if count <= 0 || len(a.attrs) == 0 {
		return false
	}
	start := len(a.elems)
	end := start + count
	for key := range a.attrs {
		if key == "length" {
			continue
		}
		idx, ok := arrayIndex(key)
		if ok && idx >= start && idx < end {
			return true
		}
	}
	return false
}

// CanWriteRange reports whether a bulk JIT write matches individual assignments.
func (a *ArrayValue) CanWriteRange(start, count int) bool {
	if start < 0 || count < 0 || uint64(start)+uint64(count) > uint64(1)<<32-1 {
		return false
	}
	for i := start; i < start+count && i < len(a.elems); i++ {
		if a.isPresent(i) {
			d, _ := OwnPropertyDescriptor(a, strconv.Itoa(i))
			if d.HasGet || !d.Writable {
				return false
			}
		} else if a.nonExtensible {
			return false
		}
	}
	return start+count <= len(a.elems) || a.CanAppend(start+count-len(a.elems))
}

// IsFullyWritable reports whether mutating Array methods may update all elements.
func (a *ArrayValue) IsFullyWritable() bool {
	if !a.attrOf("length").Writable {
		return false
	}
	for i := range a.elems {
		if !a.isPresent(i) {
			if a.nonExtensible {
				return false
			}
			continue
		}
		d, _ := OwnPropertyDescriptor(a, strconv.Itoa(i))
		if d.HasGet || !d.Writable || !d.Configurable {
			return false
		}
	}
	return true
}

// Append appends a value to the array and updates the length property.
func (a *ArrayValue) Append(v Value) {
	a.elems = append(a.elems, v)
	if a.present != nil {
		a.present = append(a.present, true)
	}
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
}

// AppendValues appends vs in order and synchronizes length once.
func (a *ArrayValue) AppendValues(vs []Value) {
	if len(vs) == 0 {
		return
	}
	if len(vs) == 1 {
		a.Append(vs[0])
		return
	}
	oldLen := len(a.elems)
	n := len(vs)
	if cap(a.elems)-oldLen < n {
		grown := make([]Value, oldLen, oldLen+n)
		copy(grown, a.elems)
		a.elems = grown
	}
	a.elems = a.elems[:oldLen+n]
	copy(a.elems[oldLen:], vs)
	if a.present != nil {
		a.present = append(a.present, make([]bool, n)...)
		for i := 0; i < n; i++ {
			a.present[oldLen+i] = true
		}
	}
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
}

// SetIndex writes a value to a numeric array index, growing the slice with
// holes (undefined) when idx exceeds the current length, and synchronizes the
// length property once. It mirrors the numeric-key branch of Set without the
// string conversion, for the VM's array-index write fast path (M1-2 写侧).
func (a *ArrayValue) SetIndex(idx int, value Value) {
	key := strconv.Itoa(idx)
	if idx < len(a.elems) {
		if attrs, ok := a.attrs[key]; ok && !attrs.Writable {
			return
		}
	} else if a.nonExtensible || !a.attrOf("length").Writable {
		return
	}
	for len(a.elems) <= idx {
		if a.present == nil {
			a.materializePresent()
		}
		a.elems = append(a.elems, Undefined())
		a.present = append(a.present, false)
	}
	a.elems[idx] = value
	if a.present != nil {
		a.present[idx] = true
	}
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
}

// AppendNumberRange appends count consecutive Number values starting at start
// and updates length once. It is intentionally narrow: the interpreter JIT
// uses it only after guarding the ArrayValue receiver and loop semantics.
func (a *ArrayValue) AppendNumberRange(start float64, count int) {
	if count <= 0 {
		return
	}
	oldLen := len(a.elems)
	if cap(a.elems)-oldLen < count {
		grown := make([]Value, oldLen, oldLen+count)
		copy(grown, a.elems)
		a.elems = grown
	}
	a.elems = a.elems[:oldLen+count]
	if a.present != nil {
		a.present = append(a.present, make([]bool, count)...)
	}
	for i := 0; i < count; i++ {
		a.elems[oldLen+i] = newNumber(start + float64(i))
		if a.present != nil {
			a.present[oldLen+i] = true
		}
	}
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
}

// WriteNumberRange fills elems[start:start+count] with count consecutive
// Number values starting at valueStart (elems[start+i] = valueStart + i),
// growing the slice with holes (undefined) first when start+count exceeds the
// current length, and synchronizes the length property once. It mirrors the
// per-write semantics of Set("k", v) for numeric keys (extend with holes,
// fill, length slot sync) but commits the whole range atomically, so the
// observable final state is identical to the per-iteration Tier 0 sequence
// while the length slot is updated a single time. Like AppendNumberRange it is
// intentionally narrow: the interpreter JIT uses it only after guarding the
// ArrayValue receiver, the safe-integer index range and the loop semantics.
func (a *ArrayValue) WriteNumberRange(start int, valueStart float64, count int) {
	if count <= 0 {
		return
	}
	oldLen := len(a.elems)
	end := start + count
	if end > oldLen {
		a.materializePresent()
		if cap(a.elems)-oldLen < end-oldLen {
			grown := make([]Value, oldLen, end)
			copy(grown, a.elems)
			a.elems = grown
		}
		a.elems = a.elems[:end]
		a.present = append(a.present, make([]bool, end-oldLen)...)
		for i := oldLen; i < end; i++ {
			a.elems[i] = Undefined() // holes above the previous length
		}
	}
	for i := 0; i < count; i++ {
		a.elems[start+i] = newNumber(valueStart + float64(i))
		if a.present != nil {
			a.present[start+i] = true
		}
	}
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
}
