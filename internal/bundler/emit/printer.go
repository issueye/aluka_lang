// Package emit 实现 AST → JavaScript 源码打印器（aluka build --target=web 的核心）。
//
// 设计约束：
//   - 输出为 minify 形态（无缩进/换行），语句统一分号终止，杜绝 ASI 歧义；
//   - 表达式按 JS 优先级括号化，采取保守策略（必要时多括号，绝不丢语义）；
//   - NumberLit 优先使用 Raw（保留源码写法），折叠产物按值格式化；
//   - 单语句体一律花括号包裹（if/for/while），规避悬挂 else 类陷阱；
//   - 正确性以回读对拍保障（printer_test.go：parse→print→parse→print 幂等）。
package emit

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// Print 将 Program 打印为 JS 源码（minify 形态）。
func Print(prog *ast.Program) string {
	return PrintOpts(prog, PrintOptions{})
}

// PrintOptions 控制 web ESM 图打印：define 替换与 import 路径重写。
type PrintOptions struct {
	Defines        map[string]string
	RewriteImport  func(spec string) (next string, keep bool)
	RewriteDynamic func(spec string) string
}

// PrintOpts 按选项打印 Program。
func PrintOpts(prog *ast.Program, opts PrintOptions) string {
	p := &printer{
		sb:             &strings.Builder{},
		defines:        opts.Defines,
		rewriteImport:  opts.RewriteImport,
		rewriteDynamic: opts.RewriteDynamic,
	}
	for _, stmt := range prog.Body {
		p.stmt(stmt)
		p.sb.WriteByte(';')
	}
	return p.sb.String()
}

type printer struct {
	sb       *strings.Builder
	resolved map[string]string // import specifier → 模块 ID（nil 时原样保留；web bundle 用）
	ctx      *bundleCtx        // web bundle 跨模块上下文（可为 nil）
	dynamic  map[string]DynamicImport
	defines  map[string]string // 构建期 define：点分成员链 → 替换文本（web bundle 用）
	// rewriteImport 把静态 import/export-from 的 specifier 改写成产物路径。
	// keep=false 时省略该 import（CSS 已抽到独立样式表）。
	rewriteImport func(spec string) (next string, keep bool)
	// rewriteDynamic 把动态 import() 的字面量 specifier 改写成产物路径。
	rewriteDynamic func(spec string) string
}

func (p *printer) w(s string) { p.sb.WriteString(s) }

// ---------- 优先级 ----------

const (
	precLowest = iota
	precComma
	precAssign
	precCond
	precNullish
	precLogicalOr
	precLogicalAnd
	precBitOr
	precBitXor
	precBitAnd
	precEqual
	precCompare
	precShift
	precAdd
	precMul
	precExp
	precUnary
	precUpdate
	precCallMember
)

func binPrec(op string) int {
	switch op {
	case "**":
		return precExp
	case "*", "/", "%":
		return precMul
	case "+", "-":
		return precAdd
	case "<<", ">>", ">>>":
		return precShift
	case "<", "<=", ">", ">=", "in", "instanceof":
		return precCompare
	case "==", "!=", "===", "!==":
		return precEqual
	case "&":
		return precBitAnd
	case "^":
		return precBitXor
	case "|":
		return precBitOr
	case "&&":
		return precLogicalAnd
	case "||":
		return precLogicalOr
	case "??":
		return precNullish
	}
	return precCallMember
}

func isRightAssoc(op string) bool { return op == "**" }

func isWordOp(op string) bool {
	switch op {
	case "typeof", "void", "delete", "in", "instanceof":
		return true
	}
	return false
}

// expr 以「不低于 minPrec」的上下文打印表达式，必要时加括号。
func (p *printer) expr(e ast.Expression, minPrec int) {
	if e == nil {
		return
	}
	prec := exprPrec(e)
	paren := prec < minPrec
	if paren {
		p.w("(")
	}
	p.exprInner(e)
	if paren {
		p.w(")")
	}
}

