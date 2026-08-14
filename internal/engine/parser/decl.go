package parser

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

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
		nameTok := p.peek()
		// 上下文关键字（async/of/await/yield 等）是合法变量名（IdentifierName）。
		if nameTok.Type == lexer.TokenIdent || nameTok.Type == lexer.TokenKeyword {
			p.next()
		} else {
			return vd, p.errorf(nameTok, "expected variable name")
		}
		vd.Name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	}
	// TypeScript: definite assignment assertion `let x!: T` — 断言此处
	// 已初始化（仅编译期标记，运行时无语义），直接消费 `!`。
	p.matchPunct("!")
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
	// 函数名是 IdentifierName：允许上下文关键字（async/of/let 等），
	// 与 expectName（变量/参数名）一致。此前只认 TokenIdent，导致
	// `function async() {}` / `const f = function async() {}` 语法错误
	// （@babel/core 等 esbuild/minify 产物常见此写法）。
	if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword {
		nameTok := p.next()
		name = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
	} else if !isExpr {
		return nil, p.errorf(p.peek(), "function declaration requires a name")
	}
	// TypeScript: skip generic type parameters `<T, U extends X, R = D>`.
	if err := p.skipTypeParameters(); err != nil {
		return nil, err
	}
	// TypeScript 函数重载签名：function f(params): RetType;——无函数体，
	// 返回空声明（编译期擦除）。lookahead 探测并回溯。
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
		savedPos := p.pos
		if p.isOverloadSignature() {
			return &ast.FunctionDecl{
				Name:   name,
				Params: nil,
				Body:   &ast.BlockStmt{},
				Loc:    posOf(t),
			}, nil
		}
		p.pos = savedPos
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
		Defaults:      defaults,
		RestParam:     rest,
		Body:          body,
		IsAsync:       isAsync,
		IsGenerator:   isGenerator,
		Loc:           posOf(t),
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
	var restPattern ast.Pattern
	var parameterProperties []*ast.Identifier
	if !(p.peek().Type == lexer.TokenPunct && p.peek().Value == ")") {
		for {
			// TypeScript constructor parameter properties: consume visibility/
			// readonly modifiers and emit this.name = name after parsing the body.
			isParameterProperty := false
			for {
				current := p.peek()
				next := p.peekAt(1)
				isModifier := current.Value == "public" || current.Value == "private" ||
					current.Value == "protected" || current.Value == "readonly"
				nextIsName := next.Type == lexer.TokenIdent || next.Type == lexer.TokenKeyword
				if !isModifier || !nextIsName {
					break
				}
				p.next()
				isParameterProperty = true
			}
			// ES2015 rest parameter: `...name`
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "..." {
				restTok := p.next()
				if p.peek().Type == lexer.TokenPunct && (p.peek().Value == "[" || p.peek().Value == "{") {
					pat, err := p.parsePatternTarget()
					if err != nil {
						return nil, nil, nil, nil, nil, err
					}
					restPattern = pat
					rest = &ast.Identifier{
						Name: fmt.Sprintf("__aluka_rest_%d_%d", restTok.Line, restTok.Col),
						Loc:  posOf(restTok),
					}
				} else {
					nameTok, err := p.expectName()
					if err != nil {
						return nil, nil, nil, nil, nil, err
					}
					rest = &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
				}
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
			nameTok, err := p.expectName()
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}
			param := &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}
			params = append(params, param)
			patterns = append(patterns, nil)
			if isParameterProperty {
				parameterProperties = append(parameterProperties, param)
			}
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
	if restPattern != nil {
		body.Body = append([]ast.Statement{&ast.VarDecl{
			Kind: "let",
			Decls: []ast.VarDeclarator{{
				Pattern: restPattern,
				Init:    &ast.Identifier{Name: rest.Name, Loc: rest.Loc},
			}},
			Loc: rest.Loc,
		}}, body.Body...)
	}
	if len(parameterProperties) > 0 {
		assignments := make([]ast.Statement, 0, len(parameterProperties))
		for _, param := range parameterProperties {
			loc := param.Loc
			assignments = append(assignments, &ast.ExprStmt{Expr: &ast.AssignExpr{
				Op: "=",
				Left: &ast.MemberExpr{
					Object:   &ast.ThisExpr{Loc: loc},
					Property: &ast.Identifier{Name: param.Name, Loc: loc},
					Loc:      loc,
				},
				Right: &ast.Identifier{Name: param.Name, Loc: loc},
				Loc:   loc,
			}, Loc: loc})
		}
		insertAt := 0
		if len(body.Body) > 0 {
			if exprStmt, ok := body.Body[0].(*ast.ExprStmt); ok {
				if call, ok := exprStmt.Expr.(*ast.CallExpr); ok {
					if _, ok := call.Callee.(*ast.SuperExpr); ok {
						insertAt = 1
					}
				}
			}
		}
		newBody := make([]ast.Statement, 0, len(body.Body)+len(assignments))
		newBody = append(newBody, body.Body[:insertAt]...)
		newBody = append(newBody, assignments...)
		newBody = append(newBody, body.Body[insertAt:]...)
		body.Body = newBody
	}
	return params, patterns, defaults, rest, body, nil
}


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

