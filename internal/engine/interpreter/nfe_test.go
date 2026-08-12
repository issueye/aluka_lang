package interpreter

import "testing"

// 具名函数表达式（NFE）自引用：`const f = function named() {...}` 的函数体
// 内 `named` 绑定到函数自身（递归 NFE）。此前 AST 引用扫描器（walkValue
// Ptr 分支）不遍历嵌套子树导致检测恒 false，自引用槽从不分配。
func TestNFESelfReference(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`const f = function factorial(n) { if (n <= 1) return 1; return n * factorial(n - 1); }; f(5)`, "120"},
		{`const g = (function self(x) { if (x <= 0) return "done"; return self(x - 1); })(3); g`, "done"},
		{`const h = function named() { return typeof named; }; h()`, "function"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}
