// Package ast defines JavaScript abstract syntax tree node types.
package ast

import "github.com/aluka-lang/aluka/internal/engine/lexer"

// Pos represents a source position.
type Pos struct {
	Line int
	Col  int
}

// Node is the common interface for all AST nodes.
type Node interface {
	Pos() Pos
	node()
}

// Statement is a statement node.
type Statement interface {
	Node
	stmtNode()
}

// Expression is an expression node.
type Expression interface {
	Node
	exprNode()
}

type Program struct {
	Body       []Statement
	SourceFile string
	Loc        Pos
}

func (p *Program) Pos() Pos { return p.Loc }
func (p *Program) node()    {}

type VarDecl struct {
	Kind  string
	Decls []VarDeclarator
	Loc   Pos
}

type VarDeclarator struct {
	Name    *Identifier // simple name (nil if Pattern is set)
	Pattern Pattern     // destructuring pattern (nil if Name is set)
	Init    Expression
}

func (d *VarDecl) Pos() Pos  { return d.Loc }
func (d *VarDecl) stmtNode() {}
func (d *VarDecl) node()     {}

type FunctionDecl struct {
	Name          *Identifier
	Params        []*Identifier
	ParamPatterns []Pattern    // 解构参数；nil 条目 = 普通参数。len 与 Params 一致
	Defaults      []Expression // ES2015 default values; nil entry = no default. len == len(Params)
	RestParam     *Identifier  // ES2015 rest param (`...rest`); nil if none
	Body          *BlockStmt
	IsAsync       bool
	IsGenerator   bool
	Loc           Pos
}

func (f *FunctionDecl) Pos() Pos  { return f.Loc }
func (f *FunctionDecl) stmtNode() {}
func (f *FunctionDecl) node()     {}

type BlockStmt struct {
	Body []Statement
	Loc  Pos
}

func (b *BlockStmt) Pos() Pos  { return b.Loc }
func (b *BlockStmt) stmtNode() {}
func (b *BlockStmt) node()     {}

type ExprStmt struct {
	Expr Expression
	Loc  Pos
}

func (e *ExprStmt) Pos() Pos  { return e.Loc }
func (e *ExprStmt) stmtNode() {}
func (e *ExprStmt) node()     {}

type EmptyStmt struct{ Loc Pos }

func (e *EmptyStmt) Pos() Pos  { return e.Loc }
func (e *EmptyStmt) stmtNode() {}
func (e *EmptyStmt) node()     {}

type IfStmt struct {
	Test       Expression
	Consequent Statement
	Alternate  Statement
	Loc        Pos
}

func (i *IfStmt) Pos() Pos  { return i.Loc }
func (i *IfStmt) stmtNode() {}
func (i *IfStmt) node()     {}

type WhileStmt struct {
	Test Expression
	Body Statement
	Loc  Pos
}

func (w *WhileStmt) Pos() Pos  { return w.Loc }
func (w *WhileStmt) stmtNode() {}
func (w *WhileStmt) node()     {}

type DoWhileStmt struct {
	Body Statement
	Test Expression
	Loc  Pos
}

func (d *DoWhileStmt) Pos() Pos  { return d.Loc }
func (d *DoWhileStmt) stmtNode() {}
func (d *DoWhileStmt) node()     {}

type ForStmt struct {
	Init   Node
	Test   Expression
	Update Expression
	Body   Statement
	Loc    Pos
}

func (f *ForStmt) Pos() Pos  { return f.Loc }
func (f *ForStmt) stmtNode() {}
func (f *ForStmt) node()     {}

type ForInStmt struct {
	Left  Node
	Right Expression
	Body  Statement
	Loc   Pos
}

func (f *ForInStmt) Pos() Pos  { return f.Loc }
func (f *ForInStmt) stmtNode() {}
func (f *ForInStmt) node()     {}

type ForOfStmt struct {
	Left    Node
	Right   Expression
	Body    Statement
	IsAwait bool
	Loc     Pos
}

func (f *ForOfStmt) Pos() Pos  { return f.Loc }
func (f *ForOfStmt) stmtNode() {}
func (f *ForOfStmt) node()     {}

type ReturnStmt struct {
	Arg Expression
	Loc Pos
}

func (r *ReturnStmt) Pos() Pos  { return r.Loc }
func (r *ReturnStmt) stmtNode() {}
func (r *ReturnStmt) node()     {}

