package interpreter

import "testing"

// 本文件覆盖顶层 try/catch/finally 语句作为最后一条语句时的返回值（REPL 语义）。
// 这是 v1.2 发现并修复的既有缺陷：compileStmtValue 缺少 TryStmt 分支，导致
// 顶层 try 语句的值未作为返回值。修复方式为新增 compileTryValue 值模式编译。
// 风格对齐 vm_test.go：vmEvalStr + 表格驱动。

// TestTopLevelTryReturnValue: try 块正常完成时，其最后表达式值作为返回值。
func TestTopLevelTryReturnValue(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// try 正常完成
		{`try { 42 } catch(e) { 99 }`, "42"},
		{`try { 1 + 2 } catch(e) { 0 }`, "3"},
		{`try { var x = 10; x * 2 } catch(e) { 0 }`, "20"},
		// try 内多语句，最后一表达式为返回值
		{`try { var a = 1; var b = 2; a + b } catch(e) { 0 }`, "3"},
		// try 块为空 → undefined
		{`try { } catch(e) { 99 }`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestTopLevelCatchReturnValue: try 抛出时，catch 块的返回值。
func TestTopLevelCatchReturnValue(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// throw 字符串
		{`try { throw "hello"; } catch(e) { e }`, "hello"},
		// throw Error 对象，访问属性
		{`try { throw new Error("oops"); } catch(e) { e.message }`, "oops"},
		{`try { throw new TypeError("typefail"); } catch(e) { e.name }`, "TypeError"},
		// catch 块返回独立值
		{`try { throw "x"; } catch(e) { "caught" }`, "caught"},
		// catch 块访问 cause
		{`try { throw new Error("outer", {cause: "inner"}); } catch(e) { e.cause }`, "inner"},
		// 可选 catch 绑定（无参数）
		{`try { throw "x"; } catch { "no-param" }`, "no-param"},
		// catch 块为空 → undefined
		{`try { throw "x"; } catch(e) { }`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestTopLevelTryFinally: finally 块的返回值覆盖 try/catch。
func TestTopLevelTryFinally(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// finally 正常 → finally 的值
		{`try { 1 } finally { 99 }`, "99"},
		// try 抛出 + catch + finally → finally 的值
		{`try { throw "x"; } catch(e) { e } finally { "done" }`, "done"},
		// try 正常、无 catch、有 finally
		{`try { 42 } finally { "final" }`, "final"},
		// finally 为空 → undefined
		{`try { 42 } finally { }`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestTopLevelTryNested: 嵌套 try/catch（含 rethrow）。
func TestTopLevelTryNested(t *testing.T) {
	got := vmEvalStr(t, `
try {
  try {
    throw "inner";
  } catch(e) {
    throw e + "-rethrown";
  }
} catch(e2) {
  e2;
}`)
	if got != "inner-rethrown" {
		t.Errorf("nested try = %q, want inner-rethrown", got)
	}
}

// TestCatchRethrow: catch 块内 rethrow 传播到外层 catch。
// 这覆盖了 VM findHandlerInFrame 的既有 bug：catch 块内 rethrow 会重新匹配
// 同一个 catch handler 导致无限循环（phase==1 的 handler 未被跳过）。
func TestCatchRethrow(t *testing.T) {
	got := vmEvalStr(t, `try { try { throw "x"; } catch(e) { throw e; } } catch(outer) { outer }`)
	if got != "x" {
		t.Errorf("rethrow = %q, want x", got)
	}
	// 函数内 rethrow
	got = vmEvalStr(t, `function f() { try { throw "y"; } catch(e) { throw e + "!"; } }
try { f() } catch(e) { e }`)
	if got != "y!" {
		t.Errorf("in-func rethrow = %q, want y!", got)
	}
}

// TestTopLevelTryAfterOtherStmts: try 不是第一条语句时的返回值。
func TestTopLevelTryAfterOtherStmts(t *testing.T) {
	got := vmEvalStr(t, `var a = 1; var b = 2; try { a + b } catch(e) { 0 }`)
	if got != "3" {
		t.Errorf("try after stmts = %q, want 3", got)
	}
}
