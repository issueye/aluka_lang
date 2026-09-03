package jit

import (
	"math"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// r4_call_test.go: R4-1 unit coverage for the inlineCallTarget site analysis
// (multiple call sites, callee routed through a local, boolean-returning
// leaves) and the verify-mismatch recovery evidence.

func mustBindCallTarget(t *testing.T, caller, callee *Program) bool {
	t.Helper()
	callee.SelfUpvalue = -1 // a plain leaf callee requires no self upvalue
	if _, err := caller.BindCallTarget(callee); err != nil {
		t.Fatalf("BindCallTarget: %v", err)
	}
	return callerHasNoSelfCall(t, caller)
}

func callerHasNoSelfCall(t *testing.T, p *Program) bool {
	t.Helper()
	for _, in := range p.Code {
		if in.Op == OpSelfCall {
			return false
		}
	}
	return true
}

// TestInlineCallTargetMultipleCallSitesDirectUpvalue covers the Pattern A
// shape: the bytecode compiler emits OpPushSelf directly before every
// OpSelfCall (module-level `let target = callee;` with the callee kept as an
// upvalue). Every site must inline.
func TestInlineCallTargetMultipleCallSitesDirectUpvalue(t *testing.T) {
	// caller: return f(x, 2) + f(x, 3) with f(a, b) = a * b
	caller := &Program{NumLocals: 2, SelfUpvalue: 0}
	caller.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 2},
		{Op: OpSelfCall, Operand: 2},
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 3},
		{Op: OpSelfCall, Operand: 2},
		{Op: OpAdd},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 2}
	callee.Code = []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpLoadLocal, Operand: 2},
		{Op: OpMul},
		{Op: OpReturn},
	}
	if !mustBindCallTarget(t, caller, callee) {
		t.Fatal("multiple direct-upvalue sites were not inlined")
	}
	// x=3: 3*2 + 3*3 = 15
	result, reason, err := caller.Execute(engine.Undefined(), []engine.Value{engine.Number(3)})
	if err != nil || reason != Executed || result.String() != "15" {
		t.Fatalf("result=%v reason=%v err=%v (want 15)", result, reason, err)
	}
}

// TestInlineCallTargetLocalStoredCalleeMultipleSites covers the Pattern B
// shape (R4-1 extension): `let target = callee; ...; target(a); target(b);`
// compiles to a single OpPushSelf; OpStoreLocal X at the entry and an
// OpLoadLocal X before each OpSelfCall. The store, the loads and the sites
// are dead together and must all be inlined.
func TestInlineCallTargetLocalStoredCalleeMultipleSites(t *testing.T) {
	caller := &Program{NumLocals: 5, SelfUpvalue: 0}
	caller.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpStoreLocal, Operand: 4},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpLoadLocal, Operand: 4},
		{Op: OpConst, Value: 2},
		{Op: OpSelfCall, Operand: 1},
		{Op: OpAdd},
		{Op: OpLoadLocal, Operand: 4},
		{Op: OpConst, Value: 3},
		{Op: OpSelfCall, Operand: 1},
		{Op: OpMul},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 1}
	callee.Code = []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 10},
		{Op: OpAdd},
		{Op: OpReturn},
	}
	if !mustBindCallTarget(t, caller, callee) {
		t.Fatal("local-stored callee sites were not inlined")
	}
	// The caller computes (x + (arg1+10)) * (arg2+10) with args 2 then 3:
	// (1 + 12) * 13 = 169.
	result, reason, err := caller.Execute(engine.Undefined(), []engine.Value{engine.Number(1)})
	if err != nil || reason != Executed || result.String() != "169" {
		t.Fatalf("result=%v reason=%v err=%v (want 169)", result, reason, err)
	}
	// The dropped instructions must be gone: no self pushes, no stores of the
	// callee local, no loads feeding the sites.
	for i, in := range caller.Code {
		if in.Op == OpPushSelf || in.Op == OpStoreLocal && in.Operand == 4 || in.Op == OpLoadLocal && in.Operand == 4 {
			t.Fatalf("stale instruction at %d: %v", i, in)
		}
	}
}

