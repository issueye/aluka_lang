package regex

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// 不支持的正则特性对应的错误。
var (
	errBackref      = errors.New("backreferences are not supported")
	errLookaround   = errors.New("lookahead/lookbehind are not supported")
	errClassSubset  = errors.New("character class subset complement is not supported")
	errUnterminated = errors.New("invalid regular expression: unterminated pattern")
)

// jsWhiteSpaceClass 是 JS 的 \s（WhiteSpace + LineTerminator）全集：
// TAB/VT/FF/SP/NBSP/ZWNBSP/USP + LF/CR/LS/PS。
const jsWhiteSpaceClass = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// jsWhiteSpaceGoRanges 将 JS 空白全集转成 Go 类内范围（\x{...} 形式，
// 相邻字符合并为范围）。字符类内嵌 `\s` 必须内联展开——Go 不支持嵌套类，
// `[[...]]` 会把 [ 当作类成员，导致 [^\s] 语义错误。
func jsWhiteSpaceGoRanges() string {
	runes := []rune(jsWhiteSpaceClass)
	var b strings.Builder
	i := 0
	for i < len(runes) {
		j := i
		for j+1 < len(runes) && runes[j+1] == runes[j]+1 {
			j++
		}
		if j-i >= 1 {
			b.WriteString(goCodePoint(runes[i]))
			b.WriteByte('-')
			b.WriteString(goCodePoint(runes[j]))
		} else {
			for k := i; k <= j; k++ {
				b.WriteString(goCodePoint(runes[k]))
			}
		}
		i = j + 1
	}
	return b.String()
}

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
				// Go RE2 不支持的属性（Default_Ignorable_Code_Point / RGI_Emoji）：
				// 展开为码点类或近似类；类外需包裹 [] 表示单字符匹配。
				if s, ok := unicodePropToGo(prop); ok {
					return "[" + s + "]", end + 1, nil
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
		// 统一翻译为 \x{...}（≤0xFFFF）或 UTF-8 字面量（>0xFFFF，
		// Go 的 \x{...} 仅支持 4 位十六进制）。
		if i+2 < len(pattern) && pattern[i+2] == '{' {
			end := i + 2
			for end < len(pattern) && pattern[end] != '}' {
				end++
			}
			if end >= len(pattern) {
				return "", 0, errors.New("invalid regular expression: unterminated \\u escape")
			}
			return goCodePoint(hexVal(pattern[i+3 : end])), end + 1, nil
		}
		if i+6 > len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return goCodePoint(hexVal(pattern[i+2 : i+6])), i + 6, nil
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
		if c == '-' && f.UnicodeSets && i+1 < len(pattern) && pattern[i+1] == '-' {
			// v 模式集合差集：[\p{Prop}--[子集]]（如 tui 的
			// [\p{Spacing_Mark}--[\u1734\u302E\u302F]]）。
			// setDifferenceClass 返回裸范围（不含 [ ]），类闭合仍由
			// 本函数外层 ']' 逻辑统一处理，避免双闭合。
			negatedIn := strings.Contains(b.String(), "^")
			out, ni, err := translateSetDifference(pattern, i, &b)
			if err != nil {
				return "", 0, err
			}
			b.Reset()
			if negatedIn {
				b.WriteString("[^")
			} else {
				b.WriteByte('[')
			}
			b.WriteString(out)
			i = ni
			hasAtom = true
			continue
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
			// 内联展开：Go 无嵌套类，[[...]] 会把 [ 当类成员。
			return jsWhiteSpaceGoRanges(), i + 2, nil
		}
		// 类内 \S 需"补集成员"语义（如 [^\S] = 空白），Go 类语法无法
		// 内联表达；报错回退到回溯引擎（其类 part 支持逐项取补）。
		return "", 0, errClassSubset
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
			return goCodePoint(hexVal(pattern[i+3 : end])), end + 1, nil
		}
		if i+6 > len(pattern) {
			return "", 0, errors.New("invalid regular expression: incomplete \\u escape")
		}
		return goCodePoint(hexVal(pattern[i+2 : i+6])), i + 6, nil
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
				// 类内直接输出展开串（无需包裹 []）。
				if s, ok := unicodePropToGo(prop); ok {
					return s, end + 1, nil
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

// unicodePropToGo 处理 Go RE2 不支持的 JS Unicode 属性（PropList 衍生属性），
// 返回可嵌入字符类的展开串。返回值用于类内（裸）或类外（调用方包裹 []）。
func unicodePropToGo(prop string) (string, bool) {
	switch prop {
	case "Default_Ignorable_Code_Point":
		// Unicode 15.1 PropList.txt：软连字符/零宽字符/变体选择符/标签字符等。
		// Go regexp 无此属性表，展开为码点范围类。
		return `\x{00AD}\x{034F}\x{061C}\x{115F}-\x{1160}\x{17B4}-\x{17B5}\x{180B}-\x{180E}\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2060}-\x{206F}\x{3164}\x{FE00}-\x{FE0F}\x{FEFF}\x{FFA0}\x{FFF0}-\x{FFF8}\x{1BCA0}-\x{1BCA3}\x{1D173}-\x{1D17A}\x{E0000}-\x{E0FFF}`, true
	case "RGI_Emoji":
		// Go RE2 无 emoji 序列集（含 ZWJ/肤色修饰组合）。近似为 \p{So}
		// （绝大多数 emoji 属此类）；精度近似，文档标注。
		return `\p{So}`, true
	}
	return "", false
}

// translateSetDifference 处理 v 模式字符类差集 [\p{Prop}--[子集]]。
// i 指向 "--" 的起始 '-'；b 已包含类前缀（如 "[\p{Mc}"）。仅支持
// "\p{Prop}--[字面量/\uHHHH/\x{...}/范围]" 形式（Pi 实际用法）。
func translateSetDifference(pattern string, i int, b *strings.Builder) (string, int, error) {
	cur := b.String()
	// 提取前缀中的 \p{Prop}：形如 "[\p{X}" 或 "[^\p{X}"。
	prop, ok := extractClassProp(cur)
	if !ok {
		return "", 0, errors.New("invalid regular expression: unsupported set difference (only \\p{Prop}--[subset] is supported)")
	}
	rt := rangeTableForProp(prop)
	if rt == nil {
		return "", 0, fmt.Errorf("invalid regular expression: unknown property %q in set difference", prop)
	}
	// 解析 "--" 后的子集 [ ... ]。
	exclude, ni, err := parseSubsetCodePoints(pattern, i+2)
	if err != nil {
		return "", 0, err
	}
	return setDifferenceClass(rt, exclude), ni, nil
}

// extractClassProp 从字符类前缀（如 "[\p{Mc}"、"[\p{Spacing_Mark}"）提取属性名。
func extractClassProp(prefix string) (string, bool) {
	// 查找 "\p{"，其后到字符串结尾即属性名。
	idx := strings.Index(prefix, `\p{`)
	if idx < 0 {
		return "", false
	}
	rest := prefix[idx+3:]
	if rest == "" {
		return "", false
	}
	for _, r := range rest {
		if r == '}' {
			rest = rest[:len(rest)-1]
			break
		}
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return "", false
		}
	}
	return strings.TrimSuffix(rest, "}"), true
}

