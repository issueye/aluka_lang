package ast

import "testing"

// TestDeepCopyStructuralIndependence：复制后修改 clone 不影响原 AST。
func TestDeepCopyStructuralIndependence(t *testing.T) {
	prog := &Program{
		Body: []Statement{
			&VarDecl{Kind: "let", Decls: []VarDeclarator{
				{Name: &Identifier{Name: "x"}, Init: &NumberLit{Value: 1, Raw: "1"}},
				{Name: &Identifier{Name: "y"}, Init: &StringLit{Value: "s"}},
			}},
			&FunctionDecl{Name: &Identifier{Name: "f"}, Params: []*Identifier{{Name: "a"}}},
			&ImportDecl{Source: "mod", Attributes: map[string]string{"type": "json"}},
		},
		SourceFile: "test.js",
	}
	cp := DeepCopy(prog)

	// 修改 clone 的每个层次。
	cp.Body[0].(*VarDecl).Decls[0].Name.Name = "X"
	cp.Body[0].(*VarDecl).Decls[0].Init.(*NumberLit).Value = 99
	cp.Body[1].(*FunctionDecl).Name.Name = "F"
	cp.Body[2].(*ImportDecl).Attributes["type"] = "zzz"
	cp.SourceFile = "other.js"

	// 原 AST 必须保持不变。
	if got := prog.Body[0].(*VarDecl).Decls[0].Name.Name; got != "x" {
		t.Errorf("original var name = %q, want x", got)
	}
	if got := prog.Body[0].(*VarDecl).Decls[0].Init.(*NumberLit).Value; got != 1 {
		t.Errorf("original init = %v, want 1", got)
	}
	if got := prog.Body[1].(*FunctionDecl).Name.Name; got != "f" {
		t.Errorf("original func name = %q, want f", got)
	}
	if got := prog.Body[2].(*ImportDecl).Attributes["type"]; got != "json" {
		t.Errorf("original import attribute = %q, want json", got)
	}
	if prog.SourceFile != "test.js" {
		t.Errorf("original sourcefile = %q, want test.js", prog.SourceFile)
	}
}

// TestDeepCopyInterfaceAndSliceElements：接口字段与切片元素都复制为独立实例。
func TestDeepCopyInterfaceAndSliceElements(t *testing.T) {
	prog := &Program{
		Body: []Statement{
			&ExprStmt{Expr: &BinaryExpr{Op: "+", Left: &Identifier{Name: "a"}, Right: &Identifier{Name: "b"}}},
		},
	}
	cp := DeepCopy(prog)
	stmt := cp.Body[0].(*ExprStmt)
	bin := stmt.Expr.(*BinaryExpr)
	bin.Left.(*Identifier).Name = "A"
	if got := prog.Body[0].(*ExprStmt).Expr.(*BinaryExpr).Left.(*Identifier).Name; got != "a" {
		t.Errorf("original left = %q, want a", got)
	}
	if bin == prog.Body[0].(*ExprStmt).Expr.(*BinaryExpr) {
		t.Error("binary expr pointer shared between original and copy")
	}
}

// TestDeepCopyNilSafe：nil 指针/切片/接口复制后仍为 nil。
func TestDeepCopyNilSafe(t *testing.T) {
	prog := &Program{Body: nil}
	cp := DeepCopy(prog)
	if cp.Body != nil {
		t.Error("copy of nil body is not nil")
	}
	vd := &VarDecl{Decls: nil}
	cvd := DeepCopy(vd)
	if cvd.Decls != nil {
		t.Error("copy of nil decls is not nil")
	}
}