// TestInlineCallTargetBooleanReturningCallee covers a leaf whose body
// produces a Boolean (`return a < b`). The inlined result is a quickBoolean
// that flows into the caller's comparison branch; the trailing OpReturn of
// the callee is not inlined so the value stays on the stack.
func TestInlineCallTargetBooleanReturningCallee(t *testing.T) {
	// caller: if (f(x)) return 100; return 200; with f(x) = x < 3
	caller := &Program{NumLocals: 2, SelfUpvalue: 0}
	caller.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpSelfCall, Operand: 1},
		{Op: OpJumpFalse, Operand: 6},
		{Op: OpConst, Value: 100},
		{Op: OpReturn},
		{Op: OpConst, Value: 200},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 1}
	callee.Code = []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 3},
		{Op: OpLt},
		{Op: OpReturn},
	}
	if !mustBindCallTarget(t, caller, callee) {
		t.Fatal("boolean-returning callee was not inlined")
	}
	for _, tc := range []struct {
		arg  float64
		want string
	}{
		{1, "100"}, // 1 < 3 -> true
		{9, "200"}, // 9 < 3 -> false
	} {
		result, reason, err := caller.Execute(engine.Undefined(), []engine.Value{engine.Number(tc.arg)})
		if err != nil || reason != Executed || result.String() != tc.want {
			t.Fatalf("arg=%v result=%v reason=%v err=%v want=%s", tc.arg, result, reason, err, tc.want)
		}
	}
}

// TestInlineCallTargetRejectsNonSelfCalleeSources locks the conservative
// fallbacks: a callee that comes from a parameter, from a reassigned local,
// or from a nested call must not be inlined (it stays on the guarded
// non-inlined callTarget path or falls back to Tier 0).
func TestInlineCallTargetRejectsNonSelfCalleeSources(t *testing.T) {
	callee := &Program{NumParams: 1}
	callee.Code = []Instr{{Op: OpLoadLocal, Operand: 1}, {Op: OpConst, Value: 1}, {Op: OpAdd}, {Op: OpReturn}}

	t.Run("callee from parameter", func(t *testing.T) {
		caller := &Program{NumLocals: 4, SelfUpvalue: -1}
		caller.Code = []Instr{
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpConst, Value: 1},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpReturn},
		}
		if caller.inlineCallTarget(callee) {
			t.Fatal("parameter callee was inlined")
		}
	})
	t.Run("callee from reassigned local", func(t *testing.T) {
		caller := &Program{NumLocals: 4, SelfUpvalue: 0}
		caller.Code = []Instr{
			{Op: OpPushSelf},
			{Op: OpStoreLocal, Operand: 3},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpStoreLocal, Operand: 3}, // reassigned from a non-self source
			{Op: OpLoadLocal, Operand: 3},
			{Op: OpConst, Value: 1},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpReturn},
		}
		if caller.inlineCallTarget(callee) {
			t.Fatal("reassigned-local callee was inlined")
		}
	})
	t.Run("callee from nested call", func(t *testing.T) {
		caller := &Program{NumLocals: 2, SelfUpvalue: 0}
		caller.Code = []Instr{
			{Op: OpPushSelf},
			{Op: OpConst, Value: 1},
			{Op: OpSelfCall, Operand: 1}, // inner call result is the callee
			{Op: OpConst, Value: 1},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpReturn},
		}
		if caller.inlineCallTarget(callee) {
			t.Fatal("nested-call callee was inlined")
		}
	})
	t.Run("selfLocal used as argument", func(t *testing.T) {
		caller := &Program{NumLocals: 4, SelfUpvalue: 0}
		caller.Code = []Instr{
			{Op: OpPushSelf},
			{Op: OpStoreLocal, Operand: 3},
			{Op: OpLoadLocal, Operand: 3}, // callee
			{Op: OpLoadLocal, Operand: 3}, // also an argument
			{Op: OpSelfCall, Operand: 1},
			{Op: OpReturn},
		}
		if caller.inlineCallTarget(callee) {
			t.Fatal("selfLocal-argument callee was inlined")
		}
	})
	t.Run("argument count mismatch", func(t *testing.T) {
		caller := &Program{NumLocals: 2, SelfUpvalue: 0}
		caller.Code = []Instr{
			{Op: OpPushSelf},
			{Op: OpConst, Value: 1},
			{Op: OpConst, Value: 2},
			{Op: OpSelfCall, Operand: 2},
			{Op: OpReturn},
		}
		if caller.inlineCallTarget(callee) {
			t.Fatal("arity mismatch was inlined")
		}
	})
}

