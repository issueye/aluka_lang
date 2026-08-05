package regex

import (
	"errors"
	"fmt"
	"strings"
)

// 不支持的正则特性对应的错误。
var (
	errBackref      = errors.New("backreferences are not supported")
	errLookaround   = errors.New("lookahead/lookbehind are not supported")
	errUnterminated = errors.New("invalid regular expression: unterminated pattern")
)

// jsWhiteSpaceClass 是 JS 的 \s（WhiteSpace + LineTerminator）全集：
// TAB/VT/FF/SP/NBSP/ZWNBSP/USP + LF/CR/LS/PS。
const jsWhiteSpaceClass = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// translate 将 JS 正则 pattern 翻译为 Go RE2 语法。
//
// 处理要点：
//   - 字符类与点号：JS "." 不匹配换行（除非 s 标志），翻译为 [^\n\r\u2028\u2029]。
//   - \s/\S：Go 的 \s 集合窄于 JS，展开为 JS 空白全集以保证语义一致。
//   - 命名捕获 (?<name>...) 翻译为 Go 的 (?P<name>...)。
//   - \u{...}（u 模式码点转义）翻译为 Go 的 \x{...}。
//   - \/（字面量转义斜杠）翻译为 /；source 属性保留原文 \/。
//   - 反向引用（\1-\9、\k<name>）与前瞻/后行断言（(?=)(?!)(?<=)(?<!)）报错。
//   - 非 u 模式未知转义为 identity（去掉反斜杠）；u 模式未知转义报错。
func translate(pattern string, f Flags) (string, error) {
	var b strings.Builder
	b.Grow(len(pattern) + 8)
	i, n := 0, len(pattern)
	for i < n {
		c := pattern[i]
		switch {
		case c == '\\':
			if i+1 >= n {
				return "", errors.New("invalid regular expression: trailing backslash")
			}
			out, ni, err := translateEscape(pattern, i, f)
			if err != nil {
				return "", err
			}
			b.WriteString(out)
			i = ni
		case c == '.':
			if f.DotAll {
				// s：点号匹配一切字符。Go regexp 的 "." 默认不匹配换行，
				// 用 [\s\S] 覆盖全部字符。
				b.WriteString(`[\s\S]`)
			} else {
				// 无 s：点号匹配除 LineTerminator 外的一切字符
				// （Go regexp 不支持 \uHHHH 转义，统一用 \x{...} 表示码点）。
				b.WriteString(`[^\n\r\x{2028}\x{2029}]`)
			}
			i++
		case c == '[':
			out, ni, err := translateClass(pattern, i, f)
			if err != nil {
				return "", err
			}
			b.WriteString(out)
			i = ni
		case c == '(' && i+1 < n && pattern[i+1] == '?':
			out, ni, err := translateGroup(pattern, i)
			if err != nil {
				return "", err
			}
			b.WriteString(out)
			i = ni
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), nil
}

// translateEscape 处理 pattern[i]=='\\' 处的转义序列，返回翻译结果与下一个扫描位置。
func translateEscape(pattern string, i int, f Flags) (string, int, error) {
	esc := pattern[i+1]
	switch {
	case esc == 'k':
		// \k<name> 命名反向引用。
		return "", 0, errBackref
	case esc >= '1' && esc <= '9':
		// \1-\9：反向引用（u 模式）或八进制/反向引用（非 u 模式），均不支持。
		return "", 0, errBackref
	case esc == 'p' || esc == 'P':
		// \p{...} 仅在 u/v 模式为 Unicode 属性转义（Go 原生支持）；
		// 非 u 模式为 identity 转义（字面量 p/P）。
		if (f.Unicode || f.UnicodeSets) && i+2 < len(pattern) && pattern[i+2] == '{' {
			end := i + 2
			for end < len(pattern) && pattern[end] != '}' {
				end++
			}
			if end >= len(pattern) {
				return "", 0, errors.New("invalid regular expression: unterminated \\p escape")
			}
			// ID_Start / ID_Continue 是 ECMAScript 用于标识符检测的衍生属性，
			// Go 的 regexp 不支持，这里展开为 Go 可识别的通用类别并集
			// （覆盖绝大多数标识符字符，path-to-regexp 等依赖它）。
			prop := pattern[i+3 : end]
			if esc == 'p' {
				if prop == "ID_Start" {
					return `\p{L}\p{Nl}`, end + 1, nil
				}
				if prop == "ID_Continue" {
					return `\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}_`, end + 1, nil
				}
			}
			return pattern[i : end+1], end + 1, nil
		}
		return string(esc), i + 2, nil
	case esc == 's' || esc == 'S':
		if esc == 's' {
			return "[" + jsWhiteSpaceClass + "]", i + 2, nil
		}
		return "[^" + jsWhiteSpaceClass + "]", i + 2, nil
	case esc == '/':
		// Go 不接受 \/，字面量转义斜杠译为裸斜杠。
		return "/", i + 2, nil
	case esc == '\\':
		// \\ 是字面反斜杠（Go 同样用 \\ 表示），原样保留。
		return `\\`, i + 2, nil
	case strings.ContainsRune(`.+*?()[]{}|^$`, rune(esc)):
		// 正则元字符转义：JS 中 \X 表示字面量 X，Go 同样接受 \X，原样保留
		// （如 \. \+ \* \? 等）。若塌成裸元字符会被 Go 当作运算符报错。
		return pattern[i : i+2], i + 2, nil
	case esc == '-':
		// 类外 - 为字面量；Go 不接受 \-(类外)，输出裸 -。
		return "-", i + 2, nil
	case esc == '0':
		// NUL 字符。
		return `\x00`, i + 2, nil
	case esc == 'u':
		// \uHHHH 或 \u{...}（u 模式码点）。Go regexp 不支持 \u 转义，
		// 统一翻译为 \x{HHHH}（\x{...} 按码点匹配，语义与 u 模式一致）。
		if i+2 < len(pattern) && pattern[i+2] == '{' {
			end := i + 2
			for end < len(pattern) && pattern[end] != '}' {
				end++
			}
			if end >= len(pattern) {
				return "", 0, errors.New("invalid regular expression: unterminated \\u escape")
			}
			return `\x` + pattern[i+2:end+1], end + 1, nil
		}
		if i+6 > len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return `\x{` + pattern[i+2:i+6] + `}`, i + 6, nil
	case esc == 'x':
		if i+4 > len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\x escape")
		}
		return pattern[i : i+4], i + 4, nil
	case esc == 'c':
		// \cX 控制字符。非字母的 \c 在非 u 模式为 identity 'c'。
		if i+2 >= len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\c escape")
		}
		ch := pattern[i+2]
		if ch < 'A' || ch > 'Z' {
			return "c", i + 2, nil
		}
		return fmt.Sprintf(`\x%02x`, ch&0x1f), i + 3, nil
	case strings.ContainsRune("dDwWbBnrtfv", rune(esc)):
		// 与 Go 语法一致的转义（\d \D \w \W \b \B \n \t \r \f \v）。
		return pattern[i : i+2], i + 2, nil
	default:
		// 未知转义：u 模式为语法错误；非 u 模式为 identity（去掉反斜杠）。
		if f.Unicode || f.UnicodeSets {
			return "", 0, fmt.Errorf("invalid regular expression: invalid escape \\%c", esc)
		}
		return string(esc), i + 2, nil
	}
}

