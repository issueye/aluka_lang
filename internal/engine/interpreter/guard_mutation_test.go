package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// TestMethodCallICInvalidatesOnDelete is the Tier 0 regression for the R1-6
// guard-mutation discovery: the VM method-call inline cache (O1-C4) did not
// check the deleted map, so deleting an own method after warmup kept
// returning the deleted closure instead of resolving the prototype chain.
func TestMethodCallICInvalidatesOnDelete(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	_, err = vm.Eval(`
		const PROTO = { getV() { return 7; } };
		const P = { _a: 2, getV() { return this._a; } };
		Object.setPrototypeOf(P, PROTO);
		globalThis.before = P.getV();   // warms up the per-PC method IC
		delete P.getV;
		globalThis.after = P.getV();    // must resolve through the prototype
	`, "method-ic-delete.js")
	if err != nil {
		t.Fatal(err)
	}
	val := func(name string) string {
		v, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return v.String()
	}
	if got := val("before"); got != "2" {
		t.Fatalf("before delete = %s, want 2", got)
	}
	if got := val("after"); got != "7" {
		t.Fatalf("after delete = %s, want 7 (prototype method; IC must not return the deleted own method)", got)
	}
}

// TestGuardMutationThirdShapeDisablesNativeReleasesRX drives the R1-6
// third-shape fixed scenario (-24) in Auto mode: the two-way property PIC
// absorbs the first two shapes, the third shape's guard failures disable the
// Native trace, the RX code is released (NativeCodeBytes back to zero) and
// the global executable-memory accounting returns to the pre-test baseline
// after the VM closes.
func TestGuardMutationThirdShapeDisablesNativeReleasesRX(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, Stats: true})
	_, err = vm.Eval(`
		function kS(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.a; } return s; }
		const S1 = { a: 1, b: 2 };
		const S2 = { a: 3, c: 4 };
		const S3 = { a: 5, d: 6, e: 7 };
		globalThis.R1 = kS(S1, 4);
		globalThis.R2 = kS(S2, 4);
		globalThis.R3 = kS(S3, 4);
		globalThis.R3b = kS(S3, 4);   // second third-shape call reaches the disable threshold
		globalThis.R4 = kS(S1, 4);
	`, "guard-mutation-shape3.js")
	if err != nil {
		t.Fatal(err)
	}
	val := func(name string) string {
		v, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return v.String()
	}
	if val("R1") != "4" || val("R2") != "12" || val("R3") != "20" || val("R4") != "4" {
		t.Fatalf("results R1=%s R2=%s R3=%s R4=%s, want 4/12/20/4", val("R1"), val("R2"), val("R3"), val("R4"))
	}
	stats := vm.JITStats()
	if stats.NativeCompiled == 0 && stats.NativeTracesCompiled == 0 {
		t.Fatalf("warmup shapes did not execute any native code: %+v", stats)
	}
	if stats.NativeGuardDisabled == 0 && stats.NativeTraceGuardDisabled == 0 {
		t.Fatalf("third shape did not disable the native version: %+v", stats)
	}
	if stats.NativeCodeBytes != 0 {
		t.Fatalf("disabled native trace code was not released: %+v", stats)
	}
	if stats.GuardFailures < 2 {
		t.Fatalf("third shape produced only %d guard failures, want >= 2", stats.GuardFailures)
	}
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("executable memory not back to baseline: regions %d->%d bytes %d->%d", baseRegions, regions, baseBytes, bytes)
	}
}

// TestGuardMutationThirdTargetDisablesNativeReleasesRX drives the R1-6
// third-target callee scenario (-26) in Auto mode: the callee PIC absorbs two
// targets, the third target's guard failures disable the callee
// specialization, the Native code is released and the executable-memory
// accounting returns to the baseline after the VM closes. Tier 0 keeps
// producing the right results for every callee.
func TestGuardMutationThirdTargetDisablesNativeReleasesRX(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, Stats: true})
	_, err = vm.Eval(`
		function makeRun(fn) { return function(n) { let s = 0; for (let i = 0; i < n; i++) { s += fn(i); } return s; }; }
		function leafA(x) { return x + 1; }
		function leafB(x) { return x * 10; }
		function leafC(x) { return x - 5; }
		const R = makeRun(leafA);
		globalThis.R1 = R(6);
		const R2 = makeRun(leafB);
		globalThis.R2v = R2(6);
		const R3 = makeRun(leafC);
		globalThis.R3v = R3(6);
		globalThis.R3w = R3(6);   // second third-target call reaches the disable threshold
		globalThis.R4 = R(6);
	`, "guard-mutation-callee3.js")
	if err != nil {
		t.Fatal(err)
	}
	val := func(name string) string {
		v, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return v.String()
	}
	if val("R1") != "21" || val("R2v") != "150" || val("R3v") != "-15" || val("R3w") != "-15" || val("R4") != "21" {
		t.Fatalf("results R1=%s R2v=%s R3v=%s R3w=%s R4=%s, want 21/150/-15/-15/21", val("R1"), val("R2v"), val("R3v"), val("R3w"), val("R4"))
	}
	stats := vm.JITStats()
	if stats.CalleeSpecialized == 0 {
		t.Fatalf("callee specialization never engaged: %+v", stats)
	}
	if stats.CalleeGuardDisabled == 0 || stats.CalleeGuardFailures < 2 {
		t.Fatalf("third callee target did not disable the callee guard after repeated failures: %+v", stats)
	}
	disabledStates := 0
	for _, state := range vm.jitStates {
		if state == nil || !state.calleeDisabled {
			continue
		}
		disabledStates++
		if jitStateHasNative(state) {
			t.Fatal("callee-disabled state still owns native code")
		}
	}
	if disabledStates == 0 {
		t.Fatal("callee guard disable counter has no matching disabled JIT state")
	}
	// Remaining NativeCodeBytes may belong to independent leaf-function
	// specializations. The disabled state itself must already be released;
	// VM.Close below verifies all remaining executable memory is reclaimed.
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("executable memory not back to baseline: regions %d->%d bytes %d->%d", baseRegions, regions, baseBytes, bytes)
	}
}
