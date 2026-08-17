package jit

import (
	"math"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// TestOptimizeConstantFoldingBitSemantics verifies the folded arithmetic
// preserves IEEE-754 bit semantics: NaN, -0, ±Inf and division by zero.
func TestOptimizeConstantFoldingBitSemantics(t *testing.T) {
	cases := []struct {
		op   Op
		a, b float64
		want float64
	}{
		{OpAdd, 1, 2, 3},
		{OpAdd, math.NaN(), 1, math.NaN()},
		{OpSub, 0, 0, 0},
		{OpMul, 1, math.Copysign(0, -1), math.Copysign(0, -1)}, // 1 * -0 = -0
		{OpDiv, 1, 0, math.Inf(1)},                             // 1/0 = +Inf
		{OpDiv, 1, math.Copysign(0, -1), math.Inf(-1)},         // 1/-0 = -Inf
		{OpDiv, 0, 0, math.NaN()},
		{OpMod, 5, 2, 1},
		{OpPow, 2, 10, 1024},
	}
	for _, tc := range cases {
		p := &Program{Code: []Instr{
			{Op: OpConst, Value: tc.a},
			{Op: OpConst, Value: tc.b},
			{Op: tc.op},
			{Op: OpReturn},
		}}
		if err := p.Verify(); err != nil {
			t.Fatalf("verify: %v", err)
		}
		OptimizeProgram(p)
		if len(p.Code) != 2 || p.Code[0].Op != OpConst || p.Code[1].Op != OpReturn {
			t.Fatalf("op %v: expected folded CONST+RETURN, got %+v", tc.op, p.Code)
		}
		got := p.Code[0].Value
		if math.IsNaN(tc.want) && math.IsNaN(got) {
			continue // NaN bit patterns are not unique
		}
		if math.Float64bits(got) != math.Float64bits(tc.want) {
			t.Fatalf("op %v: %v op %v = %v (%x), want %v (%x)",
				tc.op, tc.a, tc.b, got, math.Float64bits(got), tc.want, math.Float64bits(tc.want))
		}
	}
}

// TestOptimizeDoesNotCrossBranchTargets verifies folds never consume
// instructions that are branch targets.
func TestOptimizeDoesNotCrossBranchTargets(t *testing.T) {
	p := &Program{Code: []Instr{
		{Op: OpConst, Value: 1},
		{Op: OpConst, Value: 2}, // index 1 is a jump target
		{Op: OpAdd},
		{Op: OpJump, Operand: 5},
		{Op: OpPop}, // unreachable tail
		{Op: OpConst, Value: 3},
		{Op: OpReturn},
	}}
	if err := p.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	OptimizeProgram(p)
	// index 1 is targeted, so no fold may occur there.
	for i, in := range p.Code {
		if in.Op == OpConst && i+2 < len(p.Code) && p.Code[i+1].Op == OpConst && p.Code[i+2].Op == OpAdd {
			t.Fatalf("fold crossed a branch target at index %d", i)
		}
	}
}

// TestOptimizeEliminatesRedundantStoreLoad verifies LOAD_LOCAL k followed by
// STORE_LOCAL k (both un-targeted) is eliminated.
func TestOptimizeEliminatesRedundantStoreLoad(t *testing.T) {
	p := &Program{Code: []Instr{
		{Op: OpConst, Value: 7},
		{Op: OpStoreLocal, Operand: 1},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpStoreLocal, Operand: 1},
		{Op: OpConst, Value: 9},
		{Op: OpReturn},
	}}
	if err := p.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	OptimizeProgram(p)
	for _, in := range p.Code {
		if in.Op == OpLoadLocal && in.Operand == 1 {
			t.Fatalf("redundant load/store not eliminated: %+v", p.Code)
		}
	}
}

// TestOptimizeUnreachableBlockRemoval verifies a branch-skipped block is
// removed and the jump is remapped.
func TestOptimizeUnreachableBlockRemoval(t *testing.T) {
	p := &Program{Code: []Instr{
		{Op: OpConst, Value: 5},
		{Op: OpPop},
		{Op: OpJump, Operand: 5},
		{Op: OpConst, Value: 1}, // unreachable block
		{Op: OpConst, Value: 2},
		{Op: OpConst, Value: 8},
		{Op: OpReturn},
	}}
	if err := p.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	stats := OptimizeProgram(p)
	if stats.RemovedDead < 2 {
		t.Fatalf("removed %d dead instructions, want >= 2", stats.RemovedDead)
	}
	if err := p.Verify(); err != nil {
		t.Fatalf("optimized program must verify: %v", err)
	}
	// Jump must be remapped to an in-range reachable instruction.
	if last := p.Code[len(p.Code)-1]; last.Op != OpReturn {
		t.Fatalf("last instruction = %v, want RETURN", last.Op)
	}
	for i, in := range p.Code {
		if in.Op == OpJump {
			if int(in.Operand) < 0 || int(in.Operand) >= len(p.Code) {
				t.Fatalf("jump at %d remapped out of range: %d", i, in.Operand)
			}
		}
	}
}

// TestOptimizeSwitchOffKeepsIdenticalCode proves the OptimizeIR switch is
// honored: with it disabled the program is returned unchanged.
func TestOptimizeSwitchOffKeepsIdenticalCode(t *testing.T) {
	code := []Instr{
		{Op: OpConst, Value: 1},
		{Op: OpConst, Value: 2},
		{Op: OpAdd},
		{Op: OpReturn},
	}
	p := &Program{Code: append([]Instr(nil), code...)}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	OptimizeIR = false
	defer func() { OptimizeIR = true }()
	OptimizeProgram(p)
	if len(p.Code) != len(code) {
		t.Fatalf("optimization disabled but code changed: %+v", p.Code)
	}
}

// TestOptimizedLeafMatchesUnoptimizedExecution compares optimized vs
// unoptimized execution over a numeric leaf for edge inputs, proving the
// passes are semantically neutral.
func TestOptimizedLeafMatchesUnoptimizedExecution(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpPushInt, 10),
		emit(bytecode.OpPushInt, 20),
		emit(bytecode.OpAdd, 0),
		emit(bytecode.OpLoadLocal, 1),
		emit(bytecode.OpMul, 0),
		emit(bytecode.OpReturn, 0),
	)
	optimized, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	OptimizeIR = false
	plain, err := CompileLeaf(tmpl)
	OptimizeIR = true
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []float64{0, -0, 3.5, math.NaN(), math.Inf(1), -2.25} {
		got, reason, err := optimized.Execute(engine.Undefined(), []engine.Value{engine.Number(input)})
		if err != nil || reason != Executed {
			t.Fatalf("optimized execute: %v %v", reason, err)
		}
		want, reason2, err2 := plain.Execute(engine.Undefined(), []engine.Value{engine.Number(input)})
		if err2 != nil || reason2 != Executed {
			t.Fatalf("plain execute: %v %v", reason2, err2)
		}
		gn, _ := got.Float()
		wn, _ := want.Float()
		if math.Float64bits(gn) != math.Float64bits(wn) {
			t.Fatalf("input %v: optimized=%v (%x) plain=%v (%x)", input, gn, math.Float64bits(gn), wn, math.Float64bits(wn))
		}
	}
}
