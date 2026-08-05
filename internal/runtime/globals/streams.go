package globals

// Web Streams API：ReadableStream / WritableStream / TransformStream
// （开发计划 3.3）。
//
// 实现要点（基础但实用的队列模型）：
//   - ReadableStream：内部 value 队列 + pending reads（read() 的 Promise
//     resolve）；start 回调在构造时同步调用，controller 提供 enqueue/close/
//     error；getReader().read() 返回 Promise<{value, done}>。
//   - WritableStream：write/close/abort 回调；getWriter().write() 返回
//     Promise。
//   - TransformStream：writable 端写入经 transform 回调后入 readable 端队列。
//   - pipeTo：读源流逐个写目标流。

import (
	"fmt"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
)

// StreamConfig 配置 Streams 全局（当前无可用选项）。
type StreamConfig struct{}

// NewStream 注册全局 ReadableStream / WritableStream / TransformStream。
func NewStream(ctx engine.Context, cfg StreamConfig) error {
	_ = ctx.Global().Set("ReadableStream", engine.NewFunction("ReadableStream", func(args []engine.Value) (engine.Value, error) {
		return newReadableStream(ctx, args)
	}))
	_ = ctx.Global().Set("WritableStream", engine.NewFunction("WritableStream", func(args []engine.Value) (engine.Value, error) {
		return newWritableStream(ctx, args), nil
	}))
	_ = ctx.Global().Set("TransformStream", engine.NewFunction("TransformStream", func(args []engine.Value) (engine.Value, error) {
		return newTransformStream(ctx, args), nil
	}))
	return nil
}

// --- ReadableStream -------------------------------------------------------

// rsState 是 ReadableStream 的内部状态。
type rsState struct {
	ctx     engine.Context
	mu      sync.Mutex
	queue   []engine.Value
	closed  bool
	errored bool
	// pendingReads 保存等数据的 read() resolve 函数。
	pendingReads []engine.Value
	startFn      engine.Value
	pullFn       engine.Value
	cancelFn     engine.Value
}

