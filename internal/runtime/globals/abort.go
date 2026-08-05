package globals

// Web 中断 API：AbortController / AbortSignal（开发计划 2.7）。
//
// 实现要点：
//   - AbortSignal 复用 EventTarget 的事件能力（addEventListener 等），
//     以闭包状态持有 aborted/reason，abort() 时同步更新并触发 'abort' 事件
//     与 onabort 回调。
//   - AbortController.abort([reason]) 转发到 signal 的 abort。

import (
	"strconv"
	"time"

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

	abortSignalCtor := engine.NewFunction("AbortSignal", func(args []engine.Value) (engine.Value, error) {
		return newAbortSignalInstance(), nil
	})
	asObj, _ := abortSignalCtor.AsObject()

	// AbortSignal.timeout(ms)：定时中断（reason 为 TimeoutError 语义）。
	_ = asObj.Set("timeout", engine.NewFunction("timeout", func(args []engine.Value) (engine.Value, error) {
		ms := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				ms = n
			}
		}
		if ms < 0 {
			ms = 0
		}
		signal := newAbortSignalInstance()
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
			ctx.PostTask(func() {
				abortSignal(signal, engine.Str("TimeoutError"))
			})
		})
		return signal, nil
	}))

	// AbortSignal.any([...signals])：任一信号中断则中断。
	_ = asObj.Set("any", engine.NewFunction("any", func(args []engine.Value) (engine.Value, error) {
		signal := newAbortSignalInstance()
		if len(args) == 0 {
			return signal, nil
		}
		if arr, ok := args[0].AsObject(); ok {
			lv, err := arr.Get("length")
			if err != nil || lv.IsUndefined() {
				return signal, nil
			}
			n, _ := lv.Int()
			for i := 0; i < n; i++ {
				sv, err := arr.Get(strconv.Itoa(i))
				if err != nil || !sv.IsObject() {
					continue
				}
				src, _ := sv.AsObject()
				// 已中断：立即传播。
				if ab, err := src.Get("aborted"); err == nil {
					if b, ok := ab.Bool(); ok && b {
						reason, _ := src.Get("reason")
						abortSignal(signal, reason)
						continue
					}
				}
				// 注册 abort 监听：源信号中断时传播。
				if fn, err := src.Get("addEventListener"); err == nil && fn.IsFunction() {
					if f, ok := fn.AsFunction(); ok {
						listener := engine.NewFunction("abortListener", func(a []engine.Value) (engine.Value, error) {
							reason, _ := src.Get("reason")
							abortSignal(signal, reason)
							return engine.Undefined(), nil
						})
						_, _ = f.Call([]engine.Value{engine.Str("abort"), listener})
					}
				}
			}
		}
		return signal, nil
	}))

	_ = ctx.Global().Set("AbortSignal", abortSignalCtor)
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
