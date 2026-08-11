package jit

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// sideEffectTraceTemplate builds a property-write loop:
//
//	for (i = 0; i < n; i++) { o.x = i; s += o.x; }
//
// with locals [this, i, n, o, s]. The trace range [0, 64] ends at the
// backedge; the loop-condition exit lands outside the range at pc 68.
func sideEffectTraceTemplate() *bytecode.FuncTemplate {
	var code []byte
	add := func(ops ...[]byte) {
		for _, op := range ops {
			code = append(code, op...)
		}
	}
	add(
		emit(bytecode.OpLoadLocal, 1),    // pc  0: i
		emit(bytecode.OpLoadLocal, 2),    // pc  4: n
		emit(bytecode.OpLt, 0),           // pc  8
		emit(bytecode.OpJmpFalsePop, 52), // pc 12: exit -> pc 68
		emit(bytecode.OpLoadLocal, 1),    // pc 16: value (i)
		emit(bytecode.OpLoadLocal, 3),    // pc 20: object (o) on top
		emit(bytecode.OpSetPropTop, 0),   // pc 24: o.x = i
		emit(bytecode.OpLoadLocal, 4),    // pc 28: s
		emit(bytecode.OpLoadLocal, 3),    // pc 32: o
		emit(bytecode.OpGetProp, 0),      // pc 36: o.x
		emit(bytecode.OpAdd, 0),          // pc 40
		emit(bytecode.OpStoreLocal, 4),   // pc 44: s += o.x
		emit(bytecode.OpLoadLocal, 1),    // pc 48: i
		emit(bytecode.OpPushInt, 1),      // pc 52
		emit(bytecode.OpAdd, 0),          // pc 56
		emit(bytecode.OpStoreLocal, 1),   // pc 60: i++
		emit(bytecode.OpJmp, (1<<24)-68), // pc 64: backedge -> pc 0
		emit(bytecode.OpReturnUndef, 0),  // pc 68
	)
	return &bytecode.FuncTemplate{
		NumParams: 4, NumLocals: 5, ArgumentsSlot: 5, NoArgumentsObject: true,
		Constants: []engine.Value{engine.Str("x")},
		Code:      code,
	}
}

// TestVerifyRejectsSideEffectsWithoutTraceProtocol covers the R1-5 verifier
// rules: side-effecting ops and trace-only protocol ops must be rejected in
// function-level programs, and trace guard indices must reference a recorded
// guard. Malformed side-effect state must be rejected explicitly, not left to
// a runtime fallback.
func TestVerifyRejectsSideEffectsWithoutTraceProtocol(t *testing.T) {
	tests := []struct {
		name string
		code []Instr
		want string
	}{
		{
			name: "set_prop in function program",
			code: []Instr{
				{Op: OpConst, Value: 1}, {Op: OpLoadLocal, Operand: 0},
				{Op: OpSetProp, Name: "x"}, {Op: OpReturnUndef},
			},
			want: "side effect set_prop requires a trace program",
		},
		{
			name: "guard_noop_call in function program",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 0}, {Op: OpGuardNoopCall, Operand: 0},
				{Op: OpReturnUndef},
			},
			want: "guard_noop_call requires a trace program",
		},
		{
			name: "guard_method_get in function program",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 0}, {Op: OpGuardMethodGet, Operand: 0},
				{Op: OpReturnUndef},
			},
			want: "guard_method_get requires a trace program",
		},
		{
			name: "guard_noop_call missing call guard",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 0}, {Op: OpGuardNoopCall, Operand: 0},
				{Op: OpTraceExit, Operand: 0},
			},
			want: "missing call guard",
		},
		{
			name: "guard_method_get missing method guard",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 0}, {Op: OpGuardMethodGet, Operand: 0},
				{Op: OpTraceExit, Operand: 0},
			},
			want: "missing method guard",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Program{Code: tc.code, traceExitDepths: nil}
			if tc.code[len(tc.code)-1].Op == OpTraceExit {
				p.traceExitDepths = []uint8{0}
			}
			err := p.Verify()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify = %v, want error containing %q", err, tc.want)
			}
		})
	}
	// Control: a valid trace program with a side effect and a recorded call
	// guard passes verification.
	valid := &Program{
		NumLocals: 1, Code: []Instr{
			{Op: OpConst, Value: 1}, {Op: OpLoadLocal, Operand: 0},
			{Op: OpSetProp, Name: "x"}, {Op: OpTraceExit, Operand: 0},
		},
		traceExitDepths: []uint8{0},
	}
	if err := valid.Verify(); err != nil {
		t.Fatalf("valid side-effecting trace rejected: %v", err)
	}
}

