package interpreter

import (
	"errors"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// runSideEffectTier evaluates source in one tier and returns the values of the
// given globals plus the JIT stats. traceBudget 0 uses the executor default
// (no budget yields); a small budget forces commit-per-slice behavior. A
// non-nil safepoint is installed on every tier (off included, via
// InterpreterSafepoints) so interruption semantics are identical across tiers.
func runSideEffectTier(t *testing.T, mode jit.Mode, source string, globals []string, traceBudget uint32, safepoint jit.Safepoint) (map[string]string, jit.Stats) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	config := jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, Stats: true, TraceBudget: traceBudget}
	if safepoint != nil {
		config.Safepoint = safepoint
		config.InterpreterSafepoints = true
	}
	vm.ConfigureJIT(config)
	if _, err := vm.Eval(source, "side-effect-deopt.js"); err != nil {
		t.Fatalf("mode=%s: %v", mode, err)
	}
	values := make(map[string]string, len(globals))
	for _, name := range globals {
		value, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("mode=%s get %s: %v", mode, name, err)
		}
		values[name] = value.String()
	}
	return values, vm.JITStats()
}

// cancelAfter returns a safepoint callback that fails with message on the
// n-th poll (1-based), simulating an embedding cancellation.
func cancelAfter(n int, message string) jit.Safepoint {
	polls := 0
	return func() error {
		polls++
		if polls >= n {
			return errors.New(message)
		}
		return nil
	}
}

// checkSideEffectScenario runs a scenario in off/quick/auto, asserts the
// results match the oracle, and requires the JIT to have exercised the given
// protocol path (trace execution, yield or guard failure) so the tests prove
// the side-effect state went through the deopt/commit machinery, not just
// Tier 0. newSafepoint is called once per tier so the poll counters never
// leak across tiers.
func checkSideEffectScenario(t *testing.T, source string, globals []string, want map[string]string, traceBudget uint32, newSafepoint func() jit.Safepoint, wantQuickTrace, wantAutoTrace bool) {
	t.Helper()
	var safepointFor = func() jit.Safepoint {
		if newSafepoint == nil {
			return nil
		}
		return newSafepoint()
	}
	off, _ := runSideEffectTier(t, jit.Off, source, globals, traceBudget, safepointFor())
	quick, quickStats := runSideEffectTier(t, jit.Quick, source, globals, traceBudget, safepointFor())
	auto, autoStats := runSideEffectTier(t, jit.Auto, source, globals, traceBudget, safepointFor())
	for _, name := range globals {
		if off[name] != want[name] {
			t.Fatalf("%s off = %q, want %q", name, off[name], want[name])
		}
		if quick[name] != off[name] || auto[name] != off[name] {
			t.Fatalf("%s: off=%q quick=%q auto=%q", name, off[name], quick[name], auto[name])
		}
	}
	traceRan := func(s jit.Stats) bool {
		return s.TracesCompiled > 0 && (s.TracesExecuted > 0 || s.TraceYields > 0 || s.NativeTracesExecuted > 0)
	}
	if wantQuickTrace && !traceRan(quickStats) {
		t.Fatalf("quick did not execute the trace (side-effect path not exercised): %+v", quickStats)
	}
	if wantAutoTrace && !traceRan(autoStats) {
		t.Fatalf("auto did not execute the trace (side-effect path not exercised): %+v", autoStats)
	}
}

// TestDeoptPropertyWriteCommitBeforeException proves the commit-before-throw
// ordering of the two-phase protocol: the deferred property write of the
// throwing iteration lands before the pending exception reaches the catch, so
// the catch observes the committed value in every tier.
func TestDeoptPropertyWriteCommitBeforeException(t *testing.T) {
	source := `
function k1(n, o) {
  let s = 0;
  try {
    for (let i = 0; i < n; i++) {
      o.a = i;
      if (i < 2) { s += 1; continue; }
      throw i * 100;
    }
  } catch (e) {
    s += 1000;
    globalThis.ER = "caught:" + typeof e + ":" + e;
  }
  return s;
}
const O = { a: -1 };
globalThis.E1 = k1(100, O);
globalThis.OA = O.a;
`
	checkSideEffectScenario(t, source, []string{"E1", "ER", "OA"}, map[string]string{
		"E1": "1002",              // 2 committed prefix + 1000 catch
		"ER": "caught:number:200", // i=2 throws 200
		"OA": "2",                 // o.a = 2 committed before the throw
	}, 0, nil, true, true)
}

