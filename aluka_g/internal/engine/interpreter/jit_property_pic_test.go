package interpreter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// R4-3 / R4-4 property PIC evidence tests (§9.3 six classes):
//
//  1. positive hit statistics  -> TestPropertyPICAbsorbsFourStableShapes
//  2. shape/accessor/Proxy negatives -> TestPropertyPICRejectsAccessorAndProxy
//  3. fourth-shape cutoff / over-cap stable fallback -> TestPropertyPICOverflowBeyondFourthShape
//  4. safepoint interruption prefix -> TestPropertyPICSafepointInterruptKeepsPrefix
//  5. verify mismatch recovery -> jit package TestNativePropertyVerifyMismatchRestoresQuickResult
//  6. benchmarks -> bench/matrix_test.go (Tier 0) and bench/jit_test.go (JIT)
//
// Existing third-shape cutoff tests (TestAutoJITNativePropertyGuardDisablesOnlyNativeAfterThirdShape,
// TestAutoJITNativeTraceGuardDisablesOnlyNativeAfterThirdShape,
// TestAutoJITNativePropertyWriteTrace, TestGuardMutationThirdShapeDisablesNativeReleasesRX)
// keep passing unchanged: the native input-plan guards stay two-way while the
// portable Quick-tier guards absorb 2-4 stable shapes.
//
// The read tests pin the leaf tier only (BackedgeThreshold = max) so the
// exact per-tier accounting stays deterministic: the trace tier is a fallback
// tier that only runs after leaf failures.

// TestPropertyPICAbsorbsFourStableShapes is the class-1 evidence (positive hit
// statistics): a stable four-shape read workload is absorbed by the extended
// Quick-tier PIC in both Quick and Auto mode. Every shape executes on the
// fast path, the guard failures are exactly the first observations of the
// shapes that arrive after the stable baseline, the PIC counters explain the
// admissions (PropertyPICAdds) and the stable fallbacks
// (PropertyPICOverflows), and the state is never rejected.
func TestPropertyPICAbsorbsFourStableShapes(t *testing.T) {
	source := `
		function readShape(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.v; } return s; }
		const S1 = { v: 1 };
		const S2 = { prefix: 0, v: 2 };
		const S3 = { prefix: 0, extra: 0, v: 3 };
		const S4 = { prefix: 0, extra: 0, more: 0, v: 4 };
		globalThis.picR1 = readShape(S1, 4);
		globalThis.picR2 = readShape(S2, 4);
		globalThis.picR3 = readShape(S1, 4);
		globalThis.picR4 = readShape(S2, 4);
		globalThis.picR5 = readShape(S1, 4);
		globalThis.picR6 = readShape(S2, 4);
		globalThis.picR7 = readShape(S1, 4);
		globalThis.picR8 = readShape(S2, 4);
		globalThis.picR9 = readShape(S3, 4);
		globalThis.picR10 = readShape(S3, 4);
		globalThis.picR11 = readShape(S4, 4);
		globalThis.picR12 = readShape(S4, 4);
		globalThis.picR13 = readShape(S3, 4);
		globalThis.picR14 = readShape(S4, 4);
		globalThis.picR15 = readShape(S3, 4);
		globalThis.picR16 = readShape(S4, 4);
		globalThis.picR17 = readShape(S1, 4);
		globalThis.picR18 = readShape(S1, 4);
		globalThis.picR19 = readShape(S2, 4);
		globalThis.picR20 = readShape(S2, 4);
		globalThis.picR21 = readShape(S3, 4);
		globalThis.picR22 = readShape(S4, 4);
	`
	wants := map[string]string{
		"picR1": "4", "picR2": "8", "picR3": "4", "picR4": "8",
		"picR5": "4", "picR6": "8", "picR7": "4", "picR8": "8",
		"picR9": "12", "picR10": "12", "picR11": "16", "picR12": "16",
		"picR13": "12", "picR14": "16", "picR15": "12", "picR16": "16",
		"picR17": "4", "picR18": "4", "picR19": "8", "picR20": "8",
		"picR21": "12", "picR22": "16",
	}
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			// BackedgeThreshold max pins the leaf tier so the per-tier stats
			// below are exact (the trace tier never compiles).
			vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: ^uint32(0), Stats: true})
			if _, err := vm.Eval(source, "jit-pic-4-shape.js"); err != nil {
				t.Fatal(err)
			}
			for name, want := range wants {
				got, err := vm.Global().Get(name)
				if err != nil || got.String() != want {
					t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
				}
			}
			stats := vm.JITStats()
			if stats.Rejected != 0 || stats.QuickGuardDisabled != 0 || stats.TraceGuardDisabled != 0 {
				t.Fatalf("four-shape workload rejected the JIT state: %+v", stats)
			}
			if stats.PropertyPICHits == 0 {
				t.Fatalf("no PIC hits recorded: %+v", stats)
			}
			if stats.PropertyPICAdds != 2 {
				t.Fatalf("third/fourth-shape admissions = %d, want 2: %+v", stats.PropertyPICAdds, stats)
			}
			if mode == jit.Quick {
				// S3 and S4 each fall back on their first observation only.
				if stats.GuardFailures != 2 {
					t.Fatalf("quick guard failures = %d, want exactly 2: %+v", stats.GuardFailures, stats)
				}
				if stats.PropertyPICOverflows != 2 {
					t.Fatalf("quick over-limit misses = %d, want 2: %+v", stats.PropertyPICOverflows, stats)
				}
			} else {
				// Native plan guard stays two-way: S3 fails twice and disables
				// the native tier; the Quick fallback then re-admits S1/S2
				// (two first observations) after the baseline went cold.
				if stats.NativeGuardDisabled != 1 {
					t.Fatalf("native tier was not disabled after the third shape: %+v", stats)
				}
				if stats.GuardFailures != 4 {
					t.Fatalf("auto guard failures = %d, want 4 (2 native + 2 quick): %+v", stats.GuardFailures, stats)
				}
				if stats.PropertyPICOverflows < 4 {
					t.Fatalf("auto over-limit misses = %d, want >= 4: %+v", stats.PropertyPICOverflows, stats)
				}
			}
		})
	}
}

