package engine

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"
)

// stringifyGuard 跟踪当前正在格式化的对象。
// 对象打印/字符串化（String()/console.log）沿对象图递归时，若遇自引用
// （循环引用）会无限递归导致 Go 栈溢出崩溃。此处用"进行中"集合检测环，
// 命中时返回 "[Circular]" 而非继续递归。
// 引擎 JS 为单线程执行，String() 仅在 JS 线程被调用，故全局表安全。
var (
	stringifyMu         sync.Mutex
	stringifyInProgress = map[*objectValue]bool{}
)

// markStringify 标记对象正在格式化；若已在格式化路径上（环）返回 false。
func markStringify(o *objectValue) bool {
	stringifyMu.Lock()
	defer stringifyMu.Unlock()
	if stringifyInProgress[o] {
		return false
	}
	stringifyInProgress[o] = true
	return true
}

// unmarkStringify 取消格式化标记（对象自身格式化完成后调用）。
func unmarkStringify(o *objectValue) {
	stringifyMu.Lock()
	delete(stringifyInProgress, o)
	stringifyMu.Unlock()
}

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
func (n numberValue) Bool() (bool, bool)           { return float64(n) != 0 && !math.IsNaN(float64(n)), true }
func (n numberValue) IsUndefined() bool            { return false }
func (n numberValue) IsNull() bool                 { return false }
func (n numberValue) IsObject() bool               { return false }
func (n numberValue) IsFunction() bool             { return false }
func (n numberValue) AsObject() (Object, bool)     { return nil, false }
func (n numberValue) AsFunction() (Function, bool) { return nil, false }

// === BigInt（ES2020）======================================================

// bigIntValue 包装 math/big.Int 实现 JS BigInt 值类型。
// 关键语义：Float() 与 Int() 都返回 (0, false) 以阻断所有 float/int 路径，
// 强制算术/位运算在分发处用类型判断走 math/big 计算。
type bigIntValue struct{ val *big.Int }

// BigInt 从 *big.Int 创建 BigInt 值（内部会拷贝，避免外部修改）。
func BigInt(i *big.Int) Value {
	if i == nil {
		return BigIntZero()
	}
	c := new(big.Int).Set(i)
	return bigIntValue{val: c}
}

// BigIntFromInt 从 int64 创建 BigInt 值。
func BigIntFromInt(i int64) Value {
	return bigIntValue{val: big.NewInt(i)}
}

// BigIntZero 返回值为 0 的 BigInt（常用，避免重复分配）。
func BigIntZero() Value { return bigIntValue{val: big.NewInt(0)} }

// BigIntVal 从字符串解析 BigInt（十进制）。
func BigIntVal(s string) (Value, bool) {
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Undefined(), false
	}
	return bigIntValue{val: bi}, true
}

func (b bigIntValue) Type() ValueType              { return TypeBigInt }
func (b bigIntValue) String() string               { return b.val.String() }
func (b bigIntValue) Int() (int, bool)             { return 0, false } // 阻断 Int 路径
func (b bigIntValue) Float() (float64, bool)       { return 0, false } // 阻断 Float 路径
func (b bigIntValue) Bool() (bool, bool)           { return b.val.Sign() != 0, true }
func (b bigIntValue) IsUndefined() bool            { return false }
func (b bigIntValue) IsNull() bool                 { return false }
func (b bigIntValue) IsObject() bool               { return false }
func (b bigIntValue) IsFunction() bool             { return false }
func (b bigIntValue) AsObject() (Object, bool)     { return nil, false }
func (b bigIntValue) AsFunction() (Function, bool) { return nil, false }

// BigIntValue 是 bigIntValue 的公开访问器（用于 VM 算术运算取底层 *big.Int）。
// 返回底层 *big.Int（只读，调用方不应修改）。
func BigIntValue(v Value) (*big.Int, bool) {
	b, ok := v.(bigIntValue)
	if !ok {
		return nil, false
	}
	return b.val, true
}

// formatNumber 按 JS Number.prototype.toString 规则格式化。
func formatNumber(n float64) string {
	// JS 特殊值（Infinity / -Infinity / NaN）。
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	if math.IsNaN(n) {
		return "NaN"
	}
	// 整数：去掉小数点
	if n == float64(int64(n)) && n >= -9007199254740991 && n <= 9007199254740991 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}

// --- string ----------------------------------------------------------------

type stringValue string

