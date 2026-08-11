//go:build !amd64 || (!windows && !linux)

// R2-7: unsupported-platform fallback validation for the JIT front end.
//
// Compiled exactly where native_emit_unsupported.go is active. Proves that
// Program.CompileNative / CompileNativeForDump reject with
// native.ErrUnsupported, never install native state, never panic, and that
// the platform gate is distinguishable from ordinary IR-level compile
// failures (exception exits, unproven locals), which keep their
// platform-independent error text on every platform.
//
// No t.Skip anywhere: every assertion executes on unsupported platforms.

package jit

import (
	"errors"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// unsupportedNumericProgram builds a small valid numeric leaf (mirrors the
// amd64-only native_emit_amd64_test.go fixture) that lowers cleanly and only
// fails at the platform gate.
func unsupportedNumericProgram() *Program {
	return &Program{
		NumLocals: 1,
		Code: []Instr{
			{Op: OpConst, Value: 1},
			{Op: OpConst, Value: 2},
			{Op: OpSwap},
			{Op: OpSub},
			{Op: OpNeg},
			{Op: OpReturn},
		},
	}
}

// unsupportedUnprovenLocalProgram mirrors TestNativeRejectsLocalNotAssigned-
// OnEveryPath: a local that is not assigned on every path fails native
// lowering ("not a proven number") before any machine code could exist.
func unsupportedUnprovenLocalProgram() *Program {
	return &Program{
		NumParams: 1,
		NumLocals: 3,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 0},
			{Op: OpLt},
			{Op: OpJumpFalse, Operand: 6},
			{Op: OpConst, Value: 1},
			{Op: OpStoreLocal, Operand: 2},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpReturn},
		},
	}
}

// plainLoopTemplate is the throwTraceTemplate (exception_test.go) without the
// throw: `while (a < b) { a = a + 1; }`. The trace [0, 32] contains no
// exception exit, so it lowers cleanly and fails only at the platform gate.
func plainLoopTemplate() *bytecode.FuncTemplate {
	return template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpLt, 0), emit(bytecode.OpJmpFalsePop, 24),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpStoreLocal, 1),
		emit(bytecode.OpJmp, (1<<24)-36),
		emit(bytecode.OpReturnUndef, 0),
	)
}

// assertNoNativeState checks that a rejected program carried no native code,
// plan, or debug artifacts.
func assertNoNativeState(t *testing.T, p *Program) {
	t.Helper()
	if p.HasNative() {
		t.Fatal("rejected program must not have native code installed")
	}
	if size := p.NativeSize(); size != 0 {
		t.Fatalf("NativeSize() = %d, want 0", size)
	}
	if debug := p.NativeDebugBytes(); debug != nil {
		t.Fatalf("NativeDebugBytes() = %v, want nil", debug)
	}
	if disasm := p.NativeDisassembly(); disasm != "" {
		t.Fatalf("NativeDisassembly() = %q, want empty", disasm)
	}
}

