package jit

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// TestM2RegallocPlanApplies 验证寄存器分配规划对典型累加循环生效。
func TestM2RegallocPlanApplies(t *testing.T) {
	// 构造 `for(i=0;i<n;i++) s+=i` 的字节码：循环体 straight-line，s/i 为热 local。
	// 简化：直接构造 IR 程序（跳过低字节码 lowering），验证 tryPlanRegalloc。
	p := &Program{
		NumLocals: 3, // slot 0=this, 1=s, 2=i
		Code: []Instr{
			// header (i=0): LOAD 1; LOAD 2; ADD; STORE 1; LOAD 2; Const 1; ADD; STORE 2; LOAD 2; Const 3000000; LT
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpAdd},
			{Op: OpStoreLocal, Operand: 1},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpConst, Value: 1},
			{Op: OpAdd},
			{Op: OpStoreLocal, Operand: 2},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpConst, Value: 3000000},
			{Op: OpLt},
			{Op: OpJumpTrue, Operand: 0}, // backedge → header
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpReturn},
		},
	}
	// assigned：所有指令证明 slot 1/2 是 Number。
	assigned := make([]uint64, len(p.Code))
	for i := range assigned {
		assigned[i] = uint64(1)<<1 | uint64(1)<<2
	}
	plan := tryPlanRegalloc(p, assigned)
	if plan == nil {
		t.Fatal("regalloc plan is nil, expected a plan for the accumulation loop")
	}
	if len(plan.hot) == 0 {
		t.Fatal("no hot locals selected")
	}
	t.Logf("hot locals: %v, regs: %v", plan.hot, plan.reg)

	// 验证完整 native 编译不报错（含 reload/spill 发射）。
	// 需要 nativePlan 字段等，走 compileNative 会做输入 lowering；这里直接
	// 调 compileNativeProgram 验证发射正确性。
	lowered := &Program{
		NumParams:           0,
		NumLocals:           p.NumLocals,
		SelfUpvalue:         -1,
		Code:                p.Code,
		nativeNumberArgs:    0,
		nativePreassigned:   uint64(1)<<1 | uint64(1)<<2,
		nativeTrace:         false,
		traceExitDepths:     nil,
		traceExceptionExits: nil,
	}
	if _, err := compileNativeProgram(lowered, true); err != nil {
		t.Fatalf("compileNativeProgram: %v", err)
	}
}

// 确保 bytecode/engine 引用不产生未使用警告（后续扩展用）。
var _ = bytecode.InstrSize
var _ = engine.TypeNumber
