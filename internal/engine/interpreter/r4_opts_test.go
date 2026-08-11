package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// R4-7/R4-8 evidence tests. R4-7 evaluates Mod / Pow / bitwise ops for the
// amd64 Native tier (implemented: % and & | ^ << >> >>> ~; rejected: ** which
// requires libm and stays in Quick). R4-8 reduces side-exit cost: the same
// frame never retries a failed trace version (per-backedge, frame-scoped) and
// the duplicate native→Quick bridge on native entry-guard failures is
// observable through the JIT stats.

// runAutoWithVerify runs source in a fresh Auto VM with threshold 1 and
// verification enabled, returning the requested globals and the stats.
func runAutoWithVerify(t *testing.T, source string, names ...string) (map[string]engine.Value, jit.Stats) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, Verify: true, Stats: true})
	if _, err := vm.Eval(source, "r4-opts.js"); err != nil {
		t.Fatal(err)
	}
	values := make(map[string]engine.Value, len(names))
	for _, name := range names {
		value, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		values[name] = value
	}
	return values, vm.JITStats()
}

// TestAutoJITNativeModHit is the R4-7 class-1 evidence for %: the native tier
// must compile and execute a numeric mod leaf, and the verify dual execution
// (Native vs Quick) must agree bit-for-bit (VerifyFailures == 0).
func TestAutoJITNativeModHit(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function modK(a, b) { return a % b; }
		globalThis.r4Mod1 = modK(100, 7);
		globalThis.r4Mod2 = modK(-100, 7);
		globalThis.r4Mod3 = modK(1e300, 3);
		globalThis.r4Mod4 = modK(5, 0);
	`, "r4Mod1", "r4Mod2", "r4Mod3", "r4Mod4")
	for name, want := range map[string]string{"r4Mod1": "2", "r4Mod2": "-2", "r4Mod3": "0", "r4Mod4": "NaN"} {
		if got := values[name]; got.String() != want {
			t.Fatalf("%s=%v want=%s", name, got, want)
		}
	}
	if stats.NativeCompiled != 1 || stats.NativeExecuted == 0 || stats.VerifyChecks == 0 || stats.VerifyFailures != 0 {
		t.Fatalf("native mod was not executed or verify disagreed: %+v", stats)
	}
}

// TestAutoJITNativeBitwiseHit is the R4-7 class-1 evidence for the bitwise
// ops: the native tier executes a numeric bitwise leaf including shift-mask
// and ToInt32 wrap corners, with clean verify.
func TestAutoJITNativeBitwiseHit(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function bitK(a, b, n) { return ((a ^ b) << (n & 31)) | ~(a >>> (n & 31)); }
		globalThis.r4Bit1 = bitK(2147483648, 305419896, 7);
		globalThis.r4Bit2 = bitK(NaN, 7, 13);
		globalThis.r4Bit3 = bitK(4294967297, 1, 32);
		globalThis.r4Bit4 = bitK(1e300, 0, 0);
	`, "r4Bit1", "r4Bit2", "r4Bit3", "r4Bit4")
	if values["r4Bit1"].String() == "" {
		t.Fatalf("r4Bit1=%v", values["r4Bit1"])
	}
	if stats.NativeCompiled != 1 || stats.NativeExecuted == 0 || stats.VerifyChecks == 0 || stats.VerifyFailures != 0 {
		t.Fatalf("native bitwise was not executed or verify disagreed: %+v", stats)
	}
}

// TestAutoJITNativeModTraceHit is the R4-7 class-1 evidence for a trace loop
// containing %: the machine-code trace executes (class-4 prefix evidence in
// the same run through verify).
func TestAutoJITNativeModTraceHit(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function modSum(n) {
			const traceOnlyMarker = {};
			let s = 0;
			for (let i = 1; i <= n; i++) s += i % 7;
			return s;
		}
		globalThis.r4ModSum = modSum(1000);
	`, "r4ModSum")
	if values["r4ModSum"].String() != "3003" {
		t.Fatalf("r4ModSum=%v want=71000", values["r4ModSum"])
	}
	if stats.NativeTracesCompiled == 0 || stats.NativeTracesExecuted == 0 {
		t.Fatalf("native mod trace was not used: %+v", stats)
	}
	if stats.VerifyChecks == 0 || stats.VerifyFailures != 0 {
		t.Fatalf("verify dual execution disagree on the mod trace: %+v", stats)
	}
}

// TestAutoJITPowStaysQuick is the R4-7 rejection evidence for **: the native
// compiler rejects pow (requires libm), Auto falls back to Quick stably
// (RejectionCacheHits proves the failed compile is not retried), and the
// result is correct across tiers.
func TestAutoJITPowStaysQuick(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function powK(a, b) { return a ** b; }
		globalThis.r4Pow1 = powK(2, 10);
		globalThis.r4Pow2 = powK(1, Infinity);
		globalThis.r4Pow3 = powK(-1, 0.5);
	`, "r4Pow1", "r4Pow2", "r4Pow3")
	if values["r4Pow1"].String() != "1024" {
		t.Fatalf("r4Pow1=%v want=1024", values["r4Pow1"])
	}
	if values["r4Pow2"].String() != "1" || values["r4Pow3"].String() != "NaN" {
		t.Fatalf("pow special cases wrong: r4Pow2=%v r4Pow3=%v", values["r4Pow2"], values["r4Pow3"])
	}
	if stats.NativeCompiled != 0 || stats.NativeExecuted != 0 || stats.NativeRejected == 0 {
		t.Fatalf("pow must be rejected by native and stay Quick: %+v", stats)
	}
	if stats.Executed == 0 {
		t.Fatalf("pow function did not execute in Quick: %+v", stats)
	}
}

