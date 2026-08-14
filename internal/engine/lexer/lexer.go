package lexer

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer 是 JS 词法分析器。
type Lexer struct {
	src  string
	pos  int
	line int
	col  int
	// 上一个 token 是否允许 regex 字面量。
	// JS 中 / 既是除法也是 regex 起始，需根据上下文区分。
	// 简化规则：当上一个 token 是 ) ] } ident number keyword(非 typeof/void/in/instanceof) 时为除法。
	allowRegex   bool
	lastToken    Token
	parenControl []bool
	// braceControl 记录每个 { 是块（语句位置）还是对象字面量（表达式位置），
	// 用于区分 } 后是除法（对象字面量除法）还是正则（块结束后语句开头）。
	braceControl []bool
	// pendingBlockBrace 标记：刚闭合的控制语句 ) 之后，下一个 { 是块。
	pendingBlockBrace bool
}

// New 创建词法分析器。
func New(src string) *Lexer {
	// 剥离 UTF-8 BOM（EF BB BF），否则首字符被当作非法字符导致解析挂起/失败。
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		src = src[3:]
	}
	return &Lexer{
		src:        src,
		line:       1,
		col:        1,
		allowRegex: true, // 程序开头允许 regex
	}
}

// Next 读取下一个 token。
// 返回 Token 与可能的错误。读到末尾返回 TokenEOF。
func (l *Lexer) Next() (Token, error) {
	l.skipWhitespaceAndComments()
	if l.pos >= len(l.src) {
		return Token{Type: TokenEOF, Line: l.line, Col: l.col}, nil
	}

	startLine, startCol := l.line, l.col
	ch := l.src[l.pos]

	// 数字字面量
	if isDigit(ch) || (ch == '.' && l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1])) {
		return l.readNumber(startLine, startCol)
	}

	// 字符串字面量
	if ch == '"' || ch == '\'' {
		return l.readString(ch, startLine, startCol)
	}

	// 模板字符串（Phase 1C 用，1A 先消费但不解析表达式）
	if ch == '`' {
		return l.readTemplate(startLine, startCol)
	}

	// 标识符 / 关键字
	if isIdentStart(ch) {
		return l.readIdent(startLine, startCol)
	}

	// 私有名称（ECMAScript #field/#method）：'#' + 标识符。
	// 词法上作为一个带 '#' 前缀的 TokenIdent 发出，语义解析在 parser。
	if ch == '#' && l.pos+1 < len(l.src) && isIdentStart(l.src[l.pos+1]) {
		return l.readPrivateName(startLine, startCol)
	}

	// regex 字面量（仅在特定上下文，须先于 punct 检测）
	if ch == '/' && l.allowRegex {
		return l.readRegex(startLine, startCol)
	}

	// 标点 / 运算符
	if IsPunctChar(ch) {
		return l.readPunct(startLine, startCol)
	}

	return Token{}, fmt.Errorf("unexpected character %q at line %d:%d", ch, l.line, l.col)
}

// Tokens 读取全部 token 直到 EOF。
func (l *Lexer) Tokens() ([]Token, error) {
	var tokens []Token
	for {
		t, err := l.Next()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
		if t.Type == TokenEOF {
			break
		}
		// 更新 allowRegex 状态。控制语句的右括号结束后进入语句位置，
		// 因此允许正则；普通调用表达式右括号后仍按除法处理。
		allow := regexAllowedAfter(t)
		if t.Type == TokenPunct && t.Value == "(" {
			control := l.lastToken.Type == TokenKeyword && isControlParenKeyword(l.lastToken.Value)
			l.parenControl = append(l.parenControl, control)
		} else if t.Type == TokenPunct && t.Value == ")" && len(l.parenControl) > 0 {
			last := len(l.parenControl) - 1
			control := l.parenControl[last]
			l.parenControl = l.parenControl[:last]
			if control {
				allow = true
				l.pendingBlockBrace = true // 控制语句 ) 后，下一个 { 是块
			}
		}
		// 跟踪 { 的上下文：块（语句位置）还是对象字面量（表达式位置）。
		// 块：; { } 之后，else/do/try/finally 之后，或控制语句 ) 之后；
		// 对象字面量：= ( [ , : 等表达式位置之后。
		if t.Type == TokenPunct && t.Value == "{" {
			isBlock := isBlockBrace(l.lastToken) || l.pendingBlockBrace
			l.braceControl = append(l.braceControl, isBlock)
		} else if t.Type == TokenPunct && t.Value == "}" && len(l.braceControl) > 0 {
			last := len(l.braceControl) - 1
			isBlock := l.braceControl[last]
			l.braceControl = l.braceControl[:last]
			if isBlock {
				allow = true
			}
		}
		if t.Type != TokenPunct || (t.Value != "{" && t.Value != ")") {
			l.pendingBlockBrace = false
		}
		l.lastToken = t
		l.allowRegex = allow
	}
	return tokens, nil
}