func exprPrec(e ast.Expression) int {
	switch t := e.(type) {
	case *ast.SequenceExpr:
		return precComma
	case *ast.AssignExpr:
		return precAssign
	case *ast.ConditionalExpr:
		return precCond
	case *ast.LogicalExpr:
		return binPrec(t.Op)
	case *ast.BinaryExpr:
		return binPrec(t.Op)
	case *ast.UnaryExpr:
		return precUnary
	case *ast.UpdateExpr:
		if t.Prefix {
			return precUnary
		}
		return precUpdate
	case *ast.AwaitExpr:
		return precUnary
	case *ast.ArrowFunc, *ast.YieldExpr:
		return precAssign
	// 函数/类/对象字面量作为操作数（如 IIFE callee、new callee）必须括号化
	case *ast.FunctionExpr, *ast.ClassExpr, *ast.ObjectLit:
		return precAssign
	}
	return precCallMember
}

func defineMemberKey(e ast.Expression) (string, bool) {
	m, ok := e.(*ast.MemberExpr)
	if !ok || m.Computed || m.Optional {
		return "", false
	}
	prop, ok := m.Property.(*ast.Identifier)
	if !ok {
		return "", false
	}
	left, ok := defineMemberKey(m.Object)
	if ok {
		return left + "." + prop.Name, true
	}
	id, ok := m.Object.(*ast.Identifier)
	if !ok {
		return "", false
	}
	return id.Name + "." + prop.Name, true
}