// newReadableStream 构造 ReadableStream（调用 start 回调）。
func newReadableStream(ctx engine.Context, args []engine.Value) (engine.Value, error) {
	stream := engine.NewObject()
	state := &rsState{ctx: ctx}

	// 解析 underlyingSource。
	var source engine.Value
	if len(args) > 0 && args[0].IsObject() {
		source = args[0]
		if o, ok := source.AsObject(); ok {
			if v, err := o.Get("start"); err == nil && v.IsFunction() {
				state.startFn = v
			}
			if v, err := o.Get("pull"); err == nil && v.IsFunction() {
				state.pullFn = v
			}
			if v, err := o.Get("cancel"); err == nil && v.IsFunction() {
				state.cancelFn = v
			}
		}
	}

	// controller：enqueue/close/error。
	controller := engine.NewObject()
	_ = controller.Set("enqueue", engine.NewFunction("enqueue", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			state.enqueue(a[0])
		}
		return engine.Undefined(), nil
	}))
	_ = controller.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
		state.close()
		return engine.Undefined(), nil
	}))
	_ = controller.Set("error", engine.NewFunction("error", func(a []engine.Value) (engine.Value, error) {
		state.error()
		return engine.Undefined(), nil
	}))

	// 调用 start 回调（同步）。
	if state.startFn != nil {
		if f, ok := state.startFn.AsFunction(); ok {
			_, _ = f.Call([]engine.Value{controller})
		}
	}

	// stream.locked / stream.cancel() / getReader()。
	_ = stream.Set("locked", engine.Boolean(false))
	// 公开的 enqueue/close（P1-1）：供 TransformStream 的 writable 端把
	// 数据推入 readable 内部队列（controller 上的同名方法仍存在）。
	_ = stream.Set("enqueue", engine.NewFunction("enqueue", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			state.enqueue(a[0])
		}
		return engine.Undefined(), nil
	}))
	_ = stream.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
		state.close()
		return engine.Undefined(), nil
	}))
	_ = stream.Set("cancel", engine.NewFunction("cancel", func(a []engine.Value) (engine.Value, error) {
		state.close()
		if state.cancelFn != nil {
			if f, ok := state.cancelFn.AsFunction(); ok {
				_, _ = f.Call(nil)
			}
		}
		return promiseResolveValue(ctx, engine.Undefined())
	}))
	_ = stream.Set("getReader", engine.NewFunction("getReader", func(a []engine.Value) (engine.Value, error) {
		reader := engine.NewObject()
		_ = reader.Set("read", engine.NewFunction("read", func(ra []engine.Value) (engine.Value, error) {
			return state.readPromise()
		}))
		_ = reader.Set("cancel", engine.NewFunction("cancel", func(ra []engine.Value) (engine.Value, error) {
			state.close()
			return promiseResolveValue(ctx, engine.Undefined())
		}))
		_ = reader.Set("releaseLock", engine.NewFunction("releaseLock", func(ra []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		return reader, nil
	}))
	// pipeTo(dest)：读数据写入目标 WritableStream。
	_ = stream.Set("pipeTo", engine.NewFunction("pipeTo", func(a []engine.Value) (engine.Value, error) {
		if len(a) == 0 {
			return promiseResolveValue(ctx, engine.Undefined())
		}
		dest := a[0]
		release := ctx.AddRef()
		go func() {
			for {
				v := state.readSync()
				if v.done {
					break
				}
				if o, ok := dest.AsObject(); ok {
					if w, err := o.Get("write"); err == nil && w.IsFunction() {
						if f, ok := w.AsFunction(); ok {
							_, _ = f.Call([]engine.Value{v.value})
						}
					}
				}
			}
			// 关闭目标流。
			if o, ok := dest.AsObject(); ok {
				if c, err := o.Get("close"); err == nil && c.IsFunction() {
					if f, ok := c.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				}
			}
			ctx.PostTask(func() { release() })
		}()
		return promiseResolveValue(ctx, engine.Undefined())
	}))

	return stream, nil
}

// enqueue 入队并 resolve 一个等待的 read。
func (s *rsState) enqueue(v engine.Value) {
	s.mu.Lock()
	if len(s.pendingReads) > 0 {
		resolve := s.pendingReads[0]
		s.pendingReads = s.pendingReads[1:]
		s.mu.Unlock()
		callResolve(resolve, engine.NewObjectFrom(map[string]engine.Value{
			"value": v,
			"done":  engine.Boolean(false),
		}))
		return
	}
	s.queue = append(s.queue, v)
	s.mu.Unlock()
}

// close 关闭流并 resolve 等待的 read 为 done。
func (s *rsState) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pending := s.pendingReads
	s.pendingReads = nil
	s.mu.Unlock()
	for _, resolve := range pending {
		callResolve(resolve, doneResult())
	}
}

// error 标记错误。
func (s *rsState) error() {
	s.mu.Lock()
	s.errored = true
	s.closed = true
	pending := s.pendingReads
	s.pendingReads = nil
	s.mu.Unlock()
	for _, resolve := range pending {
		callResolve(resolve, doneResult())
	}
}

// readPromise 返回 read() 的 Promise。
func (s *rsState) readPromise() (engine.Value, error) {
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		resolve := args[0]
		s.mu.Lock()
		if len(s.queue) > 0 {
			v := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			callResolve(resolve, engine.NewObjectFrom(map[string]engine.Value{
				"value": v,
				"done":  engine.Boolean(false),
			}))
			return engine.Undefined(), nil
		}
		if s.closed {
			s.mu.Unlock()
			callResolve(resolve, doneResult())
			return engine.Undefined(), nil
		}
		s.pendingReads = append(s.pendingReads, resolve)
		s.mu.Unlock()
		return engine.Undefined(), nil
	})
	return newPromise(s.ctx, executor)
}

// readSync 同步读一个 chunk（pipeTo 用）。
type rsChunk struct {
	value engine.Value
	done  bool
}

func (s *rsState) readSync() rsChunk {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			v := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return rsChunk{value: v, done: false}
		}
		if s.closed {
			s.mu.Unlock()
			return rsChunk{done: true}
		}
		s.mu.Unlock()
		// 等待数据（轻量轮询）。
		// 简化：pipeTo 场景数据已预填。
		if s.pullFn != nil {
			if f, ok := s.pullFn.AsFunction(); ok {
				_, _ = f.Call(nil)
			}
		}
		// 避免忙等：让出。
		// 此处数据通常已由 start/enqueue 预填，直接返回空。
		if len(s.queue) == 0 && !s.closed {
			return rsChunk{done: true}
		}
	}
}

