package interpreter

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// runControlFlowDifferential evaluates src under off/quick/auto with an
// aggressive threshold and returns per-tier results plus the quick stats.
func runControlFlowDifferential(t *testing.T, src string) (map[jit.Mode]string, jit.Stats, jit.Stats) {
	t.Helper()
	results := make(map[jit.Mode]string)
	var quickStats, autoStats jit.Stats
	for _, mode := range []jit.Mode{jit.Off, jit.Quick, jit.Auto} {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, TraceBudget: 8, Verify: mode == jit.Auto, Stats: true})
		if _, err := vm.Eval(src, "jit-controlflow.js"); err != nil {
			vm.Close()
			t.Fatalf("mode=%s: %v", mode, err)
		}
		value, err := vm.Global().Get("r")
		if err != nil {
			vm.Close()
			t.Fatalf("mode=%s get r: %v", mode, err)
		}
		results[mode] = value.String()
		stats := vm.JITStats()
		if mode == jit.Quick {
			quickStats = stats
		}
		if mode == jit.Auto {
			autoStats = stats
		}
		vm.Close()
	}
	return results, quickStats, autoStats
}

// TestQuickJITTernaryDifferential covers the R3-6 ternary leaf: `a ? b : c`
// must compile and execute in Quick/Auto with Tier 0 as the oracle, including
// falsy tests (0, NaN, "", null, undefined) that force the alternate path.
func TestQuickJITTernaryDifferential(t *testing.T) {
	results, quick, auto := runControlFlowDifferential(t, `
		function f(a, b, c) { return a ? b : c; }
		globalThis.r = f(1, 2, 3) + "|" + f(0, 2, 3) + "|" + f(NaN, 2, 3) + "|" +
			f("", 2, 3) + "|" + f(null, 2, 3) + "|" + f(undefined, 2, 3) + "|" + f(-0, 2, 3) + "|" +
			f(true, "x", "y") + "|" + f(7n, 2, 3);
	`)
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		if results[mode] != results[jit.Off] {
			t.Fatalf("ternary tier mismatch: off=%s %s=%s", results[jit.Off], mode, results[mode])
		}
	}
	if quick.Compiled != 1 || quick.Executed == 0 || quick.Rejected != 0 {
		t.Fatalf("quick ternary stats: %+v", quick)
	}
	if auto.Executed == 0 && auto.NativeExecuted == 0 {
		t.Fatalf("auto ternary did not execute: %+v", auto)
	}
}

// TestQuickJITSwitchDifferential covers integer and string switch leaves
// (strict-equality jump chains) in all tiers; string case tests exercise the
// OpConstString pool.
func TestQuickJITSwitchDifferential(t *testing.T) {
	results, quick, _ := runControlFlowDifferential(t, `
		function f(x) { switch (x) { case 1: return 10; case 2: return 20; default: return 30; } }
		function g(x) { switch (x) { case "a": return 1; case "b": return 2; default: return 3; } }
		globalThis.r = f(1) + "|" + f(2) + "|" + f(9) + "|" + f(0) + "|" + f(-0) + "|" + f(NaN) + "|" +
			g("a") + "|" + g("b") + "|" + g("z") + "|" + g(1) + "|" + g("ab");
	`)
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		if results[mode] != results[jit.Off] {
			t.Fatalf("switch tier mismatch: off=%s %s=%s", results[jit.Off], mode, results[mode])
		}
	}
	if quick.Compiled != 2 || quick.Executed == 0 || quick.Rejected != 0 {
		t.Fatalf("quick switch stats: %+v", quick)
	}
}

