// 跨模块共享 helper（分包基座）。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// callBuiltinResolve 调用 Promise resolve/reject 函数。
func callBuiltinResolve(fn engine.Value, v engine.Value) {
	if f, ok := fn.AsFunction(); ok {
		if _, err := f.Call([]engine.Value{v}); err != nil {
			interpreter.ReportUncaught(nil, err)
		}
	}
}
