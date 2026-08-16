package parser

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

func (p *Parser) parseStatement() (ast.Statement, error) {
	// TypeScript: skip leading `@decorator` expressions on declarations.
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "@" {
		if err := p.skipDecorators(); err != nil {
			return nil, err
		}
	}
	t := p.peek()
	// TypeScript ambient 声明：`declare function f(): T;` / `declare const x: T;`
	// / `declare class C {}` / `declare namespace X {}`——无运行时语义，整体擦除。
	if t.Type == lexer.TokenIdent && t.Value == "declare" {
		nx := p.peekAt(1)
		if nx.Type == lexer.TokenKeyword &&
			(nx.Value == "function" || nx.Value == "const" || nx.Value == "let" ||
				nx.Value == "var" || nx.Value == "class" || nx.Value == "enum" ||
				nx.Value == "namespace" || nx.Value == "module" || nx.Value == "abstract") {
			p.next() // 消费 'declare'
			return p.skipAmbientDecl()
		}
	}
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
			// 动态 import(...) / import.meta 作为表达式语句：
			// import 后紧跟 "(" 或 "." 时不是声明，落到
			// parseExprStmt → parsePrimary 的 import 分支处理。
			next := p.peekAt(1)
			if next.Type == lexer.TokenPunct && (next.Value == "(" || next.Value == ".") {
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
			// `namespace` 是 TS 上下文关键字：仅 `namespace Name {` /
			// `namespace A.B {` 是声明；`namespace = x`、`namespace.foo()`
			// 等是普通标识符用法（vue runtime 等真实代码大量存在）。
			next := p.peekAt(1)
			if next.Type == lexer.TokenIdent || next.Type == lexer.TokenString {
				after := p.peekAt(2)
				if after.Type == lexer.TokenPunct && (after.Value == "{" || after.Value == ".") {
					return p.parseNamespaceDecl()
				}
			}
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

// skipAmbientDecl 擦除 TypeScript `declare ...` 环境声明（无运行时语义）。
// 已消费 `declare` 关键字；按声明种类跳过到语句/块结束。

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
			// TypeScript permits a type annotation on catch bindings. It has no
			// runtime meaning and is erased before parsing the catch body.
			if err := p.parseTypeAnnotation(); err != nil {
				return nil, err
			}
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