// rangeTableForProp 将 JS 属性名映射为 Go unicode 表。
func rangeTableForProp(prop string) *unicode.RangeTable {
	switch prop {
	case "Mark", "M":
		return unicode.M
	case "Spacing_Mark", "Mc":
		return unicode.Mc
	case "Control", "Cc":
		return unicode.Cc
	case "Format", "Cf":
		return unicode.Cf
	case "Surrogate", "Cs":
		return unicode.Cs
	}
	return unicode.Categories[prop]
}

// parseSubsetCodePoints 解析差集子类 [\u1734\u302E\u302F] 为码点集合。
// 支持：字面量字符、\uHHHH、\x{HH..}、范围 a-b。返回 (集合, 结束位置)。
func parseSubsetCodePoints(pattern string, start int) (map[rune]bool, int, error) {
	if start >= len(pattern) || pattern[start] != '[' {
		return nil, 0, errors.New("invalid regular expression: set difference requires [subset]")
	}
	set := make(map[rune]bool)
	i := start + 1
	for i < len(pattern) && pattern[i] != ']' {
		c := pattern[i]
		var lo rune
		if c == '\\' {
			if i+1 >= len(pattern) {
				return nil, 0, errors.New("invalid regular expression: unterminated subset escape")
			}
			e := pattern[i+1]
			switch {
			case e == 'u' && i+5 < len(pattern) && pattern[i+2] == '{':
				// \u{...}
				j := i + 3
				for j < len(pattern) && pattern[j] != '}' {
					j++
				}
				if j >= len(pattern) {
					return nil, 0, errors.New("invalid regular expression: unterminated \\u escape")
				}
				var v rune
				for _, h := range pattern[i+3 : j] {
					v = v*16 + rune(hexDigitValue(byte(h)))
				}
				lo = v
				i = j + 1
			case e == 'u' && i+6 <= len(pattern):
				lo = rune(hex4(pattern[i+2 : i+6]))
				i += 6
			case e == 'x' && i+4 <= len(pattern):
				lo = rune(hex4("00" + pattern[i+2:i+4]))
				i += 4
			default:
				lo = rune(e)
				i += 2
			}
		} else {
			lo = rune(c)
			i++
		}
		// 范围 a-b。
		if i+1 < len(pattern) && pattern[i] == '-' && pattern[i+1] != ']' {
			i++
			var hi rune
			if pattern[i] == '\\' && i+5 < len(pattern) && pattern[i+1] == 'u' {
				hi = rune(hex4(pattern[i+2 : i+6]))
				i += 6
			} else {
				hi = rune(pattern[i])
				i++
			}
			for r := lo; r <= hi; r++ {
				set[r] = true
			}
		} else {
			set[lo] = true
		}
	}
	if i >= len(pattern) {
		return nil, 0, errors.New("invalid regular expression: unterminated subset class")
	}
	return set, i + 1, nil
}

