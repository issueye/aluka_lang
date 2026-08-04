package globals

// Web 编码 API：TextEncoder / TextDecoder / atob / btoa。
//
// 实现要点：
//   - TextEncoder.encode 返回 Buffer（作为 Uint8Array 视图，复用 Buffer 实现）。
//   - TextDecoder 支持 utf-8（默认，含 BOM 跳过与非法序列 U+FFFD 替换）、
//     utf-16le、latin1。
//   - atob/btoa 基于 Go base64（btoa 对 0-255 之外字符抛错误，Node 语义）。
//   - 实例方法均以闭包捕获状态（绕过 engine.Func 无 this 绑定限制）。

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// EncodingConfig 配置编码全局（当前无可用选项）。
type EncodingConfig struct{}

// NewEncoding 注册 TextEncoder / TextDecoder / atob / btoa 到全局。
func NewEncoding(ctx engine.Context, cfg EncodingConfig) error {
	te := engine.NewFunction("TextEncoder", func(args []engine.Value) (engine.Value, error) {
		return newTextEncoder(), nil
	})
	td := engine.NewFunction("TextDecoder", func(args []engine.Value) (engine.Value, error) {
		return newTextDecoder(args), nil
	})
	if err := ctx.Global().Set("TextEncoder", te); err != nil {
		return err
	}
	if err := ctx.Global().Set("TextDecoder", td); err != nil {
		return err
	}
	if err := ctx.Global().Set("atob", engine.NewFunction("atob", atobFn)); err != nil {
		return err
	}
	return ctx.Global().Set("btoa", engine.NewFunction("btoa", btoaFn))
}

// newTextEncoder 构造 TextEncoder 实例。
func newTextEncoder() engine.Value {
	enc := engine.NewObject()
	_ = enc.Set("encoding", engine.Str("utf-8"))

	// encode(input)：字符串 → utf8 字节的 Uint8Array（Buffer）。
	_ = enc.Set("encode", engine.NewFunction("encode", func(args []engine.Value) (engine.Value, error) {
		s := ""
		if len(args) > 0 {
			s = args[0].String()
		}
		return newBufferInstance([]byte(s)), nil
	}))

	// encodeInto(source, destination)：写入目标 Uint8Array，返回 {read, written}。
	_ = enc.Set("encodeInto", engine.NewFunction("encodeInto", func(args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		_ = result.Set("read", engine.IntValue(0))
		_ = result.Set("written", engine.IntValue(0))
		if len(args) < 2 {
			return result, nil
		}
		src := args[0].String()
		dst, ok := engine.AsBuffer(args[1])
		if !ok {
			return result, nil
		}
		written := copy(dst, src)
		_ = result.Set("read", engine.IntValue(written))
		_ = result.Set("written", engine.IntValue(written))
		return result, nil
	}))

	return enc
}

// newTextDecoder 构造 TextDecoder 实例。编码参数支持 utf-8/utf-16le/latin1。
func newTextDecoder(args []engine.Value) engine.Value {
	enc := "utf-8"
	if len(args) > 0 {
		label := strings.ToLower(args[0].String())
		switch label {
		case "utf-16le", "utf-16", "ucs-2", "ucs2":
			enc = "utf-16le"
		case "iso-8859-1", "latin1", "binary", "windows-1252":
			enc = "latin1"
		default: // utf-8 及其别名
			enc = "utf-8"
		}
	}
	dec := engine.NewObject()
	_ = dec.Set("encoding", engine.Str(enc))
	_ = dec.Set("fatal", engine.Boolean(false))
	_ = dec.Set("ignoreBOM", engine.Boolean(false))

	// decode([input])：Buffer/Uint8Array → 字符串。
	_ = dec.Set("decode", engine.NewFunction("decode", func(dargs []engine.Value) (engine.Value, error) {
		if len(dargs) == 0 {
			return engine.Str(""), nil
		}
		input := dargs[0]
		// 支持 Buffer、字节数组、undefined。
		if data, ok := engine.AsBuffer(input); ok {
			return engine.Str(decodeBuffer(data, enc)), nil
		}
		if a, ok := input.(*engine.ArrayValue); ok {
			elems := a.Elems()
			data := make([]byte, len(elems))
			for i, e := range elems {
				if n, ok := e.Int(); ok {
					data[i] = byte(n)
				}
			}
			return engine.Str(decodeBuffer(data, enc)), nil
		}
		return engine.Str(""), nil
	}))

	return dec
}

// atob(encodedData)：base64 解码为二进制字符串（每个字符 0-255）。
var atobFn = func(args []engine.Value) (engine.Value, error) {
	if len(args) == 0 {
		return engine.Undefined(), fmt.Errorf("%w: atob requires a string", engine.ErrTypeError)
	}
	b, err := decodeBase64(args[0].String())
	if err != nil {
		return engine.Undefined(), fmt.Errorf("%w: invalid base64 data", engine.ErrSyntaxError)
	}
	return engine.Str(string(b)), nil
}

// btoa(binaryString)：二进制字符串（每个字符 0-255）→ base64 编码。
var btoaFn = func(args []engine.Value) (engine.Value, error) {
	if len(args) == 0 {
		return engine.Undefined(), fmt.Errorf("%w: btoa requires a string", engine.ErrTypeError)
	}
	// Node 语义：字符必须可编码为单字节（latin1 范围 0-255）。
	s := args[0].String()
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return engine.Undefined(), fmt.Errorf("%w: character outside latin1 range", engine.ErrTypeError)
		}
		out = append(out, byte(r))
	}
	return engine.Str(base64.StdEncoding.EncodeToString(out)), nil
}