// TestInlineCallTargetExpressionArgs verifies that argument expressions with
// arithmetic (`target(x + 1, x * 2)`) inline: the backward depth walk must
// accept multi-instruction argument regions.
func TestInlineCallTargetExpressionArgs(t *testing.T) {
	caller := &Program{NumLocals: 2, SelfUpvalue: 0}
	caller.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 1},
		{Op: OpAdd},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 2},
		{Op: OpMul},
		{Op: OpSelfCall, Operand: 2},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 2}
	callee.Code = []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpLoadLocal, Operand: 2},
		{Op: OpSub},
		{Op: OpReturn},
	}
	if !mustBindCallTarget(t, caller, callee) {
		t.Fatal("expression-arg callee was not inlined")
	}
	// (x+1) - (x*2); x=20 -> 21 - 40 = -19.
	result, reason, err := caller.Execute(engine.Undefined(), []engine.Value{engine.Number(20)})
	if err != nil || reason != Executed || result.String() != "-19" {
		t.Fatalf("result=%v reason=%v err=%v (want -19)", result, reason, err)
	}
}

// TestInlineCallTargetZeroArgCallee covers `() => const` leaves (R4-1 arity
// 0): the site has no argument region and the inlined body is a constant.
func TestInlineCallTargetZeroArgCallee(t *testing.T) {
	caller := &Program{NumLocals: 1, SelfUpvalue: 0}
	caller.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpSelfCall, Operand: 0},
		{Op: OpConst, Value: 5},
		{Op: OpAdd},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 0}
	callee.Code = []Instr{
		{Op: OpConst, Value: 37},
		{Op: OpReturn},
	}
	if !mustBindCallTarget(t, caller, callee) {
		t.Fatal("zero-arg callee was not inlined")
	}
	result, reason, err := caller.Execute(engine.Undefined(), nil)
	if err != nil || reason != Executed || result.String() != "42" {
		t.Fatalf("result=%v reason=%v err=%v (want 42)", result, reason, err)
	}
}

// TestNativeVerifyMismatchDetectedForCalleeInline is the R4-1 evidence-5
// injection: the machine code of a callee-inlined program is compiled, then
// its input plan is corrupted (the same style as the R1-5 property-write
// injection) so the native execution produces a wrong result. The Quick
// executor still produces the correct value, and verifyNativeResult-style
// comparison detects the mismatch. Auto then drops the native code and
// resumes with the correct result (covered end-to-end by the interpreter
// test TestAutoJITVerifyMismatchFallsBackToCorrectResult).
func TestNativeVerifyMismatchDetectedForCalleeInline(t *testing.T) {
	caller := &Program{NumParams: 1, NumLocals: 2, SelfUpvalue: 0}
	caller.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpSelfCall, Operand: 1},
		{Op: OpConst, Value: 2},
		{Op: OpMul},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 1, SelfUpvalue: -1}
	callee.Code = []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 10},
		{Op: OpAdd},
		{Op: OpReturn},
	}
	if inlined, err := caller.BindCallTarget(callee); err != nil || !inlined {
		t.Fatalf("BindCallTarget inlined=%v err=%v", inlined, err)
	}
	if !callerHasNoSelfCall(t, caller) {
		t.Fatal("callee was not inlined")
	}
	if err := caller.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	args := []engine.Value{engine.Number(5)}
	quick, reason, err := caller.Execute(engine.Undefined(), args)
	if err != nil || reason != Executed || quick.String() != "30" {
		t.Fatalf("quick result=%v reason=%v err=%v (want (5+10)*2=30)", quick, reason, err)
	}
	native, nativeReason, _, nativeErr := caller.ExecuteNativeBudgetWithSafepoint(engine.Undefined(), args, 65536, nil)
	if nativeErr != nil || nativeReason != Executed || native.String() != "30" {
		t.Fatalf("native result=%v reason=%v err=%v (want 30)", native, nativeReason, nativeErr)
	}
	// Inject the native bug: drop the numeric-arg mapping after compilation,
	// so the frame slot the machine code reads is never filled.
	caller.nativePlan.numberArgs &^= uint16(1)
	native, nativeReason, _, nativeErr = caller.ExecuteNativeBudgetWithSafepoint(engine.Undefined(), args, 65536, nil)
	if nativeErr != nil || nativeReason != Executed {
		t.Fatalf("corrupted native execution: reason=%v err=%v", nativeReason, nativeErr)
	}
	if math.Float64bits(mustFloat(native)) == math.Float64bits(mustFloat(quick)) {
		t.Fatalf("corrupted native result %v equals the Quick result %v; injection did not diverge", native, quick)
	}
	// The bridge (tryQuickCall) drops the native code and marks the state
	// rejected on this mismatch; the RX release and Tier 0 recovery are
	// asserted end-to-end by TestAutoJITVerifyMismatchFallsBackToCorrectResult.
}

func mustFloat(v engine.Value) float64 {
	f, _ := v.Float()
	return f
}