// setDifferenceClass 计算 RangeTable 减去码点集合后的字符类（Go 语法）。
// setDifferenceClass 计算 RangeTable 减去码点集合后的字符类内容
// （裸范围序列，不含 [ ]，由调用方包裹）。
func setDifferenceClass(rt *unicode.RangeTable, exclude map[rune]bool) string {
	var b strings.Builder
	emit := func(lo, hi rune) {
		if lo == hi {
			b.WriteString(goCodePoint(lo))
		} else {
			b.WriteString(goCodePoint(lo) + "-" + goCodePoint(hi))
		}
	}
	emitRanges := func(lo, hi rune) {
		for lo <= hi {
			for lo <= hi && exclude[lo] {
				lo++
			}
			if lo > hi {
				break
			}
			start := lo
			for lo <= hi && !exclude[lo] {
				lo++
			}
			emit(start, lo-1)
		}
	}
	for _, r := range rt.R16 {
		emitRanges(rune(r.Lo), rune(r.Hi))
	}
	for _, r := range rt.R32 {
		emitRanges(rune(r.Lo), rune(r.Hi))
	}
	return b.String()
}

// goCodePoint 将码点输出为 Go regexp 可识别的形式：
// ≤0xFFFF 用 \x{HHHH}（Go 的 \x{...} 仅支持 1-4 位十六进制）；
// >0xFFFF 嵌入 UTF-8 字面量（RE2 对超出 4 位的 \x{...} 会截断解析）。
func goCodePoint(r rune) string {
	if r <= 0xFFFF {
		return fmt.Sprintf(`\x{%X}`, r)
	}
	return string(r)
}

// hex4 解析 4 位十六进制（供子集解析使用）。
func hex4(s string) int {
	v := 0
	for _, c := range s {
		v = v*16 + hexDigitValue(byte(c))
	}
	return v
}

func hexDigitValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}
