package ast

// JSX AST 节点定义 (JSXElement / JSXFragment / JSXAttribute / JSXExpressionContainer 等)

// JSXElement 表示完整的 JSX 元素：<Tag attr="val">children</Tag> 或 <Tag />
type JSXElement struct {
	OpeningElement *JSXOpeningElement
	Children       []Node // JSXText, JSXElement, JSXFragment, JSXExpressionContainer
	ClosingElement *JSXClosingElement
	Loc            Pos
}

func (e *JSXElement) Pos() Pos   { return e.Loc }
func (e *JSXElement) exprNode()  {}
func (e *JSXElement) node()      {}

// JSXOpeningElement 开始标签
type JSXOpeningElement struct {
	Name        Node // *Identifier 或 *JSXMemberExpr
	Attributes  []Node // *JSXAttribute 或 *JSXSpreadAttribute
	SelfClosing bool
	Loc         Pos
}

func (e *JSXOpeningElement) Pos() Pos   { return e.Loc }
func (e *JSXOpeningElement) exprNode()  {}
func (e *JSXOpeningElement) node()      {}

// JSXClosingElement 闭合标签
type JSXClosingElement struct {
	Name Node // *Identifier 或 *JSXMemberExpr
	Loc  Pos
}

func (e *JSXClosingElement) Pos() Pos   { return e.Loc }
func (e *JSXClosingElement) exprNode()  {}
func (e *JSXClosingElement) node()      {}

// JSXFragment 表示 <>children</> 片段
type JSXFragment struct {
	Children []Node
	Loc      Pos
}

func (f *JSXFragment) Pos() Pos   { return f.Loc }
func (f *JSXFragment) exprNode()  {}
func (f *JSXFragment) node()      {}

// JSXAttribute 属性键值对：attr="val" 或 attr={val} 或 attr (布尔属性)
type JSXAttribute struct {
	Name  string // 属性名（如 id, className, aria-label）
	Value Node   // *StringLit, *JSXExpressionContainer, nil（表示 true）
	Loc   Pos
}

func (a *JSXAttribute) Pos() Pos   { return a.Loc }
func (a *JSXAttribute) exprNode()  {}
func (a *JSXAttribute) node()      {}

// JSXSpreadAttribute 展开属性：{...props}
type JSXSpreadAttribute struct {
	Argument Expression
	Loc      Pos
}

func (s *JSXSpreadAttribute) Pos() Pos   { return s.Loc }
func (s *JSXSpreadAttribute) exprNode()  {}
func (s *JSXSpreadAttribute) node()      {}

// JSXExpressionContainer 大括号表达式容器：{expr}
type JSXExpressionContainer struct {
	Expression Expression // nil 表示空容器 {}
	Loc        Pos
}

func (c *JSXExpressionContainer) Pos() Pos   { return c.Loc }
func (c *JSXExpressionContainer) exprNode()  {}
func (c *JSXExpressionContainer) node()      {}

// JSXText JSX 文本子节点
type JSXText struct {
	Value string // 处理后的文本
	Raw   string // 原始文本
	Loc   Pos
}

func (t *JSXText) Pos() Pos   { return t.Loc }
func (t *JSXText) exprNode()  {}
func (t *JSXText) node()      {}

// JSXMemberExpr 复合组件名：<UI.Button.Primary />
type JSXMemberExpr struct {
	Object   Node   // *Identifier 或 *JSXMemberExpr
	Property string // 属性名
	Loc      Pos
}

func (m *JSXMemberExpr) Pos() Pos   { return m.Loc }
func (m *JSXMemberExpr) exprNode()  {}
func (m *JSXMemberExpr) node()      {}
