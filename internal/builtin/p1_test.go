package builtin

// Phase 3 P1 Node 模块测试：perf_hooks / timers/promises / v8 / module。

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// TestPerfHooks 验证 performance.now()。
func TestPerfHooks(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { performance } = require('node:perf_hooks');
var a = performance.now();
setTimeout(function(){}, 5);
var b = performance.now();
globalThis.__r = (b >= a) + ':' + (typeof performance.timeOrigin) + ':' +
  (globalThis.performance === performance) + ':' + (typeof performance.markResourceTiming);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:number:true:function" {
		t.Errorf("perf_hooks = %q, want true:number:true:function", got)
	}
}

// TestTimersPromises 验证 setTimeout 返回 Promise 并 resolve 值。
func TestTimersPromises(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { setTimeout } = require('node:timers/promises');
setTimeout(10, 'resolved').then(function(v) {
  globalThis.__r = v;
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "resolved" {
		t.Errorf("timers/promises = %q, want resolved", got)
	}
}

// TestV8Module 验证 v8.serialize/deserialize 与 getHeapStatistics。
func TestV8Module(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var v8 = require('node:v8');
var buf = v8.serialize({ a: 1, b: 'x' });
var back = v8.deserialize(buf);
globalThis.__r = back.a + ':' + back.b + ':' + Buffer.isBuffer(buf) + ':' +
  (typeof v8.getHeapStatistics().heap_size_limit);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "1:x:true:number" {
		t.Errorf("v8 = %q, want 1:x:true:number", got)
	}
}

// TestModuleCreateRequire 验证 createRequire 返回函数。
func TestModuleCreateRequire(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var mod = require('node:module');
var r2 = mod.createRequire('/tmp/fake/path.js');
globalThis.__r = typeof r2;
globalThis.__resolve = typeof r2.resolve;
globalThis.__resolvePaths = typeof r2.resolve.paths;
globalThis.__builtin = r2.resolve('node:fs');
globalThis.__pathsArray = Array.isArray(r2.resolve.paths('some-package'));
globalThis.__nodeModulePaths = typeof mod.Module._nodeModulePaths;
globalThis.__nodeModulePathsArray = Array.isArray(mod.Module._nodeModulePaths('/tmp/fake'));
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function" {
		t.Errorf("createRequire = %q, want function", got)
	}
	if got := env.globalGet("__resolve"); got != "function" {
		t.Errorf("createRequire.resolve = %q, want function", got)
	}
	if got := env.globalGet("__resolvePaths"); got != "function" {
		t.Errorf("createRequire.resolve.paths = %q, want function", got)
	}
	if got := env.globalGet("__builtin"); got != "node:fs" {
		t.Errorf("createRequire.resolve builtin = %q, want node:fs", got)
	}
	if got := env.globalGet("__pathsArray"); got != "true" {
		t.Errorf("createRequire.resolve.paths result = %q, want true", got)
	}
	if got := env.globalGet("__nodeModulePaths"); got != "function" {
		t.Errorf("Module._nodeModulePaths = %q, want function", got)
	}
	if got := env.globalGet("__nodeModulePathsArray"); got != "true" {
		t.Errorf("Module._nodeModulePaths result = %q, want true", got)
	}
}

// TestModuleCreateRequireFileURL 验证 createRequire(file URL)
// 不会把 file: 当成相对路径拼到 cwd（jiti 回归）。
func TestModuleCreateRequireFileURL(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "dep.cjs")
	if err := os.WriteFile(modPath, []byte(`module.exports = { ok: true };`), 0644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "parent.mjs")
	parentURL := modmodule.PathToFileURLString(parent)
	// 直接传入 file URL 字符串（与 import.meta.url / jiti 一致）。
	script := `
var { createRequire } = require('node:module');
var req = createRequire(` + strconv.Quote(parentURL) + `);
var dep = req('./dep.cjs');
globalThis.__ok = !!(dep && dep.ok === true);
globalThis.__resolved = req.resolve('./dep.cjs');
`
	env := newHTTPEnv(t)
	if err := env.runWithLoop(t, script); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__ok"); got != "true" {
		t.Errorf("createRequire(file URL) = %q, want true", got)
	}
	resolved := env.globalGet("__resolved")
	if filepath.Clean(resolved) != filepath.Clean(modPath) {
		t.Errorf("resolve = %q, want %q", resolved, modPath)
	}
}

