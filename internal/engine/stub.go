package engine

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// === 桩引擎 ================================================================
//
// Phase 0 临时实现：极简 JS 表达式求值器，仅用于验证端到端架构。
// 支持：
//   - 表达式语句（含 console.log/error/warn/info 调用）
//   - 字面量：number / string / true/false/null/undefined
//   - 二元运算：+ - * / %
//   - 一元运算：- !
//   - 括号、逗号分隔的参数列表
//   - 标识符查找（从 global 解析）
//   - 成员访问：obj.prop 与 obj[prop]
//   - 函数调用：fn(arg1, arg2, ...)
//
// 不支持（留给 Phase 1 自研 VM）：
//   - 变量声明、控制流、闭包、class、async/await 等
//   - ES2015+ 语法
//
// Phase 1 将用真正的 Lexer/Parser/Compiler/VM 替换本文件。

// NewStubEngine 创建桩引擎实例。
func NewStubEngine() Engine {
	return &stubEngine{}
}

type stubEngine struct{}

func (e *stubEngine) NewContext() (Context, error) {
	ctx := &stubContext{
		global: NewObject(),
	}
	return ctx, nil
}

func (e *stubEngine) Shutdown() error { return nil }

func (e *stubEngine) Version() string { return "stub-0.1.0" }

// stubContext 是桩引擎的执行上下文。
type stubContext struct {
	global Object
}

func (c *stubContext) Eval(code string, filename string) (Value, error) {
	// 1. 词法分析
	tokens, err := lex(code)
	if err != nil {
		return Undefined(), fmt.Errorf("%w: %s:%d:%d: %v", ErrSyntaxError, filename, 0, 0, err)
	}

	// 2. 语法分析 + 求值（单 pass）
	p := newParser(tokens)
	result, err := p.evalProgram(c.global)
	if err != nil {
		return Undefined(), err
	}
	if result == nil {
		return Undefined(), nil
	}
	return result, nil
}

func (c *stubContext) Global() Object { return c.global }

func (c *stubContext) RegisterFunc(name string, fn Func) error {
	return c.global.Set(name, NewFunction(name, fn))
}

func (c *stubContext) Close() error { return nil }

// PostTask 投递任务到 JS 执行线程（stub 直接同步执行）。
func (c *stubContext) PostTask(fn func()) { fn() }

// AddRef 跟踪活跃句柄（stub no-op）。
func (c *stubContext) AddRef() func() { return func() {} }

// FlushMicrotasks stub 无微任务队列，no-op。
func (c *stubContext) FlushMicrotasks() bool { return false }

// === 词法分析器 ============================================================

type tokenType int

const (
	tkEOF tokenType = iota
	tkNumber
	tkString
	tkIdent
	tkKeyword
	tkPunct // + - * / % ( ) , . [ ] { } ; ! = etc.
)

type token struct {
	typ  tokenType
	val  string
	line int
	col  int
}

// 关键字集合（Phase 0 仅识别字面量关键字）。
var keywords = map[string]bool{
	"true": true, "false": true, "null": true, "undefined": true,
}

// lex 词法分析：源码 → token 流。
func lex(src string) ([]token, error) {
	var tokens []token
	i := 0
	line, col := 1, 1

	advance := func(n int) {
		for k := 0; k < n; k++ {
			if i < len(src) && src[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
			i++
		}
	}

	for i < len(src) {
		ch := src[i]

		// 跳过空白
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			advance(1)
			continue
		}

		// 注释：// 行注释
		if ch == '/' && i+1 < len(src) && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				advance(1)
			}
			continue
		}
		// 注释：/* 块注释 */
		if ch == '/' && i+1 < len(src) && src[i+1] == '*' {
			advance(2)
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				advance(1)
			}
			if i+1 < len(src) {
				advance(2)
			}
			continue
		}

		// 数字字面量
		if ch >= '0' && ch <= '9' {
			start := i
			startCol := col
			for i < len(src) && (src[i] == '.' || (src[i] >= '0' && src[i] <= '9')) {
				advance(1)
			}
			tokens = append(tokens, token{tkNumber, src[start:i], line, startCol})
			continue
		}

		// 字符串字面量（单/双引号）
		if ch == '"' || ch == '\'' {
			quote := ch
			startCol := col
			advance(1) // 跳过开头引号
			var b strings.Builder
			for i < len(src) && src[i] != quote {
				if src[i] == '\\' && i+1 < len(src) {
					next := src[i+1]
					switch next {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					case '\\':
						b.WriteByte('\\')
					case '"':
						b.WriteByte('"')
					case '\'':
						b.WriteByte('\'')
					default:
						b.WriteByte(next)
					}
					advance(2)
				} else {
					b.WriteByte(src[i])
					advance(1)
				}
			}
			if i >= len(src) {
				return nil, fmt.Errorf("unterminated string at line %d", line)
			}
			advance(1) // 跳过结尾引号
			tokens = append(tokens, token{tkString, b.String(), line, startCol})
			continue
		}

		// 标识符 / 关键字
		if isIdentStart(ch) {
			start := i
			startCol := col
			for i < len(src) && isIdentPart(src[i]) {
				advance(1)
			}
			word := src[start:i]
			if keywords[word] {
				tokens = append(tokens, token{tkKeyword, word, line, startCol})
			} else {
				tokens = append(tokens, token{tkIdent, word, line, startCol})
			}
			continue
		}

		// 标点
		if isPunct(ch) {
			tokens = append(tokens, token{tkPunct, string(ch), line, col})
			advance(1)
			continue
		}

		return nil, fmt.Errorf("unexpected character %q at line %d:%d", ch, line, col)
	}
	tokens = append(tokens, token{tkEOF, "", line, col})
	return tokens, nil
}

