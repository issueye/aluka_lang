// trace tier 的 upvalue 读写：编译准入、缓存/提交语义与 guard 拒绝路径。

package jit

import (
	"math"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// fakeCell 是测试用的 TraceUpvalueCell：持一个可被断言的 float64，可配置为
// 非数值（loadFails）或拒绝写入（storeFails），用于驱动 guard 与回滚路径。
type fakeCell struct {
	value      float64
	loadFails  bool
	storeFails bool
	loads      int
	stores     int
}

func (c *fakeCell) LoadNumber() (float64, bool) {
	c.loads++
	if c.loadFails {
		return 0, false
	}
	return c.value, true
}

func (c *fakeCell) StoreNumber(number float64) bool {
	c.stores++
	if c.storeFails {
		return false
	}
	c.value = number
	return true
}

// upvalueLoopTemplate lowers `for (i = 0; i < UB; i++) UA += i` where UB is
// upvalue 0 (read-only bound) and UA is upvalue 1 (accumulator):
//
//	 0  LOAD_LOCAL 1        ; i
//	 4  LOAD_UPVALUE 0      ; UB
//	 8  LT
//	12  JMP_FALSE_POP -> 48 ; loop exit (out of trace range)
//	16  LOAD_UPVALUE 1      ; UA
//	20  LOAD_LOCAL 1        ; i
//	24  ADD
//	28  STORE_UPVALUE 1     ; UA += i
//	32  LOAD_LOCAL 1
//	36  INC
//	40  STORE_LOCAL 1       ; i++
//	44  JMP -> 0            ; backedge  (range is [0, 44])
//	48  LOAD_LOCAL 1
//	52  RETURN
func upvalueLoopTemplate() *bytecode.FuncTemplate {
	tmpl := controlTemplate(0, 2,
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadUpvalue, 0), emit(bytecode.OpLt, 0),
		emitSigned(bytecode.OpJmpFalsePop, 12, 48),
		emit(bytecode.OpLoadUpvalue, 1), emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpAdd, 0),
		emit(bytecode.OpStoreUpvalue, 1),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpInc, 0), emit(bytecode.OpStoreLocal, 1),
		emitSigned(bytecode.OpJmp, 44, 0),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpReturn, 0),
	)
	tmpl.Upvalues = []bytecode.UpvalueCapture{{IsLocal: true, Index: 0}, {IsLocal: true, Index: 1}}
	return tmpl
}

// TestTraceUpvalueRoundTrip pins the core contract: the loop reads the bound
// from a cell every iteration, accumulates into a second cell, and the
// accumulator is committed exactly once at the semantic exit with Tier 0's
// value (sum of 0..bound-1).
func TestTraceUpvalueRoundTrip(t *testing.T) {
	bound := &fakeCell{value: 6}
	acc := &fakeCell{value: 0}
	trace, err := CompileTraceWithUpvalues(upvalueLoopTemplate(), 0, 44, nil, nil,
		[]TraceUpvalueGuard{{Index: 0, Cell: bound}, {Index: 1, Cell: acc}})
	if err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0)}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed {
		t.Fatalf("reason=%v err=%v", reason, err)
	}
	if exit.ResumePC != 48 {
		t.Fatalf("exit=%+v, want ResumePC=48", exit)
	}
	if acc.value != 15 { // 0+1+2+3+4+5
		t.Fatalf("acc=%v, want 15", acc.value)
	}
	if locals[1].String() != "6" {
		t.Fatalf("i=%v, want 6", locals[1])
	}
	// The read-only bound must never be written back.
	if bound.stores != 0 {
		t.Fatalf("read-only bound was stored %d times", bound.stores)
	}
	// Cells are touched at entry and commit, not per iteration.
	if acc.stores != 1 {
		t.Fatalf("accumulator stores=%d, want exactly one commit", acc.stores)
	}
}