type BreakStmt struct {
	Label string
	Loc   Pos
}

func (b *BreakStmt) Pos() Pos  { return b.Loc }
func (b *BreakStmt) stmtNode() {}
func (b *BreakStmt) node()     {}

type ContinueStmt struct {
	Label string
	Loc   Pos
}

func (c *ContinueStmt) Pos() Pos  { return c.Loc }
func (c *ContinueStmt) stmtNode() {}
func (c *ContinueStmt) node()     {}

type ThrowStmt struct {
	Arg Expression
	Loc Pos
}

func (t *ThrowStmt) Pos() Pos  { return t.Loc }
func (t *ThrowStmt) stmtNode() {}
func (t *ThrowStmt) node()     {}

type TryStmt struct {
	Block   *BlockStmt
	Handler *CatchHandler
	Finally *BlockStmt
	Loc     Pos
}

type CatchHandler struct {
	Param *Identifier
	Body  *BlockStmt
	Loc   Pos
}

func (t *TryStmt) Pos() Pos  { return t.Loc }
func (t *TryStmt) stmtNode() {}
func (t *TryStmt) node()     {}

type SwitchStmt struct {
	Disc  Expression
	Cases []SwitchCase
	Loc   Pos
}

type SwitchCase struct {
	Test       Expression
	Consequent []Statement
	Loc        Pos
}

func (s *SwitchStmt) Pos() Pos  { return s.Loc }
func (s *SwitchStmt) stmtNode() {}
func (s *SwitchStmt) node()     {}

type LabeledStmt struct {
	Label string
	Body  Statement
	Loc   Pos
}

func (l *LabeledStmt) Pos() Pos  { return l.Loc }
func (l *LabeledStmt) stmtNode() {}
func (l *LabeledStmt) node()     {}

type Identifier struct {
	Name string
	Loc  Pos
}

func (i *Identifier) Pos() Pos     { return i.Loc }
func (i *Identifier) exprNode()    {}
func (i *Identifier) stmtNode()    {}
func (i *Identifier) patternNode() {}
func (i *Identifier) node()        {}

type NumberLit struct {
	Value float64
	Raw   string
	Loc   Pos
}

func (n *NumberLit) Pos() Pos  { return n.Loc }
func (n *NumberLit) exprNode() {}
func (n *NumberLit) node()     {}

// BigIntLit 表示 BigInt 字面量（ES2020），如 123n、0xFFn。
type BigIntLit struct {
	Text string // 十进制整数字符串（已去掉 n 后缀，用于编译期 math/big 解析）
	Loc  Pos
}

func (n *BigIntLit) Pos() Pos  { return n.Loc }
func (n *BigIntLit) exprNode() {}
func (n *BigIntLit) node()     {}

type StringLit struct {
	Value string
	Loc   Pos
}

func (s *StringLit) Pos() Pos  { return s.Loc }
func (s *StringLit) exprNode() {}
func (s *StringLit) node()     {}

type BoolLit struct {
	Value bool
	Loc   Pos
}

func (b *BoolLit) Pos() Pos  { return b.Loc }
func (b *BoolLit) exprNode() {}
func (b *BoolLit) node()     {}

type NullLit struct{ Loc Pos }

func (n *NullLit) Pos() Pos  { return n.Loc }
func (n *NullLit) exprNode() {}
func (n *NullLit) node()     {}

type UndefinedLit struct{ Loc Pos }

func (u *UndefinedLit) Pos() Pos  { return u.Loc }
func (u *UndefinedLit) exprNode() {}
func (u *UndefinedLit) node()     {}

type RegexLit struct {
	Pattern string
	Flags   string
	Loc     Pos
}

func (r *RegexLit) Pos() Pos  { return r.Loc }
func (r *RegexLit) exprNode() {}
func (r *RegexLit) node()     {}

type TemplateLit struct {
	// Quasis are the cooked literal string segments between interpolations.
	// len(Quasis) == len(Expressions) + 1.
	Quasis []string
	// RawQuasis are the raw (unescaped) literal string segments, used by
	// tagged templates to build the strings.raw array. Aligned 1:1 with Quasis.
	RawQuasis []string
	// Expressions are the interpolated ${...} expressions.
	Expressions []Expression
	Loc         Pos
}

func (t *TemplateLit) Pos() Pos  { return t.Loc }
func (t *TemplateLit) exprNode() {}
func (t *TemplateLit) node()     {}

