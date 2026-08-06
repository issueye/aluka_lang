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
// 模块标识为相对入口的虚拟路径（M3，B2.3.1）。
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
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != 3 {
		t.Errorf("modules = %d, want 3 (main/util/smallpkg)", len(res.Modules))
	}
	if res.Entry != "main.ts" {
		t.Errorf("entry = %q, want main.ts (virtual path)", res.Entry)
	}
	// 解析映射（虚拟 key）：main.ts → util.ts 与 node_modules 包。
	table, ok := res.Resolutions["main.ts"]
	if !ok {
		t.Fatalf("no resolutions for entry key %q", "main.ts")
	}
	if table["./util.ts"] != "util.ts" {
		t.Errorf("resolutions['./util.ts'] = %q, want util.ts", table["./util.ts"])
	}
	if table["smallpkg"] != "node_modules/smallpkg/index.js" {
		t.Errorf("resolutions['smallpkg'] = %q, want node_modules/smallpkg/index.js", table["smallpkg"])
	}
	// 模块类型判定：util.ts 为 ESM，smallpkg/index.js 为 CJS。
	types := map[string]string{}
	for _, m := range res.Modules {
		types[m.Path] = m.ModuleType
	}
	if types["util.ts"] != "esm" {
		t.Errorf("util.ts type = %q, want esm", types["util.ts"])
	}
	if types["node_modules/smallpkg/index.js"] != "cjs" {
		t.Errorf("smallpkg type = %q, want cjs", types["node_modules/smallpkg/index.js"])
	}
}

// TestBuildJSONAsset：.json 依赖收集为资源（Assets）而非模块。
func TestBuildJSONAsset(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts":   `import cfg from './data.json' with { type: 'json' }; console.log(cfg.name);`,
		"data.json": `{ "name": "aluka", "count": 3 }`,
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
		t.Errorf("modules = %d, want 1 (json is asset not module)", len(res.Modules))
	}
	data, ok := res.Assets["data.json"]
	if !ok || string(data) != `{ "name": "aluka", "count": 3 }` {
		t.Errorf("assets['data.json'] = %q, want json bytes", string(data))
	}
	table := res.Resolutions["main.ts"]
	if table["./data.json"] != "data.json" {
		t.Errorf("resolutions['./data.json'] = %q, want data.json", table["./data.json"])
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
