package shake

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/astutil"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// typeName 返回节点类型名。
func typeName(n ast.Node) string {
	return fmt.Sprintf("%T", n)
}

// digestAST 计算 Program 的结构哈希（按语句类型+关键字段）用于粗粒度对比。
func digestAST(prog *ast.Program) string {
	h := sha256.New()
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			h.Write([]byte{0})
			return
		}
		h.Write([]byte{1})
		h.Write([]byte(typeName(n)))
		ast.ForEachChild(n, func(c ast.Node) bool {
			walk(c)
			return false
		})
	}
	// 顶层语句的粗粒度：语句类型计数 + ImportDecl.Source/Specifiers 细节。
	for _, s := range prog.Body {
		h.Write([]byte(typeName(s)))
		if imp, ok := s.(*ast.ImportDecl); ok {
			h.Write([]byte(imp.Source))
			for _, sp := range imp.Specifiers {
				h.Write([]byte(sp.Imported))
				h.Write([]byte(sp.Local))
			}
		}
		if ex, ok := s.(*ast.ExportDecl); ok {
			h.Write([]byte(ex.Source))
			for _, sp := range ex.Specifiers {
				h.Write([]byte(sp.Local))
				h.Write([]byte(sp.Exported))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestShakePrunedConfigDiff 对比新旧 shake 后 config.ts 的剪枝结果。
func TestShakePrunedConfigDiff(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	entry := "E:/codes/github/pi/packages/coding-agent/src/cli.ts"

	buildAndShake := func(refs func(ast.Node) map[string]int) (string, string, string) {
		resolver := module.NewResolver()
		gr, err := graph.Build(vm, resolver, entry)
		if err != nil {
			t.Fatalf("graph build: %v", err)
		}
		res, err := shakeWithRefs(gr, entry, refs)
		if err != nil {
			t.Fatalf("shake: %v", err)
		}
		unit := gr.SourceUnits["config.ts"]
		prog := ast.DeepCopy(unit.Program)
		if unit.ModuleKind == module.ModuleESM {
			prog = module.TransformESMToCJS(prog, "config.ts")
		}
		return digestAST(prog), digestAST(unit.Program), func() string { _ = res; return "" }()
	}

	oldDigest, oldRaw, _ := buildAndShake(func(n ast.Node) map[string]int { return oldCollectRefsReflect(n) })
	_ = oldRaw
	newDigest, newRaw, _ := buildAndShake(astutil.CollectRefs)
	_ = newRaw
	t.Logf("old post-shake digest: %s", oldDigest)
	t.Logf("new post-shake digest: %s", newDigest)
	if oldDigest != newDigest {
		t.Logf("CONFIG POST-SHAKE DIFFERS between old/new CollectRefs")
	}
	_ = astutil.CollectRefs
}
