package ast

import (
	"strings"
	"unicode"
)

// LowerJSX 将 AST 中的所有 JSXElement 和 JSXFragment 递归转换为 React.createElement(...) 调用
func LowerJSX(node Node) Node {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *Program:
		for i, stmt := range n.Body {
			n.Body[i] = LowerJSX(stmt).(Statement)
		}
		return n

	case *VarDecl:
		for i := range n.Decls {
			if n.Decls[i].Init != nil {
				n.Decls[i].Init = LowerJSX(n.Decls[i].Init).(Expression)
			}
		}
		return n

	case *ExprStmt:
		n.Expr = LowerJSX(n.Expr).(Expression)
		return n

	case *ReturnStmt:
		if n.Arg != nil {
			n.Arg = LowerJSX(n.Arg).(Expression)
		}
		return n

	case *IfStmt:
		n.Test = LowerJSX(n.Test).(Expression)
		n.Consequent = LowerJSX(n.Consequent).(Statement)
		if n.Alternate != nil {
			n.Alternate = LowerJSX(n.Alternate).(Statement)
		}
		return n

	case *BlockStmt:
		for i, stmt := range n.Body {
			n.Body[i] = LowerJSX(stmt).(Statement)
		}
		return n

	case *FunctionDecl:
		if n.Body != nil {
			n.Body = LowerJSX(n.Body).(*BlockStmt)
		}
		return n

	case *FunctionExpr:
		if n.Body != nil {
			n.Body = LowerJSX(n.Body).(*BlockStmt)
		}
		return n

	case *ArrowFunc:
		if n.Body != nil {
			n.Body = LowerJSX(n.Body)
		}
		return n

	case *CallExpr:
		n.Callee = LowerJSX(n.Callee).(Expression)
		for i, arg := range n.Arguments {
			n.Arguments[i] = LowerJSX(arg).(Expression)
		}
		return n

	case *ArrayLit:
		for i, el := range n.Elements {
			if el != nil {
				n.Elements[i] = LowerJSX(el).(Expression)
			}
		}
		return n

	case *ObjectLit:
		for i := range n.Properties {
			if n.Properties[i].Value != nil {
				n.Properties[i].Value = LowerJSX(n.Properties[i].Value).(Expression)
			}
		}
		return n

	case *JSXElement:
		return lowerJSXElementNode(n)

	case *JSXFragment:
		return lowerJSXFragmentNode(n)

	case *ExportDefaultDecl:
		if n.Expression != nil {
			n.Expression = LowerJSX(n.Expression).(Expression)
		}
		return n

	case *ExportDecl:
		if n.Declaration != nil {
			n.Declaration = LowerJSX(n.Declaration).(Statement)
		}
		return n

	// —— 表达式递归：JSX 可出现在任意表达式位置（子元素容器、属性值、变量赋值等） ——

	case *ConditionalExpr: // 三元：{cond ? <a/> : <b/>}
		n.Test = LowerJSX(n.Test).(Expression)
		n.Consequent = LowerJSX(n.Consequent).(Expression)
		n.Alternate = LowerJSX(n.Alternate).(Expression)
		return n

	case *LogicalExpr: // 逻辑与/或：{cond && <a/>} / {a || <b/>}
		n.Left = LowerJSX(n.Left).(Expression)
		n.Right = LowerJSX(n.Right).(Expression)
		return n

	case *BinaryExpr: // 二元：字符串拼接 {`text` + <a/>}
		n.Left = LowerJSX(n.Left).(Expression)
		n.Right = LowerJSX(n.Right).(Expression)
		return n

	case *AssignExpr: // 赋值：x = <a/>
		n.Right = LowerJSX(n.Right).(Expression)
		return n

	case *UnaryExpr: // 一元：{!cond} / {-count}
		n.Arg = LowerJSX(n.Arg).(Expression)
		return n

	case *UpdateExpr: // 更新：{x++} / {--x}
		return n

	case *TemplateLit: // 模板字面量：{`text ${var}`}
		for i, expr := range n.Expressions {
			n.Expressions[i] = LowerJSX(expr).(Expression)
		}
		return n

	case *MemberExpr: // 成员访问：obj.method() 的 callee
		n.Object = LowerJSX(n.Object).(Expression)
		return n

	case *NewExpr: // new 表达式
		n.Callee = LowerJSX(n.Callee).(Expression)
		for i, arg := range n.Arguments {
			n.Arguments[i] = LowerJSX(arg).(Expression)
		}
		return n

	case *AwaitExpr: // await 表达式
		n.Argument = LowerJSX(n.Argument).(Expression)
		return n

	case *YieldExpr: // yield 表达式
		if n.Argument != nil {
			n.Argument = LowerJSX(n.Argument).(Expression)
		}
		return n

	case *TaggedTemplateExpr: // 标记模板：tag`a${x}b`
		n.Tag = LowerJSX(n.Tag).(Expression)
		if n.Template != nil {
			for i, expr := range n.Template.Expressions {
				n.Template.Expressions[i] = LowerJSX(expr).(Expression)
			}
		}
		return n
	}

	return node
}

