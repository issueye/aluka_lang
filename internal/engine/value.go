package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// === 值类型实现 ============================================================
//
// 以下类型实现 Value/Object/Function 接口。
// Phase 0 提供最小可用集合；Phase 1 起将由自研 VM 内部表示替换。

// --- undefined -------------------------------------------------------------

// undefinedValue 单例，表示 JS undefined。
type undefinedValue struct{}

// Undefined 返回 undefined 单例。
func Undefined() Value { return undefinedValue{} }

func (undefinedValue) Type() ValueType              { return TypeUndefined }
func (undefinedValue) String() string               { return "undefined" }
func (undefinedValue) Int() (int, bool)             { return 0, false }
func (undefinedValue) Float() (float64, bool)       { return 0, false }
func (undefinedValue) Bool() (bool, bool)           { return false, true } // ToBoolean(undefined) = false
func (undefinedValue) IsUndefined() bool            { return true }
func (undefinedValue) IsNull() bool                 { return false }
func (undefinedValue) IsObject() bool               { return false }
func (undefinedValue) IsFunction() bool             { return false }
func (undefinedValue) AsObject() (Object, bool)     { return nil, false }
func (undefinedValue) AsFunction() (Function, bool) { return nil, false }

// --- null ------------------------------------------------------------------

type nullValue struct{}

// Null 返回 null 单例。
func Null() Value { return nullValue{} }

func (nullValue) Type() ValueType              { return TypeNull }
func (nullValue) String() string               { return "null" }
func (nullValue) Int() (int, bool)             { return 0, false }
func (nullValue) Float() (float64, bool)       { return 0, false }
func (nullValue) Bool() (bool, bool)           { return false, true } // ToBoolean(null) = false
func (nullValue) IsUndefined() bool            { return false }
func (nullValue) IsNull() bool                 { return true }
func (nullValue) IsObject() bool               { return false }
func (nullValue) IsFunction() bool             { return false }
func (nullValue) AsObject() (Object, bool)     { return nil, false }
func (nullValue) AsFunction() (Function, bool) { return nil, false }

// --- boolean ---------------------------------------------------------------

type booleanValue bool

// Bool 包装 Go bool 为 JS Value。
func Boolean(b bool) Value { return booleanValue(b) }

func (b booleanValue) Type() ValueType { return TypeBoolean }
func (b booleanValue) String() string  { return strconv.FormatBool(bool(b)) }
func (b booleanValue) Int() (int, bool) {
	if b {
		return 1, true
	}
	return 0, true
}
func (b booleanValue) Float() (float64, bool) {
	if b {
		return 1, true
	}
	return 0, true
}
func (b booleanValue) Bool() (bool, bool)           { return bool(b), true }
func (b booleanValue) IsUndefined() bool            { return false }
func (b booleanValue) IsNull() bool                 { return false }
func (b booleanValue) IsObject() bool               { return false }
func (b booleanValue) IsFunction() bool             { return false }
func (b booleanValue) AsObject() (Object, bool)     { return nil, false }
func (b booleanValue) AsFunction() (Function, bool) { return nil, false }

// --- number ----------------------------------------------------------------

type numberValue float64

// Number 包装 Go float64 为 JS Value。
// JS 中所有数字都是 float64（除 BigInt），故统一用 float64 表示。
func Number(n float64) Value { return numberValue(n) }

// IntValue 包装 Go int 为 JS Value。
func IntValue(n int) Value { return numberValue(float64(n)) }

func (n numberValue) Type() ValueType              { return TypeNumber }
func (n numberValue) String() string               { return formatNumber(float64(n)) }
func (n numberValue) Int() (int, bool)             { return int(float64(n)), true }
func (n numberValue) Float() (float64, bool)       { return float64(n), true }
func (n numberValue) Bool() (bool, bool)           { return float64(n) != 0, true }
func (n numberValue) IsUndefined() bool            { return false }
func (n numberValue) IsNull() bool                 { return false }
func (n numberValue) IsObject() bool               { return false }
func (n numberValue) IsFunction() bool             { return false }
func (n numberValue) AsObject() (Object, bool)     { return nil, false }
func (n numberValue) AsFunction() (Function, bool) { return nil, false }

// formatNumber 按 JS Number.prototype.toString 规则格式化。
func formatNumber(n float64) string {
	// 整数：去掉小数点
	if n == float64(int64(n)) && n >= -9007199254740991 && n <= 9007199254740991 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}

