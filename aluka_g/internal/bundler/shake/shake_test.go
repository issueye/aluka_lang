package shake

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/ast"
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

	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Kept
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
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Kept
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
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	// lib.js 保留且未被剪枝（hello 全量 used → 导出不删）。
	if len(res.Kept) != 2 {
		t.Fatalf("kept modules = %d, want 2", len(res.Kept))
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
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Kept
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
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Kept
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
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Kept
	for _, name := range []string{"main.js", "barrel.js", "types.js"} {
		if !set[name] {
			t.Errorf("named namespace re-export removed %s: %v", name, set)
		}
	}
}

func TestShakeMarksShakenSourceUnit(t *testing.T) {
	gr := buildFixture(t, map[string]string{
		"main.js": "import { used } from './lib.js';\nconsole.log(used());\n",
		"lib.js":  "export function used() { return 'U'; }\nexport function unused() { return 'X'; }\n",
	}, "main.js")
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Kept["lib.js"] {
		t.Fatal("lib.js missing from kept")
	}
	unit := gr.SourceUnits["lib.js"]
	if unit == nil {
		t.Fatal("lib.js source unit missing")
	}
	if unit.Stage&module.StageShaken == 0 {
		t.Fatal("tree-shaken source unit missing StageShaken")
	}
	// 剪枝应去掉 unused 的 export 包装（不再公开导出；函数声明本身按
	// 保守副作用判定可保留为普通声明）。
	exportedUnused := false
	for _, stmt := range unit.Program.Body {
		ed, ok := stmt.(*ast.ExportDecl)
		if !ok {
			continue
		}
		if fd, ok2 := ed.Declaration.(*ast.FunctionDecl); ok2 && fd.Name != nil && fd.Name.Name == "unused" {
			exportedUnused = true
		}
	}
	if exportedUnused {
		t.Error("unused export declaration survived pruning")
	}
}

func TestShakeDynamicImportKeepsAllExports(t *testing.T) {
	// 静态 import 只引用了 a；同模块又被动态 import——返回的 namespace
	// 在运行时全量可观察（ns.b 这类动态属性访问无法静态分析），
	// b 的导出不得被导出级裁剪（曾因 handled 短路兜底而漏保留）。
	gr := buildFixture(t, map[string]string{
		"main.js": "import { a } from './lib.js';\nconst ns = await import('./lib.js');\nconsole.log(a, ns.b);\n",
		"lib.js":  "export const a = 'A';\nexport const b = 'B';\n",
	}, "main.js")
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Kept["lib.js"] {
		t.Fatal("dynamic import target removed")
	}
	exported := exportedNames(gr.SourceUnits["lib.js"])
	for _, name := range []string{"a", "b"} {
		if !exported[name] {
			t.Errorf("export %q pruned despite dynamic import", name)
		}
	}
}

func TestShakeDynamicImportThroughStarBarrel(t *testing.T) {
	// 动态 import 目标为 export * barrel：namespace 全量可观察，需要
	// "*" 标记沿 export * 链传播（"*" 缺失时链会断裂、深层模块被剪）。
	gr := buildFixture(t, map[string]string{
		"main.js":   "const ns = await import('./barrel.js');\nconsole.log(ns.a);\n",
		"barrel.js": "export * from './a.js';\nexport * from './b.js';\n",
		"a.js":      "export const a = 'A';\n",
		"b.js":      "export const b = 'B';\n",
	}, "main.js")
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Kept
	for _, name := range []string{"main.js", "barrel.js", "a.js", "b.js"} {
		if !set[name] {
			t.Errorf("dynamic import chain removed %s: %v", name, set)
		}
	}
}

func TestShakeDynamicImportFoldedSpecifier(t *testing.T) {
	// 非字面量但可常量折叠（字符串拼接）：graph.collectDeps 折叠后仍
	// 解析进 Resolutions，静态/动态混用时同样必须保守保留全部导出。
	gr := buildFixture(t, map[string]string{
		"main.js": "import { a } from './lib.js';\nconst ns = await import('./li' + 'b.js');\nconsole.log(a, ns.b);\n",
		"lib.js":  "export const a = 'A';\nexport const b = 'B';\n",
	}, "main.js")
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Kept["lib.js"] {
		t.Fatal("folded dynamic import target removed")
	}
	exported := exportedNames(gr.SourceUnits["lib.js"])
	for _, name := range []string{"a", "b"} {
		if !exported[name] {
			t.Errorf("export %q pruned despite folded dynamic import", name)
		}
	}
}

// exportedNames 提取剪枝后仍公开导出的名字（含 default）。
func exportedNames(unit *module.SourceUnit) map[string]bool {
	names := map[string]bool{}
	if unit == nil || unit.Program == nil {
		return names
	}
	for _, stmt := range unit.Program.Body {
		switch n := stmt.(type) {
		case *ast.ExportDecl:
			if n.Declaration != nil {
				for _, name := range ast.DeclNames(n.Declaration) {
					names[name] = true
				}
			}
			for _, spec := range n.Specifiers {
				names[spec.Exported] = true
			}
		case *ast.ExportDefaultDecl:
			names["default"] = true
		}
	}
	return names
}

func TestShakeFiltersUnusedNamedReExportSpecifiers(t *testing.T) {
	// Vue runtime-core 一类：`export { used, unused } from './dep'` 只要
	// 有一个名字被用就会整句保留；原生 ESM 链接要求未使用的名字也从
	// 目标模块导出，否则出现 TrackOpTypes 这类 "does not provide export"。
	gr := buildFixture(t, map[string]string{
		"main.js":       "import { used } from './core.js';\nconsole.log(used);\n",
		"core.js":       "export { used, unused } from './reactivity.js';\n",
		"reactivity.js": "export const used = 1;\nexport const unused = 2;\n",
	}, "main.js")
	res, err := Shake(gr, gr.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Kept["core.js"] || !res.Kept["reactivity.js"] {
		t.Fatalf("kept = %v", res.Kept)
	}
	core := gr.SourceUnits["core.js"]
	if core == nil || core.Program == nil {
		t.Fatal("core.js AST missing")
	}
	var exported []string
	for _, stmt := range core.Program.Body {
		ed, ok := stmt.(*ast.ExportDecl)
		if !ok || ed.Source == "" {
			continue
		}
		for _, spec := range ed.Specifiers {
			exported = append(exported, spec.Exported)
		}
	}
	if len(exported) != 1 || exported[0] != "used" {
		t.Fatalf("re-export specifiers = %v, want [used]", exported)
	}
}
