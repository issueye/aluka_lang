// 从 engine.Context 取回底层字节码 VM。

package nodebase

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// currentVM 返回当前执行上下文对应的字节码 VM（node:vm 需要编译能力）。
func CurrentVM(ctx engine.Context) *interpreter.VM {
	if v, ok := ctx.(*interpreter.VM); ok {
		return v
	}
	return nil
}
