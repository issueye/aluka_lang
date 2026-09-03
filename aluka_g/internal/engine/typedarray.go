package engine

// TypedArray / ArrayBuffer / DataView 值类型（Pi 兼容：typebox 等依赖
// Float64Array/DataView/BigInt 等现代 API）。
//
// 设计：
//   - TypedArrayValue：任意元素宽度的类型化数组，底层共享 []byte 视图
//     （字节偏移 byteOffset + 元素个数 length），.buffer 指向 ArrayBufferValue。
//   - ArrayBufferValue：字节容器（byteLength + slice）。
//   - DataViewValue：按任意类型读写字节的视图。
//
// 元素类型由 kind 标识（Int8/Uint8/Int8C/Uint16/Int32/Uint32/F32/F64/BigI64/BigU64）。

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// TypedArrayKind 标识类型化数组的元素类型。
type TypedArrayKind int

const (
	KindInt8 TypedArrayKind = iota
	KindUint8
	KindUint8Clamped
	KindInt16
	KindUint16
	KindInt32
	KindUint32
	KindFloat32
	KindFloat64
	KindBigInt64
	KindBigUint64
)

// TypeArrayName 返回类型化数组构造器名。
func (k TypedArrayKind) Name() string {
	switch k {
	case KindInt8:
		return "Int8Array"
	case KindUint8:
		return "Uint8Array"
	case KindUint8Clamped:
		return "Uint8ClampedArray"
	case KindInt16:
		return "Int16Array"
	case KindUint16:
		return "Uint16Array"
	case KindInt32:
		return "Int32Array"
	case KindUint32:
		return "Uint32Array"
	case KindFloat32:
		return "Float32Array"
	case KindFloat64:
		return "Float64Array"
	case KindBigInt64:
		return "BigInt64Array"
	case KindBigUint64:
		return "BigUint64Array"
	}
	return "TypedArray"
}

// BytesPerElement 返回每元素字节数。
func (k TypedArrayKind) BytesPerElement() int {
	switch k {
	case KindInt8, KindUint8, KindUint8Clamped:
		return 1
	case KindInt16, KindUint16:
		return 2
	case KindInt32, KindUint32, KindFloat32:
		return 4
	default: // Float64 / BigInt64 / BigUint64
		return 8
	}
}

// IsBigIntKind 判断元素是否为大整数类型。
func (k TypedArrayKind) IsBigIntKind() bool { return k == KindBigInt64 || k == KindBigUint64 }

// ArrayBufferValue 是 ArrayBuffer（字节容器）。
type ArrayBufferValue struct {
	*objectValue
	data []byte
}

// NewArrayBuffer 创建字节容器。
func NewArrayBuffer(data []byte) *ArrayBufferValue {
	b := &ArrayBufferValue{
		objectValue: &objectValue{shape: rootShape},
		data:        data,
	}
	register(b.objectValue)
	b.setSlot("byteLength", IntValue(len(data)))
	return b
}

func (b *ArrayBufferValue) Type() ValueType            { return TypeObject }
func (b *ArrayBufferValue) AsObject() (Object, bool)   { return b, true }
func (b *ArrayBufferValue) Bytes() []byte              { return b.data }
func (b *ArrayBufferValue) String() string             { return "[object ArrayBuffer]" }

// Get 重写：byteLength 直接读底层长度。
func (b *ArrayBufferValue) Get(key string) (Value, error) {
	if key == "byteLength" {
		return IntValue(len(b.data)), nil
	}
	return b.objectValue.Get(key)
}

// AsArrayBuffer 从 Value 提取 ArrayBuffer 字节，非 ArrayBuffer 返回 false。
func AsArrayBuffer(v Value) ([]byte, bool) {
	b, ok := v.(*ArrayBufferValue)
	if !ok {
		return nil, false
	}
	return b.data, true
}

// AsArrayBufferValue 从 Value 提取 *ArrayBufferValue（供视图创建）。
func AsArrayBufferValue(v Value) (*ArrayBufferValue, bool) {
	b, ok := v.(*ArrayBufferValue)
	return b, ok
}

// TypedArrayValue 是类型化数组。
type TypedArrayValue struct {
	*objectValue
	kind  TypedArrayKind
	data  []byte  // 底层字节（可能是 ArrayBuffer 的视图）
	buf   *ArrayBufferValue // 关联的 ArrayBuffer（nil 表示独立存储）
	offset int    // 相对 buf.data 的字节偏移（独立存储时为 0）
}

// NewTypedArrayValue 创建元素类型的类型化数组（独立字节存储）。
func NewTypedArrayValue(kind TypedArrayKind, data []byte) *TypedArrayValue {
	t := &TypedArrayValue{
		objectValue: &objectValue{shape: rootShape},
		kind:        kind,
		data:        data,
	}
	register(t.objectValue)
	return t
}

