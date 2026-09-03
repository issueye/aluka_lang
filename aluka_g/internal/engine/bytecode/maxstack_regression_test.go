package bytecode_test

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// === ComputeMaxStack 回边虚高发散回归 ======================================
//
// 可选链短路路径曾在链尾残留链内值（旧编译产物无清理块），JmpFalsePop
// 后继深度被污染后经回边带回循环头，每轮 +1 使 worklist 无限发散
// （编译期卡死）。修复：回边向已收敛（settled）PC 携带更大深度时丢弃。
// 本测试手搓「回边深度 > 循环头已记录深度」的形态，验证分析终止且
// 结果不受虚高回边影响（旧代码在此形态下永不返回）。

func TestComputeMaxStackBackedgeVirtualHighTerminates(t *testing.T) {
	// pc0 压条件；pc1 循环头（JmpFalsePop 弹条件，后继深度 0）；
	// 循环体 pc2..pc5 建模净 +1（pc5 的 PushInt 是虚高污染），
	// pc6 回边携带深度 2 > 循环头已记录深度 0 —— 每轮重入再 +1，
	// 修复前 worklist 永不收敛。
	code := mkCode(
		ci{op: bytecode.OpPushInt, operand: 1},        // pc0  0→1
		ci{op: bytecode.OpJmpFalsePop, operand: 0},    // pc1  header（目标稍后 patch）
		ci{op: bytecode.OpPushInt, operand: 1},        // pc2  0→1
		ci{op: bytecode.OpPushInt, operand: 1},        // pc3  1→2
		ci{op: bytecode.OpAdd, operand: 0},            // pc4  2→1
		ci{op: bytecode.OpPushInt, operand: 1},        // pc5  1→2（虚高污染）
		ci{op: bytecode.OpJmp, operand: 0},            // pc6  backedge（目标稍后 patch）
		ci{op: bytecode.OpReturnUndef, operand: 0},    // pc7  loop exit
	)
	// 有符号相对偏移 = 目标 - (源 + InstrSize)（负值经运行时 int→uint32 回绕）。
	// PatchOperand 的 pc 是字节偏移（指令边界）。
	exitPC := 7 * bytecode.InstrSize
	bytecode.PatchOperand(code, 1*bytecode.InstrSize, uint32(exitPC-(1+1)*bytecode.InstrSize)) // pc1 → pc7
	backOff := 1*bytecode.InstrSize - (6+1)*bytecode.InstrSize
	bytecode.PatchOperand(code, 6*bytecode.InstrSize, uint32(backOff)) // pc6 → pc1

	fn := &bytecode.FuncTemplate{Name: "f", Code: code, NumLocals: 0}
	mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}
	ms, err := bytecode.ComputeMaxStack(mod, fn)
	if err != nil {
		t.Fatalf("ComputeMaxStack: %v", err)
	}
	if ms != 2 {
		t.Errorf("ComputeMaxStack = %d, want 2（虚高回边应被丢弃，峰值在 pc5/pc6）", ms)
	}
}
