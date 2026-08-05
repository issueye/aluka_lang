package interpreter

import (
	"strings"
	"testing"
)

// === P0-3: Date 构造器与全局 URI 编码函数回归测试 =============================

// TestDateBasics 验证 Date 全局存在与基础行为（VM 路径）。
func TestDateBasics(t *testing.T) {
	if got := vmEvalStr(t, "typeof Date"); got != "function" {
		t.Fatalf("typeof Date = %q, want function", got)
	}
	// new Date(0).getTime() === 0
	if got := vmEvalStr(t, "new Date(0).getTime()"); got != "0" {
		t.Errorf("new Date(0).getTime() = %q, want 0", got)
	}
	// 无效日期
	if got := vmEvalStr(t, "isNaN(new Date('xx').getTime())"); got != "true" {
		t.Errorf("isNaN(new Date('xx').getTime()) = %q, want true", got)
	}
	// Date.now()
	if got := vmEvalStr(t, "Date.now() > 0"); got != "true" {
		t.Errorf("Date.now() > 0 = %q, want true", got)
	}
	// Date.parse ISO
	if got := vmEvalStr(t, "Date.parse('1970-01-01T00:00:00Z')"); got != "0" {
		t.Errorf("Date.parse('1970-01-01T00:00:00Z') = %q, want 0", got)
	}
	// toISOString
	if got := vmEvalStr(t, "new Date(0).toISOString()"); got != "1970-01-01T00:00:00.000Z" {
		t.Errorf("toISOString = %q", got)
	}
	// toString / typeof
	if got := vmEvalStr(t, "typeof new Date(0)"); got != "object" {
		t.Errorf("typeof new Date(0) = %q, want object", got)
	}
	if got := vmEvalStr(t, "new Date(0) instanceof Date"); got != "true" {
		t.Errorf("instanceof Date = %q, want true", got)
	}
}

// TestDateComponents 验证本地时间组件读取。
func TestDateComponents(t *testing.T) {
	// 2026-08-05T12:34:56.789 本地时间
	code := `
var d = new Date(2026, 7, 5, 12, 34, 56, 789);
var r = d.getFullYear() + "|" + d.getMonth() + "|" + d.getDate() + "|" +
         d.getDay() + "|" + d.getHours() + "|" + d.getMinutes() + "|" +
         d.getSeconds() + "|" + d.getMilliseconds();
r
`
	if got := vmEvalStr(t, code); got != "2026|7|5|3|12|34|56|789" {
		t.Errorf("date components = %q, want 2026|7|5|3|12|34|56|789", got)
	}
	// Date.UTC 与 getUTC*
	code2 := `
var u = new Date(Date.UTC(2026, 7, 5, 12, 34, 56));
u.getUTCFullYear() + "|" + u.getUTCMonth() + "|" + u.getUTCDate() + "|" + u.getUTCHours()
`
	if got := vmEvalStr(t, code2); got != "2026|7|5|12" {
		t.Errorf("UTC components = %q, want 2026|7|5|12", got)
	}
}

// TestDateSetters 验证 set* 方法（含缺失参数沿用当前值）。
func TestDateSetters(t *testing.T) {
	code := `
var d = new Date(0);
d.setFullYear(2020);
d.getFullYear()
`
	if got := vmEvalStr(t, code); got != "2020" {
		t.Errorf("setFullYear = %q, want 2020", got)
	}
	code2 := `
var d = new Date(2020, 0, 15);
d.setMonth(6);
d.getMonth() + "|" + d.getDate()
`
	if got := vmEvalStr(t, code2); got != "6|15" {
		t.Errorf("setMonth(6) = %q, want 6|15", got)
	}
}

// TestDateStringFormats 验证 toString/toJSON/toUTCString 系列。
func TestDateStringFormats(t *testing.T) {
	// toJSON 输出 ISO
	if got := vmEvalStr(t, "new Date(0).toJSON()"); got != "1970-01-01T00:00:00.000Z" {
		t.Errorf("toJSON = %q", got)
	}
	// valueOf === getTime
	if got := vmEvalStr(t, "new Date(42).valueOf()"); got != "42" {
		t.Errorf("valueOf = %q, want 42", got)
	}
	// toString 含 GMT
	s := vmEvalStr(t, "new Date(0).toString()")
	if !strings.Contains(s, "1970") || !strings.Contains(s, "GMT") {
		t.Errorf("toString() = %q, want 1970 + GMT", s)
	}
	// 无效日期 toString
	if got := vmEvalStr(t, "new Date('xx').toString()"); got != "Invalid Date" {
		t.Errorf("invalid toString = %q", got)
	}
}

// TestDateCloning 验证以 Date 为参数构造。
func TestDateCloning(t *testing.T) {
	code := `
var a = new Date(1234567890);
var b = new Date(a);
b.getTime()
`
	if got := vmEvalStr(t, code); got != "1234567890" {
		t.Errorf("Date clone = %q, want 1234567890", got)
	}
}

// TestURIEncoding 验证 encodeURI/decodeURI/Component 语义。
func TestURIEncoding(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"encodeURIComponent ascii", `encodeURIComponent("a b/")`, "a%20b%2F"},
		{"encodeURIComponent utf8", `encodeURIComponent("中")`, "%E4%B8%AD"},
		{"encodeURIComponent unreserved", `encodeURIComponent("-_.!~*'()")`, "-_.!~*'()"},
		{"decodeURIComponent utf8", `decodeURIComponent("%E4%B8%AD")`, "中"},
		{"decodeURIComponent slash", `decodeURIComponent("%2F")`, "/"},
		{"encodeURI keeps reserved", `encodeURI("a b?c/d:g@h")`, "a%20b?c/d:g@h"},
		{"encodeURI utf8", `encodeURI("中")`, "%E4%B8%AD"},
		{"decodeURI keeps encoded reserved", `decodeURI("%2F")`, "%2F"},
		{"decodeURI space", `decodeURI("https://a.com/x%20y")`, "https://a.com/x y"},
	}
	for _, tc := range cases {
		got := vmEvalStr(t, tc.code)
		if got != tc.want {
			t.Errorf("%s: %s = %q, want %q", tc.name, tc.code, got, tc.want)
		}
	}
}

// TestDateAST 验证 AST 解释器路径同样注册 Date/URI。
func TestDateAST(t *testing.T) {
	if got := evalStr(t, "typeof Date"); got != "function" {
		t.Errorf("AST: typeof Date = %q, want function", got)
	}
	if got := evalStr(t, "new Date(0).getTime()"); got != "0" {
		t.Errorf("AST: getTime = %q, want 0", got)
	}
	if got := evalStr(t, "encodeURIComponent('a b')"); got != "a%20b" {
		t.Errorf("AST: encodeURIComponent = %q, want a%%20b", got)
	}
}
