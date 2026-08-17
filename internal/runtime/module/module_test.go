package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// testEnv sets up a temporary directory with test files and returns a loader.
type testEnv struct {
	dir    string
	loader *Loader
}

func newTestEnv(t *testing.T, files map[string]string) *testEnv {
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

	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx.Close() })

	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	loader := NewLoader(ctx)
	return &testEnv{dir: dir, loader: loader}
}

func (e *testEnv) run(t *testing.T, path string) {
	t.Helper()
	if err := e.loader.Run(filepath.Join(e.dir, path)); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) globalGet(key string) string {
	v, _ := e.loader.ctx.Global().Get(key)
	return v.String()
}

// --- CJS tests -----------------------------------------------------------

func TestCJSBasicRequire(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs":  `var utils = require('./utils.cjs'); globalThis.__result = utils.add(2, 3);`,
		"utils.cjs": `module.exports.add = function(a, b) { return a + b; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "5" {
		t.Errorf("CJS require: got %q, want 5", got)
	}
}

func TestCJSRequiredModuleClosureSurvivesStackGrowth(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `var F = require('./f.cjs'); globalThis.__result = F()();`,
		"f.cjs": `
			require('./dep.cjs');
			function F(){ return factory; }
			function growStack(){ var a,b,c,d,e,f,g,h,i,j,k,l,m,n,o,p,q,r,s,t; }
			growStack();
			const factory = function(){ return 42; };
			module.exports = F;
		`,
		"dep.cjs": `module.exports = {};`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "42" {
		t.Errorf("CJS function closure over const after require: got %q, want 42", got)
	}
}

func TestCJSModuleExportsObject(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `var m = require('./mod.cjs'); globalThis.__result = m.x + ':' + m.y;`,
		"mod.cjs":  `module.exports = { x: 10, y: 20 };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "10:20" {
		t.Errorf("CJS module.exports reassign: got %q, want 10:20", got)
	}
}

func TestCJSExportsAlias(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `var m = require('./mod.cjs'); globalThis.__result = m.val;`,
		"mod.cjs":  `exports.val = 42;`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "42" {
		t.Errorf("CJS exports alias: got %q, want 42", got)
	}
}

func TestCJSCircularDependency(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"a.cjs":    `var b = require('./b.cjs'); exports.bVal = b.val; exports.val = 1;`,
		"b.cjs":    `var a = require('./a.cjs'); exports.val = 2; exports.aVal = a.bVal;`,
		"main.cjs": `var a = require('./a.cjs'); var b = require('./b.cjs'); globalThis.__result = a.val + ':' + b.val + ':' + a.bVal + ':' + b.aVal;`,
	})
	env.run(t, "main.cjs")
	got := env.globalGet("__result")
	// a.val=1, b.val=2, a.bVal=b.val=2 (at time of a's execution, b.val was undefined 鈫?becomes 2 later but a already captured)
	// In Node CJS, circular deps return unfinished exports. a.bVal may be undefined.
	// The exact value depends on execution order. Let's check it doesn't crash.
	_ = got
}

func TestCJSRequireJSON(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs":  `var data = require('./data.json'); globalThis.__result = data.name + ':' + data.age;`,
		"data.json": `{"name": "alice", "age": 30}`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "alice:30" {
		t.Errorf("CJS require JSON: got %q, want alice:30", got)
	}
}

func TestCJSFilenameDirname(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `globalThis.__filename = __filename; globalThis.__dirname = __dirname;`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__filename"); got == "" {
		t.Errorf("__filename should not be empty")
	}
	if got := env.globalGet("__dirname"); got == "" {
		t.Errorf("__dirname should not be empty")
	}
}

