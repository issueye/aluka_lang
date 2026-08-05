// Package parser 实现 JavaScript 语法分析器。
//
// 将 lexer.Token 流转换为 ast.Program。
// 采用递归下降 + Pratt 表达式优先级解析。
//
// Phase 1A 范围：ES5 子集 + ES2015 关键特性（let/const/arrow/spread/for-of）。
package parser

import (
	"fmt"
	"math/big"
	"strings"

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

func (p *Parser) parseStatement() (ast.Statement, error) {
	// TypeScript: skip leading `@decorator` expressions on declarations.
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "@" {
		if err := p.skipDecorators(); err != nil {
			return nil, err
		}
	}
	t := p.peek()
	switch t.Type {
	case lexer.TokenPunct:
		switch t.Value {
		case "{":
			return p.parseBlock()
		case ";":
			p.next()
			return &ast.EmptyStmt{Loc: posOf(t)}, nil
		}
	case lexer.TokenKeyword:
		switch t.Value {
		case "var", "let", "const":
			return p.parseVarDecl()
		case "function":
			return p.parseFunctionDecl(false)
		case "async":
			// `async function` declaration. Only treat as async when the next
			// token is `function`; otherwise `async` is a contextual keyword
			// (e.g. `async()` as a call, or `async = 1` as an identifier).
			next := p.peekAt(1)
			if next.Type == lexer.TokenKeyword && next.Value == "function" {
				return p.parseFunctionDecl(false)
			}
		case "class":
			return p.parseClassDecl()
		case "import":
			// 动态 import(...) 作为表达式语句：import 后紧跟 "(" 时不是声明。
			// 落到 parseExprStmt → parsePrimary 的 import 分支处理。
			next := p.peekAt(1)
			if next.Type == lexer.TokenPunct && next.Value == "(" {
				break
			}
			return p.parseImportDecl()
		case "export":
			return p.parseExportDecl()
		case "if":
			return p.parseIf()
		case "while":
			return p.parseWhile()
		case "do":
			return p.parseDoWhile()
		case "for":
			return p.parseFor()
		case "return":
			return p.parseReturn()
		case "break":
			return p.parseBreakContinue(true)
		case "continue":
			return p.parseBreakContinue(false)
		case "throw":
			return p.parseThrow()
		case "try":
			return p.parseTry()
		case "switch":
			return p.parseSwitch()
		case "debugger":
			p.next()
			return &ast.EmptyStmt{Loc: posOf(t)}, nil
		}
	case lexer.TokenIdent:
		// 标签语句：`name: statement`（仅当标识符后紧跟 ":"）。
		if p.peekAt(1).Type == lexer.TokenPunct && p.peekAt(1).Value == ":" {
			return p.parseLabeled()
		}
		// TypeScript contextual-keyword declarations.
		switch t.Value {
		case "interface":
			return p.parseInterfaceDecl()
		case "enum":
			return p.parseEnumDecl()
		case "namespace":
			return p.parseNamespaceDecl()
		case "type":
			// `type X = ...;` is a type alias. But `type` can also be a
			// regular identifier (e.g. `type === 'foo'`). Only treat as a
			// type alias when followed by an identifier and `=`.
			next := p.peekAt(1)
			if next.Type == lexer.TokenIdent {
				after := p.peekAt(2)
				if after.Type == lexer.TokenPunct && after.Value == "=" {
					return p.parseTypeAliasDecl()
				}
				// `type X<T> = ...` (generic type alias)
				if after.Type == lexer.TokenPunct && after.Value == "<" {
					return p.parseTypeAliasDecl()
				}
			}
		}
	}
	// 表达式语句
	return p.parseExprStmt()
}

// parseLabeled 解析标签语句 `name: statement`（如 OUTER: for (...) {...}）。
func (p *Parser) parseLabeled() (ast.Statement, error) {
	t := p.next() // 标签标识符
	if err := p.expectPunct(":"); err != nil {
		return nil, err
	}
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return &ast.LabeledStmt{Label: t.Value, Body: body, Loc: posOf(t)}, nil
}

func (p *Parser) parseBlock() (*ast.BlockStmt, error) {
	t := p.peek()
	if _, err := p.expect(lexer.TokenPunct, "{"); err != nil {
		return nil, err
	}
	block := &ast.BlockStmt{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated block")
		}
		if p.matchPunct(";") {
			continue
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		block.Body = append(block.Body, stmt)
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return block, nil
}

func (p *Parser) parseVarDecl() (*ast.VarDecl, error) {
	t := p.next() // var / let / const
	decl := &ast.VarDecl{Kind: t.Value, Loc: posOf(t)}
	for {
		vd, err := p.parseVarDeclarator()
		if err != nil {
			return nil, err
		}
		decl.Decls = append(decl.Decls, vd)
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.consumeSemicolon(); err != nil {
		return nil, err
	}
	return decl, nil
}

// parseVarDeclarator parses a single variable declarator: a name or
// destructuring pattern, optionally followed by `= init`.
func (p *Parser) parseVarDeclarator() (ast.VarDeclarator, error) {
	var vd ast.VarDeclarator
	// Detect destructuring pattern: [ ... ] or { ... }
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "[" {
		pat, err := p.parseArrayPattern()
		if err != nil {
			return vd, err
		}
		vd.Pattern = pat
	} else if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
		pat, err := p.parseObjectPattern()
		if err != nil {
			return vd, err
		}
		vd.Pattern = pat
	} else {
		nameTok, err := p.expect(lexer.TokenIdent, "")
		if err != nil {
			return vd, err
		}
		vd.Name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	}
	// TypeScript: optional type annotation `: T`.
	if err := p.parseTypeAnnotation(); err != nil {
		return vd, err
	}
	if p.matchPunct("=") {
		expr, err := p.parseAssignment()
		if err != nil {
			return vd, err
		}
		vd.Init = expr
	}
	return vd, nil
}

func (p *Parser) parseFunctionDecl(isExpr bool) (*ast.FunctionDecl, error) {
	// Detect `async function` — consume the `async` keyword if present.
	isAsync := false
	if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "async" {
		p.next() // consume async
		isAsync = true
	}
	t := p.next() // function
	isGenerator := p.matchPunct("*")
	if isExpr {
		// 函数表达式：可选名称
	}
	var name *ast.Identifier
	if p.peek().Type == lexer.TokenIdent {
		nameTok := p.next()
		name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	} else if !isExpr {
		return nil, p.errorf(p.peek(), "function declaration requires a name")
	}
	// TypeScript: skip generic type parameters `<T, U extends X, R = D>`.
	if err := p.skipTypeParameters(); err != nil {
		return nil, err
	}
	p.genStack = append(p.genStack, isGenerator)
	p.asyncStack = append(p.asyncStack, isAsync)
	params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
	p.genStack = p.genStack[:len(p.genStack)-1]
	p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
	if err != nil {
		return nil, err
	}
	return &ast.FunctionDecl{
		Name:          name,
		Params:        params,
		ParamPatterns: patterns,
		Defaults:    defaults,
		RestParam:   rest,
		Body:        body,
		IsAsync:     isAsync,
		IsGenerator: isGenerator,
		Loc:         posOf(t),
	}, nil
}

// parseFuncParamsAndBody parses `(params) { body }` and returns the regular
// params, their default expressions (nil entries = no default), the optional
// rest param (`...rest`), and the body block.
func (p *Parser) parseFuncParamsAndBody() ([]*ast.Identifier, []ast.Pattern, []ast.Expression, *ast.Identifier, *ast.BlockStmt, error) {
	if err := p.expectPunct("("); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	var params []*ast.Identifier
	var patterns []ast.Pattern
	var defaults []ast.Expression
	var rest *ast.Identifier
	if !(p.peek().Type == lexer.TokenPunct && p.peek().Value == ")") {
		for {
			// ES2015 rest parameter: `...name`
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "..." {
				p.next()
				nameTok, err := p.expect(lexer.TokenIdent, "")
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				rest = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
				// TypeScript: rest param type annotation `: T[]`
				if err := p.parseTypeAnnotation(); err != nil {
					return nil, nil, nil, nil, nil, err
				}
				break // rest must be the last parameter
			}
			// 解构参数：({a, b}, [x, y]) => ...（模式绑定名在编译期解构）。
			if p.peek().Type == lexer.TokenPunct && (p.peek().Value == "{" || p.peek().Value == "[") {
				pat, err := p.parsePatternTarget()
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				params = append(params, nil) // 占位：由 ParamPatterns 提供绑定
				patterns = append(patterns, pat)
				// TypeScript: 模式参数后的类型注解 `: T`。
				if err := p.parseTypeAnnotation(); err != nil {
					return nil, nil, nil, nil, nil, err
				}
				// 默认值：{a} = {} => ...
				if p.matchPunct("=") {
					def, err := p.parseAssignment()
					if err != nil {
						return nil, nil, nil, nil, nil, err
					}
					defaults = append(defaults, def)
				} else {
					defaults = append(defaults, nil)
				}
				if !p.matchPunct(",") {
					break
				}
				// 尾部逗号：(a, b,) 合法（ES2017 trailing comma）。
				if p.peek().Type == lexer.TokenPunct && p.peek().Value == ")" {
					break
				}
				continue
			}
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}
			params = append(params, &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)})
			patterns = append(patterns, nil)
			// TypeScript: optional `?` marker (param?: T) and type annotation.
			if p.matchPunct("?") {
				// optional parameter marker — just consume
			}
			if err := p.parseTypeAnnotation(); err != nil {
				return nil, nil, nil, nil, nil, err
			}
			// ES2015 default value: `name = expr`
			if p.matchPunct("=") {
				def, err := p.parseAssignment()
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				defaults = append(defaults, def)
			} else {
				defaults = append(defaults, nil)
			}
				if !p.matchPunct(",") {
					break
				}
				// 尾部逗号：(a, b,) 合法（ES2017 trailing comma）。
				if p.peek().Type == lexer.TokenPunct && p.peek().Value == ")" {
					break
				}
			}
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, nil, nil, nil, nil, err
	}
	// TypeScript: optional return type annotation `: T` before the body.
	if err := p.parseTypeAnnotation(); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return params, patterns, defaults, rest, body, nil
}

func (p *Parser) parseIf() (*ast.IfStmt, error) {
	t := p.next()
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	test, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	cons, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	var alt ast.Statement
	if p.matchKeyword("else") {
		alt, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
	}
	return &ast.IfStmt{Test: test, Consequent: cons, Alternate: alt, Loc: posOf(t)}, nil
}

func (p *Parser) parseWhile() (*ast.WhileStmt, error) {
	t := p.next()
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	test, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return &ast.WhileStmt{Test: test, Body: body, Loc: posOf(t)}, nil
}

