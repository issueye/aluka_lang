package graph_test

import (
	"os"
	"sort"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// oldHasTopLevelAwait 从 git 3a512b7 的 ast.go 抄录旧实现（对拍 TLA 判定差异）。
func oldHasTopLevelAwait(prog *ast.Program) bool {
	for _, stmt := range prog.Body {
		if oldStmtHasAwait(stmt) {
			return true
		}
	}
	return false
}

func oldStmtHasAwait(s ast.Statement) bool {
	switch n := s.(type) {
	case *ast.ExprStmt:
		return oldExprHasAwait(n.Expr)
	case *ast.VarDecl:
		for _, d := range n.Decls {
			if d.Init != nil && oldExprHasAwait(d.Init) {
				return true
			}
		}
		return false
	case *ast.ReturnStmt:
		return n.Arg != nil && oldExprHasAwait(n.Arg)
	case *ast.IfStmt:
		if oldExprHasAwait(n.Test) || oldStmtHasAwait(n.Consequent) {
			return true
		}
		return n.Alternate != nil && oldStmtHasAwait(n.Alternate)
	case *ast.WhileStmt:
		return oldExprHasAwait(n.Test) || oldStmtHasAwait(n.Body)
	case *ast.DoWhileStmt:
		return oldExprHasAwait(n.Test) || oldStmtHasAwait(n.Body)
	case *ast.ForStmt:
		if n.Test != nil && oldExprHasAwait(n.Test) {
			return true
		}
		if n.Update != nil && oldExprHasAwait(n.Update) {
			return true
		}
		return oldStmtHasAwait(n.Body)
	case *ast.ForInStmt:
		if oldExprHasAwait(n.Right) {
			return true
		}
		return oldStmtHasAwait(n.Body)
	case *ast.ForOfStmt:
		if n.IsAwait || oldExprHasAwait(n.Right) {
			return true
		}
		return oldStmtHasAwait(n.Body)
	case *ast.BlockStmt:
		for _, b := range n.Body {
			if oldStmtHasAwait(b) {
				return true
			}
		}
		return false
	case *ast.SwitchStmt:
		if oldExprHasAwait(n.Disc) {
			return true
		}
		for _, c := range n.Cases {
			if c.Test != nil && oldExprHasAwait(c.Test) {
				return true
			}
			for _, b := range c.Consequent {
				if oldStmtHasAwait(b) {
					return true
				}
			}
		}
		return false
	case *ast.TryStmt:
		if oldStmtHasAwait(n.Block) {
			return true
		}
		if n.Handler != nil && oldStmtHasAwait(n.Handler.Body) {
			return true
		}
		return n.Finally != nil && oldStmtHasAwait(n.Finally)
	case *ast.LabeledStmt:
		return oldStmtHasAwait(n.Body)
	case *ast.ThrowStmt:
		return n.Arg != nil && oldExprHasAwait(n.Arg)
	case *ast.FunctionDecl:
		return false
	default:
		return false
	}
}

func oldNodeHasAwait(n ast.Node) bool {
	if e, ok := n.(ast.Expression); ok {
		return oldExprHasAwait(e)
	}
	switch pat := n.(type) {
	case *ast.ArrayPattern:
		for _, el := range pat.Elements {
			if el.Default != nil && oldExprHasAwait(el.Default) {
				return true
			}
			if el.Target != nil && oldNodeHasAwait(el.Target) {
				return true
			}
		}
	case *ast.ObjectPattern:
		for _, prop := range pat.Properties {
			if prop.Default != nil && oldExprHasAwait(prop.Default) {
				return true
			}
			if prop.Key != nil && prop.Computed && oldExprHasAwait(prop.Key) {
				return true
			}
			if prop.Value != nil && oldNodeHasAwait(prop.Value) {
				return true
			}
		}
	}
	return false
}

func oldExprHasAwait(e ast.Expression) bool {
	switch n := e.(type) {
	case *ast.AwaitExpr:
		return true
	case *ast.UnaryExpr:
		return oldExprHasAwait(n.Arg)
	case *ast.BinaryExpr:
		return oldExprHasAwait(n.Left) || oldExprHasAwait(n.Right)
	case *ast.LogicalExpr:
		return oldExprHasAwait(n.Left) || oldExprHasAwait(n.Right)
	case *ast.AssignExpr:
		if oldNodeHasAwait(n.Left) {
			return true
		}
		return n.Right != nil && oldExprHasAwait(n.Right)
	case *ast.UpdateExpr:
		return oldExprHasAwait(n.Arg)
	case *ast.ConditionalExpr:
		return oldExprHasAwait(n.Test) || oldExprHasAwait(n.Consequent) || oldExprHasAwait(n.Alternate)
	case *ast.CallExpr:
		if oldExprHasAwait(n.Callee) {
			return true
		}
		for _, a := range n.Arguments {
			if oldExprHasAwait(a) {
				return true
			}
		}
		return false
	case *ast.MemberExpr:
		if oldExprHasAwait(n.Object) {
			return true
		}
		return n.Property != nil && oldExprHasAwait(n.Property)
	case *ast.NewExpr:
		if oldExprHasAwait(n.Callee) {
			return true
		}
		for _, a := range n.Arguments {
			if oldExprHasAwait(a) {
				return true
			}
		}
		return false
	case *ast.SequenceExpr:
		for _, e := range n.Expressions {
			if oldExprHasAwait(e) {
				return true
			}
		}
		return false
	case *ast.ArrayLit:
		for _, e := range n.Elements {
			if e != nil && oldExprHasAwait(e) {
				return true
			}
		}
		return false
	case *ast.ObjectLit:
		for _, p := range n.Properties {
			if p.Value != nil && oldExprHasAwait(p.Value) {
				return true
			}
		}
		return false
	case *ast.TemplateLit:
		for _, e := range n.Expressions {
			if oldExprHasAwait(e) {
				return true
			}
		}
		return false
	case *ast.TaggedTemplateExpr:
		if oldExprHasAwait(n.Tag) {
			return true
		}
		for _, e := range n.Template.Expressions {
			if oldExprHasAwait(e) {
				return true
			}
		}
		return false
	case *ast.FunctionExpr, *ast.ArrowFunc:
		return false
	default:
		return false
	}
}

// TestHasTopLevelAwaitDiff 对拍新旧 TLA 判定在 coding-agent 全模块上的差异。
func TestHasTopLevelAwaitDiff(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	resolver := module.NewResolver()
	candidates := []string{
		"E:/code/github/pi/packages/coding-agent/src/cli.ts",
		"E:/codes/github/pi/packages/coding-agent/src/cli.ts",
	}
	entry := ""
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			entry = c
			break
		}
	}
	if entry == "" {
		t.Skip("coding-agent not found")
	}
	gr, err := graph.Build(vm, resolver, entry)
	if err != nil {
		t.Fatalf("graph build: %v", err)
	}
	keys := make([]string, 0, len(gr.SourceUnits))
	for k := range gr.SourceUnits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		unit := gr.SourceUnits[k]
		old := oldHasTopLevelAwait(unit.Program)
		neu := ast.HasTopLevelAwait(unit.Program)
		if old != neu {
			t.Logf("TLA DIFF %-60s old=%v new=%v kind=%v", k, old, neu, unit.ModuleKind)
		}
	}
}
