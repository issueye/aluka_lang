package globals

// setTimeout 返回 Node 风格 Timeout 对象（unref/ref/hasRef/Symbol.toPrimitive）
// 的回归测试。此前 setTimeout 返回 number、无 unref 方法，proper-lockfile 等
// 依赖 `.unref()` 的代码会 TypeError，或使 unref'd 定时器错误地阻止进程退出。

import (
	"fmt"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// timerRun 执行 JS 并驱动事件循环到结束（复用 uncaught_test.go 的上下文构造）。
func timerRun(t *testing.T, ctx engine.Context, code string) {
	t.Helper()
	if _, err := ctx.Eval(code, "timers_test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	done := make(chan struct{})
	go func() {
		if vm, ok := ctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("event loop timeout")
	}
}

// TestSetTimeoutReturnsTimeoutObject: setTimeout 返回带 unref/ref/hasRef 的
// Timeout 对象；clearTimeout 接受对象（经 Symbol.toPrimitive 取 id）。
func TestSetTimeoutReturnsTimeoutObject(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	timerRun(t, ctx, `
var t = setTimeout(function() {}, 1000);
globalThis.__r = [
  typeof t.unref, typeof t.ref, typeof t.hasRef,
  t.hasRef(),
  clearTimeout(t) === undefined
].join(",");
`)
	got, _ := ctx.Global().Get("__r")
	if want := "function,function,function,true,true"; got.String() != want {
		t.Errorf("Timeout object = %q, want %q", got.String(), want)
	}
}

// TestSetTimeoutUnrefExitsEarly: unref'd 定时器不阻止进程退出（Node 语义）。
func TestSetTimeoutUnrefExitsEarly(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	start := time.Now()
	timerRun(t, ctx, `
var t = setTimeout(function() { globalThis.__fired = true; }, 3000);
t.unref();
globalThis.__r = t.hasRef();
`)
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("unref'd timer kept process alive for %v", elapsed)
	}
	got, _ := ctx.Global().Get("__r")
	if got.String() != "false" {
		t.Errorf("hasRef after unref = %q, want false", got.String())
	}
}

// TestSetTimeoutRefKeepsAlive: unref 后 ref 重新持有句柄（进程等待触发）。
func TestSetTimeoutRefKeepsAlive(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	timerRun(t, ctx, `
var t = setTimeout(function() { globalThis.__r = "fired"; }, 30);
t.unref();
t.ref();
`)
	got, _ := ctx.Global().Get("__r")
	if got.String() != "fired" {
		t.Errorf("ref'd timer = %q, want fired", got.String())
	}
}

// TestClearTimeoutObjectForm: clearTimeout 接受 Timeout 对象（Node 语义），
// 已清除的定时器不再触发。
func TestClearTimeoutObjectForm(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	timerRun(t, ctx, `
var log = [];
var t = setTimeout(function() { log.push("fired"); }, 20);
clearTimeout(t);
setTimeout(function() { globalThis.__r = log.join(",") || "none"; }, 40);
`)
	got, _ := ctx.Global().Get("__r")
	if got.String() != "none" {
		t.Errorf("cleared timer = %q, want none", got.String())
	}
}

// TestSetIntervalUnref: interval 同样支持 unref（不阻止进程退出）。
func TestSetIntervalUnref(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	timerRun(t, ctx, `
var i = setInterval(function() { globalThis.__fired = true; }, 30);
i.unref();
globalThis.__r = i.hasRef();
`)
	got, _ := ctx.Global().Get("__r")
	if got.String() != "false" {
		t.Errorf("interval hasRef after unref = %q, want false", got.String())
	}
}

var _ = fmt.Sprintf
