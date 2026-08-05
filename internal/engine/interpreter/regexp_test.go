package interpreter

import (
	"strings"
	"testing"
)

// === RegExp 基础:字面量与匹配 ===

func TestRegexpLiteralAndTest(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`/ab+c/.test("abbbc")`, "true"},
		{`/ab+c/.test("ac")`, "false"},
		{`/a\/b/.test("a/b")`, "true"},
		{`/[a-z]+/.test("hello123")`, "true"},
		{`/^v?(\d+)\.(\d+)\.(\d+)$/.test("v1.2.3")`, "true"},
		{`typeof /a/`, "object"},
		{`/a/ instanceof RegExp`, "true"},
		{`/a/g instanceof RegExp`, "true"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestRegexpFlags(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`/hello/i.test("HELLO")`, "true"},
		{`/a.b/s.test("a\nb")`, "true"},
		{`/a.b/.test("a\nb")`, "false"},
		{`/^b/m.test("a\nb")`, "true"},
		{`/^b/.test("a\nb")`, "false"},
		{`/a/gi.flags`, "gi"},
		{`/a/img.flags`, "gim"},
		{`/a/i.global + "," + /a/g.global + "," + /a/m.multiline`, "false,true,true"},
		{`/a/s.dotAll + "," + /a/u.unicode + "," + /a/y.sticky`, "true,true,true"},
		{`/\p{L}+/u.test("héllo")`, "true"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestRegexpExec(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`/(\d{4})-(\d{2})/.exec("2026-08")[0]`, "2026-08"},
		{`/(\d{4})-(\d{2})/.exec("2026-08")[1]`, "2026"},
		{`/(\d{4})-(\d{2})/.exec("2026-08")[2]`, "08"},
		{`/(\d{4})-(\d{2})/.exec("no match")`, "null"},
		{`/(?<y>\d{4})-(?<m>\d{2})/.exec("2026-08").groups.y`, "2026"},
		{`/(?<y>\d{4})-(?<m>\d{2})/.exec("2026-08").groups.m`, "08"},
		{`/(a)(b)?/.exec("a")[2]`, "undefined"},
		{`/a/.exec("xay").index`, "1"},
		{`/a/.exec("xay").input`, "xay"},
		{`/(\d+)/.exec("a123b")[1]`, "123"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestRegexpLastIndex(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// 全局匹配逐次推进 lastIndex。
		{`var re = /\d+/g; var out = []; var m; while ((m = re.exec("a1 b22"))) { out.push(m[0] + "@" + re.lastIndex); } out.join(" ")`, "1@2 22@6"},
		// 无匹配后 lastIndex 重置为 0。
		{`var re = /\d+/g; re.exec("abc"); re.lastIndex`, "0"},
		// 手动设置 lastIndex。
		{`var re = /\d/g; re.lastIndex = 4; re.exec("a1 b2")[0]`, "2"},
		// sticky：必须从 lastIndex 处匹配。
		{`var re = /a/y; re.test("abc") + " " + re.test("abc")`, "true false"},
		{`var re = /b/y; re.lastIndex = 1; re.test("abc")`, "true"},
		// lastIndex 是数据属性，可读写。
		{`var re = /a/g; re.lastIndex = 2; re.lastIndex`, "2"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestRegexpToStringSource(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`/ab+c/gi.toString()`, "/ab+c/gi"},
		{`/a\/b/.source`, "a\\/b"},
		{`new RegExp().source`, "(?:)"},
		{`new RegExp("abc").source`, "abc"},
		{`/a/g.source`, "a"},
		{`String(/x/)`, "/x/"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

// === RegExp 构造器 ===

func TestRegexpConstructor(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`new RegExp("ab+c").test("abbbc")`, "true"},
		{`RegExp("a").test("a")`, "true"}, // 无 new 调用
		{`new RegExp(/a/g).flags`, "g"},
		{`new RegExp(/a/g, "i").flags`, "i"}, // 覆盖 flags
		{`new RegExp(/a/g, "i").source`, "a"},
		{`new RegExp("a", "i").ignoreCase`, "true"},
		{`RegExp.prototype.constructor === RegExp`, "true"},
		{`new RegExp("1.2").test("1.2")`, "true"},
		{`new RegExp("^v?(\\d+)\\.(\\d+)$").test("v1.2")`, "true"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

// === RegExp 非法输入(语法错误) ===

func TestRegexpErrors(t *testing.T) {
	bad := []string{
		`new RegExp("(", "")`,
		`/a/gg`,                 // 重复 flags
		`new RegExp("a", "x")`,  // 非法 flags
		`new RegExp("[a-z")`,    // 未闭合字符类
	}
	for _, code := range bad {
		_, err := vmEvalStrErr(t, code)
		if err == nil {
			t.Errorf("%s: want error, got nil", code)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "syntaxerror") {
			t.Errorf("%s: error should be SyntaxError, got: %v", code, err)
		}
	}

	// 反向引用/前瞻/后行断言已由回溯引擎支持（此前为编译期报错），验证行为正确。
	good := []struct{ code, want string }{
		{`"aa".match(/(a)\1/)[0]`, "aa"},
		{`"ab".match(/a(?=b)/)[0]`, "a"},
		{`"ab".match(/(?<=a)b/)[0]`, "b"},
		{`"abab".match(/(?<x>ab)\k<x>/)[0]`, "abab"},
		{`"12345".match(/\B(?=(\d{3})+(?!\d))/)[1]`, "345"},
	}
	for _, c := range good {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

// === String.prototype 正则集成 ===

func TestStringRegexpMethods(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`"a,b,c".split(/,/).join("|")`, "a|b|c"},
		{`"a1b2".split(/(\d)/).join("|")`, "a|1|b|2|"},
		{`"a1b2".replace(/(\d)/g, "[$1]")`, "a[1]b[2]"},
		{`"a1b2".replace(/(\d)/, "[$1]")`, "a[1]b2"},
		{`"hello world".replace(/o/g, function(m) { return m.toUpperCase(); })`, "hellO wOrld"},
		{`"abc".replace(/b/, "<$&>")`, "a<b>c"},
		{`"2026-08".replace(/(\d{4})-(\d{2})/, function(m, y, mo) { return mo + "/" + y; })`, "08/2026"},
		{`"aXbXc".replaceAll(/X/g, "x")`, "axbxc"},
		{`"2026-08-04".match(/\d+/g).join("-")`, "2026-08-04"},
		{`"2026-08".match(/(\d{4})-(\d{2})/)[1]`, "2026"},
		{`"abcde".search(/d/)`, "3"},
		{`"abcde".search(/x/)`, "-1"},
		{`"a1b2".matchAll(/(\w)(\d)/g)[0][1]`, "a"},
		{`"a1b2".matchAll(/(\w)(\d)/g).length`, "2"},
		{`"a-b_c".split(/[-_]/).join("|")`, "a|b|c"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 转义与特殊字符 ===

func TestRegexpEscapes(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`/\d+/.test("123")`, "true"},
		{`/\w+/.test("abc_1")`, "true"},
		{`/\s+/.test(" \t")`, "true"},
		{`/a\+b/.test("a+b")`, "true"},
		{`/x\*/.test("x*")`, "true"},
		{`/a\.b/.test("a.b")`, "true"},
		{`/\x41/.test("A")`, "true"},
		{`/\u0041/.test("A")`, "true"},
		{`/\bword\b/.test("a word!")`, "true"},
		{`/[\d]+/.test("123")`, "true"},
		{`/(?:ab)+/.test("abab")`, "true"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("%s = %q, want %q", c.code, got, c.want)
		}
	}
}

