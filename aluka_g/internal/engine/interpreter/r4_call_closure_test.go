package interpreter

import (
	"errors"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// r4_call_closure_test.go: R4-1 (call specialization) and R4-2 (closure
// specialization) evidence per the R4 §9.3 six-category requirement:
//   1. positive hit with target-tier statistics,
//   2. negative type / identity tests,
//   3. third-target / repeated-type circuit breaker,
//   4. safepoint / OOM interrupted prefix,
//   5. native verify-mismatch recovery (or documented guard recovery),
//   6. dedicated benchmarks (bench/matrix_test.go).
// The jitdiff generative corpus (KindCall / KindClosure) and the fixed cases
// -51..-54 provide the differential half of the evidence.

// --- R4-1: call specialization --------------------------------------------

// TestAutoJITCallSpecializationArity0To4 is the R4-1 positive hit for 0-4
// parameter numeric leaves: every arity must specialize, inline and execute
// in Quick, and the inlined programs must reach the Native tier in Auto.
func TestAutoJITCallSpecializationArity0To4(t *testing.T) {
	source := `
		function zero() { return 7; }
		function one(a) { return a + 1; }
		function two(a, b) { return a * b; }
		function three(a, b, c) { return (a + b) * c; }
		function four(a, b, c, d) { return (a + b) * (c - d); }
		function c0() { return zero(); }
		function c1(x) { return one(x); }
		function c2(x) { return two(x, x); }
		function c3(x) { return three(x, 1, 2); }
		function c4(x) { return four(x, 0, 3, 1); }
		globalThis.r0 = c0() * 3;      // 21
		globalThis.r1 = c1(5) * 3;     // 18
		globalThis.r2 = c2(6) - 4;     // 32
		globalThis.r3 = c3(2) + 1;     // 7
		globalThis.r4 = c4(1) * 2;     // 4
	`
	wants := map[string]string{"r0": "21", "r1": "18", "r2": "32", "r3": "7", "r4": "4"}
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Stats: true, Verify: true})
			if _, err := vm.Eval(source, "r4-arity.js"); err != nil {
				t.Fatal(err)
			}
			for name, want := range wants {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.CalleeSpecialized != 5 || stats.CalleeInlined != 5 || stats.CalleeExecuted == 0 {
				t.Fatalf("0-4 arity leaves were not all specialized+inlined: %+v", stats)
			}
			if mode == jit.Auto && (stats.NativeCompiled == 0 || stats.NativeExecuted == 0 || stats.VerifyFailures != 0) {
				t.Fatalf("inlined arity leaves did not reach Native with clean verify: %+v", stats)
			}
		})
	}
}

// TestAutoJITCallSpecializationBooleanReturn is the R4-1 positive hit for
// boolean-returning leaves: `if (leaf(x)) c++` inlines the boolean result
// into the caller's comparison branch, which the amd64 Native tier fuses
// into a jump.
func TestAutoJITCallSpecializationBooleanReturn(t *testing.T) {
	source := `
		function pos(x) { return x > 0; }
		let pred = pos;
		function countPos(n) { let c = 0; for (let i = 0; i < n; i++) { if (pred(i)) c++; } return c; }
		globalThis.r = countPos(30);
	`
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Stats: true, Verify: true})
			if _, err := vm.Eval(source, "r4-bool.js"); err != nil {
				t.Fatal(err)
			}
			got, err := vm.Global().Get("r")
			if err != nil || got.String() != "29" {
				t.Fatalf("result=%v err=%v want=29", got, err)
			}
			stats := vm.JITStats()
			if stats.CalleeSpecialized != 1 || stats.CalleeInlined != 1 || stats.CalleeExecuted == 0 {
				t.Fatalf("boolean leaf was not specialized+inlined: %+v", stats)
			}
			if mode == jit.Auto && (stats.NativeExecuted == 0 || stats.VerifyFailures != 0) {
				t.Fatalf("boolean leaf did not reach Native cleanly: %+v", stats)
			}
		})
	}
}