// TestBuiltinPathCJSInterop：jiti/Babel 对 node:path 的 default/named 导入。
func TestBuiltinPathCJSInterop(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var path = require('node:path');
function _interopRequireDefault(obj) {
  return obj && obj.__esModule ? obj : { default: obj };
}
var p = _interopRequireDefault(path);
var join = path.join;
globalThis.__r = [
  typeof path.join,
  typeof p.default.join,
  typeof path.default.join,
  String('join' in path),
  path.join('a', 'b').replace(/\\\\/g, '/').endsWith('a/b') || path.join('a', 'b').indexOf('a') >= 0
].join(':');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := env.globalGet("__r")
	if !strings.HasPrefix(got, "function:function:function:true:") {
		t.Errorf("node:path interop = %q, want function:function:function:true:...", got)
	}
}

func TestESMImportNodePathDefaultAndNamed(t *testing.T) {
	env := newHTTPEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.mjs")
	if err := os.WriteFile(main, []byte(`
import path from "node:path";
import { join, dirname } from "node:path";
globalThis.__r = typeof path.join + ':' + typeof join + ':' + typeof dirname;
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := env.loader.Run(main); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function:function:function" {
		t.Errorf("esm import node:path = %q, want function:function:function", got)
	}
}

// TestJitiInteropDefaultProxy：jiti interopDefault 用 Proxy 伪造 __esModule，
// get trap 里 `prop in target` 必须能看到内置模块的 named export。
func TestJitiInteropDefaultProxy(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var path = require('node:path');
function interopDefault(mod) {
  var def = mod.default;
  var defIsNil = def === null || def === undefined;
  return new Proxy(mod, {
    get: function(target, prop) {
      if (prop === '__esModule') return true;
      if (prop === 'default') return defIsNil ? mod : def;
      if (prop in target) return target[prop];
      return undefined;
    }
  });
}
var p = interopDefault(path);
globalThis.__r = [typeof p.join, typeof p.default, typeof p.default.join, String(p.__esModule)].join(':');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function:object:function:true" {
		t.Errorf("jiti Proxy interop = %q, want function:object:function:true", got)
	}
}

// TestRequireFileURLFromCreateRequire：jiti nativeImport 在 Windows 上传 file://。
func TestRequireFileURLFromCreateRequire(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "named.cjs")
	if err := os.WriteFile(modPath, []byte(`exports.foo = function() { return 3; };`), 0644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "parent.mjs")
	modURL := modmodule.PathToFileURLString(modPath)
	script := `
var { createRequire } = require('node:module');
var req = createRequire(` + strconv.Quote(modmodule.PathToFileURLString(parent)) + `);
var dep = req(` + strconv.Quote(modURL) + `);
globalThis.__r = typeof dep.foo + ':' + dep.foo();
`
	env := newHTTPEnv(t)
	if err := env.runWithLoop(t, script); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function:3" {
		t.Errorf("require(file URL) named = %q, want function:3", got)
	}
}

func TestSpawnSyncEncodingReturnsStrings(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var cp = require('node:child_process');
var result = cp.spawnSync('go', ['version'], { encoding: 'utf-8' });
globalThis.__r = typeof result.stdout + ':' + typeof result.stderr + ':' + result.stdout.includes('go version');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "string:string:true" {
		t.Errorf("spawnSync encoding = %q, want string:string:true", got)
	}
}

func TestAsyncResourceSubclassRunInAsyncScope(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { AsyncResource } = require('node:async_hooks');
class Resource extends AsyncResource {
  constructor() { super('ALUKA_TEST'); }
  run(callback) { return this.runInAsyncScope(callback, { value: 40 }, 2); }
}
var resource = new Resource();
globalThis.__r = resource.run(function(n) { return this.value + n; });
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "42" {
		t.Errorf("AsyncResource.runInAsyncScope = %q, want 42", got)
	}
}

func TestConsoleBuiltinUsesGlobalConsole(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `globalThis.__r = require('node:console') === console;`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true" {
		t.Errorf("node:console identity = %q, want true", got)
	}
}

func TestTimersBuiltinSharesGlobalTimers(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var timers = require('node:timers');
var id = timers.setTimeout(function() { globalThis.__fired = true; }, 1);
clearTimeout(id);
globalThis.__r = timers.setTimeout === setTimeout && timers.clearTimeout === clearTimeout;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true" {
		t.Errorf("node:timers identity = %q, want true", got)
	}
	if got := env.globalGet("__fired"); got != "undefined" {
		t.Errorf("cleared module timer fired: %q", got)
	}
}

// TestReadlineRepl 验证模块加载。
func TestReadlineRepl(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var rl = require('node:readline');
var iface = rl.createInterface({});
var replMod = require('node:repl');
globalThis.__r = (typeof iface.question) + ':' + (typeof iface.on) + ':' + (typeof replMod.start);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function:function:function" {
		t.Errorf("readline/repl = %q", got)
	}
}

// TestCryptoSubtleKeys 验证 importKey/generateKey。
func TestCryptoSubtleKeys(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, ['encrypt']).then(function(key) {
  globalThis.__g = key.type + ':' + key.algorithm.name + ':' + key.extractable + ':' + key.usages.length;
  return crypto.subtle.importKey('raw', Buffer.from('0123456789abcdef'), 'AES-GCM', false, ['decrypt']);
}).then(function(k2) {
  globalThis.__i = k2.type + ':' + k2.algorithm.name;
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__g"); got != "secret:AES-GCM:true:1" {
		t.Errorf("generateKey = %q", got)
	}
	if got := env.globalGet("__i"); got != "secret:AES-GCM" {
		t.Errorf("importKey = %q", got)
	}
}

// TestFSPromises 验证 fs/promises 异步读写。
func TestFSPromises(t *testing.T) {
	env := newHTTPEnv(t)
	dir := strings.ReplaceAll(t.TempDir(), "\\", "/")
	filePath := dir + "/data.txt"
	err := env.runWithLoop(t, `
var fsp = require('node:fs/promises');
(async function() {
  await fsp.writeFile('`+filePath+`', 'async data');
  var data = await fsp.readFile('`+filePath+`', 'utf8');
  globalThis.__r = data;
  var list = await fsp.readdir('`+dir+`');
  globalThis.__n = list.length;
  await fsp.unlink('`+filePath+`');
})();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "async data" {
		t.Errorf("fs/promises = %q, want async data", got)
	}
	if got := env.globalGet("__n"); got != "1" {
		t.Errorf("readdir = %q, want 1", got)
	}
}

// TestFSRealpathSyncNative 验证 Node 的 realpathSync.native 函数别名。
func TestFSRealpathSyncNative(t *testing.T) {
	env := newHTTPEnv(t)
	dir := strings.ReplaceAll(t.TempDir(), "\\", "/")
	err := env.runWithLoop(t, `
var fs = require('node:fs');
globalThis.__r = typeof fs.realpathSync.native + ':' + (fs.realpathSync.native === fs.realpathSync) + ':' + fs.realpathSync.native('`+dir+`');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "function:true:" + dir
	if got := strings.ReplaceAll(env.globalGet("__r"), "\\", "/"); got != want {
		t.Errorf("realpathSync.native = %q, want %q", got, want)
	}
}

// TestTimersPromisesInterval 验证 setInterval 异步迭代器。
func TestTimersPromisesInterval(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { setInterval } = require('node:timers/promises');
(async function() {
  var count = 0;
  for await (var v of setInterval(5, 'tick')) {
    count++;
    if (count >= 3) break;
  }
  globalThis.__r = count;
})();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "3" {
		t.Errorf("setInterval iterator = %q, want 3", got)
	}
}
