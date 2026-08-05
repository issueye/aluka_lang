package module

import "testing"

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
		"main.cjs": `import('./utils.cjs').then(function(m) { globalThis.__r = m.add(2, 3); });`,
		"utils.cjs": `module.exports.add = function(a, b) { return a + b; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "5" {
		t.Errorf("dynamic import CJS: got %q, want 5", got)
	}
}

// TestDynamicImportCJSDefault: CJS 模块整体赋值 module.exports = func 时，
// 动态 import 返回的 namespace 即该函数本身（当前 CJS interop 简化：
// 不额外包装 .default，与 require() 返回值一致）。
func TestDynamicImportCJSDefault(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `import('./mod.cjs').then(function(m) { globalThis.__r = m(10); });`,
		"mod.cjs":  `module.exports = function(x) { return x * 2; };`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "20" {
		t.Errorf("dynamic import default: got %q, want 20", got)
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
		"main.cjs": `import('./data.json').then(function(m) { globalThis.__r = m.name + ':' + m.age; });`,
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
		"main.cjs":     `require('./sub/inner.cjs');`,
		"sub/inner.cjs": `import('../helper.cjs').then(function(m) { globalThis.__r = m.val; });`,
		"helper.cjs":   `module.exports.val = 99;`,
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
		"main.ts":  `import { b } from './dep.ts'; globalThis.__r = b;`,
		"dep.ts":   `export const b: number = 42;`,
	})
	env.run(t, "main.ts")
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("ts relative import: got %q, want 42", got)
	}
}