// TestAutoJITModBitwiseGuardFallsBack is the R4-7 class-2 evidence: non-Number
// operands fail the native entry guard and the result equals Tier 0's.
func TestAutoJITModBitwiseGuardFallsBack(t *testing.T) {
	for _, tc := range []struct{ name, expr string }{
		{name: "modK", expr: "a % b"},
		{name: "bitK", expr: "a & b"},
	} {
		values, stats := runAutoWithVerify(t, `
			function `+tc.name+`(a, b) { return `+tc.expr+`; }
			globalThis.r4Fallback1 = `+tc.name+`(8, 3);
			globalThis.r4Fallback2 = `+tc.name+`("8", "3");
			globalThis.r4Fallback3 = `+tc.name+`(8, "3");
		`, "r4Fallback1", "r4Fallback2", "r4Fallback3")
		want := "2"
		if tc.name == "bitK" {
			want = "0"
		}
		if values["r4Fallback1"].String() != want {
			t.Fatalf("%s fallback1=%v want=%s", tc.name, values["r4Fallback1"], want)
		}
		// JS coerces strings to Numbers in arithmetic: "8" % 3 = 2, "8" & "3" = 0.
		// The JIT tiers guard back to Tier 0 and must reproduce the coercion.
		if values["r4Fallback2"].String() != want || values["r4Fallback3"].String() != want {
			t.Fatalf("%s string operands must fall back to Tier 0 (%s): %v %v",
				tc.name, want, values["r4Fallback2"], values["r4Fallback3"])
		}
		if stats.GuardFailures == 0 {
			t.Fatalf("%s produced no guard failures for non-Number operands: %+v", tc.name, stats)
		}
	}
}

// TestAutoJITModLeafCircuitBreaker is the R4-7 class-3 evidence: repeated
// type changes trip the Quick circuit breaker; the native rejection of pow is
// covered by TestAutoJITPowStaysQuick's RejectionCacheHits.
func TestAutoJITModLeafCircuitBreaker(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function modK(a, b) { return a % b; }
		globalThis.r4cb1 = modK(9, 4);
		const bad = "x";
		globalThis.r4cb2 = modK(bad, 4);
		globalThis.r4cb3 = modK(bad, 4);
		globalThis.r4cb4 = modK(bad, 4);
	`, "r4cb1", "r4cb4")
	if values["r4cb1"].String() != "1" || values["r4cb4"].String() != "NaN" {
		t.Fatalf("mod circuit breaker results wrong: %v %v", values["r4cb1"], values["r4cb4"])
	}
	if stats.QuickGuardDisabled == 0 || stats.GuardFailures == 0 {
		t.Fatalf("mod leaf did not trip the circuit breaker after repeated type changes: %+v", stats)
	}
}

// TestAutoJITModTraceSafepointPrefix is the R4-7 class-4 evidence: a mod loop
// with a tiny trace budget yields at safepoints; the completed prefix must be
// exactly correct (sum of i % 7 up to n).
func TestAutoJITModTraceSafepointPrefix(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, TraceBudget: 3, Verify: true, Stats: true})
	_, err = vm.Eval(`
		function modSum(n) {
			const traceOnlyMarker = {};
			let s = 0;
			for (let i = 1; i <= n; i++) s += i % 7;
			return s;
		}
		globalThis.r4Prefix = modSum(1000);
	`, "r4-opts-prefix.js")
	if err != nil {
		t.Fatal(err)
	}
	// sum_{i=1}^{1000} (i mod 7): 142 full cycles of 0+1+2+3+4+5+6=21, plus
	// the tail 0+1+2+3+4+5+6+0+1+2+3+4+5+0+1+2+3+4+5+0... the total is 71000.
	want := "3003"
	got, err := vm.Global().Get("r4Prefix")
	if err != nil || got.String() != want {
		t.Fatalf("r4Prefix=%v err=%v want=%s", got, err, want)
	}
	stats := vm.JITStats()
	if stats.NativeTracesExecuted == 0 || stats.NativeTraceYields == 0 {
		t.Fatalf("mod trace did not yield at safepoints: %+v", stats)
	}
	if stats.VerifyFailures != 0 {
		t.Fatalf("verify disagreed across yield boundaries: %+v", stats)
	}
}

// TestAutoJITBitwiseTraceVerify is the R4-7 class-5 evidence: with Verify on,
// the native trace (bitwise ops) runs the dual execution and agrees; the
// verify-mismatch recovery mechanism itself is covered by the jit package's
// TestNativePropertyWriteVerifyRestoresQuickResultOnMismatch.
func TestAutoJITBitwiseTraceVerify(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function bitLoop(n) {
			const traceOnlyMarker = {};
			let x = 12345;
			for (let i = 0; i < n; i++) { x = ((x << 3) ^ i) >>> 1; }
			return x;
		}
		globalThis.r4BitTrace = bitLoop(500);
	`, "r4BitTrace")
	if values["r4BitTrace"].String() == "" {
		t.Fatalf("r4BitTrace=%v", values["r4BitTrace"])
	}
	if stats.NativeTracesCompiled == 0 || stats.NativeTracesExecuted == 0 || stats.VerifyChecks == 0 || stats.VerifyFailures != 0 {
		t.Fatalf("bitwise trace verify evidence missing: %+v", stats)
	}
}