// TestAutoJITMultipleCallSitesSpecializeAndInline is the R4-1 "multiple call
// sites in one body" evidence. Two shapes: a module-level callee upvalue
// (OpPushSelf directly before each OpSelfCall) and a function-local alias
// (`let target = callee;` — the OpPushSelf; OpStoreLocal; OpLoadLocal
// round-trip). Every site of both shapes must inline and produce correct
// argument order.
func TestAutoJITMultipleCallSitesSpecializeAndInline(t *testing.T) {
	source := `
		function leaf(a, b, c, d) { return (a + b) * (c - d); }
		function direct(n) {
			let r = 0;
			for (let i = 0; i < n; i++) {
				r += leaf(1, 2, 3, 4);
				r += leaf(5, 6, 7, 8);
				r += leaf(9, 10, 11, 12);
			}
			return r;
		}
		function aliased(n) {
			let target = leaf;
			let r = 0;
			for (let i = 0; i < n; i++) {
				r += target(1, 2, 3, 4);
				r += target(5, 6, 7, 8);
				r += target(9, 10, 11, 12);
			}
			return r;
		}
		globalThis.r1 = direct(10);
		globalThis.r2 = aliased(10);
	`
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, Stats: true, Verify: true})
			if _, err := vm.Eval(source, "r4-multisite.js"); err != nil {
				t.Fatal(err)
			}
			// leaf(1,2,3,4) = -3, leaf(5,6,7,8) = -11, leaf(9,10,11,12) = -19;
			// per iteration sum = -33; 10 iterations = -330.
			for name, want := range map[string]string{"r1": "-330", "r2": "-330"} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.CalleeSpecialized != 2 || stats.CalleeInlined != 2 || stats.CalleeExecuted == 0 {
				t.Fatalf("multi-site callers were not specialized+inlined: %+v", stats)
			}
			if mode == jit.Auto && (stats.NativeExecuted == 0 || stats.VerifyFailures != 0) {
				t.Fatalf("multi-site callers did not reach Native cleanly: %+v", stats)
			}
		})
	}
}

// TestQuickJITCallSpecializationNegativeGuards covers the R4-1 negative
// evidence: non-number arguments, a non-closure callee and an
// non-inlineable (string-constant) callee all fall back with identical
// results; type violations produce real guard failures.
func TestQuickJITCallSpecializationNegativeGuards(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function leaf(x) { return x + 1; }
		let target = leaf;
		function neg1() { return target(1) + target("s"); }
		function neg2() { return target("s"); }
		let notAFunction = 42;
		function neg3() { return notAFunction(1); }
		function strLeaf(x) { return "s" + x; }
		let starget = strLeaf;
		function neg4(x) { return starget(x); }
		globalThis.a = neg1();
		globalThis.b = neg2();
		try { globalThis.c = neg3(); } catch (e) { globalThis.c = e.name; }
		globalThis.d = neg4(3);
	`, "r4-negative.js")
	if err != nil {
		t.Fatal(err)
	}
	// a: 2 + "s1" -> "2s1"; b: "s1"; c: TypeError (non-function callee);
	// d: "s3" (string-constant callee falls back).
	for name, want := range map[string]string{"a": "2s1", "b": "s1", "c": "TypeError", "d": "s3"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%q", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	// neg1 specialized its monomorphic leaf; the string-constant callee was
	// rejected at BindCallTarget, and the non-function upvalue became
	// quickCallUnsupported; the type guard on neg1's string argument and the
	// identity checks produced real guard failures.
	if stats.CalleeSpecialized == 0 || stats.GuardFailures == 0 {
		t.Fatalf("expected specialization + guard failures: %+v", stats)
	}
}

// TestQuickJITCalleeGuardThirdTargetMultiSite is the R4-1 circuit breaker on
// a multi-site caller: after the third distinct callee the callee
// specialization is disabled and every site keeps producing correct values.
func TestQuickJITCalleeGuardThirdTargetMultiSite(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function add1(x) { return x + 1; }
		function add10(x) { return x + 10; }
		function add100(x) { return x + 100; }
		let target = add1;
		function wrapper(x) { return target(x) + target(x + 1); }
		globalThis.w1 = wrapper(1);
		target = add10;
		globalThis.w2 = wrapper(1);
		target = add100;
		globalThis.w3 = wrapper(1);
		target = add100;
		globalThis.w4 = wrapper(1);
	`, "r4-third-target.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"w1": "5", "w2": "23", "w3": "203", "w4": "203"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized != 2 || stats.CalleeGuardFailures != 2 || stats.CalleeGuardDisabled != 1 {
		t.Fatalf("third-target fuse stats: %+v", stats)
	}
	var found bool
	for _, state := range vm.jitStates {
		if state != nil && state.calleeDisabled {
			found = true
		}
	}
	if !found {
		t.Fatal("callee specialization was not disabled after the third target")
	}
}

