package globals

// Aluka.deflate/inflate/gzip/gunzip（Phase 4 WBS 4.12）。
//
//   - deflateSync/inflateSync：zlib 格式（Go compress/zlib）
//   - gzipSync/gunzipSync：gzip 格式（Go compress/gzip）
//   - deflate/inflate/gzip/gunzip：异步版本，返回 Promise<Buffer>

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"

	"github.com/aluka-lang/aluka/internal/engine"
)

// alukaRegisterCompress 注册压缩 API。
func alukaRegisterCompress(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()

	_ = ao.Set("deflateSync", engine.NewFunction("deflateSync", func(args []engine.Value) (engine.Value, error) {
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(argBytes(args, 0)); err != nil {
			return engine.Undefined(), err
		}
		if err := w.Close(); err != nil {
			return engine.Undefined(), err
		}
		return NewBufferInstance(buf.Bytes()), nil
	}))
	_ = ao.Set("inflateSync", engine.NewFunction("inflateSync", func(args []engine.Value) (engine.Value, error) {
		data := argBytes(args, 0)
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return engine.Undefined(), err
		}
		defer r.Close()
		out, err := io.ReadAll(r)
		if err != nil {
			return engine.Undefined(), err
		}
		return NewBufferInstance(out), nil
	}))
	_ = ao.Set("gzipSync", engine.NewFunction("gzipSync", func(args []engine.Value) (engine.Value, error) {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(argBytes(args, 0)); err != nil {
			return engine.Undefined(), err
		}
		if err := w.Close(); err != nil {
			return engine.Undefined(), err
		}
		return NewBufferInstance(buf.Bytes()), nil
	}))
	_ = ao.Set("gunzipSync", engine.NewFunction("gunzipSync", func(args []engine.Value) (engine.Value, error) {
		data := argBytes(args, 0)
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return engine.Undefined(), err
		}
		defer r.Close()
		out, err := io.ReadAll(r)
		if err != nil {
			return engine.Undefined(), err
		}
		return NewBufferInstance(out), nil
	}))

	// 异步版本：deflate/inflate/gzip/gunzip → Promise<Buffer>。
	_ = ao.Set("deflate", compressAsync(ctx, func(data []byte) ([]byte, error) {
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}))
	_ = ao.Set("inflate", compressAsync(ctx, func(data []byte) ([]byte, error) {
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}))
	_ = ao.Set("gzip", compressAsync(ctx, func(data []byte) ([]byte, error) {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}))
	_ = ao.Set("gunzip", compressAsync(ctx, func(data []byte) ([]byte, error) {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}))
}

// compressAsync 构造压缩异步函数（goroutine 内执行，PostTask 回 JS 线程）。
func compressAsync(ctx engine.Context, fn func([]byte) ([]byte, error)) engine.Value {
	return engine.NewFunction("compress", func(args []engine.Value) (engine.Value, error) {
		data := argBytes(args, 0)
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) < 2 {
				return engine.Undefined(), nil
			}
			resolve, reject := ea[0], ea[1]
			release := ctx.AddRef()
			go func() {
				out, err := fn(data)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						callResolve(reject, engine.Str(err.Error()))
						return
					}
					callResolve(resolve, NewBufferInstance(out))
				})
			}()
			return engine.Undefined(), nil
		})
		return newPromise(ctx, executor)
	})
}