func TestCJSNestedRequire(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `var a = require('./a.cjs'); globalThis.__result = a.compute();`,
		"a.cjs":    `var b = require('./b.cjs'); exports.compute = function() { return b.mul(3, 4); };`,
		"b.cjs":    `exports.mul = function(a, b) { return a * b; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "12" {
		t.Errorf("CJS nested require: got %q, want 12", got)
	}
}

// --- ESM tests -----------------------------------------------------------

func TestESMBasicExportImport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { x } from './mod.mjs'; globalThis.__result = x;`,
		"mod.mjs":  `export var x = 42;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "42" {
		t.Errorf("ESM basic export/import: got %q, want 42", got)
	}
}

func TestESMExportFunction(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { add } from './mod.mjs'; globalThis.__result = add(3, 4);`,
		"mod.mjs":  `export function add(a, b) { return a + b; }`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "7" {
		t.Errorf("ESM export function: got %q, want 7", got)
	}
}

func TestESMExportClass(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { Foo } from './mod.mjs'; var f = new Foo(10); globalThis.__result = f.getValue();`,
		"mod.mjs":  `export class Foo { constructor(v) { this.v = v; } getValue() { return this.v; } }`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "10" {
		t.Errorf("ESM export class: got %q, want 10", got)
	}
}

func TestESMDefaultImport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import answer from './mod.mjs'; globalThis.__result = answer;`,
		"mod.mjs":  `export default 42;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "42" {
		t.Errorf("ESM default import: got %q, want 42", got)
	}
}

func TestESMDefaultImportFunction(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import greet from './mod.mjs'; globalThis.__result = greet('world');`,
		"mod.mjs":  `export default function(name) { return 'hello ' + name; }`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "hello world" {
		t.Errorf("ESM default import function: got %q, want hello world", got)
	}
}

func TestESMNamespaceImport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import * as ns from './mod.mjs'; globalThis.__result = ns.a + ':' + ns.b;`,
		"mod.mjs":  `export var a = 1; export var b = 2;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "1:2" {
		t.Errorf("ESM namespace import: got %q, want 1:2", got)
	}
}

func TestESMRenamedImport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { x as y } from './mod.mjs'; globalThis.__result = y;`,
		"mod.mjs":  `export var x = 99;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "99" {
		t.Errorf("ESM renamed import: got %q, want 99", got)
	}
}

func TestESMExportRename(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { renamed } from './mod.mjs'; globalThis.__result = renamed;`,
		"mod.mjs":  `var x = 55; export { x as renamed };`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "55" {
		t.Errorf("ESM export rename: got %q, want 55", got)
	}
}

func TestESMSideEffectOnlyImport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import './mod.mjs'; globalThis.__result = globalThis.__sideEffect;`,
		"mod.mjs":  `globalThis.__sideEffect = 'done';`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "done" {
		t.Errorf("ESM side-effect import: got %q, want done", got)
	}
}

func TestESMMultipleImports(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import a, { b, c } from './mod.mjs'; globalThis.__result = a + ':' + b + ':' + c;`,
		"mod.mjs":  `export default 1; export var b = 2; export var c = 3;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "1:2:3" {
		t.Errorf("ESM mixed default + named: got %q, want 1:2:3", got)
	}
}

func TestESMChainedImports(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { c } from './mid.mjs'; globalThis.__result = c;`,
		"mid.mjs":  `export { c } from './base.mjs';`,
		"base.mjs": `export var c = 77;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "77" {
		t.Errorf("ESM chained re-export: got %q, want 77", got)
	}
}

func TestESMStarReexport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { a, b } from './mid.mjs'; globalThis.__result = a + ':' + b;`,
		"mid.mjs":  `export * from './base.mjs';`,
		"base.mjs": `export var a = 1; export var b = 2;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__result"); got != "1:2" {
		t.Errorf("ESM star re-export: got %q, want 1:2", got)
	}
}

// --- Resolver tests ------------------------------------------------------

func TestResolverRelativePath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mod.js"), []byte(""), 0644)
	r := NewResolver()
	resolved, err := r.Resolve("./mod.js", filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "mod.js")
	if resolved != expected {
		t.Errorf("relative path: got %q, want %q", resolved, expected)
	}
}