func (p *Parser) parseDoWhile() (*ast.DoWhileStmt, error) {
	t := p.next()
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	if !p.matchKeyword("while") {
		return nil, p.errorf(p.peek(), "expected 'while' after do-block")
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	test, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	_ = p.matchPunct(";")
	return &ast.DoWhileStmt{Body: body, Test: test, Loc: posOf(t)}, nil
}

func (p *Parser) parseFor() (ast.Statement, error) {
	t := p.next()

	// for await...of：仅在 async 函数体内合法（ES2018）；模块顶层（TLA
	// 上下文）同样允许（Node 允许顶层 for await...of）。
	// token 序列为 `for` `await` `(`，因此 await 必须在期望 "(" 之前消费。
	isForAwait := false
	if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "await" {
		inAsync := len(p.asyncStack) > 0 && p.asyncStack[len(p.asyncStack)-1]
		topLevelModule := p.allowTopLevelAwait && len(p.asyncStack) == 0
		if !inAsync && !topLevelModule {
			return nil, fmt.Errorf("aluka: syntax error: for await...of is only valid in async functions")
		}
		p.next() // 消费 await
		isForAwait = true
	}

	if err := p.expectPunct("("); err != nil {
		return nil, err
	}

	// 判断是否 for-in / for-of
	// 形如：for (var x in obj) / for (let x of arr) / for (x in obj)
	var leftNode ast.Node
	var initIsVarDecl bool
	var decl *ast.VarDecl

	if p.matchPunct(";") {
		// 普通 for：无 init
	} else if p.peek().Type == lexer.TokenKeyword &&
		(p.peek().Value == "var" || p.peek().Value == "let" || p.peek().Value == "const") {
		// var/let/const 声明
		kw := p.next()
		decl = &ast.VarDecl{Kind: kw.Value, Loc: posOf(kw)}

		// Detect destructuring pattern or simple identifier.
		var vd ast.VarDeclarator
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "[" {
			pat, err := p.parseArrayPattern()
			if err != nil {
				return nil, err
			}
			vd.Pattern = pat
		} else if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
			pat, err := p.parseObjectPattern()
			if err != nil {
				return nil, err
			}
			vd.Pattern = pat
		} else {
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, err
			}
			vd.Name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
		}
		// 检查 for-in / for-of
		if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "in" {
			p.next()
			decl.Decls = append(decl.Decls, vd)
			leftNode = decl
			initIsVarDecl = true
			return p.parseForIn(leftNode, initIsVarDecl, t)
		}
		if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "of" {
			p.next()
			decl.Decls = append(decl.Decls, vd)
			leftNode = decl
			initIsVarDecl = true
			return p.parseForOf(leftNode, t, isForAwait)
		}
		// 普通 for：var x = init, y = ...
		if p.matchPunct("=") {
			expr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			vd.Init = expr
		}
		decl.Decls = append(decl.Decls, vd)
		for p.matchPunct(",") {
			vd2, err := p.parseVarDeclarator()
			if err != nil {
				return nil, err
			}
			decl.Decls = append(decl.Decls, vd2)
		}
		leftNode = decl
		initIsVarDecl = true
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
	} else {
		// 表达式 init 或 for-in/of 左值。for-init 为 Expression[+In=false]，
		// 需禁用 `in` 二元操作符，否则 `for (k in obj)` 会被误解析为 `k in obj`。
		p.noIn++
		expr, err := p.parseExpression()
		p.noIn--
		if err != nil {
			return nil, err
		}
		// for-in / for-of
		if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "in" {
			p.next()
			return p.parseForIn(expr, false, t)
		}
		if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "of" {
			p.next()
			return p.parseForOf(expr, t, isForAwait)
		}
		leftNode = expr
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
	}

	// 普通 for：test ; update
	var test ast.Expression
	if !(p.peek().Type == lexer.TokenPunct && p.peek().Value == ";") {
		var err error
		test, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expectPunct(";"); err != nil {
		return nil, err
	}
	var update ast.Expression
	if !(p.peek().Type == lexer.TokenPunct && p.peek().Value == ")") {
		var err error
		update, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return &ast.ForStmt{Init: leftNode, Test: test, Update: update, Body: body, Loc: posOf(t)}, nil
}

func (p *Parser) parseForIn(left ast.Node, isVarDecl bool, forTok lexer.Token) (*ast.ForInStmt, error) {
	right, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return &ast.ForInStmt{Left: left, Right: right, Body: body, Loc: posOf(forTok)}, nil
}

func (p *Parser) parseForOf(left ast.Node, forTok lexer.Token, isAwait bool) (*ast.ForOfStmt, error) {
	right, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return &ast.ForOfStmt{Left: left, Right: right, Body: body, IsAwait: isAwait, Loc: posOf(forTok)}, nil
}

func (p *Parser) parseReturn() (*ast.ReturnStmt, error) {
	t := p.next()
	var arg ast.Expression
	// ASI：return 后若换行或 ; 或 } 则裸 return
	if p.peek().Type == lexer.TokenPunct && (p.peek().Value == ";" || p.peek().Value == "}") {
		_ = p.matchPunct(";")
		return &ast.ReturnStmt{Loc: posOf(t)}, nil
	}
	if p.peek().Type == lexer.TokenEOF {
		return &ast.ReturnStmt{Loc: posOf(t)}, nil
	}
	// 行号变化也视为裸 return
	if p.peek().Line != t.Line {
		return &ast.ReturnStmt{Loc: posOf(t)}, nil
	}
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	arg = expr
	_ = p.consumeSemicolon()
	return &ast.ReturnStmt{Arg: arg, Loc: posOf(t)}, nil
}

func (p *Parser) parseBreakContinue(isBreak bool) (ast.Statement, error) {
	t := p.next()
	// 可选 label
	if p.peek().Type == lexer.TokenIdent && p.peek().Line == t.Line {
		labelTok := p.next()
		if isBreak {
			return &ast.BreakStmt{Label: labelTok.Value, Loc: posOf(t)}, nil
		}
		return &ast.ContinueStmt{Label: labelTok.Value, Loc: posOf(t)}, nil
	}
	_ = p.consumeSemicolon()
	if isBreak {
		return &ast.BreakStmt{Loc: posOf(t)}, nil
	}
	return &ast.ContinueStmt{Loc: posOf(t)}, nil
}

func (p *Parser) parseThrow() (*ast.ThrowStmt, error) {
	t := p.next()
	if p.peek().Line != t.Line {
		return nil, p.errorf(p.peek(), "Illegal newline after throw")
	}
	arg, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	_ = p.consumeSemicolon()
	return &ast.ThrowStmt{Arg: arg, Loc: posOf(t)}, nil
}

func (p *Parser) parseTry() (*ast.TryStmt, error) {
	t := p.next()
	block, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	stmt := &ast.TryStmt{Block: block, Loc: posOf(t)}
	if p.matchKeyword("catch") {
		handler := &ast.CatchHandler{Loc: posOf(p.peek())}
		if p.matchPunct("(") {
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, err
			}
			handler.Param = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
		}
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		handler.Body = body
		stmt.Handler = handler
	}
	if p.matchKeyword("finally") {
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		stmt.Finally = body
	}
	return stmt, nil
}

func (p *Parser) parseSwitch() (*ast.SwitchStmt, error) {
	t := p.next()
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	disc, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	stmt := &ast.SwitchStmt{Disc: disc, Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated switch")
		}
		caseTok := p.peek()
		var test ast.Expression
		if p.matchKeyword("case") {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			test = expr
		} else if p.matchKeyword("default") {
			test = nil
		} else {
			return nil, p.errorf(caseTok, "expected 'case' or 'default'")
		}
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		c := ast.SwitchCase{Test: test, Loc: posOf(caseTok)}
		for !(p.peek().Type == lexer.TokenKeyword && (p.peek().Value == "case" || p.peek().Value == "default")) &&
			!(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
			if p.peek().Type == lexer.TokenEOF {
				break
			}
			if p.matchPunct(";") {
				continue
			}
			s, err := p.parseStatement()
			if err != nil {
				return nil, err
			}
			c.Consequent = append(c.Consequent, s)
		}
		stmt.Cases = append(stmt.Cases, c)
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *Parser) parseExprStmt() (*ast.ExprStmt, error) {
	t := p.peek()
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	_ = p.consumeSemicolon()
	return &ast.ExprStmt{Expr: expr, Loc: posOf(t)}, nil
}

// === 表达式解析 ==========================================================

// Pratt 运算符优先级表。
var binaryPrec = map[string]int{
	"||": 1, "??": 1, "&&": 2,
	"|": 3, "^": 4, "&": 5,
	"==": 6, "!=": 6, "===": 6, "!==": 6,
	"<": 7, "<=": 7, ">": 7, ">=": 7, "in": 7, "instanceof": 7,
	"<<": 8, ">>": 8, ">>>": 8,
	"+": 9, "-": 9,
	"*": 10, "/": 10, "%": 10,
	"**": 11,
}

// 赋值运算符集合。
var assignOps = map[string]bool{
	"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
	"**=": true, "<<=": true, ">>=": true, ">>>=": true,
	"&=": true, "|=": true, "^=": true,
	// 逻辑赋值运算符（ES2021）。
	"||=": true, "&&=": true, "??=": true,
}

func (p *Parser) parseExpression() (ast.Expression, error) {
	// 顶层表达式：逗号序列
	first, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if !p.matchPunct(",") {
		return first, nil
	}
	seq := &ast.SequenceExpr{Expressions: []ast.Expression{first}, Loc: first.Pos()}
	for {
		next, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		seq.Expressions = append(seq.Expressions, next)
		if !p.matchPunct(",") {
			break
		}
	}
	return seq, nil
}

func (p *Parser) parseAssignment() (ast.Expression, error) {
	// `yield` is only valid inside a generator function body.
	if len(p.genStack) > 0 && p.genStack[len(p.genStack)-1] {
		t := p.peek()
		if t.Type == lexer.TokenKeyword && t.Value == "yield" {
			return p.parseYield()
		}
	}
	// `await` is handled in parseUnary (as a unary operator) so that binary
	// operators after it (e.g. `await x + 1`) parse correctly as `(await x) + 1`.
	// 检测箭头函数：x => ... 或 (x, y) => ...
	if expr, ok, err := p.tryParseArrow(); ok {
		return expr, err
	}
	left, err := p.parseConditional()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.Type == lexer.TokenPunct && assignOps[t.Value] {
		p.next()
		right, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		return &ast.AssignExpr{Op: t.Value, Left: left, Right: right, Loc: posOf(t)}, nil
	}
	return left, nil
}

// parseYield parses a `yield` expression. Must only be called when inside a
// generator function. Forms:
//
//	yield            -> bare yield (argument is undefined)
//	yield expr       -> yield the value of expr
//	yield* expr      -> delegate: iterate expr's iterator and yield each value
func (p *Parser) parseYield() (ast.Expression, error) {
	t := p.next() // consume `yield`
	y := &ast.YieldExpr{Loc: posOf(t)}
	// `yield*` — delegate
	if p.matchPunct("*") {
		y.Delegate = true
		arg, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		y.Argument = arg
		return y, nil
	}
	// Bare `yield` with no operand: only when the next token cannot start an
	// expression (e.g. `;`, `)`, `}`, `,`, end of input, or a binary operator).
	// Otherwise, parse the operand as an assignment expression.
	if p.yieldHasOperand() {
		arg, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		y.Argument = arg
	}
	return y, nil
}

// yieldHasOperand reports whether the upcoming token can start an expression
// (and thus belongs to the `yield` operand rather than terminating it).
func (p *Parser) yieldHasOperand() bool {
	t := p.peek()
	if t.Type == lexer.TokenEOF {
		return false
	}
	if t.Type == lexer.TokenPunct {
		switch t.Value {
		case ";", ")", "}", ",", "]", ":", "?":
			return false
		}
	}
	return true
}

// parseAwait parses an `await expr` expression. Must only be called when
// inside an async function body. The operand is parsed as a unary expression
// so that `await a + b` is parsed as `(await a) + b`, matching JS semantics.
func (p *Parser) parseAwait() (ast.Expression, error) {
	t := p.next() // consume `await`
	// `await` requires an operand in practice; parse it as a unary expression
	// so `await a.b()` binds correctly. We use parseUnary to avoid grabbing
	// binary operators that should apply to the whole await expression.
	arg, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	return &ast.AwaitExpr{Argument: arg, Loc: posOf(t)}, nil
}

// tryParseAsyncArrow attempts to parse `async x => ...` or `async (...) => ...`.
// On success returns (expr, true, nil). If the tokens after `async` don't form
// an arrow, it backtracks and returns (nil, false, nil) so the caller can
// treat `async` as a regular identifier.
func (p *Parser) tryParseAsyncArrow() (ast.Expression, bool, error) {
	start := p.pos
	asyncTok := p.next() // consume `async`

	// `async x => ...`
	t := p.peek()
	if t.Type == lexer.TokenIdent {
		next := p.peekAt(1)
		if next.Type == lexer.TokenPunct && next.Value == "=>" {
			p.next() // ident
			p.next() // =>
			expr, ok, err := p.parseArrowBody(
				[]*ast.Identifier{{Name: t.Value, Loc: posOf(t)}}, nil, nil, nil)
			if err != nil || !ok {
				return nil, false, err
			}
			if arrow, ok := expr.(*ast.ArrowFunc); ok {
				arrow.IsAsync = true
				arrow.Loc = posOf(asyncTok)
			}
			return expr, true, err
		}
	}

	// `async (...) => ...`
	if t.Type == lexer.TokenPunct && t.Value == "(" {
		if endIdx, ok := p.findMatchingParen(p.pos); ok {
			afterParen := p.peekAt(endIdx - p.pos + 1)
			if afterParen.Type == lexer.TokenPunct && afterParen.Value == "=>" {
				// It's an async arrow with parens. Reuse parseArrowWithParens
				// but we've already consumed `async`; that's fine because
				// parseArrowWithParens starts at `(`.
				p.asyncStack = append(p.asyncStack, true)
				expr, ok, err := p.parseArrowWithParens()
				p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
				if err != nil {
					return nil, true, err
				}
				if ok {
					if arrow, ok := expr.(*ast.ArrowFunc); ok {
						arrow.IsAsync = true
						arrow.Loc = posOf(asyncTok)
					}
					return expr, true, nil
				}
			}
		}
	}

	// Not an async arrow — backtrack and let the caller treat `async` as an
	// identifier.
	p.pos = start
	return nil, false, nil
}

// tryParseArrow 尝试解析箭头函数。
// 成功：返回 (expr, true, nil)。
// 失败但非错误：返回 (nil, false, nil)，调用方继续其他解析。
// 失败且为错误：返回 (nil, false, err)。
func (p *Parser) tryParseArrow() (ast.Expression, bool, error) {
	start := p.pos
	t := p.peek()

	// 单参数无括号：x => ...
	if t.Type == lexer.TokenIdent {
		next := p.peekAt(1)
		if next.Type == lexer.TokenPunct && next.Value == "=>" {
			p.next() // ident
			p.next() // =>
			return p.parseArrowBody([]*ast.Identifier{{Name: t.Value, Loc: posOf(t)}}, nil, nil, nil)
		}
	}

	// 多参数或空参数：(x, y) => ... 或 () => ...
	if t.Type == lexer.TokenPunct && t.Value == "(" {
		// 探测是否有匹配的 ) 后跟 =>（允许中间有返回类型注解 `: T`）。
		if endIdx, ok := p.findMatchingParen(p.pos); ok {
			if p.arrowAfterParen(endIdx) {
				return p.parseArrowWithParens()
			}
		}
	}
	_ = start
	return nil, false, nil
}

