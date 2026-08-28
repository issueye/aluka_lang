package regex

import (
	"strings"
	"testing"
)

// ExecAll 一次性完成全部匹配。本组测试把 ExecAll 与「逐个 Exec/ExecAt 前进」
// 的参考实现对拍，锁定重写后的语义（零宽推进、捕获组、星体字符、多行）。

// referenceExecAll 用单次匹配循环构造参考结果（语义基准）。
func referenceExecAll(t *testing.T, c *Compiled, s string) [][]int {
	t.Helper()
	var matches [][]int
	search := 0
	length := UTF16Index(s, len(s))
	for search <= length {
		m, err := c.ExecAt(s, search)
		if err != nil {
			t.Fatalf("ExecAt(%d): %v", search, err)
		}
		if m == nil {
			break
		}
		matches = append(matches, append([]int(nil), m...))
		if m[1] == m[0] {
			search = AdvanceStringIndex(s, m[1], c.unicodeMode())
		} else {
			search = m[1]
		}
	}
	return matches
}

// TestExecAllParityWithSingleExec 对拍：ExecAll 必须与逐匹配 ExecAt 一致。
func TestExecAllParityWithSingleExec(t *testing.T) {
	cases := []struct {
		pattern, flags, s string
	}{
		{`\r?\n`, "", "a\r\nb\nc\r\n"},
		{`\r?\n`, "g", "a\r\nb\nc\r\n"},
		{`line`, "g", "line1\nline2\nline3"},
		{`a*`, "g", "baa"},                    // 零宽 + 非零宽混合
		{`(?=)`, "g", "😀a"},                   // 纯零宽（传统模式逐 code unit）
		{`(?=)`, "gu", "😀a"},                  // 纯零宽（u 模式逐码点）
		{`.`, "gu", "😀😀x"},                   // u 模式整体匹配跨代理对
		{`😀`, "g", "a😀b😀"},                    // 非 u 模式匹配星体字面量
		{`(\w+)=((\d+))`, "g", "a=12 bb=345 c=67"},
		{`x|`, "g", "xay"},                    // 空匹配回退分支
		{`^`, "gm", "l1\nl2\nl3"},             // 多行零宽
		{`\bwo`, "g", "hello world 你好 world"}, // 词边界 + 多字节 BMP
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.pattern+"/"+tc.flags, func(t *testing.T) {
			c, err := Compile(tc.pattern, tc.flags)
			if err != nil {
				t.Fatalf("Compile(%q, %q): %v", tc.pattern, tc.flags, err)
			}
			got, err := c.ExecAll(tc.s)
			if err != nil {
				t.Fatalf("ExecAll(%q, %q, %q): %v", tc.pattern, tc.flags, tc.s, err)
			}
			want := referenceExecAll(t, c, tc.s)
			if len(got) != len(want) {
				t.Fatalf("ExecAll(%q, %q, %q) = %v, want %v", tc.pattern, tc.flags, tc.s, got, want)
			}
			for i := range got {
				if !equalIdx(got[i], want[i]) {
					t.Fatalf("ExecAll(%q, %q, %q)[%d] = %v, want %v", tc.pattern, tc.flags, tc.s, i, got[i], want[i])
				}
			}
		})
	}
}

// TestExecAllLargeInputSplitLines 大输入回归：换行切分场景必须保持线性耗时
// （历史实现 O(匹配数 × 串长)，1.8MB/955 行需 19s；修复后应 <1s，实际 ~10ms）。
func TestExecAllLargeInputSplitLines(t *testing.T) {
	line := strings.Repeat("x", 1900) + "\r\n"
	s := strings.Repeat(line, 955) // ≈1.8MB，955 个匹配
	c, err := Compile(`\r?\n`, "")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := c.ExecAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 955 {
		t.Fatalf("matches = %d, want 955", len(matches))
	}
	// 抽样校验匹配位置（每行 1902 code unit：1900 个 x + \r + \n）。
	for _, i := range []int{0, 1, 500, 954} {
		wantStart := 1900 + i*1902
		if !equalIdx(matches[i], []int{wantStart, wantStart + 2}) {
			t.Fatalf("match[%d] = %v, want [%d %d]", i, matches[i], wantStart, wantStart+2)
		}
	}
}

// BenchmarkExecAllLargeInput 供人工验证大输入吞吐：go test -bench ExecAllLargeInput -benchtime 1x
func BenchmarkExecAllLargeInput(b *testing.B) {
	line := strings.Repeat("x", 1900) + "\r\n"
	s := strings.Repeat(line, 955)
	c, err := Compile(`\r?\n`, "")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(s)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.ExecAll(s); err != nil {
			b.Fatal(err)
		}
	}
}