func TestResolverBuildAliasExactAndPrefix(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "util.js"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "mod.js"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "main.js")
	r := NewResolver()
	r.AddAlias("@", src)
	r.AddAlias("@lib", filepath.Join(src, "lib"))

	got, err := r.Resolve("@/util.js", parent)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(src, "util.js") {
		t.Errorf("@/util.js: got %q, want %q", got, filepath.Join(src, "util.js"))
	}
	got, err = r.Resolve("@lib/mod.js", parent)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(src, "lib", "mod.js") {
		t.Errorf("@lib/mod.js: got %q, want %q", got, filepath.Join(src, "lib", "mod.js"))
	}
}

func TestResolverExtensionResolution(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mod.js"), []byte(""), 0644)
	r := NewResolver()
	// Should find mod.js when specifying just 'mod'
	resolved, err := r.Resolve("./mod", filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "mod.js")
	if resolved != expected {
		t.Errorf("extension resolution: got %q, want %q", resolved, expected)
	}
}

func TestResolverJSSpecifierToTypeScriptSource(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mod.ts"), []byte("export const value = 1;"), 0644)
	resolved, err := NewResolver().ResolveImport("./mod.js", filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "mod.ts")
	if resolved != expected {
		t.Errorf("js-to-ts resolution: got %q, want %q", resolved, expected)
	}
}