// TestPropertyPICRejectsAccessorAndProxy is the class-2 evidence (negative):
// accessor and Proxy receivers never enter the property PIC. The getter and
// the Proxy trap run exactly once per Tier 0 iteration (never inside the
// JIT), the fast-rejection counter explains the rejections, and rejected
// shapes are never admitted.
func TestPropertyPICRejectsAccessorAndProxy(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, Stats: true})
			_, err = vm.Eval(`
				function readValue(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.v; } return s; }
				let getterCalls = 0;
				const accessor = { get v() { getterCalls++; return 7; } };
				let proxyCalls = 0;
				const proxy = new Proxy({ v: 1 }, { get(target, key) { proxyCalls++; return target[key]; } });
				globalThis.picAccessor = readValue(accessor, 3);
				globalThis.picAccessorCalls = getterCalls;
				globalThis.picProxy = readValue(proxy, 3);
				globalThis.picProxyCalls = proxyCalls;
			`, "jit-pic-reject.js")
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
			if val("picAccessor") != "21" || val("picAccessorCalls") != "3" ||
				val("picProxy") != "3" || val("picProxyCalls") != "3" {
				t.Fatalf("accessor/proxy results: accessor=%s calls=%s proxy=%s traps=%s",
					val("picAccessor"), val("picAccessorCalls"), val("picProxy"), val("picProxyCalls"))
			}
			stats := vm.JITStats()
			if stats.GuardFailures == 0 {
				t.Fatalf("accessor/proxy reads produced no guard failures: %+v", stats)
			}
			if stats.PropertyPICRejections == 0 {
				t.Fatalf("accessor/proxy reads produced no fast rejections: %+v", stats)
			}
			if stats.PropertyPICAdds != 0 {
				t.Fatalf("rejected shapes were admitted: %+v", stats)
			}
		})
	}
}