// isContextualBindingKeyword 判断关键字token能否作为 import 绑定名。
// of/async 是上下文关键字（非保留字，可作标识符）；其余关键字（if/let/
// await/default 等）在模块代码（恒严格模式）中不可作绑定名，与 Node 一致
// 报 SyntaxError，例如 import { a as if }。
func isContextualBindingKeyword(s string) bool {
	return s == "of" || s == "async"
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
		if p.peek().Type == lexer.TokenIdent ||
			(p.peek().Type == lexer.TokenKeyword && isContextualBindingKeyword(p.peek().Value)) {
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
				// import * as ns：上下文关键字 of/async 可作绑定名。
				if p.peek().Type == lexer.TokenKeyword && isContextualBindingKeyword(p.peek().Value) {
					nameTok = p.next()
				} else {
					return nil, err
				}
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
					// 规范：关键字/字符串字面量作为导入名合法，例如 import { in as Foo, default as Bar, "pkg-name" as pkg }
					if p.peek().Type == lexer.TokenKeyword || p.peek().Type == lexer.TokenString {
						tk := p.next()
						nameTok = lexer.Token{Type: lexer.TokenIdent, Value: tk.Value}
					} else {
						return nil, err
					}
				}
				spec := ast.ImportSpecifier{Imported: nameTok.Value, Local: nameTok.Value}
				// `as` rename: {a as b}
				if p.matchIdent("as") {
					localTok, err := p.expect(lexer.TokenIdent, "")
					if err != nil {
						// 绑定名仅允许上下文关键字（of/async）；保留字
						// （if/let/await 等）按 Node 行为报 SyntaxError。
						if p.peek().Type == lexer.TokenKeyword && isContextualBindingKeyword(p.peek().Value) {
							localTok = p.next()
						} else {
							return nil, err
						}
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
			if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
				return nil, err
			}
			if err := p.skipTypeParameters(); err != nil {
				return nil, err
			}
			if err := p.expectPunct("="); err != nil {
				return nil, err
			}
			if err := p.skipType(); err != nil {
				return nil, err
			}
			if err := p.consumeSemicolon(); err != nil {
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

	// export * from 'mod' / export * as ns from 'mod'
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "*" {
		p.next() // consume '*'
		starName := ""
		if p.matchIdent("as") {
			// export * as ns from 'mod' —— 命名空间重导出。
			nameTok, err := p.expect(lexer.TokenIdent, "")
			if err != nil {
				return nil, err
			}
			starName = nameTok.Value
		}
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
		return &ast.ExportDecl{IsStar: true, StarName: starName, Source: src, Loc: posOf(t)}, nil
	}

	// export {a, b as c} [from 'mod']
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
		p.next() // consume '{'
		decl := &ast.ExportDecl{Loc: posOf(t)}
		// 空导出：export {}（仅类型标记，无运行时导出）。
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "}" {
			p.next() // consume '}'
			if err := p.consumeSemicolon(); err != nil {
				return nil, err
			}
			return decl, nil
		}
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
				// 保留字作为导出名合法：export { function } / export { default as X }
				if p.peek().Type == lexer.TokenKeyword {
					p.next()
					nameTok = lexer.Token{Type: lexer.TokenIdent, Value: p.tokens[p.pos-1].Value}
				} else {
					return nil, err
				}
			}
			spec := ast.ExportSpecifier{Local: nameTok.Value, Exported: nameTok.Value}
			if p.matchIdent("as") {
				exportedTok, err := p.expect(lexer.TokenIdent, "")
				if err != nil {
					if p.peek().Type == lexer.TokenKeyword {
						// export { X as default }：default 关键字作为导出名
						p.next()
						exportedTok = lexer.Token{Type: lexer.TokenIdent, Value: p.tokens[p.pos-1].Value}
					} else if p.peek().Type == lexer.TokenString {
						// ES2022：导出名可为字符串字面量——
						// export { jsTokens as "module.exports" }（js-tokens 等库使用）
						p.next()
						exportedTok = lexer.Token{Type: lexer.TokenIdent, Value: p.tokens[p.pos-1].Value}
					} else {
						return nil, err
					}
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
	// export abstract class Foo / export declare class Foo（abstract 是
	// TokenIdent，不能进上面的关键字分支）。
	if p.peek().Type == lexer.TokenIdent && p.peek().Value == "abstract" {
		next := p.peekAt(1)
		if next.Type == lexer.TokenKeyword && next.Value == "class" {
			p.next() // consume abstract
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
		// TypeScript: extends Super<T, U> —— 跳过泛型实参。
		if err := p.skipTypeParameters(); err != nil {
			return nil, nil, err
		}
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
// classKeyStart 判断 token 是否可作为类成员键名开头（ident/关键字/字符串/
// 数字/计算键 [）。用于区分 `get x()` 访问器与名为 get 的字段（get; get = 1）。
func classKeyStart(t lexer.Token) bool {
	switch t.Type {
	case lexer.TokenIdent, lexer.TokenKeyword, lexer.TokenString, lexer.TokenNumber:
		return true
	case lexer.TokenPunct:
		return t.Value == "[" || t.Value == "*"
	}
	return false
}

// containsArgumentsRef 判断静态块 AST 中是否存在 arguments 引用（规范
// ContainsArguments：引用位置命中即非法）。嵌套的普通函数/方法有独立的
// arguments 绑定，跳过其函数体；箭头函数沿用外层绑定，需继续深入。
// 非计算成员键/属性键/方法名等非引用位置不计数。
func containsArgumentsRef(n ast.Node) bool {
	found := false
	var walk func(ast.Node)
	// walkClassBody：非计算方法名/字段名不是引用；方法体是 FunctionExpr
	// （含独立 arguments 绑定，跳过）。
	walkClassBody := func(body *ast.ClassBody) {
		if body == nil {
			return
		}
		for i := range body.Methods {
			m := &body.Methods[i]
			if m.Computed {
				walk(m.Key)
			}
			walk(m.Init)
			if m.Value != nil {
				walk(m.Value)
			}
		}
	}
	walk = func(n ast.Node) {
		if found || n == nil {
			return
		}
		switch t := n.(type) {
		case *ast.Identifier:
			if t.Name == "arguments" {
				found = true
			}
			return
		case *ast.FunctionDecl, *ast.FunctionExpr:
			// 独立 arguments 绑定：内部引用合法。
			return
		case *ast.MemberExpr:
			walk(t.Object)
			if t.Computed {
				walk(t.Property)
			}
			return
		case *ast.ObjectLit:
			// 非计算属性键（{arguments: 1}）不是引用。
			for i := range t.Properties {
				p := &t.Properties[i]
				if p.Computed {
					walk(p.Key)
				}
				walk(p.Value)
				walk(p.Default)
			}
			return
		case *ast.ClassDecl:
			walkClassBody(t.Body)
			return
		case *ast.ClassExpr:
			walkClassBody(t.Body)
			return
		}
		ast.ForEachChild(n, func(c ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(n)
	return found
}

func (p *Parser) parseClassMember() (ast.MethodDefinition, error) {
	// TypeScript: skip leading `@decorator` expressions (parsed and discarded).
	if err := p.skipDecorators(); err != nil {
		var def ast.MethodDefinition
		return def, err
	}
	t := p.peek()
	var def ast.MethodDefinition
	def.Loc = posOf(t)

	// ES2022 类静态初始化块：`static { ... }`
	if (p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword) && p.peek().Value == "static" &&
		p.peekAt(1).Type == lexer.TokenPunct && p.peekAt(1).Value == "{" {
		p.next() // consume static
		block, err := p.parseBlock()
		if err != nil {
			return def, err
		}
		// 规范：静态块内不允许出现 arguments 引用（SyntaxError）。
		if containsArgumentsRef(block) {
			return def, p.errorf(t, "'arguments' is not allowed in class static initialization block")
		}
		def.Kind = ast.MethodStaticBlock
		def.Static = true
		def.Key = &ast.Identifier{Name: "__static_block__", Loc: posOf(t)}
		def.Value = &ast.FunctionExpr{
			Body: block,
			Loc:  posOf(t),
		}
		return def, nil
	}

	// TypeScript 可见性/修饰符前缀：private/protected/public/readonly/
	// abstract/override/declare + static（任意顺序，如 `private static readonly`）。
	// 仅当后随合法成员键时才作为修饰符消费，避免把普通方法名（如
	// `public() {}` 或 `readonly()`）误判。
	isStatic := false
	isAbstract := false
	for {
		tk := p.peek()
		if tk.Type != lexer.TokenIdent && tk.Type != lexer.TokenKeyword {
			break
		}
		v := tk.Value
		if v == "static" || v == "private" || v == "protected" || v == "public" ||
			v == "readonly" || v == "abstract" || v == "override" || v == "declare" {
			if classKeyStart(p.peekAt(1)) {
				if v == "static" {
					isStatic = true
				}
				if v == "abstract" {
					isAbstract = true
				}
				p.next() // consume modifier
				continue
			}
		}
		break
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

	// `*name() {}` / `async *name() {}` / `static *name() {}`——生成器方法。
	// `*` 不是合法属性名，出现即代表生成器标记。
	isGenerator := false
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "*" {
		p.next() // consume *
		isGenerator = true
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
		// method named "get"; `get;` / `get = 1` / `get:` 是名为 get 的字段。
		if classKeyStart(nx) {
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

	// TypeScript: 泛型方法 method<T extends X>(...)——跳过类型参数
	//（非 `<` 时安全返回，不影响普通方法/字段）。
	if err := p.skipTypeParameters(); err != nil {
		return def, err
	}

	// 抽象方法：abstract doRender(): void;——无函数体，跳过 (params) +
	// 返回类型注解 + 分号。以 MethodField（无 init）返回，编译器全部跳过。
	if isAbstract && p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
		if err := p.skipBalanced("(", ")"); err != nil {
			return def, err
		}
		if err := p.parseTypeAnnotation(); err != nil {
			return def, err
		}
		if err := p.consumeSemicolon(); err != nil {
			return def, err
		}
		def.Key = key
		def.Kind = ast.MethodField
		def.Computed = computed
		return def, nil
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

	// TypeScript 方法重载签名：method<T>(params): RetType;——无函数体，
	// 编译期擦除为 MethodField（不构造方法）。lookahead 探测并回溯。
	{
		savedPos := p.pos
		if p.isOverloadSignature() {
			def.Key = key
			def.Kind = ast.MethodField
			def.Computed = computed
			return def, nil
		}
		p.pos = savedPos
	}

	// Parse the method body using the standard function-params-and-body rule.
	p.genStack = append(p.genStack, isGenerator)
	p.asyncStack = append(p.asyncStack, isAsync)
	params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
	p.genStack = p.genStack[:len(p.genStack)-1]
	p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
	if err != nil {
		return def, err
	}
	fn := &ast.FunctionExpr{
		Params:        params,
		ParamPatterns: patterns,
		Defaults:      defaults,
		RestParam:     rest,
		Body:          body,
		IsAsync:       isAsync,
		IsGenerator:   isGenerator,
		Loc:           posOf(t),
	}
	def.Key = key
	def.Value = fn
	def.Kind = kind
	def.Computed = computed
	return def, nil
}

// isOverloadSignature 判断当前是否为方法重载签名（无函数体，以 ';' 结尾）：
//
//	method<T>(params): RetType;
//
// 从当前位置消费 (params) 与可选返回类型注解后，若下一 token 为 ';' 则返回 true
// 且位置停在 ';' 之后；否则返回 false（位置由调用方回溯）。
func (p *Parser) isOverloadSignature() bool {
	if err := p.skipBalanced("(", ")"); err != nil {
		return false
	}
	if err := p.parseTypeAnnotation(); err != nil {
		return false
	}
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == ";" {
		p.next() // consume ;
		return true
	}
	return false
}