// TestQuickJITNestedShortCircuitDifferential covers `a && b || c && d` and
// `(a || b) && c` multi-level keep-branch chains, including value-preserving
// short-circuit semantics with mixed operand types.
func TestQuickJITNestedShortCircuitDifferential(t *testing.T) {
	results, quick, _ := runControlFlowDifferential(t, `
		function f(a, b, c, d) { return a && b || c && d; }
		function g(a, b, c) { return (a || b) && c; }
		function h(a, b) { return a && (b || 3); }
		globalThis.r = f(1, 2, 0, 4) + "|" + f(0, 2, 3, 4) + "|" + f(1, 0, 3, 0) + "|" + f(0, 2, 0, 4) + "|" +
			g(0, 2, 3) + "|" + g(1, 0, 0) + "|" + g(0, 0, 3) + "|" + g(NaN, 1, 7) + "|" +
			h(1, 0) + "|" + h(0, 0) + "|" + h(0, 5) + "|" + f("", 2, 3, 4) + "|" + f(1, "x", 0, "y");
	`)
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		if results[mode] != results[jit.Off] {
			t.Fatalf("short-circuit tier mismatch: off=%s %s=%s", results[jit.Off], mode, results[mode])
		}
	}
	if quick.Compiled != 3 || quick.Executed == 0 || quick.Rejected != 0 {
		t.Fatalf("quick short-circuit stats: %+v", quick)
	}
}

// TestQuickJITControlFlowLoopTraces covers switch / ternary / nested
// short-circuit INSIDE loop bodies: the loop trace must compile with
// out-of-range deopt exits and stay identical to Tier 0.
func TestQuickJITControlFlowLoopTraces(t *testing.T) {
	results, quick, auto := runControlFlowDifferential(t, `
		function sw(a, n) { let s = 0; for (let i = 0; i < n; i++) { switch (i % 3) { case 0: s += a; break; case 1: s += a * 2; break; default: s += a * 3; } } return s; }
		function tern(a, n) { let s = 0; for (let i = 0; i < n; i++) { s += i % 2 ? a : a + 1; } return s; }
		function sc(a, b, n) { let s = 0; for (let i = 0; i < n; i++) { if (a && b || !a) { s += i; } } return s; }
		globalThis.r = sw(10, 7) + "|" + sw(-3, 11) + "|" + tern(5, 6) + "|" + tern(0, 9) + "|" + sc(1, 1, 8) + "|" + sc(0, 1, 8) + "|" + sc(1, 0, 8);
	`)
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		if results[mode] != results[jit.Off] {
			t.Fatalf("loop-trace tier mismatch: off=%s %s=%s", results[jit.Off], mode, results[mode])
		}
	}
	if quick.TracesCompiled == 0 && quick.Compiled == 0 {
		t.Fatalf("quick did not compile any control-flow loop: %+v", quick)
	}
	if auto.Executed == 0 && auto.TracesExecuted == 0 && auto.NativeExecuted == 0 {
		t.Fatalf("auto did not execute the control-flow loops: %+v", auto)
	}
}

// TestQuickJITStringSwitchNativeFallback proves the string-const leaf keeps
// working in Auto when Native rejects the string pool: NativeRejected grows,
// the Quick tier executes, and the result matches Tier 0.
func TestQuickJITStringSwitchNativeFallback(t *testing.T) {
	results, _, auto := runControlFlowDifferential(t, `
		function g(x) { switch (x) { case "a": return 1; case "b": return 2; default: return 3; } }
		globalThis.r = g("a") + "|" + g("b") + "|" + g("zz");
	`)
	if results[jit.Auto] != results[jit.Off] {
		t.Fatalf("string-switch auto mismatch: off=%s auto=%s", results[jit.Off], results[jit.Auto])
	}
	if auto.NativeRejected == 0 || auto.Executed == 0 {
		t.Fatalf("string-switch auto should fall back to Quick after Native rejection: %+v", auto)
	}
}