func isControlParenKeyword(keyword string) bool {
	switch keyword {
	case "if", "for", "while", "with", "switch", "catch":
		return true
	default:
		return false
	}
}

// isBlockBrace 判断 { 是块还是对象字面量：紧跟在 ; { } 或 else/do/try/finally
// 之后的是块，否则（= ( [ , : 等表达式位置之后）是对象字面量。控制语句 ) 后
// 的情况由 pendingBlockBrace 标记处理，不在本函数内判断。
func isBlockBrace(prev Token) bool {
	switch prev.Type {
	case TokenPunct:
		switch prev.Value {
		case ";", "{", "}":
			return true
		}
	case TokenKeyword:
		switch prev.Value {
		case "else", "do", "try", "finally":
			return true
		}
	}
	return false
}

// regexAllowedAfter 判断某 token 之后是否允许 regex 字面量。
func regexAllowedAfter(t Token) bool {
	switch t.Type {
	case TokenNumber, TokenBigInt, TokenString, TokenRegex, TokenTemplate:
		return false
	case TokenIdent:
		return false
	case TokenKeyword:
		// 这些关键字之后允许 regex：return, typeof, case, in, instanceof, new, delete, void, throw, else, do, yield, await
		switch t.Value {
		case "return", "typeof", "case", "in", "instanceof",
			"new", "delete", "void", "throw", "else", "do", "yield", "await":
			return true
		}
		return false
	case TokenPunct:
		// ) ] } > < ++ -- 之后为除法或闭合符，不允许为 regex 开头
		switch t.Value {
		case ")", "]", "}", ">", "<", "++", "--":
			return false
		}
		return true
	case TokenEOF:
		return true
	}
	return true
}

// === 内部辅助 ============================================================

func (l *Lexer) advance() {
	if l.pos < len(l.src) {
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

func (l *Lexer) peek() byte {
	if l.pos < len(l.src) {
		return l.src[l.pos]
	}
	return 0
}

func (l *Lexer) peekAt(offset int) byte {
	idx := l.pos + offset
	if idx >= 0 && idx < len(l.src) {
		return l.src[idx]
	}
	return 0
}

func (l *Lexer) token(typ TokenType, val string) (Token, error) {
	return Token{Type: typ, Value: val, Line: l.line, Col: l.col}, nil
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		// 空白
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			l.advance()
			continue
		}
		// 行注释
		if ch == '/' && l.peekAt(1) == '/' {
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.advance()
			}
			continue
		}
		// 块注释
		if ch == '/' && l.peekAt(1) == '*' {
			l.advance()
			l.advance()
			for l.pos+1 < len(l.src) {
				if l.src[l.pos] == '*' && l.src[l.pos+1] == '/' {
					l.advance()
					l.advance()
					break
				}
				l.advance()
			}
			continue
		}
		// hashbang（#!... 在文件首行）
		if ch == '#' && l.pos == 0 && l.peekAt(1) == '!' {
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.advance()
			}
			continue
		}
		// UTF-8 BOM（仅文件首，防御非 New 入口解析路径）
		if ch == 0xEF && l.pos == 0 && l.pos+2 < len(l.src) && l.src[l.pos+1] == 0xBB && l.src[l.pos+2] == 0xBF {
			l.advance()
			l.advance()
			l.advance()
			continue
		}
		break
	}
}