// arrowAfterParen 判断匹配的右括号 endIdx 之后是否为箭头函数：
// `=>` 直接跟随，或 `: T =>`（返回类型注解，TS）。扫描类型注解时
// 遇到语句边界（; , = { 等）提前判定非箭头。
func (p *Parser) arrowAfterParen(endIdx int) bool {
	idx := endIdx + 1
	if idx < len(p.tokens) && p.tokens[idx].Type == lexer.TokenPunct && p.tokens[idx].Value == "=>" {
		return true
	}
	if idx >= len(p.tokens) || p.tokens[idx].Type != lexer.TokenPunct || p.tokens[idx].Value != ":" {
		return false
	}
	depth := 0
	for j := idx + 1; j < len(p.tokens); j++ {
		tk := p.tokens[j]
		if tk.Type != lexer.TokenPunct {
			continue
		}
		switch tk.Value {
		case "{", "(", "[", "<":
			depth++
		case "}", ")", "]", ">":
			if depth > 0 {
				depth--
			}
		case "=>":
			if depth == 0 {
				return true
			}
		case ";", ",", "=":
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

// findMatchingParen 找到匹配的右括号位置。
func (p *Parser) findMatchingParen(openIdx int) (int, bool) {
	depth := 0
	for i := openIdx; i < len(p.tokens); i++ {
		t := p.tokens[i]
		if t.Type != lexer.TokenPunct {
			continue
		}
		switch t.Value {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return i, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

func (p *Parser) parseArrowWithParens() (ast.Expression, bool, error) {
	startTok := p.peek()
	p.next() // (
	var params []*ast.Identifier
	var patterns []ast.Pattern
	var defaults []ast.Expression
	var rest *ast.Identifier
	if !(p.peek().Type == lexer.TokenPunct && p.peek().Value == ")") {
		for {
			// ES2015 rest parameter: `...name`
			if p.matchPunct("...") {
				nameTok, err := p.expect(lexer.TokenIdent, "")
				if err != nil {
					return nil, true, err
				}
				rest = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
				// TypeScript: rest param type annotation `: T[]`
				if err := p.parseTypeAnnotation(); err != nil {
					return nil, true, err
				}
				break // rest must be last
			}
			// 解构参数：({a, b}, [x]) => ...
			if p.peek().Type == lexer.TokenPunct && (p.peek().Value == "{" || p.peek().Value == "[") {
				pat, err := p.parsePatternTarget()
				if err != nil {
					return nil, true, err
				}
				params = append(params, nil)
				patterns = append(patterns, pat)
				// 模式参数类型注解 `: T`。
				if err := p.parseTypeAnnotation(); err != nil {
					return nil, true, err
				}
				if p.matchPunct("=") {
					def, err := p.parseAssignment()
					if err != nil {
						return nil, true, err
					}
					defaults = append(defaults, def)
				} else {
					defaults = append(defaults, nil)
				}
				if !p.matchPunct(",") {
					break
				}
				// 尾部逗号：(a, b,) 合法（ES2017 trailing comma）。
				if p.peek().Type == lexer.TokenPunct && p.peek().Value == ")" {
					break
				}
				continue
			}
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, true, err
			}
			params = append(params, &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)})
			patterns = append(patterns, nil)
			// TypeScript: optional `?` marker and type annotation.
			if p.matchPunct("?") {
				// optional parameter marker
			}
			if err := p.parseTypeAnnotation(); err != nil {
				return nil, true, err
			}
			// ES2015 default value: `name = expr`
			if p.matchPunct("=") {
				def, err := p.parseAssignment()
				if err != nil {
					return nil, true, err
				}
				defaults = append(defaults, def)
			} else {
				defaults = append(defaults, nil)
			}
			if !p.matchPunct(",") {
				break
			}
			// 尾部逗号：(a, b,) 合法（ES2017 trailing comma）。
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == ")" {
				break
			}
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, true, err
	}
	// TypeScript: optional return type annotation `: T` before `=>`.
	if err := p.parseTypeAnnotation(); err != nil {
		return nil, true, err
	}
	if err := p.expectPunct("=>"); err != nil {
		return nil, true, err
	}
	expr, ok, err := p.parseArrowBody(params, patterns, defaults, rest)
	if err != nil {
		return nil, true, err
	}
	_ = startTok
	_ = ok
	return expr, true, nil
}

func (p *Parser) parseArrowBody(params []*ast.Identifier, patterns []ast.Pattern, defaults []ast.Expression, rest *ast.Identifier) (ast.Expression, bool, error) {
	t := p.peek()
	// 简洁体：单个表达式
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
		// 块体
		block, err := p.parseBlock()
		if err != nil {
			return nil, true, err
		}
		return &ast.ArrowFunc{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: block, Loc: posOf(t)}, true, nil
	}
	expr, err := p.parseAssignment()
	if err != nil {
		return nil, true, err
	}
	return &ast.ArrowFunc{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: expr, Loc: posOf(t)}, true, nil
}

func (p *Parser) parseConditional() (ast.Expression, error) {
	test, err := p.parseBinary(0)
	if err != nil {
		return nil, err
	}
	// TypeScript: `expr as T` / `expr as const` / `expr satisfies T` — strip
	// the type assertion, keeping only the expression.
	for {
		t := p.peek()
		if t.Type == lexer.TokenIdent && (t.Value == "as" || t.Value == "satisfies") {
			p.next()
			if err := p.skipType(); err != nil {
				return nil, err
			}
			continue
		}
		break
	}
	if !p.matchPunct("?") {
		return test, nil
	}
	cons, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(":"); err != nil {
		return nil, err
	}
	alt, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	return &ast.ConditionalExpr{Test: test, Consequent: cons, Alternate: alt, Loc: test.Pos()}, nil
}

func (p *Parser) parseBinary(minPrec int) (ast.Expression, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		var op string
		var isLogical bool
		if t.Type == lexer.TokenPunct {
			op = t.Value
		} else if t.Type == lexer.TokenKeyword && (t.Value == "in" || t.Value == "instanceof") {
			// The relational `in` operator is disabled while parsing a for-init
			// (noIn > 0), matching ECMAScript's Expression[+In=false].
			if t.Value == "in" && p.noIn > 0 {
				break
			}
			op = t.Value
		} else {
			break
		}
		prec, ok := binaryPrec[op]
		if !ok || prec < minPrec {
			break
		}
		p.next()
		right, err := p.parseBinary(prec + 1)
		if err != nil {
			return nil, err
		}
		if op == "&&" || op == "||" || op == "??" {
			left = &ast.LogicalExpr{Op: op, Left: left, Right: right, Loc: posOf(t)}
			isLogical = true
		} else {
			left = &ast.BinaryExpr{Op: op, Left: left, Right: right, Loc: posOf(t)}
		}
		_ = isLogical
	}
	return left, nil
}

func (p *Parser) parseUnary() (ast.Expression, error) {
	t := p.peek()
	if t.Type == lexer.TokenPunct {
		switch t.Value {
		case "!", "+", "-", "~":
			p.next()
			arg, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return &ast.UnaryExpr{Op: t.Value, Arg: arg, Loc: posOf(t)}, nil
		case "++", "--":
			p.next()
			arg, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return &ast.UpdateExpr{Op: t.Value, Arg: arg, Prefix: true, Loc: posOf(t)}, nil
		}
	}
	if t.Type == lexer.TokenKeyword {
		switch t.Value {
		case "typeof", "void", "delete":
			p.next()
			arg, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return &ast.UnaryExpr{Op: t.Value, Arg: arg, Loc: posOf(t)}, nil
		case "await":
			// `await` is a unary operator only valid inside async functions;
			// 模块顶层（TLA）同样允许（ParseModule 设置 allowTopLevelAwait）。
			inAsync := len(p.asyncStack) > 0 && p.asyncStack[len(p.asyncStack)-1]
			topLevelModule := p.allowTopLevelAwait && len(p.asyncStack) == 0
			if inAsync || topLevelModule {
				return p.parseAwait()
			}
		}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (ast.Expression, error) {
	expr, err := p.parseCallMember()
	if err != nil {
		return nil, err
	}
	// 后缀 ++ --
	t := p.peek()
	if t.Type == lexer.TokenPunct && (t.Value == "++" || t.Value == "--") {
		p.next()
		return &ast.UpdateExpr{Op: t.Value, Arg: expr, Prefix: false, Loc: posOf(t)}, nil
	}
	return expr, nil
}

// parseMemberTail parses trailing .prop and [expr] accessors on an existing
// expression. It stops at call parens "(" or any other non-member token.
// Returns the (possibly extended) expression; if no accessors are consumed,
// the input expression is returned unchanged.
func (p *Parser) parseMemberTail(expr ast.Expression) (ast.Expression, error) {
	for {
		t := p.peek()
		if t.Type != lexer.TokenPunct {
			return expr, nil
		}
		switch t.Value {
		case ".":
			p.next()
			propTok := p.next()
			if propTok.Type != lexer.TokenIdent && propTok.Type != lexer.TokenKeyword {
				return nil, p.errorf(propTok, "expected property name after '.'")
			}
			expr = &ast.MemberExpr{
				Object:   expr,
				Property: &ast.Identifier{Name: propTok.Value, Loc: posOf(propTok)},
				Computed: false,
				Loc:      posOf(t),
			}
		case "[":
			p.next()
			propExpr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			expr = &ast.MemberExpr{Object: expr, Property: propExpr, Computed: true, Loc: posOf(t)}
		default:
			return expr, nil
		}
	}
}

func (p *Parser) parseCallMember() (ast.Expression, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		// TypeScript: skip generic type arguments before a call, e.g.
		// `foo<T>(arg)` or `obj.method<T>(arg)`. Backtrack if the `<...>`
		// isn't followed by `(` (it might be a less-than comparison).
		p.trySkipTypeArgs()
		t := p.peek()
		if t.Type == lexer.TokenPunct && t.Value == "(" {
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			expr = &ast.CallExpr{Callee: expr, Arguments: args, Loc: posOf(t)}
			continue
		}
		// Optional chaining: ?.name, ?.[expr], ?.(args)
		if t.Type == lexer.TokenPunct && t.Value == "?." {
			p.next() // consume '?.'
			next := p.peek()
			if next.Type == lexer.TokenPunct && next.Value == "(" {
				// Optional call: a?.()
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				expr = &ast.CallExpr{Callee: expr, Arguments: args, Optional: true, Loc: posOf(t)}
				continue
			}
			if next.Type == lexer.TokenPunct && next.Value == "[" {
				// Optional computed member: a?.[expr]
				p.next() // consume '['
				propExpr, err := p.parseExpression()
				if err != nil {
					return nil, err
				}
				if err := p.expectPunct("]"); err != nil {
					return nil, err
				}
				expr = &ast.MemberExpr{Object: expr, Property: propExpr, Computed: true, Optional: true, Loc: posOf(t)}
				continue
			}
			// Optional member: a?.name
			propTok := p.next()
			if propTok.Type != lexer.TokenIdent && propTok.Type != lexer.TokenKeyword {
				return nil, p.errorf(propTok, "expected property name after '?.'")
			}
			expr = &ast.MemberExpr{
				Object:   expr,
				Property: &ast.Identifier{Name: propTok.Value, Loc: posOf(propTok)},
				Computed: false,
				Optional: true,
				Loc:      posOf(t),
			}
			continue
		}
		// Tagged template: tag`a${x}b`（tag 可为标识符/成员访问等任意表达式）。
		if t.Type == lexer.TokenTemplate {
			p.next()
			tmpl, err := p.parseTemplateLit(t.Value, t.Raw, posOf(t))
			if err != nil {
				return nil, err
			}
			expr = &ast.TaggedTemplateExpr{Tag: expr, Template: tmpl, Loc: posOf(t)}
			continue
		}
		prev := expr
		expr, err = p.parseMemberTail(expr)
		if err != nil {
			return nil, err
		}
		if expr == prev {
			// No member access consumed and no call pending; stop.
			return expr, nil
		}
	}
}

func (p *Parser) parseArgs() ([]ast.Expression, error) {
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	var args []ast.Expression
	if !(p.peek().Type == lexer.TokenPunct && p.peek().Value == ")") {
		for {
			if p.matchPunct("...") {
				expr, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				args = append(args, &ast.SpreadElement{Arg: expr, Loc: posOf(p.peek())})
			} else {
				expr, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				args = append(args, expr)
			}
			if !p.matchPunct(",") {
				break
			}
			// 尾部逗号：f(a,) / f(a,\n) 合法（ES2017 trailing comma）。
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == ")" {
				break
			}
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return args, nil
}

// parseTemplateLit splits the raw/cooked template strings (produced by the
// lexer, which preserves ${...} segments verbatim) into alternating string
// quasis and interpolated expressions.
//
// 插值边界在 raw 文本上检测：cooked 文本中 `\${`、`\u0024{`、`\x24{` 等转义
// 会伪产生 `${`（转义被处理成字面 `$`），若按 cooked 拆分会导致伪插值边界。
// 因此对 raw 与 cooked 并行扫描：raw 定位 `${` 与转义边界，cooked 按对应
// 长度推进取出已转义的 quasi。
func (p *Parser) parseTemplateLit(cooked, raw string, loc ast.Pos) (*ast.TemplateLit, error) {
	var quasis, rawQuasis []string
	var exprs []ast.Expression
	var quasi, rawQuasi strings.Builder

	i, j := 0, 0 // i 在 raw，j 在 cooked
	for i < len(raw) {
		// Detect ${...} interpolation on the raw text.
		if raw[i] == '$' && i+1 < len(raw) && raw[i+1] == '{' {
			quasis = append(quasis, quasi.String())
			rawQuasis = append(rawQuasis, rawQuasi.String())
			quasi.Reset()
			rawQuasi.Reset()
			// 用 lexer.SkipTemplateExpr 正确配对大括号（跳过字符串/注释/嵌套模板）。
			end, ok := lexer.SkipTemplateExpr(raw, i)
			if !ok {
				return nil, fmt.Errorf("template literal: unbalanced ${ at line %d", loc.Line)
			}
			exprSrc := raw[i+2 : end-1]
			// Re-parse the expression source using a fresh parser.
			sub, err := NewFromString(exprSrc)
			if err != nil {
				return nil, err
			}
			expr, err := sub.parseExpression()
			if err != nil {
				return nil, fmt.Errorf("template literal expression %q: %w", exprSrc, err)
			}
			exprs = append(exprs, expr)
			// ${...} 段在 cooked 与 raw 中逐字相同。
			segLen := end - i
			if j+segLen > len(cooked) {
				return nil, fmt.Errorf("template literal: internal alignment error at line %d", loc.Line)
			}
			i = end
			j += segLen
			continue
		}
		if raw[i] == '\\' {
			n, cookEmpty := templateEscapeLen(raw, i)
			rawQuasi.WriteString(raw[i : i+n])
			if !cookEmpty {
				if j >= len(cooked) {
					return nil, fmt.Errorf("template literal: internal alignment error at line %d", loc.Line)
				}
				quasi.WriteByte(cooked[j])
				j++
			}
			i += n
			continue
		}
		if j >= len(cooked) {
			return nil, fmt.Errorf("template literal: internal alignment error at line %d", loc.Line)
		}
		rawQuasi.WriteByte(raw[i])
		quasi.WriteByte(cooked[j])
		i++
		j++
	}
	quasis = append(quasis, quasi.String())
	rawQuasis = append(rawQuasis, rawQuasi.String())
	return &ast.TemplateLit{Quasis: quasis, RawQuasis: rawQuasis, Expressions: exprs, Loc: loc}, nil
}

// templateEscapeLen 返回 raw 文本 raw[i]=='\\' 处转义序列的长度（含 `\`），
// 以及该转义在 cooked 文本中是否贡献 0 个字符（`\`+换行的行连接）。其余转义
// 在 cooked 中贡献 1 个字符。与 lexer.readEscape 的处理保持一致。
func templateEscapeLen(raw string, i int) (n int, cookEmpty bool) {
	if i+1 >= len(raw) {
		return 1, false
	}
	switch raw[i+1] {
	case 'x':
		return 4, false // \xNN
	case 'u':
		if i+2 < len(raw) && raw[i+2] == '{' {
			j := i + 3
			for j < len(raw) && raw[j] != '}' {
				j++
			}
			if j < len(raw) {
				j++
			}
			return j - i, false // \u{...}
		}
		return 6, false // \uNNNN
	case '\n':
		return 2, true // 行连接：cooked 贡献 0 字符
	case '\r':
		return 2, false // \r 为字面 CR（readEscape 返回 "\r"）
	}
	return 2, false // 单字符转义
}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	t := p.peek()
	switch t.Type {
	case lexer.TokenNumber:
		p.next()
		val, _ := parseNumberLiteral(t.Value)
		return &ast.NumberLit{Value: val, Raw: t.Value, Loc: posOf(t)}, nil
	case lexer.TokenBigInt:
		p.next()
		// BigInt 字面量：解析为十进制整数字符串（去掉进制前缀与下划线）。
		dec := bigIntLiteralToDecimal(t.Value)
		return &ast.BigIntLit{Text: dec, Loc: posOf(t)}, nil
	case lexer.TokenString:
		p.next()
		return &ast.StringLit{Value: t.Value, Loc: posOf(t)}, nil
	case lexer.TokenTemplate:
		p.next()
		return p.parseTemplateLit(t.Value, t.Raw, posOf(t))
	case lexer.TokenRegex:
		p.next()
		return &ast.RegexLit{Pattern: t.Value, Flags: t.RegexFlags, Loc: posOf(t)}, nil
	case lexer.TokenIdent:
		p.next()
		return &ast.Identifier{Name: t.Value, Loc: posOf(t)}, nil
	case lexer.TokenKeyword:
		switch t.Value {
		case "true":
			p.next()
			return &ast.BoolLit{Value: true, Loc: posOf(t)}, nil
		case "false":
			p.next()
			return &ast.BoolLit{Value: false, Loc: posOf(t)}, nil
		case "null":
			p.next()
			return &ast.NullLit{Loc: posOf(t)}, nil
		case "undefined":
			p.next()
			return &ast.UndefinedLit{Loc: posOf(t)}, nil
		case "this":
			p.next()
			return &ast.ThisExpr{Loc: posOf(t)}, nil
		case "super":
			p.next()
			return &ast.SuperExpr{Loc: posOf(t)}, nil
		case "function":
			return p.parseFunctionExpr()
		case "async":
			// `async function` expression, or `async () =>` / `async x =>` arrow.
			next := p.peekAt(1)
			if next.Type == lexer.TokenKeyword && next.Value == "function" {
				return p.parseFunctionExpr()
			}
			// async arrow: `async x =>` or `async (...) =>`
			if expr, ok, err := p.tryParseAsyncArrow(); ok {
				return expr, err
			} else if err != nil {
				return nil, err
			}
			// Not an async arrow — fall through to treat `async` as identifier.
			p.next()
			return &ast.Identifier{Name: "async", Loc: posOf(t)}, nil
		case "class":
			return p.parseClassExpr()
		case "new":
			return p.parseNew()
		case "import":
			// 动态 import(specifier)：在 parser 层直接 lower 为对内置全局
			// __import 的调用，复用现有 CallExpr 编译链路。__import 由模块
			// 加载器在 setGlobals 时注入，返回 Promise<module exports>。
			// 这样无需新增 AST 节点/opcode/compiler 分支。
			// 第二参数为 import attributes：import(x, { with: { type: 'json' } })。
			p.next() // 消费 import 关键字
			if err := p.expectPunct("("); err != nil {
				return nil, err
			}
			spec, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			args := []ast.Expression{spec}
			if p.matchPunct(",") {
				opts, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				args = append(args, opts)
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return &ast.CallExpr{
				Callee:    &ast.Identifier{Name: "__import", Loc: posOf(t)},
				Arguments: args,
				Loc:       posOf(t),
			}, nil
		}
	case lexer.TokenPunct:
		switch t.Value {
		case "(":
			return p.parseParenOrSequence()
		case "[":
			return p.parseArrayLit()
		case "{":
			return p.parseObjectLit()
		}
	}
	return nil, p.errorf(t, "unexpected token %q", t.Value)
}

func (p *Parser) parseFunctionExpr() (ast.Expression, error) {
	// Detect `async function` expression.
	isAsync := false
	if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "async" {
		p.next() // consume async
		isAsync = true
	}
	t := p.next() // function
	isGenerator := p.matchPunct("*")
	var name *ast.Identifier
	if p.peek().Type == lexer.TokenIdent {
		nameTok := p.next()
		name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	}
	p.genStack = append(p.genStack, isGenerator)
	p.asyncStack = append(p.asyncStack, isAsync)
	params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
	p.genStack = p.genStack[:len(p.genStack)-1]
	p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
	if err != nil {
		return nil, err
	}
	return &ast.FunctionExpr{Name: name, Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: body, IsAsync: isAsync, IsGenerator: isGenerator, Loc: posOf(t)}, nil
}

// === ESM import/export parsing ===========================================

// parseImportDecl parses an ESM import declaration.
//
//	import 'mod'
//	import x from 'mod'
//	import * as ns from 'mod'
//	import {a, b as c} from 'mod'
//	import x, {a, b} from 'mod'
//	import x, * as ns from 'mod'
func (p *Parser) parseImportDecl() (ast.Statement, error) {
	t := p.next() // consume 'import'

	// TypeScript: `import type ...` — the entire import is type-only and
	// should be erased. Parse and discard it, returning an EmptyStmt.
	if p.peek().Type == lexer.TokenIdent && p.peek().Value == "type" {
		// `import type x from 'mod'` / `import type { a } from 'mod'` /
		// `import type * as ns from 'mod'`
		p.next() // consume 'type'
		// Parse the rest as a normal import, then discard.
		tmp, err := p.parseImportDeclRest(t)
		if err != nil {
			return nil, err
		}
		_ = tmp
		return &ast.EmptyStmt{Loc: posOf(t)}, nil
	}

	return p.parseImportDeclRest(t)
}

// parseImportDeclRest parses the specifier list + `from 'mod'` part of an
// import declaration (after the leading `import` [and optional `type`] has
// been consumed).
func (p *Parser) parseImportDeclRest(t lexer.Token) (*ast.ImportDecl, error) {
	decl := &ast.ImportDecl{Loc: posOf(t)}

	// Side-effect-only import: import 'mod'
	if p.peek().Type == lexer.TokenString {
		s, err := p.parseStringLiteral()
		if err != nil {
			return nil, err
		}
		decl.Source = s
		if p.matchKeyword("with") {
			attrs, err := p.parseImportAttributes()
			if err != nil {
				return nil, err
			}
			decl.Attributes = attrs
		}
		if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
		return decl, nil
	}

	// Parse import specifiers
	for {
		// Default import: import x from 'mod'
		if p.peek().Type == lexer.TokenIdent {
			nameTok := p.next()
			decl.Specifiers = append(decl.Specifiers, ast.ImportSpecifier{
				Imported: "",
				Local:    nameTok.Value,
			})
		} else if p.peek().Type == lexer.TokenPunct && p.peek().Value == "*" {
			// Namespace import: import * as ns from 'mod'
			p.next() // consume '*'
			if !p.matchIdent("as") {
				return nil, p.errorf(p.peek(), "expected 'as' after '*' in import")
			}
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, err
			}
			decl.Specifiers = append(decl.Specifiers, ast.ImportSpecifier{
				Imported: "*",
				Local:    nameTok.Value,
			})
		} else if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
			// Named imports: import {a, b as c} from 'mod'
			// TypeScript: {type a, b, type c as d} — `type` prefix marks
			// type-only specifiers which are erased.
			p.next() // consume '{'
			for {
				// TypeScript: `type` prefix on individual specifiers.
				isTypeSpec := false
				if p.peek().Type == lexer.TokenIdent && p.peek().Value == "type" {
					nx := p.peekAt(1)
					if nx.Type == lexer.TokenIdent || nx.Type == lexer.TokenString {
						isTypeSpec = true
						p.next() // consume 'type'
					}
				}
				nameTok, err := p.expect(lexer.TokenIdent, "")
				if err != nil {
					return nil, err
				}
				spec := ast.ImportSpecifier{Imported: nameTok.Value, Local: nameTok.Value}
				// `as` rename: {a as b}
				if p.matchIdent("as") {
					localTok, err := p.expect(lexer.TokenIdent, "")
					if err != nil {
						return nil, err
					}
					spec.Local = localTok.Value
				}
				// Type-only specifiers are dropped (erased).
				if !isTypeSpec {
					decl.Specifiers = append(decl.Specifiers, spec)
				}
				if !p.matchPunct(",") {
					break
				}
				// 尾部逗号：import { a, b, } 合法（ES2017 trailing comma）。
				if p.peek().Type == lexer.TokenPunct && p.peek().Value == "}" {
					break
				}
			}
			if err := p.expectPunct("}"); err != nil {
				return nil, err
			}
		} else {
			return nil, p.errorf(p.peek(), "unexpected token %q in import", p.peek().Value)
		}

		// Multiple specifier groups separated by comma: import x, {a, b} from 'mod'
		if p.matchPunct(",") {
			continue
		}
		break
	}

	// Expect 'from'
	if !p.matchIdent("from") {
		return nil, p.errorf(p.peek(), "expected 'from' in import declaration")
	}

	// Parse module specifier (string literal)
	s, err := p.parseStringLiteral()
	if err != nil {
		return nil, err
	}
	decl.Source = s
	// Import attributes：import x from 'mod' with { type: 'json' }
	if p.matchKeyword("with") {
		attrs, err := p.parseImportAttributes()
		if err != nil {
			return nil, err
		}
		decl.Attributes = attrs
	}
	if err := p.consumeSemicolon(); err != nil {
		return nil, err
	}
	return decl, nil
}

// parseImportAttributes 解析 import attributes 子句：
// with { type: 'json', key: 'value' }（仅字符串值，键为标识符或字符串）。
func (p *Parser) parseImportAttributes() (map[string]string, error) {
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	attrs := make(map[string]string)
	for {
		keyTok, err := p.expect(lexer.TokenIdent, "")
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		valTok := p.peek()
		if valTok.Type != lexer.TokenString {
			return nil, p.errorf(valTok, "expected string value in import attributes")
		}
		p.next()
		attrs[keyTok.Value] = valTok.Value
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return attrs, nil
}

// parseStringLiteral parses a string literal token and returns its value.
func (p *Parser) parseStringLiteral() (string, error) {
	t := p.peek()
	if t.Type != lexer.TokenString {
		return "", p.errorf(t, "expected string literal but got %q", t.Value)
	}
	p.next()
	// The lexer stores the raw value (without quotes) in t.Value.
	return t.Value, nil
}

// parseExportDecl parses an ESM export declaration.
//
//	export var x = 1
//	export function f() {}
//	export class C {}
//	export {a, b as c}
//	export {a, b} from 'mod'
//	export * from 'mod'
//	export default expr
//	export default function f() {}
//	export default class C {}
func (p *Parser) parseExportDecl() (ast.Statement, error) {
	t := p.next() // consume 'export'

	// TypeScript: skip decorators between `export` and the declaration,
	// e.g. `export @dec class C {}`.
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "@" {
		if err := p.skipDecorators(); err != nil {
			return nil, err
		}
	}

	// TypeScript: `export type ...` — type-only export, erase it.
	if p.peek().Type == lexer.TokenIdent && p.peek().Value == "type" {
		p.next() // consume 'type'
		// `export type { a, b as c } [from 'mod']` — parse and discard.
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
			if err := p.skipBalanced("{", "}"); err != nil {
				return nil, err
			}
			if p.matchIdent("from") {
				if _, err := p.parseStringLiteral(); err != nil {
					return nil, err
				}
			}
			if err := p.consumeSemicolon(); err != nil {
				return nil, err
			}
			return &ast.EmptyStmt{Loc: posOf(t)}, nil
		}
		// `export type * from 'mod'` (rare) — discard.
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "*" {
			p.next()
			if p.matchIdent("from") {
				if _, err := p.parseStringLiteral(); err != nil {
					return nil, err
				}
			}
			if err := p.consumeSemicolon(); err != nil {
				return nil, err
			}
			return &ast.EmptyStmt{Loc: posOf(t)}, nil
		}
		// `export type X = ...` — 类型别名声明（含泛型/联合类型等），擦除。
		if p.peek().Type == lexer.TokenIdent {
			if err := p.skipToSemicolon(); err != nil {
				return nil, err
			}
			return &ast.EmptyStmt{Loc: posOf(t)}, nil
		}
		// Anything else after `export type` is invalid; fall through to error.
		return nil, p.errorf(p.peek(), "unexpected token %q after 'export type'", p.peek().Value)
	}

	// export default ...
	if p.matchKeyword("default") {
		expr, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		// For `export default function f() {}` and `export default class C {}`,
		// parseAssignment already handles these as expressions (FunctionExpr/ClassExpr).
		if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
		return &ast.ExportDefaultDecl{Expression: expr, Loc: posOf(t)}, nil
	}

	// export * from 'mod'
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "*" {
		p.next() // consume '*'
		if !p.matchIdent("from") {
			return nil, p.errorf(p.peek(), "expected 'from' after '*' in export")
		}
		src, err := p.parseStringLiteral()
		if err != nil {
			return nil, err
		}
		if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
		return &ast.ExportDecl{IsStar: true, Source: src, Loc: posOf(t)}, nil
	}

	// export {a, b as c} [from 'mod']
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
		p.next() // consume '{'
		decl := &ast.ExportDecl{Loc: posOf(t)}
		for {
			// TypeScript: `type` prefix on individual specifiers — erase them.
			isTypeSpec := false
			if p.peek().Type == lexer.TokenIdent && p.peek().Value == "type" {
				nx := p.peekAt(1)
				if nx.Type == lexer.TokenIdent || nx.Type == lexer.TokenString {
					isTypeSpec = true
					p.next() // consume 'type'
				}
			}
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, err
			}
			spec := ast.ExportSpecifier{Local: nameTok.Value, Exported: nameTok.Value}
			if p.matchIdent("as") {
				exportedTok, err := p.expect(lexer.TokenIdent, "")
				if err != nil {
					return nil, err
				}
				spec.Exported = exportedTok.Value
			}
			// Type-only specifiers are dropped (erased).
			if !isTypeSpec {
				decl.Specifiers = append(decl.Specifiers, spec)
			}
			if !p.matchPunct(",") {
				break
			}
			// 尾部逗号：export { a, b, } 合法（ES2017 trailing comma）。
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "}" {
				break
			}
		}
		if err := p.expectPunct("}"); err != nil {
			return nil, err
		}
		// Optional re-export: export {a, b} from 'mod'
		if p.matchIdent("from") {
			src, err := p.parseStringLiteral()
			if err != nil {
				return nil, err
			}
			decl.Source = src
		}
		if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
		return decl, nil
	}

	// TypeScript 类型声明擦除：export interface / export enum /
	// export namespace / export declare（仅类型层，无运行时产物）。
	if p.peek().Type == lexer.TokenIdent {
		switch p.peek().Value {
		case "interface", "enum", "namespace", "declare":
			p.next()
			if err := p.skipTypeDeclBody(); err != nil {
				return nil, err
			}
			return &ast.EmptyStmt{Loc: posOf(t)}, nil
		}
	}

	// export <declaration>: var/let/const, function, async function, class
	if p.peek().Type == lexer.TokenKeyword {
		switch p.peek().Value {
		case "var", "let", "const":
			vd, err := p.parseVarDecl()
			if err != nil {
				return nil, err
			}
			return &ast.ExportDecl{Declaration: vd, Loc: posOf(t)}, nil
		case "function":
			fd, err := p.parseFunctionDecl(false)
			if err != nil {
				return nil, err
			}
			return &ast.ExportDecl{Declaration: fd, Loc: posOf(t)}, nil
		case "async":
			next := p.peekAt(1)
			if next.Type == lexer.TokenKeyword && next.Value == "function" {
				fd, err := p.parseFunctionDecl(false)
				if err != nil {
					return nil, err
				}
				return &ast.ExportDecl{Declaration: fd, Loc: posOf(t)}, nil
			}
		case "class":
			cd, err := p.parseClassDecl()
			if err != nil {
				return nil, err
			}
			return &ast.ExportDecl{Declaration: cd, Loc: posOf(t)}, nil
		}
	}

	return nil, p.errorf(p.peek(), "unexpected token %q after 'export'", p.peek().Value)
}

// === Class parsing (ES2015) ==============================================

// parseClassDecl parses `class Name [extends Super] { body }` as a statement.
func (p *Parser) parseClassDecl() (ast.Statement, error) {
	t := p.next() // class
	var name *ast.Identifier
	if p.peek().Type == lexer.TokenIdent {
		nameTok := p.next()
		name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	} else {
		return nil, p.errorf(p.peek(), "class declaration requires a name")
	}
	// TypeScript: skip generic type parameters `<T, U>`.
	if err := p.skipTypeParameters(); err != nil {
		return nil, err
	}
	super, body, err := p.parseClassTail()
	if err != nil {
		return nil, err
	}
	return &ast.ClassDecl{Name: name, SuperClass: super, Body: body, Loc: posOf(t)}, nil
}

// parseClassExpr parses `class [Name] [extends Super] { body }` as an expression.
func (p *Parser) parseClassExpr() (ast.Expression, error) {
	t := p.next() // class
	var name *ast.Identifier
	if p.peek().Type == lexer.TokenIdent {
		nameTok := p.next()
		name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	}
	// TypeScript: skip generic type parameters `<T, U>`.
	if err := p.skipTypeParameters(); err != nil {
		return nil, err
	}
	super, body, err := p.parseClassTail()
	if err != nil {
		return nil, err
	}
	return &ast.ClassExpr{Name: name, SuperClass: super, Body: body, Loc: posOf(t)}, nil
}

// parseClassTail parses the optional `extends Super` and the class body `{ ... }`.
func (p *Parser) parseClassTail() (ast.Expression, *ast.ClassBody, error) {
	var super ast.Expression
	if p.matchKeyword("extends") {
		s, err := p.parseCallMember()
		if err != nil {
			return nil, nil, err
		}
		super = s
		// TypeScript: skip `implements I1, I2, ...` clauses.
		if err := p.skipImplementsClause(); err != nil {
			return nil, nil, err
		}
	} else {
		// TypeScript: `implements` can appear without `extends`.
		if err := p.skipImplementsClause(); err != nil {
			return nil, nil, err
		}
	}
	body, err := p.parseClassBody()
	if err != nil {
		return nil, nil, err
	}
	return super, body, nil
}

// skipImplementsClause skips a TypeScript `implements I1, I2, ...` clause.
func (p *Parser) skipImplementsClause() error {
	if p.peek().Type != lexer.TokenIdent || p.peek().Value != "implements" {
		return nil
	}
	p.next() // consume 'implements'
	for {
		// Skip one type reference (with optional generic args).
		if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
			return err
		}
		// Qualified name / generic args
		for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
			p.next()
			if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
				return err
			}
		}
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "<" {
			if err := p.skipAngleBraces(); err != nil {
				return err
			}
		}
		if !p.matchPunct(",") {
			break
		}
	}
	return nil
}

