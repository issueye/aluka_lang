package interpreter

import "testing"

// === 可选链短路栈残留回归（字节码 VM 路径） ================================
//
// 旧编译产物可选链短路时在链尾残留链内值（无清理块），后续指令的栈操作
// 被污染——表现为链尾 `?? 默认值`、后续属性读取、循环内累计等结果错误，
// 且残留深度经回边带回循环头导致 ComputeMaxStack 发散（编译期卡死）。
// 修复：链尾生成短路清理块 + 链值计数（isMember 修正）。
// 本组用例均走默认字节码 VM（vmEvalStr），对齐 coding-agent 真实形态。

func TestVMOptionalChainResidualRegression(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// coding-agent settings-manager 原形态：链尾 ?? 默认值。
		{`class S { constructor() { this.settings = { retry: null }; } getProviderRetrySettings() { return { timeoutMs: this.settings.retry?.provider?.timeoutMs, maxRetries: this.settings.retry?.provider?.maxRetries, maxRetryDelayMs: this.settings.retry?.provider?.maxRetryDelayMs ?? 60000 }; } } const s = new S(); JSON.stringify(s.getProviderRetrySettings())`, `{"maxRetryDelayMs":60000}`},
		// 链尾 ?? 默认值（非短路路径走原值）。
		{`const o = { a: { b: { c: 1 } } }; o?.a?.b?.c ?? 99`, "1"},
		{`const o = { a: { x: { c: 1 } } }; o?.a?.x?.c ?? 99`, "1"},
		{`const o = { a: { x: null } }; o?.a?.x?.c ?? 99`, "99"},
		// 成员调用链短路（方法为 null 跳过调用）。
		{`const o = { f: () => 7 }; o.f?.() ?? -1`, "7"},
		{`const o = { g: null }; o.g?.() ?? -1`, "-1"},
		// obj.method 链（this 绑定走 temp slot）。
		{`const o = { m: { n: (x) => x * 2 } }; o?.m?.n(21) ?? -1`, "42"},
		{`const o = { m: null }; o?.m?.n(21) ?? -1`, "-1"},
		// 计算属性链。
		{`const o = { k: 5 }; o?.["k"] ?? -1`, "5"},
		{`const o = { k: 5 }; o?.["z"] ?? -1`, "-1"},
		// 嵌套链短路：null 链值 `null ?? RHS` 会求值 RHS（?? 只对 null/undefined
		// 短路），短路仅发生在链继续访问（null?.x = undefined）且无 ?? 兜底时。
		{`let called = false; const o = { deep: null }; const r = o?.deep?.x ?? (called = true, 1); r + ':' + called`, "1:true"},
		// 调用参数内链（callee 非链 + 参数链短路）。
		{`const o = { v: 3 }; const take = (a, b) => (a ?? 0) + (b ?? 0); take(o?.v, o?.w)`, "3"},
		// 链中链 f?.(a?.b)：内层链短路残留含外层 callee。
		{`const o = { g: (a) => a ?? -2 }; o?.g?.(o?.v ?? 100) ?? -3`, "100"},
		// 循环内链（回归：残留经回边污染循环头）。
		{`const o = { a: { b: { c: 42 } } }; let sum = 0; for (let i = 0; i < 10; i++) { sum += o?.a?.b?.c ?? 0; } sum`, "420"},
		// 短路路径在条件判断中再次消费结果。
		{`const o = { a: null }; if ((o?.a?.b) === undefined) 'yes'`, "yes"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === for-of 无声明迭代变量回归 ============================================
//
// `for (x of ...)`（赋给已有变量，非 let/const 声明）无 per-iteration
// 绑定槽，旧编译器仍发射 CloseUpvalues(NumLocals) 越界（validate 报
// slot out of range，编译失败）。修复：仅 VarDecl 且含 Decls 时关闭。

func TestVMForOfNoDeclBindingRegression(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`let x = 0; const a = [1,2,3]; for (x of a) { } x`, "3"},
		{`var total = 0; for (total of [5,6,7]) { } total`, "7"},
		// 循环体内含闭包/函数（会触发 upvalue 关闭路径）。
		{`let x = 0; const fns = []; for (x of [1,2]) { fns.push(() => x); } x + ':' + fns[0]() + ':' + fns[1]()`, "2:2:2"},
		// break/continue 路径也须平衡。
		{`let x = 0; for (x of [1,2,3,4]) { if (x === 3) break; } x`, "3"},
		{`let x = 0, n = 0; for (x of [1,2,3,4]) { if (x % 2 === 0) continue; n += x; } n`, "4"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}
