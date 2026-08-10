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
	"compress/flate"
	"compress/gzip"
	gzlib "compress/zlib"
	"fmt"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"hash/crc32"
	"io"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// NewZlib 构造 node:zlib 模块的导出对象。
func NewZlib(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// 同步 API。
	_ = m.Set("gzipSync", engine.NewFunction("gzipSync", makeZlibSync(gzipBytes)))
	_ = m.Set("gunzipSync", engine.NewFunction("gunzipSync", makeZlibSync(gunzipBytes)))
	_ = m.Set("deflateSync", engine.NewFunction("deflateSync", makeZlibSync(deflateBytes)))
	_ = m.Set("inflateSync", engine.NewFunction("inflateSync", makeZlibSync(inflateBytes)))
	_ = m.Set("deflateRawSync", engine.NewFunction("deflateRawSync", makeZlibSync(deflateRawBytes)))
	_ = m.Set("inflateRawSync", engine.NewFunction("inflateRawSync", makeZlibSync(inflateRawBytes)))
	_ = m.Set("unzipSync", engine.NewFunction("unzipSync", makeZlibSync(unzipBytes)))
	_ = m.Set("brotliCompressSync", engine.NewFunction("brotliCompressSync", makeZlibSync(brotliCompressBytes)))
	_ = m.Set("brotliDecompressSync", engine.NewFunction("brotliDecompressSync", makeZlibSync(brotliDecompressBytes)))
	_ = m.Set("zstdCompressSync", engine.NewFunction("zstdCompressSync", makeZlibSync(zstdCompressBytes)))
	_ = m.Set("zstdDecompressSync", engine.NewFunction("zstdDecompressSync", makeZlibSync(zstdDecompressBytes)))

	// crc32(data[, value])：CRC-32 校验和（IEEE 多项式，与 zlib 一致）。
	_ = m.Set("crc32", engine.NewFunction("crc32", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("zlib: crc32 requires data")
		}
		data, err := zlibBufferArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		value := uint32(0)
		if len(args) > 1 && !args[1].IsUndefined() {
			if n, ok := args[1].Int(); ok {
				value = uint32(n)
			}
		}
		// Node 返回无符号 32 位整数（> 2^31 时为正数）。
		return engine.Number(float64(crc32.Update(value, crc32.IEEETable, data))), nil
	}))

	// 异步回调版：gzip(buf, cb) / gunzip / deflate / inflate / brotli* / zstd*。
	_ = m.Set("gzip", engine.NewFunction("gzip", makeZlibAsync(ctx, gzipBytes)))
	_ = m.Set("gunzip", engine.NewFunction("gunzip", makeZlibAsync(ctx, gunzipBytes)))
	_ = m.Set("deflate", engine.NewFunction("deflate", makeZlibAsync(ctx, deflateBytes)))
	_ = m.Set("inflate", engine.NewFunction("inflate", makeZlibAsync(ctx, inflateBytes)))
	_ = m.Set("deflateRaw", engine.NewFunction("deflateRaw", makeZlibAsync(ctx, deflateRawBytes)))
	_ = m.Set("inflateRaw", engine.NewFunction("inflateRaw", makeZlibAsync(ctx, inflateRawBytes)))
	_ = m.Set("unzip", engine.NewFunction("unzip", makeZlibAsync(ctx, unzipBytes)))
	_ = m.Set("brotliCompress", engine.NewFunction("brotliCompress", makeZlibAsync(ctx, brotliCompressBytes)))
	_ = m.Set("brotliDecompress", engine.NewFunction("brotliDecompress", makeZlibAsync(ctx, brotliDecompressBytes)))
	_ = m.Set("zstdCompress", engine.NewFunction("zstdCompress", makeZlibAsync(ctx, zstdCompressBytes)))
	_ = m.Set("zstdDecompress", engine.NewFunction("zstdDecompress", makeZlibAsync(ctx, zstdDecompressBytes)))

	// constants：zlib/Brotli 参数枚举（与 Node 常量值一致）。
	_ = m.Set("constants", zlibConstantsObject())
	return m, nil
}