func (p *printer) exprInner(e ast.Expression) {
	switch t := e.(type) {
	case *ast.Identifier:
		if value, ok := p.defines[t.Name]; ok {
			p.w(value)
		} else {
			p.w(t.Name)
		}

	case *ast.NumberLit:
		p.number(t)

	case *ast.BigIntLit:
		p.w(t.Text)
		p.w("n")

	case *ast.StringLit:
		p.string(t.Value)

	case *ast.BoolLit:
		if t.Value {
			p.w("true")
		} else {
			p.w("false")
		}

	case *ast.NullLit:
		p.w("null")

	case *ast.UndefinedLit:
		p.w("undefined")

	case *ast.RegexLit:
		p.w("/")
		p.w(t.Pattern)
		p.w("/")
		p.w(t.Flags)

	case *ast.TemplateLit:
		p.template(t)

	case *ast.TaggedTemplateExpr:
		p.expr(t.Tag, precCallMember)
		p.template(t.Template)

	case *ast.ArrayLit:
		p.w("[")
		for i, el := range t.Elements {
			if i > 0 {
				p.w(",")
			}
			p.arrayElement(el)
		}
		p.w("]")

	case *ast.ObjectLit:
		p.w("{")
		for i, prop := range t.Properties {
			if i > 0 {
				p.w(",")
			}
			p.property(prop)
		}
		p.w("}")

	case *ast.ThisExpr:
		p.w("this")

	case *ast.SuperExpr:
		p.w("super")

	case *ast.NewTargetExpr:
		p.w("new.target")

	case *ast.MemberExpr:
		if key, ok := defineMemberKey(t); ok {
			if value, found := p.defines[key]; found {
				p.w(value)
				return
			}
		}
		// `?.` 上下文中的对象必须可链式：new X().y 场景括号由 prec 处理
		p.expr(t.Object, precCallMember)
		if t.Optional {
			p.w("?.")
		} else if !t.Computed {
			p.w(".")
		}
		if t.Computed {
			// 计算成员：a[b] / a?.[b]（?. 分支已输出 "?."，此处不再加点）
			p.w("[")
			p.expr(t.Property, precLowest)
			p.w("]")
		} else {
			if id, ok := t.Property.(*ast.Identifier); ok {
				p.w(id.Name)
			} else {
				// 非标识符非计算成员不会出现（parser 约定），兜底按计算处理
				p.w("[")
				p.expr(t.Property, precLowest)
				p.w("]")
			}
		}

	case *ast.CallExpr:
		if id, ok := t.Callee.(*ast.Identifier); ok && id.Name == "__import" && len(t.Arguments) > 0 {
			if lit, ok := t.Arguments[0].(*ast.StringLit); ok {
				if p.rewriteDynamic != nil {
					if next := p.rewriteDynamic(lit.Value); next != "" {
						p.w("import(")
						p.string(next)
						p.w(")")
						return
					}
				}
				if d, found := p.dynamic[lit.Value]; found {
					p.w("__alukaImport(")
					p.string(d.Chunk)
					p.w(",")
					p.string(d.Target)
					p.w(")")
					return
				}
			}
		}
		// 可选调用 a?.(b) 的 callee 已含 ?.；普通调用 callee 需成员级优先级
		p.expr(t.Callee, precCallMember)
		if t.Optional {
			p.w("?.")
		}
		p.w("(")
		p.args(t.Arguments)
		p.w(")")

	case *ast.NewExpr:
		p.w("new ")
		p.expr(t.Callee, precCallMember)
		p.w("(")
		p.args(t.Arguments)
		p.w(")")

	case *ast.UnaryExpr:
		p.w(t.Op)
		if isWordOp(t.Op) {
			p.w(" ")
		}
		// typeof (a + b) 场景：参数按一元优先级，序列/条件需括号
		p.expr(t.Arg, precUnary)

	case *ast.UpdateExpr:
		if t.Prefix {
			p.w(t.Op)
			p.expr(t.Arg, precUnary)
		} else {
			p.expr(t.Arg, precUpdate)
			p.w(t.Op)
		}

	case *ast.BinaryExpr:
		// 加减号与符号字面量粘连（a - -b / a + +b）语义风险 → 运算符两侧留空格
		p.expr(t.Left, leftCtx(t.Op))
		p.w(" ")
		p.w(t.Op)
		if isWordOp(t.Op) {
			p.w(" ")
		} else {
			p.w(" ")
		}
		p.expr(t.Right, rightCtx(t.Op))

	case *ast.LogicalExpr:
		p.expr(t.Left, leftCtx(t.Op))
		p.w(" ")
		p.w(t.Op)
		p.w(" ")
		p.expr(t.Right, rightCtx(t.Op))

	case *ast.AssignExpr:
		p.node(t.Left)
		p.w(" ")
		p.w(t.Op)
		p.w(" ")
		p.expr(t.Right, precAssign)

	case *ast.ConditionalExpr:
		p.expr(t.Test, precCond)
		p.w("?")
		p.expr(t.Consequent, precAssign)
		p.w(":")
		p.expr(t.Alternate, precAssign)

	case *ast.SequenceExpr:
		for i, ex := range t.Expressions {
			if i > 0 {
				p.w(",")
			}
			p.expr(ex, precAssign)
		}

	case *ast.SpreadElement:
		p.w("...")
		p.expr(t.Arg, precAssign)

	case *ast.FunctionExpr:
		p.function(t.IsAsync, t.IsGenerator, t.Name, t.Params, t.ParamPatterns, t.Defaults, t.RestParam, t.Body)

	case *ast.ArrowFunc:
		if t.IsAsync {
			p.w("async ")
		}
		p.w("(")
		p.paramList(t.Params, t.ParamPatterns, t.Defaults, t.RestParam)
		p.w(")=>")
		if block, ok := t.Body.(*ast.BlockStmt); ok {
			p.block(block)
		} else if ex, ok := t.Body.(ast.Expression); ok {
			// 简写体为对象字面量/函数/类时必须括号：=>({...})，否则 { 会被
			// 解析为块语句体（源码括号不进 AST，需在此重建）
			switch ex.(type) {
			case *ast.ObjectLit, *ast.FunctionExpr, *ast.ClassExpr:
				p.w("(")
				p.expr(ex, precLowest)
				p.w(")")
			default:
				p.expr(ex, precComma)
			}
		}

	case *ast.ClassExpr:
		p.class(t.Name, t.SuperClass, t.Body)

	case *ast.AwaitExpr:
		p.w("await ")
		p.expr(t.Argument, precUnary)

	case *ast.YieldExpr:
		p.w("yield")
		if t.Delegate {
			p.w("*")
		}
		if t.Argument != nil {
			p.w(" ")
			p.expr(t.Argument, precAssign)
		}

	default:
		// 未知节点：显式报错而非静默丢弃（回读对拍会立即暴露）
		panic(fmt.Sprintf("emit: unsupported expression %T", e))
	}
}

