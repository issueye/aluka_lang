package graph_test

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/astutil"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)


// oldCollectRefsReflect 从 git 3a512b7 抄录的旧反射实现（用于对拍差分）。
func oldCollectRefsReflect(node interface{}) map[string]int {
	refs := make(map[string]int)
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		}
	}
	collect := func(node interface{}) {
		switch n := node.(type) {
		case *ast.Identifier:
			refs[n.Name]++
		case *ast.VarDeclarator:
			if n.Init != nil {
				walk(reflect.ValueOf(n.Init))
			}
		case *ast.FunctionDecl:
			for _, d := range n.Defaults {
				if d != nil {
					walk(reflect.ValueOf(d))
				}
			}
			walk(reflect.ValueOf(n.Body))
		case *ast.ClassDecl:
			walk(reflect.ValueOf(n.SuperClass))
			walk(reflect.ValueOf(n.Body))
		default:
			walk(reflect.ValueOf(node))
		}
	}
	var walkStmt func(v reflect.Value)
	walkStmt = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collect(v.Elem().Interface())
			walkStmt(v.Elem())
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			collect(v.Interface())
			walkStmt(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walkStmt(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walkStmt(v.Field(i))
			}
		}
	}
	walkStmt(reflect.ValueOf(node))
	return refs
}

// TestImportSpecifierPruneDiff 找出"旧 CollectRefs 计为已引用、新实现未计"
// 的 import binding（新实现会把该 specifier 剪除，可能导致运行时未定义）。
func TestImportSpecifierPruneDiff(t *testing.T) {
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
		if unit.ModuleKind != module.ModuleESM {
			continue
		}
		oldRefs := oldCollectRefsReflect(unit.Program)
		newRefs := astutil.CollectRefs(unit.Program)
		for _, stmt := range unit.Program.Body {
			imp, ok := stmt.(*ast.ImportDecl)
			if !ok {
				continue
			}
			for _, spec := range imp.Specifiers {
				if spec.Imported == "*" || spec.Imported == "" {
					continue // 命名空间/默认导入走不同路径
				}
				if oldRefs[spec.Local] > 0 && newRefs[spec.Local] == 0 {
					t.Logf("PRUNE-DIFF %-55s binding=%-20s imported=%-25s src=%s",
						k, spec.Local, spec.Imported, imp.Source)
				}
			}
		}
	}
}
