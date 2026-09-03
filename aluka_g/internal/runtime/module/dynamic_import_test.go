package module

import "testing"

import (
	"path/filepath"
	"strings"
)

// 本文件覆盖 ES2020 动态 import()（1D.13）。
// 风格对齐 module_test.go：newTestEnv 写临时文件 → loader.Run → globalGet 验证。
//
// 动态 import 由两层实现：
//   - parser 把 import(spec) lower 成对内置全局 __import(spec) 的调用
//   - Loader.makeImportFunc 复用 require() 同步加载，用 Promise.resolve/reject 包装结果
//
// 测试覆盖：CJS 模块、ESM 模块、JSON 模块、命名/默认导出访问、await 解包、
// 错误处理（rejected promise）、相对路径解析。

// TestDynamicImportCJS: 动态加载 CJS 模块，访问其命名导出。
func TestDynamicImportCJS(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs":  `import('./utils.cjs').then(function(m) { globalThis.__r = m.add(2, 3); });`,
		"utils.cjs": `module.exports.add = function(a, b) { return a + b; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "5" {
		t.Errorf("dynamic import CJS: got %q, want 5", got)
	}
}

// TestDynamicImportCJSDefault: CJS 模块整体赋值 module.exports = func 时，
// 动态 import 返回命名空间 { default: func }（Node 语义：m.default(10) 可调用，
// m 本身不可调用）。
func TestDynamicImportCJSDefault(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `import('./mod.cjs').then(function(m) { globalThis.__r = m.default(10) + ':' + (typeof m); });`,
		"mod.cjs":  `module.exports = function(x) { return x * 2; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "20:object" {
		t.Errorf("dynamic import default: got %q, want 20:object", got)
	}
}

// TestDynamicImportESM: 动态加载 ESM 模块，访问命名导出与默认导出。
func TestDynamicImportESM(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
import('./mod.mjs').then(function(m) {
  globalThis.__named = m.square(4);
  globalThis.__def = m.default;
  globalThis.__ns = typeof m.PI;
});
`,
		"mod.mjs": `
export function square(x) { return x * x; }
export const PI = 3.14;
export default "hello";
`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__named"); got != "16" {
		t.Errorf("dynamic import ESM named: got %q, want 16", got)
	}
	if got := env.globalGet("__def"); got != "hello" {
		t.Errorf("dynamic import ESM default: got %q, want hello", got)
	}
	if got := env.globalGet("__ns"); got != "number" {
		t.Errorf("dynamic import ESM namespace: got %q, want number", got)
	}
}

// TestDynamicImportJSON: 动态加载 JSON 模块。
func TestDynamicImportJSON(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs":  `import('./data.json').then(function(m) { globalThis.__r = m.name + ':' + m.age; });`,
		"data.json": `{"name":"alice","age":30}`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "alice:30" {
		t.Errorf("dynamic import JSON: got %q, want alice:30", got)
	}
}

// TestDynamicImportAwait: 在 async 函数内用 await 解包动态 import。
func TestDynamicImportAwait(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `
async function main() {
  var m = await import('./mod.cjs');
  return m.dbl(21);
}
main().then(function(v) { globalThis.__r = v; });
`,
		"mod.cjs": `module.exports.dbl = function(x) { return x * 2; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("dynamic import await: got %q, want 42", got)
	}
}

// TestDynamicImportReturnsPromise: import() 返回值是 Promise 实例。
func TestDynamicImportReturnsPromise(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `
var p = import('./mod.cjs');
globalThis.__isPromise = p instanceof Promise;
p.then(function() { globalThis.__settled = "yes"; });
`,
		"mod.cjs": `module.exports = 1;`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__isPromise"); got != "true" {
		t.Errorf("import() instanceof Promise: got %q, want true", got)
	}
	if got := env.globalGet("__settled"); got != "yes" {
		t.Errorf("import() settled: got %q, want yes", got)
	}
}

// TestDynamicImportError: 加载不存在的模块时返回 rejected promise，
// 经 .catch 捕获而非同步抛出。
func TestDynamicImportError(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `
import('./nonexistent.cjs').then(function() {
  globalThis.__r = "resolved";
}, function(e) {
  globalThis.__r = "rejected:" + (e !== undefined);
});
`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "rejected:true" {
		t.Errorf("dynamic import error: got %q, want rejected:true", got)
	}
}

// TestDynamicImportRelativePath: 从子目录模块发起动态 import，相对路径
// 基于发起模块自身路径解析。
func TestDynamicImportRelativePath(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs":      `require('./sub/inner.cjs');`,
		"sub/inner.cjs": `import('../helper.cjs').then(function(m) { globalThis.__r = m.val; });`,
		"helper.cjs":    `module.exports.val = 99;`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "99" {
		t.Errorf("dynamic import relative path: got %q, want 99", got)
	}
}

// TestStaticImportAttributes: 静态 import ... with { type: 'json' }。
func TestStaticImportAttributes(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
import data from './data.json' with { type: 'json' };
globalThis.__r = data.hello + ':' + data.n;
`,
		"data.json": `{"hello":"json-attrs","n":7}`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__r"); got != "json-attrs:7" {
		t.Errorf("static import attributes: got %q, want json-attrs:7", got)
	}
}

// TestDynamicImportAttributesJSON: 动态 import(x, { with: { type: 'json' } })
// 解析为命名空间 { default: <json> }（Node 语义）。
func TestDynamicImportAttributesJSON(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
import('./data.json', { with: { type: 'json' } }).then(function(m) {
  globalThis.__r = m.default.hello + ':' + m.default.n;
});
`,
		"data.json": `{"hello":"json-attrs","n":7}`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__r"); got != "json-attrs:7" {
		t.Errorf("dynamic import attributes: got %q, want json-attrs:7", got)
	}
}

