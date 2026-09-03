package parser

import (
	"fmt"
	"math/big"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

// Parser 语法分析器。
type Parser struct {
	tokens []lexer.Token
	pos    int
	src    string

	// genStack tracks whether each enclosing function is a generator.
	// genStack[len-1] == true means we're currently inside a generator body,
	// so `yield` should be parsed as a YieldExpr.
	genStack []bool

	// noIn is set while parsing a for-init expression. ECMAScript treats the
	// for-init as Expression[+In=false], so the relational `in` operator must
	// NOT be consumed there (otherwise `for (k in obj)` miscarries as a binary
	// `k in obj` expression). Non-zero counter to allow nesting.
	noIn int

	// asyncStack tracks whether each enclosing function is async.
	// asyncStack[len-1] == true means we're currently inside an async function
	// body, so `await` should be parsed as an AwaitExpr.
	asyncStack []bool

	// allowTopLevelAwait 标记模块上下文（ESM）：顶层允许 await（TLA）。
	// 由 ParseModule 设置；脚本（Eval/REPL）保持 false。
	allowTopLevelAwait bool
}

// New 创建解析器。
func New(tokens []lexer.Token, src string) *Parser {
	return &Parser{tokens: tokens, src: src}
}

// ParseModule 以模块上下文解析源码（允许顶层 await）。
func ParseModule(src string) (*ast.Program, error) {
	l := lexer.New(src)
	tokens, err := l.Tokens()
	if err != nil {
		return nil, err
	}
	p := New(tokens, src)
	p.allowTopLevelAwait = true
	return p.parseProgram()
}

// NewFromString lexes and creates a parser from a source string. Used for
// parsing sub-expressions (e.g. template literal interpolations).
func NewFromString(src string) (*Parser, error) {
	l := lexer.New(src)
	tokens, err := l.Tokens()
	if err != nil {
		return nil, err
	}
	return New(tokens, src), nil
}

// Parse 解析整个程序。
func Parse(src string) (*ast.Program, error) {
	l := lexer.New(src)
	tokens, err := l.Tokens()
	if err != nil {
		return nil, err
	}
	p := New(tokens, src)
	return p.parseProgram()
}

// === 内部辅助 ============================================================

func (p *Parser) peek() lexer.Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return lexer.Token{Type: lexer.TokenEOF}
}

func (p *Parser) peekAt(n int) lexer.Token {
	idx := p.pos + n
	if idx >= 0 && idx < len(p.tokens) {
		return p.tokens[idx]
	}
	return lexer.Token{Type: lexer.TokenEOF}
}

func (p *Parser) next() lexer.Token {
	t := p.peek()
	if t.Type != lexer.TokenEOF {
		p.pos++
	}
	return t
}

func (p *Parser) match(typ lexer.TokenType, val string) bool {
	t := p.peek()
	if t.Type == typ && (val == "" || t.Value == val) {
		p.pos++
		return true
	}
	return false
}

func (p *Parser) matchPunct(val string) bool   { return p.match(lexer.TokenPunct, val) }
func (p *Parser) matchKeyword(val string) bool { return p.match(lexer.TokenKeyword, val) }

// matchIdent matches a contextual keyword that the lexer tokenizes as an
// identifier (e.g. "from", "as" in import/export declarations).
func (p *Parser) matchIdent(val string) bool { return p.match(lexer.TokenIdent, val) }

func (p *Parser) expect(typ lexer.TokenType, val string) (lexer.Token, error) {
	t := p.peek()
	if t.Type != typ || (val != "" && t.Value != val) {
		return t, p.errorf(t, "expected %q but got %q", val, t.Value)
	}
	p.pos++
	return t, nil
}

