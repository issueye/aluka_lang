package interpreter

import (
	"testing"
)

// === P0-2: 箭头函数 this 词法捕获 + Function.prototype call/apply/bind ========

// TestArrowThisMethod 方法内箭头函数捕获外层 this。
func TestArrowThisMethod(t *testing.T) {
	code := `
var o = { v: 42, f: function() { return (() => this.v)(); } };
o.f()
`
	if got := vmEvalStr(t, code); got != "42" {
		t.Errorf("method arrow this = %q, want 42", got)
	}
}

// TestArrowThisShorthand 简写方法内箭头（对象字面量 + class 方法）。
func TestArrowThisShorthand(t *testing.T) {
	code := `
var o = { v: 7, f() { var g = () => this.v; return g(); } };
var c = class { constructor(){ this.x = 5; } m() { return () => this.x; } };
o.f() + "|" + (new c()).m()()
`
	if got := vmEvalStr(t, code); got != "7|5" {
		t.Errorf("shorthand arrow this = %q, want 7|5", got)
	}
}

// TestArrowThisNested 多层嵌套箭头穿透捕获外层 this。
func TestArrowThisNested(t *testing.T) {
	code := `
function f() { return () => () => this.x; }
f.call({ x: 9 })()()
`
	if got := vmEvalStr(t, code); got != "9" {
		t.Errorf("nested arrow this = %q, want 9", got)
	}
}

// TestArrowThisTopLevel 顶层 this 仍为 undefined（非严格）。
func TestArrowThisTopLevel(t *testing.T) {
	code := `
var r = this;
r === undefined || r === globalThis
`
	if got := vmEvalStr(t, code); got != "true" {
		t.Errorf("top-level this = %q, want true", got)
	}
}

// TestFunctionCallApplyBind VM 闭包经 call/apply/bind 正确绑定 this。
func TestFunctionCallApplyBind(t *testing.T) {
	code := `
function f(a) { return this.v + a; }
var viaCall = f.call({ v: 100 }, 1);
var viaApply = f.apply({ v: 200 }, [2]);
var bound = f.bind({ v: 300 });
var viaBound = bound(3);
viaCall + "|" + viaApply + "|" + viaBound
`
	if got := vmEvalStr(t, code); got != "101|202|303" {
		t.Errorf("call/apply/bind = %q, want 101|202|303", got)
	}
}

// TestArrowIgnoredThis 箭头函数忽略 call/apply 传入的 this（词法优先）。
func TestArrowIgnoredThis(t *testing.T) {
	code := `
var o = { v: 5 };
var arrow = () => this;
arrow.call(o) === arrow()
`
	if got := vmEvalStr(t, code); got != "true" {
		t.Errorf("arrow ignored this = %q, want true", got)
	}
}
