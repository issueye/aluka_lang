package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// runTierGlobals evals code in a fresh VM under the given mode and returns the
// String forms of the named globals.
func runTierGlobals(t *testing.T, mode jit.Mode, code string, names []string) (map[string]string, jit.Stats) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 1, TraceBudget: 8, Verify: mode == jit.Auto, Stats: true})
	if _, err := vm.Eval(code, "jit-primitive.js"); err != nil {
		t.Fatalf("mode=%s: %v", mode, err)
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		got, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("mode=%s get %s: %v", mode, name, err)
		}
		values[name] = got.String()
	}
	return values, vm.JITStats()
}

// assertTiersAgree runs the same code under off/quick/auto and requires
// identical observable results, plus a stats predicate for the non-off tiers.
func assertTiersAgree(t *testing.T, code string, names []string, checkStats func(t *testing.T, mode jit.Mode, stats jit.Stats)) {
	t.Helper()
	results := make(map[jit.Mode]map[string]string)
	var stats map[jit.Mode]jit.Stats
	stats = make(map[jit.Mode]jit.Stats)
	for _, mode := range []jit.Mode{jit.Off, jit.Quick, jit.Auto} {
		values, s := runTierGlobals(t, mode, code, names)
		results[mode] = values
		stats[mode] = s
		if mode != jit.Off && checkStats != nil {
			checkStats(t, mode, s)
		}
	}
	for _, name := range names {
		off := results[jit.Off][name]
		if results[jit.Quick][name] != off || results[jit.Auto][name] != off {
			t.Fatalf("tier mismatch for %s: off=%q quick=%q auto=%q", name, off, results[jit.Quick][name], results[jit.Auto][name])
		}
	}
}

// TestJITStringConcatTiersDifferential is the R3-4 function-level gate: String
// `+` (same-type concat in Quick, mixed coercion via Tier 0 fallback), concat
// results feeding truthiness and strict equality — identical in all tiers.
func TestJITStringConcatTiersDifferential(t *testing.T) {
	code := `
function concatKernel(a, b, n) {
  let s = a;
  for (let i = 0; i < n; i++) { s = s + b; }
  if (s < a) { return s; }
  return b;
}
globalThis.k1 = concatKernel("a", "b", 3);
globalThis.k2 = concatKernel("", "x", 2);
globalThis.k3 = concatKernel("a", 1, 2);
globalThis.k4 = concatKernel(2, 3, 3);
globalThis.k5 = concatKernel("b", "a", 3);
function eqKernel(a, b, c) { const s = a + b; if (s === c) { return a; } return s; }
globalThis.e1 = eqKernel("a", "b", "ab");
globalThis.e2 = eqKernel("a", "b", "x");
function truthKernel(a, b, c) { const s = a + b; if (s) { return s; } return c; }
globalThis.t1 = truthKernel("a", "b", "z");
globalThis.t2 = truthKernel("", "", "z");
globalThis.t3 = truthKernel("x", "", "z");
`
	assertTiersAgree(t, code, []string{"k1", "k2", "k3", "k4", "k5", "e1", "e2", "t1", "t2", "t3"}, func(t *testing.T, mode jit.Mode, stats jit.Stats) {
		if stats.Compiled == 0 && stats.TracesCompiled == 0 {
			t.Fatalf("mode=%s did not compile a String-op program: %+v", mode, stats)
		}
		if stats.Executed+stats.TracesExecuted == 0 {
			t.Fatalf("mode=%s never executed a String-op program: %+v", mode, stats)
		}
	})
}

// TestJITStringRelationalTiersDifferential is the R3-4 relational gate: all
// four relational operators on same-type Strings, plus mixed fallbacks.
func TestJITStringRelationalTiersDifferential(t *testing.T) {
	code := `
function relKernel(a, b) {
  let r = 0;
  if (a < b) r += 1;
  if (a <= b) r += 2;
  if (a > b) r += 4;
  if (a >= b) r += 8;
  return r;
}
globalThis.r1 = relKernel("a", "b");
globalThis.r2 = relKernel("ab", "a");
globalThis.r3 = relKernel("a", "a");
globalThis.r4 = relKernel("b", "ab");
globalThis.r5 = relKernel(2, 3);
globalThis.r6 = relKernel("a", 1);
`
	assertTiersAgree(t, code, []string{"r1", "r2", "r3", "r4", "r5", "r6"}, func(t *testing.T, mode jit.Mode, stats jit.Stats) {
		if stats.Executed == 0 {
			t.Fatalf("mode=%s never executed: %+v", mode, stats)
		}
	})
}