// NewTypedArrayView 创建共享 ArrayBuffer 的类型化数组视图。
func NewTypedArrayView(kind TypedArrayKind, buf *ArrayBufferValue, offset int, byteLen int) *TypedArrayValue {
	t := &TypedArrayValue{
		objectValue: &objectValue{shape: rootShape},
		kind:        kind,
		data:        buf.data[offset : offset+byteLen],
		buf:         buf,
		offset:      offset,
	}
	register(t.objectValue)
	return t
}

func (t *TypedArrayValue) Type() ValueType          { return TypeObject }
func (t *TypedArrayValue) AsObject() (Object, bool) { return t, true }
func (t *TypedArrayValue) Kind() TypedArrayKind     { return t.kind }

// Bytes 返回底层字节。
func (t *TypedArrayValue) Bytes() []byte { return t.data }

// Buffer 返回关联的 ArrayBuffer（独立存储时为 nil）。
func (t *TypedArrayValue) Buffer() *ArrayBufferValue { return t.buf }

// ByteOffset 返回相对 ArrayBuffer 的字节偏移。
func (t *TypedArrayValue) ByteOffset() int { return t.offset }

// ElementAt 读取第 i 个元素。
func (t *TypedArrayValue) ElementAt(i int) Value {
	if i < 0 || i >= t.Length() {
		return Undefined()
	}
	return t.element(i)
}

// Length 返回元素个数。
func (t *TypedArrayValue) Length() int { return len(t.data) / t.kind.BytesPerElement() }

func (t *TypedArrayValue) String() string {
	var parts []string
	n := t.Length()
	for i := 0; i < n; i++ {
		parts = append(parts, t.elementString(i))
	}
	return strings.Join(parts, ",")
}

func (t *TypedArrayValue) elementString(i int) string {
	switch t.kind {
	case KindFloat32:
		return formatFloat(t.float32At(i))
	case KindFloat64:
		return formatFloat(t.float64At(i))
	case KindBigInt64, KindBigUint64:
		return t.bigIntAt(i).String() + "n"
	default:
		return strconv.FormatInt(int64(t.intAt(i)), 10)
	}
}