// translateClass 复制字符类 [...] 并翻译其中的转义。
func translateClass(pattern string, i int, f Flags) (string, int, error) {
	var b strings.Builder
	b.WriteByte('[')
	i++ // 跳过 '['
	negated := false
	if i < len(pattern) && pattern[i] == '^' {
		negated = true
		b.WriteByte('^')
		i++
	}
	firstContent := true // 类内第一个内容字符（在 ^ 之后）
	hasAtom := false     // 是否已写入实际内容
	for i < len(pattern) {
		c := pattern[i]
		if c == '\\' {
			if i+1 >= len(pattern) {
				return "", 0, errors.New("invalid regular expression: unterminated character class")
			}
			out, ni, err := translateClassEscape(pattern, i, f)
			if err != nil {
				return "", 0, err
			}
			if out != "" {
				hasAtom = true
			}
			b.WriteString(out)
			i = ni
			firstContent = false
			continue
		}
		if c == ']' {
			if firstContent && hasClosingBracket(pattern, i+1) {
				// 类首的 ] 且其后还有闭合括号：字面 ]（如 []a] 匹配 ] 或 a）。
				b.WriteString(`\]`)
				hasAtom = true
				firstContent = false
				i++
				continue
			}
			// 类结束：空类（无任何内容）按 JS 语义处理。
			if !hasAtom {
				if negated {
					// [^] 匹配任意字符。
					return `[\s\S]`, i + 1, nil
				}
				// [] 永不匹配。
				return `[^\s\S]`, i + 1, nil
			}
			b.WriteByte(']')
			return b.String(), i + 1, nil
		}
		b.WriteByte(c)
		hasAtom = true
		firstContent = false
		i++
	}
	return "", 0, errors.New("invalid regular expression: unterminated character class")
}

// hasClosingBracket 判断 pattern[from:] 中是否存在下一个未转义的 ']'。
func hasClosingBracket(pattern string, from int) bool {
	for j := from; j < len(pattern); j++ {
		if pattern[j] == '\\' {
			j++ // 跳过转义序列
			continue
		}
		if pattern[j] == ']' {
			return true
		}
	}
	return false
}