func lowerJSXElementNode(el *JSXElement) Expression {
	loc := el.Loc

	callee := &MemberExpr{
		Object:   &Identifier{Name: "React", Loc: loc},
		Property: &Identifier{Name: "createElement", Loc: loc},
		Loc:      loc,
	}

	var tagArg Expression
	switch name := el.OpeningElement.Name.(type) {
	case *Identifier:
		if isComponentIdentName(name.Name) {
			tagArg = &Identifier{Name: name.Name, Loc: name.Loc}
		} else {
			tagArg = &StringLit{Value: name.Name, Loc: name.Loc}
		}
	case *JSXMemberExpr:
		tagArg = lowerJSXMemberExprNode(name)
	default:
		tagArg = &StringLit{Value: "div", Loc: loc}
	}

	propsArg := lowerJSXAttributesNode(el.OpeningElement.Attributes, loc)

	var childArgs []Expression
	for _, child := range el.Children {
		lowered := lowerJSXChildNode(child)
		if lowered != nil {
			childArgs = append(childArgs, lowered)
		}
	}

	args := []Expression{tagArg, propsArg}
	args = append(args, childArgs...)

	return &CallExpr{
		Callee:    callee,
		Arguments: args,
		Loc:       loc,
	}
}

func lowerJSXFragmentNode(frag *JSXFragment) Expression {
	loc := frag.Loc

	callee := &MemberExpr{
		Object:   &Identifier{Name: "React", Loc: loc},
		Property: &Identifier{Name: "createElement", Loc: loc},
		Loc:      loc,
	}

	tagArg := &MemberExpr{
		Object:   &Identifier{Name: "React", Loc: loc},
		Property: &Identifier{Name: "Fragment", Loc: loc},
		Loc:      loc,
	}

	propsArg := &NullLit{Loc: loc}

	var childArgs []Expression
	for _, child := range frag.Children {
		lowered := lowerJSXChildNode(child)
		if lowered != nil {
			childArgs = append(childArgs, lowered)
		}
	}

	args := []Expression{tagArg, propsArg}
	args = append(args, childArgs...)

	return &CallExpr{
		Callee:    callee,
		Arguments: args,
		Loc:       loc,
	}
}

func lowerJSXMemberExprNode(m *JSXMemberExpr) Expression {
	var obj Expression
	switch o := m.Object.(type) {
	case *Identifier:
		obj = &Identifier{Name: o.Name, Loc: o.Loc}
	case *JSXMemberExpr:
		obj = lowerJSXMemberExprNode(o)
	default:
		obj = &Identifier{Name: "unknown", Loc: m.Loc}
	}

	return &MemberExpr{
		Object:   obj,
		Property: &Identifier{Name: m.Property, Loc: m.Loc},
		Loc:      m.Loc,
	}
}

func lowerJSXAttributesNode(attrs []Node, loc Pos) Expression {
	if len(attrs) == 0 {
		return &NullLit{Loc: loc}
	}

	var props []Property

	for _, a := range attrs {
		switch attr := a.(type) {
		case *JSXAttribute:
			var val Expression
			if attr.Value == nil {
				val = &BoolLit{Value: true, Loc: attr.Loc}
			} else {
				switch v := attr.Value.(type) {
				case *StringLit:
					val = v
				case *JSXExpressionContainer:
					if v.Expression != nil {
						val = LowerJSX(v.Expression).(Expression)
					} else {
						val = &NullLit{Loc: attr.Loc}
					}
				default:
					val = &NullLit{Loc: attr.Loc}
				}
			}
			props = append(props, Property{
				Key:   &StringLit{Value: attr.Name, Loc: attr.Loc},
				Value: val,
				Kind:  PropertyInit,
				Loc:   attr.Loc,
			})
		case *JSXSpreadAttribute:
			props = append(props, Property{
				Value: LowerJSX(attr.Argument).(Expression),
				Kind:  PropertySpread,
				Loc:   attr.Loc,
			})
		}
	}

	return &ObjectLit{
		Properties: props,
		Loc:        loc,
	}
}

func lowerJSXChildNode(child Node) Expression {
	switch c := child.(type) {
	case *JSXElement:
		return lowerJSXElementNode(c)
	case *JSXFragment:
		return lowerJSXFragmentNode(c)
	case *JSXExpressionContainer:
		if c.Expression != nil {
			return LowerJSX(c.Expression).(Expression)
		}
		return nil
	case *JSXText:
		text := cleanJSXTextStr(c.Value)
		if text == "" {
			return nil
		}
		return &StringLit{Value: text, Loc: c.Loc}
	default:
		return nil
	}
}

func cleanJSXTextStr(raw string) string {
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" || (i > 0 && i < len(lines)-1) {
			cleaned = append(cleaned, trimmed)
		}
	}
	res := strings.Join(cleaned, " ")
	return strings.TrimSpace(res)
}

func isComponentIdentName(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := rune(name[0])
	return unicode.IsUpper(r) || r == '_' || r == '$'
}