// ropeStringValue keeps long concatenation chains as a tree. JavaScript
// strings remain immutable; callers that need contiguous bytes flatten the
// tree once through String(), while truthiness and length stay allocation-free.
type ropeStringValue struct {
	left, right Value
	utf16Len    int
	flat        atomic.Pointer[string]
}

const flatConcatLimit = 64

// Str 包装 Go string 为 JS Value。
func Str(s string) Value { return stringValue(s) }

// ConcatStrings applies the existing string coercion rules without repeatedly
// copying an already-growing left operand. Small flat strings stay flat to
// avoid adding rope overhead to ordinary expressions.
func ConcatStrings(left, right Value) Value {
	if left.Type() != TypeString {
		left = Str(left.String())
	}
	if right.Type() != TypeString {
		right = Str(right.String())
	}

	leftLen, _ := StringLen(left)
	rightLen, _ := StringLen(right)
	if leftLen == 0 {
		return right
	}
	if rightLen == 0 {
		return left
	}
	if leftLen+rightLen <= flatConcatLimit {
		if l, lok := left.(stringValue); lok {
			if r, rok := right.(stringValue); rok {
				return stringValue(string(l) + string(r))
			}
		}
	}
	return &ropeStringValue{left: left, right: right, utf16Len: leftLen + rightLen}
}

// StringLen returns the ECMAScript UTF-16 code-unit length. It avoids
// flattening rope strings for repeated `.length` reads.
func StringLen(v Value) (int, bool) {
	if v.Type() != TypeString {
		return 0, false
	}
	if r, ok := v.(*ropeStringValue); ok {
		return r.utf16Len, true
	}
	return len(utf16.Encode([]rune(v.String()))), true
}

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

func (s *ropeStringValue) Type() ValueType { return TypeString }
func (s *ropeStringValue) String() string {
	if flat := s.flat.Load(); flat != nil {
		return *flat
	}

	var b strings.Builder
	b.Grow(s.utf16Len)
	stack := []Value{s}
	for len(stack) > 0 {
		last := len(stack) - 1
		v := stack[last]
		stack = stack[:last]
		if rope, ok := v.(*ropeStringValue); ok {
			if flat := rope.flat.Load(); flat != nil {
				b.WriteString(*flat)
				continue
			}
			stack = append(stack, rope.right, rope.left)
			continue
		}
		b.WriteString(v.String())
	}

	value := b.String()
	s.flat.CompareAndSwap(nil, &value)
	return *s.flat.Load()
}
func (s *ropeStringValue) Int() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s.String()))
	return n, err == nil
}
func (s *ropeStringValue) Float() (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(s.String()), 64)
	return n, err == nil
}
func (s *ropeStringValue) Bool() (bool, bool)           { return s.utf16Len != 0, true }
func (s *ropeStringValue) IsUndefined() bool            { return false }
func (s *ropeStringValue) IsNull() bool                 { return false }
func (s *ropeStringValue) IsObject() bool               { return false }
func (s *ropeStringValue) IsFunction() bool             { return false }
func (s *ropeStringValue) AsObject() (Object, bool)     { return nil, false }
func (s *ropeStringValue) AsFunction() (Function, bool) { return nil, false }

// --- symbol ----------------------------------------------------------------

// SymbolValue is a JS Symbol primitive. Symbols are unique, immutable values
// used as property keys (especially for well-known protocols like Symbol.iterator).
type SymbolValue struct {
	desc string
	id   uint64
}

var symbolCounter uint64

// NewSymbol creates a new unique symbol with the given description.
func NewSymbol(desc string) *SymbolValue {
	symbolCounter++
	return &SymbolValue{desc: desc, id: symbolCounter}
}

// SymbolIterator returns the well-known Symbol.iterator symbol.
var SymbolIterator = &SymbolValue{desc: "Symbol.iterator", id: 0}

// SymbolAsyncIterator returns the well-known Symbol.asyncIterator symbol.
var SymbolAsyncIterator = &SymbolValue{desc: "Symbol.asyncIterator", id: 1}

// SymbolHasInstance is the well-known Symbol.hasInstance symbol.
var SymbolHasInstance = &SymbolValue{desc: "Symbol.hasInstance", id: 2}

// SymbolToPrimitive is the well-known Symbol.toPrimitive symbol.
var SymbolToPrimitive = &SymbolValue{desc: "Symbol.toPrimitive", id: 3}

// SymbolToStringTag is the well-known Symbol.toStringTag symbol.
var SymbolToStringTag = &SymbolValue{desc: "Symbol.toStringTag", id: 4}

