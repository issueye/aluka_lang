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
		// 成员调用结果继续可选取属性：调用结果已计入链栈，父 MemberExpr
		// 不得重复计数，否则短路清理会多 POP 一个 local 槽。
		{`const m = new Map(); function f(k) { return m.get(k)?.provider; } f("missing") ?? "none"`, "none"},
		{`const m = new Map([["x", { provider: "ok" }]]); function f(k) { return m.get(k)?.provider; } f("x")`, "ok"},
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
		// undici Headers.get 形态：`m.get(lc ? name : name.toLowerCase())?.value ?? null`
		// ——参数 ternary 内部的非链 method call 不得计入外层链残留（否则短路
		// 清理 POP 过多、帧栈越界）。
		{`const h = { headersMap: { get: (n) => ({ value: "v" }) } }; function f(name, lc) { return h.headersMap.get(lc ? name : name.toLowerCase())?.value ?? null; } f("X", false)`, "v"},
		{`const h = { headersMap: { get: () => null } }; function f(name, lc) { return h.headersMap.get(lc ? name : name.toLowerCase())?.value ?? null; } f("X", false)`, "null"},
		{`const h = { headersMap: { get: (n) => ({ value: n }) } }; function f(name, lc) { return h.headersMap.get(lc ? name : name.toLowerCase())?.value ?? "none"; } f("HeLLo", true)`, "HeLLo"},
		// 私有字段存在性检查 `#x in obj`（undici 迭代器 brand 依赖）。
		{`class A { #x = 1; static has(o) { return #x in o; } } const a = new A(); String(A.has(a)) + "," + String(A.has({}))`, "true,false"},
		// 私有字段访问 this.#x / obj.#x。
		{`class A { #v = 42; get() { return this.#v; } } new A().get()`, "42"},
		// 裸迭代器（有 next 无 [Symbol.iterator]）经 yield* 迭代（undici 迭代器）。
		{`const it = { i: 0, next() { return this.i < 2 ? { value: this.i++, done: false } : { value: undefined, done: true }; } }; function* g() { yield* it; } const r = [g().next().value, g().next().value]; r.join(",")`, "0,1"},
		// Function.prototype[Symbol.hasInstance] 可调用（undici 依赖）。
		{`typeof Function.prototype[Symbol.hasInstance]`, "function"},
		{`class F {}; const f = new F(); F[Symbol.hasInstance](f)`, "true"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 普通调用作链对象的短路计数回归 ========================================
//
// `f(a)?.x` / `f(a,b)?.m(cb)`：compileCall 普通调用分支的实参经 compileCallArg
// 计入链值计数（每实参 +1），但 OpCall 后未回补 -N——链计数比真实栈值多
// N-1，父 MemberExpr 再 +1 后短路残留多记 N，清理块多发 POP 把局部槽当操
// 作数弹掉，后续 LOAD_LOCAL 帧栈越界 panic（aluka-desktop registry.ts
// findProviderModel 形态）。spread 调用两分支则少计 1（短路残留泄漏）。
func TestVMOptionalChainPlainCallArgRegression(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// 崩溃原形态：findProviderEntry(provider)?.models.find(cb) 短路
		//（返回 undefined），随后读取局部槽不得越界。
		{`function findEntry(p) { return undefined; } function f(provider, modelId) { const id = modelId?.trim(); if (!id) return "early"; if (provider?.trim()) { return findEntry(provider)?.models.find((m) => m.id === id) ?? "none"; } return "fall"; } f("p", "m")`, "none"},
		// 非短路路径取真实值。
		{`function findEntry(p) { return { models: { find: (cb) => cb({ id: "x" }) } }; } findEntry("p")?.models.find((m) => m.id)`, "x"},
		// 多实参（旧产物多发 2 个 POP）。
		{`function f(a, b) { return undefined; } f(1, 2)?.x ?? "none"`, "none"},
		{`function f(a, b) { return { x: 9 }; } f(1, 2)?.x ?? -1`, "9"},
		// 零实参（计数原本正确，不得回归）。
		{`function f() { return undefined; } f()?.x ?? "none"`, "none"},
		// 短路后继续读同帧局部变量（局部槽被误弹即崩）。
		{`let acc = ""; function miss(a) { return undefined; } function run(k) { const id = k; const hit = miss(k)?.deep.value; acc = id + ":" + (hit ?? "u"); return acc; } run("K")`, "K:u"},
		// spread 普通调用作链对象（短路残留泄漏回归）。
		{`function f(...xs) { return undefined; } f(...[1, 2])?.x ?? "none"`, "none"},
		{`function f(...xs) { return { x: xs.length }; } f(...[1, 2, 3])?.x ?? -1`, "3"},
		// 可选调用 a?.(...spread)：短路与调用路径。
		{`const o = {}; o.m?.(...[1, 2]) ?? -1`, "-1"},
		{`const f = (...xs) => xs.length; f?.(...[1, 2, 3])`, "3"},
		// 方法 spread 调用作链对象：短路与调用路径。
		{`const o = { m(...xs) { return { v: 6 + xs.length }; } }; o.m(...[1, 2])?.v ?? -1`, "8"},
		{`const o = null; o?.m(...[1, 2]) ?? -1`, "-1"},
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