// --- WritableStream -------------------------------------------------------

// wsState 是 WritableStream 的内部状态。
type wsState struct {
	writeFn engine.Value
	closeFn engine.Value
	abortFn engine.Value
	// writeOverride 是 TransformStream 等场景注入的写入处理函数：
	// 优先于 writeFn（writer.write 与 stream.write 均走此路径，P1-1）。
	writeOverride engine.Value
	// closeOverride 是 TransformStream 等场景注入的关闭处理函数：
	// writer.close / stream.close 时调用（把 readable 端一并关闭）。
	closeOverride engine.Value
	closed        bool
}

// newWritableStream 构造 WritableStream。
func newWritableStream(ctx engine.Context, args []engine.Value) engine.Value {
	stream := engine.NewObject()
	state := &wsState{}

	if len(args) > 0 && args[0].IsObject() {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("write"); err == nil && v.IsFunction() {
				state.writeFn = v
			}
			if v, err := o.Get("close"); err == nil && v.IsFunction() {
				state.closeFn = v
			}
			if v, err := o.Get("abort"); err == nil && v.IsFunction() {
				state.abortFn = v
			}
		}
	}

	// setWriteOverride 由 TransformStream 注入写处理函数。
	_ = stream.Set("_setWriteOverride", engine.NewFunction("_setWriteOverride", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			state.writeOverride = a[0]
		}
		return engine.Undefined(), nil
	}))
	// setCloseOverride 由 TransformStream 注入关闭处理函数。
	_ = stream.Set("_setCloseOverride", engine.NewFunction("_setCloseOverride", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			state.closeOverride = a[0]
		}
		return engine.Undefined(), nil
	}))
	// getWriterOverride 供 TransformStream 读取当前写处理函数。
	_ = stream.Set("_getWriteOverride", engine.NewFunction("_getWriteOverride", func(a []engine.Value) (engine.Value, error) {
		if state.writeOverride != nil {
			return state.writeOverride, nil
		}
		if state.writeFn != nil {
			return state.writeFn, nil
		}
		return engine.Undefined(), nil
	}))

	_ = stream.Set("getWriter", engine.NewFunction("getWriter", func(a []engine.Value) (engine.Value, error) {
		writer := engine.NewObject()
		_ = writer.Set("write", engine.NewFunction("write", func(wa []engine.Value) (engine.Value, error) {
			if state.closed {
				return promiseRejectValue(ctx, "stream closed")
			}
			// 优先 writeOverride（TransformStream 转发到 readable），否则 writeFn。
			handler := state.writeOverride
			if handler == nil {
				handler = state.writeFn
			}
			if handler != nil {
				if f, ok := handler.AsFunction(); ok {
					_, _ = f.Call(wa)
				}
			}
			return promiseResolveValue(ctx, engine.Undefined())
		}))
		_ = writer.Set("close", engine.NewFunction("close", func(wa []engine.Value) (engine.Value, error) {
			if !state.closed {
				state.closed = true
				// 优先 closeOverride（TransformStream 关闭 readable），否则 closeFn。
				handler := state.closeOverride
				if handler == nil {
					handler = state.closeFn
				}
				if handler != nil {
					if f, ok := handler.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				}
			}
			return promiseResolveValue(ctx, engine.Undefined())
		}))
		return writer, nil
	}))
	// 供 pipeTo 直接调用。
	_ = stream.Set("write", engine.NewFunction("write", func(wa []engine.Value) (engine.Value, error) {
		handler := state.writeOverride
		if handler == nil {
			handler = state.writeFn
		}
		if handler != nil {
			if f, ok := handler.AsFunction(); ok {
				_, _ = f.Call(wa)
			}
		}
		return engine.Undefined(), nil
	}))
	_ = stream.Set("close", engine.NewFunction("close", func(wa []engine.Value) (engine.Value, error) {
		if !state.closed {
			state.closed = true
			handler := state.closeOverride
			if handler == nil {
				handler = state.closeFn
			}
			if handler != nil {
				if f, ok := handler.AsFunction(); ok {
					_, _ = f.Call(nil)
				}
			}
		}
		return engine.Undefined(), nil
	}))

	return stream
}

// --- TransformStream ------------------------------------------------------

