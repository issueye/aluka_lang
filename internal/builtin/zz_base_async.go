// 跨模块共享 helper（分包基座）。

package builtin

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// promiseResolve 把同步解析函数包成 Promise（异步 resolve，保持时序一致）。
func promiseResolve(ctx engine.Context, compute func() (engine.Value, error)) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("dns: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := args[0], args[1]
		release := ctx.AddRef()
		go func() {
			val, cerr := compute()
			ctx.PostTask(func() {
				defer release()
				if cerr != nil {
					if f, ok := reject.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{engine.Str(cerr.Error())}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
					return
				}
				if f, ok := resolve.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{val}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			})
		}()
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("dns: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}