func (l *Lexer) readNumber(startLine, startCol int) (Token, error) {
	start := l.pos
	// 十六进制 / 八进制 / 二进制（ES2021 数字分隔符在所有进制中均支持）
	if l.src[l.pos] == '0' && l.pos+1 < len(l.src) {
		next := l.src[l.pos+1]
		if next == 'x' || next == 'X' {
			l.advance() // 0
			l.advance() // x
			l.readDigitsWithSep(isHexDigit)
			// BigInt 后缀（ES2020）：0xFFn
			if l.pos < len(l.src) && l.src[l.pos] == 'n' {
				l.advance()
				return l.makeNumToken(start, startLine, startCol, true), nil
			}
			return l.makeNumToken(start, startLine, startCol, false), nil
		}
		if next == 'o' || next == 'O' {
			l.advance()
			l.advance()
			l.readDigitsWithSep(isOctDigit)
			if l.pos < len(l.src) && l.src[l.pos] == 'n' {
				l.advance()
				return l.makeNumToken(start, startLine, startCol, true), nil
			}
			return l.makeNumToken(start, startLine, startCol, false), nil
		}
		if next == 'b' || next == 'B' {
			l.advance()
			l.advance()
			l.readDigitsWithSep(isBinDigit)
			if l.pos < len(l.src) && l.src[l.pos] == 'n' {
				l.advance()
				return l.makeNumToken(start, startLine, startCol, true), nil
			}
			return l.makeNumToken(start, startLine, startCol, false), nil
		}
	}
	// 十进制整数与小数（含数字分隔符：1_000_000、1_000.500_25、1e9_5）
	l.readDigitsWithSep(isDigit)
	hasDecimal := false
	// 小数部分
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		hasDecimal = true
		l.advance()
		l.readDigitsWithSep(isDigit)
	}
	// 指数
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		hasDecimal = true
		l.advance()
		if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
			l.advance()
		}
		l.readDigitsWithSep(isDigit)
	}
	// BigInt 后缀（ES2020）：仅整数形式可带 n（123n），小数/指数不行（1.5n 非法）。
	if !hasDecimal && l.pos < len(l.src) && l.src[l.pos] == 'n' {
		l.advance()
		return l.makeNumToken(start, startLine, startCol, true), nil
	}
	return l.makeNumToken(start, startLine, startCol, false), nil
}

// makeNumToken 构造数字 token。isBigInt 为 true 时返回 TokenBigInt，
// Value 为去掉 n 后缀的整数字面量（保留进制前缀，由 parser 转换为十进制）。
func (l *Lexer) makeNumToken(start, startLine, startCol int, isBigInt bool) Token {
	val := l.src[start:l.pos]
	if isBigInt {
		// 去掉末尾的 n 后缀。
		val = strings.TrimSuffix(val, "n")
		return Token{Type: TokenBigInt, Value: val, Raw: l.src[start:l.pos], Line: startLine, Col: startCol}
	}
	return Token{Type: TokenNumber, Value: val, Raw: val, Line: startLine, Col: startCol}
}

// readDigitsWithSep 连续读取满足 pred 的数字与单个下划线分隔符（ES2021）。
// 下划线必须夹在两个数字之间；连续下划线或末尾下划线不在此处消费（留给后续报错）。
func (l *Lexer) readDigitsWithSep(pred func(byte) bool) {
	for l.pos < len(l.src) {
		// 下划线分隔符：仅当下一个字符是数字时才消费
		if l.src[l.pos] == '_' && l.pos+1 < len(l.src) && pred(l.src[l.pos+1]) {
			l.advance() // _
			continue
		}
		if pred(l.src[l.pos]) {
			l.advance()
			continue
		}
		break
	}
}

