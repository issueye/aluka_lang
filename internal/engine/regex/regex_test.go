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
		{"backspace escape in class", `[\b]`, "", `[\x08]`, false},
		{"identity B escape in class", `[\B]`, "", `[B]`, false},
		{"babel escape class", `[\\\b\f\n\r\t]`, "", `[\\\x08\f\n\r\t]`, false},
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
		// >0xFFFF 的码点嵌入 UTF-8 字面量（Go 的 \x{...} 仅支持 4 位十六进制，
		// \x{1F600} 会被 RE2 截断解析）。
		{"unicode code point", `\u{1F600}`, "u", `😀`, false},
		{"unicode escape", `\u0041`, "", `\x{41}`, false},
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
		{"alphabetic property", `[\p{Alphabetic}\p{N}]+`, "u", "é7", true, "é7"},
		{"alphabetic property excludes punctuation", `[\p{Alphabetic}\p{N}]`, "u", "_", false, ""},
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
		// V8 实测（node 22）"1,234,567".search(/\B(?=(\d{3})+(?!\d))/) = -1：
		// (\d{3}) 跨不过逗号，千分位模式只匹配纯数字串（如 "1234567"）。
		{"bytes thousands comma", "\\B(?=(\\d{3})+(?!\\d))", "g", "1,234,567", false},
		{"bytes thousands digits", "\\B(?=(\\d{3})+(?!\\d))", "g", "1234567", true},
		{"named backref", "(?<x>ab)\\k<x>", "", "abab", true},
		{"named backref fail", "(?<x>ab)\\k<x>", "", "abac", false},
		// V8 实测：a(?=(b))c 在 "abc" 上不匹配（前瞻零宽，c 需在 a 之后紧邻）。
		{"lookahead capture", "a(?=(b))b", "", "abb", true},
		{"lookbehind capture", "(?<=(a))b", "", "ab", true},
		// 贪心重复回退：(?:(?!\{).)* 必须先吃满再逐次让出给末尾的 \}。
		{"brace lookahead repeat", "\\{(?:(?!\\{).)*\\}", "", "pre {a} post", true},
		{"brace no close", "\\{(?:(?!\\{).)*\\}", "", "{abc", false},
		// 组捕获 + 回退：重复失败后位置/捕获必须原子恢复（\d{3} 吃到一半
		// 会把 pos 停在错误位置，导致 (?!\d) 误判）。
		{"thousand giveback", "(\\d{3})+(?!\\d)", "", "1234567.89", true},
		// 反向引用触发回退：a+ 贪心吃满后 \1 需要让出。
		{"backref giveback", "^(a+)\\1$", "", "aaaaaaaa", true},
		{"backref giveback short", "^(a+)\\1$", "", "aaaaa", false},
		{"backref giveback group", "(a+)\\1", "", "aaaa", true},
		// 懒重复：先停住，后续失败时补吃。
		{"lazy repeat", "a*?b", "", "aaab", true},
		{"lazy repeat no b", "a*?b", "", "aaa", false},
		// 类内 \s 内联展开（嵌套类 [[...]] 会让 [ 变成类成员）。
		{"class s inline", "[^\\s]+@[^\\s]+", "", "mail a@b.c end", true},
		{"class S fallback", "[\\S]+", "", "ab cd", true},
		{"class S fallback no", "[\\S]+", "", "  ", false},
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

