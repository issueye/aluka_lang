package main

import "testing"

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
