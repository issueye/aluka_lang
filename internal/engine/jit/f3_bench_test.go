package jit

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// F3 专项 benchmark：fib(30) 的 Quick 自递归执行，用于精确定位 executeQuick
// 递归路径的开销热点（配合 -cpuprofile 分析）。

func fibQuickProgram() *Program {
	return &Program{
		NumParams:   1,
		NumLocals:   4,
		SelfUpvalue: 0,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 2},
			{Op: OpLt},
			{Op: OpJumpFalse, Operand: 6},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpJump, Operand: 17},
			{Op: OpPushSelf},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpSub},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpPushSelf},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 2},
			{Op: OpSub},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpAdd},
			{Op: OpReturn},
		},
	}
}

func TestFibQuickProgram(t *testing.T) {
	p := fibQuickProgram()
	result, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(30)})
	if err != nil || reason != Executed || result.String() != "832040" {
		t.Fatalf("fib(30) = %v reason=%v err=%v (want 832040)", result, reason, err)
	}
}

func BenchmarkFibQuickRecursion(b *testing.B) {
	p := fibQuickProgram()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(30)}); err != nil {
			b.Fatal(err)
		}
	}
}