func isIdentStart(ch byte) bool {
	return ch == '_' || ch == '$' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		unicode.IsLetter(rune(ch))
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func isPunct(ch byte) bool {
	return strings.ContainsRune("+-*/%(),.[]{};:!=<>&|", rune(ch))
}

// === 语法分析器 + 求值器（单 pass） ==========================================

type parser struct {
	tokens []token
	pos    int
}

func newParser(tokens []token) *parser {
	return &parser{tokens: tokens}
}

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{tkEOF, "", 0, 0}
}

func (p *parser) next() token {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) match(typ tokenType, val string) bool {
	t := p.peek()
	if t.typ == typ && (val == "" || t.val == val) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expect(typ tokenType, val string) (token, error) {
	t := p.peek()
	if t.typ != typ || (val != "" && t.val != val) {
		return t, fmt.Errorf("%w: expected %q but got %q at line %d", ErrSyntaxError, val, t.val, t.line)
	}
	p.pos++
	return t, nil
}

// evalProgram 求值整个程序，返回最后一个表达式的结果。
func (p *parser) evalProgram(global Object) (Value, error) {
	var last Value = Undefined()
	for p.peek().typ != tkEOF {
		// 跳过空语句
		for p.match(tkPunct, ";") {
		}

		if p.peek().typ == tkEOF {
			break
		}

		v, err := p.evalStatement(global)
		if err != nil {
			return nil, err
		}
		if v != nil {
			last = v
		}

		// 可选分号
		p.match(tkPunct, ";")
	}
	return last, nil
}

// evalStatement 求值单条语句。
// Phase 0 仅支持表达式语句。
func (p *parser) evalStatement(scope Object) (Value, error) {
	return p.evalExpression(scope)
}

// evalExpression 表达式入口（Pratt 风格按优先级分层）。
func (p *parser) evalExpression(scope Object) (Value, error) {
	return p.evalBinary(scope, 0)
}

// 二元运算优先级表。
var binPrecedence = map[string]int{
	"||": 1, "&&": 2,
	"==": 3, "!=": 3, "===": 3, "!==": 3,
	"<": 4, "<=": 4, ">": 4, ">=": 4,
	"+": 5, "-": 5,
	"*": 6, "/": 6, "%": 6,
}

// evalBinary 二元运算（含逻辑与/或、比较、算术）。
func (p *parser) evalBinary(scope Object, minPrec int) (Value, error) {
	left, err := p.evalUnary(scope)
	if err != nil {
		return nil, err
	}
	for {
		op, consumed, ok := p.peekBinaryOp()
		if !ok {
			break
		}
		prec, exists := binPrecedence[op]
		if !exists || prec < minPrec {
			break
		}
		p.pos += consumed

		right, err := p.evalBinary(scope, prec+1)
		if err != nil {
			return nil, err
		}
		left = applyBinaryOp(op, left, right)
	}
	return left, nil
}

// peekBinaryOp 探测当前位置的多字符二元运算符。
// 返回 (运算符, 占用 token 数, 是否为二元运算符)。
// 三字符优先：=== !==
// 二字符次之：== != <= >= && ||
// 单字符兜底：+ - * / % < > =
func (p *parser) peekBinaryOp() (string, int, bool) {
	if p.pos >= len(p.tokens) {
		return "", 0, false
	}
	t := p.tokens[p.pos]
	if t.typ != tkPunct {
		return "", 0, false
	}
	// 三字符：=== !==
	if p.pos+2 < len(p.tokens) {
		t1 := p.tokens[p.pos+1]
		t2 := p.tokens[p.pos+2]
		if t1.typ == tkPunct && t2.typ == tkPunct {
			three := t.val + t1.val + t2.val
			if three == "===" || three == "!==" {
				return three, 3, true
			}
		}
	}
	// 二字符：== != <= >= && ||
	if p.pos+1 < len(p.tokens) {
		t1 := p.tokens[p.pos+1]
		if t1.typ == tkPunct {
			two := t.val + t1.val
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||":
				return two, 2, true
			}
		}
	}
	// 单字符
	switch t.val {
	case "+", "-", "*", "/", "%", "<", ">", "=":
		return t.val, 1, true
	}
	return "", 0, false
}

