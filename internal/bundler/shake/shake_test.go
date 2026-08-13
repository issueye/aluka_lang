package shake

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// buildFixture 构造临时项目并构建模块图。
func buildFixture(t *testing.T, files map[string]string, entry string) *graph.Result {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	gr, err := graph.Build(vm, module.NewResolver(), filepath.Join(dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	return gr
}

// pathSet 提取模块路径集合。
func pathSet(gr *graph.Result) map[string]bool {
	set := make(map[string]bool)
	for _, m := range gr.Modules {
		set[m.Path] = true
	}
	return set
}

func TestShakeRemovesUnusedModule(t *testing.T) {
	gr := buildFixture(t, map[string]string{
		"main.js":   "import { used } from './lib.js';\nimport './side.js';\nimport { dead } from './dead.js';\nimport { never } from './unused.js';\nconsole.log(used());\n",
		"lib.js":    "export function used() { return 'U'; }\nexport function unused() { return 'X'; }\n",
		"dead.js":   "export function dead() { return 'D'; }\n",
		"side.js":   "globalThis.__n = (globalThis.__n || 0) + 1;\nexport const y = 2;\n",
		"unused.js": "export const never = 1;\n",
	}, "main.js")
	if len(gr.Modules) != 5 {
		t.Fatalf("fixture: want 5 modules, got %d", len(gr.Modules))
	}

	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := pathSet(&graph.Result{Modules: res.Modules})
	if !set["main.js"] || !set["lib.js"] || !set["side.js"] {
		t.Errorf("kept modules missing: %v", set)
	}
	if set["dead.js"] || set["unused.js"] {
		t.Errorf("unused modules not removed: %v", set)
	}
	if res.Removed != 2 {
		t.Errorf("Removed = %d, want 2", res.Removed)
	}
}

func TestShakeKeepsSideEffectModule(t *testing.T) {
	// 带副作用模块即使导出未用也保留（import 语句执行目标）。
	gr := buildFixture(t, map[string]string{
		"main.js": "import { a } from './se.js';\nconsole.log(typeof a);\n",
		"se.js":   "globalThis.__ran = true;\nexport const a = 1;\nexport const b = 2;\n",
	}, "main.js")
	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := pathSet(&graph.Result{Modules: res.Modules})
	if !set["se.js"] {
		t.Fatal("side-effect module removed")
	}
	if res.Removed != 0 {
		t.Errorf("Removed = %d, want 0", res.Removed)
	}
}

func TestShakeKeepsCJSRequireExports(t *testing.T) {
	// CJS require ESM 模块：导出全量保留（require 使用不可静态分析）。
	gr := buildFixture(t, map[string]string{
		"main.js": "const m = require('./lib.js');\nconsole.log(m.hello());\n",
		"lib.js":  "export function hello() { return 'H'; }\nexport function unused() { return 'X'; }\n",
	}, "main.js")
	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	// lib.js 保留且未被剪枝（hello 全量 used → 导出不删）。
	if len(res.Modules) != 2 {
		t.Fatalf("modules = %d, want 2", len(res.Modules))
	}
}

func TestShakeReExportPruning(t *testing.T) {
	// re-export：未使用的 re-export 语句删除，目标模块可剪除。
	gr := buildFixture(t, map[string]string{
		"main.js":   "import { a } from './barrel.js';\nconsole.log(a);\n",
		"barrel.js": "export { a } from './a.js';\nexport { b } from './b.js';\n",
		"a.js":      "export const a = 'A';\n",
		"b.js":      "export const b = 'B';\n",
	}, "main.js")
	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := pathSet(&graph.Result{Modules: res.Modules})
	if !set["barrel.js"] || !set["a.js"] {
		t.Errorf("kept modules missing: %v", set)
	}
	if set["b.js"] {
		t.Errorf("re-exported unused module not removed: %v", set)
	}
}

func TestShakeNamespaceImportThroughStarBarrel(t *testing.T) {
	gr := buildFixture(t, map[string]string{
		"main.js":   "import * as T from './barrel.js';\nconsole.log(T.a, T.b);\n",
		"barrel.js": "export * from './a.js';\nexport * from './nested.js';\n",
		"nested.js": "export * from './b.js';\n",
		"a.js":      "export const a = 'A';\n",
		"b.js":      "export const b = 'B';\n",
	}, "main.js")
	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := pathSet(&graph.Result{Modules: res.Modules})
	for _, name := range []string{"main.js", "barrel.js", "nested.js", "a.js", "b.js"} {
		if !set[name] {
			t.Errorf("namespace import removed %s: %v", name, set)
		}
	}
}

func TestShakeNamedNamespaceReExport(t *testing.T) {
	gr := buildFixture(t, map[string]string{
		"main.js":   "import { Type } from './barrel.js';\nconsole.log(Type.a);\n",
		"barrel.js": "export * as Type from './types.js';\n",
		"types.js":  "export const a = 'A';\nexport const b = 'B';\n",
	}, "main.js")
	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := pathSet(&graph.Result{Modules: res.Modules})
	for _, name := range []string{"main.js", "barrel.js", "types.js"} {
		if !set[name] {
			t.Errorf("named namespace re-export removed %s: %v", name, set)
		}
	}
}

func TestShakeMarksRecompiledModules(t *testing.T) {
	gr := buildFixture(t, map[string]string{
		"main.js": "import { used } from './lib.js';\nconsole.log(used());\n",
		"lib.js":  "export function used() { return 'U'; }\nexport function unused() { return 'X'; }\n",
	}, "main.js")
	vm, _ := interpreter.NewVM()
	res, err := Shake(vm, gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Modules {
		if m.Path == "lib.js" {
			if !m.Transformed {
				t.Fatal("tree-shaken module not marked Transformed")
			}
			return
		}
	}
	t.Fatal("lib.js missing from result")
}
