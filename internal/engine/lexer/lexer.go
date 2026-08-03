package lexer

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer 是 JS 词法分析器。
type Lexer struct {
	src    string
	pos    int
	line   int
	col    int
	// 上一个 token 是否允许 regex 字面量。
	// JS 中 / 既是除法也是 regex 起始，需根据上下文区分。
	// 简化规则：当上一个 token 是 ) ] } ident number keyword(非 typeof/void/in/instanceof) 时为除法。
	allowRegex bool
}

// New 创建词法分析器。
func New(src string) *Lexer {
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
		// 更新 allowRegex 状态
		l.allowRegex = regexAllowedAfter(t)
	}
	return tokens, nil
}

// regexAllowedAfter 判断某 token 之后是否允许 regex 字面量。
func regexAllowedAfter(t Token) bool {
	switch t.Type {
	case TokenNumber, TokenString, TokenRegex, TokenTemplate:
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
		// ) ] 之后为除法
		switch t.Value {
		case ")", "]", "}":
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
	l.advance() // `
	var b strings.Builder
	for l.pos < len(l.src) && l.src[l.pos] != '`' {
		if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
			l.advance()
			esc, err := l.readEscape()
			if err != nil {
				return Token{}, err
			}
			b.WriteString(esc)
			continue
		}
		// ${...} 表达式插值：Phase 1A 简化，不解析表达式，原样保留
		if l.src[l.pos] == '$' && l.peekAt(1) == '{' {
			b.WriteString("${")
			l.advance()
			l.advance()
			depth := 1
			for l.pos < len(l.src) && depth > 0 {
				if l.src[l.pos] == '{' {
					depth++
				} else if l.src[l.pos] == '}' {
					depth--
					if depth == 0 {
						b.WriteByte('}')
						l.advance()
						break
					}
				}
				b.WriteByte(l.src[l.pos])
				l.advance()
			}
			continue
		}
		b.WriteByte(l.src[l.pos])
		l.advance()
	}
	if l.pos >= len(l.src) {
		return Token{}, fmt.Errorf("unterminated template at line %d", startLine)
	}
	l.advance() // 结尾 `
	return Token{Type: TokenTemplate, Value: b.String(), Line: startLine, Col: startCol}, nil
}

func (l *Lexer) readIdent(startLine, startCol int) (Token, error) {
	start := l.pos
	// 第一个字符
	if l.src[l.pos] >= 0x80 {
		// Unicode 标识符
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if unicode.IsLetter(r) {
			l.pos += size
			l.col++
		}
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
	// 修正 col（advance 没被调用，需重新计算）
	l.col += len(word)
	if IsKeyword(word) {
		return Token{Type: TokenKeyword, Value: word, Line: startLine, Col: startCol}, nil
	}
	return Token{Type: TokenIdent, Value: word, Line: startLine, Col: startCol}, nil
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

func isDigit(ch byte) bool      { return ch >= '0' && ch <= '9' }
func isHexDigit(ch byte) bool   { return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') }
func isOctDigit(ch byte) bool   { return ch >= '0' && ch <= '7' }
func isBinDigit(ch byte) bool   { return ch == '0' || ch == '1' }

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
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '$' || r == '_'
}
