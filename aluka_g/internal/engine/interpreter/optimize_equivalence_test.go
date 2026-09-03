package interpreter

import (
	"strings"
	"testing"
)

// 字节码优化对拍测试：同一源码编译两次（一次关闭优化、一次默认优化），
// 分别经 RunModule 执行，结果/错误必须完全一致。优化器（OptimizeModule）
// 挂载在 vm.Compile/CompileAST 编译管线（FormatVersion 20 起默认启用），
// 任何优化 pass 引入语义偏差都会在此暴露。

func optimizeEquivalence(t *testing.T, code string) {
	t.Helper()

	// 未优化产物：显式关闭 VM 的优化开关。
	rawVM, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	rawVM.SetOptimizeBytecode(false)
	rawMod, err := rawVM.Compile(code, "eq.js")
	if err != nil {
		t.Fatalf("Compile(no-opt) failed: %v", err)
	}
	rawVal, rawErr := rawVM.RunModule(rawMod)

	// 优化产物：默认开关（true）。
	optVM, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	optMod, err := optVM.Compile(code, "eq.js")
	if err != nil {
		t.Fatalf("Compile(opt) failed: %v", err)
	}
	optVal, optErr := optVM.RunModule(optMod)

	rawStr, optStr := "", ""
	if rawVal != nil {
		rawStr = rawVal.String()
	}
	if optVal != nil {
		optStr = optVal.String()
	}
	if rawStr != optStr {
		t.Fatalf("result mismatch:\n  raw: %q\n  opt: %q", rawStr, optStr)
	}
	if (rawErr == nil) != (optErr == nil) {
		t.Fatalf("error presence mismatch: raw=%v opt=%v", rawErr, optErr)
	}
	if rawErr != nil && !strings.Contains(optErr.Error(), rawErr.Error()) &&
		!strings.Contains(rawErr.Error(), optErr.Error()) {
		t.Fatalf("error mismatch:\n  raw: %v\n  opt: %v", rawErr, optErr)
	}
}

func TestOptimizeEquivalence(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		// 常量折叠：number/string/bigint。
		{"const arithmetic", `console.log(1 + 2 * 3 - 4 / 2)`},
		{"const string concat", `console.log("a" + "b" + "c")`},
		{"const bigint", `console.log(5n % 2n, 7n / 2n, 2n ** 3n)`},
		{"const edge cases", `console.log(0 / 0, 1 / 0, -1 / 0)`},
		{"const mixed no fold", `console.log(1 + "x", "y" + 2)`},
		{"statement value", `1 + 2; 3 * 4; console.log("done")`},
		// 不可达代码删除。
		{"dead code after return", `function f() { return 1; 2 + 3; } console.log(f())`},
		{"dead code with throw", `function f() { throw new Error("boom"); 1 + 2; } try { f() } catch (e) { console.log("caught", e.message) }`},
		// STORE/LOAD 融合（赋值表达式值回传）。
		{"assignment value", `let x; console.log(x = 42, x)`},
		{"assignment in args", `let x = 0; function id(v) { return v } console.log(id(x = 7), x)`},
		// 综合：循环/闭包/try-finally/异常。
		{"loop closure", `let fns = []; for (let i = 0; i < 3; i++) fns.push(() => i); console.log(fns.map(f => f()).join(","))`},
		{"try finally return", `function f() { try { return 1 } finally { console.log("fin") } } console.log(f())`},
		{"try catch throw", `try { throw { code: 42 } } catch (e) { console.log("err", e.code) }`},
		{"nested try finally", `function f() { let out = []; try { try { out.push(1); throw new Error("inner") } finally { out.push(2) } } catch (e) { out.push(3) } return out.join(",") } console.log(f())`},
		{"break in try", `let s = ""; for (let i = 0; i < 5; i++) { try { if (i === 2) break; s += i } finally { s += "." } } console.log(s)`},
		{"continue in try", `let s = ""; for (let i = 0; i < 4; i++) { try { if (i % 2) continue; s += i } finally { s += "|" } } console.log(s)`},
		// 数据类型与内建。
		{"json roundtrip", `const o = { a: [1, 2, 3], b: { c: "x" } }; console.log(JSON.stringify(JSON.parse(JSON.stringify(o))))`},
		{"regexp", `console.log("a1b2c3".replace(/\d/g, "#"), /ab+c/.test("xabbbc"))`},
		{"destructure", `const { a, b: [c, ...d] } = { a: 1, b: [2, 3, 4] }; console.log(a, c, d.join("+"))`},
		{"template strings", "const n = 3; console.log(`v=${n + 4} ${'ok'}`)"},
		{"spread", `console.log([0, ...[1, 2], 3].join(","), { ...{ x: 1 }, y: 2 }.y)`},
		{"class", `class A { constructor(x) { this.x = x } get v() { return this.x * 2 } } console.log(new A(21).v)`},
		{"generator", `function* g() { yield 1; yield 2 } console.log([...g()].join(","))`},
		{"async await", `(async () => { const v = await Promise.resolve(41); console.log(v + 1) })()`},
		{"optional chain", `const o = { a: null }; console.log(o?.a?.b ?? "default")`},
		{"in instance of", `class C {} console.log({} instanceof Object, "a" in { a: 1 })`},
		{"getter setter", `const o = { _v: 1, get v() { return this._v }, set v(x) { this._v = x * 2 } }; o.v = 5; console.log(o.v)`},
		{"array methods", `console.log([3, 1, 2].sort((a, b) => a - b).map(x => x * 2).filter(x => x > 2).reduce((a, b) => a + b, 0))`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			optimizeEquivalence(t, c.code)
		})
	}
}