// TestTraceUpvalueUnguardedRejected proves the range is still rejected when the
// bridge cannot supply a guard for every touched cell — the pre-existing
// behavior for code the trace tier must not speculate on.
func TestTraceUpvalueUnguardedRejected(t *testing.T) {
	tmpl := upvalueLoopTemplate()
	for _, guards := range [][]TraceUpvalueGuard{
		nil,
		{{Index: 0, Cell: &fakeCell{}}}, // missing the accumulator
		{{Index: 1, Cell: &fakeCell{}}}, // missing the bound
		{{Index: 0, Cell: &fakeCell{}}, {Index: 1}}, // nil cell
		{{Index: 5, Cell: &fakeCell{}}},             // out of range index
	} {
		if _, err := CompileTraceWithUpvalues(tmpl, 0, 44, nil, nil, guards); err == nil {
			t.Fatalf("guards=%+v compiled, want rejection", guards)
		}
		if err := RejectTraceReasonWithUpvalues(tmpl, 0, 44, guards); err == nil {
			// The candidate scan must agree with the compiler: a range the
			// compiler rejects must not be admitted by the cheap pre-filter.
			if len(guards) == 0 || guards[0].Index != 5 {
				t.Fatalf("guards=%+v passed the scan, want rejection", guards)
			}
		}
	}
	if err := RejectTraceReasonWithUpvalues(tmpl, 0, 44, []TraceUpvalueGuard{
		{Index: 0, Cell: &fakeCell{}}, {Index: 1, Cell: &fakeCell{}},
	}); err != nil {
		t.Fatalf("fully guarded range rejected: %v", err)
	}
}

// TestTraceUpvalueDuplicateGuardRejected pins that a duplicated index is a
// malformed guard set rather than silently using the last one.
func TestTraceUpvalueDuplicateGuardRejected(t *testing.T) {
	_, err := CompileTraceWithUpvalues(upvalueLoopTemplate(), 0, 44, nil, nil,
		[]TraceUpvalueGuard{{Index: 0, Cell: &fakeCell{}}, {Index: 0, Cell: &fakeCell{}}, {Index: 1, Cell: &fakeCell{}}})
	if err == nil {
		t.Fatal("duplicate upvalue guard compiled, want rejection")
	}
}

// TestTraceUpvalueNonNumberEntryGuard proves a cell holding a non-Number fails
// the entry guard before anything is mutated.
func TestTraceUpvalueNonNumberEntryGuard(t *testing.T) {
	bound := &fakeCell{value: 6}
	acc := &fakeCell{value: 3, loadFails: true}
	trace, err := CompileTraceWithUpvalues(upvalueLoopTemplate(), 0, 44, nil, nil,
		[]TraceUpvalueGuard{{Index: 0, Cell: bound}, {Index: 1, Cell: acc}})
	if err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0)}
	_, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != GuardFailed {
		t.Fatalf("reason=%v err=%v, want GuardFailed", reason, err)
	}
	if acc.stores != 0 || locals[1].String() != "0" {
		t.Fatalf("failed entry guard mutated state: acc=%+v i=%v", acc, locals[1])
	}
}

// TestTraceUpvalueCommitFailureLeavesNoPartialState drives the commit-time
// rollback: the store is refused, so the trace must report GuardFailed with
// neither the cell nor the locals updated (Tier 0 replays the slice).
func TestTraceUpvalueCommitFailureLeavesNoPartialState(t *testing.T) {
	bound := &fakeCell{value: 4}
	acc := &fakeCell{value: 0, storeFails: true}
	trace, err := CompileTraceWithUpvalues(upvalueLoopTemplate(), 0, 44, nil, nil,
		[]TraceUpvalueGuard{{Index: 0, Cell: bound}, {Index: 1, Cell: acc}})
	if err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0)}
	_, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != GuardFailed {
		t.Fatalf("reason=%v err=%v, want GuardFailed", reason, err)
	}
	if acc.value != 0 {
		t.Fatalf("acc=%v, want the original 0 (no partial commit)", acc.value)
	}
	if locals[1].String() != "0" {
		t.Fatalf("i=%v, want the original 0 (locals commit is part of the batch)", locals[1])
	}
}

