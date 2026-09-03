package parser

import (
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

// JSX 语法解析插件 (JSXElement / JSXFragment / JSXAttributes / JSXChildren)

// isJSXStart 探测当前位置是否为 JSX 元素或 Fragment 开始 (<tag / <> / <App)
func (p *Parser) isJSXStart() bool {
	if p.peek().Type != lexer.TokenPunct || p.peek().Value != "<" {
		return false
	}
	// <> Fragment 开始
	nx := p.peekAt(1)
	if nx.Type == lexer.TokenPunct && nx.Value == ">" {
		return true
	}
	// 紧跟标识符 (如 <div, <App, <UI.Button)
	if nx.Type == lexer.TokenIdent || nx.Type == lexer.TokenKeyword {
		// 检查第三个 token：如果是 '>'、'/'、'='、标识符、'{'，则极大概率为 JSX 标签
		nx2 := p.peekAt(2)
		if nx2.Type == lexer.TokenPunct {
			if nx2.Value == ">" || nx2.Value == "/" || nx2.Value == "." {
				return true
			}
		}
		if nx2.Type == lexer.TokenIdent || nx2.Type == lexer.TokenKeyword {
			return true
		}
		if nx2.Type == lexer.TokenPunct && nx2.Value == "{" {
			return true // <Tag {...props}>
		}
	}
	return false
}

// parseJSXPrimary 解析 JSX 元素或 Fragment（作为 Primary 表达式入口）
func (p *Parser) parseJSXPrimary() (ast.Expression, error) {
	t := p.peek()
	if t.Type != lexer.TokenPunct || t.Value != "<" {
		return nil, p.errorf(t, "expected '<' at start of JSX")
	}

	// <> Fragment
	if p.peekAt(1).Type == lexer.TokenPunct && p.peekAt(1).Value == ">" {
		return p.parseJSXFragment()
	}

	return p.parseJSXElement()
}

// parseJSXElement 解析完整的 JSX 元素
func (p *Parser) parseJSXElement() (*ast.JSXElement, error) {
	loc := posOf(p.peek())
	opening, err := p.parseJSXOpeningElement()
	if err != nil {
		return nil, err
	}

	el := &ast.JSXElement{
		OpeningElement: opening,
		Loc:            loc,
	}

	// 自闭合标签：<Tag />
	if opening.SelfClosing {
		return el, nil
	}

	// 解析子节点
	children, closing, err := p.parseJSXChildrenAndClosing(opening.Name)
	if err != nil {
		return nil, err
	}
	el.Children = children
	el.ClosingElement = closing
	return el, nil
}

// parseJSXFragment 解析 <>children</>
func (p *Parser) parseJSXFragment() (*ast.JSXFragment, error) {
	loc := posOf(p.peek())
	p.next() // <
	p.next() // >

	frag := &ast.JSXFragment{Loc: loc}
	children, _, err := p.parseJSXChildrenAndClosing(nil)
	if err != nil {
		return nil, err
	}
	frag.Children = children
	return frag, nil
}

// parseJSXOpeningElement 解析开标签 <Tag attr="val">
func (p *Parser) parseJSXOpeningElement() (*ast.JSXOpeningElement, error) {
	loc := posOf(p.peek())
	p.next() // consume '<'

	// 标签名 (Identifier 或 JSXMemberExpr 如 UI.Button)
	tagTok, err := p.expectName()
	if err != nil {
		return nil, err
	}
	var tagName ast.Node = &ast.Identifier{Name: tagTok.Value, Loc: posOf(tagTok)}

	// 复合名称：<A.B.C>
	for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
		p.next() // .
		subTok, err := p.expectName()
		if err != nil {
			return nil, err
		}
		tagName = &ast.JSXMemberExpr{
			Object:   tagName,
			Property: subTok.Value,
			Loc:      posOf(subTok),
		}
	}

	var attrs []ast.Node
	selfClosing := false

	// 解析属性列表
	for {
		t := p.peek()
		if t.Type == lexer.TokenEOF {
			return nil, p.errorf(t, "unterminated JSX opening tag")
		}

		// 自闭合 />
		if t.Type == lexer.TokenPunct && t.Value == "/" {
			p.next()
			if err := p.expectPunct(">"); err != nil {
				return nil, err
			}
			selfClosing = true
			break
		}

		// 普通闭合 >
		if t.Type == lexer.TokenPunct && t.Value == ">" {
			p.next()
			break
		}

		// 属性展开：{...props}
		if t.Type == lexer.TokenPunct && t.Value == "{" {
			p.next() // {
			if !p.matchPunct("...") {
				return nil, p.errorf(p.peek(), "expected '...' in JSX spread attribute")
			}
			arg, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("}"); err != nil {
				return nil, err
			}
			attrs = append(attrs, &ast.JSXSpreadAttribute{
				Argument: arg,
				Loc:      posOf(t),
			})
			continue
		}

		// 命名属性：key="val" / key={val} / key
		attrLoc := posOf(t)
		attrNameTok := p.next()
		attrName := attrNameTok.Value

		// 支持连字符属性名 (如 aria-label, data-testid)
		for p.peek().Type == lexer.TokenPunct && p.peek().Value == "-" {
			p.next() // -
			nextPart := p.next()
			attrName += "-" + nextPart.Value
		}

		var attrValue ast.Node
		if p.matchPunct("=") {
			vTok := p.peek()
			if vTok.Type == lexer.TokenString {
				p.next()
				attrValue = &ast.StringLit{Value: vTok.Value, Loc: posOf(vTok)}
			} else if vTok.Type == lexer.TokenPunct && vTok.Value == "{" {
				p.next() // {
				expr, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				if err := p.expectPunct("}"); err != nil {
					return nil, err
				}
				attrValue = &ast.JSXExpressionContainer{
					Expression: expr,
					Loc:        posOf(vTok),
				}
			} else {
				return nil, p.errorf(vTok, "expected string or expression for JSX attribute value")
			}
		}

		attrs = append(attrs, &ast.JSXAttribute{
			Name:  attrName,
			Value: attrValue,
			Loc:   attrLoc,
		})
	}

	return &ast.JSXOpeningElement{
		Name:        tagName,
		Attributes:  attrs,
		SelfClosing: selfClosing,
		Loc:         loc,
	}, nil
}