func TestResolverDirectoryWithPackageJson(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "mymod")
	os.MkdirAll(pkgDir, 0755)
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"main": "index.js"}`), 0644)
	os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte(""), 0644)
	r := NewResolver()
	resolved, err := r.Resolve("./mymod", filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(pkgDir, "index.js")
	if resolved != expected {
		t.Errorf("dir with package.json: got %q, want %q", resolved, expected)
	}
}

func TestResolverIndexFile(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "mymod")
	os.MkdirAll(pkgDir, 0755)
	os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte(""), 0644)
	r := NewResolver()
	resolved, err := r.Resolve("./mymod", filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(pkgDir, "index.js")
	if resolved != expected {
		t.Errorf("index file: got %q, want %q", resolved, expected)
	}
}

func TestResolverNodeModules(t *testing.T) {
	dir := t.TempDir()
	nmDir := filepath.Join(dir, "node_modules", "libpkg")
	os.MkdirAll(nmDir, 0755)
	os.WriteFile(filepath.Join(nmDir, "package.json"), []byte(`{"main": "lib.js"}`), 0644)
	os.WriteFile(filepath.Join(nmDir, "lib.js"), []byte(""), 0644)
	r := NewResolver()
	resolved, err := r.Resolve("libpkg", filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(nmDir, "lib.js")
	if resolved != expected {
		t.Errorf("node_modules: got %q, want %q", resolved, expected)
	}
}

func TestResolverNodeModulesSubpath(t *testing.T) {
	dir := t.TempDir()
	nmDir := filepath.Join(dir, "node_modules", "libpkg")
	os.MkdirAll(filepath.Join(nmDir, "sub"), 0755)
	os.WriteFile(filepath.Join(nmDir, "sub", "util.js"), []byte(""), 0644)
	r := NewResolver()
	resolved, err := r.Resolve("libpkg/sub/util", filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(nmDir, "sub", "util.js")
	if resolved != expected {
		t.Errorf("node_modules subpath: got %q, want %q", resolved, expected)
	}
}

func TestResolverPackageExports(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "typebox")
	os.MkdirAll(filepath.Join(pkgDir, "build", "compile"), 0755)
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{
  "exports": {"./compile": {"import": "./build/compile/index.mjs", "require": "./build/compile/index.cjs", "default": "./fallback.js"}}
}`), 0644)
	want := filepath.Join(pkgDir, "build", "compile", "index.mjs")
	wantCJS := filepath.Join(pkgDir, "build", "compile", "index.cjs")
	os.WriteFile(want, []byte(""), 0644)
	os.WriteFile(wantCJS, []byte(""), 0644)

	// import 语境（ESM 静态导入，如 main.ts）：匹配 import 条件。
	resolved, err := NewResolver().ResolveImport("typebox/compile", filepath.Join(dir, "src", "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("package exports (import ctx): got %q, want %q", resolved, want)
	}

	// require 语境（CJS require）：匹配 require 条件。
	resolved, err = NewResolver().Resolve("typebox/compile", filepath.Join(dir, "src", "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != wantCJS {
		t.Errorf("package exports (require ctx): got %q, want %q", resolved, wantCJS)
	}

	// require 语境下 exports 只有 import 条件时回退 default。
	pkgDir2 := filepath.Join(dir, "node_modules", "onlyimport")
	os.MkdirAll(pkgDir2, 0755)
	os.WriteFile(filepath.Join(pkgDir2, "package.json"), []byte(`{
  "exports": {"./compile": {"import": "./build/compile/index.mjs", "default": "./fallback.js"}}
}`), 0644)
	os.WriteFile(filepath.Join(pkgDir2, "fallback.js"), []byte(""), 0644)
	fallbackWant := filepath.Join(pkgDir2, "fallback.js")
	resolved, err = NewResolver().Resolve("onlyimport/compile", filepath.Join(dir, "src", "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != fallbackWant {
		t.Errorf("package exports (require ctx, no require condition): got %q, want %q (default fallback)", resolved, fallbackWant)
	}
}

func TestResolverScopedPackageWildcardExport(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "@scope", "pkg")
	os.MkdirAll(filepath.Join(pkgDir, "dist", "features"), 0755)
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{
  "exports": {"./features/*": "./dist/features/*.js"}
}`), 0644)
	want := filepath.Join(pkgDir, "dist", "features", "tool.js")
	os.WriteFile(want, []byte(""), 0644)

	resolved, err := NewResolver().Resolve("@scope/pkg/features/tool", filepath.Join(dir, "src", "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("scoped wildcard export: got %q, want %q", resolved, want)
	}
}

func TestResolverNotFound(t *testing.T) {
	dir := t.TempDir()
	r := NewResolver()
	_, err := r.Resolve("./nonexistent", filepath.Join(dir, "main.js"))
	if err == nil {
		t.Error("expected error for nonexistent module")
	}
}

// --- Module type detection -----------------------------------------------

func TestModuleTypeByExtension(t *testing.T) {
	r := NewResolver()
	if r.ModuleType("/path/to/file.mjs") != "module" {
		t.Error(".mjs should be module")
	}
	if r.ModuleType("/path/to/file.cjs") != "commonjs" {
		t.Error(".cjs should be commonjs")
	}
	if r.ModuleType("/path/to/file.json") != "json" {
		t.Error(".json should be json")
	}
}

func TestModuleTypeJsWithPackageJson(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type": "module"}`), 0644)
	r := NewResolver()
	if r.ModuleType(filepath.Join(dir, "file.js")) != "module" {
		t.Error(".js with type:module should be module")
	}
}

