package builtin

// node:stream 内置模块——流式数据处理。
//
// 设计：Readable/Writable/Duplex/Transform 都继承自 EventEmitter（通过
// newEmitterInstance 获得事件能力），在其上追加流操作方法。
// 这是 Node.js stream API 的核心：流是 EventEmitter 的子类，用 'data'/
// 'end'/'error'/'finish'/'drain' 等事件驱动数据处理。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// NewStream 构造 node:stream 模块的导出对象。
func NewStream(ctx engine.Context) (engine.Value, error) {
	// Node 中 `module.exports = Stream`，导出本身是可调用的基类构造器，
	// 同时挂载 Readable/Writable 等子类。send 等包执行 `Stream.call(this)`，
	// 因此导出必须是可调用函数而非普通对象。
	m := engine.NewFunction("Stream", func(args []engine.Value) (engine.Value, error) {
		return newReadableStream(args), nil
	})
	mObj, _ := m.AsObject()

	// Readable 构造器：new Readable(options) 创建可读流。
	readableCtor := engine.NewFunction("Readable", func(args []engine.Value) (engine.Value, error) {
		return newReadableStream(args), nil
	})
	rObj, _ := readableCtor.AsObject()
	// Readable.from(iterable)：从数组/字符串创建可读流。
	_ = rObj.Set("from", engine.NewFunction("from", func(args []engine.Value) (engine.Value, error) {
		return newReadableFrom(args), nil
	}))
	// Readable.fromWeb(webStream)：把 Web ReadableStream 包装为 Node 可读流
	// （Node ≥ 17）。经 getReader().read() Promise 链桥接数据到 push()。
	// Pi 的 tools-manager 用 pipeline(Readable.fromWeb(response.body), fileStream)。
	_ = rObj.Set("fromWeb", engine.NewFunction("fromWeb", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsObject() {
			return engine.Undefined(), fmt.Errorf("%w: Readable.fromWeb requires a ReadableStream", engine.ErrTypeError)
		}
		web := args[0]
		nodeStream := newReadableStream(nil)
		vm, ok := ctx.(*interpreter.VM)
		if !ok {
			return nodeStream, nil
		}
		// reader = web.getReader()（this 绑定经 VM.InvokeFn）。
		reader, err := invokeMethod(vm, web, "getReader")
		if err != nil || reader == nil {
			return nodeStream, nil
		}
		bridgeWebRead(vm, reader, nodeStream)
		return nodeStream, nil
	}))
	_ = mObj.Set("Readable", readableCtor)

	// Writable 构造器：new Writable(options) 创建可写流。
	writableCtor := engine.NewFunction("Writable", func(args []engine.Value) (engine.Value, error) {
		return newWritableStream(args), nil
	})
	_ = mObj.Set("Writable", writableCtor)

	// Duplex 构造器：new Duplex(options) 创建双工流。
	duplexCtor := engine.NewFunction("Duplex", func(args []engine.Value) (engine.Value, error) {
		return newDuplexStream(args), nil
	})
	_ = mObj.Set("Duplex", duplexCtor)

	// Transform 构造器：new Transform(options) 创建转换流。
	transformCtor := engine.NewFunction("Transform", func(args []engine.Value) (engine.Value, error) {
		return newTransformStream(args), nil
	})
	_ = mObj.Set("Transform", transformCtor)

	// pipeline(...streams, callback)：串联流。
	_ = mObj.Set("pipeline", engine.NewFunction("pipeline", makePipeline(ctx)))

	// finished(stream, callback)：流完成时调用回调。
	_ = mObj.Set("finished", engine.NewFunction("finished", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		stream := args[0]
		cb := args[1]
		// 监听 'finish'/'end'/'error' 事件
		if o, ok := stream.AsObject(); ok {
			if onFn, err := o.Get("on"); err == nil && onFn.IsFunction() {
				f, _ := onFn.AsFunction()
				_, _ = f.Call([]engine.Value{engine.Str("finish"), cb})
				_, _ = f.Call([]engine.Value{engine.Str("end"), cb})
				_, _ = f.Call([]engine.Value{engine.Str("error"), cb})
			}
		}
		return engine.Undefined(), nil
	}))

	_ = mObj.Set("ReadableStream", readableCtor) // 别名

	return m, nil
}

// --- Readable ------------------------------------------------------------

