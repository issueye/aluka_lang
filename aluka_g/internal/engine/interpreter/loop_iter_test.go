package interpreter

import "testing"

// === 循环体块级声明按迭代绑定回归（字节码 VM 路径） ========================
//
// ES2015 语义：classic for / for-in / while / do-while 的循环体内块级 let/const
// 声明，每次迭代都是独立副本；闭包应捕获当次迭代的值（而非共享同一槽位的终值）。
// 旧编译器为体块级 let/const 静态分配一次槽位，各轮闭包捕获同一个活槽，循环结束
// 后全部读到终值。修复：对齐 for-of 的 per-iteration 机制，在循环体编译前记录
// iterationSlotStart，continue/break 目标处 OpCloseUpvalues 封存本轮 upvalue。
// 本组用例覆盖 coding-agent-bundle-report.md §3.2 bisect 全矩阵 + zod v4 形态。

func TestVMLoopBodyPerIterationBindingRegression(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// === 报告 §3.2 bisect 矩阵 ===
		// for (let i...) 体 const x = i 被闭包捕获：node 0,1,2 aluka 曾 2,2,2。
		{`const out = []; for (let i = 0; i < 3; i++) { const x = i; out.push(() => x); } out.map(f => f()).join(',')`, "0,1,2"},
		// for (const k of ...) 体 const y = k：本应正确（for-of 已 per-iteration）。
		{`const out = []; for (const k of [1,2,3]) { const y = k; out.push(() => y); } out.map(f => f()).join(',')`, "1,2,3"},
		// for (const k in ...) 体 const z = k：node a,b,c aluka 曾 c,c,c。
		{`const out = []; for (const k in {a:1,b:1,c:1}) { const z = k; out.push(() => z); } out.map(f => f()).join(',')`, "a,b,c"},
		// while 体 const q = w：node 0,1,2 aluka 曾 2,2,2。
		{`const out = []; let w = 0; while (w < 3) { const q = w; out.push(() => q); w++; } out.map(f => f()).join(',')`, "0,1,2"},
		// for (const k in ...) 头变量：node a,b,c aluka 曾 c,c,c。
		{`const out = []; for (const k in {a:1,b:1,c:1}) { out.push(() => k); } out.map(f => f()).join(',')`, "a,b,c"},
		// for (let k of ...) 头变量：已正确（for-of per-iteration）。
		{`const out = []; for (let k of [1,2,3]) { out.push(() => k); } out.map(f => f()).join(',')`, "1,2,3"},

		// 循环体内 `let l;` 无初始化器必须每轮复位为 undefined，否则
		// `l || (l = x)` 会把第一轮赋值带到后续轮次（babel template.ast）。
		{`const out = []; for (const x of ["a","b"]) { let l; l || (l = x); out.push(l); } out.join(',')`, "a,b"},
		{`const m = new Map([["node:path",{name:"_nodePath"}],["./migration.ts",{name:"_migration"}]]); const headers = []; for (const [t, r] of m) { let l; null != l || (l = r.name); headers.push(l); } headers.join(',')`, "_nodePath,_migration"},

		// === do-while 形态 ===
		{`const out = []; let w = 0; do { const q = w; out.push(() => q); w++; } while (w < 3); out.map(f => f()).join(',')`, "0,1,2"},

		// === zod v4 _installLazyMethods 级联形态（for (const key in methods) + getter 闭包） ===
		{`function install(proto, methods) { for (const key in methods) { const fn = methods[key]; Object.defineProperty(proto, key, { get() { return () => fn.name; } }); } } const proto = {}; install(proto, { aaa: function aaa(){}, bbb: function bbb(){}, ccc: function ccc(){} }); proto.aaa() + ',' + proto.bbb() + ',' + proto.ccc()`, "aaa,bbb,ccc"},

		// === break / continue 路径平衡 ===
		// continue：跳过后续语句仍封存本轮 upvalue。
		{`const out = []; for (const k in {a:1,b:1,c:1,d:1}) { const z = k; if (k === 'b' || k === 'd') continue; out.push(() => z); } out.map(f => f()).join(',')`, "a,c"},
		// break：中断处闭包仍捕获当轮值。
		{`const out = []; let w = 0; while (true) { const q = w; out.push(() => q); if (w === 2) break; w++; } out.map(f => f()).join(',')`, "0,1,2"},
	}

	for _, c := range cases {
		got, err := vmEvalStrErr(t, c.code)
		if err != nil {
			t.Errorf("VM.Eval(%q) error: %v", c.code, err)
			continue
		}
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestVMLoopBodyVarStillSharedRegression 确保 var 声明仍为函数作用域共享
// （per-iteration 封存只针对块级 let/const，不破坏 var 语义）。
func TestVMLoopBodyVarStillSharedRegression(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// var 声明在循环体内被闭包捕获：按规范共享同一绑定 → 全部终值。
		{`const out = []; for (const k in {a:1,b:1,c:1}) { var v = k; out.push(() => v); } out.map(f => f()).join(',')`, "c,c,c"},
		// while 内 var 同样共享（w=0,1,2 迭代，终值 q=2）。
		{`const out = []; let w = 0; while (w < 3) { var q = w; out.push(() => q); w++; } out.map(f => f()).join(',')`, "2,2,2"},
	}
	for _, c := range cases {
		got, err := vmEvalStrErr(t, c.code)
		if err != nil {
			t.Errorf("VM.Eval(%q) error: %v", c.code, err)
			continue
		}
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestVMLoopBodyPerIterationAsyncBindingRegression 验证 per-iteration 绑定在
// async 闭包经 await 恢复后仍正确（closeUpvalues/reopenUpvalues 路径）。
func TestVMLoopBodyPerIterationAsyncBindingRegression(t *testing.T) {
	got := vmEvalPromise(t, `
const out = [];
for (const k in {a:1,b:1}) { const z = k; out.push(async () => z); }
Promise.all(out.map(f => f())).then(function(v) { globalThis.__r = v.join(','); });
`)
	if got != "a,b" {
		t.Errorf("async per-iteration = %q, want %q", got, "a,b")
	}
}
