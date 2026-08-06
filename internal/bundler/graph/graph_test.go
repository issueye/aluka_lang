package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// newTestEnv 创建临时项目目录并写入文件。
func newTestEnv(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildMultiFile：ESM 导入 + CJS require + node_modules 包的多模块图。
func TestBuildMultiFile(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `import { greet } from './util.ts';
			const { magic } = require('smallpkg');
			console.log(greet('x'), magic());`,
		"util.ts":                            `export function greet(n) { return 'hi ' + n; }`,
		"node_modules/smallpkg/package.json": `{ "name": "smallpkg", "main": "./index.js" }`,
		"node_modules/smallpkg/index.js":     `module.exports = { magic: () => 7 };`,
	})

	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "main.ts")
	res, err := Build(vm, module.NewResolver(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != 3 {
		t.Errorf("modules = %d, want 3 (main/util/smallpkg)", len(res.Modules))
	}
	// 解析映射：main → util.ts（import 语境）与 smallpkg（require 语境）。
	mainAbs, _ := filepath.Abs(entry)
	table, ok := res.Resolutions[mainAbs]
	if !ok {
		t.Fatalf("no resolutions for entry %q", mainAbs)
	}
	utilAbs, _ := filepath.Abs(filepath.Join(dir, "util.ts"))
	if table["./util.ts"] != utilAbs {
		t.Errorf("resolutions['./util.ts'] = %q, want %q", table["./util.ts"], utilAbs)
	}
	pkgAbs, _ := filepath.Abs(filepath.Join(dir, "node_modules", "smallpkg", "index.js"))
	if table["smallpkg"] != pkgAbs {
		t.Errorf("resolutions['smallpkg'] = %q, want %q", table["smallpkg"], pkgAbs)
	}
	// 模块类型判定：util.ts 为 ESM，smallpkg/index.js 为 CJS。
	types := map[string]string{}
	for _, m := range res.Modules {
		types[m.Path] = m.ModuleType
	}
	if types[utilAbs] != "esm" {
		t.Errorf("util.ts type = %q, want esm", types[utilAbs])
	}
	if types[pkgAbs] != "cjs" {
		t.Errorf("smallpkg type = %q, want cjs", types[pkgAbs])
	}
}

// TestBuildCircularDeps：循环依赖不无限递归，去重后只编译一次。
func TestBuildCircularDeps(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"a.ts": `import { b } from './b.ts'; export const a = 'A' + b;`,
		"b.ts": `import { a } from './a.ts'; export const b = 'B';`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "a.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != 2 {
		t.Errorf("modules = %d, want 2 (a/b each once)", len(res.Modules))
	}
}

// TestBuildBuiltinSkipped：内置模块（node:fs）不嵌入也不报错。
func TestBuildBuiltinSkipped(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `import fs from 'node:fs'; console.log(typeof fs.readFileSync);`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != 1 {
		t.Errorf("modules = %d, want 1 (builtin not embedded)", len(res.Modules))
	}
}
