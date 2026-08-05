package semver

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Version{
		"1.2.3":              {Major: 1, Minor: 2, Patch: 3},
		"v1.2.3":             {Major: 1, Minor: 2, Patch: 3},
		"1.2":                {Major: 1, Minor: 2, Patch: 0},
		"1":                  {Major: 1, Minor: 0, Patch: 0},
		"0.0.1-beta.1+build": {Major: 0, Minor: 0, Patch: 1, Pre: []string{"beta", "1"}, Build: []string{"build"}},
		"1.2.3-rc.1":         {Major: 1, Minor: 2, Patch: 3, Pre: []string{"rc", "1"}},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", in, err)
			continue
		}
		if got.Major != want.Major || got.Minor != want.Minor || got.Patch != want.Patch {
			t.Errorf("Parse(%q) = %v.%v.%v, want %v.%v.%v", in, got.Major, got.Minor, got.Patch, want.Major, want.Minor, want.Patch)
		}
		if len(got.Pre) != len(want.Pre) {
			t.Errorf("Parse(%q) Pre = %v, want %v", in, got.Pre, want.Pre)
		}
	}
	if _, err := Parse(""); err == nil {
		t.Error("Parse empty should error")
	}
	if _, err := Parse("a.b.c"); err == nil {
		t.Error("Parse invalid should error")
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.2.3", "1.2.3-beta", 1}, // 稳定 > prerelease
		{"1.2.3-beta", "1.2.3-alpha", 1},
		{"1.2.3-beta.2", "1.2.3-beta.10", -1}, // 数值比较
		{"1.2.3-beta", "1.2.3-beta.1", -1},    // 短 < 长
		{"1.2.3", "1.2.3+build1", 0},          // build 忽略
	}
	for _, c := range cases {
		av, _ := Parse(c.a)
		bv, _ := Parse(c.b)
		if got := Compare(av, bv); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func mustRange(t *testing.T, s string) Range {
	t.Helper()
	r, err := ParseRange(s)
	if err != nil {
		t.Fatalf("ParseRange(%q): %v", s, err)
	}
	return r
}

func mustVer(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func TestRangeTest(t *testing.T) {
	cases := []struct {
		rangeStr string
		ver      string
		want     bool
	}{
		// 精确。
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{"=1.2.3", "1.2.3", true},
		// 前缀。
		{"^1.2.3", "1.2.3", true},
		{"^1.2.3", "1.9.9", true},
		{"^1.2.3", "2.0.0", false},
		{"^1.2.3", "1.2.3-rc", false}, // prerelease 排除
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{"^0.0.3", "0.0.3", true},
		{"^0.0.3", "0.0.4", false},
		{"^1", "1.5.0", true},
		{"^1", "2.0.0", false},
		{"^1.2", "1.2.9", true},
		{"^1.2", "1.3.0", true}, // ^1.2 → [1.2.0, 2.0.0)
		{"^1.2", "2.0.0", false},
		// 波浪。
		{"~1.2.3", "1.2.3", true},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"~1.2", "1.2.5", true},
		{"~1.2", "1.3.0", false},
		{"~1", "1.9.0", true},
		{"~1", "2.0.0", false},
		// 比较符。
		{">=1.0.0", "1.0.0", true},
		{">=1.0.0", "0.9.9", false},
		{">1.0.0", "1.0.0", false},
		{">1.0.0", "1.0.1", true},
		{"<2.0.0", "1.9.9", true},
		{"<=2.0.0", "2.0.0", true},
		// 通配。
		{"1.2.x", "1.2.5", true},
		{"1.2.x", "1.3.0", false},
		{"1.x", "1.9.9", true},
		{"1.x", "2.0.0", false},
		{"*", "3.4.5", true},
		{"*", "3.4.5-rc", false}, // npm 排除 prerelease
		{"latest", "1.0.0", true},
		// AND / OR。
		{">=1.0.0 <2.0.0", "1.5.0", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
		{"^1.0.0 || ^2.0.0", "1.9.0", true},
		{"^1.0.0 || ^2.0.0", "2.5.0", true},
		{"^1.0.0 || ^2.0.0", "3.0.0", false},
		// 操作符与版本间带空格（npm 常见形式，如 safer-buffer 的 ">= 2.1.2 < 3.0.0"）。
		{">= 2.1.2 < 3.0.0", "2.1.2", true},
		{">= 2.1.2 < 3.0.0", "2.9.9", true},
		{">= 2.1.2 < 3.0.0", "2.1.1", false},
		{">= 2.1.2 < 3.0.0", "3.0.0", false},
		{"~ 1.2.3", "1.2.9", true},
		{"^ 1.2.3", "1.9.0", true},
		{">= 2.1.2 < 3.0.0 || >= 4.0.0", "4.5.0", true},
		{">= 2.1.2 < 3.0.0 || >= 4.0.0", "3.5.0", false},
		// prerelease 范围（gensync 场景）：^1.0.0-beta.2 须包含 1.0.0-beta.2 本身。
		{"^1.0.0-beta.2", "1.0.0-beta.2", true},
		{"^1.0.0-beta.2", "1.0.0-beta.1", false},
		{"^1.0.0-beta.2", "1.0.0", true},
		{"^1.0.0-beta.2", "2.0.0", false},
		{"^1.0.0", "2.0.0-rc.1", false}, // npm：^1.0.0 不匹配 2.0.0-0 及以上
		{"~1.2.3-beta.2", "1.2.3-beta.2", true},
		{"~1.2.3-beta.2", "1.2.5", true},
	}
	for _, c := range cases {
		r := mustRange(t, c.rangeStr)
		v := mustVer(t, c.ver)
		if got := r.Test(v); got != c.want {
			t.Errorf("Range(%q).Test(%q) = %v, want %v", c.rangeStr, c.ver, got, c.want)
		}
	}
}

func TestMaxSatisfying(t *testing.T) {
	vers := []string{"1.0.0", "1.2.0", "1.2.5", "1.3.0", "2.0.0", "2.0.0-beta"}
	cands := make([]Version, 0, len(vers))
	for _, s := range vers {
		cands = append(cands, mustVer(t, s))
	}
	best, ok := MaxSatisfying(cands, mustRange(t, "^1.0.0"))
	if !ok || best.String() != "1.3.0" {
		t.Errorf("^1.0.0 max = %v (ok=%v), want 1.3.0", best, ok)
	}
	best, ok = MaxSatisfying(cands, mustRange(t, "*"))
	if !ok || best.String() != "2.0.0" {
		t.Errorf("* max = %v (ok=%v), want 2.0.0", best, ok)
	}
	best, ok = MaxSatisfying(cands, mustRange(t, "^2.0.0"))
	if !ok || best.String() != "2.0.0" {
		t.Errorf("^2.0.0 max = %v, want 2.0.0 (prerelease excluded)", best)
	}
}
