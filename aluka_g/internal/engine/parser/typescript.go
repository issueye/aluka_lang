package parser

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

func (p *Parser) skipAmbientDecl() (ast.Statement, error) {
	kw := p.next() // function/const/let/var/class/enum/namespace/module/abstract
	switch kw.Value {
	case "function":
		// 函数名（可选）。
		if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword {
			p.next()
		}
		if err := p.skipTypeParameters(); err != nil {
			return nil, err
		}
		if err := p.skipBalanced("(", ")"); err != nil {
			return nil, err
		}
		if err := p.parseTypeAnnotation(); err != nil {
			return nil, err
		}
		if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
	case "class":
		// 类名 + 泛型 + 可选 extends + implements + 类体。
		if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword {
			p.next()
		}
		if err := p.skipTypeParameters(); err != nil {
			return nil, err
		}
		if p.matchKeyword("extends") {
			if _, err := p.parseCallMember(); err != nil {
				return nil, err
			}
			if err := p.skipTypeParameters(); err != nil {
				return nil, err
			}
		}
		if err := p.skipImplementsClause(); err != nil {
			return nil, err
		}
		if _, err := p.parseClassBody(); err != nil {
			return nil, err
		}
	case "namespace", "module":
		// namespace/module 名（标识符或字符串）+ 可选块体。
		if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenString {
			p.next()
		}
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
			if _, err := p.parseBlock(); err != nil {
				return nil, err
			}
		} else if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
	default:
		// const/let/var/enum/abstract：跳过绑定列表到分号。类型注解经
		// parseTypeAnnotation 正确处理（对象/数组类型中的 `{`/`[` 是类型
		// 而非代码块）。enum 声明体为 `{...}` 枚举成员，单独跳过。
		if err := p.skipTypeParameters(); err != nil {
			return nil, err
		}
		if kw.Value == "enum" {
			if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword {
				p.next() // 枚举名
			}
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
				if err := p.skipBalanced("{", "}"); err != nil {
					return nil, err
				}
			} else if err := p.consumeSemicolon(); err != nil {
				return nil, err
			}
			return &ast.EmptyStmt{Loc: posOf(p.peek())}, nil
		}
		for {
			// 绑定名（标识符）或解构模式。
			if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword {
				p.next()
			} else if p.peek().Type == lexer.TokenPunct && (p.peek().Value == "{" || p.peek().Value == "[") {
				open := p.peek().Value
				close := "]"
				if open == "{" {
					close = "}"
				}
				if err := p.skipBalanced(open, close); err != nil {
					return nil, err
				}
			}
			// 可选类型注解（declare 无初始化器）。
			if err := p.parseTypeAnnotation(); err != nil {
				return nil, err
			}
			if !p.matchPunct(",") {
				break
			}
		}
		if err := p.consumeSemicolon(); err != nil {
			return nil, err
		}
	}
	return &ast.EmptyStmt{Loc: posOf(p.peek())}, nil
}

