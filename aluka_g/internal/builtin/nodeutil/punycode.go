package nodeutil

// node:punycode 内置模块——废弃（DEP0040）的 Punycode 编解码。
//
// 对齐 Node 22（punycode 包 2.1.0）的公开面：
//   - encode(input)      Unicode 字符串 → Punycode（纯 ASCII）
//   - decode(input)      Punycode → Unicode 字符串
//   - toASCII(domain)    域名/邮箱 → ASCII（xn-- 形式）
//   - toUnicode(domain)  域名/邮箱 → Unicode
//   - ucs2.decode(str)   UTF-16 字符串 → 码点数组
//   - ucs2.encode(arr)   码点数组 → 字符串（String.fromCodePoint 语义）
//   - version            "2.1.0"
//
// 算法按 RFC 3492（punycode.js 2.1.0 逐行移植）实现。溢出/非法输入抛
// RangeError（消息与 punycode.js 一致）。

import (
	"strings"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
)

// --- RFC 3492 常量（与 punycode.js 2.1.0 一致） ---
const (
	pcMaxInt       = 2147483647
	pcBase         = 36
	pcTMin         = 1
	pcTMax         = 26
	pcSkew         = 38
	pcDamp         = 700
	pcInitialBias  = 72
	pcInitialN     = 128
	pcDelimiter    = '-'
	pcBaseMinusTMin = pcBase - pcTMin
)

// punycodeError 以 RangeError 语义抛出（消息与 punycode.js 一致）。
type punycodeError struct{ msg string }

func (e *punycodeError) Error() string { return e.msg }

// Unwrap 让 errors.Is 命中 engine.ErrRangeError → 解释器构造 RangeError。
func (e *punycodeError) Unwrap() error { return engine.ErrRangeError }

func pcError(msg string) error {
	return &punycodeError{msg: msg}
}

// pcBasicToDigit 对应 Node 内嵌 punycode 的 basicToDigit（严格范围检查：
// 非字母数字返回 base，与 npm punycode 2.1.0 的宽松判断不同）。
func pcBasicToDigit(codePoint int) int {
	if codePoint >= 0x30 && codePoint < 0x3A { // '0'-'9'
		return 26 + (codePoint - 0x30)
	}
	if codePoint >= 0x41 && codePoint < 0x5B { // 'A'-'Z'
		return codePoint - 0x41
	}
	if codePoint >= 0x61 && codePoint < 0x7B { // 'a'-'z'
		return codePoint - 0x61
	}
	return pcBase
}

// pcDigitToBasic 对应 punycode.js digitToBasic。
func pcDigitToBasic(digit, flag int) int {
	// 0..25 → a..z / A..Z；26..35 → 0..9
	if digit < 26 {
		return digit + 97 - (flag << 5)
	}
	return digit - 26 + 48 - (flag << 5)
}

// pcAdapt 对应 punycode.js adapt（RFC 3492 §3.4）。
func pcAdapt(delta, numPoints int, firstTime bool) int {
	if firstTime {
		delta /= pcDamp
	} else {
		delta >>= 1
	}
	delta += delta / numPoints
	k := 0
	for delta > (pcBaseMinusTMin*pcTMax)>>1 {
		delta /= pcBaseMinusTMin
		k += pcBase
	}
	return k + (pcBaseMinusTMin+1)*delta/(delta+pcSkew)
}

// pcUcs2Decode 对应 punycode.js ucs2decode：返回码点数组。
func pcUcs2Decode(input string) []int {
	var output []int
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		output = append(output, int(runes[i]))
	}
	return output
}

// pcUcs2Encode 对应 punycode.js ucs2encode（String.fromCodePoint 语义）。
func pcUcs2Encode(codePoints []int) string {
	var b strings.Builder
	for _, cp := range codePoints {
		b.WriteRune(rune(cp))
	}
	return b.String()
}

// pcPunycodeDecode 对应 punycode.js decode。
func pcPunycodeDecode(input string) (string, error) {
	var output []int
	inputLength := len(input)
	i := 0
	n := pcInitialN
	bias := pcInitialBias

	basic := strings.LastIndex(input, string(pcDelimiter))
	if basic < 0 {
		basic = 0
	}
	for j := 0; j < basic; j++ {
		if input[j] >= 0x80 {
			return "", pcError("Illegal input >= 0x80 (not a basic code point)")
		}
		output = append(output, int(input[j]))
	}

	index := 0
	if basic > 0 {
		index = basic + 1
	}
	for index < inputLength {
		oldi := i
		w := 1
		for k := pcBase; ; k += pcBase {
			if index >= inputLength {
				return "", pcError("Invalid input")
			}
			digit := pcBasicToDigit(int(input[index]))
			index++
			if digit >= pcBase {
				return "", pcError("Invalid input")
			}
			if digit > (pcMaxInt-i)/w {
				return "", pcError("Overflow: input needs wider integers to process")
			}
			i += digit * w
			t := 0
			if k <= bias {
				t = pcTMin
			} else if k >= bias+pcTMax {
				t = pcTMax
			} else {
				t = k - bias
			}
			if digit < t {
				break
			}
			baseMinusT := pcBase - t
			if w > pcMaxInt/baseMinusT {
				return "", pcError("Overflow: input needs wider integers to process")
			}
			w *= baseMinusT
		}
		out := len(output) + 1
		bias = pcAdapt(i-oldi, out, oldi == 0)
		if i/out > pcMaxInt-n {
			return "", pcError("Overflow: input needs wider integers to process")
		}
		n += i / out
		i %= out
		output = append(output, 0)
		copy(output[i+1:], output[i:])
		output[i] = n
		i++
	}
	return pcUcs2Encode(output), nil
}

