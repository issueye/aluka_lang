package parser

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

func TestClassPrivateFieldBinaryInitializer(t *testing.T) {
	program, err := Parse(`class Probe {
		#maxSize = 1024 * 1024
		#size = 0
	}`)
	if err != nil {
		t.Fatal(err)
	}
	decl, ok := program.Body[0].(*ast.ClassDecl)
	if !ok {
		t.Fatalf("statement = %T, want *ast.ClassDecl", program.Body[0])
	}
	field := decl.Body.Methods[0]
	if _, ok := field.Key.(*ast.Identifier); !ok {
		t.Fatalf("field key = %T, want *ast.Identifier", field.Key)
	}
	if _, ok := field.Init.(*ast.BinaryExpr); !ok {
		t.Fatalf("field initializer = %T, want *ast.BinaryExpr", field.Init)
	}
	if got := len(decl.Body.Methods); got != 2 {
		t.Fatalf("class fields = %d, want 2", got)
	}
	if key, ok := decl.Body.Methods[1].Key.(*ast.Identifier); !ok || key.Name != "#size" {
		t.Fatalf("second field key = %#v, want #size", decl.Body.Methods[1].Key)
	}
}
