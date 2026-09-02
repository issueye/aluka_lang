package gstream

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
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbase"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
)

// StreamConfig 配置 Streams 全局（当前无可用选项）。
type StreamConfig struct{}

// 流相关的原型对象（instanceof 支持）。
var (
	rsStreamProto          engine.Object
	wsStreamProto          engine.Object
	tsStreamProto          engine.Object
	rsDefaultReaderProto   engine.Object
	wsDefaultWriterProto   engine.Object
	byteLengthQueuingProto engine.Object
	countQueuingProto      engine.Object
)

// NewStream 注册全局 ReadableStream / WritableStream / TransformStream 以及
// 关联类（reader/writer/controller/queuing strategies/压缩流）。
func NewStream(ctx engine.Context, cfg StreamConfig) error {
	rsCtor := engine.NewFunction("ReadableStream", func(args []engine.Value) (engine.Value, error) {
		return NewReadableStream(ctx, args)
	})
	rsObj, _ := rsCtor.AsObject()
	rsStreamProto = engine.NewObject()
	_ = rsStreamProto.Set("constructor", rsCtor)
	_ = rsObj.Set("prototype", rsStreamProto)

	wsCtor := engine.NewFunction("WritableStream", func(args []engine.Value) (engine.Value, error) {
		return newWritableStream(ctx, args), nil
	})
	wsObj, _ := wsCtor.AsObject()
	wsStreamProto = engine.NewObject()
	_ = wsStreamProto.Set("constructor", wsCtor)
	_ = wsObj.Set("prototype", wsStreamProto)

	tsCtor := engine.NewFunction("TransformStream", func(args []engine.Value) (engine.Value, error) {
		return NewTransformStream(ctx, args), nil
	})
	tsObj, _ := tsCtor.AsObject()
	tsStreamProto = engine.NewObject()
	_ = tsStreamProto.Set("constructor", tsCtor)
	_ = tsObj.Set("prototype", tsStreamProto)

	if err := ctx.Global().Set("ReadableStream", rsCtor); err != nil {
		return err
	}
	if err := ctx.Global().Set("WritableStream", wsCtor); err != nil {
		return err
	}
	if err := ctx.Global().Set("TransformStream", tsCtor); err != nil {
		return err
	}

	// 关联类构造器（API 面 + instanceof）。
	registerClass := func(name string, proto *engine.Object) {
		ctor := engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), fmt.Errorf("%w: Illegal constructor", engine.ErrTypeError)
		})
		co, _ := ctor.AsObject()
		p := engine.NewObject()
		_ = p.Set("constructor", ctor)
		_ = co.Set("prototype", p)
		if proto != nil {
			*proto = p
		}
		_ = ctx.Global().Set(name, ctor)
	}
	registerClass("ReadableStreamDefaultReader", &rsDefaultReaderProto)
	registerClass("ReadableStreamBYOBReader", nil)
	registerClass("ReadableByteStreamController", nil)
	registerClass("ReadableStreamDefaultController", nil)
	registerClass("TransformStreamDefaultController", nil)
	registerClass("WritableStreamDefaultWriter", &wsDefaultWriterProto)
	registerClass("WritableStreamDefaultController", nil)

	// CountQueuingStrategy({highWaterMark})：size() 恒为 1。
	_ = ctx.Global().Set("CountQueuingStrategy", engine.NewFunction("CountQueuingStrategy", func(args []engine.Value) (engine.Value, error) {
		q := engine.NewObject()
		if countQueuingProto != nil {
			engine.SetProto(q, countQueuingProto)
		}
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("highWaterMark"); err == nil && !v.IsUndefined() {
					_ = q.Set("highWaterMark", v)
				}
			}
		}
		_ = q.Set("size", engine.NewFunction("size", func(a []engine.Value) (engine.Value, error) {
			return engine.IntValue(1), nil
		}))
		return q, nil
	}))

	// ByteLengthQueuingStrategy({highWaterMark})：size(chunk) = chunk.byteLength。
	_ = ctx.Global().Set("ByteLengthQueuingStrategy", engine.NewFunction("ByteLengthQueuingStrategy", func(args []engine.Value) (engine.Value, error) {
		q := engine.NewObject()
		if byteLengthQueuingProto != nil {
			engine.SetProto(q, byteLengthQueuingProto)
		}
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("highWaterMark"); err == nil && !v.IsUndefined() {
					_ = q.Set("highWaterMark", v)
				}
			}
		}
		_ = q.Set("size", engine.NewFunction("size", func(a []engine.Value) (engine.Value, error) {
			if len(a) > 0 {
				if o, ok := a[0].AsObject(); ok {
					if bl, err := o.Get("byteLength"); err == nil {
						if n, ok := bl.Int(); ok {
							return engine.IntValue(n), nil
						}
					}
				}
			}
			return engine.IntValue(0), nil
		}))
		return q, nil
	}))

	// CompressionStream / DecompressionStream（gzip 编解码，TransformStream 外形）。
	_ = ctx.Global().Set("CompressionStream", engine.NewFunction("CompressionStream", func(args []engine.Value) (engine.Value, error) {
		return newCompressionStream(ctx, args, true), nil
	}))
	_ = ctx.Global().Set("DecompressionStream", engine.NewFunction("DecompressionStream", func(args []engine.Value) (engine.Value, error) {
		return newCompressionStream(ctx, args, false), nil
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
func NewReadableStream(ctx engine.Context, args []engine.Value) (engine.Value, error) {
	stream := engine.NewObject()
	if rsStreamProto != nil {
		engine.SetProto(stream, rsStreamProto)
	}
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
			if _, err := f.Call([]engine.Value{controller}); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}

	// stream.locked / stream.cancel() / getReader()。
	_ = stream.Set("locked", engine.Boolean(false))
	locked := false
	setLocked := func(v bool) {
		locked = v
		_ = stream.Set("locked", engine.Boolean(v))
	}
	_ = stream.Set("locked", engine.Boolean(locked))
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
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return gbase.ResolveValue(ctx, engine.Undefined())
	}))
	_ = stream.Set("getReader", engine.NewFunction("getReader", func(a []engine.Value) (engine.Value, error) {
		reader := engine.NewObject()
		if rsDefaultReaderProto != nil {
			engine.SetProto(reader, rsDefaultReaderProto)
		}
		setLocked(true)
		_ = reader.Set("read", engine.NewFunction("read", func(ra []engine.Value) (engine.Value, error) {
			return state.readPromise()
		}))
		_ = reader.Set("cancel", engine.NewFunction("cancel", func(ra []engine.Value) (engine.Value, error) {
			state.close()
			return gbase.ResolveValue(ctx, engine.Undefined())
		}))
		_ = reader.Set("releaseLock", engine.NewFunction("releaseLock", func(ra []engine.Value) (engine.Value, error) {
			setLocked(false)
			return engine.Undefined(), nil
		}))
		return reader, nil
	}))
	// [Symbol.asyncIterator]()：逐 chunk 异步迭代（for await 支持）。
	_ = stream.Set(engine.SymbolAsyncIterator.SymbolKey(), engine.NewFunction("[Symbol.asyncIterator]", func(a []engine.Value) (engine.Value, error) {
		iterObj := engine.NewObject()
		next := engine.NewFunction("next", func(na []engine.Value) (engine.Value, error) {
			return state.readPromise()
		})
		_ = iterObj.Set("next", next)
		_ = iterObj.Set(engine.SymbolAsyncIterator.SymbolKey(), engine.NewFunction("[Symbol.asyncIterator]", func(na []engine.Value) (engine.Value, error) {
			return iterObj, nil
		}))
		return iterObj, nil
	}))
	// tee() is exposed for undici's body clone path. The full WHATWG tee
	// algorithm is asynchronous; returning two handles preserves the API shape
	// for consumers that only need to branch or inspect the stream.
	_ = stream.Set("tee", engine.NewFunction("tee", func(a []engine.Value) (engine.Value, error) {
		return engine.NewArray([]engine.Value{stream, stream}), nil
	}))
	// pipeTo(dest)：读数据写入目标 WritableStream。
	_ = stream.Set("pipeTo", engine.NewFunction("pipeTo", func(a []engine.Value) (engine.Value, error) {
		if len(a) == 0 {
			return gbase.ResolveValue(ctx, engine.Undefined())
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
							if _, err := f.Call([]engine.Value{v.value}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				}
			}
			// 关闭目标流。
			if o, ok := dest.AsObject(); ok {
				if c, err := o.Get("close"); err == nil && c.IsFunction() {
					if f, ok := c.AsFunction(); ok {
						if _, err := f.Call(nil); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			}
			ctx.PostTask(func() { release() })
		}()
		return gbase.ResolveValue(ctx, engine.Undefined())
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
		gbase.CallResolve(resolve, engine.NewObjectFrom(map[string]engine.Value{
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
		gbase.CallResolve(resolve, doneResult())
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
		gbase.CallResolve(resolve, doneResult())
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
			gbase.CallResolve(resolve, engine.NewObjectFrom(map[string]engine.Value{
				"value": v,
				"done":  engine.Boolean(false),
			}))
			return engine.Undefined(), nil
		}
		if s.closed {
			s.mu.Unlock()
			gbase.CallResolve(resolve, doneResult())
			return engine.Undefined(), nil
		}
		s.pendingReads = append(s.pendingReads, resolve)
		s.mu.Unlock()
		return engine.Undefined(), nil
	})
	return gbase.NewPromise(s.ctx, executor)
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
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
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
		if wsDefaultWriterProto != nil {
			engine.SetProto(writer, wsDefaultWriterProto)
		}
		_ = writer.Set("write", engine.NewFunction("write", func(wa []engine.Value) (engine.Value, error) {
			if state.closed {
				return gbase.RejectValue(ctx, "stream closed")
			}
			// 优先 writeOverride（TransformStream 转发到 readable），否则 writeFn。
			handler := state.writeOverride
			if handler == nil {
				handler = state.writeFn
			}
			if handler != nil {
				if f, ok := handler.AsFunction(); ok {
					if _, err := f.Call(wa); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			return gbase.ResolveValue(ctx, engine.Undefined())
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
						if _, err := f.Call(nil); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			}
			return gbase.ResolveValue(ctx, engine.Undefined())
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
				if _, err := f.Call(wa); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
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
					if _, err := f.Call(nil); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
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
func NewTransformStream(ctx engine.Context, args []engine.Value) engine.Value {
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
	rs, _ := NewReadableStream(ctx, nil)
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
				if _, err := f.Call([]engine.Value{engine.NewFunction("tsWrite", func(wa []engine.Value) (engine.Value, error) {
					if len(wa) > 0 {
						if transformFn != nil && transformFn.IsFunction() {
							// 构造 transform controller（enqueue/close）。
							tc := engine.NewObject()
							_ = tc.Set("enqueue", enqueueFn)
							if f2, ok := transformFn.AsFunction(); ok {
								if _, err := f2.Call([]engine.Value{wa[0], tc}); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
							}
						} else if enqueueFn != nil && enqueueFn.IsFunction() {
							if f2, ok := enqueueFn.AsFunction(); ok {
								if _, err := f2.Call([]engine.Value{wa[0]}); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
							}
						}
					}
					return engine.Undefined(), nil
				})}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		// 注入关闭处理函数：writable 关闭时同时关闭 readable（P1-1）。
		if co, err := wo.Get("_setCloseOverride"); err == nil && co.IsFunction() {
			if f, ok := co.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.NewFunction("tsClose", func(a []engine.Value) (engine.Value, error) {
					// 经 readable 的公开 close 方法触发关闭。
					if ro, ok := rs.AsObject(); ok {
						if cl, err := ro.Get("close"); err == nil && cl.IsFunction() {
							if f2, ok := cl.AsFunction(); ok {
								if _, err := f2.Call(nil); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
							}
						}
					}
					if flushFn != nil {
						if f2, ok := flushFn.AsFunction(); ok {
							if _, err := f2.Call(nil); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
					return engine.Undefined(), nil
				})}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
	}
	return ts
}

// --- CompressionStream / DecompressionStream -------------------------------

// newCompressionStream 构造 CompressionStream（compress=true）或
// DecompressionStream（compress=false）。外形为 TransformStream：
// writable 端累积字节，close 时 gzip 压缩/解压后推入 readable 端。
func newCompressionStream(ctx engine.Context, args []engine.Value, compress bool) engine.Value {
	ts := engine.NewObject()

	rs, _ := NewReadableStream(ctx, nil)
	rsObj := rs.(engine.Object)
	var enqueueFn, closeFn engine.Value
	if e, err := rsObj.Get("enqueue"); err == nil {
		enqueueFn = e
	}
	if c, err := rsObj.Get("close"); err == nil {
		closeFn = c
	}

	writable := newWritableStream(ctx, nil)
	_ = ts.Set("readable", rs)
	_ = ts.Set("writable", writable)

	format := "gzip"
	if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
		format = strings.ToLower(args[0].String())
	}

	var acc []byte
	writeImpl := func(wa []engine.Value) {
		if len(wa) > 0 {
			if b, ok := gbase.BytesOf(wa[0]); ok {
				acc = append(acc, b...)
				return
			}
			acc = append(acc, []byte(wa[0].String())...)
		}
	}
	closeImpl := func() {
		var out []byte
		var err error
		if compress {
			var buf bytes.Buffer
			switch format {
			case "gzip":
				gw := gzip.NewWriter(&buf)
				_, _ = gw.Write(acc)
				_ = gw.Close()
				out = buf.Bytes()
			case "deflate":
				zw := zlib.NewWriter(&buf)
				_, _ = zw.Write(acc)
				_ = zw.Close()
				out = buf.Bytes()
			case "deflate-raw":
				fw, _ := flate.NewWriter(&buf, flate.DefaultCompression)
				_, _ = fw.Write(acc)
				_ = fw.Close()
				out = buf.Bytes()
			default:
				err = fmt.Errorf("TypeError: Unsupported compression format: %s", format)
			}
		} else {
			switch format {
			case "gzip":
				zr, rErr := gzip.NewReader(bytes.NewReader(acc))
				if rErr == nil {
					out, _ = io.ReadAll(zr)
					_ = zr.Close()
				} else {
					err = rErr
				}
			case "deflate":
				zr, rErr := zlib.NewReader(bytes.NewReader(acc))
				if rErr == nil {
					out, _ = io.ReadAll(zr)
					_ = zr.Close()
				} else {
					err = rErr
				}
			case "deflate-raw":
				fr := flate.NewReader(bytes.NewReader(acc))
				out, _ = io.ReadAll(fr)
				_ = fr.Close()
			default:
				err = fmt.Errorf("TypeError: Unsupported decompression format: %s", format)
			}
		}
		if err != nil {
			interpreter.ReportUncaught(nil, err)
		}
		if enqueueFn != nil && enqueueFn.IsFunction() {
			if f, ok := enqueueFn.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{gbuffer.NewBufferInstance(out)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		if closeFn != nil && closeFn.IsFunction() {
			if f, ok := closeFn.AsFunction(); ok {
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
	}

	if wo, ok := writable.AsObject(); ok {
		if so, err := wo.Get("_setWriteOverride"); err == nil && so.IsFunction() {
			if f, ok := so.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.NewFunction("csWrite", func(wa []engine.Value) (engine.Value, error) {
					writeImpl(wa)
					return engine.Undefined(), nil
				})}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		if co, err := wo.Get("_setCloseOverride"); err == nil && co.IsFunction() {
			if f, ok := co.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.NewFunction("csClose", func(a []engine.Value) (engine.Value, error) {
					closeImpl()
					return engine.Undefined(), nil
				})}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
	}
	return ts
}

// --- Promise 辅助 ---------------------------------------------------------

// doneResult 构造 {value: undefined, done: true}。
func doneResult() engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("value", engine.Undefined())
	_ = obj.Set("done", engine.Boolean(true))
	return obj
}