// --- string ----------------------------------------------------------------

type stringValue string

// Str 包装 Go string 为 JS Value。
func Str(s string) Value { return stringValue(s) }

func (s stringValue) Type() ValueType { return TypeString }
func (s stringValue) String() string  { return string(s) }
func (s stringValue) Int() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(string(s)))
	if err != nil {
		return 0, false
	}
	return n, true
}
func (s stringValue) Float() (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(string(s)), 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
func (s stringValue) Bool() (bool, bool)           { return string(s) != "", true } // ToBoolean("") = false
func (s stringValue) IsUndefined() bool            { return false }
func (s stringValue) IsNull() bool                 { return false }
func (s stringValue) IsObject() bool               { return false }
func (s stringValue) IsFunction() bool             { return false }
func (s stringValue) AsObject() (Object, bool)     { return nil, false }
func (s stringValue) AsFunction() (Function, bool) { return nil, false }

// --- object ----------------------------------------------------------------

// objectValue is the minimal JS Object implementation: map + insertion order.
type objectValue struct {
	keys   []string         // insertion order
	values map[string]Value // property table
	proto  Object           // [[Prototype]]
}

// NewObject creates an empty JS object.
func NewObject() Object {
	return &objectValue{
		values: make(map[string]Value),
	}
}

// NewObjectFrom creates an object from a map (random order).
func NewObjectFrom(m map[string]Value) Object {
	o := &objectValue{values: make(map[string]Value, len(m))}
	for k, v := range m {
		o.keys = append(o.keys, k)
		o.values[k] = v
	}
	return o
}

// Proto returns the [[Prototype]] of the object (may be nil).
func (o *objectValue) Proto() Object { return o.proto }

// SetProto sets the [[Prototype]] of the object.
func (o *objectValue) SetProto(p Object) { o.proto = p }

// SetProto sets the [[Prototype]] on any value whose concrete type supports it.
func SetProto(obj Value, proto Object) {
	if o, ok := obj.(*objectValue); ok {
		o.proto = proto
	} else if a, ok := obj.(*ArrayValue); ok {
		if a.objectValue != nil {
			a.objectValue.proto = proto
		}
	} else if f, ok := obj.(*functionValue); ok {
		if f.objectValue != nil {
			f.objectValue.proto = proto
		}
	}
}

// GetProto returns the [[Prototype]] of the value, or nil if not available.
func GetProto(obj Value) Object {
	if o, ok := obj.(*objectValue); ok {
		return o.proto
	}
	if a, ok := obj.(*ArrayValue); ok {
		if a.objectValue != nil {
			return a.objectValue.proto
		}
	}
	if f, ok := obj.(*functionValue); ok {
		if f.objectValue != nil {
			return f.objectValue.proto
		}
	}
	return nil
}

func (o *objectValue) Type() ValueType { return TypeObject }

// String 返回对象的字符串表示（简化版，类似 Node util.inspect）。
func (o *objectValue) String() string {
	if len(o.keys) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range o.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(inspectValue(o.values[k]))
	}
	b.WriteString(" }")
	return b.String()
}

func (o *objectValue) Int() (int, bool)             { return 0, false }
func (o *objectValue) Float() (float64, bool)       { return 0, false }
func (o *objectValue) Bool() (bool, bool)           { return true, true } // ToBoolean(object) = true
func (o *objectValue) IsUndefined() bool            { return false }
func (o *objectValue) IsNull() bool                 { return false }
func (o *objectValue) IsObject() bool               { return true }
func (o *objectValue) IsFunction() bool             { return false }
func (o *objectValue) AsObject() (Object, bool)     { return o, true }
func (o *objectValue) AsFunction() (Function, bool) { return nil, false }

func (o *objectValue) Get(key string) (Value, error) {
	// Walk own + prototype chain.
	cur := o
	for cur != nil {
		if v, ok := cur.values[key]; ok {
			return v, nil
		}
		if p, ok := cur.proto.(*objectValue); ok {
			cur = p
		} else {
			break
		}
	}
	return Undefined(), nil
}

func (o *objectValue) Set(key string, value Value) error {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
	return nil
}

func (o *objectValue) Keys() []string {
	out := make([]string, len(o.keys))
	copy(out, o.keys)
	return out
}