// TestDynamicImportAttributesBadType: 不支持的 attribute type 拒绝。
func TestDynamicImportAttributesBadType(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
import('./data.json', { with: { type: 'css' } }).then(function() {
  globalThis.__r = "resolved";
}, function(e) {
  globalThis.__r = "rejected";
});
`,
		"data.json": `{"x":1}`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__r"); got != "rejected" {
		t.Errorf("bad attribute type: got %q, want rejected", got)
	}
}

// TestModuleSyntaxDetection: typeless .js 含 ESM 语法时按 ESM 重新加载
// （Node 22 module-syntax detection）。
func TestModuleSyntaxDetection(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.js": `
import { d } from './dep.js';
globalThis.__r = d;
`,
		"dep.js": `export const d = "esm-detected";`,
	})
	env.run(t, "main.js")
	if got := env.globalGet("__r"); got != "esm-detected" {
		t.Errorf("module syntax detection: got %q, want esm-detected", got)
	}
}

// TestTypeScriptRelativeImport: .ts 扩展名相对导入。
func TestTypeScriptRelativeImport(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.ts": `import { b } from './dep.ts'; globalThis.__r = b;`,
		"dep.ts":  `export const b: number = 42;`,
	})
	env.run(t, "main.ts")
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("ts relative import: got %q, want 42", got)
	}
}

// TestTopLevelAwait: ESM 模块顶层 await（TLA）按顺序执行。
func TestTopLevelAwait(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
const a = await Promise.resolve(10);
const b = await Promise.resolve(20);
globalThis.__r = a + b;
`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__r"); got != "30" {
		t.Errorf("TLA: got %q, want 30", got)
	}
}

func TestImportMetaUsesCurrentModule(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `
import { childURL } from './child.mjs';
globalThis.__mainURL = import.meta.url;
globalThis.__childURL = childURL;
`,
		"child.mjs": `export const childURL = import.meta.url;`,
	})
	env.run(t, "main.mjs")

	mainURL := filepath.ToSlash(filepath.Join(env.dir, "main.mjs"))
	childURL := filepath.ToSlash(filepath.Join(env.dir, "child.mjs"))
	if got := filepath.ToSlash(env.globalGet("__mainURL")); !strings.HasSuffix(got, mainURL) {
		t.Errorf("main import.meta.url = %q, want suffix %q", got, mainURL)
	}
	if got := filepath.ToSlash(env.globalGet("__childURL")); !strings.HasSuffix(got, childURL) {
		t.Errorf("child import.meta.url = %q, want suffix %q", got, childURL)
	}
}

// TestTopLevelAwaitError: TLA 中未捕获拒绝 → 模块加载失败。
func TestTopLevelAwaitReject(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `await Promise.reject(new Error("tla-boom"));`,
	})
	if err := env.loader.Run(env.dir + "/main.mjs"); err == nil {
		t.Error("TLA rejection should fail module load")
	}
}

// TestDestructuringParams: 箭头/函数解构参数（({a}) => / ([x]) => / (a, {b}) =>）。
func TestDestructuringParams(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.js": `
const f = ({ x }) => x;
const g = ([a, b]) => a + b;
const h = (first, { second }) => first + second;
const k = ({ a, b } = { a: 1, b: 2 }) => a * 10 + b;
globalThis.__r = f({ x: 5 }) + ":" + g([1, 2]) + ":" + h("A", { second: "B" }) + ":" + k();
`,
	})
	env.run(t, "main.js")
	if got := env.globalGet("__r"); got != "5:3:AB:12" {
		t.Errorf("destructuring params: got %q, want 5:3:AB:12", got)
	}
}

// TestArrowReturnTypeAnnotation: 箭头函数返回类型注解 (: T =>)。
func TestArrowReturnTypeAnnotation(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.ts": `
const f = (x: number): string => String(x * 2);
globalThis.__r = f(21);
`,
	})
	env.run(t, "main.ts")
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("arrow return type: got %q, want 42", got)
	}
}

// TestExportTypeErase: export type X = ... / export interface 擦除。
func TestExportTypeErase(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.ts": `
export type Alias = { a: number };
export interface Detail { b: string }
export const value: number = 7;
globalThis.__r = value;
`,
	})
	env.run(t, "main.ts")
	if got := env.globalGet("__r"); got != "7" {
		t.Errorf("export type erase: got %q, want 7", got)
	}
}

// TestTrailingCommaParams: 多行参数尾部逗号（函数/箭头/import/export 列表）。
func TestTrailingCommaParams(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.js": `
function f(
  a,
  b,
) {
  return a + b;
}
const g = (
  x,
  y,
) => x * y;
globalThis.__r = f(1, 2) + ":" + g(3, 4);
`,
	})
	env.run(t, "main.js")
	if got := env.globalGet("__r"); got != "3:12" {
		t.Errorf("trailing comma params: got %q, want 3:12", got)
	}
}
