package shake

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

// oldCollectRefsReflect 从 git 3a512b7 抄录的旧反射实现（对拍差分）。
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

// shakeWithRefs 复制 Shake 主流程，但引用收集器可注入（对拍旧实现）。
func shakeWithRefs(gr *graph.Result, entry string, collect func(ast.Node) map[string]int) (*Result, error) {
	keys := make([]string, 0, len(gr.SourceUnits))
	for key := range gr.SourceUnits {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	infos := make(map[string]*moduleInfo, len(keys))
	for _, key := range keys {
		unit := gr.SourceUnits[key]
		if unit.ModuleKind != module.ModuleESM {
			infos[key] = &moduleInfo{key: key, deps: depKeys(gr, key)}
			continue
		}
		if unit.Program == nil {
			return nil, nil
		}
		infos[key] = analyze(key, unit.Program, gr)
	}

	keep := map[string]bool{entry: true}
	usedExports := make(map[string]map[string]bool)
	queue := []string{entry}
	processed := make(map[string]bool)

	keepMod := func(key string) {
		if !keep[key] {
			keep[key] = true
			queue = append(queue, key)
		}
	}
	markUsed := func(key, name string) {
		if usedExports[key] == nil {
			usedExports[key] = make(map[string]bool)
		}
		if !usedExports[key][name] {
			usedExports[key][name] = true
			if processed[key] {
				processed[key] = false
				queue = append(queue, key)
			}
		}
	}
	markImportUsed := func(t, imported string) {
		switch imported {
		case "*":
			markUsed(t, "*")
			if tin := infos[t]; tin != nil && tin.prog != nil {
				for name := range tin.exports {
					markUsed(t, name)
				}
			}
		case "":
			markUsed(t, "default")
		default:
			markUsed(t, imported)
		}
	}

	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if processed[key] {
			continue
		}
		processed[key] = true
		info := infos[key]
		if info == nil {
			continue
		}
		if info.prog == nil {
			for dep := range info.deps {
				if tin := infos[dep]; tin != nil && tin.prog != nil {
					for name := range tin.exports {
						markUsed(dep, name)
					}
				}
				keepMod(dep)
			}
			continue
		}
		refs := collect(info.prog)
		handled := make(map[string]bool)
		for _, imp := range info.imports {
			t := resolveTarget(gr, key, imp.Spec)
			if t == "" {
				continue
			}
			handled[t] = true
			if len(imp.Specifiers) == 0 {
				keepMod(t)
				continue
			}
			for _, spec := range imp.Specifiers {
				if refs[spec.Local] > 0 {
					markImportUsed(t, spec.Imported)
					keepMod(t)
				} else if tin := infos[t]; tin != nil && tin.sideEffect {
					keepMod(t)
				}
			}
		}
		for _, re := range info.reExports {
			t := resolveTarget(gr, key, re.Source)
			if t == "" {
				continue
			}
			handled[t] = true
			if re.IsStar && re.StarName != "" {
				if usedExports[key]["*"] || usedExports[key][re.StarName] {
					keepMod(t)
					markUsed(t, "*")
					if tin := infos[t]; tin != nil && tin.prog != nil {
						for name := range tin.exports {
							markUsed(t, name)
						}
					}
				}
			} else if re.IsStar {
				keepMod(t)
				if usedExports[key]["*"] {
					markUsed(t, "*")
					if tin := infos[t]; tin != nil && tin.prog != nil {
						for name := range tin.exports {
							markUsed(t, name)
						}
					}
				} else {
					for name := range usedExports[key] {
						if name != "default" {
							markUsed(t, name)
						}
					}
				}
			} else {
				for _, spec := range re.Specifiers {
					if usedExports[key]["*"] || usedExports[key][spec.Exported] {
						markUsed(t, spec.Local)
						keepMod(t)
					}
				}
			}
		}
		for dep := range info.deps {
			if handled[dep] {
				continue
			}
			if tin := infos[dep]; tin != nil && tin.prog != nil {
				for name := range tin.exports {
					markUsed(dep, name)
				}
			}
			keepMod(dep)
		}
	}

	res := &Result{
		Kept:        keep,
		Resolutions: make(map[string]map[string]string, len(gr.Resolutions)),
		Assets:      gr.Assets,
	}
	for _, key := range keys {
		info := infos[key]
		if !keep[key] {
			res.Removed++
			continue
		}
		if info != nil && info.prog != nil {
			pruneModule(info, usedExports, infos, gr)
		}
		if table, ok := gr.Resolutions[key]; ok {
			res.Resolutions[key] = table
		}
	}
	return res, nil
}

// TestShakeKeptSetDiff 对比新旧 CollectRefs 下 shake 的保留模块集合。
func TestShakeKeptSetDiff(t *testing.T) {
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
	oldRes, err := shakeWithRefs(gr, entry, func(n ast.Node) map[string]int {
		return oldCollectRefsReflect(n)
	})
	if err != nil {
		t.Fatalf("old shake: %v", err)
	}
	newRes, err := shakeWithRefs(gr, entry, astutil.CollectRefs)
	if err != nil {
		t.Fatalf("new shake: %v", err)
	}
	var oldOnly, newOnly []string
	for k := range oldRes.Kept {
		if !newRes.Kept[k] {
			oldOnly = append(oldOnly, k)
		}
	}
	for k := range newRes.Kept {
		if !oldRes.Kept[k] {
			newOnly = append(newOnly, k)
		}
	}
	sort.Strings(oldOnly)
	sort.Strings(newOnly)
	t.Logf("old keeps but new drops (%d):", len(oldOnly))
	for _, k := range oldOnly {
		t.Logf("  DROPPED %s", k)
	}
	t.Logf("new keeps but old drops (%d):", len(newOnly))
	for _, k := range newOnly {
		t.Logf("  ADDED %s", k)
	}
}