// pcPunycodeEncode 对应 punycode.js encode。
func pcPunycodeEncode(input string) (string, error) {
	var output []string
	codePoints := pcUcs2Decode(input)
	inputLength := len(codePoints)

	n := pcInitialN
	delta := 0
	bias := pcInitialBias

	for _, cp := range codePoints {
		if cp < 0x80 {
			output = append(output, string(rune(cp)))
		}
	}
	basicLength := len(output)
	handledCPCount := basicLength
	if basicLength > 0 {
		output = append(output, string(pcDelimiter))
	}

	for handledCPCount < inputLength {
		m := pcMaxInt
		for _, cp := range codePoints {
			if cp >= n && cp < m {
				m = cp
			}
		}
		handledCPCountPlusOne := handledCPCount + 1
		if m-n > (pcMaxInt-delta)/handledCPCountPlusOne {
			return "", pcError("Overflow: input needs wider integers to process")
		}
		delta += (m - n) * handledCPCountPlusOne
		n = m

		for _, cp := range codePoints {
			if cp < n {
				delta++
				if delta > pcMaxInt {
					return "", pcError("Overflow: input needs wider integers to process")
				}
			}
			if cp == n {
				q := delta
				for k := pcBase; ; k += pcBase {
					t := 0
					if k <= bias {
						t = pcTMin
					} else if k >= bias+pcTMax {
						t = pcTMax
					} else {
						t = k - bias
					}
					if q < t {
						break
					}
					qMinusT := q - t
					baseMinusT := pcBase - t
					output = append(output, string(rune(pcDigitToBasic(t+qMinusT%baseMinusT, 0))))
					q = qMinusT / baseMinusT
				}
				output = append(output, string(rune(pcDigitToBasic(q, 0))))
				bias = pcAdapt(delta, handledCPCountPlusOne, handledCPCount == basicLength)
				delta = 0
				handledCPCount++
			}
		}
		delta++
		n++
	}
	return strings.Join(output, ""), nil
}

// pcMapDomain 对应 punycode.js mapDomain：邮箱 local part 不动，标签按
// RFC 3490 分隔符切分后逐个应用 fn。
func pcMapDomain(domain string, fn func(label string) string) string {
	parts := strings.SplitN(domain, "@", 2)
	result := ""
	if len(parts) > 1 {
		result = parts[0] + "@"
		domain = parts[1]
	}
	domain = strings.ReplaceAll(domain, "\u3002", "\u002e")
	domain = strings.ReplaceAll(domain, "\uff0e", "\u002e")
	domain = strings.ReplaceAll(domain, "\uff61", "\u002e")
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		result += fn(label)
		result += "."
	}
	// 去掉尾部多余的分隔符（Node 的 join('.') 语义）。
	return strings.TrimSuffix(result, ".")
}

// NewPunycode 构造 node:punycode 模块导出对象（DEP0040 废弃）。
func NewPunycode(ctx engine.Context) (engine.Value, error) {
	nodebase.EmitDeprecation("DEP0040", "The `punycode` module is deprecated. Please use a userland alternative instead.")
	m := engine.NewObject()

	_ = m.Set("version", engine.Str("2.1.0"))

	// ucs2 子对象。
	ucs2 := engine.NewObject()
	_ = ucs2.Set("decode", engine.NewFunction("decode", func(args []engine.Value) (engine.Value, error) {
		input := nodebase.StrArg(args, 0)
		cps := pcUcs2Decode(input)
		vals := make([]engine.Value, len(cps))
		for i, cp := range cps {
			vals[i] = engine.IntValue(cp)
		}
		return engine.NewArray(vals), nil
	}))
	_ = ucs2.Set("encode", engine.NewFunction("encode", func(args []engine.Value) (engine.Value, error) {
		var cps []int
		if arr, ok := args[0].(*engine.ArrayValue); ok {
			for _, e := range arr.Elems() {
				n, _ := e.Int()
				cps = append(cps, n)
			}
		}
		return engine.Str(pcUcs2Encode(cps)), nil
	}))
	_ = m.Set("ucs2", ucs2)

	// decode(input)：Punycode → Unicode。
	_ = m.Set("decode", engine.NewFunction("decode", func(args []engine.Value) (engine.Value, error) {
		out, err := pcPunycodeDecode(nodebase.StrArg(args, 0))
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(out), nil
	}))

	// encode(input)：Unicode → Punycode。
	_ = m.Set("encode", engine.NewFunction("encode", func(args []engine.Value) (engine.Value, error) {
		out, err := pcPunycodeEncode(nodebase.StrArg(args, 0))
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(out), nil
	}))

	// toUnicode(domain)。
	_ = m.Set("toUnicode", engine.NewFunction("toUnicode", func(args []engine.Value) (engine.Value, error) {
		input := nodebase.StrArg(args, 0)
		return engine.Str(pcMapDomain(input, func(label string) string {
			if strings.HasPrefix(label, "xn--") {
				if dec, err := pcPunycodeDecode(strings.ToLower(label[4:])); err == nil {
					return dec
				}
			}
			return label
		})), nil
	}))

	// toASCII(domain)。
	_ = m.Set("toASCII", engine.NewFunction("toASCII", func(args []engine.Value) (engine.Value, error) {
		input := nodebase.StrArg(args, 0)
		return engine.Str(pcMapDomain(input, func(label string) string {
			if hasNonASCII(label) {
				if enc, err := pcPunycodeEncode(label); err == nil {
					return "xn--" + enc
				}
			}
			return label
		})), nil
	}))

	return m, nil
}

// hasNonASCII 判断字符串是否含非 ASCII 字符（Node: /[^\0-\x7F]/，
// 即 >= 0x80 的字符才需要 Punycode 编码）。
func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}
