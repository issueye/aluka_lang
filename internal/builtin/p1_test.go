package builtin

// Phase 3 P1 Node 模块测试：perf_hooks / timers/promises / v8 / module。

import (
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
