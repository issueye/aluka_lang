package builtin

// Phase 3 P1 Node 模块测试：perf_hooks / timers/promises / v8 / module。

import (
	"strings"
	"testing"
)

// TestPerfHooks 验证 performance.now()。
func TestPerfHooks(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { performance } = require('node:perf_hooks');
var a = performance.now();
setTimeout(function(){}, 5);
var b = performance.now();
globalThis.__r = (b >= a) + ':' + (typeof performance.timeOrigin);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:number" {
		t.Errorf("perf_hooks = %q, want true:number", got)
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
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function" {
		t.Errorf("createRequire = %q, want function", got)
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