// newReadableStream 创建一个可读流（基于 EventEmitter）。
func newReadableStream(args []engine.Value) engine.Value {
	// stream 基于 EventEmitter（获得 on/emit/off 等事件能力）。
	stream := newEmitterInstance().(engine.Object)

	// 内部状态
	// 注意：state 通过闭包捕获，实例间隔离。
	state := &streamState{
		buffer:   []engine.Value{},
		encoding: "",
		flowing:  false,
		ended:    false,
		emitter:  stream, // 用于 emit
	}

	// 解析 options
	if len(args) > 0 {
		if opts, ok := args[0].AsObject(); ok {
			if enc, err := opts.Get("encoding"); err == nil && enc.String() != "" {
				state.encoding = enc.String()
			}
			// read 函数（自定义 _read）
			if readFn, err := opts.Get("read"); err == nil && readFn.IsFunction() {
				state.readFn = readFn
			}
		}
	}

	// push(chunk)：向流中推送数据。push(null) 表示流结束。
	_ = stream.Set("push", engine.NewFunction("push", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			chunk := args[0]
			if chunk.IsNull() {
				// null 表示流结束。
				state.ended = true
				if state.flowing {
					finishReadable(state)
					// 结束 pipe 目标流（触发其 finish 事件），否则
					// pipeline 等依赖目标流 finish 的链路会挂起。
					endPipeDest(stream)
				}
				return engine.Boolean(false), nil
			}
			if !chunk.IsUndefined() {
				state.buffer = append(state.buffer, chunk)
				if state.flowing {
					// 已 pipe：写入目标流（drainToDest）；未 pipe：
					// 发 'data' 事件。只发事件会让 pipe 后的数据丢失
					// （pipe 只经 drainToDest 写目标，无 data 监听者）。
					if dest := pipeDest(stream); dest != nil {
						drainToDest(state, dest)
					} else {
						drainBuffer(state, "data")
					}
				}
			}
		}
		return engine.Boolean(true), nil
	}))

	// read([size])：读取数据（简化：返回缓冲区第一个元素或 null）。
	_ = stream.Set("read", engine.NewFunction("read", func(args []engine.Value) (engine.Value, error) {
		if len(state.buffer) > 0 {
			chunk := state.buffer[0]
			state.buffer = state.buffer[1:]
			return chunk, nil
		}
		if state.ended {
			return engine.Null(), nil
		}
		return engine.Null(), nil
	}))

	// pause() / resume()：暂停/恢复 flowing 模式。
	_ = stream.Set("pause", engine.NewFunction("pause", func(args []engine.Value) (engine.Value, error) {
		state.flowing = false
		return stream, nil
	}))
	_ = stream.Set("resume", engine.NewFunction("resume", func(args []engine.Value) (engine.Value, error) {
		state.flowing = true
		// 立即排空缓冲区
		drainBuffer(state, "data")
		if state.ended {
			finishReadable(state)
		}
		return stream, nil
	}))

	// pipe(destination)：将流管道连接到目标可写流。
	_ = stream.Set("pipe", engine.NewFunction("pipe", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return stream, nil
		}
		dest := args[0]
		// 监听 data 事件，写入目标。
		_ = stream.Set("__pipeDest", dest)
		// 触发 flowing
		state.flowing = true
		drainToDest(state, dest)
		// 源流已结束（push(null) 早于 pipe）：结束目标流，触发其 finish
		// （pipeline 依赖目标流的 finish 事件判定完成）。
		if state.ended {
			endPipeDest(stream)
		}
		return dest, nil
	}))

	// isPaused()
	_ = stream.Set("isPaused", engine.NewFunction("isPaused", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(!state.flowing), nil
	}))

	// destroy([error])
	_ = stream.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		state.ended = true
		state.buffer = nil
		emitEvent(stream, "close")
		return stream, nil
	}))

	// 当 'data' 监听器注册时自动切换到 flowing 模式。
	// 通过覆盖 on 方法实现（包装原始 on）。
	if onFn, err := stream.Get("on"); err == nil && onFn.IsFunction() {
		origOnFn, _ := onFn.AsFunction()
		wrappedOn := engine.NewFunction("on", func(onArgs []engine.Value) (engine.Value, error) {
			result, err := origOnFn.Call(onArgs)
			if err == nil && len(onArgs) >= 2 && onArgs[0].String() == "data" {
				// 'data' 监听器注册：切换到 flowing 模式
				state.flowing = true
				drainBuffer(state, "data")
				if state.ended {
					finishReadable(state)
				}
			}
			return result, err
		})
		_ = stream.Set("on", wrappedOn)
	}

	return stream
}

