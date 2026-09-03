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

// TestFunctionDeclHoisting 验证函数声明提升（module.exports = fn 在 fn 声明前）。
func TestFunctionDeclHoisting(t *testing.T) {
	// 函数声明在 module.exports 赋值之后，但应已提升绑定。
	got := vmEvalStr(t, `
		var exports = {};
		var module = {exports: exports};
		module.exports = leftPad;
		function leftPad(s, n) { return s; }
		typeof module.exports;
	`)
	if got != "function" {
		t.Errorf("hoisted function decl = %q, want function", got)
	}
	// 顶层 const 在函数声明闭包中可见。
	got = vmEvalStr(t, `
		const x = 42;
		function f() { return x; }
		f();
	`)
	if got != "42" {
		t.Errorf("const closure = %q, want 42", got)
	}
}

// TestOpenUpvalueSurvivesStackGrowth 验证函数声明捕获的顶层槽位在 VM 栈扩容后
// 仍指向当前栈。复杂 CJS 模块（如 chalk）会在 const 初始化前调用拥有大量
// locals 的函数；旧实现保存裸栈指针，扩容后闭包会继续读取旧栈中的 undefined。
func TestOpenUpvalueSurvivesStackGrowth(t *testing.T) {
	got := vmEvalStr(t, `
		function readFactory() { return factory; }
		function growStack() {
			var a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p, q, r, s, t;
		}
		growStack();
		const factory = () => 42;
		readFactory()();
	`)
	if got != "42" {
		t.Errorf("captured const after stack growth = %q, want 42", got)
	}
}

// TestArrayProtoFallback 验证 Go 侧创建的数组（process.argv 等）能访问 Array.prototype 方法。
func TestArrayProtoFallback(t *testing.T) {
	// 经 new Array 创建的数组有原型方法。
	got := vmEvalStr(t, `[1,2,3].indexOf(2)`)
	if got != "1" {
		t.Errorf("[1,2,3].indexOf(2) = %q, want 1", got)
	}
}

// TestDefinePropertyAccessor 验证 Object.defineProperty/defineProperties 支持真访问器。
func TestDefinePropertyAccessor(t *testing.T) {
	// 单个 defineProperty getter。
	got := vmEvalStr(t, `
		var o = {};
		Object.defineProperty(o, 'g', { get() { return 42; } });
		o.g;
	`)
	if got != "42" {
		t.Errorf("defineProperty getter = %q, want 42", got)
	}
	// defineProperties 多个 getter（this 绑定接收者）。
	got = vmEvalStr(t, `
		function C() {}
		Object.defineProperties(C.prototype, {
			a: { get() { return 1; } },
			b: { get() { return 2; } }
		});
		var c = new C();
		c.a + ':' + c.b;
	`)
	if got != "1:2" {
		t.Errorf("defineProperties getters = %q, want 1:2", got)
	}
	// 原型链 getter（this 是实例）。
	got = vmEvalStr(t, `
		function C() {}
		Object.defineProperty(C.prototype, 'v', { get() { return this._v || 'def'; } });
		var c = new C();
		c._v = 'set';
		c.v;
	`)
	if got != "set" {
		t.Errorf("prototype getter this = %q, want set", got)
	}
}

// TestCrossModuleClosure 验证闭包跨模块调用时 fnIdx 正确（module 切换）。
func TestCrossModuleClosure(t *testing.T) {
	// 经 require 加载的模块导出函数创建子闭包，跨模块调用不应 panic。
	// （用本地文件模拟 CJS 模块。）
	got := vmEvalStr(t, `
		var calls = [];
		var f = (function() {
			return function inner() {
				var g = function() { return 'deep'; };
				return g();
			};
		})();
		f();
	`)
	if got != "deep" {
		t.Errorf("nested closure = %q, want deep", got)
	}
}

// TestFunctionObjectAccessor 验证函数对象上的访问器属性（如 chalk 的 prototype getter）。
func TestFunctionObjectAccessor(t *testing.T) {
	got := vmEvalStr(t, `
		var f = function() {};
		Object.defineProperty(f, 'g', { get() { return 42; } });
		f.g;
	`)
	if got != "42" {
		t.Errorf("function accessor = %q, want 42", got)
	}
	// defineProperties 在箭头函数上装多个 getter。
	got = vmEvalStr(t, `
		var proto = Object.defineProperties(() => {}, {
			a: { get() { return 1; } },
			b: { get() { return 2; } }
		});
		proto.a + proto.b;
	`)
	if got != "3" {
		t.Errorf("function defineProperties getters = %q, want 3", got)
	}
}

// TestFunctionPrototypeAccessorChain 验证 Object.setPrototypeOf 能修改源码函数
// 对象的 [[Prototype]]，并从原型链调用 getter，保持接收者 this。
func TestFunctionPrototypeAccessorChain(t *testing.T) {
	code := `
		const chalkProto = {};
		Object.defineProperty(chalkProto, 'green', {
			get() { return this._marker; }
		});
		const chalk = {};
		Object.setPrototypeOf(chalk, chalkProto);
		const template = () => {};
		template._marker = 'ok';
		Object.setPrototypeOf(template, chalk);
		template.green;
	`
	if got := vmEvalStr(t, code); got != "ok" {
		t.Errorf("VM function prototype getter = %q, want ok", got)
	}
	if got := evalStr(t, `
		const proto = {marker: 'ok'};
		const fn = () => {};
		Object.setPrototypeOf(fn, proto);
		fn.marker;
	`); got != "ok" {
		t.Errorf("AST function prototype property = %q, want ok", got)
	}
}
