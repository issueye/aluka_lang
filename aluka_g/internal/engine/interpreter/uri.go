package interpreter

import (
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupURI 注册全局 URI 编码函数（开发优化方案 P0-3）：
// encodeURI / decodeURI / encodeURIComponent / decodeURIComponent。
//
//   - encodeURI 保留 URI 结构字符：A-Z a-z 0-9 ; , / ? : @ & = + $ # 及 - _ . ! ~ * ' ( )
//   - encodeURIComponent 额外编码结构字符，仅保留 unreserved：A-Z a-z 0-9 - _ . ! ~ * ' ( )
//   - 非 ASCII 按 UTF-8 字节逐一 %XX 编码
func (interp *Interpreter) setupURI() {
	_ = interp.globalObj.Set("encodeURI", interp.makeFunc("encodeURI", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(uriEncode(args[0].String(), false)), nil
	}))
	_ = interp.globalObj.Set("encodeURIComponent", interp.makeFunc("encodeURIComponent", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(uriEncode(args[0].String(), true)), nil
	}))
	_ = interp.globalObj.Set("decodeURI", interp.makeFunc("decodeURI", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(uriDecode(args[0].String(), false)), nil
	}))
	_ = interp.globalObj.Set("decodeURIComponent", interp.makeFunc("decodeURIComponent", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(uriDecode(args[0].String(), true)), nil
	}))
}

// uriUnreserved 是不需要转义的字符集（两模式共用的安全集合）。
const uriUnreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"

// uriReserved 是 encodeURI 额外保留（不转义）的结构字符。
const uriReserved = ";/?:@&=+$,#"

const hexDigits = "0123456789ABCDEF"

// uriEncode 按 JS encodeURI/encodeURIComponent 语义编码字符串。
func uriEncode(s string, component bool) string {
	var b strings.Builder
	for _, c := range s {
		if c < 0x80 {
			bc := byte(c)
			if strings.IndexByte(uriUnreserved, bc) >= 0 {
				b.WriteByte(bc)
				continue
			}
			if !component && strings.IndexByte(uriReserved, bc) >= 0 {
				b.WriteByte(bc)
				continue
			}
			b.WriteByte('%')
			b.WriteByte(hexDigits[(bc>>4)&0xF])
			b.WriteByte(hexDigits[bc&0xF])
			continue
		}
		// 多字节 UTF-8
		for _, u := range []byte(string(c)) {
			b.WriteByte('%')
			b.WriteByte(hexDigits[(u>>4)&0xF])
			b.WriteByte(hexDigits[u&0xF])
		}
	}
	return b.String()
}

// uriDecode 解码 %XX 序列。component=true（decodeURIComponent）时允许任意
// 百分号编码（含保留字符的编码）；否则对保留字符的编码保持原样不解码。
func uriDecode(s string, component bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			hi := hexVal(s[i+1])
			lo := hexVal(s[i+2])
			decoded := byte(hi<<4 | lo)
			// decodeURI 对保留字符的编码序列不解码。
			if !component && strings.IndexByte(uriReserved, decoded) >= 0 {
				b.WriteByte(c)
				b.WriteByte(s[i+1])
				b.WriteByte(s[i+2])
				i += 2
				continue
			}
			b.WriteByte(decoded)
			i += 2
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