// parseClassBody parses `{ members }` for a class.
func (p *Parser) parseClassBody() (*ast.ClassBody, error) {
	t := p.peek()
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	cb := &ast.ClassBody{Loc: posOf(t)}
	for {
		// Allow stray semicolons between members.
		if p.matchPunct(";") {
			continue
		}
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "}" {
			break
		}
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated class body")
		}
		m, err := p.parseClassMember()
		if err != nil {
			return nil, err
		}
		cb.Methods = append(cb.Methods, m)
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return cb, nil
}

// parseClassMember parses a single class member: an optional `static` prefix,
// then either a `get`/`set` accessor, a `constructor`, or a normal method.
func (p *Parser) parseClassMember() (ast.MethodDefinition, error) {
	// TypeScript: skip leading `@decorator` expressions (parsed and discarded).
	if err := p.skipDecorators(); err != nil {
		var def ast.MethodDefinition
		return def, err
	}
	t := p.peek()
	var def ast.MethodDefinition
	def.Loc = posOf(t)

	// `static` is a contextual keyword: only treat as static when the next
	// token is a valid method-name start (ident/string/number/[).
	isStatic := false
	if t.Type == lexer.TokenIdent && t.Value == "static" {
		nx := p.peekAt(1)
		if nx.Type == lexer.TokenIdent || nx.Type == lexer.TokenString || nx.Type == lexer.TokenNumber ||
			(nx.Type == lexer.TokenPunct && (nx.Value == "[" || nx.Value == "{")) {
			p.next() // consume static
			isStatic = true
		}
	}
	def.Static = isStatic

	// `async` is a contextual keyword for async methods: `async foo() {}`.
	// Only treat as async when followed by a method name (not `(`, which
	// would make `async` a regular method name).
	isAsync := false
	if t2 := p.peek(); t2.Type == lexer.TokenKeyword && t2.Value == "async" {
		nx := p.peekAt(1)
		if nx.Type != lexer.TokenPunct || nx.Value != "(" {
			p.next() // consume async
			isAsync = true
		}
	}

	// get/set accessors are also contextual keywords.
	// 注意：必须用当前 token（static/async 已消费后重新 peek），
	// 不能用 parseClassMember 开头捕获的 t，否则 `static get x()` 会被
	// 误判为以 "get" 命名的普通方法/字段。
	kind := ast.MethodNormal
	computed := false
	var key ast.Expression
	if ct := p.peek(); ct.Type == lexer.TokenIdent && (ct.Value == "get" || ct.Value == "set") {
		nx := p.peekAt(1)
		// Treat as accessor only if a real key follows. `get() {}` is a normal
		// method named "get".
		if nx.Type != lexer.TokenPunct || nx.Value != "(" {
			if ct.Value == "get" {
				kind = ast.MethodGetter
			} else {
				kind = ast.MethodSetter
			}
			p.next() // consume get/set
		}
	}

	// Computed key: [expr]
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "[" {
		p.next() // [
		expr, err := p.parseAssignment()
		if err != nil {
			return def, err
		}
		if err := p.expectPunct("]"); err != nil {
			return def, err
		}
		key = expr
		computed = true
	} else {
		kt := p.next()
		switch kt.Type {
		case lexer.TokenIdent, lexer.TokenKeyword:
			key = &ast.Identifier{Name: kt.Value, Loc: posOf(kt)}
		case lexer.TokenString:
			key = &ast.StringLit{Value: kt.Value, Loc: posOf(kt)}
		case lexer.TokenNumber:
			val, _ := parseNumberLiteral(kt.Value)
			key = &ast.NumberLit{Value: val, Raw: kt.Value, Loc: posOf(kt)}
		default:
			return def, p.errorf(kt, "unexpected token %q in class body", kt.Value)
		}
	}

	// `constructor` (non-static, non-computed, identifier key) is the ctor.
	if !isStatic && !computed && kind == ast.MethodNormal {
		if id, ok := key.(*ast.Identifier); ok && id.Name == "constructor" {
			kind = ast.MethodConstructor
		}
	}

	// TypeScript: optional `?` (optional member) or `!` (definite assignment)
	// markers after the key, before `(` / `:` / `=` / `;`.
	if p.peek().Type == lexer.TokenPunct && (p.peek().Value == "?" || p.peek().Value == "!") {
		p.next() // consume ? or !
	}

	// Distinguish method (`(`) from field declaration (`:` / `=` / `;`).
	if p.peek().Type != lexer.TokenPunct || p.peek().Value != "(" {
		// Class field declaration: `[static] key [: T] [= init] ;`
		// TypeScript: skip optional type annotation.
		if err := p.parseTypeAnnotation(); err != nil {
			return def, err
		}
		var initExpr ast.Expression
		if p.matchPunct("=") {
			init, err := p.parseAssignment()
			if err != nil {
				return def, err
			}
			initExpr = init
		}
		if err := p.consumeSemicolon(); err != nil {
			return def, err
		}
		def.Key = key
		def.Kind = ast.MethodField
		def.Computed = computed
		def.Init = initExpr
		return def, nil
	}

	// Parse the method body using the standard function-params-and-body rule.
	p.asyncStack = append(p.asyncStack, isAsync)
	params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
	p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
	if err != nil {
		return def, err
	}
	fn := &ast.FunctionExpr{
		Params:        params,
		ParamPatterns: patterns,
		Defaults:      defaults,
		RestParam: rest,
		Body:      body,
		IsAsync:   isAsync,
		Loc:       posOf(t),
	}
	def.Key = key
	def.Value = fn
	def.Kind = kind
	def.Computed = computed
	return def, nil
}

