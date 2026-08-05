package regex

import (
	"strings"
	"testing"
)

// TestTranslate 验证 JS→Go 语法翻译规则。
func TestTranslate(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		flags   string
		want    string // 翻译后的 Go 正则
		wantErr bool
	}{
		{"plain", "abc", "", "abc", false},
		{"escaped slash", `a\/b`, "", "a/b", false},
		{"escaped plus", `a\+b`, "", `a\+b`, false},
		{"escaped star", `x\*`, "", `x\*`, false},
		{"escaped dot", `a\.b`, "", `a\.b`, false},
		{"escaped hyphen outside class", `a\-b`, "", "a-b", false},
		{"escaped hyphen in class", `[\-]`, "", `[\-]`, false},
		{"escaped caret in class", `[\^]`, "", `[\^]`, false},
		{"escaped backslash in class", `[\\]`, "", `[\\]`, false},
		{"char class", "[a-z0-9_]+", "", "[a-z0-9_]+", false},
		{"class first-bracket literal", "[]a]", "", `[\]a]`, false},
		{"empty class never matches", "[]", "", `[^\s\S]`, false},
		{"negated empty class", "[^]", "", `[\s\S]`, false},
		{"dot no-dotall", "a.b", "", `a[^\n\r\x{2028}\x{2029}]b`, false},
		{"dot with s", "a.b", "s", `a[\s\S]b`, false},
		{"s escape", `\s+`, "", "[" + jsWhiteSpaceClass + "]+", false},
		{"S escape", `\S+`, "", "[^" + jsWhiteSpaceClass + "]+", false},
		{"named group", "(?<year>\\d{4})", "", `(?P<year>\d{4})`, false},
		{"non-capturing", "(?:ab)+", "", "(?:ab)+", false},
		{"unicode code point", `\u{1F600}`, "u", `\x{1F600}`, false},
		{"unicode escape", `\u0041`, "", `\x{0041}`, false},
		{"property escape u", `\p{L}+`, "u", `\p{L}+`, false},
		{"property escape non-u is literal", `\p{L}`, "", "p{L}", false},
		{"nul", `\0`, "", `\x00`, false},
		{"control escape", `\cA`, "", `\x01`, false},
		{"word boundary", `\bword\b`, "", `\bword\b`, false},
		{"backref rejected", `(a)\1`, "", "", true},
		{"named backref rejected", `(?<x>a)\k<x>`, "", "", true},
		{"lookahead rejected", `a(?=b)`, "", "", true},
		{"neg lookahead rejected", `a(?!b)`, "", "", true},
		{"lookbehind rejected", `(?<=a)b`, "", "", true},
		{"neg lookbehind rejected", `(?<!a)b`, "", "", true},
		{"trailing backslash", `a\`, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := ParseFlags(c.flags)
			if err != nil {
				t.Fatalf("ParseFlags(%q): %v", c.flags, err)
			}
			got, err := translate(c.pattern, f)
			if c.wantErr {
				if err == nil {
					t.Fatalf("translate(%q) = %q, want error", c.pattern, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("translate(%q): %v", c.pattern, err)
			}
			if got != c.want {
				t.Errorf("translate(%q) = %q, want %q", c.pattern, got, c.want)
			}
		})
	}
}

// TestParseFlags 验证标志解析与校验。
func TestParseFlags(t *testing.T) {
	cases := []struct {
		flags   string
		want    string // Flags.String() 输出
		wantErr bool
	}{
		{"", "", false},
		{"gim", "gim", false},
		{"img", "gim", false}, // 顺序无关
		{"dgimsy", "dgimsy", false},
		{"gg", "", true}, // 重复
		{"x", "", true},  // 非法
		{"uv", "", true}, // u 与 v 互斥
	}
	for _, c := range cases {
		f, err := ParseFlags(c.flags)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseFlags(%q): want error", c.flags)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseFlags(%q): %v", c.flags, err)
			continue
		}
		if got := f.String(); got != c.want {
			t.Errorf("ParseFlags(%q).String() = %q, want %q", c.flags, got, c.want)
		}
	}
}

// TestCompileMatch 验证编译后匹配行为。
func TestCompileMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		flags   string
		input   string
		match   bool
		full    string // 首个整体匹配
	}{
		{"basic", "ab+c", "", "abbbc", true, "abbbc"},
		{"no match", "ab+c", "", "ac", false, ""},
		{"ignore case", "hello", "i", "HELLO", true, "HELLO"},
		{"multiline", "^b", "m", "a\nb", true, "b"},
		{"dotall", "a.b", "s", "a\nb", true, "a\nb"},
		{"dot no dotall", "a.b", "", "a\nb", false, ""},
		{"named group", "(?<y>\\d{4})-(?<m>\\d{2})", "", "2026-08", true, "2026-08"},
		{"escaped slash", `a\/b`, "", "a/b", true, "a/b"},
		{"whitespace", `\s+`, "", "a \t\nb", true, " \t\n"},
		{"unicode property", `\p{L}+`, "u", "héllo", true, "héllo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compiled, err := Compile(c.pattern, c.flags)
			if err != nil {
				t.Fatalf("Compile(%q, %q): %v", c.pattern, c.flags, err)
			}
			m := compiled.MatchIndex(c.input)
			if c.match != (m != nil) {
				t.Errorf("MatchIndex(%q) match=%v, want %v", c.input, m != nil, c.match)
				return
			}
			if c.match && !strings.HasPrefix(c.input[m[0]:m[1]], c.full) {
				t.Errorf("MatchIndex(%q) full=%q, want %q", c.input, c.input[m[0]:m[1]], c.full)
			}
		})
	}
}

// TestCompileErrors 验证非法模式编译报错。
func TestCompileErrors(t *testing.T) {
	bad := []struct {
		pattern string
		flags   string
	}{
		{"(", ""},
		{"[a-z", ""},
		{"a)", ""},
		{"a{2,1}", ""},
		{"a", "x"},
		{"a", "ii"},
	}
	for _, b := range bad {
		if _, err := Compile(b.pattern, b.flags); err == nil {
			t.Errorf("Compile(%q, %q): want error", b.pattern, b.flags)
		}
	}
}

// TestBacktrackLookahead 验证回退引擎对前瞻/反向引用的支持。
func TestBacktrackLookahead(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		flags   string
		input   string
		match   bool
	}{
		{"simple lookahead", "a(?=b)", "", "ab", true},
		{"lookahead negative", "a(?!b)", "", "ac", true},
		{"neg lookahead fail", "a(?!b)", "", "ab", false},
		{"lookbehind", "(?<=a)b", "", "ab", true},
		{"neg lookbehind fail", "(?<!a)b", "", "ab", false},
		{"backref", "(a)\\1", "", "aa", true},
		{"backref fail", "(a)\\1", "", "ab", false},
		{"bytes thousands", "\\B(?=(\\d{3})+(?!\\d))", "g", "1,234,567", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compiled, err := Compile(c.pattern, c.flags)
			if err != nil {
				t.Fatalf("Compile(%q, %q): %v", c.pattern, c.flags, err)
			}
			m := compiled.MatchIndex(c.input)
			if c.match != (m != nil) {
				t.Errorf("MatchIndex(%q) match=%v, want %v", c.input, m != nil, c.match)
			}
		})
	}
}

// TestGroupNames 验证命名捕获组名提取。
func TestGroupNames(t *testing.T) {
	compiled, err := Compile("(?<y>\\d{4})-(?:\\d{2})-(?<d>\\d{2})", "")
	if err != nil {
		t.Fatal(err)
	}
	if compiled.NumGroups() != 2 {
		t.Errorf("NumGroups = %d, want 2", compiled.NumGroups())
	}
	if got := compiled.GroupName(1); got != "y" {
		t.Errorf("GroupName(1) = %q, want y", got)
	}
	if got := compiled.GroupName(2); got != "d" {
		t.Errorf("GroupName(2) = %q, want d", got)
	}
	if got := compiled.GroupName(3); got != "" {
		t.Errorf("GroupName(3) = %q, want empty", got)
	}
}