// zlibConstantsObject 构造 zlib.constants 对象（值取自 zlib/zlib.h 与 brotli 枚举）。
func zlibConstantsObject() engine.Value {
	c := engine.NewObject()
	iv := func(name string, v int) {
		_ = c.Set(name, engine.IntValue(v))
	}
	// flush 模式。
	iv("Z_NO_FLUSH", 0)
	iv("Z_PARTIAL_FLUSH", 1)
	iv("Z_SYNC_FLUSH", 2)
	iv("Z_FULL_FLUSH", 3)
	iv("Z_FINISH", 4)
	iv("Z_BLOCK", 5)
	// 返回码。
	iv("Z_OK", 0)
	iv("Z_STREAM_END", 1)
	iv("Z_NEED_DICT", 2)
	iv("Z_ERRNO", -1)
	iv("Z_STREAM_ERROR", -2)
	iv("Z_DATA_ERROR", -3)
	iv("Z_MEM_ERROR", -4)
	iv("Z_BUF_ERROR", -5)
	iv("Z_VERSION_ERROR", -6)
	// 压缩级别。
	iv("Z_NO_COMPRESSION", 0)
	iv("Z_BEST_SPEED", 1)
	iv("Z_BEST_COMPRESSION", 9)
	iv("Z_DEFAULT_COMPRESSION", -1)
	// 压缩策略。
	iv("Z_FILTERED", 1)
	iv("Z_HUFFMAN_ONLY", 2)
	iv("Z_RLE", 3)
	iv("Z_FIXED", 4)
	iv("Z_DEFAULT_STRATEGY", 0)
	// Brotli 模式/操作/参数。
	iv("BROTLI_MODE_GENERIC", 0)
	iv("BROTLI_MODE_TEXT", 1)
	iv("BROTLI_MODE_FONT", 2)
	iv("BROTLI_OPERATION_PROCESS", 0)
	iv("BROTLI_OPERATION_FLUSH", 1)
	iv("BROTLI_OPERATION_FINISH", 2)
	iv("BROTLI_OPERATION_EMIT_METADATA", 3)
	iv("BROTLI_PARAM_MODE", 0)
	iv("BROTLI_PARAM_QUALITY", 1)
	iv("BROTLI_PARAM_LGWIN", 2)
	iv("BROTLI_PARAM_LGBLOCK", 3)
	iv("BROTLI_PARAM_DISABLE_LITERAL_CONTEXT_MODELING", 4)
	iv("BROTLI_PARAM_SIZE_HINT", 5)
	iv("BROTLI_PARAM_LARGE_WINDOW", 6)
	iv("BROTLI_PARAM_NPOSTFIX", 7)
	iv("BROTLI_PARAM_NDIRECT", 8)
	iv("BROTLI_MIN_QUALITY", 0)
	iv("BROTLI_MAX_QUALITY", 11)
	iv("BROTLI_DEFAULT_QUALITY", 11)
	iv("BROTLI_MIN_WINDOW_BITS", 10)
	iv("BROTLI_MAX_WINDOW_BITS", 24)
	iv("BROTLI_DEFAULT_WINDOW", 22)
	iv("BROTLI_MIN_INPUT_BLOCK_BITS", 16)
	iv("BROTLI_MAX_INPUT_BLOCK_BITS", 24)
	iv("BROTLI_DEFAULT_LG_BLOCK", 22)
	iv("BROTLI_DECODER_RESULT_ERROR", 0)
	iv("BROTLI_DECODER_RESULT_SUCCESS", 1)
	iv("BROTLI_DECODER_RESULT_NEEDS_MORE_INPUT", 2)
	iv("BROTLI_DECODER_RESULT_NEEDS_MORE_OUTPUT", 3)
	// zstd 参数枚举（libzstd ZSTD_cParameter 值，与 Node 一致）。
	iv("ZSTD_c_compressionLevel", 100)
	iv("ZSTD_c_strategy", 107)
	return c
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
							if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						} else {
							if _, err := f.Call([]engine.Value{engine.Null(), globals.NewBufferInstance(out)}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
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

func deflateRawBytes(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func inflateRawBytes(data []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	return io.ReadAll(r)
}

// unzipBytes 自动识别 gzip（1f 8b）与 zlib（deflate）流。
func unzipBytes(data []byte) ([]byte, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return gunzipBytes(data)
	}
	return inflateBytes(data)
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

// zstdEncoder/zstdDecoder 惰性初始化（复用实例，zstd 允许并发用同一
// Encoder/Decoder，但 EncodeAll 需持有锁）。
var (
	zstdOnce  sync.Once
	zstdEnc   *zstd.Encoder
	zstdDec   *zstd.Decoder
	zstdEncMu sync.Mutex
)

func zstdCompressBytes(data []byte) ([]byte, error) {
	zstdOnce.Do(func() {
		zstdEnc, _ = zstd.NewWriter(nil)
		zstdDec, _ = zstd.NewReader(nil)
	})
	if zstdEnc == nil {
		return nil, fmt.Errorf("zstd: encoder init failed")
	}
	zstdEncMu.Lock()
	defer zstdEncMu.Unlock()
	return zstdEnc.EncodeAll(data, nil), nil
}

func zstdDecompressBytes(data []byte) ([]byte, error) {
	zstdOnce.Do(func() {
		zstdEnc, _ = zstd.NewWriter(nil)
		zstdDec, _ = zstd.NewReader(nil)
	})
	if zstdDec == nil {
		return nil, fmt.Errorf("zstd: decoder init failed")
	}
	return zstdDec.DecodeAll(data, nil)
}
