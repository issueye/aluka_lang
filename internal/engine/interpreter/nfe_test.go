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

// TestNFENameShadowing：函数体 var/let/const/形参必须遮蔽 NFE 名字。
// Express 5 Layer.prototype.match = function match(path) { let match; ... }
// 依赖此语义；未遮蔽时 match 恒为函数，任意路径都返回 true。
func TestNFENameShadowing(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`const f = function match() { let match; return typeof match; }; f()`, "undefined"},
		{`const f = function match() { var match; return typeof match; }; f()`, "undefined"},
		{`const f = function match() { const match = 1; return match; }; f()`, "1"},
		{`const f = function foo(foo) { return foo; }; f(42)`, "42"},
		{`const f = function named() { let named = "x"; return named; }; f()`, "x"},
		// Express 5 路由层精简模型：matcher 未命中时 layer.match 必须是 false。
		{`(function(){ const layer = { matchers: [function(p){ return p === "/health" ? {path:p} : false; }], slash: false }; layer.match = function match(path) { let match; if (path != null) { if (this.slash) return true; let i = 0; while (!match && i < this.matchers.length) { match = this.matchers[i](path); i++; } } if (!match) return false; return true; }; return [layer.match("/"), layer.match("/health"), layer.match("/services")].join(","); })()`, "false,true,false"},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			got := vmEvalStr(t, c.code)
			if got != c.want {
				t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
			}
		})
	}
}