func leftCtx(op string) int {
	if isRightAssoc(op) {
		return binPrec(op)
	}
	return binPrec(op) + 1
}

func rightCtx(op string) int {
	if isRightAssoc(op) {
		return binPrec(op) + 1
	}
	return binPrec(op)
}

// ---------- 语句 ----------

func (p *printer) stmt(s ast.Statement) {
	switch t := s.(type) {
	case *ast.VarDecl:
		p.w(t.Kind)
		p.w(" ")
		for i, d := range t.Decls {
			if i > 0 {
				p.w(",")
			}
			p.node(d.Name)
			if d.Pattern != nil {
				p.pattern(d.Pattern)
			}
			if d.Init != nil {
				p.w("=")
				p.expr(d.Init, precAssign)
			}
		}

	case *ast.FunctionDecl:
		p.function(t.IsAsync, t.IsGenerator, t.Name, t.Params, t.ParamPatterns, t.Defaults, t.RestParam, t.Body)

	case *ast.ClassDecl:
		p.class(t.Name, t.SuperClass, t.Body)

	case *ast.BlockStmt:
		p.block(t)

	case *ast.ExprStmt:
		// 语句起始的 { / function / class 会被解析为块/声明，必须括号化
		if stmtNeedsParens(t.Expr) {
			p.w("(")
			p.expr(t.Expr, precLowest)
			p.w(")")
		} else {
			p.expr(t.Expr, precLowest)
		}

	case *ast.EmptyStmt:
		// 空语句：外层统一分号已覆盖

	case *ast.IfStmt:
		p.w("if(")
		p.expr(t.Test, precLowest)
		p.w(")")
		p.stmtBody(t.Consequent)
		if t.Alternate != nil {
			p.w("else")
			p.stmtBody(t.Alternate)
		}

	case *ast.WhileStmt:
		p.w("while(")
		p.expr(t.Test, precLowest)
		p.w(")")
		p.stmtBody(t.Body)

	case *ast.DoWhileStmt:
		p.w("do")
		p.stmtBody(t.Body)
		p.w("while(")
		p.expr(t.Test, precLowest)
		p.w(")")

	case *ast.ForStmt:
		p.w("for(")
		if t.Init != nil {
			if vd, ok := t.Init.(*ast.VarDecl); ok {
				p.stmt(vd)
			} else {
				p.expr(t.Init.(ast.Expression), precComma)
			}
		}
		p.w(";")
		if t.Test != nil {
			p.expr(t.Test, precLowest)
		}
		p.w(";")
		if t.Update != nil {
			p.expr(t.Update, precComma)
		}
		p.w(")")
		p.stmtBody(t.Body)

	case *ast.ForInStmt:
		p.w("for(")
		p.forLeft(t.Left)
		p.w(" in ")
		p.expr(t.Right, precLowest)
		p.w(")")
		p.stmtBody(t.Body)

	case *ast.ForOfStmt:
		p.w("for")
		if t.IsAwait {
			p.w(" await")
		}
		p.w("(")
		p.forLeft(t.Left)
		p.w(" of ")
		p.expr(t.Right, precLowest)
		p.w(")")
		p.stmtBody(t.Body)

	case *ast.ReturnStmt:
		p.w("return")
		if t.Arg != nil {
			p.w(" ")
			p.expr(t.Arg, precLowest)
		}

	case *ast.BreakStmt:
		p.w("break")
		if t.Label != "" {
			p.w(" ")
			p.w(t.Label)
		}

	case *ast.ContinueStmt:
		p.w("continue")
		if t.Label != "" {
			p.w(" ")
			p.w(t.Label)
		}

	case *ast.ThrowStmt:
		p.w("throw ")
		p.expr(t.Arg, precLowest)

	case *ast.TryStmt:
		p.w("try")
		p.block(t.Block)
		if t.Handler != nil {
			p.w("catch")
			if t.Handler.Param != nil {
				p.w("(")
				p.w(t.Handler.Param.Name)
				p.w(")")
			}
			p.block(t.Handler.Body)
		}
		if t.Finally != nil {
			p.w("finally")
			p.block(t.Finally)
		}

	case *ast.SwitchStmt:
		p.w("switch(")
		p.expr(t.Disc, precLowest)
		p.w("){")
		for _, c := range t.Cases {
			if c.Test != nil {
				p.w("case ")
				p.expr(c.Test, precLowest)
				p.w(":")
			} else {
				p.w("default:")
			}
			for _, cs := range c.Consequent {
				p.stmt(cs)
				p.w(";")
			}
		}
		p.w("}")

	case *ast.LabeledStmt:
		p.w(t.Label)
		p.w(":")
		p.stmtBody(t.Body)

	case *ast.ImportDecl:
		p.importDecl(t)

	case *ast.ExportDecl:
		p.exportDecl(t)

	case *ast.ExportDefaultDecl:
		p.w("export default ")
		p.expr(t.Expression, precAssign)

	default:
		panic(fmt.Sprintf("emit: unsupported statement %T", s))
	}
}

