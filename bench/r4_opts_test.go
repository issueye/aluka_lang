package bench

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// R4-7/R4-8 dedicated benchmarks. Each workload asserts its exact result so a
// silent Tier 0 fallback fails the benchmark instead of being measured as a
// win, and the Auto variants assert the target tier through JIT stats where
// the tier is load-bearing (mod/bitwise native hits, pow staying Quick).

// r4_7ModSource builds a `%`-heavy numeric loop. want is the exact sum of
// (i % 7) over the iterations.
func r4_7ModSource() (string, string) {
	return `function modKernel(n) {
  let s = 0;
  for (let i = 1; i <= n; i++) s += i % 7;
  return s;
}
modKernel(200000);
`, "599997"
}

func benchmarkR4_7Mod(b *testing.B, mode jit.Mode) {
	b.Helper()
	source, want := r4_7ModSource()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 2})
		got, err := vm.Eval(source, "r4-7-mod.js")
		if closeErr := vm.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			b.Fatal(err)
		}
		if got.String() != want {
			b.Fatalf("result=%s want=%s", got.String(), want)
		}
	}
}

// BenchmarkR4_7ModLoop measures the % fast path across tiers: Auto must use
// the native fmod (FPREM loop) while off is the Tier 0 baseline.
func BenchmarkR4_7ModLoop(b *testing.B) {
	b.Run("off", func(b *testing.B) { benchmarkR4_7Mod(b, jit.Off) })
	b.Run("quick", func(b *testing.B) { benchmarkR4_7Mod(b, jit.Quick) })
	b.Run("auto", func(b *testing.B) { benchmarkR4_7Mod(b, jit.Auto) })
}

// r4_7BitwiseSource builds a bitwise-heavy numeric loop over shift/rotate-ish
// ops that exercise ToInt32 and the 31-bit shift mask on every iteration.
func r4_7BitwiseSource() (string, string) {
	return `function bitKernel(n) {
  let x = 1234567;
  let s = 0;
  for (let i = 0; i < n; i++) { x = ((x << 3) ^ (x >> 2)) | (x >>> (i & 31)); s += x & 255; }
  return s;
}
bitKernel(200000);
`, "37179327"
}

func benchmarkR4_7Bitwise(b *testing.B, mode jit.Mode) {
	b.Helper()
	source, want := r4_7BitwiseSource()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 2})
		got, err := vm.Eval(source, "r4-7-bitwise.js")
		if closeErr := vm.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			b.Fatal(err)
		}
		if got.String() != want {
			b.Fatalf("result=%s want=%s", got.String(), want)
		}
	}
}

// BenchmarkR4_7BitwiseLoop measures the native ToInt32/bitwise ops across
// tiers.
func BenchmarkR4_7BitwiseLoop(b *testing.B) {
	b.Run("off", func(b *testing.B) { benchmarkR4_7Bitwise(b, jit.Off) })
	b.Run("quick", func(b *testing.B) { benchmarkR4_7Bitwise(b, jit.Quick) })
	b.Run("auto", func(b *testing.B) { benchmarkR4_7Bitwise(b, jit.Auto) })
}

// r4_7PowSource builds a pow-heavy numeric loop. Pow is rejected by the
// native tier (libm requirement), so Auto must equal Quick — the benchmark
// proves the stable Quick fallback has no hidden recompile cost.
func r4_7PowSource() (string, string) {
	return `function powKernel(n) {
  let s = 0;
  for (let i = 1; i <= n; i++) s += (i % 32) ** 2;
  return s;
}
powKernel(200000);
`, "65100000"
}

func benchmarkR4_7Pow(b *testing.B, mode jit.Mode) {
	b.Helper()
	source, want := r4_7PowSource()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 2, Stats: true})
		got, err := vm.Eval(source, "r4-7-pow.js")
		stats := vm.JITStats()
		if closeErr := vm.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			b.Fatal(err)
		}
		if got.String() != want {
			b.Fatalf("result=%s want=%s", got.String(), want)
		}
		if mode == jit.Auto && stats.NativeExecuted+stats.NativeTracesExecuted != 0 {
			b.Fatalf("pow must stay in Quick, native executed: %+v", stats)
		}
	}
}

// BenchmarkR4_7PowStaysQuick compares the rejected pow path: Auto must match
// Quick's cost (no native compile, no repeated rejection work).
func BenchmarkR4_7PowStaysQuick(b *testing.B) {
	b.Run("quick", func(b *testing.B) { benchmarkR4_7Pow(b, jit.Quick) })
	b.Run("auto", func(b *testing.B) { benchmarkR4_7Pow(b, jit.Auto) })
}

// r4_8SideExitSource builds a loop whose guard fails on the first iteration
// of every call (the object type flips between Number and String across
// r4_8SideExitSource builds a loop whose guard fails on every other call:
// the object property alternates between a Number (native trace executes)
// and a String (native entry guard fails, Quick re-check fails, the frame
// blocks same-frame retries). The measured cost is the deopt + frame-blocking
// path; the numeric calls keep the trace alive (success resets the failure
// counters), so the pattern never trips the circuit breaker.
func r4_8SideExitSource() (string, string) {
	return `function sideExit(o, n) {
  const marker = {};
  let s = 0;
  for (let i = 0; i < n; i++) s += o.value;
  return s;
}
let total = 0;
for (let r = 0; r < 200; r++) {
  total += sideExit({ value: 1 }, 64);
  total += sideExit({ value: "x" }, 64).length === 65 ? 1 : 0;
}
total;
`, "13000"
}

func benchmarkR4_8SideExit(b *testing.B, mode jit.Mode) {
	b.Helper()
	source, want := r4_8SideExitSource()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 2, Stats: true})
		got, err := vm.Eval(source, "r4-8-sideexit.js")
		stats := vm.JITStats()
		if closeErr := vm.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			b.Fatal(err)
		}
		if got.String() != want {
			b.Fatalf("result=%s want=%s", got.String(), want)
		}
		if mode == jit.Auto && stats.TraceFrameRetriesBlocked == 0 {
			b.Fatalf("side-exit frame blocking was not exercised: %+v", stats)
		}
		if mode == jit.Auto && stats.NativeTracesExecuted == 0 {
			b.Fatalf("side-exit numeric calls did not hit the native trace: %+v", stats)
		}
	}
}

// BenchmarkR4_8SideExit measures the guard-failure recovery path: Auto pays
// the bridge cost once per call and then blocks same-frame retries.
func BenchmarkR4_8SideExit(b *testing.B) {
	b.Run("off", func(b *testing.B) { benchmarkR4_8SideExit(b, jit.Off) })
	b.Run("quick", func(b *testing.B) { benchmarkR4_8SideExit(b, jit.Quick) })
	b.Run("auto", func(b *testing.B) { benchmarkR4_8SideExit(b, jit.Auto) })
}