func (l *Lexer) readString(quote byte, startLine, startCol int) (Token, error) {
	l.advance() // 跳过开头引号
	var b strings.Builder
	for l.pos < len(l.src) && l.src[l.pos] != quote {
		if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
			l.advance() // backslash
			esc, err := l.readEscape()
			if err != nil {
				return Token{}, err
			}
			b.WriteString(esc)
			continue
		}
		if l.src[l.pos] == '\n' {
			return Token{}, fmt.Errorf("unterminated string at line %d", startLine)
		}
		b.WriteByte(l.src[l.pos])
		l.advance()
	}
	if l.pos >= len(l.src) {
		return Token{}, fmt.Errorf("unterminated string at line %d", startLine)
	}
	l.advance() // 跳过结尾引号
	return Token{Type: TokenString, Value: b.String(), Line: startLine, Col: startCol}, nil
}

// readEscape 读取转义序列并返回解译后的字符串。
func (l *Lexer) readEscape() (string, error) {
	ch := l.src[l.pos]
	l.advance()
	switch ch {
	case 'n':
		return "\n", nil
	case 't':
		return "\t", nil
	case 'r':
		return "\r", nil
	case 'b':
		return "\b", nil
	case 'f':
		return "\f", nil
	case 'v':
		return "\v", nil
	case '0':
		return "\x00", nil
	case '\\':
		return "\\", nil
	case '"':
		return "\"", nil
	case '\'':
		return "'", nil
	case '`':
		return "`", nil
	case '/':
		return "/", nil
	case 'x':
		if l.pos+1 >= len(l.src) {
			return "", fmt.Errorf("invalid \\x escape")
		}
		hex := string(l.src[l.pos]) + string(l.src[l.pos+1])
		l.advance()
		l.advance()
		var n int
		_, err := fmt.Sscanf(hex, "%x", &n)
		if err != nil {
			return "", err
		}
		return string(rune(n)), nil
	case 'u':
		if l.pos < len(l.src) && l.src[l.pos] == '{' {
			l.advance() // {
			start := l.pos
			for l.pos < len(l.src) && l.src[l.pos] != '}' {
				l.advance()
			}
			hex := l.src[start:l.pos]
			if l.pos < len(l.src) {
				l.advance() // }
			}
			var n int
			_, err := fmt.Sscanf(hex, "%x", &n)
			if err != nil {
				return "", err
			}
			return string(rune(n)), nil
		}
		if l.pos+3 >= len(l.src) {
			return "", fmt.Errorf("invalid \\u escape")
		}
		hex := l.src[l.pos : l.pos+4]
		for i := 0; i < 4; i++ {
			l.advance()
		}
		var n int
		_, err := fmt.Sscanf(hex, "%x", &n)
		if err != nil {
			return "", err
		}
		return string(rune(n)), nil
	case '\n':
		return "", nil // 行续
	}
	return string(ch), nil
}

func (l *Lexer) readTemplate(startLine, startCol int) (Token, error) {
	l.advance()             // `
	var b strings.Builder   // cooked 文本（转义已处理）
	var raw strings.Builder // raw 文本（转义原文保留，供 tagged template 的 strings.raw 使用）
	for l.pos < len(l.src) && l.src[l.pos] != '`' {
		if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
			escStart := l.pos
			l.advance()
			esc, err := l.readEscape()
			if err != nil {
				return Token{}, err
			}
			b.WriteString(esc)
			raw.WriteString(l.src[escStart:l.pos])
			continue
		}
		// ${...} 表达式插值：Phase 1A 简化，不解析表达式，原样保留。
		// 用 SkipTemplateExpr 正确配对大括号（跳过字符串/注释/嵌套模板）。
		if l.src[l.pos] == '$' && l.peekAt(1) == '{' {
			b.WriteString("${")
			raw.WriteString("${")
			l.advance()
			l.advance()
			end, ok := SkipTemplateExpr(l.src, l.pos-2)
			if !ok {
				return Token{}, fmt.Errorf("unterminated template literal at line %d", startLine)
			}
			// end 指向匹配 '}' 之后；表达式文本为 [l.pos, end-1)。
			seg := l.src[l.pos : end-1]
			b.WriteString(seg)
			raw.WriteString(seg)
			b.WriteByte('}')
			raw.WriteByte('}')
			for l.pos < end {
				l.advance()
			}
			continue
		}
		ch := l.src[l.pos]
		b.WriteByte(ch)
		raw.WriteByte(ch)
		l.advance()
	}
	if l.pos >= len(l.src) {
		return Token{}, fmt.Errorf("unterminated template at line %d", startLine)
	}
	l.advance() // 结尾 `
	return Token{Type: TokenTemplate, Value: b.String(), Raw: raw.String(), Line: startLine, Col: startCol}, nil
}

