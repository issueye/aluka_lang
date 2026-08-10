package builtin

// node:timers/promises 内置模块（开发计划 3.11）。
// setTimeout/setImmediate 返回 Promise；setInterval 返回异步迭代器（简化）。

import (
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"sync"
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
						if _, err := f.Call([]engine.Value{value}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
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
						if _, err := f.Call([]engine.Value{value}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				})
			})
			return engine.Undefined(), nil
		})
		return newBuiltinPromise(ctx, executor)
	}))

	// setInterval(ms[, value]) → 异步迭代器（for await 每次迭代 resolve value）。
	_ = m.Set("setInterval", engine.NewFunction("setInterval", func(args []engine.Value) (engine.Value, error) {
		delay := intArg(args, 0, 1)
		if delay <= 0 {
			delay = 1
		}
		var value engine.Value = engine.Undefined()
		if len(args) > 1 {
			value = args[1]
		}

		ch := make(chan engine.Value, 16)
		stop := make(chan struct{})
		var stopOnce sync.Once
		stopFn := func() { stopOnce.Do(func() { close(stop) }) }

		// 定时器 goroutine：每个间隔投递一个值。
		go func() {
			ticker := time.NewTicker(time.Duration(delay) * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					select {
					case ch <- value:
					case <-stop:
						return
					}
				case <-stop:
					return
				}
			}
		}()

		// 返回 { [Symbol.asyncIterator]() } 对象。
		iter := engine.NewObject()
		_ = iter.Set(engine.SymbolAsyncIterator.SymbolKey(), engine.NewFunction("__asyncIterator", func(ia []engine.Value) (engine.Value, error) {
			it := engine.NewObject()
			// next() → Promise<{value, done}>
			_ = it.Set("next", engine.NewFunction("next", func(na []engine.Value) (engine.Value, error) {
				executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
					if len(ea) == 0 {
						return engine.Undefined(), nil
					}
					resolve := ea[0]
					release := ctx.AddRef()
					go func() {
						select {
						case v := <-ch:
							ctx.PostTask(func() {
								defer release()
								callBuiltinResolve(resolve, iterationResult(v, false))
							})
						case <-stop:
							ctx.PostTask(func() {
								defer release()
								callBuiltinResolve(resolve, iterationResult(engine.Undefined(), true))
							})
						}
					}()
					return engine.Undefined(), nil
				})
				return newBuiltinPromise(ctx, executor)
			}))
			// return()：停止定时器并结束迭代。
			_ = it.Set("return", engine.NewFunction("return", func(ra []engine.Value) (engine.Value, error) {
				stopFn()
				executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
					if len(ea) > 0 {
						callBuiltinResolve(ea[0], iterationResult(engine.Undefined(), true))
					}
					return engine.Undefined(), nil
				})
				return newBuiltinPromise(ctx, executor)
			}))
			return it, nil
		}))
		return iter, nil
	}))

	return m, nil
}

// iterationResult 构造迭代器结果 {value, done}。
func iterationResult(value engine.Value, done bool) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("value", value)
	_ = obj.Set("done", engine.Boolean(done))
	return obj
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
