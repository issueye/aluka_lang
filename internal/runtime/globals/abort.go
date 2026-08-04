package globals

// Web 中断 API：AbortController / AbortSignal（开发计划 2.7）。
//
// 实现要点：
//   - AbortSignal 复用 EventTarget 的事件能力（addEventListener 等），
//     以闭包状态持有 aborted/reason，abort() 时同步更新并触发 'abort' 事件
//     与 onabort 回调。
//   - AbortController.abort([reason]) 转发到 signal 的 abort。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// AbortConfig 配置 Abort 全局（当前无可用选项）。
type AbortConfig struct{}

// NewAbort 注册全局 AbortController 构造器。
func NewAbort(ctx engine.Context, cfg AbortConfig) error {
	_ = ctx.Global().Set("AbortController", engine.NewFunction("AbortController", func(args []engine.Value) (engine.Value, error) {
		ctrl := engine.NewObject()
		signal := newAbortSignalInstance()
		_ = ctrl.Set("signal", signal)

		_ = ctrl.Set("abort", engine.NewFunction("abort", func(a []engine.Value) (engine.Value, error) {
			reason := engine.Undefined()
			if len(a) > 0 {
				reason = a[0]
			}
			abortSignal(signal, reason)
			return engine.Undefined(), nil
		}))
		return ctrl, nil
	}))
	_ = ctx.Global().Set("AbortSignal", engine.NewFunction("AbortSignal", func(args []engine.Value) (engine.Value, error) {
		return newAbortSignalInstance(), nil
	}))
	return nil
}

// newAbortSignalInstance 构造 AbortSignal（基于 EventTarget）。
func newAbortSignalInstance() engine.Value {
	signal := newEventTargetInstance().(engine.Object)
	_ = signal.Set("aborted", engine.Boolean(false))
	_ = signal.Set("reason", engine.Undefined())
	_ = signal.Set("onabort", engine.Undefined())

	// throwIfAborted()：已中断则抛 reason。
	_ = signal.Set("throwIfAborted", engine.NewFunction("throwIfAborted", func(args []engine.Value) (engine.Value, error) {
		aborted, _ := signal.Get("aborted")
		if b, ok := aborted.Bool(); ok && b {
			reason, _ := signal.Get("reason")
			return engine.Undefined(), &abortError{reason: reason}
		}
		return engine.Undefined(), nil
	}))
	return signal
}

// abortError 是 throwIfAborted 抛出的错误（包装 reason）。
type abortError struct{ reason engine.Value }

func (e *abortError) Error() string {
	if e.reason == nil || e.reason.IsUndefined() {
		return "This operation was aborted"
	}
	return e.reason.String()
}

// abortSignal 触发中断：设置 aborted/reason，调 onabort，派发 'abort' 事件。
// 已中断的信号再次 abort 被忽略（Node 语义：事件只触发一次）。
func abortSignal(signal engine.Value, reason engine.Value) {
	if o, ok := signal.AsObject(); ok {
		if v, err := o.Get("aborted"); err == nil {
			if b, ok := v.Bool(); ok && b {
				return // 已中断
			}
		}
		_ = o.Set("aborted", engine.Boolean(true))
		_ = o.Set("reason", reason)
		// onabort 回调。
		if v, err := o.Get("onabort"); err == nil && v.IsFunction() {
			if f, ok := v.AsFunction(); ok {
				_, _ = f.Call(nil)
			}
		}
		// 'abort' 事件。
		if d, err := o.Get("dispatchEvent"); err == nil && d.IsFunction() {
			if f, ok := d.AsFunction(); ok {
				ev, _ := newEventInstance([]engine.Value{engine.Str("abort")}).AsObject()
				_, _ = f.Call([]engine.Value{ev})
			}
		}
	}
}