// SkipTemplateExpr 从 raw[i]=='$' 且 raw[i+1]=='{' 处扫描模板插值表达式，
// 跳过字符串字面量、行/块注释、转义序列与嵌套模板，返回匹配 '}' 之后的下标。
// 未找到匹配的 '}' 时返回 (0, false)。
func SkipTemplateExpr(raw string, i int) (int, bool) {
	depth := 1
	j := i + 2
	// lastSig 记录最近一个有效字符，用于判定 `/` 是正则起始还是除法。
	// 初始为 '{'（插值开括号之后允许正则）。
	lastSig := byte('{')
	for j < len(raw) && depth > 0 {
		c := raw[j]
		switch {
		case c == '\\':
			if j+1 < len(raw) {
				lastSig = raw[j+1]
				j += 2
			} else {
				j++
			}
			continue
		case c == '\'' || c == '"':
			quote := c
			j++
			for j < len(raw) && raw[j] != quote {
				if raw[j] == '\\' && j+1 < len(raw) {
					j += 2
				} else {
					j++
				}
			}
			j++ // 跳过结尾引号
			lastSig = quote
			continue
		case c == '`':
			nj, ok := skipTemplateLiteral(raw, j)
			if !ok {
				return 0, false
			}
			j = nj
			lastSig = '`'
			continue
		case c == '/' && j+1 < len(raw) && raw[j+1] == '/':
			for j < len(raw) && raw[j] != '\n' {
				j++
			}
			lastSig = '/'
			continue
		case c == '/' && j+1 < len(raw) && raw[j+1] == '*':
			j += 2
			for j+1 < len(raw) && !(raw[j] == '*' && raw[j+1] == '/') {
				j++
			}
			if j+1 < len(raw) {
				j += 2
			}
			lastSig = '/'
			continue
		case c == '/' && skipTemplateRegexAllowed(lastSig):
			// 正则字面量：扫描到未转义的闭合 '/'（字符类内 '/' 为普通字符），
			// 再消费 flags。结束后 `/` 视为除法。
			k := j + 1
			inClass := false
			for k < len(raw) {
				ch := raw[k]
				if ch == '\\' {
					k += 2
					continue
				}
				if ch == '[' {
					inClass = true
				} else if ch == ']' {
					inClass = false
				} else if ch == '/' && !inClass {
					break
				}
				k++
			}
			k++ // 闭合 /
			for k < len(raw) && (isIdentStart(raw[k]) || isDigit(raw[k])) {
				k++
			}
			lastSig = 'x' // 标识符类字符：之后 / 是除法
			j = k
			continue
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return j + 1, true
			}
		default:
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				lastSig = c
			}
		}
		j++
	}
	return 0, false
}

// skipTemplateRegexAllowed 判定模板插值扫描中 `/` 是否开启正则字面量：
// 前一个有效字符不是标识符/数字/右括号/右中括号/右大括号/引号/反引号时，
// `/` 起始正则（与主词法器 regexAllowedAfter 规则一致）。
func skipTemplateRegexAllowed(last byte) bool {
	switch {
	case isIdentStart(last) || isDigit(last):
		return false
	case last == ')' || last == ']' || last == '}':
		return false
	case last == '\'' || last == '"' || last == '`':
		return false
	}
	return true
}

// skipTemplateLiteral 跳过 raw[i]=='`' 处的模板字面量（含其插值），返回结尾 ` 之后的下标。
func skipTemplateLiteral(raw string, i int) (int, bool) {
	j := i + 1
	for j < len(raw) {
		switch {
		case raw[j] == '\\':
			if j+1 < len(raw) {
				j += 2
			} else {
				j++
			}
		case raw[j] == '`':
			return j + 1, true
		case raw[j] == '$' && j+1 < len(raw) && raw[j+1] == '{':
			end, ok := SkipTemplateExpr(raw, j)
			if !ok {
				return 0, false
			}
			j = end
		default:
			j++
		}
	}
	return 0, false
}