func TestUnsupportedPlatformCompileNativeRejects(t *testing.T) {
	p := unsupportedNumericProgram()
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	err := p.CompileNative()
	if !errors.Is(err, jitnative.ErrUnsupported) {
		t.Fatalf("CompileNative = %v, want native.ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "not supported on this platform") {
		t.Fatalf("CompileNative error = %q, want platform-gate message", err)
	}
	assertNoNativeState(t, p)
	if err := p.Close(); err != nil {
		t.Fatalf("Close after rejection = %v, want nil", err)
	}
	assertNoNativeState(t, p) // Close must be idempotent
}

func TestUnsupportedPlatformCompileNativeForDumpRejects(t *testing.T) {
	p := unsupportedNumericProgram()
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	err := p.CompileNativeForDump()
	if !errors.Is(err, jitnative.ErrUnsupported) {
		t.Fatalf("CompileNativeForDump = %v, want native.ErrUnsupported", err)
	}
	assertNoNativeState(t, p)
}

func TestUnsupportedPlatformTraceCompileNativeRejects(t *testing.T) {
	// An exception-exit trace is rejected by the IR-level gate BEFORE the
	// platform gate: its message must stay the platform-independent one, and
	// must NOT be ErrUnsupported (requirement: distinguish platform
	// rejection from ordinary compile failure).
	throwTrace, err := CompileTrace(throwTraceTemplate(), 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	err = throwTrace.CompileNative()
	if err == nil || !strings.Contains(err.Error(), "exception exit") {
		t.Fatalf("exception-exit trace CompileNative = %v, want exception-exit rejection", err)
	}
	if errors.Is(err, jitnative.ErrUnsupported) {
		t.Fatalf("exception-exit rejection = %v, must NOT be ErrUnsupported", err)
	}
	if throwTrace.HasNative() {
		t.Fatal("exception-exit trace must not have native code")
	}
	_ = throwTrace.Close()

	// A plain numeric loop trace lowers cleanly and hits the platform gate.
	plainTrace, err := CompileTrace(plainLoopTemplate(), 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	err = plainTrace.CompileNative()
	if !errors.Is(err, jitnative.ErrUnsupported) {
		t.Fatalf("plain trace CompileNative = %v, want native.ErrUnsupported", err)
	}
	if plainTrace.HasNative() {
		t.Fatal("rejected trace must not have native code")
	}
	_ = plainTrace.Close()
}

func TestUnsupportedPlatformRejectionReasonsDistinguishable(t *testing.T) {
	// Ordinary IR-level failure: unproven local. Keeps its own message and is
	// never reported as the platform gate.
	unproven := unsupportedUnprovenLocalProgram()
	if err := unproven.Verify(); err != nil {
		t.Fatal(err)
	}
	err := unproven.CompileNative()
	if err == nil || !strings.Contains(err.Error(), "not a proven number") {
		t.Fatalf("unproven-local CompileNative = %v, want proven-number rejection", err)
	}
	if errors.Is(err, jitnative.ErrUnsupported) {
		t.Fatalf("unproven-local rejection = %v, must NOT be ErrUnsupported", err)
	}

	// Platform gate: the same failure mode on a lowering-clean program.
	p := unsupportedNumericProgram()
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	err = p.CompileNative()
	if !errors.Is(err, jitnative.ErrUnsupported) {
		t.Fatalf("clean program CompileNative = %v, want native.ErrUnsupported", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "exception exit") || strings.Contains(msg, "proven number") {
		t.Fatalf("platform-gate error %q must not carry IR-level failure text", msg)
	}
}

func TestUnsupportedPlatformCloneAndAdoptStayInert(t *testing.T) {
	p := unsupportedNumericProgram()
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	clone := p.CloneForNative()
	assertNoNativeState(t, clone)
	if err := clone.Close(); err != nil {
		t.Fatalf("clone Close = %v, want nil", err)
	}
	// AdoptNativeFrom rejects candidates without native code; a failed adopt
	// must not install anything.
	if err := p.AdoptNativeFrom(clone); err == nil {
		t.Fatal("AdoptNativeFrom with unprepared program must fail")
	}
	if err := p.AdoptNativeFrom(nil); err == nil {
		t.Fatal("AdoptNativeFrom(nil) must fail")
	}
	assertNoNativeState(t, p)
}

func TestUnsupportedPlatformCompileNeverPanics(t *testing.T) {
	unproven := unsupportedUnprovenLocalProgram()
	if err := unproven.Verify(); err != nil {
		t.Fatal(err)
	}
	valid := unsupportedNumericProgram()
	if err := valid.Verify(); err != nil {
		t.Fatal(err)
	}
	for i, p := range []*Program{nil, valid, unproven} {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("case %d: CompileNative panicked: %v", i, recovered)
				}
			}()
			err := p.CompileNative()
			if err == nil {
				t.Fatalf("case %d: CompileNative succeeded, want rejection", i)
			}
			switch i {
			case 0:
				if !strings.Contains(err.Error(), "nil program") {
					t.Fatalf("case 0: error = %q, want nil-program message", err)
				}
			case 1:
				if !errors.Is(err, jitnative.ErrUnsupported) {
					t.Fatalf("case 1: error = %v, want native.ErrUnsupported", err)
				}
			case 2:
				if !strings.Contains(err.Error(), "not a proven number") {
					t.Fatalf("case 2: error = %q, want proven-number rejection", err)
				}
			}
		}()
	}
}
