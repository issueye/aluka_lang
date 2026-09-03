package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// M2 Native 寄存器分配 benchmark：数值累加循环（寄存器化 sum/i 的典型场景）。
// 对比 jit.Off（字节码 VM）与 jit.Auto（Native + 寄存器分配）。

func benchNumLoop(b *testing.B, mode jit.Mode) {
	vm, err := NewVM()
	if err != nil {
		b.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, TraceBudget: 65536})
	code := `var s = 0; for (var i = 0; i < 3000000; i++) { s += i; } s`
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

func BenchmarkNumLoopOff(b *testing.B)   { benchNumLoop(b, jit.Off) }
func BenchmarkNumLoopAuto(b *testing.B)  { benchNumLoop(b, jit.Auto) }
func BenchmarkNumLoopQuick(b *testing.B) { benchNumLoop(b, jit.Quick) }

// BenchmarkNumLoopMulti：多个累加器 + 计数器（4 个热 local），寄存器压力更大，
// 更能体现寄存器分配消除 Frame 内存往返的收益。
func BenchmarkNumLoopMulti(b *testing.B) {
	vm, err := NewVM()
	if err != nil {
		b.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, TraceBudget: 65536})
	code := `var a=0,b=0,c=0; for (var i = 0; i < 3000000; i++) { a += i; b += i*2; c += i*3; } a+b+c`
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
