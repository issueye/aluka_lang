// 跨模块共享：Node 风格错误码载体与深比较。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// codedError 携带 Node 风格错误码的错误（经 goErrorToJSValue 转为 err.code）。
type codedError struct {
	err  error
	code string
}

func (e *codedError) Error() string { return e.err.Error() }

func (e *codedError) Unwrap() error { return e.err }

func (e *codedError) Code() string { return e.code }

// deepStrictEqual 简化版严格相等（递归比较对象/数组）。
func deepStrictEqual(a, b engine.Value) bool {
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