// TaggedTemplateExpr 是标记模板字面量 `tag`a${x}b“。
// tag 可以是任意表达式（标识符/成员访问/调用结果等）。
type TaggedTemplateExpr struct {
	Tag      Expression
	Template *TemplateLit
	Loc      Pos
}

func (t *TaggedTemplateExpr) Pos() Pos  { return t.Loc }
func (t *TaggedTemplateExpr) exprNode() {}
func (t *TaggedTemplateExpr) node()     {}

type ArrayLit struct {
	Elements []Expression
	Loc      Pos
}

func (a *ArrayLit) Pos() Pos  { return a.Loc }
func (a *ArrayLit) exprNode() {}
func (a *ArrayLit) node()     {}

type ObjectLit struct {
	Properties []Property
	Loc        Pos
}

type Property struct {
	Key      Expression
	Value    Expression
	Kind     PropertyKind
	Computed bool
	Loc      Pos
}

type PropertyKind int

const (
	PropertyInit PropertyKind = iota
	PropertyGet
	PropertySet
	PropertyMethod
	PropertySpread
)

func (o *ObjectLit) Pos() Pos  { return o.Loc }
func (o *ObjectLit) exprNode() {}
func (o *ObjectLit) node()     {}

type ThisExpr struct{ Loc Pos }

func (t *ThisExpr) Pos() Pos  { return t.Loc }
func (t *ThisExpr) exprNode() {}
func (t *ThisExpr) node()     {}

type SuperExpr struct{ Loc Pos }

func (s *SuperExpr) Pos() Pos  { return s.Loc }
func (s *SuperExpr) exprNode() {}
func (s *SuperExpr) node()     {}

type MemberExpr struct {
	Object   Expression
	Property Expression
	Computed bool
	Optional bool
	Loc      Pos
}

func (m *MemberExpr) Pos() Pos  { return m.Loc }
func (m *MemberExpr) exprNode() {}
func (m *MemberExpr) node()     {}

type CallExpr struct {
	Callee    Expression
	Arguments []Expression
	Optional  bool
	Loc       Pos
}

func (c *CallExpr) Pos() Pos  { return c.Loc }
func (c *CallExpr) exprNode() {}
func (c *CallExpr) node()     {}

type NewExpr struct {
	Callee    Expression
	Arguments []Expression
	Loc       Pos
}

func (n *NewExpr) Pos() Pos  { return n.Loc }
func (n *NewExpr) exprNode() {}
func (n *NewExpr) node()     {}

type UnaryExpr struct {
	Op  string
	Arg Expression
	Loc Pos
}

func (u *UnaryExpr) Pos() Pos  { return u.Loc }
func (u *UnaryExpr) exprNode() {}
func (u *UnaryExpr) node()     {}

type UpdateExpr struct {
	Op     string
	Arg    Expression
	Prefix bool
	Loc    Pos
}

func (u *UpdateExpr) Pos() Pos  { return u.Loc }
func (u *UpdateExpr) exprNode() {}
func (u *UpdateExpr) node()     {}

type BinaryExpr struct {
	Op    string
	Left  Expression
	Right Expression
	Loc   Pos
}

func (b *BinaryExpr) Pos() Pos  { return b.Loc }
func (b *BinaryExpr) exprNode() {}
func (b *BinaryExpr) node()     {}

type LogicalExpr struct {
	Op    string
	Left  Expression
	Right Expression
	Loc   Pos
}

func (l *LogicalExpr) Pos() Pos  { return l.Loc }
func (l *LogicalExpr) exprNode() {}
func (l *LogicalExpr) node()     {}

type AssignExpr struct {
	Op    string
	Left  Expression
	Right Expression
	Loc   Pos
}

func (a *AssignExpr) Pos() Pos  { return a.Loc }
func (a *AssignExpr) exprNode() {}
func (a *AssignExpr) node()     {}

type ConditionalExpr struct {
	Test       Expression
	Consequent Expression
	Alternate  Expression
	Loc        Pos
}

func (c *ConditionalExpr) Pos() Pos  { return c.Loc }
func (c *ConditionalExpr) exprNode() {}
func (c *ConditionalExpr) node()     {}

type SequenceExpr struct {
	Expressions []Expression
	Loc         Pos
}

func (s *SequenceExpr) Pos() Pos  { return s.Loc }
func (s *SequenceExpr) exprNode() {}
func (s *SequenceExpr) node()     {}

