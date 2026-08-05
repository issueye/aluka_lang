package engine

// DateValue 是 JS Date 对象：内部持有自纪元（1970-01-01T00:00:00Z）起的毫秒时间值。
// 结构仿照 ArrayValue——嵌入 *objectValue 以支持 own 属性与原型链，同时携带 ms 字段。
type DateValue struct {
	*objectValue
	ms float64 // 内部时间值（毫秒）；NaN 表示无效日期（Invalid Date）
}

// NewDateValue 创建持有指定毫秒时间值的 Date 对象。
func NewDateValue(ms float64) *DateValue {
	d := &DateValue{
		objectValue: &objectValue{shape: rootShape},
		ms:          ms,
	}
	register(d.objectValue)
	return d
}

func (d *DateValue) Type() ValueType { return TypeObject }

// AsObject 返回自身，保证 Get/Set 命中 DateValue 的处理。
func (d *DateValue) AsObject() (Object, bool) { return d, true }

// String 返回 JS 风格的本地时间字符串（如 "Wed Aug 05 2026 12:00:00 GMT+0800"）。
func (d *DateValue) String() string { return FormatDateString(d.ms) }

func (d *DateValue) Int() (int, bool)             { return 0, false }
func (d *DateValue) Float() (float64, bool)       { return 0, false }
func (d *DateValue) Bool() (bool, bool)           { return true, true }
func (d *DateValue) IsUndefined() bool            { return false }
func (d *DateValue) IsNull() bool                 { return false }
func (d *DateValue) IsObject() bool               { return true }
func (d *DateValue) IsFunction() bool             { return false }
func (d *DateValue) AsFunction() (Function, bool) { return nil, false }

// Get/Set/Keys/Delete 委托给嵌入的 objectValue（原型链含 Date.prototype）。
func (d *DateValue) Get(key string) (Value, error) { return d.objectValue.Get(key) }
func (d *DateValue) Set(key string, value Value) error {
	d.objectValue.Set(key, value)
	return nil
}
func (d *DateValue) Keys() []string { return d.objectValue.Keys() }
func (d *DateValue) Delete(key string) bool { return d.objectValue.Delete(key) }

// TimeMs 返回内部毫秒时间值。
func (d *DateValue) TimeMs() float64 { return d.ms }

// SetTimeMs 设置内部毫秒时间值。
func (d *DateValue) SetTimeMs(ms float64) { d.ms = ms }

// AsDate 尝试将 Value 转为 *DateValue。
func AsDate(v Value) (*DateValue, bool) {
	d, ok := v.(*DateValue)
	return d, ok
}
