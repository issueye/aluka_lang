package builtin

// node:stream/promises 内置模块——Promise 版流操作（Node ≥ 15）。
//
//	pipeline(...streams)：串联流，返回 Promise（resolve/reject）
//	finished(stream)：流结束时返回 Promise
//
// 复用 node:stream 的回调版实现（makePipeline 与 finished 的事件监听逻辑），
// 以 Go 回调包装为 Promise（interpreter.NewPromiseValue + fulfill/reject）。
import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// NewStreamPromises 构造 node:stream/promises 模块导出对象。
func NewStreamPromises(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	_ = m.Set("pipeline", engine.NewFunction("pipeline", makePromisePipeline(ctx)))
	_ = m.Set("finished", engine.NewFunction("finished", makePromiseFinished(ctx)))
	return m, nil
}

// makePromisePipeline 包装回调版 stream.pipeline：返回 Promise，
// 完成时 resolve，任一环节出错时 reject。
func makePromisePipeline(ctx engine.Context) engine.Func {
	cbPipeline := makePipeline(ctx)
	return func(args []engine.Value) (engine.Value, error) {
		vm, ok := ctx.(*interpreter.VM)
		if !ok {
			return engine.Undefined(), fmt.Errorf("stream/promises.pipeline: requires the VM engine")
		}
		p := interpreter.NewPromiseValue(vm.Interp())
		cb := makePromiseCallback(p, "__pipelinePromiseCb")
		callArgs := append(append([]engine.Value{}, args...), cb)
		if _, err := cbPipeline(callArgs); err != nil {
			p.Reject(makeErrorValue(ctx, err))
		}
		return p, nil
	}
}

// makePromiseFinished 包装 stream.finished 的事件监听逻辑：返回 Promise，
// 'finish'/'end' 时 resolve，'error' 时 reject。
func makePromiseFinished(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		vm, ok := ctx.(*interpreter.VM)
		if !ok {
			return engine.Undefined(), fmt.Errorf("stream/promises.finished: requires the VM engine")
		}
		p := interpreter.NewPromiseValue(vm.Interp())
		if len(args) == 0 {
			p.Reject(makeErrorValue(ctx, fmt.Errorf("stream/promises.finished: missing stream argument")))
			return p, nil
		}
		stream := args[0]
		cb := makePromiseCallback(p, "__finishedPromiseCb")
		// 监听 'finish'/'end'/'error' 事件（与 node:stream.finished 一致）。
		if o, ok := stream.AsObject(); ok {
			if onFn, err := o.Get("on"); err == nil && onFn.IsFunction() {
				f, _ := onFn.AsFunction()
				_, _ = f.Call([]engine.Value{engine.Str("finish"), cb})
				_, _ = f.Call([]engine.Value{engine.Str("end"), cb})
				_, _ = f.Call([]engine.Value{engine.Str("error"), cb})
			}
		}
		return p, nil
	}
}

// makePromiseCallback 构造流回调的 Promise 包装：无错误参数（undefined/null）
// 时 resolve，否则 reject（Node 回调约定：err 为第一个参数）。
func makePromiseCallback(p *interpreter.PromiseValue, name string) engine.Value {
	return engine.NewFunction(name, func(cbArgs []engine.Value) (engine.Value, error) {
		if len(cbArgs) > 0 && !cbArgs[0].IsUndefined() && !cbArgs[0].IsNull() {
			p.Reject(cbArgs[0])
		} else {
			p.Fulfill(engine.Undefined())
		}
		return engine.Undefined(), nil
	})
}
