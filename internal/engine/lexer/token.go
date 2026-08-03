// Package lexer 实现 JavaScript 词法分析器。
//
// 将源码字符串转换为 Token 流，支持 ES5 + ES2015 关键 token 子集。
// Phase 1A 范围：完整 ES5 + 部分 ES2015（let/const/arrow/=>/spread）。
package lexer

import "fmt"

// TokenType 表示 token 的种类。
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenNumber     // 123, 1.5, 0x1F, 0o17, 0b101
	TokenString     // "..." 或 '...'
	TokenTemplate   // `...` 模板字符串（Phase 1C 起用）
	TokenRegex      // /pattern/flags
	TokenIdent      // 标识符
	TokenKeyword    // 关键字
	TokenPunct      // 标点与运算符
)

// Token 是词法分析的产物。
type Token struct {
	Type  TokenType
	Value string // 字面值（字符串已去除引号与转义）
	// 原始文本，便于 source map 与错误定位
	Raw   string
	Line  int
	Col   int
	// 对于 TokenRegex：标志位（gimuy）
	RegexFlags string
}

// String 用于调试输出。
func (t Token) String() string {
	switch t.Type {
	case TokenEOF:
		return "EOF"
	case TokenNumber:
		return fmt.Sprintf("Num(%s)", t.Value)
	case TokenString:
		return fmt.Sprintf("Str(%q)", t.Value)
	case TokenIdent:
		return fmt.Sprintf("Ident(%s)", t.Value)
	case TokenKeyword:
		return fmt.Sprintf("Kw(%s)", t.Value)
	case TokenPunct:
		return fmt.Sprintf("Punct(%s)", t.Value)
	case TokenRegex:
		return fmt.Sprintf("Regex(/%s/%s)", t.Value, t.RegexFlags)
	case TokenTemplate:
		return fmt.Sprintf("Tpl(%q)", t.Value)
	}
	return "?"
}

// 关键字集合。
// 包含 ES5 + 部分 ES2015 关键字。
var keywords = map[string]bool{
	// ES5
	"break": true, "case": true, "catch": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true,
	"else": true, "finally": true, "for": true, "function": true,
	"if": true, "in": true, "instanceof": true, "new": true,
	"return": true, "switch": true, "this": true, "throw": true,
	"try": true, "typeof": true, "var": true, "void": true,
	"while": true, "with": true,
	// 字面量
	"true": true, "false": true, "null": true, "undefined": true,
	// ES2015+（部分提前支持）
	"let": true, "const": true, "class": true, "extends": true,
	"super": true, "import": true, "export": true, "yield": true,
	"of": true, "async": true, "await": true,
}

// IsKeyword 判断字符串是否为关键字。
func IsKeyword(s string) bool { return keywords[s] }

// 多字符标点运算符（按长度降序匹配，最长优先）。
var multiPuncts = []string{
	">>>=", "===", "!==",
	">>>", "**=", "<<=", ">>=", "...", "=>",
	"??", "==", "!=", "<=", ">=", "&&", "||", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "**",
	"<<", ">>", "?.",
}

// 单字符标点集合。
var singlePuncts = "{}()[].;,?:+-*/%<>=!&|^~"

// IsPunctChar 判断字符是否可作为标点的起始。
func IsPunctChar(ch byte) bool {
	for i := 0; i < len(singlePuncts); i++ {
		if singlePuncts[i] == ch {
			return true
		}
	}
	return false
}