// SymbolMatch is the well-known Symbol.match symbol (String.prototype.match dispatch).
var SymbolMatch = &SymbolValue{desc: "Symbol.match", id: 5}

// SymbolReplace is the well-known Symbol.replace symbol.
var SymbolReplace = &SymbolValue{desc: "Symbol.replace", id: 6}

// SymbolSearch is the well-known Symbol.search symbol.
var SymbolSearch = &SymbolValue{desc: "Symbol.search", id: 7}

// SymbolSplit is the well-known Symbol.split symbol.
var SymbolSplit = &SymbolValue{desc: "Symbol.split", id: 8}

// SymbolSpecies is the well-known Symbol.species symbol.
var SymbolSpecies = &SymbolValue{desc: "Symbol.species", id: 9}

// symbolRegistry implements the global Symbol registry for Symbol.for()/keyFor().
var symbolRegistry = struct {
	entries map[string]*SymbolValue
	order   []string
}{
	entries: make(map[string]*SymbolValue),
}

// SymbolFor returns the shared symbol registered under the given key, creating
// a new one if none exists yet. Implements the global Symbol registry.
func SymbolFor(key string) *SymbolValue {
	if s, ok := symbolRegistry.entries[key]; ok {
		return s
	}
	symbolCounter++
	s := &SymbolValue{desc: key, id: symbolCounter}
	symbolRegistry.entries[key] = s
	symbolRegistry.order = append(symbolRegistry.order, key)
	return s
}

// KeyFor returns the registry key under which the symbol was registered via
// SymbolFor, or ("", false) if the symbol is not in the global registry.
func (s *SymbolValue) KeyFor() (string, bool) {
	for k, v := range symbolRegistry.entries {
		if v == s {
			return k, true
		}
	}
	return "", false
}

func (s *SymbolValue) Type() ValueType { return TypeSymbol }
func (s *SymbolValue) String() string {
	if s.desc == "" {
		return "Symbol()"
	}
	return "Symbol(" + s.desc + ")"
}
func (s *SymbolValue) Int() (int, bool)             { return 0, false }
func (s *SymbolValue) Float() (float64, bool)       { return 0, false }
func (s *SymbolValue) Bool() (bool, bool)           { return true, true }
func (s *SymbolValue) IsUndefined() bool            { return false }
func (s *SymbolValue) IsNull() bool                 { return false }
func (s *SymbolValue) IsObject() bool               { return false }
func (s *SymbolValue) IsFunction() bool             { return false }
func (s *SymbolValue) AsObject() (Object, bool)     { return nil, false }
func (s *SymbolValue) AsFunction() (Function, bool) { return nil, false }

// SymbolKey returns the string key used to store a symbol-keyed property.
// Since our object implementation uses string keys, we map symbols to unique
// internal strings.
func (s *SymbolValue) SymbolKey() string {
	return fmt.Sprintf("\x00symbol:%d:%s", s.id, s.desc)
}

// IsWellKnown returns true if this is a well-known symbol (id < 100).
func (s *SymbolValue) IsWellKnown() bool { return s.id < 100 }

// --- object ----------------------------------------------------------------

// objectValue 是 JS Object 的实现：隐藏类（Shape）+ 槽位数组。
// 同类对象共享 Shape，属性访问经 shape.index 映射 O(1)。
type objectValue struct {
	shape   *Shape
	slots   []Value
	deleted map[string]bool // 对象级已删除属性（避免污染共享 Shape）
	proto   Object          // [[Prototype]]
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

	o := &objectValue{shape: shape, slots: make([]Value, shape.NumProps())}
	for i := 0; i+1 < len(pairs); i += 2 {
		idx, _ := shape.lookup(pairs[i].String())
		o.slots[idx] = pairs[i+1]
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

// GetProto returns the [[Prototype]] of the value, or nil if not available.
func GetProto(obj Value) Object {
	if getter, ok := obj.(interface{ Proto() Object }); ok {
		return getter.Proto()
	}
	return nil
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

func (o *objectValue) Int() (int, bool)             { return 0, false }
func (o *objectValue) Float() (float64, bool)       { return 0, false }
func (o *objectValue) Bool() (bool, bool)           { return true, true } // ToBoolean(object) = true
func (o *objectValue) IsUndefined() bool            { return false }
func (o *objectValue) IsNull() bool                 { return false }
func (o *objectValue) IsObject() bool               { return true }
func (o *objectValue) IsFunction() bool             { return false }
func (o *objectValue) AsObject() (Object, bool)     { return o, true }
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
		out = append(out, name)
	}
	return out
}

// Delete removes an own property. Returns true if the property was removed or
// did not exist; false only if the property is non-configurable (not modelled
// here, so always true).
func (o *objectValue) Delete(key string) bool {
	if _, ok := o.getSlot(key); !ok {
		return true // property doesn't exist — delete returns true
	}
	if o.deleted == nil {
		o.deleted = make(map[string]bool)
	}
	o.deleted[key] = true
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
		objectValue: &objectValue{shape: rootShape},
		elems:       elems,
	}
	register(a.objectValue)
	// 同步 length 属性
	a.objectValue.setSlot("length", IntValue(len(elems)))
	return a
}

