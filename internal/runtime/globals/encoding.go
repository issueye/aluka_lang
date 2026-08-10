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
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// EncodingConfig 配置编码全局（当前无可用选项）。
type EncodingConfig struct{}

// textEncoderProto / textDecoderProto 是实例原型（instanceof 支持）。
var (
	textEncoderProto engine.Object
	textDecoderProto engine.Object
)

// NewEncoding 注册 TextEncoder / TextDecoder / atob / btoa 到全局。
func NewEncoding(ctx engine.Context, cfg EncodingConfig) error {
	te := engine.NewFunction("TextEncoder", func(args []engine.Value) (engine.Value, error) {
		return newTextEncoder(), nil
	})
	teObj, _ := te.AsObject()
	textEncoderProto = engine.NewObject()
	_ = textEncoderProto.Set("constructor", te)
	_ = teObj.Set("prototype", textEncoderProto)

	td := engine.NewFunction("TextDecoder", func(args []engine.Value) (engine.Value, error) {
		return newTextDecoder(args), nil
	})
	tdObj, _ := td.AsObject()
	textDecoderProto = engine.NewObject()
	_ = textDecoderProto.Set("constructor", td)
	_ = tdObj.Set("prototype", textDecoderProto)

	if err := ctx.Global().Set("TextEncoder", te); err != nil {
		return err
	}
	if err := ctx.Global().Set("TextDecoder", td); err != nil {
		return err
	}
	atob := func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: atob requires a string", engine.ErrTypeError)
		}
		b, err := decodeBase64(args[0].String())
		if err != nil {
			return engine.Undefined(), throwDOMException(ctx, "InvalidCharacterError", "Invalid character")
		}
		return engine.Str(string(b)), nil
	}
	if err := ctx.Global().Set("atob", engine.NewFunction("atob", atob)); err != nil {
		return err
	}
	btoa := func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: btoa requires a string", engine.ErrTypeError)
		}
		// Node 语义：字符必须可编码为单字节（latin1 范围 0-255）。
		s := args[0].String()
		out := make([]byte, 0, len(s))
		for _, r := range s {
			if r > 0xFF {
				return engine.Undefined(), throwDOMException(ctx, "InvalidCharacterError", "The string contains characters outside of the Latin1 range")
			}
			out = append(out, byte(r))
		}
		return engine.Str(base64.StdEncoding.EncodeToString(out)), nil
	}
	if err := ctx.Global().Set("btoa", engine.NewFunction("btoa", btoa)); err != nil {
		return err
	}
	if err := ctx.Global().Set("TextEncoderStream", engine.NewFunction("TextEncoderStream", func(args []engine.Value) (engine.Value, error) {
		return newTextEncoderStream(ctx), nil
	})); err != nil {
		return err
	}
	return ctx.Global().Set("TextDecoderStream", engine.NewFunction("TextDecoderStream", func(args []engine.Value) (engine.Value, error) {
		return newTextDecoderStream(ctx, args), nil
	}))
}

// newTextEncoderStream 构造 TextEncoderStream：writable 收字符串，
// readable 出 Uint8Array（TransformStream 外形）。
func newTextEncoderStream(ctx engine.Context) engine.Value {
	obj := engine.NewObject()
	transform := engine.NewFunction("transform", func(a []engine.Value) (engine.Value, error) {
		s := ""
		if len(a) > 0 {
			s = a[0].String()
		}
		if len(a) > 1 {
			if c, ok := a[1].AsObject(); ok {
				if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
					if f, ok := e.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{newBufferInstance([]byte(s))}); err != nil {
							interpreter.ReportUncaught(ctx, err)
						}
					}
				}
			}
		}
		return engine.Undefined(), nil
	})
	ts := newTransformStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{"transform": transform})})
	if tsObj, ok := ts.AsObject(); ok {
		if r, err := tsObj.Get("readable"); err == nil {
			_ = obj.Set("readable", r)
		}
		if w, err := tsObj.Get("writable"); err == nil {
			_ = obj.Set("writable", w)
		}
	}
	_ = obj.Set("encoding", engine.Str("utf-8"))
	return obj
}

