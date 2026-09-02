// 跨模块共享 helper（分包基座）。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// newBuiltinPromise 用全局 Promise 构造器创建 Promise。
func newBuiltinPromise(ctx engine.Context, executor engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), err
	}
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), err
	}
	return pf.Call([]engine.Value{executor})
}