type SpreadElement struct {
	Arg Expression
	Loc Pos
}

func (s *SpreadElement) Pos() Pos  { return s.Loc }
func (s *SpreadElement) exprNode() {}
func (s *SpreadElement) node()     {}

type FunctionExpr struct {
	Name          *Identifier
	Params        []*Identifier
	ParamPatterns []Pattern    // 解构参数；nil 条目 = 普通参数。len 与 Params 一致
	Defaults      []Expression // ES2015 default values; nil entry = no default. len == len(Params)
	RestParam     *Identifier  // ES2015 rest param (`...rest`); nil if none
	Body          *BlockStmt
	IsAsync       bool
	IsGenerator   bool
	Loc           Pos
}

func (f *FunctionExpr) Pos() Pos  { return f.Loc }
func (f *FunctionExpr) exprNode() {}
func (f *FunctionExpr) node()     {}

type ArrowFunc struct {
	Params        []*Identifier
	ParamPatterns []Pattern    // 解构参数；nil 条目 = 普通参数。len 与 Params 一致
	Defaults      []Expression // ES2015 default values; nil entry = no default. len == len(Params)
	RestParam     *Identifier  // ES2015 rest param (`...rest`); nil if none
	Body          Node
	IsAsync       bool
	Loc           Pos
}

func (a *ArrowFunc) Pos() Pos  { return a.Loc }
func (a *ArrowFunc) exprNode() {}
func (a *ArrowFunc) node()     {}

type NewTargetExpr struct{ Loc Pos }

func (n *NewTargetExpr) Pos() Pos  { return n.Loc }
func (n *NewTargetExpr) exprNode() {}
func (n *NewTargetExpr) node()     {}

// YieldExpr is a `yield` expression (only valid inside generator functions).
//
//	yield            -> Argument == nil
//	yield expr       -> Argument = expr
//	yield* expr      -> Delegate = true, Argument = expr
type YieldExpr struct {
	Argument Expression
	Delegate bool // `yield*`
	Loc      Pos
}

func (y *YieldExpr) Pos() Pos  { return y.Loc }
func (y *YieldExpr) exprNode() {}
func (y *YieldExpr) node()     {}

// AwaitExpr is `await expr` — only valid inside an async function.
type AwaitExpr struct {
	Argument Expression
	Loc      Pos
}

func (a *AwaitExpr) Pos() Pos  { return a.Loc }
func (a *AwaitExpr) exprNode() {}
func (a *AwaitExpr) node()     {}

// === Class declarations (ES2015) ==========================================

// MethodKind classifies a class method definition.
type MethodKind int

const (
	MethodNormal MethodKind = iota
	MethodConstructor
	MethodGetter
	MethodSetter
	MethodField // class field declaration (ES2022 / TS): `x = init;` or `x: T;`
)

// MethodDefinition is one entry in a class body: a constructor, method,
// getter, setter, or field declaration (optionally static).
type MethodDefinition struct {
	Key      Expression // Identifier / StringLit / NumberLit / computed expr
	Value    *FunctionExpr
	Kind     MethodKind
	Static   bool
	Computed bool
	Loc      Pos
	// Init is the initializer expression for a class field (Kind == MethodField).
	// Nil means the field is declared without an initializer (sets to undefined).
	Init Expression
}

// ClassBody is the { ... } part of a class, holding method definitions.
type ClassBody struct {
	Methods []MethodDefinition
	Loc     Pos
}

// ClassDecl is a class declaration: `class Name [extends Super] { body }`.
type ClassDecl struct {
	Name       *Identifier
	SuperClass Expression // nil if no extends
	Body       *ClassBody
	Loc        Pos
}

func (c *ClassDecl) Pos() Pos  { return c.Loc }
func (c *ClassDecl) stmtNode() {}
func (c *ClassDecl) node()     {}

// ClassExpr is a class expression: `class [Name] [extends Super] { body }`.
type ClassExpr struct {
	Name       *Identifier
	SuperClass Expression
	Body       *ClassBody
	Loc        Pos
}

func (c *ClassExpr) Pos() Pos  { return c.Loc }
func (c *ClassExpr) exprNode() {}
func (c *ClassExpr) node()     {}

// === Destructuring patterns (ES2015) ======================================

// Pattern is a binding pattern for destructuring declarations.
type Pattern interface {
	Node
	patternNode()
}

