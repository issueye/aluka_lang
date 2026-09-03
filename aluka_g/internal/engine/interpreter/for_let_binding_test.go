package interpreter

import "testing"

// 本文件覆盖 ES2015 for 循环 per-iteration 绑定（`for (let i = 0; ...)` 的
// 闭包语义）：每次迭代的闭包必须捕获当次迭代的 i（0 1 2），而非循环结束
// 后的共享值（3 3 3）。此前只分配单槽位导致所有闭包共享，异步回调中
// `if (i % N === 0)` 等条件全部成立，可触发 fs 调用风暴（线程耗尽崩溃）。

// TestForLetPerIterationBinding: 同步闭包捕获每次迭代值。
func TestForLetPerIterationBinding(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// 基础：闭包数组
		{`var f=[]; for (let i = 0; i < 3; i++) { f.push(() => i); } f[0]()+","+f[1]()+","+f[2]()`, "0,1,2"},
		// 函数表达式
		{`var f=[]; for (let i = 0; i < 3; i++) { f.push(function(){ return i*10; }); } f[0]()+","+f[1]()+","+f[2]()`, "0,10,20"},
		// 嵌套循环同名遮蔽
		{`var out=[]; for (let i = 0; i < 2; i++) { for (let i = 10; i < 12; i++) { out.push(() => i); } } out[0]()+","+out[1]()+","+out[2]()+","+out[3]()`, "10,11,10,11"},
		// 多声明
		{`var f=[]; for (let i = 0, j = 5; i < 2; i++, j++) { f.push(() => i + j); } f[0]()+","+f[1]()`, "5,7"},
		// continue 路径的迭代值
		{`var f=[]; for (let i = 0; i < 4; i++) { if (i === 1) continue; f.push(() => i); } f[0]()+","+f[1]()+","+f[2]()`, "0,2,3"},
		// var 保持共享（非 per-iteration）
		{`var f=[]; for (var i = 0; i < 3; i++) { f.push(() => i); } f[0]()+","+f[1]()+","+f[2]()`, "3,3,3"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestForLetAsyncClosure: 异步（setTimeout）闭包捕获每次迭代值。
func TestForLetAsyncClosure(t *testing.T) {
	got := vmEvalPromise(t, `
var out = [];
for (let j = 0; j < 3; j++) {
	Promise.resolve().then(() => out.push(j));
}
Promise.all([1,2,3].map(() => Promise.resolve())).then(() => {
	globalThis.__r = out.slice().sort().join(",");
});
`)
	if got != "0,1,2" {
		t.Errorf("async per-iteration = %q, want %q", got, "0,1,2")
	}
}
