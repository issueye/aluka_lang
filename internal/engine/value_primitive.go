// 原始值：undefined / null / boolean 的 engine.Value 实现。

package engine

import (
	"strconv"
)

// undefinedValue 单例，表示 JS undefined。
type undefinedValue struct{}

// Undefined 返回 undefined 单例。
func Undefined() Value { return undefinedValue{} }

func (undefinedValue) Type() ValueType { return TypeUndefined }

func (undefinedValue) String() string { return "undefined" }

func (undefinedValue) Int() (int, bool) { return 0, false }

func (undefinedValue) Float() (float64, bool) { return 0, false }

func (undefinedValue) Bool() (bool, bool) { return false, true } // ToBoolean(undefined) = false

func (undefinedValue) IsUndefined() bool { return true }

func (undefinedValue) IsNull() bool { return false }

func (undefinedValue) IsObject() bool { return false }

func (undefinedValue) IsFunction() bool { return false }

func (undefinedValue) AsObject() (Object, bool) { return nil, false }

func (undefinedValue) AsFunction() (Function, bool) { return nil, false }

type nullValue struct{}

// Null 返回 null 单例。
func Null() Value { return nullValue{} }

func (nullValue) Type() ValueType { return TypeNull }

func (nullValue) String() string { return "null" }

func (nullValue) Int() (int, bool) { return 0, false }

func (nullValue) Float() (float64, bool) { return 0, false }

func (nullValue) Bool() (bool, bool) { return false, true } // ToBoolean(null) = false

func (nullValue) IsUndefined() bool { return false }

func (nullValue) IsNull() bool { return true }

func (nullValue) IsObject() bool { return false }

func (nullValue) IsFunction() bool { return false }

func (nullValue) AsObject() (Object, bool) { return nil, false }

func (nullValue) AsFunction() (Function, bool) { return nil, false }

type booleanValue bool

// Bool 包装 Go bool 为 JS Value。
func Boolean(b bool) Value { return booleanValue(b) }

func (b booleanValue) Type() ValueType { return TypeBoolean }

func (b booleanValue) String() string { return strconv.FormatBool(bool(b)) }

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

func (b booleanValue) Bool() (bool, bool) { return bool(b), true }

func (b booleanValue) IsUndefined() bool { return false }

func (b booleanValue) IsNull() bool { return false }

func (b booleanValue) IsObject() bool { return false }

func (b booleanValue) IsFunction() bool { return false }

func (b booleanValue) AsObject() (Object, bool) { return nil, false }

func (b booleanValue) AsFunction() (Function, bool) { return nil, false }