// TestTraceCommitProtocolAppliesWritesExactlyOnce drives the two-phase commit
// protocol across budget slices: every slice commits its completed iterations
// exactly once, the deferred property write lands once, and the loop-condition
// exit resumes at the recorded bytecode boundary.
func TestTraceCommitProtocolAppliesWritesExactlyOnce(t *testing.T) {
	tmpl := sideEffectTraceTemplate()
	trace, err := CompileTrace(tmpl, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	obj := engine.NewObject()
	if err := obj.Set("x", engine.Number(-1)); err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0), engine.Number(4), obj, engine.Number(0)}
	// Slice 1: iterations 0 and 1 commit o.x = 1, s = 1, i = 2.
	exit, reason, err := trace.ExecuteBudgetDetailedWithSafepoint(locals, 2, nil)
	if err != nil || reason != Yielded {
		t.Fatalf("slice 1: exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if value, err := obj.Get("x"); err != nil || value != engine.Number(1) {
		t.Fatalf("after slice 1 o.x = %v (err %v), want 1 (committed once)", value, err)
	}
	if i, _ := locals[1].Float(); i != 2 {
		t.Fatalf("after slice 1 i = %v, want 2", i)
	}
	if s, _ := locals[4].Float(); s != 1 {
		t.Fatalf("after slice 1 s = %v, want 1", s)
	}
	// Slice 2: iterations 2 and 3 commit o.x = 3, s = 6, i = 4.
	exit, reason, err = trace.ExecuteBudgetDetailedWithSafepoint(locals, 2, nil)
	if err != nil || reason != Yielded {
		t.Fatalf("slice 2: exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if value, err := obj.Get("x"); err != nil || value != engine.Number(3) {
		t.Fatalf("after slice 2 o.x = %v (err %v), want 3 (no repeated write)", value, err)
	}
	if s, _ := locals[4].Float(); s != 6 {
		t.Fatalf("after slice 2 s = %v, want 6", s)
	}
	// Slice 3: the loop condition fails and the trace exits at pc 68.
	exit, reason, err = trace.ExecuteBudgetDetailedWithSafepoint(locals, 2, nil)
	if err != nil || reason != Executed {
		t.Fatalf("slice 3: exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if exit.ResumePC != 68 {
		t.Fatalf("slice 3 resume PC = %d, want 68", exit.ResumePC)
	}
	if value, err := obj.Get("x"); err != nil || value != engine.Number(3) {
		t.Fatalf("final o.x = %v (err %v), want 3", value, err)
	}
}

// TestTraceGuardFailureAfterCommittedSliceNoPartialWrite proves that a guard
// failure after a committed slice discards only the failing slice: the
// object retains the committed writes plus whatever the poll observed, the
// locals keep the last committed values, and no partial write leaks.
func TestTraceGuardFailureAfterCommittedSliceNoPartialWrite(t *testing.T) {
	tmpl := sideEffectTraceTemplate()
	trace, err := CompileTrace(tmpl, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	obj := engine.NewObject()
	if err := obj.Set("x", engine.Number(-1)); err != nil {
		t.Fatal(err)
	}
	poisoned := false
	var committedBeforePoll engine.Value
	poll := func() error {
		if !poisoned {
			poisoned = true
			// The poll runs after slice 1's commit: record the committed value,
			// then mutate the object so the property is no longer a Number. The
			// next slice's write guard must fail before any write of that
			// failing slice.
			var err error
			committedBeforePoll, err = obj.Get("x")
			if err != nil {
				return err
			}
			return obj.Set("x", engine.Str("poison"))
		}
		return nil
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0), engine.Number(4), obj, engine.Number(0)}
	exit, reason, err := trace.ExecuteBudgetDetailedWithSafepoint(locals, 2, poll)
	if err != nil || reason != Yielded {
		t.Fatalf("slice 1: exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if committedBeforePoll != engine.Number(1) {
		t.Fatalf("slice 1 committed o.x = %v, want 1 (observed at the poll)", committedBeforePoll)
	}
	// Slice 2 starts with the poisoned property: the write guard fails at the
	// prepare point and nothing of the failing slice is committed.
	exit, reason, err = trace.ExecuteBudgetDetailedWithSafepoint(locals, 2, poll)
	if err != nil || reason != GuardFailed {
		t.Fatalf("slice 2: exit=%+v reason=%v err=%v, want GuardFailed", exit, reason, err)
	}
	got, err := obj.Get("x")
	if err != nil || got != engine.Str("poison") {
		t.Fatalf("o.x = %v (err %v), want poison (failing slice wrote nothing)", got, err)
	}
	if i, _ := locals[1].Float(); i != 2 {
		t.Fatalf("i = %v, want 2 (last committed slice)", i)
	}
	if s, _ := locals[4].Float(); s != 1 {
		t.Fatalf("s = %v, want 1 (last committed slice)", s)
	}
}
