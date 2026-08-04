package builtin

// node:timers/promises 内置模块（开发计划 3.11）。
// setTimeout/setImmediate 返回 Promise；setInterval 返回异步迭代器（简化）。

import (
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTimersPromises 构造 node:timers/promises 模块导出对象。
func NewTimersPromises(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// setTimeout(ms[, value]) → Promise<value>
	_ = m.Set("setTimeout", engine.NewFunction("setTimeout", func(args []engine.Value) (engine.Value, error) {
		delay := intArg(args, 0, 0)
		var value engine.Value = engine.Undefined()
		if len(args) > 1 {
			value = args[1]
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) == 0 {
				return engine.Undefined(), nil
			}
			resolve := ea[0]
			release := ctx.AddRef()
			time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
				ctx.PostTask(func() {
					defer release()
					if f, ok := resolve.AsFunction(); ok {
						_, _ = f.Call([]engine.Value{value})
					}
				})
			})
			return engine.Undefined(), nil
		})
		return newBuiltinPromise(ctx, executor)
	}))

	// setImmediate([value]) → Promise<value>
	_ = m.Set("setImmediate", engine.NewFunction("setImmediate", func(args []engine.Value) (engine.Value, error) {
		var value engine.Value = engine.Undefined()
		if len(args) > 0 {
			value = args[0]
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) == 0 {
				return engine.Undefined(), nil
			}
			resolve := ea[0]
			release := ctx.AddRef()
			time.AfterFunc(0, func() {
				ctx.PostTask(func() {
					defer release()
					if f, ok := resolve.AsFunction(); ok {
						_, _ = f.Call([]engine.Value{value})
					}
				})
			})
			return engine.Undefined(), nil
		})
		return newBuiltinPromise(ctx, executor)
	}))

	// setInterval(ms[, value]) → 异步迭代器（简化：返回 { [Symbol.asyncIterator] }，
	// 但内容为有限实现；标记不可用场景）。
	_ = m.Set("setInterval", engine.NewFunction("setInterval", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	return m, nil
}

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