// stmtNeedsParens 判断表达式作为语句开头是否需要括号包裹
// （对象字面量/函数表达式/类表达式，或以它们开头的赋值/序列/条件）。
func stmtNeedsParens(e ast.Expression) bool {
	switch t := e.(type) {
	case *ast.ObjectLit, *ast.FunctionExpr, *ast.ClassExpr:
		return true
	case *ast.AssignExpr:
		return stmtNeedsParensLeft(t.Left)
	case *ast.SequenceExpr:
		if len(t.Expressions) > 0 {
			return stmtNeedsParens(t.Expressions[0])
		}
	case *ast.ConditionalExpr:
		return stmtNeedsParens(t.Test)
	}
	return false
}

func stmtNeedsParensLeft(n ast.Node) bool {
	switch n.(type) {
	case *ast.ObjectPattern, *ast.ArrayPattern:
		return true
	}
	if ex, ok := n.(ast.Expression); ok {
		switch ex.(type) {
		case *ast.ObjectLit:
			return true
		}
	}
	return false
}

// stmtBody 打印控制流体：块语句原样，单语句统一花括号包裹。
func (p *printer) stmtBody(s ast.Statement) {
	if b, ok := s.(*ast.BlockStmt); ok {
		p.block(b)
		return
	}
	p.w("{")
	p.stmt(s)
	p.w(";")
	p.w("}")
}

func (p *printer) block(b *ast.BlockStmt) {
	p.w("{")
	for _, s := range b.Body {
		p.stmt(s)
		p.w(";")
	}
	p.w("}")
}

func (p *printer) forLeft(n ast.Node) {
	switch t := n.(type) {
	case *ast.VarDecl:
		p.stmt(t)
	case *ast.Identifier:
		p.w(t.Name)
	default:
		if pat, ok := n.(ast.Pattern); ok {
			p.pattern(pat)
			return
		}
		if ex, ok := n.(ast.Expression); ok {
			p.expr(ex, precLowest)
		}
	}
}

