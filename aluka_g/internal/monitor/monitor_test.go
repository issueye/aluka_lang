package monitor

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// TestSnapshot 验证快照字段与周期增量。
func TestSnapshot(t *testing.T) {
	base := engine.MetricsCounters.Insns.Load()
	m := New(Config{Enabled: true, Out: &bytes.Buffer{}})
	engine.BumpInsns()
	engine.BumpInsns()
	engine.BumpCalls()

	rep := m.Snapshot()
	if rep.Insns < base+2 {
		t.Errorf("Insns = %d, want >= %d", rep.Insns, base+2)
	}
	if rep.Calls < 1 {
		t.Errorf("Calls = %d, want >= 1", rep.Calls)
	}
	if rep.Goroutines < 1 {
		t.Errorf("Goroutines = %d", rep.Goroutines)
	}
	if rep.Elapsed < 0 {
		t.Errorf("Elapsed = %v", rep.Elapsed)
	}
	// 终报为累计值。
	m.Stop()
	rep2 := m.Snapshot()
	if rep2.Insns < rep.Insns {
		t.Errorf("final Insns regressed: %d -> %d", rep.Insns, rep2.Insns)
	}
	// 清理（EnableMetrics 是全局的，避免影响其他测试）。
	engine.DisableMetricsForTest()
}

// TestTextAndJSONFormat 验证 text/json 输出内容。
func TestTextAndJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	m := New(Config{Enabled: true, Format: FormatText, Out: &buf})
	m.Print(m.Snapshot())
	out := buf.String()
	if !strings.Contains(out, "elapsed") || !strings.Contains(out, "heap") {
		t.Errorf("text output missing sections:\n%s", out)
	}
	if !strings.Contains(out, "goroutines") {
		t.Errorf("text output missing goroutines:\n%s", out)
	}

	buf.Reset()
	m2 := New(Config{Enabled: true, Format: FormatJSON, Out: &buf})
	m2.Print(m2.Snapshot())
	j := buf.String()
	for _, key := range []string{`"insns"`, `"heap_alloc"`, `"goroutines"`, `"elapsed_ms"`, `"gc_count"`} {
		if !strings.Contains(j, key) {
			t.Errorf("json output missing %s:\n%s", key, j)
		}
	}
	// 单位换算正确性。
	if formatBytes(1024) != "1.0 KB" {
		t.Errorf("formatBytes(1024) = %q", formatBytes(1024))
	}
	if formatBytes(1536) != "1.5 KB" {
		t.Errorf("formatBytes(1536) = %q", formatBytes(1536))
	}
	engine.DisableMetricsForTest()
}

// TestMonitorInterval 验证周期采样模式（Interval > 0 时增量语义）。
func TestMonitorInterval(t *testing.T) {
	base := engine.MetricsCounters.Insns.Load()
	m := New(Config{Enabled: true, Interval: time.Hour, Out: &bytes.Buffer{}})
	engine.BumpInsns()
	rep := m.Snapshot()
	if rep.Insns != base+1 {
		t.Errorf("periodic snapshot Insns = %d, want %d", rep.Insns, base+1)
	}
	engine.BumpInsns()
	rep2 := m.Snapshot()
	if rep2.Insns != 1 {
		t.Errorf("periodic delta Insns = %d, want 1 (delta only)", rep2.Insns)
	}
	engine.DisableMetricsForTest()
}
