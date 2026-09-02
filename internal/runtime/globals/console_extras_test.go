package globals

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/runtime/globals/gconsole"
)

// TestConsoleCount 验证 count/countReset。
func TestConsoleCount(t *testing.T) {
	ctx, out, _ := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.count("a"); console.count("a"); console.countReset("a"); console.count("a")`, "test.js")
	got := out.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("count lines = %d, want 3 (%q)", len(lines), got)
	}
	if !strings.Contains(lines[0], "a: 1") || !strings.Contains(lines[1], "a: 2") || !strings.Contains(lines[2], "a: 1") {
		t.Errorf("count output = %q", got)
	}
}

// TestConsoleGroupIndent 验证 group 缩进。
func TestConsoleGroupIndent(t *testing.T) {
	ctx, out, _ := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.group("g"); console.log("inner"); console.groupEnd(); console.log("outer")`, "test.js")
	got := out.String()
	if !strings.Contains(got, "  inner") {
		t.Errorf("inner should be indented: %q", got)
	}
	if strings.Contains(got, "  outer") {
		t.Errorf("outer should not be indented: %q", got)
	}
}

// TestConsoleTable 验证 table 输出。
func TestConsoleTable(t *testing.T) {
	ctx, out, _ := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.table([1, 2])`, "test.js")
	if !strings.Contains(out.String(), "1") || !strings.Contains(out.String(), "2") {
		t.Errorf("table output = %q", out.String())
	}
}

// TestConsoleTrace 验证 trace 输出到 stderr。
func TestConsoleTrace(t *testing.T) {
	ctx, _, errOut := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	_, _ = ctx.Eval(`console.trace("here")`, "test.js")
	if !strings.Contains(errOut.String(), "Trace: here") {
		t.Errorf("trace output = %q", errOut.String())
	}
}
