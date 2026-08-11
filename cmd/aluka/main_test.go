package main

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// TestParseMemorySize 验证 --max-memory 大小解析（bytes/KB/MB/GB）。
func TestParseMemorySize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"100", 100, true},
		{"1024", 1024, true},
		{"1KB", 1024, true},
		{"1kb", 1024, true},
		{"2MB", 2 << 20, true},
		{"256MB", 256 << 20, true},
		{"1GB", 1 << 30, true},
		{"512B", 512, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"1.5MB", 0, false}, // 不支持小数
	}
	for _, c := range cases {
		got, err := parseMemorySize(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("parseMemorySize(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("parseMemorySize(%q) = %d, nil; want error", c.in, got)
		}
	}
}

func TestFormatJITStatsIncludesGuardDisableCounters(t *testing.T) {
	text := formatJITStatsSummary(jit.Stats{
		Mode: jit.Auto, QuickGuardDisabled: 1, TraceGuardDisabled: 2,
		NativeGuardDisabled: 3, NativeTraceGuardDisabled: 4, CalleeGuardDisabled: 5,
	})
	for _, want := range []string{
		"quickGuardDisabled=1", "traceGuardDisabled=2", "nativeGuardDisabled=3",
		"nativeTraceGuardDisabled=4", "calleeGuardDisabled=5",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stats output missing %q: %s", want, text)
		}
	}
}

// TestFormatJITStatsIncludesR5Aggregates verifies the R5-7 derived rates
// (guard/deopt/eviction) and aggregate counters are printed by --jit-stats.
func TestFormatJITStatsIncludesR5Aggregates(t *testing.T) {
	text := formatJITStatsSummary(jit.Stats{
		Mode: jit.Auto, Compiled: 5, CompileNanos: 1000, Executed: 100,
		GuardFailures: 10, Deopts: 2, NativeCompiled: 5, NativeEvictions: 1,
		CompileBenefit: 20, Executions: 100,
	})
	for _, want := range []string{
		"executions=100", "deopts=2", "compileBenefit=20", "hotEvictions=0",
		"guardRate=9.09%", "deoptRate=2.00%", "evictionRate=20.00%",
		"compileCostPerSiteNanos=200",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stats output missing %q: %s", want, text)
		}
	}
}