// node 打印 可能为 nil 的标识符。
func (p *printer) node(n ast.Node) {
	if n == nil {
		return
	}
	// typed-nil 防护：nil *Identifier 包装成接口后 != nil
	if id, ok := n.(*ast.Identifier); ok {
		if id == nil {
			return
		}
		p.w(id.Name)
		return
	}
	if pat, ok := n.(ast.Pattern); ok {
		p.pattern(pat)
		return
	}
	if ex, ok := n.(ast.Expression); ok {
		p.expr(ex, precLowest)
	}
}

// ---------- 函数 / 类 ----------

func (p *printer) function(isAsync, isGen bool, name *ast.Identifier,
	params []*ast.Identifier, patterns []ast.Pattern, defaults []ast.Expression,
	rest *ast.Identifier, body *ast.BlockStmt) {
	if isAsync {
		p.w("async ")
	}
	p.w("function")
	if isGen {
		p.w("*")
	}
	if name != nil {
		p.w(" ")
		p.w(name.Name)
	}
	p.w("(")
	p.paramList(params, patterns, defaults, rest)
	p.w(")")
	p.block(body)
}

func (p *printer) paramList(params []*ast.Identifier, patterns []ast.Pattern,
	defaults []ast.Expression, rest *ast.Identifier) {
	n := len(params)
	for i := 0; i < n; i++ {
		if i > 0 {
			p.w(",")
		}
		if patterns != nil && patterns[i] != nil {
			p.pattern(patterns[i])
		} else {
			p.w(params[i].Name)
		}
		if defaults != nil && defaults[i] != nil {
			p.w("=")
			p.expr(defaults[i], precAssign)
		}
	}
	if rest != nil {
		if n > 0 {
			p.w(",")
		}
		p.w("...")
		p.w(rest.Name)
	}
}

func (p *printer) class(name *ast.Identifier, superClass ast.Expression, body *ast.ClassBody) {
	p.w("class")
	if name != nil {
		p.w(" ")
		p.w(name.Name)
	}
	if superClass != nil {
		p.w(" extends ")
		p.expr(superClass, precCallMember)
	}
	p.w("{")
	for _, m := range body.Methods {
		p.methodDef(m)
	}
	p.w("}")
}

func (p *printer) methodDef(m ast.MethodDefinition) {
	if m.Static && m.Kind != ast.MethodStaticBlock {
		p.w("static ")
	}
	switch m.Kind {
	case ast.MethodStaticBlock:
		p.w("static")
		p.block(m.Value.Body)
		return
	case ast.MethodConstructor:
		p.w("constructor")
	case ast.MethodGetter:
		p.w("get ")
		p.key(m.Key, m.Computed)
	case ast.MethodSetter:
		p.w("set ")
		p.key(m.Key, m.Computed)
	case ast.MethodField:
		if m.Static {
			// static 前缀已输出
		}
		p.key(m.Key, m.Computed)
		if m.Init != nil {
			p.w("=")
			p.expr(m.Init, precAssign)
		}
		p.w(";")
		return
	default:
		if m.Value != nil && m.Value.IsAsync {
			p.w("async ")
		}
		if m.Value != nil && m.Value.IsGenerator {
			p.w("*")
		}
		p.key(m.Key, m.Computed)
	}
	if m.Value != nil {
		p.w("(")
		p.paramList(m.Value.Params, m.Value.ParamPatterns, m.Value.Defaults, m.Value.RestParam)
		p.w(")")
		p.block(m.Value.Body)
	}
}

func (p *printer) key(k ast.Expression, computed bool) {
	if computed {
		p.w("[")
		p.expr(k, precLowest)
		p.w("]")
		return
	}
	switch t := k.(type) {
	case *ast.Identifier:
		p.w(t.Name)
	case *ast.StringLit:
		p.string(t.Value)
	case *ast.NumberLit:
		p.number(t)
	default:
		p.w("[")
		p.expr(k, precLowest)
		p.w("]")
	}
}

// ---------- 对象字面量 / 数组元素 / 模式 ----------

