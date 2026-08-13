package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// M1 优化专项测试：数组索引读快路径（OpGetElem）。
//
// 覆盖快路径正确性与慢路径语义等价（越界 / 负数 / 非整数 / NaN / 稀疏）。
//
// 说明：M1-1 全局变量 IC 经 benchmark 实测无收益（全局对象 shape.index 的
// map 查找本就很快，IC 省下的开销被类型断言 + 哈希抵消，反而会污染 IC 表
// 与对象属性访问竞争槽位），已基于测量证据回退，故无对应测试。

func TestVMArrayIndexGetFastPath(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"read element", "var a = [10,20,30]; a[1]", "20"},
		{"read first", "var a = [5]; a[0]", "5"},
		{"read last", "var a = [1,2,3]; a[2]", "3"},
		{"out of bounds", "var a = [1,2]; a[5]", "undefined"},
		{"negative index", "var a = [1,2]; a[-1]", "undefined"},
		{"non-integer index", "var a = [1,2,3]; a[1.5]", "undefined"},
		{"loop sum", "var a=[1,2,3,4,5]; var s=0; for(var i=0;i<5;i++){s+=a[i];} s", "15"},
		{"index is variable", "var a=[7,8]; var i=1; a[i]", "8"},
		{"index computed", "var a=[3,4,5]; a[1+1]", "5"},
		{"string index falls back", "var a=[1,2]; a['1']", "2"},
		{"nan index", "var a=[1,2]; a[NaN]", "undefined"},
		{"infinity index", "var a=[1,2]; a[Infinity]", "undefined"},
		{"zero index", "var a=[9]; a[0]", "9"},
		{"sparse read", "var a=[]; a[10]=42; a[10]", "42"},
		{"sparse hole read", "var a=[]; a[10]=42; a[5]", "undefined"},
		{"float index hole", "var a=[1,2]; a[0.0]", "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vmEvalStr(t, c.code); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestVMArrayIndexGetFastPathSemantics(t *testing.T) {
	// 快路径与慢路径必须语义一致：同一数组、同一索引，无论走哪条路径结果相同。
	// 通过字符串下标（慢路径）与数值下标（快路径）对照验证。
	code := `
		var a = [10, 20, 30];
		var byNum = [a[0], a[1], a[2], a[3], a[-1], a[1.5]];
		var byStr = [a["0"], a["1"], a["2"], a["3"], a["-1"], a["1.5"]];
		JSON.stringify(byNum) + "|" + JSON.stringify(byStr);
	`
	if got := vmEvalStr(t, code); got != "[10,20,30,null,null,null]|[10,20,30,null,null,null]" {
		t.Errorf("fast/slow path mismatch: got %q", got)
	}
}

// --- M1 benchmark（jit.Off 隔离字节码层收益） -----------------------------

func BenchmarkArrayIndexRead(b *testing.B) {
	vm, err := NewVM()
	if err != nil {
		b.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Off})
	// 循环体 5 次固定索引数组读，放大数组索引在循环中的占比。
	code := `var a = [1,2,3,4,5]; var s = 0; for (var i = 0; i < 200000; i++) { s += a[0] + a[1] + a[2] + a[3] + a[4]; } s`
	if _, err := vm.Eval(code, "bench.js"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Eval(code, "bench.js"); err != nil {
			b.Fatal(err)
		}
	}
}