// TestPropertyPICOverflowBeyondFourthShape is the class-3 evidence (cutoff):
// a fifth shape at a site that already absorbed four shapes is never admitted
// (the hard cap), every observation of it is a stable fallback recorded in
// PropertyPICOverflows, and after two misses the Quick state stabilizes to
// Tier 0 with identical results.
func TestPropertyPICOverflowBeyondFourthShape(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, BackedgeThreshold: ^uint32(0), Stats: true})
	_, err = vm.Eval(`
		function readShape(o, n) { let s = 0; for (let i = 0; i < n; i++) { s += o.v; } return s; }
		const S1 = { v: 1 };
		const S2 = { a: 0, v: 2 };
		const S3 = { a: 0, b: 0, v: 3 };
		const S4 = { a: 0, b: 0, c: 0, v: 4 };
		const S5 = { a: 0, b: 0, c: 0, d: 0, v: 5 };
		globalThis.ov1 = readShape(S1, 4);
		globalThis.ov2 = readShape(S2, 4);
		globalThis.ov3 = readShape(S1, 4);
		globalThis.ov4 = readShape(S2, 4);
		globalThis.ov5 = readShape(S1, 4);
		globalThis.ov6 = readShape(S2, 4);
		globalThis.ov7 = readShape(S1, 4);
		globalThis.ov8 = readShape(S2, 4);
		globalThis.ov9 = readShape(S3, 4);
		globalThis.ov10 = readShape(S3, 4);
		globalThis.ov11 = readShape(S4, 4);
		globalThis.ov12 = readShape(S4, 4);
		globalThis.ov13 = readShape(S5, 4);
		globalThis.ov14 = readShape(S5, 4);
		globalThis.ov15 = readShape(S1, 4);
	`, "jit-pic-overflow.js")
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
	for name, want := range map[string]string{
		"ov1": "4", "ov2": "8", "ov3": "4", "ov4": "8", "ov5": "4", "ov6": "8", "ov7": "4", "ov8": "8",
		"ov9": "12", "ov10": "12", "ov11": "16", "ov12": "16", "ov13": "20", "ov14": "20", "ov15": "4",
	} {
		if got := val(name); got != want {
			t.Fatalf("%s=%s want=%s", name, got, want)
		}
	}
	stats := vm.JITStats()
	// S3 and S4 were absorbed (2 adds); S5 overflowed four times in total
	// (first observations of S3/S4 plus the two S5 observations) and the
	// state stabilized to Tier 0 afterwards.
	if stats.PropertyPICAdds != 2 {
		t.Fatalf("adds = %d, want 2 (S3/S4 absorbed): %+v", stats.PropertyPICAdds, stats)
	}
	if stats.PropertyPICOverflows < 4 {
		t.Fatalf("overflows = %d, want >= 4 (S5 rejected twice): %+v", stats.PropertyPICOverflows, stats)
	}
	if stats.GuardFailures != 4 {
		t.Fatalf("guard failures = %d, want 4: %+v", stats.GuardFailures, stats)
	}
	if stats.QuickGuardDisabled != 1 {
		t.Fatalf("fifth shape did not stabilize the Quick state: %+v", stats)
	}
}