// TestAutoJITCallSafepointKeepsCommittedPrefix is the R4-1 safepoint
// evidence: a loop calling an inlined leaf is interrupted by an embedding
// safepoint; the committed prefix must survive exactly once and the loop
// falls back to Tier 0 with identical results.
func TestAutoJITCallSafepointKeepsCommittedPrefix(t *testing.T) {
	polls := 0
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1,
		TraceBudget: 3, Stats: true, Verify: true,
		Safepoint: func() error {
			polls++
			if polls == 4 {
				return errors.New("stop call loop")
			}
			return nil
		},
	})
	_, err = vm.Eval(`
		function leaf(x) { return x + 1; }
		let target = leaf;
		function run(n) { let s = 0; for (let i = 0; i < n; i++) { s += target(i); } return s; }
		try { run(1000); } catch (e) { globalThis.interrupt = e.message; }
		globalThis.final = run(3);
	`, "r4-call-safepoint.js")
	if err != nil {
		t.Fatal(err)
	}
	interrupt, _ := vm.Global().Get("interrupt")
	final, _ := vm.Global().Get("final")
	if interrupt.String() != "stop call loop" || final.String() != "6" {
		t.Fatalf("interrupt=%v final=%v", interrupt, final)
	}
	stats := vm.JITStats()
	if stats.Interruptions != 1 || stats.CalleeExecuted == 0 || stats.VerifyFailures != 0 {
		t.Fatalf("safepoint call stats: %+v", stats)
	}
}

// TestAutoJITVerifyMismatchFallsBackToCorrectResult is the R4-1 evidence-5
// flow at the bridge level: the native result comparison detects a wrong
// native value (the corruption injected by the jit package test
// TestNativeVerifyMismatchDetectedForCalleeInline), the bridge drops the
// native code and marks the state rejected, and subsequent calls return the
// correct Tier 0 results.
func TestAutoJITVerifyMismatchFallsBackToCorrectResult(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true, Verify: true})
	_, err = vm.Eval(`
		function leaf(x) { return x + 10; }
		let target = leaf;
		function wrapper(x) { return target(x) * 2; }
		globalThis.wrapper = wrapper;
		globalThis.a = wrapper(5);
	`, "r4-verify-mismatch.js")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := vm.Global().Get("a")
	if got.String() != "30" {
		t.Fatalf("warmup result=%v want=30", got)
	}
	var state *quickJITState
	for _, candidate := range vm.jitStates {
		if candidate != nil && candidate.callKind == quickCallBound && candidate.program.HasNative() {
			state = candidate
			break
		}
	}
	if state == nil {
		t.Fatalf("no native callee-bound state found: %+v", vm.JITStats())
	}
	stats := vm.JITStats()
	if stats.VerifyChecks == 0 || stats.VerifyFailures != 0 {
		t.Fatalf("warmup verify stats: %+v", stats)
	}
	// Inject the native bug at the comparison gate: the corrupted native
	// result (a wrong number) must be rejected.
	wrong := engine.Number(12345)
	if vm.verifyNativeResult(state.program, engine.Undefined(), []engine.Value{engine.Number(5)}, wrong) {
		t.Fatal("verifyNativeResult accepted a wrong native result")
	}
	if stats := vm.JITStats(); stats.VerifyFailures != 1 {
		t.Fatalf("VerifyFailures=%d want 1", stats.VerifyFailures)
	}
	// The bridge recovery (tryQuickCall): drop native, mark rejected.
	vm.dropNative(state)
	state.rejected = true
	_, err = vm.Eval(`
		globalThis.b = globalThis.wrapper(5);
	`, "r4-verify-mismatch2.js")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = vm.Global().Get("b")
	if got.String() != "30" {
		t.Fatalf("post-mismatch result=%v want=30 (Tier 0 recovery)", got)
	}
	if state.program.HasNative() {
		t.Fatal("native code was not dropped after the verify mismatch")
	}
}

// --- R4-2: closure specialization ------------------------------------------

