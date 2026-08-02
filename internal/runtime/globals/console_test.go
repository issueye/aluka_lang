package globals

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// newTestContext 创建带 console 的测试上下文。
func newTestContext(t *testing.T, cfg ConsoleConfig) (engine.Context, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	eng := engine.NewStubEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	if cfg.Stdout == nil {
		cfg.Stdout = out
	}
	if cfg.Stderr == nil {
		cfg.Stderr = errOut
	}
	if cfg.Now == nil {
		// 固定时间，便于 timeEnd 断言
		fixed := time.Unix(0, 0)
		cfg.Now = func() time.Time { return fixed }
	}
	if err := NewConsole(ctx, cfg); err != nil {
		t.Fatalf("NewConsole: %v", err)
	}
	return ctx, out, errOut
}

// TestConsoleLog 验证 log 输出。
func TestConsoleLog(t *testing.T) {
	ctx, out, _ := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, err := ctx.Eval(`console.log("hello", "world")`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello world") {
		t.Errorf("got %q, want contains 'hello world'", got)
	}
}

// TestConsoleLogNumber 验证数字输出（如 1+1=2）。
func TestConsoleLogNumber(t *testing.T) {
	ctx, out, _ := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, err := ctx.Eval(`console.log(1 + 1)`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if strings.TrimSpace(out.String()) != "2" {
		t.Errorf("got %q, want '2'", out.String())
	}
}

// TestConsoleError 验证 error 输出到 stderr。
func TestConsoleError(t *testing.T) {
	ctx, _, errOut := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, err := ctx.Eval(`console.error("oops")`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(errOut.String(), "oops") {
		t.Errorf("stderr got %q", errOut.String())
	}
}

// TestConsoleWarn 验证 warn 输出到 stderr。
func TestConsoleWarn(t *testing.T) {
	ctx, _, errOut := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.warn("caution")`, "test.js")
	if !strings.Contains(errOut.String(), "caution") {
		t.Errorf("stderr got %q", errOut.String())
	}
}

// TestConsoleInfo 验证 info 输出到 stdout。
func TestConsoleInfo(t *testing.T) {
	ctx, out, _ := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.info("info")`, "test.js")
	if !strings.Contains(out.String(), "info") {
		t.Errorf("stdout got %q", out.String())
	}
}

// TestConsoleAssert 验证 assert 行为：条件为 false 时打印。
func TestConsoleAssert(t *testing.T) {
	ctx, _, errOut := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.assert(1 === 2, "should fail")`, "test.js")
	if !strings.Contains(errOut.String(), "Assertion failed") {
		t.Errorf("stderr got %q", errOut.String())
	}

	// 条件为 true 不输出
	_, _ = ctx.Eval(`console.assert(1 === 1, "should not print")`, "test.js")
}

// TestConsoleTime 验证 time/timeEnd。
func TestConsoleTime(t *testing.T) {
	baseTime := time.Unix(0, 0)
	ctx, out, _ := newTestContext(t, ConsoleConfig{
		Now: func() time.Time {
			// 每次 Now 调用都前进 1ms
			baseTime = baseTime.Add(time.Millisecond)
			return baseTime
		},
	})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.time("t1"); console.timeEnd("t1")`, "test.js")
	got := out.String()
	if !strings.Contains(got, "t1:") {
		t.Errorf("got %q", got)
	}
}

// TestConsoleDirAndDebug 验证 dir/debug 与 log 行为一致。
func TestConsoleDirAndDebug(t *testing.T) {
	ctx, out, _ := newTestContext(t, ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.dir("x"); console.debug("y")`, "test.js")
	got := out.String()
	if !strings.Contains(got, "x") || !strings.Contains(got, "y") {
		t.Errorf("got %q", got)
	}
}
