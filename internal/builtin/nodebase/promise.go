// Promise 构造与 resolve/reject 驱动 helper。

package nodebase

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

func NewBuiltinPromise(ctx engine.Context, executor engine.Value) (engine.Value, error) {
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

func CallBuiltinResolve(fn engine.Value, v engine.Value) {
	if f, ok := fn.AsFunction(); ok {
		if _, err := f.Call([]engine.Value{v}); err != nil {
			interpreter.ReportUncaught(nil, err)
		}
	}
}

func PromiseResolve(ctx engine.Context, compute func() (engine.Value, error)) (engine.Value, error) {
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

func PromiseResolved(ctx engine.Context, val engine.Value) (engine.Value, error) {
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
