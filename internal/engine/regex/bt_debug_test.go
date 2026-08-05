package regex

import "testing"

func TestBtDebug(t *testing.T) {
	// 期望值以 V8 实测为准（node 22，String.raw 构造 pattern 防转义污染）：
	//   \B on "," → search=0（全串无词边界，\B 在任意位置匹配）
	//   \B(?=(\d{3})+(?!\d)) on "1,234,567"/"345,567" → -1（(\d{3}) 跨不过逗号）
	//   \B(?=(\d{3})) on "1,234,567" → -1（同上）
	cases := []struct {
		pat, inp string
		want     bool
	}{
		{`\B`, ",", true},
		{`\B`, "23", true},
		{`\B`, "1,234,567", true},
		{`\B(?=cd)`, "abcd", true},
		{`(?=cd)`, "abcd", true},
		{`\B(?=(\d{3})+(?!\d))`, "1,234,567", false},
		{`\B(?=(\d{3})+(?!\d))`, "345,567", false},
		{`\B(?=(111))`, "a111", true},
		{`\B(?=(234))`, "12345", true},
		{`\B(?=(\d{3}))`, "12345", true},
		{`\B(?=(\d{3})+(?!\d))`, "12345", true},
		{`\B(?=(\d{3})+)`, "12345", true},
		{`\B(?=(\d{3}))`, "1,234,567", false},
	}
	for _, c := range cases {
		compiled, err := Compile(c.pat, "g")
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		m := compiled.MatchIndex(c.inp)
		got := m != nil
		if got != c.want {
			t.Errorf("MatchIndex(%q) on %q = %v (m=%v), want %v", c.pat, c.inp, got, m, c.want)
		}
	}
}
