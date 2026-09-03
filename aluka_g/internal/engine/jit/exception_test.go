package jit

import (
	"math"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// throwTraceTemplate compiles a loop whose body unconditionally throws:
//
//	while (a < b) { a = a + 10; throw a; }
//
// The trace range [0, 32] contains the OpThrow, so CompileTrace must produce
// an exception exit carrying the thrown value.
func throwTraceTemplate() *bytecode.FuncTemplate {
	return template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpLt, 0), emit(bytecode.OpJmpFalsePop, 24),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 10),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpThrow, 0),
		emit(bytecode.OpJmp, (1<<24)-36),
		emit(bytecode.OpReturnUndef, 0),
	)
}

// TestExceptionExitCompilesAndExecutes proves that a trace containing an
// OpThrow compiles to an exception exit whose execution moves the thrown JS
// value into DeoptExit.PendingException (the R1-4 pending-exception model).
func TestExceptionExitCompilesAndExecutes(t *testing.T) {
	tmpl := throwTraceTemplate()
	trace, err := CompileTrace(tmpl, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	exits := trace.DeoptExits()
	if len(exits) != 2 {
		t.Fatalf("exits = %d, want 2 (exception + normal)", len(exits))
	}
	// The IR dump must describe the exception exit.
	dump := trace.DumpIR()
	if !strings.Contains(dump, "(exception)") {
		t.Fatalf("IR dump does not mark the exception exit:\n%s", dump)
	}
	// Execution: a=1, b=5 -> body computes 11 and throws it.
	locals := []engine.Value{engine.Undefined(), engine.Number(1), engine.Number(5)}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed {
		t.Fatalf("exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if exit.PendingException == nil {
		t.Fatal("exception exit has no PendingException")
	}
	if got, _ := exit.PendingException.Float(); got != 11 {
		t.Fatalf("PendingException = %v, want 11", exit.PendingException)
	}
	if exit.StackDepth != 0 || len(exit.StackValues) != 0 {
		t.Fatalf("exception exit should discard the operand stack, got depth=%d values=%v", exit.StackDepth, exit.StackValues)
	}
	// A normal exit (loop condition false) carries no pending exception.
	locals2 := []engine.Value{engine.Undefined(), engine.Number(5), engine.Number(1)}
	exit2, reason, err := trace.ExecuteBudgetDetailed(locals2, 0)
	if err != nil || reason != Executed {
		t.Fatalf("exit2=%+v reason=%v err=%v", exit2, reason, err)
	}
	if exit2.PendingException != nil {
		t.Fatalf("normal exit must have nil PendingException, got %v", exit2.PendingException)
	}
}

// TestSameDeoptExitPendingException verifies that SameDeoptExit compares the
// pending-exception state: nil must match nil, and values compare with the
// same semantics as trace values (numbers bitwise incl. NaN).
func TestSameDeoptExitPendingException(t *testing.T) {
	obj := engine.NewObject()
	otherObj := engine.NewObject()
	cases := []struct {
		name string
		a, b engine.Value
		want bool
	}{
		{"nil-nil", nil, nil, true},
		{"nil-value", nil, engine.Number(1), false},
		{"value-nil", engine.Number(1), nil, false},
		{"same-number", engine.Number(7), engine.Number(7), true},
		{"different-number", engine.Number(7), engine.Number(8), false},
		{"nan-nan", engine.Number(math.NaN()), engine.Number(math.NaN()), true},
		{"same-string", engine.Str("boom"), engine.Str("boom"), true},
		{"different-string", engine.Str("boom"), engine.Str("bam"), false},
		{"same-object", obj, obj, true},
		{"different-object", obj, otherObj, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := DeoptExit{ID: 0, ResumePC: 8, StackDepth: 0}
			a, b := base, base
			a.PendingException = tc.a
			b.PendingException = tc.b
			if got := SameDeoptExit(a, b); got != tc.want {
				t.Fatalf("SameDeoptExit = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestVerifyRejectsInvalidExceptionMaps covers the R1-4 verifier rules for
// exception exits: a truncated exception map and a missing thrown value are
// both malformed IR.
func TestVerifyRejectsInvalidExceptionMaps(t *testing.T) {
	tests := []struct {
		name   string
		code   []Instr
		depths []uint8
		excMap []bool
		want   string
	}{
		{
			// Truncated exception map: the map does not cover the exit.
			name:   "truncated exception map",
			code:   []Instr{{Op: OpConst, Value: 1}, {Op: OpTraceExit, Operand: 0}},
			depths: []uint8{^uint8(0)},
			excMap: []bool{},
			want:   "exception map size",
		},
		{
			// Extended exception map: unused entries are also malformed; every
			// exception-state entry must correspond to one deopt exit.
			name:   "extended exception map",
			code:   []Instr{{Op: OpConst, Value: 1}, {Op: OpTraceExit, Operand: 0}},
			depths: []uint8{^uint8(0)},
			excMap: []bool{false, true},
			want:   "exception map size",
		},
		{
			// Exception exit with no thrown value on the stack.
			name:   "exception exit stack underflow",
			code:   []Instr{{Op: OpTraceExit, Operand: 0}},
			depths: []uint8{^uint8(0)},
			excMap: []bool{true},
			want:   "stack underflow",
		},
		{
			// Valid exception exit: one value on the stack is the thrown value.
			name:   "valid exception exit",
			code:   []Instr{{Op: OpConst, Value: 1}, {Op: OpTraceExit, Operand: 0}},
			depths: []uint8{^uint8(0)},
			excMap: []bool{true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Program{Code: tc.code, traceExitDepths: tc.depths, traceExceptionExits: tc.excMap}
			err := p.Verify()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Verify = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

// TestNativeRejectsExceptionExit proves that a trace with an exception exit
// is never published as machine code: Native cannot represent the pending JS
// exception value, so Auto falls back to Quick.
func TestNativeRejectsExceptionExit(t *testing.T) {
	tmpl := throwTraceTemplate()
	trace, err := CompileTrace(tmpl, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	err = trace.CompileNative()
	if err == nil || !strings.Contains(err.Error(), "exception exit") {
		t.Fatalf("CompileNative = %v, want exception-exit rejection", err)
	}
	if trace.HasNative() {
		t.Fatal("exception-exit trace must not have native code")
	}
}
