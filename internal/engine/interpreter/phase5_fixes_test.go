package interpreter

// 引擎缺陷修复回归测试（Phase 5 真实 npm 包暴露）：
//   - 一元加 ToNumber（+"x" → NaN，而非 0）
//   - 按位非 ~ 运算符
//   - 计算成员调用 obj[key](args)（保留 this 绑定）

import (
	"math"
	"testing"
)

// TestUnaryPlusToNumber 验证一元加遵循 ToNumber 语义。
func TestUnaryPlusToNumber(t *testing.T) {
	cases := map[string]string{
		`+"42"`:      "42",
		`+""`:        "0",
		`+"  "`:      "0",
		`+"0xFF"`:    "255",
		`+true`:      "1",
		`+false`:     "0",
		`+null`:      "0",
		`String(+"x")`:  "NaN",
		`String(+"  x  ")`: "NaN",
		`String(+undefined)`: "NaN",
	}
	for code, want := range cases {
		got := vmEvalStr(t, code)
		if got != want {
			t.Errorf("%s = %q, want %q", code, got, want)
		}
	}
}

// TestBitNot 验证按位非 ~。
func TestBitNot(t *testing.T) {
	cases := map[string]string{
		`~0`:  "-1",
		`~5`:  "-6",
		`~-1`: "0",
		`~255`: "-256",
	}
	for code, want := range cases {
		got := vmEvalStr(t, code)
		if got != want {
			t.Errorf("%s = %q, want %q", code, got, want)
		}
	}
}

// TestComputedMethodCall 验证 obj[key](args) 计算成员调用保留 this。
func TestComputedMethodCall(t *testing.T) {
	code := `
		var obj = { x: 10, add(n) { return this.x + n; } };
		var m = "add";
		obj[m](5);
	`
	got := vmEvalStr(t, code)
	if got != "15" {
		t.Errorf("obj[m](5) = %q, want 15", got)
	}

	// 链式计算成员调用。
	code2 := `
		var o = { a: { b: { f() { return 42; } } } };
		o["a"]["b"]["f"]();
	`
	if got := vmEvalStr(t, code2); got != "42" {
		t.Errorf("chain computed call = %q, want 42", got)
	}

	// 计算成员调用 + spread（用 rest param 收集）。
	code3 := `
		var obj = { sum(...nums) { var t = 0; for (var i = 0; i < nums.length; i++) t += nums[i]; return t; } };
		var args = [1, 2, 3];
		obj["sum"](...args);
	`
	if got := vmEvalStr(t, code3); got != "6" {
		t.Errorf("computed spread call = %q, want 6", got)
	}
}

// TestForOfNestedFunction 验证 for-of 内嵌套函数编译不 panic。
func TestForOfNestedFunction(t *testing.T) {
	code := `
		var used = ['a', 'b'];
		var styles = {};
		for (var i = 0; i < used.length; i++) {
			(function(model) {
				styles[model] = {
					get() {
						return function () { return model; };
					}
				};
			})(used[i]);
		}
		styles.a.get()();
	`
	got := vmEvalStr(t, code)
	if got != "a" {
		t.Errorf("for-of nested = %q, want a", got)
	}
}

// TestBareBuiltinRequire 验证计算属性访问链在复杂场景下工作。
func TestBareBuiltinRequire(t *testing.T) {
	// 多级计算成员读取 + 调用。
	got := vmEvalStr(t, `
		var matrix = { row: { col: { val: 99, get() { return this.val; } } } };
		matrix["row"]["col"]["get"]();
	`)
	if got != "99" {
		t.Errorf("deep computed call = %q, want 99", got)
	}
}

// 防止 import 未用（math 仅在文档引用）。
var _ = math.NaN