// applyBinaryOp 执行二元运算。
func applyBinaryOp(op string, l, r Value) Value {
	switch op {
	case "+":
		// JS +：若任一方为字符串则连接，否则数字加
		if l.Type() == TypeString || r.Type() == TypeString {
			return ConcatStrings(l, r)
		}
		ln, lok := l.Float()
		rn, rok := r.Float()
		if lok && rok {
			return Number(ln + rn)
		}
		return Null() // 简化：非法操作
	case "-":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return Number(ln - rn)
	case "*":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return Number(ln * rn)
	case "/":
		ln, _ := l.Float()
		rn, _ := r.Float()
		if rn == 0 {
			if ln == 0 {
				return Null() // NaN 简化
			}
			return Null() // Infinity 简化
		}
		return Number(ln / rn)
	case "%":
		ln, _ := l.Float()
		rn, _ := r.Float()
		return Number(float64(int64(ln) % int64(rn)))
	case "==":
		return Boolean(valueEquals(l, r, false))
	case "!=":
		return Boolean(!valueEquals(l, r, false))
	case "===":
		return Boolean(valueEquals(l, r, true))
	case "!==":
		return Boolean(!valueEquals(l, r, true))
	case "<":
		return Boolean(compareValues(l, r) < 0)
	case "<=":
		return Boolean(compareValues(l, r) <= 0)
	case ">":
		return Boolean(compareValues(l, r) > 0)
	case ">=":
		return Boolean(compareValues(l, r) >= 0)
	case "&&":
		b, _ := l.Bool()
		if !b {
			return l
		}
		return r
	case "||":
		b, _ := l.Bool()
		if b {
			return l
		}
		return r
	}
	return Undefined()
}

// valueEquals 实现 JS == 与 === 比较。
func valueEquals(l, r Value, strict bool) bool {
	if strict {
		if l.Type() != r.Type() {
			return false
		}
		return strictEqual(l, r)
	}
	// 宽松比较（简化版）
	if l.Type() == r.Type() {
		return strictEqual(l, r)
	}
	// null == undefined
	if (l.IsNull() && r.IsUndefined()) || (l.IsUndefined() && r.IsNull()) {
		return true
	}
	// number/string 互转
	if l.Type() == TypeNumber && r.Type() == TypeString {
		if rf, ok := r.Float(); ok {
			ln, _ := l.Float()
			return ln == rf
		}
	}
	if l.Type() == TypeString && r.Type() == TypeNumber {
		if lf, ok := l.Float(); ok {
			rn, _ := r.Float()
			return lf == rn
		}
	}
	// bool → number
	if l.Type() == TypeBoolean {
		lb, _ := l.Bool()
		v := Number(0)
		if lb {
			v = Number(1)
		}
		return valueEquals(v, r, false)
	}
	if r.Type() == TypeBoolean {
		rb, _ := r.Bool()
		v := Number(0)
		if rb {
			v = Number(1)
		}
		return valueEquals(l, v, false)
	}
	return false
}

func strictEqual(l, r Value) bool {
	switch l.Type() {
	case TypeUndefined, TypeNull:
		return l.Type() == r.Type()
	case TypeBoolean:
		lb, _ := l.Bool()
		rb, _ := r.Bool()
		return lb == rb
	case TypeNumber:
		ln, _ := l.Float()
		rn, _ := r.Float()
		return ln == rn
	case TypeString:
		return l.String() == r.String()
	default:
		return l == r // 引用相等
	}
}

// compareValues 实现 < > <= >=（简化：仅数字与字符串）。
func compareValues(l, r Value) int {
	if l.Type() == TypeString && r.Type() == TypeString {
		return strings.Compare(l.String(), r.String())
	}
	ln, _ := l.Float()
	rn, _ := r.Float()
	if ln < rn {
		return -1
	}
	if ln > rn {
		return 1
	}
	return 0
}

// evalUnary 一元运算：- ! + 等。
func (p *parser) evalUnary(scope Object) (Value, error) {
	t := p.peek()
	if t.typ == tkPunct && (t.val == "-" || t.val == "!" || t.val == "+") {
		p.next()
		v, err := p.evalUnary(scope)
		if err != nil {
			return nil, err
		}
		switch t.val {
		case "-":
			n, _ := v.Float()
			return Number(-n), nil
		case "+":
			n, _ := v.Float()
			return Number(n), nil
		case "!":
			b, _ := v.Bool()
			return Boolean(!b), nil
		}
	}
	return p.evalPostfix(scope)
}