// translateClassEscape 处理字符类内的转义。JS 字符类内未知转义一律为 identity
// （去掉反斜杠），与类外不同（类外非 u 模式才允许 identity）。
func translateClassEscape(pattern string, i int, f Flags) (string, int, error) {
	esc := pattern[i+1]
	switch {
	case esc == 's' || esc == 'S':
		if esc == 's' {
			return "[" + jsWhiteSpaceClass + "]", i + 2, nil
		}
		return "[^" + jsWhiteSpaceClass + "]", i + 2, nil
	case esc == '/':
		return "/", i + 2, nil
	case esc == '\\':
		return `\\`, i + 2, nil
	case strings.ContainsRune(`.+*?()[]{}|^$-`, rune(esc)):
		// 字符类内正则元字符转义原样保留（Go 支持类内 \X 转义）。
		// 注意 [\-] 若塌成 [-] 会被当作范围运算符，必须保留反斜杠。
		return pattern[i : i+2], i + 2, nil
	case esc == '0':
		return `\x00`, i + 2, nil
	case esc == 'u':
		if i+2 < len(pattern) && pattern[i+2] == '{' {
			end := i + 2
			for end < len(pattern) && pattern[end] != '}' {
				end++
			}
			if end >= len(pattern) {
				return "", 0, errors.New("invalid regular expression: unterminated \\u escape")
			}
			return `\x` + pattern[i+2:end+1], end + 1, nil
		}
		if i+6 > len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return `\x{` + pattern[i+2:i+6] + `}`, i + 6, nil
	case esc == 'x':
		if i+4 > len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\x escape")
		}
		return pattern[i : i+4], i + 4, nil
	case esc == 'c':
		if i+2 >= len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\c escape")
		}
		ch := pattern[i+2]
		if ch < 'A' || ch > 'Z' {
			return "c", i + 2, nil
		}
		return fmt.Sprintf(`\x%02x`, ch&0x1f), i + 3, nil
	case strings.ContainsRune("dDwWbBnrtfv]", rune(esc)):
		// \b 在字符类内表示退格（与 JS 一致）；\] 转义字面 ]。
		return pattern[i : i+2], i + 2, nil
	case esc == 'p' || esc == 'P':
		if (f.Unicode || f.UnicodeSets) && i+2 < len(pattern) && pattern[i+2] == '{' {
			end := i + 2
			for end < len(pattern) && pattern[end] != '}' {
				end++
			}
			if end >= len(pattern) {
				return "", 0, errors.New("invalid regular expression: unterminated \\p escape")
			}
			// 与 translateEscape 一致：展开 Go 不支持的 ECMAScript 标识符属性。
			prop := pattern[i+3 : end]
			if esc == 'p' {
				if prop == "ID_Start" {
					return `\p{L}\p{Nl}`, end + 1, nil
				}
				if prop == "ID_Continue" {
					return `\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}_`, end + 1, nil
				}
			}
			return pattern[i : end+1], end + 1, nil
		}
		return string(esc), i + 2, nil
	default:
		// 字符类内未知转义为 identity（JS 规范，u 模式同样允许部分）。
		return string(esc), i + 2, nil
	}
}

// translateGroup 处理 '(' 后紧跟 '?' 的分组构造。
func translateGroup(pattern string, i int) (string, int, error) {
	// pattern[i]=='('，pattern[i+1]=='?'。
	if i+2 >= len(pattern) {
		return "", 0, errUnterminated
	}
	switch pattern[i+2] {
	case '=':
		return "", 0, errLookaround // (?= 前瞻
	case '!':
		return "", 0, errLookaround // (?! 负前瞻
	case '<':
		// (?<= / (?<! 后行；否则为命名捕获 (?<name>...) → (?P<name>...)。
		if i+3 < len(pattern) && (pattern[i+3] == '=' || pattern[i+3] == '!') {
			return "", 0, errLookaround
		}
		j := i + 3
		for j < len(pattern) && pattern[j] != '>' {
			j++
		}
		if j >= len(pattern) {
			return "", 0, errors.New("invalid regular expression: unterminated group name")
		}
		name := pattern[i+3 : j]
		if name == "" {
			return "", 0, errors.New("invalid regular expression: empty group name")
		}
		return "(?P<" + name + ">", j + 1, nil
	case ':':
		return "(?:", i + 3, nil
	case 'P':
		// (?P<name>...) 已是 Go 语法，透传。
		return "(?P", i + 3, nil
	default:
		return "", 0, errors.New("invalid regular expression: unsupported group")
	}
}
