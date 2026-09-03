package ast

import (
	"reflect"
	"testing"
)

// collectChildTypes 用 ForEachChild 收集子节点类型名序列。
func collectChildTypes(n Node) []string {
	var out []string
	ForEachChild(n, func(c Node) bool {
		out = append(out, typeName(c))
		return false
	})
	return out
}

func typeName(n Node) string { return reflect.TypeOf(n).Elem().Name() }

// TestForEachChildCounts 对每种容器节点类型验证子节点枚举的精确数量与类型，
// 防止 ForEachChild 漏枚举字段（单一事实来源契约）。
func TestForEachChildCounts(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want []string
	}{
		{"Program", &Program{Body: []Statement{&EmptyStmt{}, &EmptyStmt{}, &EmptyStmt{}}},
			[]string{"EmptyStmt", "EmptyStmt", "EmptyStmt"}},
		{"VarDecl", &VarDecl{Decls: []VarDeclarator{
			{Name: &Identifier{Name: "a"}, Init: &NumberLit{}},
			{Pattern: &ArrayPattern{}}, // 模式 + 无 Init
		}}, []string{"Identifier", "NumberLit", "ArrayPattern"}},
		{"FunctionDecl", &FunctionDecl{
			Name: &Identifier{Name: "f"}, Params: []*Identifier{{Name: "a"}, {Name: "b"}},
			ParamPatterns: []Pattern{&Identifier{Name: "p"}}, Defaults: []Expression{&Identifier{Name: "d"}},
			RestParam: &Identifier{Name: "r"}, Body: &BlockStmt{},
		}, []string{"Identifier", "Identifier", "Identifier", "Identifier", "Identifier", "Identifier", "BlockStmt"}},
		{"FunctionExpr", &FunctionExpr{
			Name: &Identifier{Name: "f"}, Params: []*Identifier{{Name: "a"}}, Body: &BlockStmt{},
		}, []string{"Identifier", "Identifier", "BlockStmt"}},
		{"ArrowFunc", &ArrowFunc{
			Params: []*Identifier{{Name: "a"}}, Body: &Identifier{Name: "x"},
		}, []string{"Identifier", "Identifier"}},
		{"BlockStmt", &BlockStmt{Body: []Statement{&EmptyStmt{}, &EmptyStmt{}}},
			[]string{"EmptyStmt", "EmptyStmt"}},
		{"ExprStmt", &ExprStmt{Expr: &Identifier{Name: "x"}}, []string{"Identifier"}},
		{"EmptyStmt", &EmptyStmt{}, nil},
		{"IfStmt", &IfStmt{Test: &Identifier{}, Consequent: &EmptyStmt{}, Alternate: &EmptyStmt{}},
			[]string{"Identifier", "EmptyStmt", "EmptyStmt"}},
		{"WhileStmt", &WhileStmt{Test: &Identifier{}, Body: &EmptyStmt{}},
			[]string{"Identifier", "EmptyStmt"}},
		{"DoWhileStmt", &DoWhileStmt{Body: &EmptyStmt{}, Test: &Identifier{}},
			[]string{"EmptyStmt", "Identifier"}},
		{"ForStmt", &ForStmt{Init: &EmptyStmt{}, Test: &Identifier{}, Update: &Identifier{}, Body: &EmptyStmt{}},
			[]string{"EmptyStmt", "Identifier", "Identifier", "EmptyStmt"}},
		{"ForInStmt", &ForInStmt{Left: &Identifier{}, Right: &Identifier{}, Body: &EmptyStmt{}},
			[]string{"Identifier", "Identifier", "EmptyStmt"}},
		{"ForOfStmt", &ForOfStmt{Left: &Identifier{}, Right: &Identifier{}, Body: &EmptyStmt{}},
			[]string{"Identifier", "Identifier", "EmptyStmt"}},
		{"ReturnStmt", &ReturnStmt{Arg: &Identifier{}}, []string{"Identifier"}},
		{"BreakStmt", &BreakStmt{Label: "l"}, nil},
		{"ContinueStmt", &ContinueStmt{Label: "l"}, nil},
		{"ThrowStmt", &ThrowStmt{Arg: &Identifier{}}, []string{"Identifier"}},
		{"TryStmt", &TryStmt{
			Block:   &BlockStmt{},
			Handler: &CatchHandler{Param: &Identifier{}, Body: &BlockStmt{}},
			Finally: &BlockStmt{},
		}, []string{"BlockStmt", "Identifier", "BlockStmt", "BlockStmt"}},
		{"SwitchStmt", &SwitchStmt{Disc: &Identifier{}, Cases: []SwitchCase{
			{Test: &Identifier{}, Consequent: []Statement{&EmptyStmt{}}},
			{Test: &Identifier{}, Consequent: []Statement{&EmptyStmt{}}},
		}}, []string{"Identifier", "Identifier", "EmptyStmt", "Identifier", "EmptyStmt"}},
		{"LabeledStmt", &LabeledStmt{Label: "l", Body: &EmptyStmt{}}, []string{"EmptyStmt"}},
		{"ClassDecl", &ClassDecl{
			Name: &Identifier{}, SuperClass: &Identifier{},
			Body: &ClassBody{Methods: []MethodDefinition{
				{Key: &Identifier{}, Value: &FunctionExpr{}},
				{Key: &Identifier{}, Value: &FunctionExpr{}, Init: &Identifier{}}, // 字段带初始化
			}},
		}, []string{"Identifier", "Identifier", "Identifier", "FunctionExpr", "Identifier", "FunctionExpr", "Identifier"}},
		{"ClassExpr", &ClassExpr{
			Name: &Identifier{}, SuperClass: &Identifier{},
			Body: &ClassBody{Methods: []MethodDefinition{{Key: &Identifier{}, Value: &FunctionExpr{}}}},
		}, []string{"Identifier", "Identifier", "Identifier", "FunctionExpr"}},
		{"ImportDecl", &ImportDecl{Source: "m", Specifiers: []ImportSpecifier{{Imported: "a", Local: "b"}}}, nil},
		{"ExportDecl", &ExportDecl{Declaration: &VarDecl{}}, []string{"VarDecl"}},
		{"ExportDefaultDecl", &ExportDefaultDecl{Expression: &Identifier{}}, []string{"Identifier"}},

		{"TemplateLit", &TemplateLit{Expressions: []Expression{&Identifier{}, &Identifier{}}},
			[]string{"Identifier", "Identifier"}},
		{"TaggedTemplateExpr", &TaggedTemplateExpr{Tag: &Identifier{}, Template: &TemplateLit{Expressions: []Expression{&Identifier{}}}},
			[]string{"Identifier", "TemplateLit"}},
		{"ArrayLit", &ArrayLit{Elements: []Expression{&Identifier{}, &Identifier{}, &Identifier{}}},
			[]string{"Identifier", "Identifier", "Identifier"}},
		{"ObjectLit", &ObjectLit{Properties: []Property{
			{Key: &Identifier{}, Value: &Identifier{}},
			{Key: &Identifier{}, Value: &Identifier{}, Default: &Identifier{}},
		}}, []string{"Identifier", "Identifier", "Identifier", "Identifier", "Identifier"}},
		{"MemberExpr", &MemberExpr{Object: &Identifier{}, Property: &Identifier{}},
			[]string{"Identifier", "Identifier"}},
		{"CallExpr", &CallExpr{Callee: &Identifier{}, Arguments: []Expression{&Identifier{}, &Identifier{}}},
			[]string{"Identifier", "Identifier", "Identifier"}},
		{"NewExpr", &NewExpr{Callee: &Identifier{}, Arguments: []Expression{&Identifier{}}},
			[]string{"Identifier", "Identifier"}},
		{"UnaryExpr", &UnaryExpr{Arg: &Identifier{}}, []string{"Identifier"}},
		{"UpdateExpr", &UpdateExpr{Arg: &Identifier{}}, []string{"Identifier"}},
		{"BinaryExpr", &BinaryExpr{Left: &Identifier{}, Right: &Identifier{}},
			[]string{"Identifier", "Identifier"}},
		{"LogicalExpr", &LogicalExpr{Left: &Identifier{}, Right: &Identifier{}},
			[]string{"Identifier", "Identifier"}},
		{"AssignExpr", &AssignExpr{Left: &Identifier{}, Right: &Identifier{}},
			[]string{"Identifier", "Identifier"}},
		{"ConditionalExpr", &ConditionalExpr{Test: &Identifier{}, Consequent: &Identifier{}, Alternate: &Identifier{}},
			[]string{"Identifier", "Identifier", "Identifier"}},
		{"SequenceExpr", &SequenceExpr{Expressions: []Expression{&Identifier{}, &Identifier{}, &Identifier{}}},
			[]string{"Identifier", "Identifier", "Identifier"}},
		{"SpreadElement", &SpreadElement{Arg: &Identifier{}}, []string{"Identifier"}},
		{"YieldExpr", &YieldExpr{Argument: &Identifier{}}, []string{"Identifier"}},
		{"AwaitExpr", &AwaitExpr{Argument: &Identifier{}}, []string{"Identifier"}},
		{"ArrayPattern", &ArrayPattern{Elements: []ArrayPatternElement{
			{Target: &Identifier{}},
			{Target: &Identifier{}, Default: &Identifier{}},
		}}, []string{"Identifier", "Identifier", "Identifier"}},
		{"ObjectPattern", &ObjectPattern{Properties: []ObjectPatternProperty{
			{Key: &Identifier{}, Value: &Identifier{}},
			{Key: &Identifier{}, Value: &Identifier{}, Default: &Identifier{}},
		}}, []string{"Identifier", "Identifier", "Identifier", "Identifier", "Identifier"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectChildTypes(tc.node)
			if len(got) != len(tc.want) {
				t.Fatalf("ForEachChild(%s) visited %d children %v, want %d %v", tc.name, len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ForEachChild(%s) child[%d] = %s, want %s (all: %v vs %v)", tc.name, i, got[i], tc.want[i], got, tc.want)
				}
			}
		})
	}
}