// evalPostfix 后缀：成员访问 + 函数调用。
func (p *parser) evalPostfix(scope Object) (Value, error) {
	v, err := p.evalPrimary(scope)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.typ == tkPunct && t.val == "." {
			p.next()
			propTok, err := p.expect(tkIdent, "")
			if err != nil {
				return nil, err
			}
			obj, ok := v.AsObject()
			if !ok {
				return nil, fmt.Errorf("%w: cannot read property %q of %s", ErrTypeError, propTok.val, v.Type())
			}
			v, err = obj.Get(propTok.val)
			if err != nil {
				return nil, err
			}
		} else if t.typ == tkPunct && t.val == "[" {
			p.next()
			idxVal, err := p.evalExpression(scope)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tkPunct, "]"); err != nil {
				return nil, err
			}
			obj, ok := v.AsObject()
			if !ok {
				return nil, fmt.Errorf("%w: cannot index %s", ErrTypeError, v.Type())
			}
			v, err = obj.Get(idxVal.String())
			if err != nil {
				return nil, err
			}
		} else if t.typ == tkPunct && t.val == "(" {
			// 函数调用
			p.next()
			args, err := p.parseArgs(scope)
			if err != nil {
				return nil, err
			}
			fn, ok := v.AsFunction()
			if !ok {
				return nil, fmt.Errorf("%w: %s is not a function", ErrTypeError, v.Type())
			}
			result, err := fn.Call(args)
			if err != nil {
				return nil, err
			}
			v = result
		} else {
			break
		}
	}
	return v, nil
}

// parseArgs 解析函数调用参数列表。
func (p *parser) parseArgs(scope Object) ([]Value, error) {
	var args []Value
	if p.peek().typ == tkPunct && p.peek().val == ")" {
		p.next()
		return args, nil
	}
	for {
		v, err := p.evalExpression(scope)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		if p.match(tkPunct, ",") {
			continue
		}
		if _, err := p.expect(tkPunct, ")"); err != nil {
			return nil, err
		}
		break
	}
	return args, nil
}

// evalPrimary 基础表达式：字面量、标识符、括号。
func (p *parser) evalPrimary(scope Object) (Value, error) {
	t := p.peek()
	switch t.typ {
	case tkNumber:
		p.next()
		if n, err := strconv.ParseFloat(t.val, 64); err == nil {
			return Number(n), nil
		}
		return nil, fmt.Errorf("invalid number %q", t.val)
	case tkString:
		p.next()
		return Str(t.val), nil
	case tkKeyword:
		p.next()
		switch t.val {
		case "true":
			return Boolean(true), nil
		case "false":
			return Boolean(false), nil
		case "null":
			return Null(), nil
		case "undefined":
			return Undefined(), nil
		}
		return nil, fmt.Errorf("unknown keyword %q", t.val)
	case tkIdent:
		p.next()
		v, err := scope.Get(t.val)
		if err != nil {
			return nil, err
		}
		return v, nil
	case tkPunct:
		if t.val == "(" {
			p.next()
			v, err := p.evalExpression(scope)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tkPunct, ")"); err != nil {
				return nil, err
			}
			return v, nil
		}
		if t.val == "[" {
			// 数组字面量
			p.next()
			var elems []Value
			if !(p.peek().typ == tkPunct && p.peek().val == "]") {
				for {
					v, err := p.evalExpression(scope)
					if err != nil {
						return nil, err
					}
					elems = append(elems, v)
					if p.match(tkPunct, ",") {
						continue
					}
					break
				}
			}
			if _, err := p.expect(tkPunct, "]"); err != nil {
				return nil, err
			}
			return NewArray(elems), nil
		}
		if t.val == "{" {
			// 对象字面量（简化：仅支持 key: value 形式，key 为标识符或字符串）
			p.next()
			obj := NewObject()
			if !(p.peek().typ == tkPunct && p.peek().val == "}") {
				for {
					kt := p.peek()
					var key string
					switch kt.typ {
					case tkIdent:
						key = kt.val
						p.next()
					case tkString:
						key = kt.val
						p.next()
					case tkNumber:
						key = kt.val
						p.next()
					default:
						return nil, fmt.Errorf("%w: invalid object key at line %d", ErrSyntaxError, kt.line)
					}
					if _, err := p.expect(tkPunct, ":"); err != nil {
						return nil, err
					}
					v, err := p.evalExpression(scope)
					if err != nil {
						return nil, err
					}
					if err := obj.Set(key, v); err != nil {
						return nil, err
					}
					if p.match(tkPunct, ",") {
						continue
					}
					break
				}
			}
			if _, err := p.expect(tkPunct, "}"); err != nil {
				return nil, err
			}
			return obj, nil
		}
	}
	return nil, fmt.Errorf("%w: unexpected token %q at line %d", ErrSyntaxError, t.val, t.line)
}
