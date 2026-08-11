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