// parseJSXChildrenAndClosing 解析标签内的子节点与结束标签
func (p *Parser) parseJSXChildrenAndClosing(openTag ast.Node) ([]ast.Node, *ast.JSXClosingElement, error) {
	var children []ast.Node

	for {
		t := p.peek()
		if t.Type == lexer.TokenEOF {
			return nil, nil, p.errorf(t, "unterminated JSX element")
		}

		// 检查是否遇到结束标签 </Tag> 或 </>
		if t.Type == lexer.TokenPunct && t.Value == "<" {
			if p.peekAt(1).Type == lexer.TokenPunct && p.peekAt(1).Value == "/" {
				// 结束标签
				p.next() // <
				p.next() // /
				closeLoc := posOf(t)

				// </> Fragment 闭合
				if p.matchPunct(">") {
					return children, &ast.JSXClosingElement{Loc: closeLoc}, nil
				}

				closeNameTok, err := p.expectName()
				if err != nil {
					return nil, nil, err
				}
				var closeName ast.Node = &ast.Identifier{Name: closeNameTok.Value, Loc: posOf(closeNameTok)}
				for p.peek().Type == lexer.TokenPunct && p.peek().Value == "." {
					p.next()
					subTok, err := p.expectName()
					if err != nil {
						return nil, nil, err
					}
					closeName = &ast.JSXMemberExpr{
						Object:   closeName,
						Property: subTok.Value,
						Loc:      posOf(subTok),
					}
				}

				if err := p.expectPunct(">"); err != nil {
					return nil, nil, err
				}

				return children, &ast.JSXClosingElement{
					Name: closeName,
					Loc:  closeLoc,
				}, nil
			}

			// 嵌套子元素 <Child ...>
			childEl, err := p.parseJSXPrimary()
			if err != nil {
				return nil, nil, err
			}
			children = append(children, childEl)
			continue
		}

		// 表达式容器 {expr}
		if t.Type == lexer.TokenPunct && t.Value == "{" {
			p.next() // {
			if p.matchPunct("}") {
				children = append(children, &ast.JSXExpressionContainer{Loc: posOf(t)})
				continue
			}
			expr, err := p.parseAssignment()
			if err != nil {
				return nil, nil, err
			}
			if err := p.expectPunct("}"); err != nil {
				return nil, nil, err
			}
			children = append(children, &ast.JSXExpressionContainer{
				Expression: expr,
				Loc:        posOf(t),
			})
			continue
		}

		// 纯文本子节点：消费所有非 '<' 和 '{' 的 token
		textLoc := posOf(t)
		var textParts []string
		for {
			cur := p.peek()
			if cur.Type == lexer.TokenEOF || (cur.Type == lexer.TokenPunct && (cur.Value == "<" || cur.Value == "{")) {
				break
			}
			p.next()
			textParts = append(textParts, cur.Value)
		}

		rawText := strings.Join(textParts, " ")
		trimmed := strings.TrimSpace(rawText)
		if trimmed != "" {
			children = append(children, &ast.JSXText{
				Value: rawText,
				Raw:   rawText,
				Loc:   textLoc,
			})
		}
	}
}
