package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/vue"
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

type isolatedSFCCompiler struct{}

func (isolatedSFCCompiler) Compile(src, name string) (*vue.CompileResult, error) {
	return &vue.CompileResult{
		Facade: `import C from "./Component.vue.__aluka_script.ts"; export * from "./Component.vue.__aluka_script.ts"; import { render } from "./Component.vue.__aluka_template.js"; C.render = render; export default C;`,
		Modules: []vue.GeneratedModule{
			{Name: "Component.vue.__aluka_script.ts", Source: `import helper from "./helper.ts"; export const answer: number = helper; export default { answer };`},
			{Name: "Component.vue.__aluka_template.js", Source: `export function render() { return "template"; }`},
		},
	}, nil
}

func TestBuildVueGeneratedModulesAreIsolated(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts":       `import Component, { answer } from "./Component.vue"; export { Component, answer };`,
		"Component.vue": `<template/>`,
		"helper.ts":     `export default 1;`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"), WithVueCompiler(isolatedSFCCompiler{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Component.vue", "Component.vue.__aluka_script.ts", "Component.vue.__aluka_template.js", "helper.ts"} {
		if _, ok := res.SourceUnits[key]; !ok {
			t.Fatalf("source unit %q missing; got %v", key, keysOf(res.SourceUnits))
		}
	}
	if got := res.SourceUnits["Component.vue.__aluka_script.ts"].SourceKind; got != module.SourceTypeScript {
		t.Fatalf("generated script source kind = %v, want TypeScript", got)
	}
	if strings.Contains(string(res.SourceUnits["Component.vue"].Source), "function render") {
		t.Fatal("facade contains template implementation; scopes were merged")
	}
	if got := res.Resolutions["Component.vue"]["./Component.vue.__aluka_script.ts"]; got != "Component.vue.__aluka_script.ts" {
		t.Fatalf("facade script resolution = %q", got)
	}
	if !strings.Contains(string(res.SourceUnits["Component.vue"].Source), "export * from") {
		t.Fatalf("facade lost named export forwarding: %s", res.SourceUnits["Component.vue"].Source)
	}
	if !strings.Contains(string(res.SourceUnits["Component.vue.__aluka_script.ts"].Source), "export const answer") {
		t.Fatalf("generated script lost named export: %s", res.SourceUnits["Component.vue.__aluka_script.ts"].Source)
	}
	if got := res.Resolutions["Component.vue.__aluka_template.js"]; got != nil {
		t.Fatalf("template should not have unexpected resolutions: %+v", got)
	}
	if got := res.Resolutions["Component.vue.__aluka_script.ts"]["./helper.ts"]; got != "helper.ts" {
		t.Fatalf("script relative resolution = %q, want helper.ts", got)
	}
}

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

// TestBuildBuiltinReferences：所有静态引用语境都记录 node:*，但不嵌入模块图。
func TestBuildBuiltinReferences(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `import fs from 'node:fs'; export { join } from 'node:path'; const http = require('node:http'); import('node:crypto'); console.log(fs, http);`,
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
		t.Fatalf("modules = %d, want 1", len(res.Modules))
	}
	if len(res.Builtins) != 4 {
		t.Fatalf("builtins = %d, want 4", len(res.Builtins))
	}
	want := map[string]bool{"node:fs": true, "node:path": true, "node:http": true, "node:crypto": true}
	for _, dep := range res.Builtins {
		if !want[dep.Spec] {
			t.Errorf("unexpected builtin %q", dep.Spec)
		}
		if dep.Source != "main.ts" {
			t.Errorf("builtin %q source = %q, want main.ts", dep.Spec, dep.Source)
		}
	}
}

func TestBuildDynamicDependency(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `export async function load() { return await import('./lazy.ts'); }`,
		"lazy.ts": `export const value = 7;`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DynamicDeps) != 1 {
		t.Fatalf("dynamic deps = %d, want 1", len(res.DynamicDeps))
	}
	dep := res.DynamicDeps[0]
	if dep.Source != "main.ts" || dep.Spec != "./lazy.ts" || dep.Target != "lazy.ts" {
		t.Fatalf("dynamic dep = %+v", dep)
	}
}

// TestBuildVueSFC：.vue 单文件组件在图构建期编译为 JS 模块；编译产物
// import 的 'vue' 运行时 helper 经 node_modules 正常解析（Vite 式架构：
// 编译器不内嵌运行时）。
func TestBuildVueSFC(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": "import Counter from './Counter.vue';\nconsole.log(Counter);",
		"Counter.vue": `<template>
  <div class="counter"><span class="count">{{ count }}</span></div>
</template>

<script>
import { ref } from 'vue';
export default { setup() { return { count: ref(0) }; } };
</script>`,
		"node_modules/vue/package.json": `{ "name": "vue", "version": "1.0.0", "main": "./index.js" }`,
		"node_modules/vue/index.js":     `export function h(t,p,c){return {type:t,props:p||{},children:c||[]}} export function ref(v){return {value:v}} export function unref(v){return v} export function toDisplayString(v){return String(v)}`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.SourceUnits["Counter.vue"]; !ok {
		t.Fatalf("Counter.vue not compiled into SourceUnits: %+v", keysOf(res.SourceUnits))
	}
	if _, ok := res.SourceUnits["node_modules/vue/index.js"]; !ok {
		t.Fatalf("vue runtime not resolved from node_modules: %+v", keysOf(res.SourceUnits))
	}
	table, ok := res.Resolutions["Counter.vue"]
	if !ok || table["vue"] != "node_modules/vue/index.js" {
		t.Errorf("Counter.vue 'vue' import not resolved: %+v", table)
	}
}

func keysOf(m map[string]*module.SourceUnit) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

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