// TestForEachChildTermination 验证 fn 返回 true 时提前终止并回报 true。
func TestForEachChildTermination(t *testing.T) {
	// 命中第二个子节点后终止。
	hits := 0
	stopped := ForEachChild(&BlockStmt{Body: []Statement{&EmptyStmt{}, &EmptyStmt{}, &EmptyStmt{}}}, func(c Node) bool {
		hits++
		return hits == 2
	})
	if !stopped {
		t.Fatal("expected early stop to report true")
	}
	if hits != 2 {
		t.Fatalf("fn called %d times, want 2", hits)
	}
	// 不提前终止时返回 false。
	hits = 0
	stopped = ForEachChild(&BlockStmt{Body: []Statement{&EmptyStmt{}, &EmptyStmt{}}}, func(c Node) bool {
		hits++
		return false
	})
	if stopped {
		t.Fatal("expected no early stop")
	}
	if hits != 2 {
		t.Fatalf("fn called %d times, want 2", hits)
	}
}

// TestWalkSkipSubtree 验证 visit 返回 false 时跳过子树但继续兄弟节点。
func TestWalkSkipSubtree(t *testing.T) {
	prog := &Program{Body: []Statement{
		&FunctionDecl{Name: &Identifier{Name: "inner"}, Body: &BlockStmt{
			Body: []Statement{&ExprStmt{Expr: &Identifier{Name: "hidden"}}},
		}},
		&ExprStmt{Expr: &Identifier{Name: "visible"}},
	}}
	var visited []string
	Walk(prog, func(n Node) bool {
		if _, ok := n.(*FunctionDecl); ok {
			return false // 跳过函数子树
		}
		if id, ok := n.(*Identifier); ok {
			visited = append(visited, id.Name)
		}
		return true
	})
	if len(visited) != 1 || visited[0] != "visible" {
		t.Fatalf("Walk visited %v, want [visible]", visited)
	}
}
