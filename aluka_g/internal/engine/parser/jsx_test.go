package parser_test

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

func TestParseJSX(t *testing.T) {
	tests := []struct {
		name string
		src  string
		eval func(t *testing.T, prog *ast.Program)
	}{
		{
			name: "simple self closing tag",
			src:  `const el = <div className="test" id="main" />;`,
			eval: func(t *testing.T, prog *ast.Program) {
				vd := prog.Body[0].(*ast.VarDecl)
				jsx := vd.Decls[0].Init.(*ast.JSXElement)
				if !jsx.OpeningElement.SelfClosing {
					t.Fatalf("expected self-closing element")
				}
				id := jsx.OpeningElement.Name.(*ast.Identifier)
				if id.Name != "div" {
					t.Fatalf("expected tag name div, got %s", id.Name)
				}
				if len(jsx.OpeningElement.Attributes) != 2 {
					t.Fatalf("expected 2 attributes, got %d", len(jsx.OpeningElement.Attributes))
				}
			},
		},
		{
			name: "nested component with children and expressions",
			src: `
				const app = (
					<UI.Container title="App" {...props}>
						<h1>Hello JSX</h1>
						<p>{user.name + '!'}</p>
					</UI.Container>
				);
			`,
			eval: func(t *testing.T, prog *ast.Program) {
				vd := prog.Body[0].(*ast.VarDecl)
				jsx := vd.Decls[0].Init.(*ast.JSXElement)
				mem := jsx.OpeningElement.Name.(*ast.JSXMemberExpr)
				if mem.Property != "Container" {
					t.Fatalf("expected property Container, got %s", mem.Property)
				}
				if len(jsx.Children) != 2 {
					t.Fatalf("expected 2 children, got %d", len(jsx.Children))
				}
			},
		},
		{
			name: "jsx fragment",
			src:  `const frag = <><span>1</span><span>2</span></>;`,
			eval: func(t *testing.T, prog *ast.Program) {
				vd := prog.Body[0].(*ast.VarDecl)
				frag := vd.Decls[0].Init.(*ast.JSXFragment)
				if len(frag.Children) != 2 {
					t.Fatalf("expected 2 children in fragment, got %d", len(frag.Children))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := parser.Parse(tt.src)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			tt.eval(t, prog)
		})
	}
}
