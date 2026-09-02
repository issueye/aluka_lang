// 跨模块共享 helper（分包基座）。

package builtin

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// promiseResolved 构造一个立即 resolve 的 Promise。
func promiseResolved(ctx engine.Context, val engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("readline/promises: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 1 {
			if f, ok := args[0].AsFunction(); ok {
				if _, err := f.Call([]engine.Value{val}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("readline/promises: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}