func (p *printer) property(prop ast.Property) {
	switch prop.Kind {
	case ast.PropertySpread:
		p.w("...")
		p.expr(prop.Value, precAssign)
		return
	case ast.PropertyGet:
		p.w("get ")
		p.key(prop.Key, prop.Computed)
		if fe, ok := prop.Value.(*ast.FunctionExpr); ok {
			p.w("(")
			p.paramList(fe.Params, fe.ParamPatterns, fe.Defaults, fe.RestParam)
			p.w(")")
			p.block(fe.Body)
		}
		return
	case ast.PropertySet:
		p.w("set ")
		p.key(prop.Key, prop.Computed)
		if fe, ok := prop.Value.(*ast.FunctionExpr); ok {
			p.w("(")
			p.paramList(fe.Params, fe.ParamPatterns, fe.Defaults, fe.RestParam)
			p.w(")")
			p.block(fe.Body)
		}
		return
	case ast.PropertyMethod:
		if fe, ok := prop.Value.(*ast.FunctionExpr); ok {
			if fe.IsAsync {
				p.w("async ")
			}
			if fe.IsGenerator {
				p.w("*")
			}
		}
		p.key(prop.Key, prop.Computed)
		if fe, ok := prop.Value.(*ast.FunctionExpr); ok {
			p.w("(")
			p.paramList(fe.Params, fe.ParamPatterns, fe.Defaults, fe.RestParam)
			p.w(")")
			p.block(fe.Body)
		}
		return
	}
	// PropertyInit
	p.key(prop.Key, prop.Computed)
	p.w(":")
	p.expr(prop.Value, precAssign)
}

func (p *printer) arrayElement(el ast.Expression) {
	if el == nil {
		return // 数组空洞：[a,,b]
	}
	if sp, ok := el.(*ast.SpreadElement); ok {
		p.w("...")
		p.expr(sp.Arg, precAssign)
		return
	}
	p.expr(el, precAssign)
}

func (p *printer) args(list []ast.Expression) {
	for i, a := range list {
		if i > 0 {
			p.w(",")
		}
		p.arrayElement(a)
	}
}

func (p *printer) pattern(pat ast.Pattern) {
	switch t := pat.(type) {
	case *ast.Identifier:
		p.w(t.Name)
	case *ast.MemberExpr:
		p.expr(t, precLowest)
	case *ast.ArrayPattern:
		p.w("[")
		for i, el := range t.Elements {
			if i > 0 {
				p.w(",")
			}
			if el.IsRest {
				p.w("...")
			}
			if el.Target != nil {
				p.pattern(el.Target)
			}
			if el.Default != nil {
				p.w("=")
				p.expr(el.Default, precAssign)
			}
		}
		p.w("]")
	case *ast.ObjectPattern:
		p.w("{")
		for i, prop := range t.Properties {
			if i > 0 {
				p.w(",")
			}
			if prop.IsRest {
				p.w("...")
				p.pattern(prop.Value)
				continue
			}
			// 简写 {a} vs 展开 {a: b}
			shorthand := false
			if !prop.Computed {
				if idKey, ok := prop.Key.(*ast.Identifier); ok {
					if idVal, ok := prop.Value.(*ast.Identifier); ok && idVal.Name == idKey.Name {
						shorthand = true
					}
				}
			}
			if shorthand {
				p.pattern(prop.Value)
			} else {
				p.key(prop.Key, prop.Computed)
				p.w(":")
				p.pattern(prop.Value)
			}
			if prop.Default != nil {
				p.w("=")
				p.expr(prop.Default, precAssign)
			}
		}
		p.w("}")
	default:
		panic(fmt.Sprintf("emit: unsupported pattern %T", pat))
	}
}

// ---------- 字面量 ----------

func (p *printer) number(n *ast.NumberLit) {
	if n.Raw != "" {
		p.w(n.Raw)
		return
	}
	p.w(formatNumber(n.Value))
}