// parseLabeled 解析标签语句 `name: statement`（如 OUTER: for (...) {...}）。

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
	// TypeScript assertion signatures:
	//
	//   function assert(value: unknown): asserts value { ... }
	//   function assertFoo(value: unknown): asserts value is Foo { ... }
	//
	// `asserts` and `is` are contextual identifiers. The asserted target may
	// also be `this`, which the lexer classifies as a keyword.
	if p.peek().Type == lexer.TokenIdent && p.peek().Value == "asserts" {
		p.next()
		target := p.peek()
		if target.Type != lexer.TokenIdent && !(target.Type == lexer.TokenKeyword && target.Value == "this") {
			return p.errorf(target, "expected assertion target after 'asserts'")
		}
		p.next()
		if p.peek().Type == lexer.TokenIdent && p.peek().Value == "is" {
			p.next()
			return p.skipType()
		}
		return nil
	}
	// TypeScript 类型谓词返回注解：(provider): provider is X => ...——
	// 跳过 `paramName is TypeExpr` 谓词部分。
	if p.peek().Type == lexer.TokenIdent {
		nx := p.peekAt(1)
		if nx.Type == lexer.TokenIdent && nx.Value == "is" {
			p.next() // 谓词参数名
			p.next() // is
			return p.skipType()
		}
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
	// 前导 union/intersection（多行风格 `type X =\n\t| "a" | "b"`）。
	for p.peek().Type == lexer.TokenPunct && (p.peek().Value == "|" || p.peek().Value == "&") {
		p.next()
	}
	if err := p.skipTypePrefix(); err != nil {
		return false, err
	}
	wasParen, wasAngle, err := p.skipTypeAtom()
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
	// 泛型函数类型：`<T>(args) => U`。类型位置的 `<...>` 后跟 `(` 必为函数
	// 类型参数列表；消费后置 wasParen，使末尾的 `=> 返回类型` 分支生效。
	if wasAngle && p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
		if err := p.skipBalanced("(", ")"); err != nil {
			return false, err
		}
		wasParen = true
		wasAngle = false
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
		// typeof 是词法关键字（其余前缀是 ident）。
		if t.Type == lexer.TokenKeyword && t.Value == "typeof" {
			p.next()
			continue
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
// skipTypeAtom skips one type atom: primitive, reference, literal, object,
// tuple, function, or parenthesised type. Returns whether it was a `(...)`
// (wasParen) and whether it was a generic `<...>` (wasAngle, for generic
// function types `<T>(args) => U`).
func (p *Parser) skipTypeAtom() (wasParen, wasAngle bool, err error) {
	t := p.peek()
	switch t.Type {
	case lexer.TokenString, lexer.TokenNumber:
		p.next()
	case lexer.TokenKeyword:
		if t.Value == "import" && p.peekAt(1).Type == lexer.TokenPunct && p.peekAt(1).Value == "(" {
			p.next() // import
			p.next() // (
			if _, err := p.expect(lexer.TokenString, ""); err != nil {
				return false, false, err
			}
			if err := p.expectPunct(")"); err != nil {
				return false, false, err
			}
			for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
				p.next()
				if _, err := p.expect(lexer.TokenIdent, ""); err != nil {
					return false, false, err
				}
			}
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "<" {
				if err := p.skipAngleBraces(); err != nil {
					return false, false, err
				}
			}
			break
		}
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
				return false, false, err
			}
		}
		// Generic type args: Foo<T, U>
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "<" {
			if err := p.skipAngleBraces(); err != nil {
				return false, false, err
			}
		}
	case lexer.TokenPunct:
		switch t.Value {
		case "(":
			// Function type (params) => ret OR parenthesised type.
			if err := p.skipBalanced("(", ")"); err != nil {
				return false, false, err
			}
			return true, false, nil
		case "{":
			// Object/mapped type.
			if err := p.skipBalanced("{", "}"); err != nil {
				return false, false, err
			}
		case "[":
			// Tuple type [T, U] or index-access type T[K].
			if err := p.skipBalanced("[", "]"); err != nil {
				return false, false, err
			}
		case "<":
			// Generic parameter list. In type position this is the head of a
			// generic function type `<T>(args) => U`.
			if err := p.skipAngleBraces(); err != nil {
				return false, false, err
			}
			return false, true, nil
		case "-":
			// `-` literal type (e.g. -1). Consume and expect a number.
			p.next()
			if p.peek().Type != lexer.TokenNumber {
				return false, false, p.errorf(p.peek(), "expected number after '-' in type")
			}
			p.next()
		default:
			return false, false, p.errorf(t, "unexpected token %q in type", t.Value)
		}
	case lexer.TokenTemplate:
		// Template literal type: `foo${T}bar`
		p.next()
	default:
		return false, false, p.errorf(t, "unexpected token %q in type", t.Value)
	}
	return false, false, nil
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
	// paren tracks parentheses opened inside the generic type arguments
	// (e.g. function types `<() => void>`). An unbalanced ')' at the top
	// level of the generic means the '<' was actually a comparison
	// (`if (1 / v < 0) return ...`), so the skip must fail and let the
	// caller backtrack instead of swallowing arbitrary source up to the
	// next '>'.
	paren := 0
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
			case "(":
				paren++
				p.next()
			case ")":
				if paren == 0 {
					return p.errorf(t, "unbalanced ')' in generic type")
				}
				paren--
				p.next()
			case ">":
				// A '>' inside parentheses is a comparison operator (e.g.
				// `a < ((b) > (c))`), not a generic closer; only a top-level
				// '>' closes the type-argument list.
				if paren > 0 && depth == 1 {
					p.next()
					continue
				}
				depth--
				p.next()
			case ">>":
				// Consume at most two generic closers. If the current generic
				// only needs one, preserve the second '>' for the outer JS
				// expression instead of inventing an extra closer.
				if paren > 0 && depth == 1 {
					p.next()
					continue
				}
				if depth >= 2 {
					depth -= 2
					p.next()
				} else {
					depth = 0
					p.tokens[p.pos] = lexer.Token{
						Type: lexer.TokenPunct, Value: ">", Line: t.Line, Col: t.Col,
					}
				}
			case ">>>":
				if paren > 0 && depth == 1 {
					p.next()
					continue
				}
				if depth >= 3 {
					depth -= 3
					p.next()
				} else {
					remaining := 3 - depth
					depth = 0
					p.tokens[p.pos] = lexer.Token{
						Type: lexer.TokenPunct, Value: strings.Repeat(">", remaining), Line: t.Line, Col: t.Col,
					}
				}
			case ">=":
				// T >= U shouldn't appear in type position; treat as `>` then `=`.
				if paren > 0 && depth == 1 {
					p.next()
					continue
				}
				depth--
				p.tokens[p.pos] = lexer.Token{
					Type: lexer.TokenPunct, Value: "=", Line: t.Line, Col: t.Col,
				}
			case ">>=":
				if paren > 0 && depth == 1 {
					p.next()
					continue
				}
				if depth >= 2 {
					depth -= 2
					p.tokens[p.pos] = lexer.Token{
						Type: lexer.TokenPunct, Value: "=", Line: t.Line, Col: t.Col,
					}
				} else {
					depth = 0
					p.tokens[p.pos] = lexer.Token{
						Type: lexer.TokenPunct, Value: ">=", Line: t.Line, Col: t.Col,
					}
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
