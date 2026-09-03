package parser

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

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
		// 解构赋值：`({a, b} = x)` / `[a, b] = x`。解析器在此处已把 LHS
		// 按表达式解析成 ObjectLit/ArrayLit，转换为 Pattern 后两个后端
		// （编译器/解释器）统一按模式目标处理。
		return &ast.AssignExpr{Op: t.Value, Left: p.litToPattern(left), Right: right, Loc: posOf(t)}, nil
	}
	return left, nil
}

// litToPattern 把赋值 LHS 的 ObjectLit/ArrayLit 表达式转换为对应的
// ObjectPattern/ArrayPattern 绑定模式（ES2015 解构赋值）。
// 非字面量目标（标识符/成员访问等）原样返回。

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
			// 块体解析需要 async 上下文（await 合法性校验）。
			p.asyncStack = append(p.asyncStack, true)
			expr, ok, err := p.parseArrowBody(
				[]*ast.Identifier{{Name: t.Value, Loc: posOf(t)}}, nil, nil, nil)
			p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
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

	// `async (...) => ...`（允许返回类型注解 `: T`）。
	if t.Type == lexer.TokenPunct && t.Value == "(" {
		if endIdx, ok := p.findMatchingParen(p.pos); ok {
			if p.arrowAfterParen(endIdx) {
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

	// 泛型箭头：<T, U extends V>(key: K): T => body（表达式起始的 `<`
	// 只能是泛型参数，不可能是小于号）。跳过泛型后按 (…) => 路径解析。
	if t.Type == lexer.TokenPunct && t.Value == "<" {
		save := p.pos
		if err := p.skipTypeParameters(); err == nil {
			if p.peek().Type == lexer.TokenPunct && p.peek().Value == "(" {
				if endIdx, ok := p.findMatchingParen(p.pos); ok {
					if p.arrowAfterParen(endIdx) {
						return p.parseArrowWithParens()
					}
				}
			}
		}
		p.pos = save // 回退
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
		case "}":
			if depth > 0 {
				depth--
			}
		case ")", "]":
			if depth > 0 {
				depth--
			}
		case ">":
			if depth > 0 {
				depth--
			}
		case ">>", ">=":
			// 嵌套泛型闭合产生 `>>`（如 Pick<T, K>）——按两个 `>` 减深度。
			if depth > 0 {
				depth--
			}
			if depth > 0 {
				depth--
			}
		case ">>>":
			if depth > 0 {
				depth--
			}
			if depth > 0 {
				depth--
			}
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
		// TypeScript：`expr as T` / `expr satisfies T` 绑定在单目级——
		// 剥离后继续二元循环，使后续运算符（`??`/`+` 等）仍作用于断言结果
		// （`a as T ?? b` 即 `(a as T) ?? b`）。此前在 parseConditional 剥离
		// 发生在 parseBinary 返回后，`??` 无人认领报 "expected ';' but got '??'"。
		if t.Type == lexer.TokenIdent && (t.Value == "as" || t.Value == "satisfies") {
			p.next()
			if err := p.skipType(); err != nil {
				return nil, err
			}
			continue
		}
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
		// TS 非空断言：expr!（类型层操作，运行时无副作用）。
		// 注意 != / !== 是独立 token，不会误入。
		if t.Type == lexer.TokenPunct && t.Value == "!" {
			p.next()
			continue
		}
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
		// Only identifiers and member chains can carry type arguments; a
		// number literal followed by `<` is always a comparison and must
		// never be mis-skipped.
		if isGenericCallee(expr) {
			p.trySkipTypeArgs()
		}
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

func isGenericCallee(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.Identifier, *ast.MemberExpr, *ast.CallExpr, *ast.NewExpr,
		*ast.FunctionExpr, *ast.ClassExpr, *ast.ConditionalExpr,
		*ast.SequenceExpr, *ast.TaggedTemplateExpr, *ast.AwaitExpr,
		*ast.YieldExpr:
		return true
	}
	return false
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
			// Template interpolation is part of the surrounding grammar context.
			// Preserve async/generator and module state so await/yield are parsed
			// exactly as they would be outside the template literal.
			sub.asyncStack = append([]bool(nil), p.asyncStack...)
			sub.genStack = append([]bool(nil), p.genStack...)
			sub.allowTopLevelAwait = p.allowTopLevelAwait
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
			// import.meta —— 模块元数据。lower 为对内置全局 __importMeta() 的
			// 调用；后续 .url / .dirname / .resolve 由 parseCallMember 的
			// parseMemberTail 接续解析。__importMeta 由模块加载器在 setGlobals
			// 时注入（返回当前模块的元数据对象）。
			nx := p.peekAt(1)
			if nx.Type == lexer.TokenPunct && nx.Value == "." {
				p.next() // import
				p.next() // .
				prop := p.next()
				if prop.Type != lexer.TokenIdent || prop.Value != "meta" {
					return nil, p.errorf(prop, "expected 'meta' after 'import.'")
				}
				return &ast.CallExpr{
					Callee:    &ast.Identifier{Name: "__importMeta", Loc: posOf(prop)},
					Arguments: nil,
					Loc:       posOf(prop),
				}, nil
			}
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
		case "<":
			if p.isJSXStart() {
				return p.parseJSXPrimary()
			}
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
	// 函数名是 IdentifierName：允许上下文关键字（async/of/let 等），与
	// parseFunctionDecl 一致（`const f = function async() {}` 等写法）。
	if p.peek().Type == lexer.TokenIdent || p.peek().Type == lexer.TokenKeyword {
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
			generatorMethod := p.matchPunct("*")
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
				asyncGenerator := false
				if id.Name == "async" && p.peek().Type == lexer.TokenPunct && p.peek().Value == "*" {
					p.next()
					asyncGenerator = true
				}
				if id.Name == "async" && (asyncGenerator || p.peek().Type != lexer.TokenPunct || p.peek().Value == "[") {
					methodTok := p.peek()
					var methodKey ast.Expression
					methodComputed := false
					if methodTok.Type == lexer.TokenPunct && methodTok.Value == "[" {
						p.next()
						computedExpr, err := p.parseAssignment()
						if err != nil {
							return nil, err
						}
						if err := p.expectPunct("]"); err != nil {
							return nil, err
						}
						methodKey = computedExpr
						methodComputed = true
					} else if methodTok.Type == lexer.TokenIdent || methodTok.Type == lexer.TokenKeyword {
						p.next()
						methodKey = &ast.Identifier{Name: methodTok.Value, Loc: posOf(methodTok)}
					} else if methodTok.Type == lexer.TokenString {
						p.next()
						methodKey = &ast.StringLit{Value: methodTok.Value, Loc: posOf(methodTok)}
					} else {
						return nil, p.errorf(methodTok, "invalid async method name")
					}
					if err := p.skipTypeParameters(); err != nil {
						return nil, err
					}
					p.asyncStack = append(p.asyncStack, true)
					p.genStack = append(p.genStack, asyncGenerator)
					params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
					p.genStack = p.genStack[:len(p.genStack)-1]
					p.asyncStack = p.asyncStack[:len(p.asyncStack)-1]
					if err != nil {
						return nil, err
					}
					fn := &ast.FunctionExpr{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: body, IsAsync: true, IsGenerator: asyncGenerator, Loc: posOf(methodTok)}
					obj.Properties = append(obj.Properties, ast.Property{Key: methodKey, Value: fn, Kind: ast.PropertyMethod, Computed: methodComputed, Loc: posOf(propTok)})
					if !p.matchPunct(",") {
						break
					}
					continue
				}
				if (id.Name == "get" || id.Name == "set") &&
					(p.peek().Type != lexer.TokenPunct || p.peek().Value == "[") {
					// 实际是访问器：get prop() {} / get [prop]() {}
					methodTok := p.peek()
					var methodKey ast.Expression
					accessorComputed := false
					if methodTok.Type == lexer.TokenPunct && methodTok.Value == "[" {
						p.next() // [
						computedExpr, err := p.parseAssignment()
						if err != nil {
							return nil, err
						}
						if err := p.expectPunct("]"); err != nil {
							return nil, err
						}
						methodKey = computedExpr
						accessorComputed = true
					} else if methodTok.Type == lexer.TokenIdent || methodTok.Type == lexer.TokenKeyword {
						p.next()
						methodKey = &ast.Identifier{Name: methodTok.Value, Loc: posOf(methodTok)}
					} else if methodTok.Type == lexer.TokenString {
						p.next()
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
					obj.Properties = append(obj.Properties, ast.Property{Key: methodKey, Value: fn, Kind: kind, Computed: accessorComputed, Loc: posOf(propTok)})
					if !p.matchPunct(",") {
						break
					}
					continue
				}
			}
			// TypeScript generic object methods: `{ method<T>(value: T) {} }`.
			// Type parameters are erased before parsing the runtime signature.
			if err := p.skipTypeParameters(); err != nil {
				return nil, err
			}
			// 普通 init 或 method shorthand
			if generatorMethod || (p.peek().Type == lexer.TokenPunct && p.peek().Value == "(") {
				// method shorthand
				p.genStack = append(p.genStack, generatorMethod)
				params, patterns, defaults, rest, body, err := p.parseFuncParamsAndBody()
				p.genStack = p.genStack[:len(p.genStack)-1]
				if err != nil {
					return nil, err
				}
				fn := &ast.FunctionExpr{Params: params, ParamPatterns: patterns, Defaults: defaults, RestParam: rest, Body: body, IsGenerator: generatorMethod, Loc: posOf(propTok)}
				obj.Properties = append(obj.Properties, ast.Property{Key: key, Value: fn, Kind: ast.PropertyMethod, Computed: computedKey, Loc: posOf(propTok)})
			} else if p.matchPunct(":") {
				val, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				obj.Properties = append(obj.Properties, ast.Property{Key: key, Value: val, Kind: ast.PropertyInit, Computed: computedKey, Loc: posOf(propTok)})
			} else if id, ok := key.(*ast.Identifier); ok {
				// shorthand: { x } → { x: x }
				prop := ast.Property{Key: key, Value: id, Kind: ast.PropertyInit, Computed: computedKey, Loc: posOf(propTok)}
				// 解构默认值：{ x = 1 }（仅模式语境合法；表达式语境由后端报错）
				if p.peek().Type == lexer.TokenPunct && p.peek().Value == "=" {
					p.next()
					def, err := p.parseAssignment()
					if err != nil {
						return nil, err
					}
					prop.Default = def
				}
				obj.Properties = append(obj.Properties, prop)
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
