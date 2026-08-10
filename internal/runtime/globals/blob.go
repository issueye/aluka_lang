package globals

// Web API：Blob / File（开发计划 3.4）。
//
// Blob 持有一段字节（由 parts 拼接），提供 size/type/text()/arrayBuffer()/
// slice()。File 继承 Blob 并附加 name/lastModified。
// text()/arrayBuffer() 返回 Promise（WHATWG 语义），用全局 Promise 包装。

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// BlobConfig 配置 Blob 全局（当前无可用选项）。
type BlobConfig struct{}

// NewBlob 注册全局 Blob / File。
func NewBlob(ctx engine.Context, cfg BlobConfig) error {
	blobCtor := engine.NewFunction("Blob", func(args []engine.Value) (engine.Value, error) {
		return newBlobInstance(ctx, args, ""), nil
	})
	blobObj, _ := blobCtor.AsObject()
	blobProto = engine.NewObject()
	_ = blobProto.Set("constructor", blobCtor)
	_ = blobObj.Set("prototype", blobProto)

	fileCtor := engine.NewFunction("File", func(args []engine.Value) (engine.Value, error) {
		name := ""
		parts := args
		if len(args) > 1 {
			name = args[1].String()
			parts = args[:1]
		}
		typeHint := ""
		lastModified := int64(0)
		if len(args) > 2 && args[2].IsObject() {
			if o, ok := args[2].AsObject(); ok {
				if v, err := o.Get("type"); err == nil && !v.IsUndefined() {
					typeHint = v.String()
				}
				if v, err := o.Get("lastModified"); err == nil && !v.IsUndefined() {
					if n, ok := v.Int(); ok {
						lastModified = int64(n)
					}
				}
			}
		}
		blob := newBlobInstance(ctx, parts, typeHint).(engine.Object)
		_ = blob.Set("name", engine.Str(name))
		_ = blob.Set("lastModified", engine.IntValue(int(lastModified)))
		if fileProto != nil {
			engine.SetProto(blob, fileProto)
		}
		return blob, nil
	})
	fileObj, _ := fileCtor.AsObject()
	fileProto = engine.NewObject()
	if blobProto != nil {
		engine.SetProto(fileProto, blobProto)
	}
	_ = fileProto.Set("constructor", fileCtor)
	_ = fileObj.Set("prototype", fileProto)

	if err := ctx.Global().Set("Blob", blobCtor); err != nil {
		return err
	}
	return ctx.Global().Set("File", fileCtor)
}

// blobProto / fileProto 是 Blob / File 实例原型（instanceof 支持）。
var (
	blobProto engine.Object
	fileProto engine.Object
)

// newBlobInstance 构造 Blob（parts 支持字符串/Buffer/Blob）。
func newBlobInstance(ctx engine.Context, args []engine.Value, typeHint string) engine.Value {
	blob := engine.NewObject()
	if blobProto != nil {
		engine.SetProto(blob, blobProto)
	}

	// 收集字节 + 保留各部分（stream() 逐 part 产出，node 语义）。
	var data []byte
	var chunks [][]byte
	if len(args) > 0 {
		if a, ok := args[0].(*engine.ArrayValue); ok {
			for _, part := range a.Elems() {
				b := blobPartBytes(part)
				data = append(data, b...)
				chunks = append(chunks, b)
			}
		} else {
			b := blobPartBytes(args[0])
			data = append(data, b...)
			chunks = append(chunks, b)
		}
	}

	// options：{type}。
	blobType := typeHint
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("type"); err == nil && !v.IsUndefined() {
				blobType = v.String()
			}
		}
	}
	// File 的 options 在 args[2]（[parts, name, options]）。
	if typeHint == "" && len(args) > 2 && args[2].IsObject() {
		if o, ok := args[2].AsObject(); ok {
			if v, err := o.Get("type"); err == nil && !v.IsUndefined() {
				blobType = v.String()
			}
		}
	}

	_ = blob.Set("size", engine.IntValue(len(data)))
	_ = blob.Set("type", engine.Str(blobType))
	// 内部数据（供嵌套 Blob part 拼接与测试访问）。
	_ = blob.Set("_data", NewBufferInstance(data))

	// text() → Promise<string>
	_ = blob.Set("text", engine.NewFunction("text", func(a []engine.Value) (engine.Value, error) {
		return promiseResolveValue(ctx, engine.Str(string(data)))
	}))
	// arrayBuffer() → Promise<Buffer>
	_ = blob.Set("arrayBuffer", engine.NewFunction("arrayBuffer", func(a []engine.Value) (engine.Value, error) {
		return promiseResolveValue(ctx, NewBufferInstance(data))
	}))
	// bytes() → Promise<Uint8Array>（Node ≥ 21.0）
	_ = blob.Set("bytes", engine.NewFunction("bytes", func(a []engine.Value) (engine.Value, error) {
		return promiseResolveValue(ctx, NewBufferInstance(data))
	}))
	// stream() → ReadableStream（逐 part 推入 Buffer 后关闭，node 语义）
	_ = blob.Set("stream", engine.NewFunction("stream", func(a []engine.Value) (engine.Value, error) {
		stream, _ := newReadableStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{
			"start": engine.NewFunction("start", func(a2 []engine.Value) (engine.Value, error) {
				if len(a2) > 0 {
					if c, ok := a2[0].AsObject(); ok {
						if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
							if f, ok := e.AsFunction(); ok {
								for _, ch := range chunks {
									if _, err := f.Call([]engine.Value{NewBufferInstance(ch)}); err != nil {
										interpreter.ReportUncaught(nil, err)
									}
								}
							}
						}
						if cl, err := c.Get("close"); err == nil && cl.IsFunction() {
							if f, ok := cl.AsFunction(); ok {
								if _, err := f.Call(nil); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
							}
						}
					}
				}
				return engine.Undefined(), nil
			}),
		})})
		return stream, nil
	}))
	// slice([start[, end[, type]]]) → 新 Blob（start/end 支持负数，node 语义）
	_ = blob.Set("slice", engine.NewFunction("slice", func(a []engine.Value) (engine.Value, error) {
		n := len(data)
		start := 0
		if len(a) > 0 {
			start = argInt(a, 0, 0)
		}
		end := n
		if len(a) > 1 {
			end = argInt(a, 1, n)
		}
		if start < 0 {
			start = n + start
		}
		if end < 0 {
			end = n + end
		}
		start = clampIdx(start, 0, n)
		end = clampIdx(end, start, n)
		sliceType := blobType
		if len(a) > 2 {
			sliceType = a[2].String()
		}
		sub := data[start:end]
		// 构造新 Blob（用原始字节直接包装）。
		nb := newBlobInstance(ctx, []engine.Value{NewBufferInstance(sub)}, sliceType)
		return nb, nil
	}))

	return blob
}

// blobPartBytes 把 Blob 构造参数的一个 part 转为字节。
func blobPartBytes(v engine.Value) []byte {
	switch {
	case v.Type() == engine.TypeString:
		return []byte(v.String())
	case v.IsObject():
		if b, ok := engine.AsBuffer(v); ok {
			return b
		}
		// 嵌套 Blob：读取其 _data 内部属性。
		if o, ok := v.AsObject(); ok {
			if d, err := o.Get("_data"); err == nil {
				if b, ok := engine.AsBuffer(d); ok {
					return b
				}
			}
		}
	}
	return nil
}