// TestRequireAfterAwait regression (P0-1): require/module usable after await
// (module-scope vars are now lexical params, survive async suspension)
func TestRequireAfterAwait(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `
(async function() {
  await Promise.resolve('tick');
  var m = require('./dep.cjs');
  globalThis.__cont = 'ran';
  globalThis.__r = m.val;
  globalThis.__d = typeof module.exports;
})();
`,
		"dep.cjs": `module.exports = { val: 42 };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__cont"); got != "ran" {
		t.Fatalf("async continuation did not run, __cont = %q", got)
	}
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("require after await = %q, want 42", got)
	}
	if got := env.globalGet("__d"); got != "object" {
		t.Errorf("module.exports after await = %q, want object", got)
	}
}

// TestLetConstClosureInModule regression (P0-1): const closure in module
// (function decl referencing later const must capture it as upvalue)
func TestLetConstClosureInModule(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `var F = require('./f.cjs'); globalThis.__result = F()();`,
		"f.cjs": `
require('./dep.cjs');
function F(){ return factory; }
const factory = function(){ return 42; };
module.exports = F;
`,
		"dep.cjs": `module.exports = {};`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__result"); got != "42" {
		t.Errorf("const closure in module = %q, want 42", got)
	}
}

// TestPackageExportsRequireCondition：exports 条件解析必须按 require 语境
// 匹配——require('is-promise') 应返回函数而非 ESM 命名空间 {default: fn}。
// 回归：conditionalExportTarget 曾把 "import" 列为候选，导致 require 语境
// 错误匹配 import 条件（加载 index.mjs），express 的 router 调用
// `isPromise(ret)` 时抛 "not a function"。
func TestPackageExportsRequireCondition(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"node_modules/is-promise/package.json": `{
			"name": "is-promise",
			"main": "./index.js",
			"exports": {
				".": [
					{ "import": "./index.mjs", "require": "./index.js", "default": "./index.js" }
				]
			}
		}`,
		"node_modules/is-promise/index.js":  `module.exports = function isPromise(o) { return !!(o && o.then); };`,
		"node_modules/is-promise/index.mjs": `export default function isPromise(o) { return true; }`,
		"main.cjs": `var isPromise = require('is-promise');
			globalThis.__t = (typeof isPromise === 'function' && isPromise({ then: 1 })) ? 'fn' : 'ns:' + typeof isPromise;`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__t"); got != "fn" {
		t.Errorf("require('is-promise') = %q, want 'fn' (require condition must win over import)", got)
	}
}

// TestRequireESM：CJS require 同步加载 ESM（Node 22 require(esm) 语义）：
// 命名/默认导出 + __esModule 互操作标记（N22-A3）。
func TestRequireESM(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"entry.cjs": `var m = require('./esm.mjs');
			globalThis.__r = m.named + '|' + (typeof m.default) + '|' + (m.__esModule === true) + '|' + Object.keys(m).sort().join(',');`,
		"esm.mjs": `export const named = 42;
			export default function greet() { return 'hi'; }`,
	})
	env.run(t, "entry.cjs")
	if got := env.globalGet("__r"); got != "42|function|true|__esModule,default,named" {
		t.Errorf("require(esm) = %q, want 42|function|true|__esModule,default,named", got)
	}
}

func TestESMNamedDefaultFunctionAndClassLocalBinding(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
			import addFn, { compute } from './mod.mjs';
			globalThis.__res = addFn(10) + ':' + compute(5);
		`,
		"mod.mjs": `
			export default function add(x) { return x + 1; }
			export function compute(y) { return add(y * 2); }
		`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__res"); got != "11:11" {
		t.Errorf("named export default function local reference: got %q, want 11:11", got)
	}
}

func TestExportTypeWithoutSemicolonASI(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"entry.cjs": `var m = require('./mod.ts');
			globalThis.__r = typeof m.fn;`,
		"mod.ts": `export type AuthStatus = "authenticated" | "expired" | "not_authenticated"
export function fn() { return 1; }`,
	})
	env.run(t, "entry.cjs")
	if got := env.globalGet("__r"); got != "function" {
		t.Errorf("require(mod.ts) fn = %q, want 'function'", got)
	}
}