// TestJITBigIntArithTiersDifferential is the R3-5 arithmetic gate: same-type
// BigInt + - * / % and unary -, RangeError on division by zero, TypeError on
// mixed operands, all identical across tiers. The loop kernels carry a
// property write so function-level compilation is rejected and the loops run
// as traces (BigInt arithmetic inside the trace executor).
func TestJITBigIntArithTiersDifferential(t *testing.T) {
	code := `
function arithKernel(a, b, n, o) {
  let s = -a;
  for (let i = 0; i < n; i++) { s = s + b; o.last = i; }
  return s * b - a;
}
globalThis.b1 = arithKernel(5n, 2n, 3, { last: -1 });
globalThis.b2 = arithKernel(1n, 0n, 2, { last: -1 });
globalThis.b3 = arithKernel(7n, 1n, 0, { last: -1 });
function loopDiv(a, b, n, o) {
  let s = a;
  for (let i = 0; i < n; i++) { s = s / b; o.last = i; }
  return s;
}
globalThis.d1 = loopDiv(100n, 7n, 3, { last: -1 });
globalThis.d2 = loopDiv(-7n, 3n, 1, { last: -1 });
try { loopDiv(1n, 0n, 2, { last: -1 }); globalThis.d3 = "no-throw"; } catch (e) { globalThis.d3 = e.name + ":" + e.message; }
try { loopDiv(1n, 1, 2, { last: -1 }); globalThis.d4 = "no-throw"; } catch (e) { globalThis.d4 = e.name + ":" + e.message; }
globalThis.d5 = 7n % 2n;
function modKernel(a, b) { return a % b; }
try { modKernel(1n, 0n); globalThis.d6 = "no-throw"; } catch (e) { globalThis.d6 = e.name + ":" + e.message; }
`
	assertTiersAgree(t, code, []string{"b1", "b2", "b3", "d1", "d2", "d3", "d4", "d5", "d6"}, func(t *testing.T, mode jit.Mode, stats jit.Stats) {
		if stats.TracesCompiled == 0 || stats.TracesExecuted == 0 {
			t.Fatalf("mode=%s did not compile/execute a BigInt trace: %+v", mode, stats)
		}
	})
}

// TestJITBigIntBitwiseTiersDifferential is the R3-5 bitwise gate: same-type
// BigInt & | ^ << >>, plus the fallback exceptions (negative shift RangeError,
// `>>>` TypeError, mixed TypeError) that must be identical across tiers.
func TestJITBigIntBitwiseTiersDifferential(t *testing.T) {
	code := `
function bitKernel(a, b, n) {
  let s = a;
  for (let i = 0; i < n; i++) { s = s ^ b; }
  return s << b;
}
globalThis.w1 = bitKernel(5n, 3n, 3);
globalThis.w2 = bitKernel(7n, 3n, 0);
globalThis.w3 = bitKernel(8n, 2n, 1) >> 1n;
try { bitKernel(1n, -1n, 1); globalThis.w4 = "no-throw"; } catch (e) { globalThis.w4 = e.name + ":" + e.message; }
try { bitKernel(5n, 1, 2); globalThis.w5 = "no-throw"; } catch (e) { globalThis.w5 = e.name + ":" + e.message; }
function ushrKernel(a, b) { return a >>> b; }
try { ushrKernel(8n, 1n); globalThis.w6 = "no-throw"; } catch (e) { globalThis.w6 = e.name + ":" + e.message; }
globalThis.w7 = ushrKernel(8, 1);
globalThis.w8 = (1n & 3n) | (4n ^ 2n);
`
	assertTiersAgree(t, code, []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8"}, func(t *testing.T, mode jit.Mode, stats jit.Stats) {
		if stats.TracesCompiled == 0 {
			t.Fatalf("mode=%s did not compile a BigInt trace: %+v", mode, stats)
		}
	})
}

// TestJITBigIntCompareTiersDifferential is the R3-5 comparison gate: all four
// relational operators and strict equality on BigInt pairs (same-type in
// Quick, BigInt/Number mixes via Tier 0).
func TestJITBigIntCompareTiersDifferential(t *testing.T) {
	code := `
function cmpKernel(a, b) {
  let r = 0;
  if (a < b) r += 1;
  if (a <= b) r += 2;
  if (a > b) r += 4;
  if (a >= b) r += 8;
  if (a === b) r += 16;
  if (a !== b) r += 32;
  return r;
}
globalThis.c1 = cmpKernel(5n, 8n);
globalThis.c2 = cmpKernel(7n, 7n);
globalThis.c3 = cmpKernel(8n, 5n);
globalThis.c4 = cmpKernel(-3n, 1n);
globalThis.c5 = cmpKernel(2n, 1);
globalThis.c6 = cmpKernel("a", "b");
globalThis.c7 = cmpKernel("ab", "a");
`
	assertTiersAgree(t, code, []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7"}, func(t *testing.T, mode jit.Mode, stats jit.Stats) {
		if stats.Executed == 0 {
			t.Fatalf("mode=%s never executed: %+v", mode, stats)
		}
	})
}

