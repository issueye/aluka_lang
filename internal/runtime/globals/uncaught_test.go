package globals

// 未捕获 JS 回调异常上报（Node 'uncaughtException' 语义）测试：
// setTimeout / process.nextTick / queueMicrotask 回调抛出时，应派发给
// process 的 'uncaughtException' 监听器（此前被静默吞掉）。

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// newUncaughtTestEnv 创建带 process/timers/console 的 VM 上下文。
func newUncaughtTestEnv(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := NewProcess(ctx, ProcessConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := NewTimers(ctx, TimerConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := NewConsole(ctx, ConsoleConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

// uncaughtRun 执行 JS 并驱动事件循环，直到退出或超时。
func uncaughtRun(t *testing.T, ctx engine.Context, code string) error {
	t.Helper()
	if _, err := ctx.Eval(code, "uncaught_test.js"); err != nil {
		return err
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
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("event loop timeout")
	}
}

// TestUncaughtExceptionFromTimer: setTimeout 回调抛出 → 监听器收到错误。
func TestUncaughtExceptionFromTimer(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	if err := uncaughtRun(t, ctx, `
process.on("uncaughtException", (e) => { globalThis.__err = String(e && e.message); });
setTimeout(() => { throw new Error("boom-timer"); }, 5);
`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__err"); got != "boom-timer" {
		t.Errorf("uncaughtException message = %q, want %q", got, "boom-timer")
	}
}

// TestUncaughtExceptionFromNextTick: process.nextTick 回调抛出 → 监听器收到错误。
func TestUncaughtExceptionFromNextTick(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	if err := uncaughtRun(t, ctx, `
process.on("uncaughtException", (e) => { globalThis.__err = String(e && e.message); });
process.nextTick(() => { throw new Error("boom-nexttick"); });
`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__err"); got != "boom-nexttick" {
		t.Errorf("uncaughtException message = %q, want %q", got, "boom-nexttick")
	}
}

// TestUncaughtExceptionFromQueueMicrotask: queueMicrotask 回调抛出 → 监听器收到错误。
func TestUncaughtExceptionFromQueueMicrotask(t *testing.T) {
	ctx := newUncaughtTestEnv(t)
	if err := uncaughtRun(t, ctx, `
process.on("uncaughtException", (e) => { globalThis.__err = String(e && e.message); });
queueMicrotask(() => { throw new Error("boom-microtask"); });
`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__err"); got != "boom-microtask" {
		t.Errorf("uncaughtException message = %q, want %q", got, "boom-microtask")
	}
}

// TestUncaughtExceptionPrintedWithoutListener: 无监听器时错误打印到 stderr
// （修复前被静默吞掉，stderr 无输出）。
func TestUncaughtExceptionPrintedWithoutListener(t *testing.T) {
	// 捕获 stderr。
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	ctx := newUncaughtTestEnv(t)
	runErr := uncaughtRun(t, ctx, `
setTimeout(() => { throw new Error("boom-silent"); }, 5);
`)
	_ = w.Close()
	os.Stderr = origStderr
	buf, _ := io.ReadAll(r)
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if !strings.Contains(string(buf), "boom-silent") {
		t.Errorf("stderr does not contain the uncaught error: %q", string(buf))
	}
}