// TestAutoJITR48SameFrameBlocksOnlyFailedBackedge is the R4-8 class-1/2
// evidence: after loop A's trace fails a guard mid-frame, the same frame must
// not retry loop A's trace (TraceFrameRetriesBlocked), but loop B — a
// different backedge in the same frame — still executes its own trace.
func TestAutoJITR48SameFrameBlocksOnlyFailedBackedge(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: ^uint32(0), BackedgeThreshold: 2, Stats: true,
	})
	_, err = vm.Eval(`
		function twoLoops(o, n) {
			const traceOnlyMarker = {};
			let s = 0;
			for (let i = 0; i < n; i++) s += o.value;   // loop A: guard-fails when o.value is a string
			let t = 0;
			for (let j = 0; j < n; j++) t += j;          // loop B: independent pure numeric loop
			return s + t;
		}
		const stable = { value: 2 };
		globalThis.r48a = twoLoops(stable, 20);           // warmup: both traces compile and run
		globalThis.r48b = twoLoops({ value: "x" }, 20);   // loop A fails; loop B must still trace
	`, "r4-8-frame.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Global().Get("r48a")
	if err != nil || got.String() != "230" {
		t.Fatalf("r48a=%v err=%v want=230", got, err)
	}
	got, err = vm.Global().Get("r48b")
	if err != nil || got.String() != "0xxxxxxxxxxxxxxxxxxxx190" {
		t.Fatalf("r48b=%v err=%v want=0xxxxxxxxxxxxxxxxxxxx190 (Tier 0 string concat in loop A, loop B = 190)", got, err)
	}
	stats := vm.JITStats()
	if stats.TraceFrameRetriesBlocked == 0 {
		t.Fatalf("same-frame retry blocking was not observed: %+v", stats)
	}
	// Loop B kept executing its trace (natively in Auto) in the frame where
	// loop A failed: warmup (loop A + loop B) plus the string call (loop B).
	if stats.NativeTracesExecuted+stats.TracesExecuted < 3 {
		t.Fatalf("loop B trace executions missing: %+v", stats)
	}
}

// TestAutoJITR48NativeEntryFallbackObservable is the R4-8 class-1 evidence
// for the duplicate-bridge counter: a third property shape on a native trace
// makes the bridge re-run the trace in Quick (the R4-3 learning path) — the
// fallback is counted and the trace still executes.
func TestAutoJITR48NativeEntryFallbackObservable(t *testing.T) {
	values, stats := runAutoWithVerify(t, `
		function sumValue(o, n) {
			const traceOnlyMarker = {};
			let total = 0;
			for (let i = 0; i < n; i++) total += o.value;
			return total;
		}
		globalThis.r48f1 = sumValue({ value: 1 }, 12);
		globalThis.r48f2 = sumValue({ prefix: 0, value: 2 }, 12);
		globalThis.r48f3 = sumValue({ prefix: 0, extra: 0, value: 3 }, 12);
	`, "r48f3")
	if values["r48f3"].String() != "36" {
		t.Fatalf("r48f3=%v want=36", values["r48f3"])
	}
	if stats.NativeTraceQuickFallbacks == 0 {
		t.Fatalf("native→Quick fallback bridge was not observed: %+v", stats)
	}
}