// Delete removes an own property. Returns true if the property was removed or
// did not exist; false only if the property is non-configurable (not modelled
// here, so always true).
func (o *objectValue) Delete(key string) bool {
	if _, exists := o.values[key]; !exists {
		return true // property doesn't exist — delete returns true
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

// --- array -----------------------------------------------------------------

// ArrayValue 是 JS 数组的对象实现（带 length 属性）。
type ArrayValue struct {
	*objectValue
	elems []Value
}

// NewArray 创建数组对象。
func NewArray(elems []Value) *ArrayValue {
	a := &ArrayValue{
		objectValue: &objectValue{values: make(map[string]Value)},
		elems:       elems,
	}
	// 同步 length 属性
	a.values["length"] = IntValue(len(elems))
	return a
}

func (a *ArrayValue) Type() ValueType { return TypeObject }

// AsObject 重写以返回数组自身（而非嵌入的 *objectValue），
// 否则后续 Get/Set 会绕过 ArrayValue 的索引处理逻辑。
func (a *ArrayValue) AsObject() (Object, bool) { return a, true }

func (a *ArrayValue) String() string {
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
	if idx, err := strconv.Atoi(key); err == nil && idx >= 0 && idx < len(a.elems) {
		return a.elems[idx], nil
	}
	return a.objectValue.Get(key)
}

// Set 重写以支持数字索引写入与 length 同步。
func (a *ArrayValue) Set(key string, value Value) error {
	if key == "length" {
		n, ok := value.Int()
		if !ok {
			return fmt.Errorf("%w: invalid length", ErrTypeError)
		}
		if n >= 0 {
			if n < len(a.elems) {
				a.elems = a.elems[:n]
			} else {
				for i := len(a.elems); i < n; i++ {
					a.elems = append(a.elems, Undefined())
				}
			}
			a.values["length"] = IntValue(n)
		}
		return nil
	}
	if idx, err := strconv.Atoi(key); err == nil && idx >= 0 {
		for len(a.elems) <= idx {
			a.elems = append(a.elems, Undefined())
		}
		a.elems[idx] = value
		a.values["length"] = IntValue(len(a.elems))
		return nil
	}
	return a.objectValue.Set(key, value)
}

func (a *ArrayValue) Keys() []string {
	out := make([]string, 0, len(a.elems)+1)
	for i := range a.elems {
		out = append(out, strconv.Itoa(i))
	}
	out = append(out, "length")
	return out
}

// Elems 返回数组元素切片（只读视图）。
func (a *ArrayValue) Elems() []Value { return a.elems }

// Append appends a value to the array and updates the length property.
func (a *ArrayValue) Append(v Value) {
	a.elems = append(a.elems, v)
	a.values["length"] = IntValue(len(a.elems))
}

// --- function --------------------------------------------------------------

// functionValue 包装一个 Go Func 为 JS Function 对象。
type functionValue struct {
	*objectValue
	fn   Func
	name string
}

// NewFunction 创建函数对象。
func NewFunction(name string, fn Func) Function {
	f := &functionValue{
		objectValue: &objectValue{values: make(map[string]Value)},
		fn:          fn,
		name:        name,
	}
	_ = f.objectValue.Set("name", Str(name))
	_ = f.objectValue.Set("length", IntValue(0)) // 形参数量，Phase 0 固定 0
	return f
}

func (f *functionValue) Type() ValueType { return TypeFunction }

func (f *functionValue) String() string {
	if f.name == "" {
		return "[Function (anonymous)]"
	}
	return "[Function: " + f.name + "]"
}

func (f *functionValue) IsFunction() bool             { return true }
func (f *functionValue) IsObject() bool               { return true }
func (f *functionValue) AsObject() (Object, bool)     { return f.objectValue, true }
func (f *functionValue) AsFunction() (Function, bool) { return f, true }

func (f *functionValue) Call(args []Value) (Value, error) {
	return f.fn(args)
}

// --- 辅助函数 --------------------------------------------------------------

// inspectValue 返回值的可读字符串表示（用于 console.log 输出）。
// 比 String() 更详细：字符串不带引号，对象展开属性。
func inspectValue(v Value) string {
	if v == nil {
		return "undefined"
	}
	switch v.Type() {
	case TypeString:
		return v.String() // console.log 输出字符串时不带引号
	case TypeUndefined, TypeNull, TypeBoolean, TypeNumber:
		return v.String()
	default:
		return v.String()
	}
}

// InspectValues 将多个 Value 用空格连接（console.log 多参数行为）。
func InspectValues(vs []Value) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = inspectValue(v)
	}
	return strings.Join(parts, " ")
}