func (l *Lexer) readIdent(startLine, startCol int) (Token, error) {
	start := l.pos
	// 第一个字符
	if l.src[l.pos] >= 0x80 {
		// Unicode 标识符/符号首字符
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !unicode.IsGraphic(r) {
			// 非图形的高字节（如 UTF-8 BOM U+FEFF、孤立续字节）不能作为字符
			// 必须返回错误而非静默不前进，否则 Tokens() 会无限循环
			return Token{}, fmt.Errorf("unexpected character %q at line %d:%d", r, startLine, startCol)
		}
		l.pos += size
		l.col++
	}
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if isIdentPartRune(r) {
			l.pos += size
			l.col++
		} else {
			break
		}
	}
	word := l.src[start:l.pos]
	if IsKeyword(word) {
		return Token{Type: TokenKeyword, Value: word, Line: startLine, Col: startCol}, nil
	}
	return Token{Type: TokenIdent, Value: word, Line: startLine, Col: startCol}, nil
}

// readPrivateName 读取 ECMAScript 私有名称（#field / #method）。
// 值为带 '#' 前缀的标识符（如 "#matchOne"），保证与公有名称不冲突。
func (l *Lexer) readPrivateName(startLine, startCol int) (Token, error) {
	start := l.pos
	l.advance() // '#'
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if isIdentPartRune(r) {
			l.pos += size
			l.col++
		} else {
			break
		}
	}
	return Token{Type: TokenIdent, Value: l.src[start:l.pos], Line: startLine, Col: startCol}, nil
}

func (l *Lexer) readPunct(startLine, startCol int) (Token, error) {
	// 多字符优先（最长匹配）
	for _, p := range multiPuncts {
		if strings.HasPrefix(l.src[l.pos:], p) {
			for i := 0; i < len(p); i++ {
				l.advance()
			}
			return Token{Type: TokenPunct, Value: p, Line: startLine, Col: startCol}, nil
		}
	}
	// 单字符
	ch := l.src[l.pos]
	l.advance()
	return Token{Type: TokenPunct, Value: string(ch), Line: startLine, Col: startCol}, nil
}

func (l *Lexer) readRegex(startLine, startCol int) (Token, error) {
	l.advance() // 跳过开头 /
	start := l.pos
	inClass := false
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '\\' && l.pos+1 < len(l.src) {
			l.advance()
			l.advance()
			continue
		}
		if ch == '[' {
			inClass = true
		} else if ch == ']' {
			inClass = false
		} else if ch == '/' && !inClass {
			break
		} else if ch == '\n' {
			return Token{}, fmt.Errorf("unterminated regex at line %d", startLine)
		}
		l.advance()
	}
	pattern := l.src[start:l.pos]
	if l.pos >= len(l.src) {
		return Token{}, fmt.Errorf("unterminated regex at line %d", startLine)
	}
	l.advance() // 跳过结尾 /
	// flags
	flagsStart := l.pos
	for l.pos < len(l.src) && isIdentPartByte(l.src[l.pos]) {
		l.advance()
	}
	flags := l.src[flagsStart:l.pos]
	return Token{Type: TokenRegex, Value: pattern, RegexFlags: flags, Line: startLine, Col: startCol}, nil
}

// === 字符分类辅助 ========================================================

func isDigit(ch byte) bool { return ch >= '0' && ch <= '9' }
func isHexDigit(ch byte) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
func isOctDigit(ch byte) bool { return ch >= '0' && ch <= '7' }
func isBinDigit(ch byte) bool { return ch == '0' || ch == '1' }

func isIdentStart(ch byte) bool {
	return ch == '_' || ch == '$' ||
		(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		ch >= 0x80
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch)
}

func isIdentPartByte(ch byte) bool {
	return ch == '_' || ch == '$' ||
		(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch >= 0x80
}

func isIdentPartRune(r rune) bool {
	if r < 0x80 {
		return isIdentPartByte(byte(r))
	}
	return unicode.IsGraphic(r) || r == '$' || r == '_'
}
