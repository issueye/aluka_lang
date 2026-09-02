// 跨模块共享 helper（分包基座）。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// currentVM 返回当前执行上下文对应的字节码 VM（node:vm 需要编译能力）。
func currentVM(ctx engine.Context) *interpreter.VM {
	if v, ok := ctx.(*interpreter.VM); ok {
		return v
	}
	return nil
}
