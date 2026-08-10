package builtin

// node:stream/consumers 内置模块——把流消费为值（Promise API）。
//
// 实现：基于 'data'/'end'/'error' 事件消费 Node Readable；缓冲的数据在
// on('data') 注册时由流的 flowing 包装排空。与 Node 一致，`end` 监听器
// 先于 `data` 注册，避免同步结束的流丢失 'end' 事件。
// 注意：Web ReadableStream 消费暂不支持（knownDifference，M8 补齐）。

import (
	"fmt"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewStreamConsumers 构造 node:stream/consumers 模块导出对象。
func NewStreamConsumers(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// text(stream) → Promise<string>：拼接所有 chunk 的字符串。
	_ = m.Set("text", engine.NewFunction("text", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("stream/consumers.text: requires a stream")
		}
		return consumeStream(ctx, args[0], func(chunks []engine.Value) engine.Value {
			var sb strings.Builder
			for _, c := range chunks {
				sb.WriteString(c.String())
			}
			return engine.Str(sb.String())
		})
	}))

	// buffer(stream) → Promise<Buffer>：拼接 chunk 为 Buffer。
	_ = m.Set("buffer", engine.NewFunction("buffer", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("stream/consumers.buffer: requires a stream")
		}
		return consumeStream(ctx, args[0], func(chunks []engine.Value) engine.Value {
			return concatBuffers(ctx, chunks)
		})
	}))

	// arrayBuffer(stream) → Promise<ArrayBuffer>：Buffer 的底层 buffer。
	_ = m.Set("arrayBuffer", engine.NewFunction("arrayBuffer", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("stream/consumers.arrayBuffer: requires a stream")
		}
		return consumeStream(ctx, args[0], func(chunks []engine.Value) engine.Value {
			buf := concatBuffers(ctx, chunks)
			o, ok := buf.AsObject()
			if !ok {
				return engine.Null()
			}
			if ab, err := o.Get("buffer"); err == nil && !ab.IsUndefined() {
				return ab
			}
			return engine.Null()
		})
	}))

	// blob(stream) → Promise<Blob>。
	_ = m.Set("blob", engine.NewFunction("blob", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("stream/consumers.blob: requires a stream")
		}
		return consumeStream(ctx, args[0], func(chunks []engine.Value) engine.Value {
			buf := concatBuffers(ctx, chunks)
			blobCtor, err := ctx.Global().Get("Blob")
			if err != nil || !blobCtor.IsFunction() {
				return engine.Undefined()
			}
			if f, ok := blobCtor.AsFunction(); ok {
				val, _ := f.Call([]engine.Value{engine.NewArray([]engine.Value{buf})})
				return val
			}
			return engine.Undefined()
		})
	}))

	// json(stream) → Promise<any>：text 后 JSON.parse。
	_ = m.Set("json", engine.NewFunction("json", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("stream/consumers.json: requires a stream")
		}
		return consumeStream(ctx, args[0], func(chunks []engine.Value) engine.Value {
			var sb strings.Builder
			for _, c := range chunks {
				sb.WriteString(c.String())
			}
			jsonFn, err := ctx.Global().Get("JSON")
			if err != nil {
				return engine.Undefined()
			}
			jo, _ := jsonFn.AsObject()
			if parse, err := jo.Get("parse"); err == nil && parse.IsFunction() {
				if f, ok := parse.AsFunction(); ok {
					val, _ := f.Call([]engine.Value{engine.Str(sb.String())})
					return val
				}
			}
			return engine.Undefined()
		})
	}))

	return m, nil
}

// consumeStream 消费流并在结束时把 chunks 交给 convert 转换为最终值（Promise）。
func consumeStream(ctx engine.Context, stream engine.Value, convert func([]engine.Value) engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("stream/consumers: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := args[0], args[1]
		var chunks []engine.Value
		var settled bool

		dataCb := engine.NewFunction("dataCb", func(ca []engine.Value) (engine.Value, error) {
			if len(ca) > 0 && !ca[0].IsNull() {
				chunks = append(chunks, ca[0])
			}
			return engine.Undefined(), nil
		})
		endCb := engine.NewFunction("endCb", func(ca []engine.Value) (engine.Value, error) {
			if settled {
				return engine.Undefined(), nil
			}
			settled = true
			if f, ok := resolve.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{convert(chunks)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			return engine.Undefined(), nil
		})
		errCb := engine.NewFunction("errCb", func(ca []engine.Value) (engine.Value, error) {
			if settled {
				return engine.Undefined(), nil
			}
			settled = true
			if f, ok := reject.AsFunction(); ok {
				msg := "stream error"
				if len(ca) > 0 {
					msg = ca[0].String()
				}
				if _, err := f.Call([]engine.Value{engine.Str(msg)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			return engine.Undefined(), nil
		})
		// 先注册 end/error，再注册 data（data 注册会触发缓冲区排空与同步 end）。
		_, _ = callEmitterMethod(stream, "on", []engine.Value{engine.Str("end"), endCb})
		_, _ = callEmitterMethod(stream, "on", []engine.Value{engine.Str("error"), errCb})
		_, _ = callEmitterMethod(stream, "on", []engine.Value{engine.Str("data"), dataCb})
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("stream/consumers: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// concatBuffers 用全局 Buffer.concat 拼接 chunk。
func concatBuffers(ctx engine.Context, chunks []engine.Value) engine.Value {
	// 每个 chunk 转 Buffer（字符串用 Buffer.from）。
	bufVals := make([]engine.Value, 0, len(chunks))
	bufferCtor, err := ctx.Global().Get("Buffer")
	if err != nil || !bufferCtor.IsFunction() {
		return engine.Undefined()
	}
	bcf, _ := bufferCtor.AsFunction()
	for _, c := range chunks {
		b, _ := bcf.Call([]engine.Value{c})
		bufVals = append(bufVals, b)
	}
	if bo, ok := bufferCtor.AsObject(); ok {
		if concat, err := bo.Get("concat"); err == nil && concat.IsFunction() {
			if f, ok := concat.AsFunction(); ok {
				val, _ := f.Call([]engine.Value{engine.NewArray(bufVals)})
				return val
			}
		}
	}
	// 退化：返回拼接字符串。
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(c.String())
	}
	return engine.Str(sb.String())
}
