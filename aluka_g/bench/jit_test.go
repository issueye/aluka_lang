package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

func jitColdStartSource(functions int) (string, string) {
	var source strings.Builder
	source.Grow(functions * 64)
	source.WriteString("let total = 0;\n")
	want := 0
	for i := 0; i < functions; i++ {
		fmt.Fprintf(&source, "function cold%d(x) { return x + %d; } total += cold%d(%d);\n", i, i, i, i)
		want += i * 2
	}
	source.WriteString("total;")
	return source.String(), fmt.Sprint(want)
}

func benchmarkJITColdStart(b *testing.B, mode jit.Mode) {
	b.Helper()
	const functions = 256
	source, want := jitColdStartSource(functions)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1000, BackedgeThreshold: 10000})
		got, err := vm.Eval(source, "jit-cold-start.js")
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

func BenchmarkJITColdStart(b *testing.B) {
	b.Run("off", func(b *testing.B) { benchmarkJITColdStart(b, jit.Off) })
	b.Run("auto", func(b *testing.B) { benchmarkJITColdStart(b, jit.Auto) })
}

// jitPropertyPICSource builds the R4-3/R4-4 dedicated property-PIC workload:
// a stable polymorphic rotation over 4 (or 4+1) own-data shapes. Every new
// shape is observed twice consecutively (the first observation is the
// confirmation fallback, the second is absorbed by the extended guard), so
// the workload converges to the JIT fast path by the second round and every
// subsequent round measures the absorbed steady state. The miss variant adds
// a fifth shape observed once per round: it can never be admitted (hard
// four-entry cap), so every round measures the R4-4 over-cap miss cost
// (cache rejection + stable fallback) without tripping the failure limit.
// Both variants assert the exact result, so a silent Tier 0 fallback would
// fail the benchmark instead of being measured as a win.
func jitPropertyPICSource(miss bool) (string, string) {
	shapes := `const S1 = { a: 1, b: 2 };
const S2 = { a: 3, b: 4, c: 1 };
const S3 = { a: 5, b: 6, c: 2, d: 3 };
const S4 = { a: 7, b: 8, d: 4, e: 5, f: 6 };`
	round := `
  total += picRot(S1, 40);
  total += picRot(S2, 40);
  total += picRot(S3, 40);
  total += picRot(S3, 40);
  total += picRot(S4, 40);
  total += picRot(S4, 40);`
	want := "248000"
	if miss {
		shapes += "\nconst S5 = { a: 9, b: 10, c: 3, d: 4, e: 5, f: 6, g: 7 };"
		round += "\n  total += picRot(S5, 40);"
		want = "324000"
	}
	source := fmt.Sprintf(`function picRot(o, n) {
  let s = 0;
  for (let i = 0; i < n; i++) { s += o.a + o.b; }
  return s;
}
%s
let total = 0;
for (let r = 0; r < 100; r++) {%s
}
total;
`, shapes, round)
	return source, want
}

func benchmarkJITPropertyPIC(b *testing.B, mode jit.Mode, miss bool) {
	b.Helper()
	source, want := jitPropertyPICSource(miss)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: mode, Threshold: 1, BackedgeThreshold: 2})
		got, err := vm.Eval(source, "jit-prop-pic.js")
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

// BenchmarkJITPropertyPICPolymorphic4 measures the absorbed four-shape
// rotation (§9.3 class-6 positive): in quick/auto the extended guard keeps
// every shape on the fast path; off is the Tier 0 baseline.
func BenchmarkJITPropertyPICPolymorphic4(b *testing.B) {
	b.Run("off", func(b *testing.B) { benchmarkJITPropertyPIC(b, jit.Off, false) })
	b.Run("quick", func(b *testing.B) { benchmarkJITPropertyPIC(b, jit.Quick, false) })
	b.Run("auto", func(b *testing.B) { benchmarkJITPropertyPIC(b, jit.Auto, false) })
}

// BenchmarkJITPropertyPICMissFifthShape measures the R4-4 miss path: the
// over-cap fifth shape is rejected by the cache every round and falls back
// to Tier 0 while the four absorbed shapes stay on the fast path.
func BenchmarkJITPropertyPICMissFifthShape(b *testing.B) {
	b.Run("off", func(b *testing.B) { benchmarkJITPropertyPIC(b, jit.Off, true) })
	b.Run("quick", func(b *testing.B) { benchmarkJITPropertyPIC(b, jit.Quick, true) })
	b.Run("auto", func(b *testing.B) { benchmarkJITPropertyPIC(b, jit.Auto, true) })
}