func (p *Parser) parseNew() (ast.Expression, error) {
	t := p.next() // new
	// new.target
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
		p.next()
		if _, err := p.expect(lexer.TokenIdent, "target"); err != nil {
			return nil, err
		}
		return &ast.NewTargetExpr{Loc: posOf(t)}, nil
	}
	// Parse the constructor callee: a primary expression followed by
	// member access (`.prop` / `[expr]`). Call parens `(args)` are NOT
	// consumed here — they belong to the NewExpr.Arguments below. This
	// matches JS `new` precedence where `new Foo()` is a constructor call
	// (not `new (Foo())`).
	var callee ast.Expression
	var err error
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
		// Parenthesized callee: new (expr)()
		p.next()
		callee, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
	} else {
		callee, err = p.parsePrimary()
		if err != nil {
			return nil, err
		}
	}
	callee, err = p.parseMemberTail(callee)
	if err != nil {
		return nil, err
	}
	// TypeScript: skip generic type arguments on `new`, e.g. `new Box<T>(v)`.
	p.trySkipTypeArgs()
	var args []ast.Expression
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
		args, err = p.parseArgs()
		if err != nil {
			return nil, err
		}
	}
	return &ast.NewExpr{Callee: callee, Arguments: args, Loc: posOf(t)}, nil
}