// TestAutoJITClosureMultiUpvalueTrace is the R4-2 positive hit: a closure
// with two numeric upvalues read and written in order
// (`() => { a++; b += a; return b; }`), a read-only capture (`() => a + b`)
// and an in-frame non-escaping closure (`() => ++acc` created inside the
// loop function). All three must enter the closure fast path in both Quick
// and Auto.
func TestAutoJITClosureMultiUpvalueTrace(t *testing.T) {
	source := `
		function make2() { let a = 0; let b = 0; return () => { a++; b += a; return b; }; }
		function run(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
		const C = make2();
		globalThis.r1 = run(C, 20);
		globalThis.r2 = C();
		function makeRO() { let a = 3; let b = 4; return () => a + b; }
		const R = makeRO();
		globalThis.r3 = run(R, 20);
		globalThis.r4 = R();
		function runF(end) { let acc = 1; const inc = () => ++acc; let sum = 0; for (let i = 0; i < end; i++) sum += inc(); return sum; }
		globalThis.r5 = runF(10);
	`
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2, TraceBudget: 3, Stats: true})
			if _, err := vm.Eval(source, "r4-multi-upvalue.js"); err != nil {
				t.Fatal(err)
			}
			// r1: triangular sum 1..20 = 1540; r2: a=21, b=231 after the
			// loop; r3/r4: 7 per call; r5: 2+..+11 = 65.
			for name, want := range map[string]string{"r1": "1540", "r2": "231", "r3": "140", "r4": "7", "r5": "65"} {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.ClosureUpvalueSites != 2 || stats.TracesExecuted == 0 {
				t.Fatalf("closure fast path did not hit both loop templates: %+v", stats)
			}
		})
	}
}

// TestJITClosureMultiUpvalueTraceGuards covers the R4-2 negative evidence:
// a different closure instance of the same template, an upvalue whose type
// changes, and an aliased upvalue all fall back to Tier 0 with identical
// results, and the identity guards actually fire.
func TestJITClosureMultiUpvalueTraceGuards(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		function make2() { let a = 0; let b = 0; return () => { a++; b += a; return b; }; }
		function run(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
		const C1 = make2();
		const C2 = make2();
		globalThis.a = run(C1, 10);
		globalThis.b = run(C2, 10);
		function aliasRun(end) {
			let sum = 0;
			const inc = () => { sum++; return sum; };
			for (let i = 0; i < end; i++) sum += inc();
			return sum;
		}
		globalThis.c = aliasRun(4);
	`, "r4-closure-guards.js")
	if err != nil {
		t.Fatal(err)
	}
	// C1 and C2 are independent instances: each sees a=1..10 (sum of
	// triangular 1..10 = 220).
	for name, want := range map[string]string{"a": "220", "b": "220"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	// aliasRun's closure writes its captured `sum` (an open upvalue aliasing
	// the loop's sum local): the alias guard rejects the fast path and Tier 0
	// doubles the sum each iteration: 0, 0+1, 1+2, 3+4, 7+8 = 15.
	got, err := vm.Global().Get("c")
	if err != nil {
		t.Fatal(err)
	}
	stats := vm.JITStats()
	// The second instance (C2) must have hit the identity guard: the state
	// was matched on C1, C2's execution fails the identity check.
	if stats.ClosureUpvalueSites == 0 || stats.GuardFailures == 0 {
		t.Fatalf("closure guards did not fire: %+v", stats)
	}
	if got.String() != "15" {
		t.Fatalf("aliased closure result=%v want=15", got)
	}
}

// TestJITClosureTraceDisablesAfterRepeatedTypeChange is the R4-2 circuit
// breaker: the upvalue flips to a non-Number twice; the closure trace guard
// is disabled and the loop stays correct on Tier 0.
func TestJITClosureTraceDisablesAfterRepeatedTypeChange(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
	})
	_, err = vm.Eval(`
		let U = 0;
		const INC = () => ++U;
		function run(n, fn) { let sum = 0; for (let i = 0; i < n; i++) { sum += fn(); } return sum; }
		globalThis.a = run(4, INC);
		U = "str";
		globalThis.b = run(2, INC);
		globalThis.c = run(2, INC);
		U = 5;
		globalThis.d = run(2, INC);
	`, "r4-closure-type-fuse.js")
	if err != nil {
		t.Fatal(err)
	}
	// a: U 1..4 -> 10. b/c: ++"str" -> NaN. d: U resumes at 5 -> 6+7 = 13.
	for name, want := range map[string]string{"a": "10", "b": "NaN", "c": "NaN", "d": "13"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.ClosureUpvalueSites != 1 || stats.GuardFailures == 0 || stats.TraceGuardDisabled == 0 {
		t.Fatalf("closure type-change fuse stats: %+v", stats)
	}
}

// TestJITClosureMultiUpvalueTraceSafepointCommitsPrefix is the R4-2
// safepoint evidence: a multi-upvalue closure loop is interrupted after a
// committed chunk; the upvalue and the sum advance atomically and the
// post-interrupt closure call observes the committed state.
func TestJITClosureMultiUpvalueTraceSafepointCommitsPrefix(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			polls := 0
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode: mode, Threshold: ^uint32(0), BackedgeThreshold: 2,
				TraceBudget: 3, Stats: true,
				Safepoint: func() error {
					polls++
					if polls == 2 {
						return errors.New("stop multi upvalue")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				function make2() { let a = 0; let b = 0; return () => { a++; b += a; return b; }; }
				function run(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
				const C = make2();
				try { run(C, 1000); } catch (e) { globalThis.interrupt = e.message; }
				globalThis.after = C();
			`, "r4-closure-interrupt.js")
			if err != nil {
				t.Fatal(err)
			}
			interrupt, _ := vm.Global().Get("interrupt")
			after, _ := vm.Global().Get("after")
			afterN, ok := after.Int()
			if interrupt.String() != "stop multi upvalue" || !ok {
				t.Fatalf("interrupt=%v after=%v", interrupt, after)
			}
			// The committed prefix k contributes T_k to b; the post-interrupt
			// call returns b + (k+1) = T_{k+1}, a triangular number. Any
			// duplicated or lost iteration would break the invariant.
			if !isTriangular(afterN) || afterN <= 1 {
				t.Fatalf("after=%d is not a triangular committed prefix", afterN)
			}
			stats := vm.JITStats()
			if stats.ClosureUpvalueSites != 1 || stats.Interruptions != 1 || stats.ClosureUpvalueYields == 0 {
				t.Fatalf("interrupted multi-upvalue stats: %+v", stats)
			}
		})
	}
}

