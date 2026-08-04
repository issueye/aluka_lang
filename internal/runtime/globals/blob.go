package globals

// Web API：Blob / File（开发计划 3.4）。
//
// Blob 持有一段字节（由 parts 拼接），提供 size/type/text()/arrayBuffer()/
// slice()。File 继承 Blob 并附加 name/lastModified。
// text()/arrayBuffer() 返回 Promise（WHATWG 语义），用全局 Promise 包装。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// BlobConfig 配置 Blob 全局（当前无可用选项）。
type BlobConfig struct{}

// NewBlob 注册全局 Blob / File。
func NewBlob(ctx engine.Context, cfg BlobConfig) error {
	_ = ctx.Global().Set("Blob", engine.NewFunction("Blob", func(args []engine.Value) (engine.Value, error) {
		return newBlobInstance(ctx, args, ""), nil
	}))
	_ = ctx.Global().Set("File", engine.NewFunction("File", func(args []engine.Value) (engine.Value, error) {
		name := ""
		parts := args
		if len(args) > 1 {
			name = args[1].String()
			parts = args[:1]
		}
		blob := newBlobInstance(ctx, parts, "").(engine.Object)
		_ = blob.Set("name", engine.Str(name))
		_ = blob.Set("lastModified", engine.IntValue(0))
		return blob, nil
	}))
	return nil
}

// newBlobInstance 构造 Blob（parts 支持字符串/Buffer/Blob）。
func newBlobInstance(ctx engine.Context, args []engine.Value, typeHint string) engine.Value {
	blob := engine.NewObject()

	// 收集字节。
	var data []byte
	if len(args) > 0 {
		if a, ok := args[0].(*engine.ArrayValue); ok {
			for _, part := range a.Elems() {
				data = append(data, blobPartBytes(part)...)
			}
		} else {
			data = append(data, blobPartBytes(args[0])...)
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
	// slice([start[, end[, type]]]) → 新 Blob
	_ = blob.Set("slice", engine.NewFunction("slice", func(a []engine.Value) (engine.Value, error) {
		start := argInt(a, 0, 0)
		end := len(data)
		if len(a) > 1 {
			end = argInt(a, 1, len(data))
		}
		start = clampIdx(start, 0, len(data))
		end = clampIdx(end, start, len(data))
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