// TestBtCaptures 验证前瞻/后行断言内的捕获组写入整体结果、命名反向引用取值。
func TestBtCaptures(t *testing.T) {
	cases := []struct {
		pattern, input string
		want           []int
	}{
		// "1234567" 千分位：匹配于索引 1，组1 为最后一次迭代 "567"（V8 实测 ["","567"]）。
		{`\B(?=(\d{3})+(?!\d))`, "1234567", []int{1, 1, 4, 7}},
		// /a(?=(b))/ 组1 = "b"（V8 实测 ["a","b"]）。
		{`a(?=(b))`, "ab", []int{0, 1, 1, 2}},
		// /(?<=(a))b/ 整体 "b"（索引 1）、组1 = "a"（V8 实测 ["b","a"]）。
		{`(?<=(a))b`, "ab", []int{1, 2, 0, 1}},
		// 命名反向引用 (?<x>ab)\k<x> 整体 "abab"、组x = "ab"。
		{`(?<x>ab)\k<x>`, "abab", []int{0, 4, 0, 2}},
	}
	for _, c := range cases {
		compiled, err := Compile(c.pattern, "g")
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pattern, err)
		}
		m := compiled.MatchIndex(c.input)
		if m == nil {
			t.Errorf("MatchIndex(%q) = nil, want %v", c.input, c.want)
			continue
		}
		for i, w := range c.want {
			if i >= len(m) || m[i] != w {
				t.Errorf("MatchIndex(%q) = %v, want %v (idx %d)", c.input, m, c.want, i)
				break
			}
		}
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

// TestUnicodeSetsV 验证 v 模式（unicodeSets）支持：属性类、衍生属性展开、
// 集合差集（对齐 Node 22 / V8 实测）。
func TestUnicodeSetsV(t *testing.T) {
	cases := []struct {
		name, pattern, input string
		want                 bool
	}{
		{"zeroWidth zwsp", `^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Mark}|\p{Surrogate})+$`, "\u200b", true},
		{"zeroWidth normal", `^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Mark}|\p{Surrogate})+$`, "abc", false},
		{"zeroWidth combining", `^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Mark}|\p{Surrogate})+$`, "\u0301", true},
		{"markChar", `^\p{Mark}$`, "\u0301", true},
		{"setdiff Mc", `^[\p{Spacing_Mark}--[\u1734\u302E\u302F]]$`, "\u0903", true},
		{"setdiff excluded", `^[\p{Spacing_Mark}--[\u1734\u302E\u302F]]$`, "\u1734", false},
		{"setdiff other", `^[\p{Spacing_Mark}--[\u1734\u302E\u302F]]$`, "\u1AFF", false},
		// Go UTF-8 模型无法表示孤立代理项（Node 为 true，文档标注差异）。
		{"surrogate alias", `^\p{Surrogate}$`, string([]byte{0xED, 0xA0, 0x80}), false},
		{"RGI emoji approx", `^\p{RGI_Emoji}$`, "\u2764", true}, // 近似 \p{So}（Node 裸 ❤ 为 false，需 VS16）
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compiled, err := Compile(c.pattern, "v")
			if err != nil {
				t.Fatalf("Compile(%q, v): %v", c.pattern, err)
			}
			m := compiled.MatchIndex(c.input)
			if (m != nil) != c.want {
				t.Errorf("MatchIndex(%q) match=%v, want %v", c.input, m != nil, c.want)
			}
		})
	}
}

// TestCompileCache 验证相同 (source, flags) 复用编译结果（正则字面量每次
// 求值都会调用 Compile；缓存避免重复翻译 + Go regexp 编译）。
func TestCompileCache(t *testing.T) {
	a, err := Compile(`([a-z]+)(\d+)`, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	b, err := Compile(`([a-z]+)(\d+)`, "")
	if err != nil {
		t.Fatalf("Compile second: %v", err)
	}
	if a != b {
		t.Error("identical (source, flags) should return the cached instance")
	}
	// 不同 flags 不共享缓存。
	c, err := Compile(`([a-z]+)(\d+)`, "i")
	if err != nil {
		t.Fatalf("Compile with 'i': %v", err)
	}
	if a == c {
		t.Error("different flags must not share the cache entry")
	}
	// 缓存的 Compiled 可正常匹配。
	m := a.MatchIndex("prefix abc123 suffix")
	if m == nil {
		t.Fatal("cached compiled regex should match")
	}
}