func (p *Parser) parseParenOrSequence() (ast.Expression, error) {
	t := p.peek()
	p.next() // (
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	// 若是单表达式则直接返回，否则包为 SequenceExpr
	if seq, ok := expr.(*ast.SequenceExpr); ok {
		return seq, nil
	}
	_ = t
	return expr, nil
}

func (p *Parser) parseArrayLit() (ast.Expression, error) {
	t := p.peek()
	p.next() // [
	arr := &ast.ArrayLit{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "]") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated array literal")
		}
		// hole
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "," {
			arr.Elements = append(arr.Elements, nil)
			p.next()
			continue
		}
		if p.matchPunct("...") {
			expr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			arr.Elements = append(arr.Elements, &ast.SpreadElement{Arg: expr, Loc: posOf(p.peek())})
		} else {
			expr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			arr.Elements = append(arr.Elements, expr)
		}
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("]"); err != nil {
		return nil, err
	}
	return arr, nil
}

func (p *Parser) parseObjectLit() (ast.Expression, error) {
	t := p.peek()
	p.next() // {
	obj := &ast.ObjectLit{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated object literal")
		}
		// spread
		if p.matchPunct("...") {
			expr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			obj.Properties = append(obj.Properties, ast.Property{Value: expr, Kind: ast.PropertySpread, Loc: posOf(t)})
		} else {
			propTok := p.peek()
			var key ast.Expression
			computedKey := false
			switch propTok.Type {
			case lexer.TokenIdent, lexer.TokenKeyword:
				p.next()
				key = &ast.Identifier{Name: propTok.Value, Loc: posOf(propTok)}
			case lexer.TokenString:
				p.next()
				key = &ast.StringLit{Value: propTok.Value, Loc: posOf(propTok)}
			case lexer.TokenNumber:
				p.next()
				v, _ := parseNumberLiteral(propTok.Value)
				key = &ast.NumberLit{Value: v, Raw: propTok.Value, Loc: posOf(propTok)}
			case lexer.TokenPunct:
				if propTok.Value == "[" {
					p.next()
					ce, err := p.parseAssignment()
					if err != nil {
						return nil, err
					}
					if err := p.expectPunct("]"); err != nil {
						return nil, err
					}
					key = ce
					computedKey = true
					_ = propTok
				} else {
					return nil, p.errorf(propTok, "invalid object key")
				}
			default:
				return nil, p.errorf(propTok, "invalid object key")
			}
			// get / set / method shorthand
			kind := ast.PropertyInit
			if id, ok := key.(*ast.Identifier); ok {
				// async method shorthand: `async foo() {}`
				if id.Name == "async" && p.peek().Type != lexer.TokenPunct {
					methodTok := p.next()
					var methodKey ast.Expression
					if methodTok.Type == lexer.TokenIdent || methodTok.Type == lexer.TokenKeyword {
						methodKey = &ast.Identifier{Name: methodTok.Value, Loc: posOf(methodTok)}
					} else if methodTok.Type == lexer.TokenString {
						methodKey = &ast.StringLit{Value: methodTok.Value, Loc: posOf(methodTok)}
					} else {
						return nil, p.errorf(methodTok, "invalid async method name")
					}
					p.asyncStack = append(p.asyncStack, true)
					params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
					p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
					if err != nil {
						return nil, err
					}
					fn := &ast.FunctionExpr{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: body, IsAsync: true, Loc: posOf(methodTok)}
					obj.Properties = append(obj.Properties, ast.Property{Key: methodKey, Value: fn, Kind: ast.PropertyMethod, Loc: posOf(propTok)})
					if !p.matchPunct(",") {
						break
					}
					continue
				}
				if (id.Name == "get" || id.Name == "set") &&
					p.peek().Type != lexer.TokenPunct {
					// 实际是访问器：get prop() {}
					methodTok := p.next()
					var methodKey ast.Expression
					if methodTok.Type == lexer.TokenIdent || methodTok.Type == lexer.TokenKeyword {
						methodKey = &ast.Identifier{Name: methodTok.Value, Loc: posOf(methodTok)}
					} else if methodTok.Type == lexer.TokenString {
						methodKey = &ast.StringLit{Value: methodTok.Value, Loc: posOf(methodTok)}
					} else {
						return nil, p.errorf(methodTok, "invalid accessor name")
					}
					params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
					if err != nil {
						return nil, err
					}
					fn := &ast.FunctionExpr{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: body, Loc: posOf(methodTok)}
					if id.Name == "get" {
						kind = ast.PropertyGet
					} else {
						kind = ast.PropertySet
					}
					obj.Properties = append(obj.Properties, ast.Property{Key: methodKey, Value: fn, Kind: kind, Loc: posOf(propTok)})
					if !p.matchPunct(",") {
						break
					}
					continue
				}
			}
			// 普通 init 或 method shorthand
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
				// method shorthand
				params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
				if err != nil {
					return nil, err
				}
				fn := &ast.FunctionExpr{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: body, Loc: posOf(propTok)}
				obj.Properties = append(obj.Properties, ast.Property{Key: key, Value: fn, Kind: ast.PropertyMethod, Computed: computedKey, Loc: posOf(propTok)})
			} else if p.matchPunct(":") {
				val, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				obj.Properties = append(obj.Properties, ast.Property{Key: key, Value: val, Kind: ast.PropertyInit, Computed: computedKey, Loc: posOf(propTok)})
			} else if id, ok := key.(*ast.Identifier); ok {
				// shorthand: { x } → { x: x }
				obj.Properties = append(obj.Properties, ast.Property{Key: key, Value: id, Kind: ast.PropertyInit, Computed: computedKey, Loc: posOf(propTok)})
			} else {
				return nil, p.errorf(p.peek(), "expected ':' after object key")
			}
		}
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return obj, nil
}

// === Destructuring patterns ===============================================

// parseArrayPattern parses `[a, b, , c, ...rest]`.
func (p *Parser) parseArrayPattern() (*ast.ArrayPattern, error) {
	t := p.peek()
	p.next() // [
	pat := &ast.ArrayPattern{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "]") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated array pattern")
		}
		// hole (elision)
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "," {
			pat.Elements = append(pat.Elements, ast.ArrayPatternElement{})
			p.next()
			continue
		}
		var el ast.ArrayPatternElement
		// rest: ...rest
		if p.matchPunct("...") {
			target, err := p.parsePatternTarget()
			if err != nil {
				return nil, err
			}
			el.Target = target
			el.IsRest = true
			pat.Elements = append(pat.Elements, el)
			break // rest must be last
		}
		target, err := p.parsePatternTarget()
		if err != nil {
			return nil, err
		}
		el.Target = target
		if p.matchPunct("=") {
			def, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			el.Default = def
		}
		pat.Elements = append(pat.Elements, el)
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("]"); err != nil {
		return nil, err
	}
	return pat, nil
}

// parseObjectPattern parses `{a, b: c, ...rest}`.
func (p *Parser) parseObjectPattern() (*ast.ObjectPattern, error) {
	t := p.peek()
	p.next() // {
	pat := &ast.ObjectPattern{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated object pattern")
		}
		var prop ast.ObjectPatternProperty
		// rest: ...rest
		if p.matchPunct("...") {
			target, err := p.parsePatternTarget()
			if err != nil {
				return nil, err
			}
			prop.Value = target
			prop.IsRest = true
			pat.Properties = append(pat.Properties, prop)
			break // rest must be last
		}
		// key
		keyTok := p.peek()
		var key ast.Expression
		switch keyTok.Type {
		case lexer.TokenIdent, lexer.TokenKeyword:
			p.next()
			key = &ast.Identifier{Name: keyTok.Value, Loc: posOf(keyTok)}
		case lexer.TokenString:
			p.next()
			key = &ast.StringLit{Value: keyTok.Value, Loc: posOf(keyTok)}
		case lexer.TokenNumber:
			p.next()
			v, _ := parseNumberLiteral(keyTok.Value)
			key = &ast.NumberLit{Value: v, Raw: keyTok.Value, Loc: posOf(keyTok)}
		default:
			return nil, p.errorf(keyTok, "invalid property name in object pattern")
		}
		prop.Key = key
		if p.matchPunct(":") {
			// renamed binding: key: target [= default]
			target, err := p.parsePatternTarget()
			if err != nil {
				return nil, err
			}
			prop.Value = target
			if p.matchPunct("=") {
				def, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				prop.Default = def
			}
		} else if p.matchPunct("=") {
			// shorthand with default: name = default
			prop.Value = &ast.Identifier{Name: keyTok.Value, Loc: posOf(keyTok)}
			def, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			prop.Default = def
		} else {
			// shorthand: { x } → { x: x }
			prop.Value = &ast.Identifier{Name: keyTok.Value, Loc: posOf(keyTok)}
		}
		pat.Properties = append(pat.Properties, prop)
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return pat, nil
}

// parsePatternTarget parses the target of a binding: identifier or nested pattern.
func (p *Parser) parsePatternTarget() (ast.Pattern, error) {
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "[" {
		return p.parseArrayPattern()
	}
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
		return p.parseObjectPattern()
	}
	nameTok, err := p.expect(lexer.TokenIdent, "")
	if err != nil {
		return nil, err
	}
	return &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}, nil
}