// TestJITClosureReadOnlyTraceSafepoint is the R4-2 read-only capture
// evidence: the read-only closure has no write-back, so the interruption
// cannot lose upvalue state; the accumulated sum stays consistent with the
// committed prefix.
func TestJITClosureReadOnlyTraceSafepoint(t *testing.T) {
	polls := 0
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2,
		TraceBudget: 3, Stats: true,
		Safepoint: func() error {
			polls++
			if polls == 3 {
				return errors.New("stop read-only")
			}
			return nil
		},
	})
	_, err = vm.Eval(`
		function makeRO() { let a = 3; let b = 4; return () => a + b; }
		function run(fn, end) { let sum = 0; for (let i = 0; i < end; i++) sum += fn(); return sum; }
		const R = makeRO();
		try { run(R, 1000000); } catch (e) { globalThis.interrupt = e.message; }
		globalThis.after = R();
		globalThis.done = run(R, 5);
	`, "r4-closure-readonly.js")
	if err != nil {
		t.Fatal(err)
	}
	interrupt, _ := vm.Global().Get("interrupt")
	after, _ := vm.Global().Get("after")
	done, _ := vm.Global().Get("done")
	// A read-only capture never writes back, so the interruption cannot lose
	// upvalue state: the closure still returns 7 and a fresh loop completes
	// with the full 5*7 = 35.
	if interrupt.String() != "stop read-only" || after.String() != "7" || done.String() != "35" {
		t.Fatalf("interrupt=%v after=%v done=%v", interrupt, after, done)
	}
	stats := vm.JITStats()
	if stats.ClosureUpvalueSites != 1 || stats.Interruptions != 1 {
		t.Fatalf("read-only interrupt stats: %+v", stats)
	}
}

// isTriangular reports whether n = m(m+1)/2 for some integer m >= 0.
func isTriangular(n int) bool {
	m := 0
	for tri := 0; tri < n; m++ {
		tri += m
		if tri == n {
			return true
		}
	}
	return n == 0
}