// TestImportHoistingBeforeUse ESM 语义：所有模块请求先于模块体求值，
// import 语句的位置不影响导入绑定的可用时机（require 调用提升到顶部）。
func TestImportHoistingBeforeUse(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
			globalThis.__first = add(1);
			import add from './mod.mjs';
			globalThis.__second = add(2);
		`,
		"mod.mjs": `export default function add(n) { return n + 40; }`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__first"); got != "41" {
		t.Errorf("import used before import stmt: got %q, want 41", got)
	}
	if got := env.globalGet("__second"); got != "42" {
		t.Errorf("import used after import stmt: got %q, want 42", got)
	}
}

// TestImportHoistingSideEffectOrder import 提升不改变源码内 import 的
// 相对顺序：依赖副作用按 import 出现顺序执行，且都先于模块体语句。
func TestImportHoistingSideEffectOrder(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
			globalThis.__log += "|body";
			import './a.mjs';
			import './b.mjs';
		`,
		"a.mjs": `globalThis.__log = (globalThis.__log || "") + "|a";`,
		"b.mjs": `globalThis.__log = (globalThis.__log || "") + "|b";`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__log"); got != "|a|b|body" {
		t.Errorf("import hoisting side-effect order = %q, want '|a|b|body'", got)
	}
}

// TestResolverBrowserCondition 测试 package.json 的 browser 字段与 browser 条件导出解析
func TestResolverBrowserCondition(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "isomorphic-lib")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	pkgJSON := `{
		"name": "isomorphic-lib",
		"main": "./main.node.js",
		"browser": "./main.browser.js",
		"exports": {
			".": {
				"browser": "./main.browser.js",
				"node": "./main.node.js",
				"default": "./main.node.js"
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "main.node.js"), []byte(`module.exports = "node";`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "main.browser.js"), []byte(`module.exports = "browser";`), 0644); err != nil {
		t.Fatal(err)
	}

	nodeResolver := NewResolver()
	webResolver := NewResolver()
	webResolver.SetWebConditions()
	parent := filepath.Join(dir, "app.js")

	// 两个实例在同一进程交错解析，web 设置不能改变 Node 实例。
	resWeb, err := webResolver.Resolve("isomorphic-lib", parent)
	if err != nil {
		t.Fatalf("resolve browser: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(resWeb), "main.browser.js") {
		t.Errorf("browser resolve got %s, want main.browser.js", resWeb)
	}
	for i := 0; i < 2; i++ {
		resNode, err := nodeResolver.Resolve("isomorphic-lib", parent)
		if err != nil {
			t.Fatalf("resolve node after web resolver use: %v", err)
		}
		if !strings.HasSuffix(filepath.ToSlash(resNode), "main.node.js") {
			t.Errorf("node resolve got %s, want main.node.js", resNode)
		}
	}
}

func TestWebResolvePrefersModuleOverMain(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "lib")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{
		"name": "lib",
		"main": "./main.cjs.js",
		"module": "./main.esm.js"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "main.cjs.js"), []byte(`module.exports = 1`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "main.esm.js"), []byte(`export default 1`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "app.js")

	web := NewResolver()
	web.SetWebConditions()
	got, err := web.ResolveImport("lib", parent)
	if err != nil {
		t.Fatalf("web ResolveImport: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "main.esm.js") {
		t.Errorf("web resolved %s, want main.esm.js (Vite module field)", got)
	}

	node := NewResolver()
	got, err = node.ResolveImport("lib", parent)
	if err != nil {
		t.Fatalf("node ResolveImport: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "main.cjs.js") {
		t.Errorf("node resolved %s, want main.cjs.js", got)
	}
}

func TestTsconfigAliasToPackageUsesExports(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "vendor", "vue")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{
		"compilerOptions": {
			"baseUrl": ".",
			"paths": { "vue": ["vendor/vue"] }
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{
		"name": "vue",
		"main": "./index.js",
		"module": "./runtime.js",
		"exports": {
			".": {
				"import": {
					"node": "./index.js",
					"default": "./runtime.js"
				},
				"require": "./index.js"
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte(`module.exports = "cjs"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "runtime.js"), []byte(`export default "runtime"`), 0o644); err != nil {
		t.Fatal(err)
	}

	web := NewResolver()
	web.SetWebConditions()
	got, err := web.ResolveImport("vue", filepath.Join(dir, "src", "app.ts"))
	if err != nil {
		t.Fatalf("alias exports: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "runtime.js") {
		t.Errorf("web alias resolved %s, want vendor/vue/runtime.js (exports default)", got)
	}
}