// expectName 读取一个标识符名：普通 TokenIdent 或上下文关键字
// （async/of/await/yield 等是合法 IdentifierName，可作变量/参数名）。
func (p *Parser) expectName() (lexer.Token, error) {
	t := p.peek()
	if t.Type == lexer.TokenIdent || t.Type == lexer.TokenKeyword {
		p.pos++
		return t, nil
	}
	return t, p.errorf(t, "expected name but got %q", t.Value)
}

func (p *Parser) expectPunct(val string) error {
	_, err := p.expect(lexer.TokenPunct, val)
	return err
}

func (p *Parser) errorf(t lexer.Token, format string, args ...interface{}) error {
	return fmt.Errorf("aluka: syntax error at line %d:%d: %s",
		t.Line, t.Col, fmt.Sprintf(format, args...))
}

func posOf(t lexer.Token) ast.Pos { return ast.Pos{Line: t.Line, Col: t.Col} }

// consumeSemicolon 消费可选分号或应用 ASI。
func (p *Parser) consumeSemicolon() error {
	if p.matchPunct(";") {
		return nil
	}
	// ASI: 行尾或 } 或 EOF 自动插入分号
	t := p.peek()
	if t.Type == lexer.TokenEOF || t.Type == lexer.TokenPunct && (t.Value == "}" || t.Value == ")") {
		return nil
	}
	// 行号变化也算 ASI（简化）
	if p.pos > 0 {
		prev := p.tokens[p.pos-1]
		if prev.Line != t.Line {
			return nil
		}
	}
	return p.errorf(t, "expected ';' but got %q", t.Value)
}

// === 程序入口 ============================================================

func (p *Parser) parseProgram() (*ast.Program, error) {
	prog := &ast.Program{SourceFile: "<input>"}
	for p.peek().Type != lexer.TokenEOF {
		// 跳过空语句
		if p.matchPunct(";") {
			continue
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		prog.Body = append(prog.Body, stmt)
	}
	return prog, nil
}

// === 语句解析 ============================================================


func bigIntLiteralToDecimal(s string) string {
	clean := ""
	for i := 0; i < len(s); i++ {
		if s[i] != '_' {
			clean += string(s[i])
		}
	}
	if len(clean) >= 2 && clean[0] == '0' {
		switch clean[1] {
		case 'x', 'X':
			if bi, ok := new(big.Int).SetString(clean[2:], 16); ok {
				return bi.String()
			}
		case 'o', 'O':
			if bi, ok := new(big.Int).SetString(clean[2:], 8); ok {
				return bi.String()
			}
		case 'b', 'B':
			if bi, ok := new(big.Int).SetString(clean[2:], 2); ok {
				return bi.String()
			}
		}
	}
	if bi, ok := new(big.Int).SetString(clean, 10); ok {
		return bi.String()
	}
	return clean
}

func parseNumberLiteral(s string) (float64, error) {
	clean := ""
	for i := 0; i < len(s); i++ {
		if s[i] != '_' {
			clean += string(s[i])
		}
	}
	// 十六进制
	if len(clean) >= 2 && clean[0] == '0' && (clean[1] == 'x' || clean[1] == 'X') {
		var n int64
		_, err := fmt.Sscanf(clean[2:], "%x", &n)
		return float64(n), err
	}
	// 八进制
	if len(clean) >= 2 && clean[0] == '0' && (clean[1] == 'o' || clean[1] == 'O') {
		var n int64
		_, err := fmt.Sscanf(clean[2:], "%o", &n)
		return float64(n), err
	}
	// 二进制
	if len(clean) >= 2 && clean[0] == '0' && (clean[1] == 'b' || clean[1] == 'B') {
		var n int64
		_, err := fmt.Sscanf(clean[2:], "%b", &n)
		return float64(n), err
	}
	var f float64
	_, err := fmt.Sscanf(clean, "%g", &f)
	return f, err
}

// === TypeScript declarations (interface / type / enum / namespace) ========

// parseInterfaceDecl skips a TypeScript `interface Name extends ... { members }`
// declaration — interfaces are compile-time only and produce no runtime code.