// newTextDecoderStream 构造 TextDecoderStream：writable 收 Uint8Array，
// readable 出字符串（TransformStream 外形）。
func newTextDecoderStream(ctx engine.Context, args []engine.Value) engine.Value {
	enc := "utf-8"
	if len(args) > 0 {
		label := strings.ToLower(args[0].String())
		switch label {
		case "utf-16le", "utf-16", "ucs-2", "ucs2":
			enc = "utf-16le"
		case "iso-8859-1", "latin1", "binary", "windows-1252":
			enc = "latin1"
		}
	}
	obj := engine.NewObject()
	transform := engine.NewFunction("transform", func(a []engine.Value) (engine.Value, error) {
		var decoded string
		if len(a) > 0 {
			if data, ok := bytesOf(a[0]); ok {
				decoded = decodeBuffer(stripBOM(data, enc), enc)
			}
		}
		if len(a) > 1 {
			if c, ok := a[1].AsObject(); ok {
				if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
					if f, ok := e.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{engine.Str(decoded)}); err != nil {
							interpreter.ReportUncaught(ctx, err)
						}
					}
				}
			}
		}
		return engine.Undefined(), nil
	})
	ts := newTransformStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{"transform": transform})})
	if tsObj, ok := ts.AsObject(); ok {
		if r, err := tsObj.Get("readable"); err == nil {
			_ = obj.Set("readable", r)
		}
		if w, err := tsObj.Get("writable"); err == nil {
			_ = obj.Set("writable", w)
		}
	}
	_ = obj.Set("encoding", engine.Str(enc))
	_ = obj.Set("fatal", engine.Boolean(false))
	_ = obj.Set("ignoreBOM", engine.Boolean(false))
	return obj
}

// newTextEncoder 构造 TextEncoder 实例。
func newTextEncoder() engine.Value {
	enc := engine.NewObject()
	if textEncoderProto != nil {
		engine.SetProto(enc, textEncoderProto)
	}
	_ = enc.Set("encoding", engine.Str("utf-8"))

	// encode(input)：字符串 → utf8 字节的 Uint8Array（Buffer）。
	_ = enc.Set("encode", engine.NewFunction("encode", func(args []engine.Value) (engine.Value, error) {
		s := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			s = args[0].String()
		}
		return newBufferInstance([]byte(s)), nil
	}))

	// encodeInto(source, destination)：写入目标 Uint8Array，返回 {read, written}。
	// read = 消耗的 UTF-16 码元数；written = 写入的字节数（空间不足时截断）。
	_ = enc.Set("encodeInto", engine.NewFunction("encodeInto", func(args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		_ = result.Set("read", engine.IntValue(0))
		_ = result.Set("written", engine.IntValue(0))
		if len(args) < 2 {
			return result, nil
		}
		src := args[0].String()
		dst, ok := bytesOf(args[1])
		if !ok {
			return result, nil
		}
		read, written := encodeIntoUTF8(src, dst)
		_ = result.Set("read", engine.IntValue(read))
		_ = result.Set("written", engine.IntValue(written))
		return result, nil
	}))

	return enc
}

// encodeIntoUTF8 把字符串的 UTF-8 字节写入 dst，返回（消耗码元数, 写入字节数）。
func encodeIntoUTF8(s string, dst []byte) (int, int) {
	written := 0
	read := 0
	for i := 0; i < len(s); {
		r, size := utf8DecodeRuneInString(s[i:])
		if size <= 0 {
			size = 1
			r = '\uFFFD'
		}
		var buf [4]byte
		n := utf8EncodeRune(buf[:], r)
		if written+n > len(dst) {
			break
		}
		copy(dst[written:], buf[:n])
		written += n
		if r > 0xFFFF {
			read += 2 // 代理对
		} else {
			read++
		}
		i += size
	}
	return read, written
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
	fatal := false
	ignoreBOM := false
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("fatal"); err == nil {
				if b, ok := v.Bool(); ok {
					fatal = b
				}
			}
			if v, err := o.Get("ignoreBOM"); err == nil {
				if b, ok := v.Bool(); ok {
					ignoreBOM = b
				}
			}
		}
	}
	dec := engine.NewObject()
	if textDecoderProto != nil {
		engine.SetProto(dec, textDecoderProto)
	}
	_ = dec.Set("encoding", engine.Str(enc))
	_ = dec.Set("fatal", engine.Boolean(fatal))
	_ = dec.Set("ignoreBOM", engine.Boolean(ignoreBOM))

	// decode([input])：Buffer/Uint8Array/ArrayBuffer → 字符串。
	_ = dec.Set("decode", engine.NewFunction("decode", func(dargs []engine.Value) (engine.Value, error) {
		if len(dargs) == 0 || dargs[0].IsUndefined() {
			return engine.Str(""), nil
		}
		data, ok := bytesOf(dargs[0])
		if !ok {
			// 数组兜底。
			if a, ok := dargs[0].(*engine.ArrayValue); ok {
				elems := a.Elems()
				data = make([]byte, len(elems))
				for i, e := range elems {
					if n, ok := e.Int(); ok {
						data[i] = byte(n)
					}
				}
			} else {
				return engine.Str(""), nil
			}
		}
		// BOM 跳过（UTF-8 / UTF-16LE）。
		if !ignoreBOM {
			data = stripBOM(data, enc)
		}
		out := decodeBuffer(data, enc)
		if fatal && strings.ContainsRune(out, '\uFFFD') && !utf8ValidInput(out) {
			// fatal 模式：非法序列应抛 TypeError；简化以 U+FFFD 判定近似。
			return engine.Undefined(), fmt.Errorf("%w: encoded data is not valid", engine.ErrTypeError)
		}
		return engine.Str(out), nil
	}))

	return dec
}

