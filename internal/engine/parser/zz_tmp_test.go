package parser

import "testing"

func TestZZTmpTemplate(t *testing.T) {
	bs := "\\"
	tk := "`"
	src := "const x = " + tk + bs + bs + "u${1}" + tk + ";"
	t.Logf("src=%q", src)
	p, err := NewFromString(src); if err != nil { t.Fatalf("new: %v", err) }; ast, err := p.parseProgram()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Logf("parsed %d stmts", len(ast.Body))
}
