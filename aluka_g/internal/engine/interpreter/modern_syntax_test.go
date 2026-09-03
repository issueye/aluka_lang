package interpreter

import "testing"

// 本文件覆盖一批 ES2019-ES2023 语法特性的修复与新增实现。
// 风格对齐 vm_test.go：vmEvalStr + 表格驱动。

// === 数字分隔符（ES2021）==============================================

func TestNumberSeparators(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		// 十进制（整数/小数/指数）
		{`1_000_000`, 1000000},
		{`1_000_000.5`, 1000000.5},
		{`1_000.500_25`, 1000.50025},
		// 十六进制
		{`0xFF_FF`, 65535},
		{`0xDEAD_BEEF`, 3735928559},
		// 八进制
		{`0o7777_7777`, 16777215},
		// 二进制
		{`0b1010_1010`, 170},
		{`0b1111_0000_1111_0000`, 61680},
	}
	for _, c := range cases {
		v, err := vmEvalTest(t, c.code)
		if err != nil {
			t.Fatalf("Eval(%q) error: %v", c.code, err)
		}
		got, ok := v.Float()
		if !ok {
			t.Errorf("Eval(%q) not a number: %q", c.code, v.String())
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.code, got, c.want)
		}
	}
}

// === Error cause（ES2022）=============================================

func TestErrorCause(t *testing.T) {
	// 基本构造与访问
	got := vmEvalStr(t, `var inner = new Error("inner"); var outer = new Error("outer", {cause: inner}); outer.cause.message`)
	if got != "inner" {
		t.Errorf("cause.message = %q, want inner", got)
	}
	// cause 链：外层访问内层的 cause
	got = vmEvalStr(t, `var a = new Error("a"); var b = new Error("b", {cause: a}); b.cause === a`)
	if got != "true" {
		t.Errorf("cause identity = %q, want true", got)
	}
	// 无 cause 时为 undefined
	got = vmEvalStr(t, `new Error("plain").cause`)
	if got != "undefined" {
		t.Errorf("no cause = %q, want undefined", got)
	}
	// 在函数内的 try/catch 中抛出带 cause 的错误（顶层 throw/catch 存在既有缺陷，
	// 见 development-plan 已知问题；此处用函数包裹验证 cause 经 throw 后保留）。
	got = vmEvalStr(t, `
function f() {
  try {
    throw new Error("wrapped", {cause: "original"});
  } catch (e) {
    return e.cause;
  }
}
f();`)
	if got != "original" {
		t.Errorf("thrown cause = %q, want original", got)
	}
}

// === 逻辑赋值运算符（ES2021）=========================================

func TestLogicalAssignmentOR(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// ||= : 左值 falsy 时赋值
		{`var a = 0; a ||= 10; a`, "10"},
		{`var a = false; a ||= "yes"; a`, "yes"},
		{`var a = null; a ||= "fallback"; a`, "fallback"},
		// ||= : 左值 truthy 时短路（不赋值，不求右值）
		{`var a = 5; a ||= 99; a`, "5"},
		{`var a = "x"; var called = false; a ||= (called = true); called`, "false"},
		// ||= 返回值是赋值后的左值
		{`var a = 0; (a ||= 7)`, "7"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestLogicalAssignmentAND(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// &&= : 左值 truthy 时赋值
		{`var a = 1; a &&= 10; a`, "10"},
		{`var a = "x"; a &&= "y"; a`, "y"},
		// &&= : 左值 falsy 时短路
		{`var a = 0; a &&= 99; a`, "0"},
		{`var a = null; a &&= "never"; a`, "null"},
		{`var a = 0; var called = false; a &&= (called = true); called`, "false"},
		// 返回值
		{`var a = 1; (a &&= 7)`, "7"},
		{`var a = 0; (a &&= 7)`, "0"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestLogicalAssignmentNullish(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// ??= : 左值 null/undefined 时赋值
		{`var a = null; a ??= "default"; a`, "default"},
		{`var a = undefined; a ??= 42; a`, "42"},
		// ??= : 左值非 nullish 时短路（即使 falsy 如 0、"" 也保留）
		{`var a = 0; a ??= 99; a`, "0"},
		{`var a = ""; a ??= "x"; a`, ""},
		{`var a = false; a ??= true; a`, "false"},
		// 返回值
		{`var a = null; (a ??= 7)`, "7"},
		{`var a = 0; (a ??= 7)`, "0"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestLogicalAssignmentMember(t *testing.T) {
	// 成员表达式上的逻辑赋值
	got := vmEvalStr(t, `var o = {x: 0}; o.x ||= 5; o.x`)
	if got != "5" {
		t.Errorf("obj.x ||= : got %q, want 5", got)
	}
	got = vmEvalStr(t, `var o = {x: 5}; o.x ??= 99; o.x`)
	if got != "5" {
		t.Errorf("obj.x ??= : got %q, want 5", got)
	}
	got = vmEvalStr(t, `var config = {}; config.timeout ??= 3000; config.timeout`)
	if got != "3000" {
		t.Errorf("config init via ??= : got %q, want 3000", got)
	}
}
