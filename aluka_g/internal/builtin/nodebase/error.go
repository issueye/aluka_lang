// Node 风格错误码载体 CodedError 与简化深比较。

package nodebase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// CodedError 携带 Node 风格错误码的错误（经 goErrorToJSValue 转为 err.code）。
// 字段保持未导出，构造走 NewCodedError。
type CodedError struct {
	err  error
	code string
}

// NewCodedError 包装一个错误并附带 Node 错误码。
func NewCodedError(err error, code string) *CodedError {
	return &CodedError{err: err, code: code}
}

func (e *CodedError) Error() string { return e.err.Error() }

func (e *CodedError) Unwrap() error { return e.err }

func (e *CodedError) Code() string { return e.code }

// DeepStrictEqual 简化版严格相等（递归比较对象/数组）。
func DeepStrictEqual(a, b engine.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch a.Type() {
	case engine.TypeNumber:
		af, _ := a.Float()
		bf, _ := b.Float()
		return af == bf
	case engine.TypeString:
		return a.String() == b.String()
	case engine.TypeBoolean:
		ab, _ := a.Bool()
		bb, _ := b.Bool()
		return ab == bb
	case engine.TypeBigInt:
		return a.String() == b.String()
	case engine.TypeUndefined, engine.TypeNull:
		return true
	default:
		// 对象/数组/函数：递归比较（简化版，用 String 兜底）。
		return a.String() == b.String()
	}
}
