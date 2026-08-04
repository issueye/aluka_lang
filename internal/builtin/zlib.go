package builtin

// node:zlib 内置模块——压缩/解压（gzip/deflate/brotli）。
//
// 实现要点：
//   - 同步 API（*Sync）在 JS 线程直接执行压缩，返回 Buffer。
//   - 异步 API 在 goroutine 执行压缩，完成后经 ctx.PostTask 回 JS 线程
//     回调 (err, result)。
//   - gzip/deflate 用 Go compress/*；brotli 用 github.com/andybalholm/brotli
//     （Go 标准库 compress/brotli 在部分工具链缺失）。

import (
	"bytes"
	"compress/gzip"
	gzlib "compress/zlib"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewZlib 构造 node:zlib 模块的导出对象。
func NewZlib(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// 同步 API。
	_ = m.Set("gzipSync", engine.NewFunction("gzipSync", makeZlibSync(gzipBytes)))
	_ = m.Set("gunzipSync", engine.NewFunction("gunzipSync", makeZlibSync(gunzipBytes)))
	_ = m.Set("deflateSync", engine.NewFunction("deflateSync", makeZlibSync(deflateBytes)))
	_ = m.Set("inflateSync", engine.NewFunction("inflateSync", makeZlibSync(inflateBytes)))
	_ = m.Set("brotliCompressSync", engine.NewFunction("brotliCompressSync", makeZlibSync(brotliCompressBytes)))
	_ = m.Set("brotliDecompressSync", engine.NewFunction("brotliDecompressSync", makeZlibSync(brotliDecompressBytes)))

	// 异步回调版：gzip(buf, cb) / gunzip / deflate / inflate / brotli*。
	_ = m.Set("gzip", engine.NewFunction("gzip", makeZlibAsync(ctx, gzipBytes)))
	_ = m.Set("gunzip", engine.NewFunction("gunzip", makeZlibAsync(ctx, gunzipBytes)))
	_ = m.Set("deflate", engine.NewFunction("deflate", makeZlibAsync(ctx, deflateBytes)))
	_ = m.Set("inflate", engine.NewFunction("inflate", makeZlibAsync(ctx, inflateBytes)))
	_ = m.Set("brotliCompress", engine.NewFunction("brotliCompress", makeZlibAsync(ctx, brotliCompressBytes)))
	_ = m.Set("brotliDecompress", engine.NewFunction("brotliDecompress", makeZlibAsync(ctx, brotliDecompressBytes)))

	_ = m.Set("constants", engine.NewObject())
	return m, nil
}

// makeZlibSync 包装纯压缩函数为同步 engine.Func。
func makeZlibSync(fn func([]byte) ([]byte, error)) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		data, err := zlibBufferArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		out, err := fn(data)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("zlib: %w", err)
		}
		return globals.NewBufferInstance(out), nil
	}
}

// makeZlibAsync 包装纯压缩函数为异步回调版（在 goroutine 执行）。
// 用 AddRef 保持事件循环存活，直到回调投递并执行。
func makeZlibAsync(ctx engine.Context, fn func([]byte) ([]byte, error)) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("zlib: missing buffer argument")
		}
		data, err := zlibBufferArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		var cb engine.Value
		if len(args) > 1 && args[1].IsFunction() {
			cb = args[1]
		} else if len(args) > 1 && args[1].IsObject() {
			// 支持 (buf, options, cb)
			if len(args) > 2 && args[2].IsFunction() {
				cb = args[2]
			}
		}
		release := ctx.AddRef()
		go func() {
			out, err := fn(data)
			ctx.PostTask(func() {
				defer release()
				if cb != nil && cb.IsFunction() {
					if f, ok := cb.AsFunction(); ok {
						if err != nil {
							_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
						} else {
							_, _ = f.Call([]engine.Value{engine.Null(), globals.NewBufferInstance(out)})
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}
}

// zlibBufferArg 取第 i 个参数为字节（Buffer 或字符串）。
func zlibBufferArg(args []engine.Value, i int) ([]byte, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("zlib: missing buffer argument")
	}
	if b, ok := engine.AsBuffer(args[i]); ok {
		return b, nil
	}
	return []byte(args[i].String()), nil
}

// --- 压缩实现 --------------------------------------------------------------

func gzipBytes(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func gunzipBytes(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func deflateBytes(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w := gzlib.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func inflateBytes(data []byte) ([]byte, error) {
	r, err := gzlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func brotliCompressBytes(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w := brotli.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func brotliDecompressBytes(data []byte) ([]byte, error) {
	return io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
}