// formatNumber 将 float64 格式化为合法 JS 数字字面量。
func formatNumber(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "1e1000"
	}
	if math.IsInf(f, -1) {
		return "-1e1000"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func (p *printer) importDecl(d *ast.ImportDecl) {
	src := d.Source
	rewrote := false
	if p.rewriteImport != nil {
		next, keep := p.rewriteImport(src)
		if !keep {
			return
		}
		rewrote = next != src
		src = next
	}
	var defaultName, nsName string
	var named []string
	for _, spec := range d.Specifiers {
		switch spec.Imported {
		case "":
			defaultName = spec.Local
		case "*":
			nsName = spec.Local
		default:
			if spec.Imported == spec.Local {
				named = append(named, spec.Imported)
			} else {
				named = append(named, spec.Imported+" as "+spec.Local)
			}
		}
	}
	p.w("import")
	needFrom := defaultName != "" || nsName != "" || len(named) > 0
	if defaultName != "" {
		p.w(" ")
		p.w(defaultName)
	}
	if nsName != "" {
		if defaultName != "" {
			p.w(",")
		}
		p.w(" * as ")
		p.w(nsName)
	}
	if len(named) > 0 {
		if defaultName != "" || nsName != "" {
			p.w(",")
		} else {
			p.w(" ")
		}
		p.w("{")
		p.w(strings.Join(named, ","))
		p.w("}")
	}
	if needFrom {
		p.w(" from ")
	} else {
		p.w(" ")
	}
	p.string(src)
	if !rewrote {
		p.importAttrs(d.Attributes)
	}
}

func (p *printer) exportDecl(d *ast.ExportDecl) {
	src := d.Source
	if src != "" && p.rewriteImport != nil {
		next, keep := p.rewriteImport(src)
		if !keep {
			if d.Declaration != nil {
				p.stmt(d.Declaration)
			}
			return
		}
		src = next
	}
	p.w("export ")
	if d.Declaration != nil {
		p.stmt(d.Declaration)
		return
	}
	if d.IsStar {
		p.w("*")
		if d.StarName != "" {
			p.w(" as ")
			p.w(d.StarName)
		}
		if src != "" {
			p.w(" from ")
			p.string(src)
		}
		return
	}
	p.w("{")
	for i, spec := range d.Specifiers {
		if i > 0 {
			p.w(",")
		}
		p.w(spec.Local)
		if spec.Exported != spec.Local {
			p.w(" as ")
			p.w(spec.Exported)
		}
	}
	p.w("}")
	if src != "" {
		p.w(" from ")
		p.string(src)
	}
}

func (p *printer) importAttrs(attrs map[string]string) {
	if len(attrs) == 0 {
		return
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p.w(" with{")
	for i, k := range keys {
		if i > 0 {
			p.w(",")
		}
		p.w(k)
		p.w(":")
		p.string(attrs[k])
	}
	p.w("}")
}

func (p *printer) string(s string) {
	p.w("\"")
	for _, r := range s {
		switch r {
		case '"':
			p.w("\\\"")
		case '\\':
			p.w("\\\\")
		case '\n':
			p.w("\\n")
		case '\r':
			p.w("\\r")
		case '\t':
			p.w("\\t")
		default:
			if r < 0x20 || r == 0x2028 || r == 0x2029 {
				p.w(fmt.Sprintf("\\u%04x", r))
			} else {
				p.sb.WriteRune(r)
			}
		}
	}
	p.w("\"")
}

func (p *printer) template(t *ast.TemplateLit) {
	p.w("`")
	for i, q := range t.Quasis {
		// 反引号 / ${ / 反斜杠 需转义
		escaped := strings.ReplaceAll(q, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "`", "\\`")
		escaped = strings.ReplaceAll(escaped, "$", "\\$")
		p.w(escaped)
		if i < len(t.Expressions) {
			p.w("${")
			p.expr(t.Expressions[i], precLowest)
			p.w("}")
		}
	}
	p.w("`")
}
