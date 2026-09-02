// 全局函数：parseInt/parseFloat/isNaN/eval/escape/unescape 等 globalThis 直挂成员。

package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/aluka-lang/aluka/internal/engine"
)

func (interp *Interpreter) setupGlobalFuncs() {
	parseIntFn := interp.makeFunc("parseInt", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.NaN()), nil
		}
		s := strings.TrimSpace(args[0].String())
		radix := 10
		if len(args) > 1 {
			r, ok := args[1].Int()
			if ok && r != 0 {
				radix = r
			}
		}
		n, err := strconv.ParseInt(s, radix, 64)
		if err != nil {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(float64(n)), nil
	})
	_ = interp.globalObj.Set("parseInt", parseIntFn)
	parseFloatFn := interp.makeFunc("parseFloat", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.NaN()), nil
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(args[0].String()), 64)
		if err != nil {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(f), nil
	})
	_ = interp.globalObj.Set("parseFloat", parseFloatFn)
	// Number.parseInt / Number.parseFloat are the same function objects as
	// their global counterparts (ECMAScript 2024, 21.1.2.13/14).
	if numberVal, err := interp.globalObj.Get("Number"); err == nil {
		if numberObj, ok := numberVal.AsObject(); ok {
			_ = numberObj.Set("parseInt", parseIntFn)
			_ = numberObj.Set("parseFloat", parseFloatFn)
		}
	}
	_ = interp.globalObj.Set("isNaN", interp.makeFunc("isNaN", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(true), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(!ok || math.IsNaN(f)), nil
	}))
	_ = interp.globalObj.Set("isFinite", interp.makeFunc("isFinite", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0)), nil
	}))
	// Node/V8 中 `hasOwnProperty.call(...)` 自由变量经 globalThis 的原型链
	// （→ Object.prototype）解析，不是全局自有键。globalThis 的原型链在
	// sweepBuiltinEnumerability 中统一挂接，这里不再误注册自有键。
	_ = interp.globalObj.Set("String", interp.constructors["String"])

	// eval（Node 语义）：参数非字符串原样返回；字符串在新程序全局作用域
	// 求值（间接 eval 语义——看不到调用方局部变量，与 V8 直接 eval 的
	// 差异记录于 gap-closure-plan §3 P1-5）。
	_ = interp.globalObj.Set("eval", interp.makeFunc("eval", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		if args[0].Type() != engine.TypeString {
			return args[0], nil
		}
		return interp.Eval(args[0].String(), "eval.js")
	}))

	// escape/unescape（deprecated 但 Node 保留）：escape 按 UTF-16 code unit
	// 编码——安全字符 A-Z a-z 0-9 @ * _ + - . / 原样；cu ≤ 0xFF → %XX，
	// 否则 %uXXXX（大写十六进制）。unescape 逆操作：%XX 按 Latin-1 字节、
	// %uXXXX 按 code unit 还原；非法转义序列原样保留。
	_ = interp.globalObj.Set("escape", interp.makeFunc("escape", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		var b strings.Builder
		for _, cu := range utf16.Encode([]rune(args[0].String())) {
			if isEscapeSafe(cu) {
				b.WriteRune(rune(cu))
				continue
			}
			if cu <= 0xFF {
				fmt.Fprintf(&b, "%%%02X", cu)
			} else {
				fmt.Fprintf(&b, "%%u%04X", cu)
			}
		}
		return engine.Str(b.String()), nil
	}))
	_ = interp.globalObj.Set("unescape", interp.makeFunc("unescape", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		var b strings.Builder
		s := args[0].String()
		hexVal := func(c byte) int {
			switch {
			case c >= '0' && c <= '9':
				return int(c - '0')
			case c >= 'a' && c <= 'f':
				return int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				return int(c-'A') + 10
			}
			return -1
		}
		for i := 0; i < len(s); {
			c := s[i]
			if c != '%' {
				b.WriteByte(c)
				i++
				continue
			}
			if i+5 < len(s) && (s[i+1] == 'u' || s[i+1] == 'U') {
				h := []int{hexVal(s[i+2]), hexVal(s[i+3]), hexVal(s[i+4]), hexVal(s[i+5])}
				if h[0] >= 0 && h[1] >= 0 && h[2] >= 0 && h[3] >= 0 {
					b.WriteRune(rune(h[0]<<12 | h[1]<<8 | h[2]<<4 | h[3]))
					i += 6
					continue
				}
			}
			if i+2 < len(s) {
				h1, h2 := hexVal(s[i+1]), hexVal(s[i+2])
				if h1 >= 0 && h2 >= 0 {
					b.WriteRune(rune(h1<<4 | h2))
					i += 3
					continue
				}
			}
			b.WriteByte(c)
			i++
		}
		return engine.Str(b.String()), nil
	}))
}

// isEscapeSafe 判断 code unit 是否属于 escape 保留的安全字符集。
func isEscapeSafe(cu uint16) bool {
	switch {
	case cu >= 'A' && cu <= 'Z', cu >= 'a' && cu <= 'z', cu >= '0' && cu <= '9':
		return true
	}
	return strings.IndexRune("@*_+-./", rune(cu)) >= 0
}

// toValues converts []string to []Value.
func toValues(keys []string) []engine.Value {
	vals := make([]engine.Value, len(keys))
	for i, k := range keys {
		vals[i] = engine.Str(k)
	}
	return vals
}
