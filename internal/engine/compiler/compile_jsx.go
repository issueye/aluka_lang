package compiler

import (
	"strings"
	"unicode"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// JSX Lowering 转换器：将 JSXElement / JSXFragment 降低为 React.createElement(...) 调用表达式

// lowerJSXElement 将 JSXElement 转换为 React.createElement(tag, props, ...children)
func lowerJSXElement(el *ast.JSXElement) ast.Expression {
	loc := el.Loc

	// 1. Callee: React.createElement
	callee := &ast.MemberExpr{
		Object:   &ast.Identifier{Name: "React", Loc: loc},
		Property: &ast.Identifier{Name: "createElement", Loc: loc},
		Loc:      loc,
	}

	// 2. Tag 参数
	var tagArg ast.Expression
	switch name := el.OpeningElement.Name.(type) {
	case *ast.Identifier:
		// 如果首字母大写，则是自定义组件标识符；如果是小写，则是原生 HTML 字符串
		if isComponentIdent(name.Name) {
			tagArg = &ast.Identifier{Name: name.Name, Loc: name.Loc}
		} else {
			tagArg = &ast.StringLit{Value: name.Name, Loc: name.Loc}
		}
	case *ast.JSXMemberExpr:
		tagArg = lowerJSXMemberExpr(name)
	default:
		tagArg = &ast.StringLit{Value: "div", Loc: loc}
	}

	// 3. Props 参数
	propsArg := lowerJSXAttributes(el.OpeningElement.Attributes, loc)

	// 4. Children 参数列表
	var childArgs []ast.Expression
	for _, child := range el.Children {
		lowered := lowerJSXChild(child)
		if lowered != nil {
			childArgs = append(childArgs, lowered)
		}
	}

	args := []ast.Expression{tagArg, propsArg}
	args = append(args, childArgs...)

	return &ast.CallExpr{
		Callee:    callee,
		Arguments: args,
		Loc:       loc,
	}
}

// lowerJSXFragment 将 <>children</> 转换为 React.createElement(React.Fragment, null, ...children)
func lowerJSXFragment(frag *ast.JSXFragment) ast.Expression {
	loc := frag.Loc

	callee := &ast.MemberExpr{
		Object:   &ast.Identifier{Name: "React", Loc: loc},
		Property: &ast.Identifier{Name: "createElement", Loc: loc},
		Loc:      loc,
	}

	tagArg := &ast.MemberExpr{
		Object:   &ast.Identifier{Name: "React", Loc: loc},
		Property: &ast.Identifier{Name: "Fragment", Loc: loc},
		Loc:      loc,
	}

	propsArg := &ast.NullLit{Loc: loc}

	var childArgs []ast.Expression
	for _, child := range frag.Children {
		lowered := lowerJSXChild(child)
		if lowered != nil {
			childArgs = append(childArgs, lowered)
		}
	}

	args := []ast.Expression{tagArg, propsArg}
	args = append(args, childArgs...)

	return &ast.CallExpr{
		Callee:    callee,
		Arguments: args,
		Loc:       loc,
	}
}

// lowerJSXMemberExpr 将 UI.Button 转换为 MemberExpr
func lowerJSXMemberExpr(m *ast.JSXMemberExpr) ast.Expression {
	var obj ast.Expression
	switch o := m.Object.(type) {
	case *ast.Identifier:
		obj = &ast.Identifier{Name: o.Name, Loc: o.Loc}
	case *ast.JSXMemberExpr:
		obj = lowerJSXMemberExpr(o)
	default:
		obj = &ast.Identifier{Name: "unknown", Loc: m.Loc}
	}

	return &ast.MemberExpr{
		Object:   obj,
		Property: &ast.Identifier{Name: m.Property, Loc: m.Loc},
		Loc:      m.Loc,
	}
}

// lowerJSXAttributes 将属性列表转换为 ObjectLit 或 NullLit
func lowerJSXAttributes(attrs []ast.Node, loc ast.Pos) ast.Expression {
	if len(attrs) == 0 {
		return &ast.NullLit{Loc: loc}
	}

	var props []ast.Property
	hasSpread := false

	for _, a := range attrs {
		switch attr := a.(type) {
		case *ast.JSXAttribute:
			var val ast.Expression
			if attr.Value == nil {
				// <Tag disabled /> -> { disabled: true }
				val = &ast.BoolLit{Value: true, Loc: attr.Loc}
			} else {
				switch v := attr.Value.(type) {
				case *ast.StringLit:
					val = v
				case *ast.JSXExpressionContainer:
					val = v.Expression
				default:
					val = &ast.NullLit{Loc: attr.Loc}
				}
			}
			props = append(props, ast.Property{
				Key:   &ast.StringLit{Value: attr.Name, Loc: attr.Loc},
				Value: val,
				Kind:  ast.PropertyInit,
				Loc:   attr.Loc,
			})
		case *ast.JSXSpreadAttribute:
			hasSpread = true
			props = append(props, ast.Property{
				Value: attr.Argument,
				Kind:  ast.PropertySpread,
				Loc:   attr.Loc,
			})
		}
	}

	_ = hasSpread
	return &ast.ObjectLit{
		Properties: props,
		Loc:        loc,
	}
}

// lowerJSXChild 处理单个 JSX 子节点
func lowerJSXChild(child ast.Node) ast.Expression {
	switch c := child.(type) {
	case *ast.JSXElement:
		return lowerJSXElement(c)
	case *ast.JSXFragment:
		return lowerJSXFragment(c)
	case *ast.JSXExpressionContainer:
		return c.Expression
	case *ast.JSXText:
		text := cleanJSXText(c.Value)
		if text == "" {
			return nil
		}
		return &ast.StringLit{Value: text, Loc: c.Loc}
	default:
		return nil
	}
}

// cleanJSXText 清理 JSX 多行文本中的缩进与多余空格
func cleanJSXText(raw string) string {
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

// isComponentIdent 判断标识符是否为组件名（首字母大写）
func isComponentIdent(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := rune(name[0])
	return unicode.IsUpper(r) || r == '_' || r == '$'
}