func (a *ArrayValue) Type() ValueType { return TypeObject }

// AsObject 重写以返回数组自身（而非嵌入的 *objectValue），
// 否则后续 Get/Set 会绕过 ArrayValue 的索引处理逻辑。
func (a *ArrayValue) AsObject() (Object, bool) { return a, true }

func (a *ArrayValue) String() string {
	if !markStringify(a.objectValue) {
		return "[Circular]"
	}
	defer unmarkStringify(a.objectValue)
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
			a.objectValue.setSlot("length", IntValue(n))
		}
		return nil
	}
	if idx, err := strconv.Atoi(key); err == nil && idx >= 0 {
		for len(a.elems) <= idx {
			a.elems = append(a.elems, Undefined())
		}
		a.elems[idx] = value
		a.objectValue.setSlot("length", IntValue(len(a.elems)))
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
	// 合并对象自有属性（负索引/非规范键，如 jsdiff 的 bestPath[-1]）。
	// objectValue.Keys() 含 "length"（setSlot 写入），去重。
	seen := make(map[string]bool, len(out))
	for _, k := range out {
		seen[k] = true
	}
	for _, k := range a.objectValue.Keys() {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Elems 返回数组元素切片（只读视图）。
func (a *ArrayValue) Elems() []Value { return a.elems }

// Append appends a value to the array and updates the length property.
func (a *ArrayValue) Append(v Value) {
	a.elems = append(a.elems, v)
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
}

// SetIndex writes a value to a numeric array index, growing the slice with
// holes (undefined) when idx exceeds the current length, and synchronizes the
// length property once. It mirrors the numeric-key branch of Set without the
// string conversion, for the VM's array-index write fast path (M1-2 写侧).
func (a *ArrayValue) SetIndex(idx int, value Value) {
	for len(a.elems) <= idx {
		a.elems = append(a.elems, Undefined())
	}
	a.elems[idx] = value
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
	for i := 0; i < count; i++ {
		a.elems[oldLen+i] = numberValue(start + float64(i))
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
		if cap(a.elems)-oldLen < end-oldLen {
			grown := make([]Value, oldLen, end)
			copy(grown, a.elems)
			a.elems = grown
		}
		a.elems = a.elems[:end]
		for i := oldLen; i < end; i++ {
			a.elems[i] = Undefined() // holes above the previous length
		}
	}
	for i := 0; i < count; i++ {
		a.elems[start+i] = numberValue(valueStart + float64(i))
	}
	a.objectValue.setSlot("length", IntValue(len(a.elems)))
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
		objectValue: &objectValue{shape: rootShape},
		fn:          fn,
		name:        name,
	}
	register(f.objectValue)
	_ = f.objectValue.Set("name", Str(name))
	_ = f.objectValue.Set("length", IntValue(0)) // 形参数量，Phase 0 固定 0
	// ES 语义：普通函数都有 .prototype 属性（一个对象，constructor 指向自身）。
	// engine.NewFunction 常用于原生模块构造器（如 stream.Transform），npm 包常
	// 访问 <Ctor>.prototype（iconv-lite 的 Object.create(Transform.prototype)）。
	proto := NewObject()
	_ = proto.Set("constructor", f)
	_ = f.objectValue.Set("prototype", proto)
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

// --- accessor (getter/setter) --------------------------------------------

// AccessorValue wraps a getter/setter pair. It is stored as a property value
// on an object to model ES2015 class accessors and Object.defineProperty
// accessors. The VM/interpreter detect it via type assertion and invoke the
// getter/setter with the appropriate `this` binding instead of returning the
// accessor itself.
type AccessorValue struct {
	Getter Value // function or Undefined
	Setter Value // function or Undefined
}

// NewAccessor creates an accessor value. Either getter or setter may be nil
// (treated as undefined — i.e. a no-op getter/setter).
func NewAccessor(getter, setter Value) *AccessorValue {
	if getter == nil {
		getter = Undefined()
	}
	if setter == nil {
		setter = Undefined()
	}
	return &AccessorValue{Getter: getter, Setter: setter}
}

func (a *AccessorValue) Type() ValueType              { return TypeObject } // internal sentinel
func (a *AccessorValue) String() string               { return "[Accessor]" }
func (a *AccessorValue) Int() (int, bool)             { return 0, false }
func (a *AccessorValue) Float() (float64, bool)       { return 0, false }
func (a *AccessorValue) Bool() (bool, bool)           { return false, true }
func (a *AccessorValue) IsUndefined() bool            { return false }
func (a *AccessorValue) IsNull() bool                 { return false }
func (a *AccessorValue) IsObject() bool               { return false }
func (a *AccessorValue) IsFunction() bool             { return false }
func (a *AccessorValue) AsObject() (Object, bool)     { return nil, false }
func (a *AccessorValue) AsFunction() (Function, bool) { return nil, false }

// SetAccessor installs a getter/setter pair as an own property on obj.
func SetAccessor(obj Object, key string, getter, setter Value) {
	// 直接 objectValue。
	if ov, ok := obj.(*objectValue); ok {
		ov.setSlot(key, NewAccessor(getter, setter))
		return
	}
	// 嵌入 objectValue 的类型（functionValue/ArrayValue/BufferValue）。
	if embedded := embeddedObjectValue(obj); embedded != nil {
		embedded.setSlot(key, NewAccessor(getter, setter))
		return
	}
	// Fall back to plain set (accessors unsupported on this type).
	_ = obj.Set(key, NewAccessor(getter, setter))
}

// embeddedObjectValue 从嵌入 objectValue 的类型中取出其 *objectValue。
func embeddedObjectValue(obj Object) *objectValue {
	switch v := obj.(type) {
	case *functionValue:
		return v.objectValue
	case *ArrayValue:
		return v.objectValue
	case *BufferValue:
		return v.objectValue
	}
	return nil
}

// UpdateAccessor installs or updates a single getter or setter on obj. If an
// accessor already exists for key, only the requested half (getter or setter)
// is updated, preserving the other. Used by class assembly when get/set pairs
// are installed as separate method definitions.
func UpdateAccessor(obj Object, key string, isGetter bool, fn Value) {
	ov, ok := obj.(*objectValue)
	if !ok {
		return
	}
	if existing, exists := ov.getSlot(key); exists {
		if acc, ok := existing.(*AccessorValue); ok {
			if isGetter {
				acc.Getter = fn
			} else {
				acc.Setter = fn
			}
			return
		}
	}
	getter, setter := Undefined(), Undefined()
	if isGetter {
		getter = fn
	} else {
		setter = fn
	}
	ov.setSlot(key, NewAccessor(getter, setter))
}

// FindAccessor walks the prototype chain of obj looking for an accessor
// stored under key. Returns the accessor and true if found.
func FindAccessor(obj Value, key string) (*AccessorValue, bool) {
	cur := obj
	for cur != nil {
		if o, ok := cur.(*objectValue); ok {
			if v, exists := o.getSlot(key); exists {
				if acc, ok := v.(*AccessorValue); ok {
					return acc, true
				}
				// Non-accessor own property shadows accessors up the chain.
				return nil, false
			}
			if o.proto == nil {
				return nil, false
			}
			// 交给循环重新分发（functionValue/ArrayValue/闭包等原型类型）。
			cur = o.proto
		} else if a, ok := cur.(*ArrayValue); ok {
			if a.objectValue != nil {
				if v, exists := a.objectValue.getSlot(key); exists {
					if acc, ok := v.(*AccessorValue); ok {
						return acc, true
					}
					return nil, false
				}
			}
			cur = GetProto(cur)
		} else if f, ok := cur.(*functionValue); ok {
			if f.objectValue != nil {
				if v, exists := f.objectValue.getSlot(key); exists {
					if acc, ok := v.(*AccessorValue); ok {
						return acc, true
					}
					return nil, false
				}
			}
			cur = GetProto(cur)
		} else if p := GetProto(cur); p != nil {
			// 自定义类型（如闭包）作为原型：经 Proto() 解包继续。
			cur = p
		} else {
			return nil, false
		}
	}
	return nil, false
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