// parseNumberLiteral 解析数字字面量字符串为 float64。
// 支持：十进制、十六进制(0x)、八进制(0o)、二进制(0b)、科学计数法、数字分隔符。
// bigIntLiteralToDecimal 将 BigInt 字面量（已去 n 后缀）转为十进制整数字符串。
// 支持 0x/0o/0b 前缀与下划线分隔符。例："0xFF" → "255"，"1_000" → "1000"。
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
func (p *Parser) parseInterfaceDecl() (ast.Statement, error) {
	t := p.next() // consume 'interface'
	nameTok, err := p.expect(lexer.TokenIdent, "")
	if err != nil {
		return nil, err
	}
	_ = nameTok
	// Optional generic type params: interface Foo<T> { ... }
	if err := p.skipTypeParameters(); err != nil {
		return nil, err
	}
	// Optional `extends A, B<...>` clause.
	if p.peek().Type == lexer.TokenKeyword && p.peek().Value == "extends" {
		p.next()
		for {
			if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
				return nil, err
			}
			// Qualified name / generic args
			for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
				p.next()
				if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
					return nil, err
				}
			}
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "<" {
				if err := p.skipAngleBraces(); err != nil {
					return nil, err
				}
			}
			if !p.matchPunct(",") {
				break
			}
		}
	}
	// Body: { members }
	if err := p.skipBalanced("{", "}"); err != nil {
		return nil, err
	}
	return &ast.EmptyStmt{Loc: posOf(t)}, nil
}

// parseTypeAliasDecl skips a TypeScript `type Name<T> = SomeType;` declaration
// — type aliases are compile-time only and produce no runtime code.
func (p *Parser) parseTypeAliasDecl() (ast.Statement, error) {
	t := p.next() // consume 'type'
	if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
		return nil, err
	}
	// Optional generic type params: type Foo<T> = ...
	if err := p.skipTypeParameters(); err != nil {
		return nil, err
	}
	if err := p.expectPunct("="); err != nil {
		return nil, err
	}
	// Skip the type expression on the right-hand side.
	if err := p.skipType(); err != nil {
		return nil, err
	}
	if err := p.consumeSemicolon(); err != nil {
		return nil, err
	}
	return &ast.EmptyStmt{Loc: posOf(t)}, nil
}

// parseEnumDecl parses a TypeScript `enum Name { members }` declaration and
// lowers it to JavaScript: `var Name; (function(Name) { ... })(Name || (Name = {}));`
//
// Numeric members get both forward (Name.Member = N) and reverse (Name[N] = "Member")
// mappings; string members get forward mapping only.
func (p *Parser) parseEnumDecl() (ast.Statement, error) {
	t := p.next() // consume 'enum'
	// `const enum` — erase entirely (const enums are inlined at usage sites).
	// Since we don't do inlining, treat const enums as regular enums (generate
	// the object) to preserve runtime behavior.
	_ = t

	nameTok, err := p.expect(lexer.TokenIdent, "")
	if err != nil {
		return nil, err
	}
	enumName := nameTok.Value
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}

	// Parse members into (name, value) pairs. Values are nil for auto-incremented
	// numeric members, or ast.Expression for explicit values.
	type enumMember struct {
		name  string
		value ast.Expression // nil = auto-increment
	}
	var members []enumMember

	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated enum body")
		}
		// Member name: identifier or string literal
		memberTok := p.peek()
		var memberName string
		if memberTok.Type == lexer.TokenString {
			p.next()
			memberName = memberTok.Value
		} else if memberTok.Type == lexer.TokenIdent || memberTok.Type == lexer.TokenKeyword {
			p.next()
			memberName = memberTok.Value
		} else {
			return nil, p.errorf(memberTok, "expected enum member name but got %q", memberTok.Value)
		}
		// Optional type annotation: `Name: T` (TS 5.0+) — skip.
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == ":" {
			if err := p.parseTypeAnnotation(); err != nil {
				return nil, err
			}
		}
		// Optional initializer: `Name = expr`
		var val ast.Expression
		if p.matchPunct("=") {
			v, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			val = v
		}
		members = append(members, enumMember{name: memberName, value: val})
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}

	// Lower to JS: build an IIFE that sets properties on the enum object.
	// var Name;
	// (function(Name) { Name[Name["M"] = N] = "M"; ... })(Name || (Name = {}));
	//
	// For clarity we emit two assignments per numeric member:
	//   Name["M"] = N; Name[N] = "M";
	// and one per string member:
	//   Name["M"] = "value";
	enumIdent := &ast.Identifier{Name: enumName, Loc: posOf(nameTok)}

	// Build the IIFE body: a block of expression statements.
	iifeBody := &ast.BlockStmt{Loc: posOf(t)}
	autoIdx := 0.0
	for _, m := range members {
		var valExpr ast.Expression
		if m.value != nil {
			valExpr = m.value
			// Track auto-increment for numeric values.
			if lit, ok := m.value.(*ast.NumberLit); ok {
				autoIdx = lit.Value
			}
		} else {
			valExpr = &ast.NumberLit{Value: autoIdx, Raw: fmt.Sprintf("%g", autoIdx), Loc: posOf(t)}
		}

		// Forward mapping: Name["Member"] = value
		forwardAssign := &ast.AssignExpr{
			Left: &ast.MemberExpr{
				Object:   enumIdent,
				Property: &ast.StringLit{Value: m.name, Loc: posOf(t)},
				Computed: true,
				Loc:      posOf(t),
			},
			Op:    "=",
			Right: valExpr,
			Loc:   posOf(t),
		}
		iifeBody.Body = append(iifeBody.Body, &ast.ExprStmt{Expr: forwardAssign, Loc: posOf(t)})

		// Reverse mapping only for numeric (auto or explicit number) members.
		if _, isNum := valExpr.(*ast.NumberLit); isNum {
			reverseAssign := &ast.AssignExpr{
				Left: &ast.MemberExpr{
					Object:   enumIdent,
					Property: valExpr, // the number literal as key
					Computed: true,
					Loc:      posOf(t),
				},
				Op:    "=",
				Right: &ast.StringLit{Value: m.name, Loc: posOf(t)},
				Loc:   posOf(t),
			}
			iifeBody.Body = append(iifeBody.Body, &ast.ExprStmt{Expr: reverseAssign, Loc: posOf(t)})
			autoIdx++
		}
	}

	// Build: (function(Name) { body })(Name || (Name = {}))
	fnExpr := &ast.FunctionExpr{
		Params: []*ast.Identifier{enumIdent},
		Body:   iifeBody,
		Loc:    posOf(t),
	}
	// Name || (Name = {})
	innerAssign := &ast.AssignExpr{
		Left:  enumIdent,
		Op:    "=",
		Right: &ast.ObjectLit{Loc: posOf(t)},
		Loc:   posOf(t),
	}
	arg := &ast.LogicalExpr{
		Op:    "||",
		Left:  enumIdent,
		Right: innerAssign,
		Loc:   posOf(t),
	}
	call := &ast.CallExpr{
		Callee:    fnExpr,
		Arguments: []ast.Expression{arg},
		Loc:       posOf(t),
	}

	// var Name;
	varDecl := &ast.VarDecl{
		Kind: "var",
		Loc:  posOf(t),
	}
	varDecl.Decls = append(varDecl.Decls, ast.VarDeclarator{
		Name: &ast.Identifier{Name: enumName, Loc: posOf(nameTok)},
	})

	// Wrap both statements in a block.
	block := &ast.BlockStmt{Loc: posOf(t)}
	block.Body = append(block.Body, varDecl)
	block.Body = append(block.Body, &ast.ExprStmt{Expr: call, Loc: posOf(t)})
	return block, nil
}

// parseNamespaceDecl parses a TypeScript `namespace Name { body }` declaration
// and lowers it to: `var Name; (function(Name) { ...body... })(Name || (Name = {}));`
//
// Inside the body, `export`-prefixed declarations become assignments to the
// namespace object (e.g. `export const x = 1` → `Name.x = 1`); non-exported
// declarations remain local to the IIFE.
func (p *Parser) parseNamespaceDecl() (ast.Statement, error) {
	t := p.next() // consume 'namespace'
	nameTok, err := p.expect(lexer.TokenIdent, "")
	if err != nil {
		return nil, err
	}
	nsName := nameTok.Value
	// Parse the body as a block, but strip `export` modifiers.
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	nsIdent := &ast.Identifier{Name: nsName, Loc: posOf(nameTok)}
	iifeBody := &ast.BlockStmt{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated namespace body")
		}
		if p.matchPunct(";") {
			continue
		}
		// Check for `export` prefix.
		isExport := false
		if (p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword) && p.peek().Value == "export" {
			p.next()
			isExport = true
		}
		// Also handle `declare` (ambient declarations) — skip the entire
		// declaration since it produces no runtime code.
		if p.peek().Type == lexer.TokenIdent && p.peek().Value == "declare" {
			p.next()
			// `declare const x: T;` / `declare function f(): T;` / etc.
			// Skip until `;` or `}`.
			for !(p.peek().Type == lexer.TokenPunct && (p.peek().Value == ";" || p.peek().Value == "}")) {
				if p.peek().Type == lexer.TokenEOF {
					break
				}
				if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
					if err := p.skipBalanced("{", "}"); err != nil {
						return nil, err
					}
					break
				}
				p.next()
			}
			p.matchPunct(";")
			continue
		}

		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		if isExport {
			// Transform exported declarations into namespace assignments.
			// For `export const x = v` → `ns.x = v`
			// For `export function f() {}` → `ns.f = function f() {}`
			// For `export class C {}` → `ns.C = class C {}`
			transformed := p.transformExportedDecl(stmt, nsIdent)
			if transformed != nil {
				iifeBody.Body = append(iifeBody.Body, transformed...)
			}
		} else {
			iifeBody.Body = append(iifeBody.Body, stmt)
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}

	// Build: (function(Name) { body })(Name || (Name = {}))
	fnExpr := &ast.FunctionExpr{
		Params: []*ast.Identifier{nsIdent},
		Body:   iifeBody,
		Loc:    posOf(t),
	}
	innerAssign := &ast.AssignExpr{
		Left:  nsIdent,
		Op:    "=",
		Right: &ast.ObjectLit{Loc: posOf(t)},
		Loc:   posOf(t),
	}
	arg := &ast.LogicalExpr{
		Op:    "||",
		Left:  nsIdent,
		Right: innerAssign,
		Loc:   posOf(t),
	}
	call := &ast.CallExpr{
		Callee:    fnExpr,
		Arguments: []ast.Expression{arg},
		Loc:       posOf(t),
	}

	// var Name;
	varDecl := &ast.VarDecl{
		Kind: "var",
		Loc:  posOf(t),
	}
	varDecl.Decls = append(varDecl.Decls, ast.VarDeclarator{
		Name: &ast.Identifier{Name: nsName, Loc: posOf(nameTok)},
	})

	block := &ast.BlockStmt{Loc: posOf(t)}
	block.Body = append(block.Body, varDecl)
	block.Body = append(block.Body, &ast.ExprStmt{Expr: call, Loc: posOf(t)})
	return block, nil
}

// transformExportedDecl converts a namespace `export` declaration into
// assignments on the namespace object. Returns a slice of statements.
func (p *Parser) transformExportedDecl(stmt ast.Statement, nsIdent *ast.Identifier) []ast.Statement {
	switch n := stmt.(type) {
	case *ast.VarDecl:
		var result []ast.Statement
		for _, d := range n.Decls {
			if d.Name == nil {
				continue
			}
			// ns.Name = init
			assign := &ast.AssignExpr{
				Left: &ast.MemberExpr{
					Object:   nsIdent,
					Property: &ast.Identifier{Name: d.Name.Name, Loc: d.Name.Loc},
					Loc:      d.Name.Loc,
				},
				Op:    "=",
				Right: d.Init,
				Loc:   d.Name.Loc,
			}
			result = append(result, &ast.ExprStmt{Expr: assign, Loc: d.Name.Loc})
		}
		return result
	case *ast.FunctionDecl:
		if n.Name == nil {
			return nil
		}
		fnExpr := &ast.FunctionExpr{
			Name:        n.Name,
			Params:      n.Params,
			Defaults:    n.Defaults,
			RestParam:   n.RestParam,
			Body:        n.Body,
			IsAsync:     n.IsAsync,
			IsGenerator: n.IsGenerator,
			Loc:         n.Loc,
		}
		assign := &ast.AssignExpr{
			Left: &ast.MemberExpr{
				Object:   nsIdent,
				Property: &ast.Identifier{Name: n.Name.Name, Loc: n.Name.Loc},
				Loc:      n.Name.Loc,
			},
			Op:    "=",
			Right: fnExpr,
			Loc:   n.Loc,
		}
		return []ast.Statement{&ast.ExprStmt{Expr: assign, Loc: n.Loc}}
	case *ast.ClassDecl:
		if n.Name == nil {
			return nil
		}
		classExpr := &ast.ClassExpr{
			Name:       n.Name,
			SuperClass: n.SuperClass,
			Body:       n.Body,
			Loc:        n.Loc,
		}
		assign := &ast.AssignExpr{
			Left: &ast.MemberExpr{
				Object:   nsIdent,
				Property: &ast.Identifier{Name: n.Name.Name, Loc: n.Name.Loc},
				Loc:      n.Name.Loc,
			},
			Op:    "=",
			Right: classExpr,
			Loc:   n.Loc,
		}
		return []ast.Statement{&ast.ExprStmt{Expr: assign, Loc: n.Loc}}
	}
	// For unsupported declaration types, keep as-is.
	return []ast.Statement{stmt}
}

