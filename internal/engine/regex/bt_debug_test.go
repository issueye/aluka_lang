package regex

import "testing"

func TestBtDebug(t *testing.T) {
	cases := []struct {
		pat, inp string
		want     bool
	}{
		{`\B`, ",", false},
		{`\B`, "23", true},
		{`\B`, "1,234,567", true},
		{`\B(?=cd)`, "abcd", true},
		{`(?=cd)`, "abcd", true},
		{`\B(?=(\d{3})+(?!\d))`, "1,234,567", true},
		{`\B(?=(\d{3})+(?!\d))`, "345,567", true},
		{`\B(?=(111))`, "a111", true},
		{`\B(?=(234))`, "12345", true},
		{`\B(?=(\d{3}))`, "12345", true},
		{`\B(?=(\d{3})+(?!\d))`, "12345", true},
		{`\B(?=(\d{3})+)`, "12345", true},
		{`\B(?=(\d{3}))`, "1,234,567", true},
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
