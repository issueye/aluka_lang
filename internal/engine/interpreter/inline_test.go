package interpreter

// I-2 小函数内联行为测试：`const f = ...` 单表达式箭头函数调用点展开后，
// 结果与未内联语义一致（通过 VM 执行验证）。

import "testing"

// TestInlineBehavior 覆盖内联展开的正确性（含回退路径）。
func TestInlineBehavior(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		// 算术表达式（LoadLocal + PushInt + 二元运算）
		{"add", `const add = (a, b) => a + b; globalThis.__r = add(1, 2);`, "3"},
		{"mul-chain", `const f = (x) => x * 2 + 1; globalThis.__r = f(5);`, "11"},
		{"sub-div", `const f = (a, b, c) => (a - b) / c; globalThis.__r = f(10, 4, 2);`, "3"},
		{"literal", `const z = () => 42; globalThis.__r = z();`, "42"},
		// 未传参数 b = undefined：内联与未内联行为一致（aluka 既有
		// undefined 参与算术返回 0 而非 NaN，见 value.Float；Node 为 NaN）。
		{"undefined-arg", `const f = (a, b) => a + b; globalThis.__r = f(1);`, "1"},
		{"string-concat", `const f = (a, b) => a + "!" + b; globalThis.__r = f("x", "y");`, "x!y"},
		// 比较
		{"cmp", `const f = (a, b) => a < b; globalThis.__r = f(1, 2);`, "true"},
		{"cmp-false", `const f = (a, b) => a >= b; globalThis.__r = f(1, 2);`, "false"},
		// 属性访问（GetProp / GetPropLocal）
		{"prop", `const f = (o) => o.v * 2; globalThis.__r = f({ v: 21 });`, "42"},
		{"prop-string", `const f = (s) => s.length; globalThis.__r = f("abc");`, "3"},
		// 方法调用（CallMethod，nameIdx 重映射）
		{"method", `const f = (s) => s.toUpperCase(); globalThis.__r = f("ab");`, "AB"},
		// 嵌套调用点：内联体在调用者循环/表达式中
		{"nested-expr", `const add = (a, b) => a + b; globalThis.__r = add(1, 2) * add(3, 4);`, "21"},
		{"loop", `const add = (a, b) => a + b; let s = 0; for (let i = 0; i < 10; i++) s += add(i, 1); globalThis.__r = s;`, "55"},
		// 返回未内联路径
		{"closure-fallback", `const k = 3; const f = (x) => x + k; globalThis.__r = f(1);`, "4"},
		{"shadowed-rebind", `const f = (a) => a + 1; f = 5; globalThis.__r = typeof f;`, "number"},
		{"let-fallback", `let f = (a) => a + 1; globalThis.__r = f(2);`, "3"},
		{"param-exceed", `const f = (a) => a; globalThis.__r = f(1, 2, 3);`, "1"},
		{"recursive-fallback", `const rec = (n) => n <= 0 ? 0 : rec(n - 1); globalThis.__r = rec(3);`, "0"},
		// 真实调用其他函数（函数值传递，不内联）
		{"pass-as-value", `const add = (a, b) => a + b; function apply(f, x, y) { return f(x, y); } globalThis.__r = apply(add, 1, 2);`, "3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := vmEvalStr(t, tc.code)
			if got != tc.want {
				t.Errorf("code %q = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// TestInlineCallerSlots 验证调用者已有局部变量时内联展开不破坏其槽位。
func TestInlineCallerSlots(t *testing.T) {
	got := vmEvalStr(t, `
const add = (a, b) => a + b;
const x = 100;
const y = 200;
globalThis.__r = add(x, y) + add(1, 2);`)
	if got != "303" {
		t.Errorf("caller slots clobbered: got %q, want 303", got)
	}
}

// TestInlineInsideFunction 验证函数体内 const 绑定 + 调用展开。
func TestInlineInsideFunction(t *testing.T) {
	got := vmEvalStr(t, `
function outer(n) {
	const inc = (x) => x + 1;
	let s = 0;
	for (let i = 0; i < n; i++) s += inc(i);
	return s;
}
globalThis.__r = outer(5);`)
	if got != "15" {
		t.Errorf("inline inside function: got %q, want 15", got)
	}
}

// TestInlineConstantRebind 验证 const 同名遮蔽回退（块作用域内 const 覆盖）。
func TestInlineConstantRebind(t *testing.T) {
	got := vmEvalStr(t, `
const f = (a) => a + 1;
{ const f = (a) => a * 10; globalThis.__r = f(3); }`)
	if got != "30" {
		t.Errorf("shadowed const inline: got %q, want 30", got)
	}
}