// TestTraceUpvalueBudgetYieldCommits pins that a budget yield publishes the
// partial accumulator and that resuming continues from it, so the final value
// after repeated yields equals the single-pass result.
func TestTraceUpvalueBudgetYieldCommits(t *testing.T) {
	bound := &fakeCell{value: 10}
	acc := &fakeCell{value: 0}
	trace, err := CompileTraceWithUpvalues(upvalueLoopTemplate(), 0, 44, nil, nil,
		[]TraceUpvalueGuard{{Index: 0, Cell: bound}, {Index: 1, Cell: acc}})
	if err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0)}
	for round := 0; round < 20; round++ {
		exit, reason, err := trace.ExecuteBudgetDetailed(locals, 2)
		if err != nil {
			t.Fatal(err)
		}
		if reason == Executed {
			if exit.ResumePC != 48 {
				t.Fatalf("exit=%+v, want ResumePC=48", exit)
			}
			break
		}
		if reason != Yielded {
			t.Fatalf("reason=%v, want Yielded or Executed", reason)
		}
		// Every yield must have published a consistent (i, acc) pair.
		i, _ := locals[1].Float()
		if want := i * (i - 1) / 2; acc.value != want {
			t.Fatalf("after yield i=%v acc=%v, want %v", i, acc.value, want)
		}
	}
	if acc.value != 45 { // 0..9
		t.Fatalf("acc=%v, want 45", acc.value)
	}
}

// TestTraceUpvalueNativeLowering documents that upvalue traces do reach the
// Native tier: the cells live in the plan (Go side) while the frame only
// holds their float64, so lowering must succeed and the committed value must
// match the Quick tier.
func TestTraceUpvalueNativeLowering(t *testing.T) {
	bound := &fakeCell{value: 8}
	acc := &fakeCell{value: 0}
	trace, err := CompileTraceWithUpvalues(upvalueLoopTemplate(), 0, 44, nil, nil,
		[]TraceUpvalueGuard{{Index: 0, Cell: bound}, {Index: 1, Cell: acc}})
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.CompileNative(); err != nil {
		t.Skipf("native trace unavailable on this platform: %v", err)
	}
	defer trace.Close()
	if !trace.HasSideEffectWrites() {
		t.Fatal("an upvalue-writing trace must report side-effect writes")
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0)}
	exit, reason, _, err := trace.ExecuteNativeBudgetDetailed(locals, 0)
	if err != nil || reason != Executed {
		t.Fatalf("reason=%v err=%v", reason, err)
	}
	if exit.ResumePC != 48 {
		t.Fatalf("exit=%+v, want ResumePC=48", exit)
	}
	if acc.value != 28 { // 0..7
		t.Fatalf("native acc=%v, want 28", acc.value)
	}
	if math.Float64bits(acc.value) != math.Float64bits(28) {
		t.Fatalf("native acc bits differ from the exact value")
	}
}

// TestTraceUpvalueVerifierRejectsOutsideTrace pins that the two IR ops are
// trace-only: a function-level program has no commit points for them.
func TestTraceUpvalueVerifierRejectsOutsideTrace(t *testing.T) {
	for _, op := range []Op{OpLoadUpvalueNum, OpStoreUpvalueNum} {
		p := &Program{NumParams: 0, NumLocals: 1, SelfUpvalue: -1, Code: []Instr{
			{Op: OpConst, Value: 1}, {Op: op}, {Op: OpReturn},
		}}
		if err := p.Verify(); err == nil {
			t.Fatalf("%v verified in a function program, want rejection", op)
		}
	}
}

// TestTraceUpvalueVerifierRejectsMissingCell pins the operand bound check: a
// trace program whose op references an absent cell is malformed IR.
func TestTraceUpvalueVerifierRejectsMissingCell(t *testing.T) {
	p := &Program{
		NumParams: 0, NumLocals: 1, SelfUpvalue: -1,
		traceExitDepths:     []uint8{^uint8(0)},
		traceExceptionExits: []bool{false},
		Code: []Instr{
			{Op: OpLoadUpvalueNum, Operand: 0}, {Op: OpPop}, {Op: OpTraceExit, Operand: 0},
		},
	}
	if err := p.Verify(); err == nil {
		t.Fatal("load_upvalue_f64 with no recorded cell verified, want rejection")
	}
}