// === TypeScript type-stripping helpers ====================================
//
// These helpers skip over TypeScript type annotations without building AST
// nodes — the types are erased at parse time, mirroring how tsc/swc/esbuild
// emit JS by stripping types. The grammar handled here covers the common
// surface of TS types: primitives, references, generics, unions/intersections,
// object/tuple/function types, array postfix, conditional types, mapped types,
// type predicates, keyof/typeof/infer, and `as` assertions inside types.
//
// Notes:
//   - `<` / `>` are ambiguous with comparison operators in JS, but inside a
//     type context they always introduce/close generic args. `>>` / `>>>` from
//     nested generics (e.g. Foo<Array<T>>) are split by treating one `>` as
//     the closer and rewriting the current token in place.
//   - Boundary tokens (`,`, `)`, `}`, `]`, `;`, `=`, `{` (block start),
//     `=>` (arrow body), and contextual keywords `extends`/`implements`/
//     `return`/`throw`) terminate a type expression at the outer level; nested
//     delimiters are consumed by skipBalanced/skipAngleBraces.

// parseTypeAnnotation consumes an optional `: Type` and discards the type.
// Used for variable declarators, parameters, return types, and class fields.
func (p *Parser) parseTypeAnnotation() error {
	if !p.matchPunct(":") {
		return nil
	}
	return p.skipType()
}

// skipType skips one full type expression (union/intersection/conditional).
// Returns whether the outermost atom was a parenthesised group (for function
// type `=>` detection by the caller).
func (p *Parser) skipType() error {
	_, err := p.skipTypeInner()
	return err
}

// skipTypeInner is the recursive worker for skipType; it returns whether the
// outermost atom was `(...)`, which determines whether a following `=>` is a
// function-type return (consumed) or an arrow body separator (left alone).
func (p *Parser) skipTypeInner() (bool, error) {
	if err := p.skipTypePrefix(); err != nil {
		return false, err
	}
	wasParen, err := p.skipTypeAtom()
	if err != nil {
		return false, err
	}
	// Postfix array: T[]
	for p.peek().Type == lexer.TokenPunct && p.peek().Value == "[" {
		if err := p.skipBalanced("[", "]"); err != nil {
			return false, err
		}
		wasParen = false
	}
	// Infix union/intersection/conditional/type-predicate/as-assertion.
	for {
		t := p.peek()
		if t.Type == lexer.TokenPunct && (t.Value == "|" || t.Value == "&") {
			p.next()
			if _, err := p.skipTypeInner(); err != nil {
				return false, err
			}
			wasParen = false
			continue
		}
		if t.Type == lexer.TokenKeyword && t.Value == "extends" {
			// Conditional type: T extends U ? X : Y  (also `extends` constraint
			// inside generic params, but skipType handles either by recursing).
			p.next()
			if _, err := p.skipTypeInner(); err != nil {
				return false, err
			}
			if p.matchPunct("?") {
				if _, err := p.skipTypeInner(); err != nil {
					return false, err
				}
				if !p.matchPunct(":") {
					return false, p.errorf(p.peek(), "expected ':' in conditional type")
				}
				if _, err := p.skipTypeInner(); err != nil {
					return false, err
				}
			}
			wasParen = false
			continue
		}
		if t.Type == lexer.TokenIdent && t.Value == "is" {
			// Type predicate: x is T
			p.next()
			if _, err := p.skipTypeInner(); err != nil {
				return false, err
			}
			wasParen = false
			continue
		}
		break
	}
	// Function return type: only after `(...)` atom, e.g. `(a: T) => U`.
	// This avoids consuming `=>` when it's an arrow body separator following
	// a return-type annotation like `(): T => body`.
	if wasParen && p.peek().Type == lexer.TokenPunct && p.peek().Value == "=>" {
		p.next()
		if _, err := p.skipTypeInner(); err != nil {
			return false, err
		}
	}
	return wasParen, nil
}

// skipTypePrefix consumes leading unary type operators: readonly, keyof,
// typeof, infer, new, unique.
func (p *Parser) skipTypePrefix() error {
	for {
		t := p.peek()
		if t.Type == lexer.TokenIdent {
			switch t.Value {
			case "readonly", "keyof", "typeof", "infer", "unique":
				p.next()
				continue
			}
		}
		if t.Type == lexer.TokenKeyword && t.Value == "new" {
			p.next()
			continue
		}
		break
	}
	return nil
}

// skipTypeAtom skips one type atom: primitive, reference, literal, object,
// tuple, function, or parenthesised type. Returns whether it was a `(...)`.
func (p *Parser) skipTypeAtom() (bool, error) {
	t := p.peek()
	switch t.Type {
	case lexer.TokenString, lexer.TokenNumber:
		p.next()
	case lexer.TokenKeyword:
		// true/false/null/undefined are value literals; other keywords
		// (number/string/boolean/any/unknown/void/never/object/symbol/bigint/
		// in/out/const/abstract) are type primitives.
		p.next()
	case lexer.TokenIdent:
		p.next()
		// Qualified name: Foo.Bar.Baz
		for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
			p.next()
			if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
				return false, err
			}
		}
		// Generic type args: Foo<T, U>
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "<" {
			if err := p.skipAngleBraces(); err != nil {
				return false, err
			}
		}
	case lexer.TokenPunct:
		switch t.Value {
		case "(":
			// Function type (params) => ret OR parenthesised type.
			if err := p.skipBalanced("(", ")"); err != nil {
				return false, err
			}
			return true, nil
		case "{":
			// Object/mapped type.
			if err := p.skipBalanced("{", "}"); err != nil {
				return false, err
			}
		case "[":
			// Tuple type [T, U] or index-access type T[K].
			if err := p.skipBalanced("[", "]"); err != nil {
				return false, err
			}
		case "<":
			// Standalone generic type (rare at atom position).
			if err := p.skipAngleBraces(); err != nil {
				return false, err
			}
		case "-":
			// `-` literal type (e.g. -1). Consume and expect a number.
			p.next()
			if p.peek().Type != lexer.TokenNumber {
				return false, p.errorf(p.peek(), "expected number after '-' in type")
			}
			p.next()
		default:
			return false, p.errorf(t, "unexpected token %q in type", t.Value)
		}
	case lexer.TokenTemplate:
		// Template literal type: `foo${T}bar`
		p.next()
	default:
		return false, p.errorf(t, "unexpected token %q in type", t.Value)
	}
	return false, nil
}

// skipBalanced consumes tokens from `open` to the matching `close`, tracking
// nesting of any of the bracket pairs ()/[]/{}. Used for object/tuple/
// parenthesised types where the inner contents can be arbitrary.
func (p *Parser) skipBalanced(open, close string) error {
	t := p.peek()
	if t.Type != lexer.TokenPunct || t.Value != open {
		return p.errorf(t, "expected %q", open)
	}
	p.next() // consume open
	depth := 1
	for depth > 0 {
		t := p.peek()
		if t.Type == lexer.TokenEOF {
			return p.errorf(t, "unterminated %q, expected %q", open, close)
		}
		if t.Type == lexer.TokenPunct {
			switch t.Value {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				// Only the matching close decrements depth at the top level;
				// nested closes of other bracket kinds are tracked too.
				depth--
			}
			if depth == 0 && t.Value == close {
				p.next()
				return nil
			}
		}
		p.next()
	}
	return nil
}

// skipAngleBraces consumes a `<...>` generic argument list, handling `>>` /
// `>>>` token splitting for nested generics (e.g. Foo<Array<T>>).
func (p *Parser) skipAngleBraces() error {
	t := p.peek()
	if t.Type != lexer.TokenPunct || t.Value != "<" {
		return p.errorf(t, "expected '<' in generic type")
	}
	p.next() // consume '<'
	depth := 1
	for depth > 0 {
		t := p.peek()
		if t.Type == lexer.TokenEOF {
			return p.errorf(t, "unterminated generic type, expected '>'")
		}
		if t.Type == lexer.TokenPunct {
			switch t.Value {
			case "<":
				depth++
				p.next()
			case ">":
				depth--
				p.next()
			case ">>":
				// Two closing angles: consume one, rewrite remaining as '>'.
				depth--
				if depth >= 1 {
					depth--
				}
				if depth > 0 {
					p.tokens[p.pos] = lexer.Token{
						Type: lexer.TokenPunct, Value: ">", Line: t.Line, Col: t.Col,
					}
				} else {
					p.next()
				}
			case ">>>":
				depth--
				if depth >= 1 {
					depth--
				}
				if depth >= 1 {
					depth--
				}
				if depth > 0 {
					p.tokens[p.pos] = lexer.Token{
						Type: lexer.TokenPunct, Value: ">>", Line: t.Line, Col: t.Col,
					}
				} else {
					p.next()
				}
			case ">=":
				// T >= U shouldn't appear in type position; treat as `>` then `=`.
				depth--
				p.tokens[p.pos] = lexer.Token{
					Type: lexer.TokenPunct, Value: "=", Line: t.Line, Col: t.Col,
				}
			case ">>=":
				depth--
				if depth >= 1 {
					depth--
				}
				p.tokens[p.pos] = lexer.Token{
					Type: lexer.TokenPunct, Value: "=", Line: t.Line, Col: t.Col,
				}
			default:
				p.next()
			}
		} else {
			p.next()
		}
	}
	return nil
}

// skipTypeParameters consumes an optional `<T, U extends X, R = D>` generic
// parameter list before a function/class/arrow signature.
func (p *Parser) skipTypeParameters() error {
	if p.peek().Type != lexer.TokenPunct || p.peek().Value != "<" {
		return nil
	}
	return p.skipAngleBraces()
}

// trySkipTypeArgs attempts to skip TypeScript generic type arguments
// (`<T, U>`) before a call expression or `new`. It backtracks (restoring the
// position) if the `<...>` is not followed by `(`, since `<` could also be a
// less-than comparison. Returns true if type arguments were skipped.
//
// 注意：skipAngleBraces 会把 `>=`/`>>`/`>>>` 就地改写（拆解嵌套泛型闭合角括号），
// 因此回溯时必须同时恢复 token 快照，否则后续 `a >= b` 等比较表达式的 token
// 已被破坏（`>=` 被改成 `=`）。
func (p *Parser) trySkipTypeArgs() bool {
	if p.peek().Type != lexer.TokenPunct || p.peek().Value != "<" {
		return false
	}
	savedPos := p.pos
	savedTokens := make([]lexer.Token, len(p.tokens))
	copy(savedTokens, p.tokens)
	if err := p.skipAngleBraces(); err != nil {
		p.pos = savedPos
		p.tokens = savedTokens
		return false
	}
	// Only treat as type args if followed by `(` (a call).
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
		return true
	}
	p.pos = savedPos
	p.tokens = savedTokens
	return false
}

// skipDecorators consumes and discards TypeScript decorator expressions
// (`@expr`, `@expr(args)`, `@foo.bar`). Decorators are parsed but not applied
// — the runtime sees no decorator code. Multiple stacked decorators are
// consumed: `@a @b class C {}`.
func (p *Parser) skipDecorators() error {
	for p.peek().Type == lexer.TokenPunct && p.peek().Value == "@" {
		p.next() // consume '@'
		// Decorator name: identifier (possibly qualified: foo.bar.baz).
		nameTok, err := p.expect(lexer.TokenIdent, "")
		if err != nil {
			return err
		}
		_ = nameTok
		// Qualified decorator: @foo.bar.baz
		for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
			p.next()
			if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
				return err
			}
		}
		// Optional call arguments: @dec(arg1, arg2)
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
			if _, err := p.parseArgs(); err != nil {
				return err
			}
		}
	}
	return nil
}

// skipToSemicolon 跳过 token 直到语句结束（顶层 ';' 或 EOF）。
// 用于擦除 TS 类型声明（export type X = ... 等），跳过时保持嵌套深度
// （{} () [] 内的 ';' 不终止）。
func (p *Parser) skipToSemicolon() error {
	depth := 0
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		switch tok.Type {
		case lexer.TokenEOF:
			return nil
		case lexer.TokenString, lexer.TokenTemplate, lexer.TokenRegex:
			p.pos++
			continue
		case lexer.TokenPunct:
			switch tok.Value {
			case "{", "(", "[":
				depth++
			case "}", ")", "]":
				if depth > 0 {
					depth--
				}
			case ";":
				if depth == 0 {
					p.pos++
					return nil
				}
			}
		}
		p.pos++
	}
	return nil
}

// skipTypeDeclBody 跳过 TS 类型声明体（interface/enum/namespace）：
// 名称 + 泛型/extends/implements 子句（至顶层 '{'），再平衡跳过块体。
func (p *Parser) skipTypeDeclBody() error {
	depth := 0
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		if tok.Type == lexer.TokenEOF {
			return nil
		}
		if tok.Type == lexer.TokenPunct {
			switch tok.Value {
			case "{":
				if depth == 0 {
					return p.skipBalanced("{", "}")
				}
			case "(", "[":
				depth++
			case ")", "]":
				if depth > 0 {
					depth--
				}
			case ";":
				if depth == 0 {
					p.pos++
					return nil // 无块体声明（如 declare 语句）
				}
			}
		}
		p.pos++
	}
	return nil
}