// ArrayPatternElement represents one slot in an array destructuring pattern.
// A nil Target indicates a hole (elision). IsRest marks a `...rest` binding.
type ArrayPatternElement struct {
	Target  Pattern    // nil = hole
	Default Expression // nil = no default
	IsRest  bool       // ...rest (must be last element)
}

// ArrayPattern: [a, b, , c, ...rest]
type ArrayPattern struct {
	Elements []ArrayPatternElement
	Loc      Pos
}

func (a *ArrayPattern) Pos() Pos     { return a.Loc }
func (a *ArrayPattern) patternNode() {}
func (a *ArrayPattern) node()        {}

// ObjectPatternProperty represents one binding in an object destructuring
// pattern.
type ObjectPatternProperty struct {
	Key      Expression // property name or computed key expression
	Value    Pattern    // binding target
	Default  Expression // nil = no default
	IsRest   bool       // ...rest (must be last)
	Computed bool       // {[expr]: target}
}

// ObjectPattern: {a, b: c, ...rest}
type ObjectPattern struct {
	Properties []ObjectPatternProperty
	Loc        Pos
}

func (o *ObjectPattern) Pos() Pos     { return o.Loc }
func (o *ObjectPattern) patternNode() {}
func (o *ObjectPattern) node()        {}

// PosFromToken builds a Pos from a lexer.Token.
func PosFromToken(t lexer.Token) Pos {
	return Pos{Line: t.Line, Col: t.Col}
}

// --- ESM import/export nodes --------------------------------------------

// ImportSpecifier describes one binding imported from a module.
type ImportSpecifier struct {
	Imported string // imported name; "" = default import, "*" = namespace import
	Local    string // local binding name
}

// ImportDecl represents an ESM import declaration:
//
//	import 'mod'
//	import x from 'mod'
//	import * as ns from 'mod'
//	import {a, b as c} from 'mod'
//	import x, {a, b} from 'mod'
//	import data from './d.json' with { type: 'json' }
type ImportDecl struct {
	Source     string            // module specifier (string literal)
	Specifiers []ImportSpecifier // empty for side-effect-only import
	Attributes map[string]string // import attributes（with { type: 'json' }）
	Loc        Pos
}

func (d *ImportDecl) Pos() Pos  { return d.Loc }
func (d *ImportDecl) stmtNode() {}
func (d *ImportDecl) node()     {}

// ExportSpecifier describes one name exported from the current module.
type ExportSpecifier struct {
	Local    string // local name (what's being exported)
	Exported string // exported name (may differ via `as`)
}

// ExportDecl represents an ESM export declaration:
//
//	export {a, b as c}
//	export {a, b} from 'mod'
//	export * from 'mod'
//	export var x = 1
//	export function f() {}
//	export class C {}
type ExportDecl struct {
	Declaration Statement         // non-nil for `export <decl>` (VarDecl/FunctionDecl/ClassDecl)
	Specifiers  []ExportSpecifier // non-empty for `export {a, b}`
	Source      string            // non-empty for re-export `export {a} from 'mod'`
	IsStar      bool              // `export * from 'mod'`
	StarName    string            // `export * as ns from 'mod'` 的命名空间名（非空时生效）
	Loc         Pos
}

func (d *ExportDecl) Pos() Pos  { return d.Loc }
func (d *ExportDecl) stmtNode() {}
func (d *ExportDecl) node()     {}

// ExportDefaultDecl represents `export default <expr>`.
type ExportDefaultDecl struct {
	Expression Expression // the default export expression
	Loc        Pos
}

func (d *ExportDefaultDecl) Pos() Pos  { return d.Loc }
func (d *ExportDefaultDecl) stmtNode() {}
func (d *ExportDefaultDecl) node()     {}

// HasTopLevelAwait 报告程序顶层（模块级）是否含 await 表达式。
// 不深入嵌套函数体（函数内的 await 不是 TLA），但深入语句内的表达式
// （表达式语句、声明初始化、if/for/return 等分支）。
func HasTopLevelAwait(prog *Program) bool {
	for _, stmt := range prog.Body {
		if stmtHasAwait(stmt) {
			return true
		}
	}
	return false
}

