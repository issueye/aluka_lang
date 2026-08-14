package graph_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/bundler/shake"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// TestDebugConfigShakeLower 复现 build.go 的 shake + ESM lower 管线，
// 打印 config.ts lower 后的 fs 导入绑定语句，定位 readFileSync 为何 undefined。
func TestDebugConfigShakeLower(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	resolver := module.NewResolver()
	entry := "E:/codes/github/pi/packages/coding-agent/src/config.ts"
	gr, err := graph.Build(vm, resolver, entry)
	if err != nil {
		t.Fatalf("graph build: %v", err)
	}
	res, err := shake.Shake(gr, entry)
	if err != nil {
		t.Fatalf("shake: %v", err)
	}
	unit := gr.SourceUnits["config.ts"]
	if unit == nil {
		keys := make([]string, 0, len(gr.SourceUnits))
		for k := range gr.SourceUnits {
			keys = append(keys, k)
		}
		t.Fatalf("config.ts unit not found; keys: %v", keys)
	}
	if !res.Kept[entry] {
		t.Fatal("config.ts not kept by shake")
	}
	t.Logf("module kind: %v", unit.ModuleKind)
	// 复现 compile.CompileSourceUnit：ESM → clone + TransformESMToCJS。
	prog := ast.DeepCopy(unit.Program)
	if unit.ModuleKind == module.ModuleESM {
		prog = module.TransformESMToCJS(prog, "config.ts")
	}
	for i, stmt := range prog.Body {
		fmt.Printf("stmt[%d] %T: %s\n", i, stmt, summarize(stmt))
		if i > 48 {
			break
		}
	}
}

func blockSummaries(stmts []ast.Statement) []string {
	out := make([]string, 0, len(stmts))
	for _, s := range stmts {
		out = append(out, summarize(s))
	}
	return out
}

func summarizeHandler(h *ast.CatchHandler) string {
	if h == nil || h.Body == nil {
		return "<none>"
	}
	out := ""
	for _, s := range h.Body.Body {
		out += "[" + summarize(s) + "]"
	}
	return out
}

func summarize(s ast.Statement) string {
	switch n := s.(type) {
	case *ast.VarDecl:
		out := n.Kind + " "
		for _, d := range n.Decls {
			if d.Name != nil {
				out += d.Name.Name + " = " + exprStr(d.Init) + "; "
			}
		}
		return out
	case *ast.ExprStmt:
		return "ExprStmt " + exprStr(n.Expr)
	case *ast.BlockStmt:
		return "Block{" + strings.Join(blockSummaries(n.Body), " | ") + "}"
	case *ast.TryStmt:
		return "Try{block=" + summarize(n.Block) + " handler=" + summarizeHandler(n.Handler) + "}"
	default:
		return ""
	}
}

func exprStr(e ast.Expression) string {
	switch n := e.(type) {
	case *ast.Identifier:
		return n.Name
	case *ast.MemberExpr:
		return exprStr(n.Object) + "." + exprStr(n.Property)
	case *ast.CallExpr:
		args := make([]string, len(n.Arguments))
		for i, a := range n.Arguments {
			args[i] = exprStr(a)
		}
		return exprStr(n.Callee) + "(" + strings.Join(args, ",") + ")"
	case *ast.ConditionalExpr:
		return exprStr(n.Test) + " ? ... : ..."
	case *ast.BinaryExpr:
		return exprStr(n.Left) + " " + n.Op + " " + exprStr(n.Right)
	case *ast.AssignExpr:
		return "= " + exprStr(n.Right)
	case *ast.StringLit:
		return "'" + n.Value + "'"
	case nil:
		return "<nil>"
	default:
		return fmt.Sprintf("<%T>", e)
	}
}