// TestJITStringTraceTiersDifferential proves the R3-4 trace path: a loop whose
// body concatenates Strings (allocations inside the trace executor), compares
// them relationally and carries a numeric property write commits identical
// results in every tier. The property write rejects function-level
// compilation, so the loop actually runs as a trace.
func TestJITStringTraceTiersDifferential(t *testing.T) {
	code := `
function loopConcat(a, b, n, o) {
  let s = a;
  for (let i = 0; i < n; i++) { if (s < a) { s = s + b; } o.last = i; }
  return s;
}
globalThis.l1 = loopConcat("a", "b", 3, { last: -1 });
globalThis.l2 = loopConcat("", "x", 4, { last: -1 });
globalThis.l3 = loopConcat("b", "a", 5, { last: -1 });
globalThis.l4 = loopConcat("a", 1, 3, { last: -1 });
`
	assertTiersAgree(t, code, []string{"l1", "l2", "l3", "l4"}, func(t *testing.T, mode jit.Mode, stats jit.Stats) {
		if stats.TracesCompiled == 0 || stats.TracesExecuted == 0 {
			t.Fatalf("mode=%s did not run the String-op trace: %+v", mode, stats)
		}
	})
}

// TestJITNativeEntryStringBigIntFallsBackToQuick is the R3-4/R3-5 Native
// gate: Auto compiles the numeric-IR function to machine code, but String and
// BigInt arguments fail the Native ABI entry guard (no ABI expansion) and the
// execution must stably fall back to the Quick tier with identical results.
func TestJITNativeEntryStringBigIntFallsBackToQuick(t *testing.T) {
	code := `
function primitiveAdd(a, b) { return a + b; }
globalThis.n1 = primitiveAdd(7n, 3n);
globalThis.n2 = primitiveAdd("a", "b");
globalThis.n3 = primitiveAdd("a", 1);
globalThis.n4 = primitiveAdd(2, 3);
`
	names := []string{"n1", "n2", "n3", "n4"}
	offValues, _ := runTierGlobals(t, jit.Off, code, names)
	autoValues, stats := runTierGlobals(t, jit.Auto, code, names)
	for _, name := range names {
		if autoValues[name] != offValues[name] {
			t.Fatalf("auto mismatch for %s: off=%q auto=%q", name, offValues[name], autoValues[name])
		}
	}
	if stats.NativeCompiled == 0 {
		t.Fatalf("Auto did not compile the numeric-IR function to native: %+v", stats)
	}
	if stats.Executed == 0 {
		t.Fatalf("Auto never executed the Quick fallback: %+v", stats)
	}
	// The String/BigInt calls must not have been absorbed by Native: every
	// call either executed in Quick or failed the native entry guard (which is
	// counted in GuardFailures when it happens after warmup).
	if stats.NativeExecuted > 1 {
		t.Fatalf("Native absorbed calls it cannot serve: %+v", stats)
	}
}

// TestJITBigIntTraceDivZeroFallsBack covers the R3-5 exception path inside a
// trace: the division happens from the second loop iteration on (so the trace
// compiles and executes at least one slice first), then the BigInt division by
// zero aborts the trace slice (no partial commit) and Tier 0 raises the
// identical RangeError in every tier.
func TestJITBigIntTraceDivZeroFallsBack(t *testing.T) {
	code := `
function loopDiv(a, b, n, o) {
  let s = a;
  for (let i = 0; i < n; i++) { o.last = i; if (i >= 1) { s = s / b; } }
  return s;
}
try { loopDiv(1n, 0n, 4, { last: -1 }); globalThis.z = "no-throw"; } catch (e) { globalThis.z = e.name + ":" + e.message; }
`
	offValues, _ := runTierGlobals(t, jit.Off, code, []string{"z"})
	for _, mode := range []jit.Mode{jit.Quick, jit.Auto} {
		values, stats := runTierGlobals(t, mode, code, []string{"z"})
		if values["z"] != offValues["z"] {
			t.Fatalf("mode=%s: z=%q off=%q", mode, values["z"], offValues["z"])
		}
		if stats.TracesCompiled == 0 {
			t.Fatalf("mode=%s did not compile the trace: %+v", mode, stats)
		}
	}
}