// TestRejectionCacheSkipsRepeatedLeafCompiles is the R3-7 leaf gate: a loop
// whose body contains a try/catch region is rejected once; every later
// backedge is served by the structured rejection cache (RejectionCacheHits
// grows) and Candidates / Rejected never grow again. The loop's trace is
// unaffected (the try region is outside the loop body) and keeps executing.
func TestRejectionCacheSkipsRepeatedLeafCompiles(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, BackedgeThreshold: 1, Stats: true})
	_, err = vm.Eval(`
		function f(n) { let s = 0; try { for (let i = 0; i < n; i++) { s += i; } } catch (e) { s = -1; } return s; }
		globalThis.r = 0;
		for (let k = 0; k < 50; k++) globalThis.r += f(100);
	`, "jit-rejection-cache-leaf.js")
	if err != nil {
		t.Fatal(err)
	}
	stats := vm.JITStats()
	// f is rejected once (try region); the top-level program is rejected once
	// (not a leaf candidate) and its trace is rejected once (LOAD_GLOBAL in
	// the range). Nothing recompiles on later backedges.
	if stats.Candidates != 2 || stats.Rejected != 2 || stats.Compiled != 0 || stats.TracesRejected != 1 {
		t.Fatalf("leaf rejection was not cached after one attempt: %+v", stats)
	}
	if stats.RejectionCacheHits == 0 {
		t.Fatalf("no rejection-cache hits despite repeated backedges/calls: %+v", stats)
	}
	var found bool
	for _, state := range vm.jitStates {
		if state != nil && state.rejected && state.reason == "jit: unsupported opcode TRY_ENTER" {
			found = true
		}
	}
	if !found {
		t.Fatal("no leaf state rejected with the try-region reason")
	}
	var traceRejected bool
	for _, state := range vm.jitTraces {
		if state != nil && state.rejected && state.reason != "" {
			traceRejected = true
		}
	}
	if !traceRejected {
		t.Fatal("no rejected trace state recorded")
	}
	if len(stats.RejectionReasons) != 3 || stats.RejectionReasons[0].Tier != "quick" ||
		stats.RejectionReasons[0].Reason != "jit: function is not a leaf candidate" {
		t.Fatalf("rejection reasons=%+v", stats.RejectionReasons)
	}
}

// TestRejectionCacheSkipsRepeatedTraceCompiles is the R3-7 trace gate: a loop
// containing an unguarded user call (Math.abs) is rejected at trace compile
// once; subsequent backedges hit the (template, backedge) rejection cache and
// the trace compiler never runs again.
func TestRejectionCacheSkipsRepeatedTraceCompiles(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, BackedgeThreshold: 1, Stats: true})
	_, err = vm.Eval(`
		function f(n) { let s = 0; for (let i = 0; i < n; i++) { s += Math.abs(i); } return s; }
		globalThis.r = f(30);
	`, "jit-rejection-cache-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("r")
	if err != nil || got.String() != "435" {
		t.Fatalf("result=%v err=%v", got, err)
	}
	stats := vm.JITStats()
	if stats.TracesRejected != 1 || stats.TracesCompiled != 0 || stats.Candidates != 1 || stats.Rejected != 1 {
		t.Fatalf("trace rejection was not cached after one attempt: %+v", stats)
	}
	if stats.RejectionCacheHits == 0 {
		t.Fatalf("no rejection-cache hits despite repeated backedges: %+v", stats)
	}
	for _, state := range vm.jitTraces {
		if state != nil && state.rejected {
			if state.reason == "" {
				t.Fatal("trace rejection has no recorded reason")
			}
			return
		}
	}
	t.Fatal("no rejected trace state recorded")
}

// TestRejectionCacheReasonTextStable asserts the R3-7 pre-filter reason text
// is what lands in the per-state cache and the aggregate stats, so the
// observable rejection is stable across repeated attempts.
func TestRejectionCacheReasonTextStable(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function f(x) { return x.a; }
		function g(x) { return arguments[0] + 1; }
		globalThis.r = f({ a: 1 });
		for (let k = 0; k < 5; k++) globalThis.r = g(k);
	`, "jit-rejection-reason-stable.js")
	if err != nil {
		t.Fatal(err)
	}
	stats := vm.JITStats()
	if stats.Rejected != 1 {
		t.Fatalf("rejected=%d, want exactly the arguments rejection: %+v", stats.Rejected, stats)
	}
	for _, reason := range stats.RejectionReasons {
		if reason.Tier == "quick" && reason.Reason != "jit: function is not a leaf candidate" {
			t.Fatalf("unexpected quick rejection reason: %+v", reason)
		}
		if strings.Contains(reason.Reason, "unsupported") && reason.Reason == "jit: unsupported opcode LOAD_ARGUMENTS" {
			t.Fatalf("arguments rejection should use the leaf-candidate reason, got %q", reason.Reason)
		}
	}
}