// formatFloat 输出 JS 风格浮点字符串。
func formatFloat(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == float64(int64(f)) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// AsTypedArray 从 Value 提取类型化数组字节，非类型化数组返回 false。
func AsTypedArray(v Value) (*TypedArrayValue, bool) {
	t, ok := v.(*TypedArrayValue)
	return t, ok
}

// Get 重写：length/byteLength/byteOffset 与数字索引。
func (t *TypedArrayValue) Get(key string) (Value, error) {
	switch key {
	case "length":
		return IntValue(t.Length()), nil
	case "byteLength":
		return IntValue(len(t.data)), nil
	case "byteOffset":
		return IntValue(t.offset), nil
	case "buffer":
		if t.buf != nil {
			return t.buf, nil
		}
		// 独立存储：包装当前字节的新 ArrayBuffer（JS 语义为共享，简化拷贝）。
		return NewArrayBuffer(t.data), nil
	}
	if idx, err := strconv.Atoi(key); err == nil {
		if idx >= 0 && idx < t.Length() {
			return t.element(idx), nil
		}
		if idx < 0 || idx >= t.Length() {
			// 越界返回 undefined（JS 语义）。
			return Undefined(), nil
		}
	}
	return t.objectValue.Get(key)
}

// Set 重写：数字索引写元素，length 只读。
func (t *TypedArrayValue) Set(key string, value Value) error {
	if key == "length" || key == "byteLength" || key == "byteOffset" {
		return nil // 只读
	}
	if idx, err := strconv.Atoi(key); err == nil && idx >= 0 && idx < t.Length() {
		if t.kind.IsBigIntKind() {
			if b, ok := BigIntValue(value); ok {
				t.setBigInt(idx, b)
				return nil
			}
			// BigInt64Array 赋值 Number 会截断为 BigInt（简化：直接存）。
			if f, ok := value.Float(); ok {
				t.setBigInt(idx, big.NewInt(int64(f)))
				return nil
			}
		}
		if f, ok := value.Float(); ok {
			t.setNumber(idx, f)
			return nil
		}
		return fmt.Errorf("%w: typed array index value must be a number", ErrTypeError)
	}
	return t.objectValue.Set(key, value)
}

// Keys 重写：数字索引 + length。
func (t *TypedArrayValue) Keys() []string {
	n := t.Length()
	out := make([]string, 0, n+1)
	for i := 0; i < n; i++ {
		out = append(out, strconv.Itoa(i))
	}
	out = append(out, "length")
	return out
}

// --- 元素读写 ---------------------------------------------------------

func (t *TypedArrayValue) element(i int) Value {
	switch t.kind {
	case KindFloat32:
		return Number(t.float32At(i))
	case KindFloat64:
		return Number(t.float64At(i))
	case KindBigInt64, KindBigUint64:
		return BigInt(t.bigIntAt(i))
	default:
		return IntValue(int(t.intAt(i)))
	}
}

// intAt 读取有符号/无符号整数元素。
func (t *TypedArrayValue) intAt(i int) int64 {
	off := i * t.kind.BytesPerElement()
	switch t.kind {
	case KindInt8:
		return int64(int8(t.data[off]))
	case KindUint8, KindUint8Clamped:
		return int64(t.data[off])
	case KindInt16:
		return int64(int16(binary.LittleEndian.Uint16(t.data[off:])))
	case KindUint16:
		return int64(binary.LittleEndian.Uint16(t.data[off:]))
	case KindInt32:
		return int64(int32(binary.LittleEndian.Uint32(t.data[off:])))
	case KindUint32:
		return int64(binary.LittleEndian.Uint32(t.data[off:]))
	}
	return 0
}

// setNumber 写入数值元素（含截断语义）。
func (t *TypedArrayValue) setNumber(i int, f float64) {
	off := i * t.kind.BytesPerElement()
	switch t.kind {
	case KindInt8:
		t.data[off] = byte(int8(clampInt(f, -128, 127)))
	case KindUint8:
		t.data[off] = byte(clampUint(f, 0, 255))
	case KindUint8Clamped:
		v := math.Round(f)
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		t.data[off] = byte(v)
	case KindInt16:
		binary.LittleEndian.PutUint16(t.data[off:], uint16(int16(clampInt(f, -32768, 32767))))
	case KindUint16:
		binary.LittleEndian.PutUint16(t.data[off:], uint16(clampUint(f, 0, 65535)))
	case KindInt32:
		binary.LittleEndian.PutUint32(t.data[off:], uint32(int32(clampInt(f, math.MinInt32, math.MaxInt32))))
	case KindUint32:
		binary.LittleEndian.PutUint32(t.data[off:], uint32(clampUint(f, 0, math.MaxUint32)))
	case KindFloat32:
		binary.LittleEndian.PutUint32(t.data[off:], math.Float32bits(float32(f)))
	case KindFloat64:
		binary.LittleEndian.PutUint64(t.data[off:], math.Float64bits(f))
	}
}

func clampInt(f float64, lo, hi int64) int64 {
	if f <= float64(lo) {
		return lo
	}
	if f >= float64(hi) {
		return hi
	}
	return int64(f)
}

func clampUint(f float64, lo, hi uint64) uint64 {
	if f <= float64(lo) {
		return lo
	}
	if f >= float64(hi) {
		return hi
	}
	return uint64(f)
}

func (t *TypedArrayValue) float32At(i int) float64 {
	off := i * 4
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(t.data[off:])))
}

func (t *TypedArrayValue) float64At(i int) float64 {
	off := i * 8
	return math.Float64frombits(binary.LittleEndian.Uint64(t.data[off:]))
}

func (t *TypedArrayValue) bigIntAt(i int) *big.Int {
	off := i * 8
	switch t.kind {
	case KindBigInt64:
		return big.NewInt(int64(binary.LittleEndian.Uint64(t.data[off:])))
	default: // BigUint64
		return new(big.Int).SetUint64(binary.LittleEndian.Uint64(t.data[off:]))
	}
}

func (t *TypedArrayValue) setBigInt(i int, b *big.Int) {
	off := i * 8
	if t.kind == KindBigInt64 {
		binary.LittleEndian.PutUint64(t.data[off:], uint64(b.Int64()))
	} else {
		binary.LittleEndian.PutUint64(t.data[off:], b.Uint64())
	}
}

// SetElement 供原型方法使用：写入元素（直接字节级）。
func (t *TypedArrayValue) SetElement(i int, v Value) error {
	if i < 0 || i >= t.Length() {
		return nil
	}
	if t.kind.IsBigIntKind() {
		if b, ok := BigIntValue(v); ok {
			t.setBigInt(i, b)
			return nil
		}
		if f, ok := v.Float(); ok {
			t.setBigInt(i, big.NewInt(int64(f)))
			return nil
		}
		return fmt.Errorf("%w: invalid bigint value", ErrTypeError)
	}
	if f, ok := v.Float(); ok {
		t.setNumber(i, f)
		return nil
	}
	return fmt.Errorf("%w: invalid number value", ErrTypeError)
}