// newReadableFrom 从数组/字符串创建可读流。
func newReadableFrom(args []engine.Value) engine.Value {
	stream := newReadableStream(nil)

	if len(args) > 0 {
		src := args[0]
		switch {
		case src.Type() == engine.TypeString:
			// 字符串：逐字符推送（简化：整段推送）
			streamPush(stream, src)
		default:
			// 数组：逐元素推送
			if arr, ok := src.(*engine.ArrayValue); ok {
				for _, e := range arr.Elems() {
					streamPush(stream, e)
				}
			} else if o, ok := src.AsObject(); ok {
				// 试图遍历 keys
				for _, k := range o.Keys() {
					if v, err := o.Get(k); err == nil {
						streamPush(stream, v)
					}
				}
			}
		}
	}
	// 推送 null 表示结束
	finishReadableByPush(stream)
	return stream
}

// --- Writable ------------------------------------------------------------

// newWritableStream 创建一个可写流（基于 EventEmitter）。
func newWritableStream(args []engine.Value) engine.Value {
	stream := newEmitterInstance().(engine.Object)

	state := &writableState{
		buffer:   []engine.Value{},
		ended:    false,
		finished: false,
		writeFn:  nil,
	}

	// 解析 options.write
	if len(args) > 0 {
		if opts, ok := args[0].AsObject(); ok {
			if w, err := opts.Get("write"); err == nil && w.IsFunction() {
				state.writeFn = w
			}
		}
	}

	// write(chunk[, encoding][, callback])
	_ = stream.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		chunk := args[0]
		// 如果有自定义 write 函数，调用它。
		if state.writeFn != nil {
			if f, ok := state.writeFn.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{chunk, engine.Str(""), engine.Undefined()})
			}
		} else {
			// 默认：缓冲数据。
			state.buffer = append(state.buffer, chunk)
		}
		return engine.Boolean(true), nil
	}))

	// end([chunk][, callback])
	_ = stream.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
		// 可选的最后一个 chunk
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			if args[0].Type() == engine.TypeFunction {
				// end(callback) 形式
			} else {
				// end(chunk) 形式：先写入
				if state.writeFn != nil {
					if f, ok := state.writeFn.AsFunction(); ok {
						_, _ = f.Call([]engine.Value{args[0]})
					}
				} else {
					state.buffer = append(state.buffer, args[0])
				}
			}
		}
		state.ended = true
		state.finished = true
		emitEvent(stream, "finish")
		emitEvent(stream, "close")
		return stream, nil
	}))

	// destroy([error])
	_ = stream.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		emitEvent(stream, "close")
		return stream, nil
	}))

	// cork() / uncork()（简化：无操作）
	_ = stream.Set("cork", engine.NewFunction("cork", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = stream.Set("uncork", engine.NewFunction("uncork", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// setDefaultEncoding(encoding)
	_ = stream.Set("setDefaultEncoding", engine.NewFunction("setDefaultEncoding", func(args []engine.Value) (engine.Value, error) {
		return stream, nil
	}))

	return stream
}

// --- Duplex & Transform --------------------------------------------------

// newDuplexStream 创建双工流（同时可读可写）。
func newDuplexStream(args []engine.Value) engine.Value {
	// Duplex = Readable + Writable 的合并。
	stream := newReadableStream(args).(engine.Object)
	writable := newWritableStream(args).(engine.Object)

	// 合并 writable 方法到 stream 对象。
	for _, key := range writable.Keys() {
		if v, err := writable.Get(key); err == nil {
			// 只添加 writable 特有方法，不覆盖 readable 已有的（如 on/emit）。
			if existing, _ := stream.Get(key); existing != nil && !existing.IsUndefined() {
				// 已存在：仅当是 writable 特有方法时覆盖。
				if key == "write" || key == "end" || key == "cork" || key == "uncork" || key == "setDefaultEncoding" {
					_ = stream.Set(key, v)
				}
			} else {
				_ = stream.Set(key, v)
			}
		}
	}
	return stream
}

// newTransformStream 创建转换流（可读可写，写入数据经 transform 后推到可读端）。
func newTransformStream(args []engine.Value) engine.Value {
	stream := newDuplexStream(args).(engine.Object)

	// 解析 transform 函数。
	var transformFn engine.Value
	if len(args) > 0 {
		if opts, ok := args[0].AsObject(); ok {
			if tf, err := opts.Get("transform"); err == nil && tf.IsFunction() {
				transformFn = tf
			}
		}
	}

	// 覆盖 write：写入时调 transform，结果 push 到可读端。
	if transformFn != nil {
		_ = stream.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Boolean(false), nil
			}
			chunk := args[0]
			if f, ok := transformFn.AsFunction(); ok {
				result, err := f.Call([]engine.Value{chunk, engine.Undefined(), engine.Undefined()})
				if err != nil {
					emitEvent(stream, "error")
					return engine.Boolean(false), nil
				}
				if result != nil && !result.IsUndefined() && !result.IsNull() {
					streamPush(stream, result)
				}
			}
			return engine.Boolean(true), nil
		}))
	}

	return stream
}