// TestPropertyPICSafepointInterruptKeepsPrefix is the class-4 evidence: a
// safepoint interruption inside a polymorphic property-write trace preserves
// the committed prefix exactly once (o.a === o.b - 1 proves every committed
// iteration wrote both properties atomically), in both Quick and Auto, after
// the third shape was absorbed by the extended guard.
func TestPropertyPICSafepointInterruptKeepsPrefix(t *testing.T) {
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			polls := 0
			vm, err := NewVM()
			if err != nil {
				t.Fatal(err)
			}
			defer vm.Close()
			vm.ConfigureJIT(jit.Config{
				Mode:              mode,
				Threshold:         ^uint32(0),
				BackedgeThreshold: 2,
				TraceBudget:       1,
				Stats:             true,
				Safepoint: func() error {
					polls++
					if polls == 200 {
						return errors.New("pic trace interrupted")
					}
					return nil
				},
			})
			_, err = vm.Eval(`
				function writeBoth(o, n) { for (let i = 0; i < n; i++) { o.a = i; o.b = i + 1; } return o.a; }
				const S1 = { a: -1, b: 0 };
				const S2 = { a: -1, b: 0, c: 1 };
				const S3 = { a: -1, b: 0, d: 2 };
				globalThis.picW1 = writeBoth(S1, 6);
				globalThis.picW2 = writeBoth(S2, 6);
				globalThis.picW3 = writeBoth(S1, 6);
				globalThis.picW4 = writeBoth(S2, 6);
				globalThis.picW5 = writeBoth(S1, 6);
				globalThis.picW6 = writeBoth(S2, 6);
				globalThis.picW7 = writeBoth(S1, 6);
				globalThis.picW8 = writeBoth(S2, 6);
				globalThis.picW9 = writeBoth(S3, 6);
				globalThis.picW10 = writeBoth(S3, 6);
				let message = "";
				try { globalThis.picW11 = writeBoth(S3, 1000000); } catch (e) { message = e.message; }
				globalThis.picInterruptMessage = message;
				globalThis.picInvariant = S3.a === S3.b - 1;
			`, "jit-pic-safepoint.js")
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
			for i := 1; i <= 10; i++ {
				if got := val(fmt.Sprintf("picW%d", i)); got != "5" {
					t.Fatalf("picW%d=%s want=5", i, got)
				}
			}
			if val("picInterruptMessage") != "pic trace interrupted" {
				t.Fatalf("interruption message = %s", val("picInterruptMessage"))
			}
			if val("picInvariant") != "true" {
				t.Fatalf("committed prefix invariant violated")
			}
			stats := vm.JITStats()
			if stats.Interruptions != 1 || stats.SafepointPolls < 200 {
				t.Fatalf("safepoint stats: polls=%d interruptions=%d", stats.SafepointPolls, stats.Interruptions)
			}
			// The third shape was absorbed: no rejection and the over-cap
			// misses of its confirmation are recorded.
			if stats.TraceGuardDisabled != 0 || stats.QuickGuardDisabled != 0 {
				t.Fatalf("polymorphic safepoint workload disabled the JIT: %+v", stats)
			}
			if stats.PropertyPICOverflows == 0 {
				t.Fatalf("third-shape confirmation fallbacks not recorded: %+v", stats)
			}
		})
	}
}

// TestPropertyPICStatsExplainHitAndReject exercises the R4-4 statistical
// contract: a monomorphic hit workload reports PIC hits and zero
// rejections/overflows, and a following non-Number shape reports fast
// rejections without admitting anything.
func TestPropertyPICStatsExplainHitAndReject(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	_, err = vm.Eval(`
		function read(o) { return o.v + 1; }
		const o = { v: 7 };
		globalThis.picStats1 = read(o);
		o.v = 9;
		globalThis.picStats2 = read(o);
		o.v = "changed";
		globalThis.picStats3 = read(o);
	`, "jit-pic-stats.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"picStats1": "8", "picStats2": "10", "picStats3": "changed1"} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s=%v err=%v want=%s", name, got, err, want)
		}
	}
	stats := vm.JITStats()
	if stats.PropertyPICHits == 0 {
		t.Fatalf("monomorphic hits not recorded: %+v", stats)
	}
	if stats.PropertyPICRejections == 0 {
		t.Fatalf("non-Number shape was not rejected: %+v", stats)
	}
	if stats.PropertyPICAdds != 0 {
		t.Fatalf("non-Number shape was admitted: %+v", stats)
	}
	if stats.PropertyPICOverflows != 0 {
		t.Fatalf("unexpected over-limit misses: %+v", stats)
	}
}