// newTransformStream 构造 TransformStream。
// readable 端 queue 由 transform 回调 push；writable 端 write 调 transform。
func newTransformStream(ctx engine.Context, args []engine.Value) engine.Value {
	ts := engine.NewObject()

	// readable 端复用 ReadableStream。
	var transformFn, flushFn engine.Value
	if len(args) > 0 && args[0].IsObject() {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("transform"); err == nil && v.IsFunction() {
				transformFn = v
			}
			if v, err := o.Get("flush"); err == nil && v.IsFunction() {
				flushFn = v
			}
		}
	}
	_ = flushFn // flush 在 writable close 时调用（简化场景通常不依赖）

	// readable：数据经 enqueue 注入。
	rs, _ := newReadableStream(ctx, nil)
	rsObj := rs.(engine.Object)
	var enqueueFn engine.Value
	if e, err := rsObj.Get("enqueue"); err == nil {
		enqueueFn = e
	}

	// writable：write 经 transform 转发到 readable。
	writable := newWritableStream(ctx, nil)
	_ = ts.Set("readable", rs)
	_ = ts.Set("writable", writable)

	// 注入写处理函数（P1-1）：writer.write 与 stream.write 均经此路径，
	// 确保 getWriter().write() 的数据流向 readable 端。
	if wo, ok := writable.AsObject(); ok {
		if so, err := wo.Get("_setWriteOverride"); err == nil && so.IsFunction() {
			if f, ok := so.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.NewFunction("tsWrite", func(wa []engine.Value) (engine.Value, error) {
					if len(wa) > 0 {
						if transformFn != nil && transformFn.IsFunction() {
							// 构造 transform controller（enqueue/close）。
							tc := engine.NewObject()
							_ = tc.Set("enqueue", enqueueFn)
							if f2, ok := transformFn.AsFunction(); ok {
								_, _ = f2.Call([]engine.Value{wa[0], tc})
							}
						} else if enqueueFn != nil && enqueueFn.IsFunction() {
							if f2, ok := enqueueFn.AsFunction(); ok {
								_, _ = f2.Call([]engine.Value{wa[0]})
							}
						}
					}
					return engine.Undefined(), nil
				})})
			}
		}
		// 注入关闭处理函数：writable 关闭时同时关闭 readable（P1-1）。
		if co, err := wo.Get("_setCloseOverride"); err == nil && co.IsFunction() {
			if f, ok := co.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.NewFunction("tsClose", func(a []engine.Value) (engine.Value, error) {
					// 经 readable 的公开 close 方法触发关闭。
					if ro, ok := rs.AsObject(); ok {
						if cl, err := ro.Get("close"); err == nil && cl.IsFunction() {
							if f2, ok := cl.AsFunction(); ok {
								_, _ = f2.Call(nil)
							}
						}
					}
					if flushFn != nil {
						if f2, ok := flushFn.AsFunction(); ok {
							_, _ = f2.Call(nil)
						}
					}
					return engine.Undefined(), nil
				})})
			}
		}
	}
	return ts
}

// --- Promise 辅助 ---------------------------------------------------------

// newPromise 用全局 Promise 构造器创建 Promise。
func newPromise(ctx engine.Context, executor engine.Value) (engine.Value, error) {
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
func promiseResolveValue(ctx engine.Context, v engine.Value) (engine.Value, error) {
	return newPromise(ctx, engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if f, ok := args[0].AsFunction(); ok {
				_, _ = f.Call([]engine.Value{v})
			}
		}
		return engine.Undefined(), nil
	}))
}

// promiseRejectValue 用 Promise.reject 包装错误。
func promiseRejectValue(ctx engine.Context, msg string) (engine.Value, error) {
	return newPromise(ctx, engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 1 {
			if f, ok := args[1].AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.Str(msg)})
			}
		}
		return engine.Undefined(), nil
	}))
}

// doneResult 构造 {value: undefined, done: true}。
func doneResult() engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("value", engine.Undefined())
	_ = obj.Set("done", engine.Boolean(true))
	return obj
}

// callResolve 调用 Promise resolve 函数。
func callResolve(resolve engine.Value, v engine.Value) {
	if f, ok := resolve.AsFunction(); ok {
		_, _ = f.Call([]engine.Value{v})
	}
}

// callReject 调用 Promise reject 函数。
func callReject(reject engine.Value, msg string) {
	if f, ok := reject.AsFunction(); ok {
		_, _ = f.Call([]engine.Value{engine.Str(msg)})
	}
}
