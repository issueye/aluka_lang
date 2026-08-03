// Package engine 提供 JavaScript 引擎的抽象层。
//
// 设计目标：
//   - 解耦运行时与具体引擎实现（Phase 0 为桩引擎，Phase 1 起替换为自研字节码 VM）
//   - 提供最小化的 Engine/Context/Value/Function 接口
//   - 纯 Go 实现，禁止 cgo
package engine

import "errors"

// Engine 是 JS 引擎的抽象接口。
// 一个 Engine 实例可以创建多个独立的执行上下文（Context）。
type Engine interface {
	// NewContext 创建一个新的执行上下文，全局对象独立。
	NewContext() (Context, error)
	// Shutdown 释放引擎资源。
	Shutdown() error
	// Version 返回引擎版本信息。
	Version() string
}

// Context 是一次 JS 执行上下文，对应一个全局对象与独立的模块图。
type Context interface {
	// Eval 执行一段 JS 代码，返回最后一个表达式的求值结果。
	Eval(code string, filename string) (Value, error)
	// Global 返回全局对象（globalThis）。
	Global() Object
	// RegisterFunc 在全局对象上注册一个 Go 函数。
	// 等价于 globalThis[name] = func(...) {...}。
	RegisterFunc(name string, fn Func) error
	// Close 释放上下文资源。
	Close() error
}

// Value 是 JS 值的统一抽象。
// 实现：Undefined / Null / Boolean / Number / String / Object / Function。
type Value interface {
	// Type 返回值类型。
	Type() ValueType
	// String 返回 JS 风格的字符串表示（用于 console.log 输出）。
	String() string
	// Int 返回整数值与是否可转换。
	// 数字类型按 JS ToInt32 规则；字符串尝试解析。
	Int() (int, bool)
	// Float 返回浮点数值与是否可转换。
	Float() (float64, bool)
	// Bool 返回布尔值与是否可转换（按 JS ToBoolean 规则）。
	Bool() (bool, bool)
	// IsUndefined 是否为 undefined。
	IsUndefined() bool
	// IsNull 是否为 null。
	IsNull() bool
	// IsObject 是否为 Object（含数组、函数等派生类型）。
	IsObject() bool
	// IsFunction 是否为 Function。
	IsFunction() bool
	// AsObject 尝试转换为 Object 接口。
	AsObject() (Object, bool)
	// AsFunction 尝试转换为 Function 接口。
	AsFunction() (Function, bool)
}

// Object 是 JS 对象的抽象（含属性读写、键枚举）。
type Object interface {
	Value
	// Get 读取属性，不存在时返回 Undefined（而非错误）。
	Get(key string) (Value, error)
	// Set 写入属性。
	Set(key string, value Value) error
	// Keys 返回所有可枚举自有属性名（顺序：插入顺序）。
	Keys() []string
	// Delete 删除自有属性，返回是否成功（属性不存在也返回 true）。
	Delete(key string) bool
}

// Function 是 JS 函数对象的抽象。
type Function interface {
	Value
	// Call 调用函数，返回返回值。
	Call(args []Value) (Value, error)
}

// Func 是 Go 函数的类型签名，可注册为 JS 全局函数或对象方法。
type Func func(args []Value) (Value, error)

// ValueType 枚举 JS 值类型。
type ValueType int

const (
	TypeUndefined ValueType = iota
	TypeNull
	TypeBoolean
	TypeNumber
	TypeString
	TypeObject
	TypeFunction
)

// String 返回类型名称（用于 typeof 输出）。
func (t ValueType) String() string {
	switch t {
	case TypeUndefined:
		return "undefined"
	case TypeNull:
		return "object" // JS 中 typeof null === "object"
	case TypeBoolean:
		return "boolean"
	case TypeNumber:
		return "number"
	case TypeString:
		return "string"
	case TypeObject:
		return "object"
	case TypeFunction:
		return "function"
	default:
		return "unknown"
	}
}

// 通用错误定义。
var (
	ErrNotImplemented = errors.New("aluka: not implemented")
	ErrTypeError      = errors.New("aluka: type error")
	ErrSyntaxError    = errors.New("aluka: syntax error")
	ErrReferenceError = errors.New("aluka: reference error")
	ErrRangeError     = errors.New("aluka: range error")
)