// --- pipeline ------------------------------------------------------------

// makePipeline 构造 pipeline 函数。
func makePipeline(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("pipeline: requires at least 2 arguments")
		}
		// 最后一个参数是 callback（可选）。
		var callback engine.Value
		streams := args
		if args[len(args)-1].IsFunction() {
			callback = args[len(args)-1]
			streams = args[:len(args)-1]
		}
		if len(streams) < 2 {
			return engine.Undefined(), fmt.Errorf("pipeline: requires at least 2 streams")
		}

		// 先监听最后一个流的 finish 事件，再串联 pipe——pipe 可能同步完成
		// （数据已缓冲时在 pipe 内部触发 finish），后注册会丢失事件导致
		// pipeline 永不完成。
		if callback != nil {
			lastStream := streams[len(streams)-1]
			if o, ok := lastStream.AsObject(); ok {
				if onFn, err := o.Get("on"); err == nil && onFn.IsFunction() {
					f, _ := onFn.AsFunction()
					cbWrapper := engine.NewFunction("pipelineCb", func(cbArgs []engine.Value) (engine.Value, error) {
						if cb, ok := callback.AsFunction(); ok {
							_, _ = cb.Call(nil)
						}
						return engine.Undefined(), nil
					})
					_, _ = f.Call([]engine.Value{engine.Str("finish"), cbWrapper})
				}
			}
		}

		// 串联 pipe：source.pipe(dest1).pipe(dest2)...
		current := streams[0]
		for i := 1; i < len(streams); i++ {
			if o, ok := current.AsObject(); ok {
				if pipeFn, err := o.Get("pipe"); err == nil && pipeFn.IsFunction() {
					f, _ := pipeFn.AsFunction()
					result, err := f.Call([]engine.Value{streams[i]})
					if err != nil {
						if callback != nil {
							if cb, ok := callback.AsFunction(); ok {
								_, _ = cb.Call([]engine.Value{engine.Str(err.Error())})
							}
						}
						return engine.Undefined(), err
					}
					current = result
					continue
				}
			}
			current = streams[i]
		}

		return streams[len(streams)-1], nil
	}
}

// --- 内部辅助 ------------------------------------------------------------

// streamState 是可读流的内部状态。
type streamState struct {
	buffer   []engine.Value // 待推送的数据
	encoding string         // 编码
	flowing  bool           // flowing 模式（true 时自动推送 data）
	ended    bool           // 流是否已结束
	readFn   engine.Value   // 自定义 _read 函数
	emitter  engine.Value   // EventEmitter 实例（用于 emit）
}

// writableState 是可写流的内部状态。
type writableState struct {
	buffer   []engine.Value
	ended    bool
	finished bool
	writeFn  engine.Value // 自定义 write 函数
}

// streamPush 向流推送数据（通过 push 方法）。
func streamPush(stream engine.Value, chunk engine.Value) {
	if o, ok := stream.AsObject(); ok {
		if pushFn, err := o.Get("push"); err == nil && pushFn.IsFunction() {
			f, _ := pushFn.AsFunction()
			_, _ = f.Call([]engine.Value{chunk})
		}
	}
}

// finishReadableByPush 推送 null 结束可读流。
func finishReadableByPush(stream engine.Value) {
	streamPush(stream, engine.Null())
}

// drainBuffer 排空缓冲区，触发指定事件（'data'）。
func drainBuffer(state *streamState, event string) {
	for len(state.buffer) > 0 {
		chunk := state.buffer[0]
		state.buffer = state.buffer[1:]
		if chunk.IsNull() {
			// null 表示流结束
			finishReadable(state)
			return
		}
		emitEvent(state.emitter, event, chunk)
	}
}

// drainToDest 排空缓冲区到目标可写流。
func drainToDest(state *streamState, dest engine.Value) {
	for len(state.buffer) > 0 {
		chunk := state.buffer[0]
		state.buffer = state.buffer[1:]
		if chunk.IsNull() {
			finishReadable(state)
			if o, ok := dest.AsObject(); ok {
				if endFn, err := o.Get("end"); err == nil && endFn.IsFunction() {
					if f, ok := endFn.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				}
			}
			return
		}
		if o, ok := dest.AsObject(); ok {
			if writeFn, err := o.Get("write"); err == nil && writeFn.IsFunction() {
				f, _ := writeFn.AsFunction()
				_, _ = f.Call([]engine.Value{chunk})
			}
		}
	}
}

