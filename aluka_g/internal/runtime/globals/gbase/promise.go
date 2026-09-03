// Promise 构造与 resolve/reject 驱动。

package gbase

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// newPromise 用全局 Promise 构造器创建 Promise。
func NewPromise(ctx engine.Context, executor engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("Promise not available")
	}
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// promiseResolveValue 用 Promise.resolve 包装一个已定值。
func ResolveValue(ctx engine.Context, v engine.Value) (engine.Value, error) {
	return NewPromise(ctx, engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if f, ok := args[0].AsFunction(); ok {
				if _, err := f.Call([]engine.Value{v}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return engine.Undefined(), nil
	}))
}

// newTypeError 创建一个 TypeError / Error 实例（优先调用全局构造器）。
func NewTypeError(ctx engine.Context, msg string) engine.Value {
	if ctx != nil {
		if ctorVal, err := ctx.Global().Get("TypeError"); err == nil && ctorVal.IsFunction() {
			if fn, ok := ctorVal.AsFunction(); ok {
				if res, err := fn.Call([]engine.Value{engine.Str(msg)}); err == nil {
					return res
				}
			}
		}
		if ctorVal, err := ctx.Global().Get("Error"); err == nil && ctorVal.IsFunction() {
			if fn, ok := ctorVal.AsFunction(); ok {
				if res, err := fn.Call([]engine.Value{engine.Str(msg)}); err == nil {
					return res
				}
			}
		}
	}
	errObj := engine.NewObject()
	_ = errObj.Set("name", engine.Str("TypeError"))
	_ = errObj.Set("message", engine.Str(msg))
	return errObj
}

// promiseRejectValue 用 Promise.reject 包装错误。
func RejectValue(ctx engine.Context, msg string) (engine.Value, error) {
	return NewPromise(ctx, engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 1 {
			if f, ok := args[1].AsFunction(); ok {
				errVal := NewTypeError(ctx, msg)
				if _, err := f.Call([]engine.Value{errVal}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return engine.Undefined(), nil
	}))
}

// callResolve 调用 Promise resolve 函数。
func CallResolve(resolve engine.Value, v engine.Value) {
	if f, ok := resolve.AsFunction(); ok {
		if _, err := f.Call([]engine.Value{v}); err != nil {
			interpreter.ReportUncaught(nil, err)
		}
	}
}

// callRejectError 调用 Promise reject 函数并传入指定 Error 实例。
func CallRejectError(reject engine.Value, errVal engine.Value) {
	if f, ok := reject.AsFunction(); ok {
		if _, err := f.Call([]engine.Value{errVal}); err != nil {
			interpreter.ReportUncaught(nil, err)
		}
	}
}

// callReject 调用 Promise reject 函数。
func CallReject(reject engine.Value, msg string) {
	if f, ok := reject.AsFunction(); ok {
		errObj := engine.NewObject()
		_ = errObj.Set("name", engine.Str("TypeError"))
		_ = errObj.Set("message", engine.Str(msg))
		if _, err := f.Call([]engine.Value{errObj}); err != nil {
			interpreter.ReportUncaught(nil, err)
		}
	}
}