// bytesOf 从 Buffer / TypedArray / ArrayBuffer 提取底层字节。
func bytesOf(v engine.Value) ([]byte, bool) {
	if b, ok := engine.AsBuffer(v); ok {
		return b, true
	}
	if b, ok := engine.AsArrayBuffer(v); ok {
		return b, true
	}
	if t, ok := engine.AsTypedArray(v); ok {
		return t.Bytes(), true
	}
	return nil, false
}

// stripBOM 按编码跳过前导 BOM。
func stripBOM(data []byte, enc string) []byte {
	if len(data) == 0 {
		return data
	}
	switch enc {
	case "utf-16le":
		if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
			return data[2:]
		}
	default: // utf-8
		if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
			return data[3:]
		}
	}
	return data
}

// atob(encodedData)：base64 解码为二进制字符串（每个字符 0-255）。
// 已在 NewEncoding 内注册（带 ctx 的闭包，抛 DOMException InvalidCharacterError）。

// btoa(binaryString)：二进制字符串（每个字符 0-255）→ base64 编码。
// 已在 NewEncoding 内注册（带 ctx 的闭包，抛 DOMException InvalidCharacterError）。

// utf8DecodeRuneInString 解码字符串中的一个码点。
func utf8DecodeRuneInString(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	c := s[0]
	if c < 0x80 {
		return rune(c), 1
	}
	var r rune
	n := 0
	if c >= 0xC2 && c <= 0xDF && len(s) >= 2 {
		r, n = rune(c&0x1F)<<6|rune(s[1]&0x3F), 2
	} else if c >= 0xE0 && c <= 0xEF && len(s) >= 3 {
		r, n = rune(c&0x0F)<<12|rune(s[1]&0x3F)<<6|rune(s[2]&0x3F), 3
	} else if c >= 0xF0 && c <= 0xF4 && len(s) >= 4 {
		r, n = rune(c&0x07)<<18|rune(s[1]&0x3F)<<12|rune(s[2]&0x3F)<<6|rune(s[3]&0x3F), 4
	} else {
		return 0, 0 // 非法首字节
	}
	return r, n
}

// utf8EncodeRune 编码码点为 UTF-8 字节。
func utf8EncodeRune(dst []byte, r rune) int {
	switch {
	case r < 0x80:
		dst[0] = byte(r)
		return 1
	case r < 0x800:
		dst[0] = 0xC0 | byte(r>>6)
		dst[1] = 0x80 | byte(r&0x3F)
		return 2
	case r < 0x10000:
		dst[0] = 0xE0 | byte(r>>12)
		dst[1] = 0x80 | byte((r>>6)&0x3F)
		dst[2] = 0x80 | byte(r&0x3F)
		return 3
	default:
		dst[0] = 0xF0 | byte(r>>18)
		dst[1] = 0x80 | byte((r>>12)&0x3F)
		dst[2] = 0x80 | byte((r>>6)&0x3F)
		dst[3] = 0x80 | byte(r&0x3F)
		return 4
	}
}

// utf8ValidInput 宽松判断字符串是否含 U+FFFD 替换符。
func utf8ValidInput(s string) bool { return !strings.ContainsRune(s, '\uFFFD') }
