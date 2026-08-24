package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/plugin"
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

func (isolatedSFCCompiler) Compile(req vue.CompileRequest) (*vue.CompileResult, error) {
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

func TestBuildRequireFoldedSpecifier(t *testing.T) {
	// require 非字面量但可常量折叠（字符串拼接）：与 __import 分支对齐，
	// 折叠后解析进依赖图（此前静默漏图 → web 产物缺模块且无警告）。
	dir := newTestEnv(t, map[string]string{
		"main.js": "const m = require('./li' + 'b.js');\nconsole.log(m.v);",
		"lib.js":  "exports.v = 42;",
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UnresolvedDynamic) != 0 {
		t.Fatalf("unresolved = %v, want none", res.UnresolvedDynamic)
	}
	table := res.Resolutions["main.js"]
	if table == nil || table["./lib.js"] != "lib.js" {
		t.Fatalf("folded require not resolved: %v", table)
	}
	if _, ok := res.SourceUnits["lib.js"]; !ok {
		t.Fatalf("lib.js not in graph: %v", keysOf(res.SourceUnits))
	}
}

func TestBuildRequireNonConstantUnresolved(t *testing.T) {
	// require(pathVar)：无法静态解析 → 记入 UnresolvedDynamic（web 构建
	// 期报错、--compile 警告 + 运行期按 RootDir 回退），不再是静默漏图。
	dir := newTestEnv(t, map[string]string{
		"main.js": "const name = './lib.js';\nconst m = require(name);\nconsole.log(m.v);",
		"lib.js":  "exports.v = 42;",
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UnresolvedDynamic) != 1 || res.UnresolvedDynamic[0] != "main.js" {
		t.Fatalf("unresolved = %v, want [main.js]", res.UnresolvedDynamic)
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

func TestBuildVueSFCStylesAndSrc(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts":                       "import App from './App.vue';\nconsole.log(App);",
		"theme.css":                     ".ext{display:block}",
		"App.vue":                       "<template><div class=\"app\">x</div></template>\n<style scoped>.app{color:red}</style>\n<style src=\"./theme.css\"></style>\n<script>import { ref } from 'vue'; export default { setup(){ return { n: ref(1) } } }</script>",
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
	styleKey := "App.vue.__aluka_style.0.css"
	if _, ok := res.Assets[styleKey]; !ok {
		t.Fatalf("scoped css asset missing; assets=%v", keysOfBytes(res.Assets))
	}
	id := vue.ScopeID("App.vue")
	if !strings.Contains(string(res.Assets[styleKey]), "data-v-"+id) {
		t.Fatalf("scoped css = %s", res.Assets[styleKey])
	}
	if _, ok := res.Assets["App.vue.__aluka_style.1.css"]; !ok {
		if _, ok := res.Assets["theme.css"]; !ok {
			// src style is a generated virtual module next to the SFC
			t.Fatalf("src css asset missing; assets=%v", keysOfBytes(res.Assets))
		}
	}
	foundTheme := false
	for path := range res.Assets {
		if strings.Contains(string(res.Assets[path]), ".ext{display:block}") || strings.Contains(string(res.Assets[path]), ".ext{display:block") {
			foundTheme = true
		}
	}
	if !foundTheme {
		t.Fatalf("theme.css content not in assets: %v", keysOfBytes(res.Assets))
	}
	vueFile := filepath.Join(dir, "App.vue")
	themeFile := filepath.Join(dir, "theme.css")
	if !containsPath(res.WatchFiles, vueFile) || !containsPath(res.WatchFiles, themeFile) {
		t.Fatalf("WatchFiles missing vue/theme: %v", res.WatchFiles)
	}
}

func TestBuildVueMissingStyleSrc(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts":                       "import App from './App.vue';\nconsole.log(App);",
		"App.vue":                       "<template><div/></template>\n<style src=\"./missing.css\"></style>\n<script>export default {}</script>",
		"node_modules/vue/package.json": `{ "name": "vue", "version": "1.0.0", "main": "./index.js" }`,
		"node_modules/vue/index.js":     `export function h(t,p,c){return {type:t,props:p||{},children:c||[]}} export function unref(v){return v} export function toDisplayString(v){return String(v)}`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"))
	if err == nil || !strings.Contains(err.Error(), "src") {
		t.Fatalf("missing style src error = %v", err)
	}
}

func keysOfBytes(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsPath(files []string, want string) bool {
	want, _ = filepath.Abs(want)
	for _, f := range files {
		got, _ := filepath.Abs(f)
		if got == want {
			return true
		}
	}
	return false
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

type virtualHost struct {
	plugin.Nop
}

func (virtualHost) ResolveId(id, _ string) (string, bool, error) {
	if id == "virtual:ok" {
		return "\x00virtual:ok", true, nil
	}
	return "", false, nil
}

func (virtualHost) Load(id string) (string, bool, error) {
	if id == "\x00virtual:ok" {
		return "export const v = 1;\n", true, nil
	}
	return "", false, nil
}

func (virtualHost) Transform(id, code string) (string, error) {
	if strings.HasSuffix(id, "main.ts") {
		return code + "\nexport const tagged = 1;\n", nil
	}
	return code, nil
}

func TestBuildPluginVirtualModule(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `import { v } from "virtual:ok"; export { v };`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"), WithPlugins(virtualHost{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.SourceUnits["plugin/virtual-ok.js"]; !ok {
		t.Fatalf("virtual module missing; units = %v", keysOf(res.SourceUnits))
	}
	if got := res.Resolutions["main.ts"]["virtual:ok"]; got != "plugin/virtual-ok.js" {
		t.Fatalf("resolution = %q", got)
	}
	if !strings.Contains(string(res.SourceUnits["main.ts"].Source), "tagged") {
		t.Fatalf("transform not applied: %s", res.SourceUnits["main.ts"].Source)
	}
}

type externalHost struct {
	plugin.Nop
}

func (externalHost) ResolveId(id, _ string) (string, bool, error) {
	if id == "ext:skip" {
		return "", true, nil
	}
	return "", false, nil
}

func TestBuildPluginResolveIdFalseExternal(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `import "ext:skip"; export const n = 1;`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"), WithPlugins(externalHost{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Resolutions["main.ts"]["ext:skip"]; ok {
		t.Fatalf("external import should not be in Resolutions: %#v", res.Resolutions["main.ts"])
	}
	if len(res.SourceUnits) != 1 {
		t.Fatalf("units = %v, want only main.ts", keysOf(res.SourceUnits))
	}
}

type cssTransformHost struct {
	plugin.Nop
}

func (cssTransformHost) Transform(id, code string) (string, error) {
	if strings.HasSuffix(strings.ToLower(id), ".css") {
		return code + "/*p*/", nil
	}
	return code, nil
}

func TestBuildPluginTransformCSS(t *testing.T) {
	dir := newTestEnv(t, map[string]string{
		"main.ts": `import "./a.css"; export const n = 1;`,
		"a.css":   `body{color:red}`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(vm, module.NewResolver(), filepath.Join(dir, "main.ts"), WithPlugins(cssTransformHost{}))
	if err != nil {
		t.Fatal(err)
	}
	css, ok := res.Assets["a.css"]
	if !ok {
		t.Fatalf("css asset missing: %#v", res.Assets)
	}
	if !strings.Contains(string(css), "/*p*/") {
		t.Fatalf("css transform missing: %s", css)
	}
}
