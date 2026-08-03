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
