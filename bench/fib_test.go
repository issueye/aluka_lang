// Package bench contains performance benchmarks for the aluka JS engine.
//
// fib(30) is the canonical recursive-Fibonacci micro-benchmark: it stresses
// function calls, arithmetic, and control flow. We compare the bytecode VM
// (Phase 1B) against the AST-walking interpreter (Phase 1A) to measure the
// speedup from compiling to bytecode.
package bench

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// fibCode is the canonical recursive Fibonacci JS source.
const fibCode = `function fib(n) { if (n < 2) return n; return fib(n-1) + fib(n-2); } fib(30)`

// BenchmarkFibVM measures the bytecode VM on fib(30).
func BenchmarkFibVM(b *testing.B) {
	vm, err := interpreter.NewVM()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Eval(fibCode, "fib.js"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFibAST measures the AST-walking interpreter on fib(30).
func BenchmarkFibAST(b *testing.B) {
	interp, err := interpreter.NewInterpreter()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := interp.Eval(fibCode, "fib.js"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFibVMSmaller runs fib(20) on the VM for a faster, finer-grained
// measurement that is less sensitive to GC noise.
func BenchmarkFibVMSmaller(b *testing.B) {
	vm, err := interpreter.NewVM()
	if err != nil {
		b.Fatal(err)
	}
	const code = `function fib(n) { if (n < 2) return n; return fib(n-1) + fib(n-2); } fib(20)`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Eval(code, "fib.js"); err != nil {
			b.Fatal(err)
		}
	}
}

// TestFibCorrectness verifies both engines produce the correct fib(30) value.
func TestFibCorrectness(t *testing.T) {
	const want = "832040"

	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Eval(fibCode, "fib.js")
	if err != nil {
		t.Fatalf("VM fib(30) error: %v", err)
	}
	if got.String() != want {
		t.Errorf("VM fib(30) = %q, want %q", got.String(), want)
	}

	interp, err := interpreter.NewInterpreter()
	if err != nil {
		t.Fatal(err)
	}
	got2, err := interp.Eval(fibCode, "fib.js")
	if err != nil {
		t.Fatalf("AST fib(30) error: %v", err)
	}
	if got2.String() != want {
		t.Errorf("AST fib(30) = %q, want %q", got2.String(), want)
	}
}