// DataViewValue 是 DataView（任意类型字节读写视图）。
type DataViewValue struct {
	*objectValue
	data   []byte
	buf    *ArrayBufferValue
	offset int
}

// NewDataViewValue 创建 DataView。
func NewDataViewValue(data []byte, buf *ArrayBufferValue, offset int) *DataViewValue {
	d := &DataViewValue{
		objectValue: &objectValue{shape: rootShape},
		data:        data,
		buf:         buf,
		offset:      offset,
	}
	register(d.objectValue)
	return d
}

func (d *DataViewValue) Type() ValueType          { return TypeObject }
func (d *DataViewValue) AsObject() (Object, bool) { return d, true }
func (d *DataViewValue) String() string           { return "[object DataView]" }

func (d *DataViewValue) Get(key string) (Value, error) {
	switch key {
	case "byteLength":
		return IntValue(len(d.data)), nil
	case "byteOffset":
		return IntValue(d.offset), nil
	case "buffer":
		if d.buf != nil {
			return d.buf, nil
		}
		return NewArrayBuffer(d.data), nil
	}
	return d.objectValue.Get(key)
}

func (d *DataViewValue) Set(key string, value Value) error {
	if key == "byteLength" || key == "byteOffset" {
		return nil
	}
	return d.objectValue.Set(key, value)
}

// checkBounds 校验读取边界。
func (d *DataViewValue) checkBounds(offset, size int) error {
	if offset < 0 || offset+size > len(d.data) {
		return fmt.Errorf("%w: DataView out of bounds", ErrRangeError)
	}
	return nil
}

// GetInt 读取整数类型元素。
func (d *DataViewValue) GetInt(offset, size int, signed bool) (int64, error) {
	if err := d.checkBounds(offset, size); err != nil {
		return 0, err
	}
	switch size {
	case 1:
		if signed {
			return int64(int8(d.data[offset])), nil
		}
		return int64(d.data[offset]), nil
	case 2:
		v := binary.LittleEndian.Uint16(d.data[offset:])
		if signed {
			return int64(int16(v)), nil
		}
		return int64(v), nil
	case 4:
		v := binary.LittleEndian.Uint32(d.data[offset:])
		if signed {
			return int64(int32(v)), nil
		}
		return int64(v), nil
	case 8:
		v := binary.LittleEndian.Uint64(d.data[offset:])
		if signed {
			return int64(v), nil
		}
		// 无符号 64 位超出 int64 时返回原始值（走 bigint 语义由调用方处理）。
		return int64(v), nil
	}
	return 0, fmt.Errorf("%w: invalid DataView size", ErrRangeError)
}

// SetInt 写入整数类型元素。
func (d *DataViewValue) SetInt(offset, size int, signed bool, v uint64) error {
	if err := d.checkBounds(offset, size); err != nil {
		return err
	}
	switch size {
	case 1:
		d.data[offset] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(d.data[offset:], uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(d.data[offset:], uint32(v))
	case 8:
		binary.LittleEndian.PutUint64(d.data[offset:], v)
	}
	return nil
}

// GetFloat 读取浮点元素。
func (d *DataViewValue) GetFloat(offset, size int) (float64, error) {
	if err := d.checkBounds(offset, size); err != nil {
		return 0, err
	}
	if size == 4 {
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(d.data[offset:]))), nil
	}
	return math.Float64frombits(binary.LittleEndian.Uint64(d.data[offset:])), nil
}

// SetFloat 写入浮点元素。
func (d *DataViewValue) SetFloat(offset, size int, f float64) error {
	if err := d.checkBounds(offset, size); err != nil {
		return err
	}
	if size == 4 {
		binary.LittleEndian.PutUint32(d.data[offset:], math.Float32bits(float32(f)))
	} else {
		binary.LittleEndian.PutUint64(d.data[offset:], math.Float64bits(f))
	}
	return nil
}

// AsDataView 从 Value 提取 DataView。
func AsDataView(v Value) (*DataViewValue, bool) {
	d, ok := v.(*DataViewValue)
	return d, ok
}

// AsBytes 从任意字节承载值（Buffer/TypedArray/ArrayBuffer/DataView）提取字节。
func AsBytes(v Value) ([]byte, bool) {
	if bytes, ok := AsBuffer(v); ok {
		return bytes, true
	}
	if ta, ok := AsTypedArray(v); ok {
		return ta.data, true
	}
	if bytes, ok := AsArrayBuffer(v); ok {
		return bytes, true
	}
	if dv, ok := AsDataView(v); ok {
		return dv.data, true
	}
	return nil, false
}
