package parser

import (
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
)

func (p *Parser) litToPattern(expr ast.Expression) ast.Node {
	switch n := expr.(type) {
	case *ast.ObjectLit:
		return exprToPattern(n)
	case *ast.ArrayLit:
		return exprToPattern(n)
	}
	return expr
}

// exprToPattern 递归把表达式转换为绑定模式（用于解构赋值的嵌套目标）。
// 嵌套对象/数组字面量继续转换；标识符与成员表达式保持原样；
// 赋值表达式（{a: b = 1} / [a = 1] 的解析产物）解包为目标 + 默认值。
// 非法目标（数字/字符串等字面量）返回 nil，后端会报错。
func exprToPattern(expr ast.Node) ast.Pattern {
	switch n := expr.(type) {
	case *ast.ObjectLit:
		pat := &ast.ObjectPattern{Loc: n.Loc}
		for _, prop := range n.Properties {
			if prop.Kind == ast.PropertySpread {
				pat.Properties = append(pat.Properties, ast.ObjectPatternProperty{
					Key:    prop.Key,
					Value:  exprToPattern(prop.Value),
					IsRest: true,
				})
				continue
			}
			var val ast.Node = prop.Value
			def := prop.Default
			if as, ok := prop.Value.(*ast.AssignExpr); ok && as.Op == "=" {
				// {a: b = 1} / {a: {b} = 1}：目标 + 默认值
				val = as.Left
				def = as.Right
			}
			pat.Properties = append(pat.Properties, ast.ObjectPatternProperty{
				Key:      prop.Key,
				Value:    exprToPattern(val),
				Default:  def,
				Computed: prop.Computed,
			})
		}
		return pat
	case *ast.ArrayLit:
		pat := &ast.ArrayPattern{Loc: n.Loc}
		for _, el := range n.Elements {
			if sp, ok := el.(*ast.SpreadElement); ok {
				pat.Elements = append(pat.Elements, ast.ArrayPatternElement{
					Target: exprToPattern(sp.Arg),
					IsRest: true,
				})
			} else if el != nil {
				var target ast.Node = el
				var def ast.Expression
				if as, ok := el.(*ast.AssignExpr); ok && as.Op == "=" {
					// [a = 1] / [[x] = 1]
					target = as.Left
					def = as.Right
				}
				pat.Elements = append(pat.Elements, ast.ArrayPatternElement{
					Target:  exprToPattern(target),
					Default: def,
				})
			} else {
				pat.Elements = append(pat.Elements, ast.ArrayPatternElement{})
			}
		}
		return pat
	case *ast.Identifier:
		return n
	case *ast.MemberExpr:
		return n
	case *ast.ObjectPattern:
		// 嵌套解构已被 parseAssignment 转换（{a: {b}} = v）
		return n
	case *ast.ArrayPattern:
		return n
	}
	return nil
}


func (p *Parser) parseArrayPattern() (*ast.ArrayPattern, error) {
	t := p.peek()
	p.next() // [
	pat := &ast.ArrayPattern{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "]") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated array pattern")
		}
		// hole (elision)
		if p.peek().Type == lexer.TokenPunct && p.peek().Value == "," {
			pat.Elements = append(pat.Elements, ast.ArrayPatternElement{})
			p.next()
			continue
		}
		var el ast.ArrayPatternElement
		// rest: ...rest
		if p.matchPunct("...") {
			target, err := p.parsePatternTarget()
			if err != nil {
				return nil, err
			}
			el.Target = target
			el.IsRest = true
			pat.Elements = append(pat.Elements, el)
			break // rest must be last
		}
		target, err := p.parsePatternTarget()
		if err != nil {
			return nil, err
		}
		el.Target = target
		if p.matchPunct("=") {
			def, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			el.Default = def
		}
		pat.Elements = append(pat.Elements, el)
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("]"); err != nil {
		return nil, err
	}
	return pat, nil
}

// parseObjectPattern parses `{a, b: c, ...rest}`.
func (p *Parser) parseObjectPattern() (*ast.ObjectPattern, error) {
	t := p.peek()
	p.next() // {
	pat := &ast.ObjectPattern{Loc: posOf(t)}
	for !(p.peek().Type == lexer.TokenPunct && p.peek().Value == "}") {
		if p.peek().Type == lexer.TokenEOF {
			return nil, p.errorf(p.peek(), "unterminated object pattern")
		}
		var prop ast.ObjectPatternProperty
		// rest: ...rest
		if p.matchPunct("...") {
			target, err := p.parsePatternTarget()
			if err != nil {
				return nil, err
			}
			prop.Value = target
			prop.IsRest = true
			pat.Properties = append(pat.Properties, prop)
			break // rest must be last
		}
		// key
		keyTok := p.peek()
		var key ast.Expression
		if p.matchPunct("[") {
			computedKey, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			key = computedKey
			prop.Computed = true
		} else {
			switch keyTok.Type {
			case lexer.TokenIdent, lexer.TokenKeyword:
				p.next()
				key = &ast.Identifier{Name: keyTok.Value, Loc: posOf(keyTok)}
			case lexer.TokenString:
				p.next()
				key = &ast.StringLit{Value: keyTok.Value, Loc: posOf(keyTok)}
			case lexer.TokenNumber:
				p.next()
				v, _ := parseNumberLiteral(keyTok.Value)
				key = &ast.NumberLit{Value: v, Raw: keyTok.Value, Loc: posOf(keyTok)}
			default:
				return nil, p.errorf(keyTok, "invalid property name in object pattern")
			}
		}
		prop.Key = key
		if p.matchPunct(":") {
			// renamed binding: key: target [= default]
			target, err := p.parsePatternTarget()
			if err != nil {
				return nil, err
			}
			prop.Value = target
			if p.matchPunct("=") {
				def, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				prop.Default = def
			}
		} else if prop.Computed {
			return nil, p.errorf(p.peek(), "computed property in object pattern requires a target")
		} else if p.matchPunct("=") {
			// shorthand with default: name = default
			prop.Value = &ast.Identifier{Name: keyTok.Value, Loc: posOf(keyTok)}
			def, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			prop.Default = def
		} else {
			// shorthand: { x } → { x: x }
			prop.Value = &ast.Identifier{Name: keyTok.Value, Loc: posOf(keyTok)}
		}
		pat.Properties = append(pat.Properties, prop)
		if !p.matchPunct(",") {
			break
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return pat, nil
}

// parsePatternTarget parses the target of a binding: identifier or nested pattern.
func (p *Parser) parsePatternTarget() (ast.Pattern, error) {
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "[" {
		return p.parseArrayPattern()
	}
	if p.peek().Type == lexer.TokenPunct && p.peek().Value == "{" {
		return p.parseObjectPattern()
	}
	nameTok, err := p.expect(lexer.TokenIdent, "")
	if err != nil {
		return nil, err
	}
	return &ast.Identifier{Name: nameTok.Value, Loc: posOf(nameTok)}, nil
}

// parseNumberLiteral 解析数字字面量字符串为 float64。
// 支持：十进制、十六进制(0x)、八进制(0o)、二进制(0b)、科学计数法、数字分隔符。
// bigIntLiteralToDecimal 将 BigInt 字面量（已去 n 后缀）转为十进制整数字符串。
// 支持 0x/0o/0b 前缀与下划线分隔符。例："0xFF" → "255"，"1_000" → "1000"。