// TestDeoptCallGuardFailureNoPartialWrite replaces a noop callee with a
// late-throwing callee after warmup: the second invocation enters the trace
// (the throw is delayed past the first backedge), the trace's callee-identity
// guard fails before the call, the pending property write of that iteration is
// discarded (no partial write), Tier 0 replays the iteration, and the user
// call throws into the existing catch. The event log must be identical in
// every tier.
func TestDeoptCallGuardFailureNoPartialWrite(t *testing.T) {
	source := `
function NOOP() {}
let THROW_COUNT = 0;
function LATE_THROWER() { THROW_COUNT++; if (THROW_COUNT >= 2) throw new Error("call-boom"); }
function kY(n, o, cb) { for (let i = 0; i < n; i++) { o.a = i; cb(); } return o.a; }
const O = { a: -1 };
const EV = [];
try { EV.push("r1:" + kY(5, O, NOOP)); } catch (e) { EV.push("e1:" + e.name); }
try { EV.push("r2:" + kY(3, O, LATE_THROWER)); } catch (e) { EV.push("e2:" + e.name + ":" + e.message); }
EV.push("post:" + O.a);
globalThis.LOGS = EV.join("|");
`
	off, offStats := runSideEffectTier(t, jit.Off, source, []string{"LOGS"}, 0, nil)
	if off["LOGS"] != "r1:4|e2:Error:call-boom|post:1" {
		t.Fatalf("off LOGS = %q", off["LOGS"])
	}
	quick, quickStats := runSideEffectTier(t, jit.Quick, source, []string{"LOGS"}, 0, nil)
	auto, autoStats := runSideEffectTier(t, jit.Auto, source, []string{"LOGS"}, 0, nil)
	if quick["LOGS"] != off["LOGS"] || auto["LOGS"] != off["LOGS"] {
		t.Fatalf("LOGS: off=%q quick=%q auto=%q", off["LOGS"], quick["LOGS"], auto["LOGS"])
	}
	// The warmup ran in the trace (noop call guard) and the swap fired the
	// guard mid-trace, before any partial write of the failing iteration.
	if quickStats.TracesCompiled == 0 || quickStats.TracesExecuted == 0 {
		t.Fatalf("quick did not execute the trace: %+v", quickStats)
	}
	if autoStats.TracesCompiled == 0 || (autoStats.TracesExecuted == 0 && autoStats.NativeTracesExecuted == 0) {
		t.Fatalf("auto did not execute the trace: %+v", autoStats)
	}
	if quickStats.GuardFailures == 0 {
		t.Fatalf("quick has no guard failure for the callee swap: %+v", quickStats)
	}
	if autoStats.GuardFailures == 0 {
		t.Fatalf("auto has no guard failure for the callee swap: %+v", autoStats)
	}
	if offStats.GuardFailures != 0 {
		t.Fatalf("off must not record guard failures: %+v", offStats)
	}
}

// TestDeoptArrayPushInterruptNoDuplicateNoLoss cancels a push loop mid-JIT:
// every committed chunk must land exactly once (consecutive elements, no
// duplicates, no loss) and the interruption must enter the same catch in all
// tiers.
func TestDeoptArrayPushInterruptNoDuplicateNoLoss(t *testing.T) {
	source := `
function kA(arr, n) { for (let i = 0; i < n; i++) arr.push(i); return arr.length; }
const A = [];
try { globalThis.R = kA(A, 1000000); } catch (e) { globalThis.ERR = e.name + ":" + e.message; }
globalThis.OK = A.length > 0 && A.length === A[A.length - 1] + 1;
`
	checkSideEffectScenario(t, source, []string{"ERR", "OK"}, map[string]string{
		"OK":  "true",
		"ERR": "Error:cancel-append",
	}, 1, func() jit.Safepoint { return cancelAfter(5, "cancel-append") }, true, true)
}

// TestDeoptUpvalueWriteAtomicOnInterrupt cancels a numeric-upvalue closure
// loop: the upvalue and the sum are written back atomically per chunk, so the
// invariant sum == N*(N+1)/2 (every committed iteration contributed exactly
// once) must hold in every tier after the interruption.
func TestDeoptUpvalueWriteAtomicOnInterrupt(t *testing.T) {
	source := `
let N = 0;
const INC = () => ++N;
function kR(n, fn) {
  let sum = 0;
  try {
    for (let i = 0; i < n; i++) { sum += fn(); }
  } catch (e) { globalThis.ERR = e.name + ":" + e.message; }
  return sum;
}
globalThis.R = kR(1000000, INC);
globalThis.OK = N > 0 && R === N * (N + 1) / 2;
`
	checkSideEffectScenario(t, source, []string{"ERR", "OK"}, map[string]string{
		"OK":  "true",
		"ERR": "Error:cancel-upvalue",
	}, 1, func() jit.Safepoint { return cancelAfter(5, "cancel-upvalue") }, true, true)
}
