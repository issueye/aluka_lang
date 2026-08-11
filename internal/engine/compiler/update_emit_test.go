package compiler

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// compileFirst compiles source and returns the first non-module function's
// code (the module wrapper is Functions[0]).
func compileFirst(t *testing.T, src string) *bytecode.FuncTemplate {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := New().Compile(prog, "update-emit.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range mod.Functions {
		if fn.Name != "" && fn.Name != "<main>" {
			return fn
		}
	}
	t.Fatal("no named function found")
	return nil
}

// TestCompileUpdateEmitsIncDec locks the update-expression bytecode shapes
// that the JIT arrayPush/closureIncrement matchers depend on: postfix `i++`
// must emit OpInc and prefix `++n` must emit LOAD_UPVALUE/INC/DUP/
// STORE_UPVALUE (see the dedicated tests below for each shape). The disk
// bytecode cache is keyed on FormatVersion so any future change to these
// shapes must bump it, otherwise stale caches silently disable the JIT
// specializations.
func TestCompileUpdateEmitsIncDec(t *testing.T) {
	fn := compileFirst(t, `
function f(iters) {
  let s = 0;
  for (let i = 0; i < iters; i++) { s += i; }
  return s;
}
`)
	code := fn.Code
	found := false
	for pc := 0; pc+bytecode.InstrSize <= len(code); pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) == bytecode.OpInc {
			found = true
		}
	}
	if !found {
		t.Fatal("no OpInc emitted for postfix i++")
	}
}

// TestCompileUpdateEmitsDec is the decrement mirror of
// TestCompileUpdateEmitsIncDec: postfix `i--` must emit OpDec so the JIT
// matcher shapes stay locked on both directions of the update operators.
func TestCompileUpdateEmitsDec(t *testing.T) {
	fn := compileFirst(t, `
function f(iters) {
  let s = 0;
  for (let i = iters; i > 0; i--) { s += i; }
  return s;
}
`)
	code := fn.Code
	found := false
	for pc := 0; pc+bytecode.InstrSize <= len(code); pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) == bytecode.OpDec {
			found = true
		}
	}
	if !found {
		t.Fatal("no OpDec emitted for postfix i--")
	}
}

// TestCompileForUpdateIncKeepsDupShape locks the exact loop-update tail the
// JIT arrayPush matcher depends on: LOAD_LOCAL, DUP, INC, STORE_LOCAL, POP.
// The `s += f()` loop body (benchmark closureCall/arrayPush shape) keeps the
// postfix result DUP even though the for-update value is discarded.
func TestCompileForUpdateIncKeepsDupShape(t *testing.T) {
	prog, err := parser.Parse(`
function f(iters) {
  const a = [];
  for (let i = 0; i < iters; i++) { a.push(i); }
  return a.length;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := New().Compile(prog, "update-emit.js")
	if err != nil {
		t.Fatal(err)
	}
	var fn *bytecode.FuncTemplate
	for _, f := range mod.Functions {
		if f.Name == "f" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("function f not found")
	}
	code := fn.Code
	lastJmp := -1
	for pc := 0; pc+bytecode.InstrSize <= len(code); pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) == bytecode.OpJmp {
			lastJmp = pc
		}
	}
	if lastJmp < 5*bytecode.InstrSize {
		t.Fatalf("no loop backedge found")
	}
	pre := lastJmp - bytecode.InstrSize
	if bytecode.Opcode(code[pre]) != bytecode.OpPop ||
		bytecode.Opcode(code[pre-bytecode.InstrSize]) != bytecode.OpStoreLocal ||
		bytecode.Opcode(code[pre-2*bytecode.InstrSize]) != bytecode.OpInc ||
		bytecode.Opcode(code[pre-3*bytecode.InstrSize]) != bytecode.OpDup ||
		bytecode.Opcode(code[pre-4*bytecode.InstrSize]) != bytecode.OpLoadLocal {
		t.Fatalf("arrayPush loop-update tail broken: %s %s %s %s %s",
			bytecode.Opcode(code[pre-4*bytecode.InstrSize]),
			bytecode.Opcode(code[pre-3*bytecode.InstrSize]),
			bytecode.Opcode(code[pre-2*bytecode.InstrSize]),
			bytecode.Opcode(code[pre-bytecode.InstrSize]),
			bytecode.Opcode(code[pre]))
	}
}

// TestCompilePrefixIncEmitsIncDup locks the prefix `++n` shape used by the
// numeric-upvalue closure matcher: LOAD_UPVALUE, INC, DUP, STORE_UPVALUE.
func TestCompilePrefixIncEmitsIncDup(t *testing.T) {
	prog, err := parser.Parse(`
function make() { let n = 0; return () => ++n; }
`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := New().Compile(prog, "update-emit.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range mod.Functions {
		if fn.Name != "" || len(fn.Upvalues) != 1 || len(fn.Code) < 4*bytecode.InstrSize {
			continue
		}
		if bytecode.Opcode(fn.Code[0]) != bytecode.OpLoadUpvalue ||
			bytecode.Opcode(fn.Code[1*bytecode.InstrSize]) != bytecode.OpInc ||
			bytecode.Opcode(fn.Code[2*bytecode.InstrSize]) != bytecode.OpDup ||
			bytecode.Opcode(fn.Code[3*bytecode.InstrSize]) != bytecode.OpStoreUpvalue {
			t.Fatalf("prefix ++n shape broken: %s %s %s %s",
				bytecode.Opcode(fn.Code[0]), bytecode.Opcode(fn.Code[4]),
				bytecode.Opcode(fn.Code[8]), bytecode.Opcode(fn.Code[12]))
		}
		return
	}
	t.Fatal("no increment closure template found")
}

// TestCompilePrefixDecEmitsDecDup locks the prefix `--n` closure shape:
// LOAD_UPVALUE, DEC, DUP, STORE_UPVALUE (decrement mirror of the ++n test).
func TestCompilePrefixDecEmitsDecDup(t *testing.T) {
	prog, err := parser.Parse(`
function make() { let n = 0; return () => --n; }
`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := New().Compile(prog, "update-emit.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range mod.Functions {
		if fn.Name != "" || len(fn.Upvalues) != 1 || len(fn.Code) < 4*bytecode.InstrSize {
			continue
		}
		if bytecode.Opcode(fn.Code[0]) != bytecode.OpLoadUpvalue ||
			bytecode.Opcode(fn.Code[1*bytecode.InstrSize]) != bytecode.OpDec ||
			bytecode.Opcode(fn.Code[2*bytecode.InstrSize]) != bytecode.OpDup ||
			bytecode.Opcode(fn.Code[3*bytecode.InstrSize]) != bytecode.OpStoreUpvalue {
			t.Fatalf("prefix --n shape broken: %s %s %s %s",
				bytecode.Opcode(fn.Code[0]), bytecode.Opcode(fn.Code[4]),
				bytecode.Opcode(fn.Code[8]), bytecode.Opcode(fn.Code[12]))
		}
		return
	}
	t.Fatal("no decrement closure template found")
}