// stmtHasAwait 判断语句中（非嵌套函数内）是否出现 await。
func stmtHasAwait(s Statement) bool {
	switch n := s.(type) {
	case *ExprStmt:
		return exprHasAwait(n.Expr)
	case *VarDecl:
		for _, d := range n.Decls {
			if d.Init != nil && exprHasAwait(d.Init) {
				return true
			}
		}
		return false
	case *ReturnStmt:
		return n.Arg != nil && exprHasAwait(n.Arg)
	case *IfStmt:
		if exprHasAwait(n.Test) || stmtHasAwait(n.Consequent) {
			return true
		}
		return n.Alternate != nil && stmtHasAwait(n.Alternate)
	case *WhileStmt:
		return exprHasAwait(n.Test) || stmtHasAwait(n.Body)
	case *DoWhileStmt:
		return exprHasAwait(n.Test) || stmtHasAwait(n.Body)
	case *ForStmt:
		if n.Test != nil && exprHasAwait(n.Test) {
			return true
		}
		if n.Update != nil && exprHasAwait(n.Update) {
			return true
		}
		return stmtHasAwait(n.Body)
	case *ForInStmt:
		if exprHasAwait(n.Right) {
			return true
		}
		return stmtHasAwait(n.Body)
	case *ForOfStmt:
		if n.IsAwait || exprHasAwait(n.Right) {
			return true
		}
		return stmtHasAwait(n.Body)
	case *BlockStmt:
		for _, b := range n.Body {
			if stmtHasAwait(b) {
				return true
			}
		}
		return false
	case *SwitchStmt:
		if exprHasAwait(n.Disc) {
			return true
		}
		for _, c := range n.Cases {
			if c.Test != nil && exprHasAwait(c.Test) {
				return true
			}
			for _, b := range c.Consequent {
				if stmtHasAwait(b) {
					return true
				}
			}
		}
		return false
	case *TryStmt:
		if stmtHasAwait(n.Block) {
			return true
		}
		if n.Handler != nil && stmtHasAwait(n.Handler.Body) {
			return true
		}
		return n.Finally != nil && stmtHasAwait(n.Finally)
	case *LabeledStmt:
		return stmtHasAwait(n.Body)
	case *ThrowStmt:
		return n.Arg != nil && exprHasAwait(n.Arg)
	case *FunctionDecl:
		return false // 嵌套函数体不算 TLA
	default:
		return false
	}
}

// exprHasAwait 判断表达式（不深入函数表达式体）中是否出现 await。
func exprHasAwait(e Expression) bool {
	switch n := e.(type) {
	case *AwaitExpr:
		return true
	case *UnaryExpr:
		return exprHasAwait(n.Arg)
	case *BinaryExpr:
		return exprHasAwait(n.Left) || exprHasAwait(n.Right)
	case *LogicalExpr:
		return exprHasAwait(n.Left) || exprHasAwait(n.Right)
	case *AssignExpr:
		if exprHasAwait(n.Left) {
			return true
		}
		return n.Right != nil && exprHasAwait(n.Right)
	case *UpdateExpr:
		return exprHasAwait(n.Arg)
	case *ConditionalExpr:
		return exprHasAwait(n.Test) || exprHasAwait(n.Consequent) || exprHasAwait(n.Alternate)
	case *CallExpr:
		if exprHasAwait(n.Callee) {
			return true
		}
		for _, a := range n.Arguments {
			if exprHasAwait(a) {
				return true
			}
		}
		return false
	case *MemberExpr:
		if exprHasAwait(n.Object) {
			return true
		}
		return n.Property != nil && exprHasAwait(n.Property)
	case *NewExpr:
		if exprHasAwait(n.Callee) {
			return true
		}
		for _, a := range n.Arguments {
			if exprHasAwait(a) {
				return true
			}
		}
		return false
	case *SequenceExpr:
		for _, e := range n.Expressions {
			if exprHasAwait(e) {
				return true
			}
		}
		return false
	case *ArrayLit:
		for _, e := range n.Elements {
			if e != nil && exprHasAwait(e) {
				return true
			}
		}
		return false
	case *ObjectLit:
		for _, p := range n.Properties {
			if p.Value != nil && exprHasAwait(p.Value) {
				return true
			}
		}
		return false
	case *TemplateLit:
		for _, e := range n.Expressions {
			if exprHasAwait(e) {
				return true
			}
		}
		return false
	case *TaggedTemplateExpr:
		if exprHasAwait(n.Tag) {
			return true
		}
		for _, e := range n.Template.Expressions {
			if exprHasAwait(e) {
				return true
			}
		}
		return false
	case *FunctionExpr, *ArrowFunc:
		return false // 函数表达式体内不算 TLA
	default:
		return false
	}
}