// pipeDest 返回流的 pipe 目标（未 pipe 时 nil）。
func pipeDest(src engine.Object) engine.Value {
	if d, err := src.Get("__pipeDest"); err == nil {
		return d
	}
	return nil
}

// endPipeDest 结束 pipe 目标流（调用其 end()，触发 finish 事件）。
// pipeline 等依赖目标流 finish 判定完成；source 结束（push(null)）时
// 必须显式结束目标，否则链路挂起。
func endPipeDest(src engine.Object) {
	if d, err := src.Get("__pipeDest"); err == nil && d != nil {
		if o, ok := d.AsObject(); ok {
			if endFn, err := o.Get("end"); err == nil && endFn.IsFunction() {
				if f, ok := endFn.AsFunction(); ok {
					_, _ = f.Call(nil)
				}
			}
		}
	}
}

// invokeMethod 带 this 调用对象方法（VM.InvokeFn——engine.Function.Call
// 无 this 绑定，JS 方法（nativeMethod/原型方法）需要 this）。
func invokeMethod(vm *interpreter.VM, obj engine.Value, method string, args ...engine.Value) (engine.Value, error) {
	o, ok := obj.AsObject()
	if !ok {
		return engine.Undefined(), fmt.Errorf("invokeMethod: not an object")
	}
	fn, err := o.Get(method)
	if err != nil || !fn.IsFunction() {
		return engine.Undefined(), fmt.Errorf("invokeMethod: no method %q", method)
	}
	f, _ := fn.AsFunction()
	return vm.InvokeFn(f, obj, args)
}

// bridgeWebRead 循环读取 Web ReadableStream reader，把数据推入 Node 流。
// reader.read() 返回 Promise——经 Then 链逐块桥接，done 时 push(null)。
func bridgeWebRead(vm *interpreter.VM, reader, nodeStream engine.Value) {
	p, err := invokeMethod(vm, reader, "read")
	if err != nil || p == nil {
		return
	}
	pv, ok := p.(*interpreter.PromiseValue)
	if !ok {
		return
	}
	onFulfilled := engine.NewFunction("__fromWebNext", func(ca []engine.Value) (engine.Value, error) {
		// result = { done, value }
		done := false
		var value engine.Value = engine.Undefined()
		if len(ca) > 0 {
			if o, ok := ca[0].AsObject(); ok {
				if d, err := o.Get("done"); err == nil {
					if b, ok := d.Bool(); ok {
						done = b
					}
				}
				if v, err := o.Get("value"); err == nil {
					value = v
				}
			}
		}
		if done {
			pushToStream(nodeStream, engine.Null())
			return engine.Undefined(), nil
		}
		if !value.IsUndefined() {
			pushToStream(nodeStream, value)
		}
		bridgeWebRead(vm, reader, nodeStream) // 递归读取下一块
		return engine.Undefined(), nil
	})
	onRejected := engine.NewFunction("__fromWebErr", func(ca []engine.Value) (engine.Value, error) {
		pushToStream(nodeStream, engine.Null()) // 出错视为流结束
		return engine.Undefined(), nil
	})
	pv.Then(onFulfilled, onRejected)
}

// pushToStream 调用流的 push 方法（闭包方法，不依赖 this）。
func pushToStream(stream engine.Value, chunk engine.Value) {
	if o, ok := stream.AsObject(); ok {
		if p, err := o.Get("push"); err == nil && p.IsFunction() {
			if f, ok := p.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{chunk})
			}
		}
	}
}

// finishReadable 结束可读流（触发 'end' 和 'close'）。
func finishReadable(state *streamState) {
	state.ended = true
	emitEvent(state.emitter, "end")
	emitEvent(state.emitter, "close")
}

// emitEvent 在对象上触发事件（通过 emit 方法）。
func emitEvent(obj engine.Value, event string, args ...engine.Value) {
	if o, ok := obj.AsObject(); ok {
		if emitFn, err := o.Get("emit"); err == nil && emitFn.IsFunction() {
			f, _ := emitFn.AsFunction()
			allArgs := append([]engine.Value{engine.Str(event)}, args...)
			_, _ = f.Call(allArgs)
		}
	}
}
