package jit

import (
	"math"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// controlTemplate builds a FuncTemplate with the given signature and code.
func controlTemplate(numParams, numLocals int, code ...[]byte) *bytecode.FuncTemplate {
	var flat []byte
	for _, in := range code {
		flat = append(flat, in...)
	}
	return &bytecode.FuncTemplate{
		NumParams: numParams, NumLocals: numLocals,
		ArgumentsSlot: -1, NoArgumentsObject: true, Code: flat,
	}
}

// emitSigned encodes a jump with an explicit instruction pc so the operand is
// the signed delta (target - (pc + InstrSize)).
func emitSigned(op bytecode.Opcode, pc, target int) []byte {
	return emit(op, uint32(target-(pc+bytecode.InstrSize)))
}

// ternaryTemplate lowers `f(a, b, c) { return a ? b : c; }`:
//
//	0000 LOAD_LOCAL 1   ; a
//	0004 JMP_FALSE_POP -> 0016
//	0008 LOAD_LOCAL 2   ; b
//	0012 JMP -> 0020
//	0016 LOAD_LOCAL 3   ; c
//	0020 RETURN
func ternaryTemplate() *bytecode.FuncTemplate {
	return controlTemplate(3, 4,
		emit(bytecode.OpLoadLocal, 1), emitSigned(bytecode.OpJmpFalsePop, 4, 16),
		emit(bytecode.OpLoadLocal, 2), emitSigned(bytecode.OpJmp, 12, 20),
		emit(bytecode.OpLoadLocal, 3), emit(bytecode.OpReturn, 0))
}

// integerSwitchTemplate lowers the compiler's canonical integer switch
// (`switch (x) { case 1: return 10; case 2: return 20; default: return 30; }`):
// a strict-equality jump chain with per-case bodies and an implicit trailing
// return_undef after the final return (which the lowering must trim).
func integerSwitchTemplate() *bytecode.FuncTemplate {
	return controlTemplate(1, 2,
		emit(bytecode.OpLoadLocal, 1),                            // pc0
		emit(bytecode.OpPushInt, 1),                              // pc4
		emit(bytecode.OpStrictEq, 0),                             // pc8
		emitSigned(bytecode.OpJmpTruePop, 12, 40),                // pc12 -> case 1 body
		emit(bytecode.OpLoadLocal, 1),                            // pc16
		emit(bytecode.OpPushInt, 2),                              // pc20
		emit(bytecode.OpStrictEq, 0),                             // pc24
		emitSigned(bytecode.OpJmpTruePop, 28, 48),                // pc28 -> case 2 body
		emitSigned(bytecode.OpJmp, 32, 56),                       // pc32 -> default body
		emitSigned(bytecode.OpJmp, 36, 64),                       // pc36 -> fall-through (unreachable)
		emit(bytecode.OpPushInt, 10), emit(bytecode.OpReturn, 0), // pc40, pc44
		emit(bytecode.OpPushInt, 20), emit(bytecode.OpReturn, 0), // pc48, pc52
		emit(bytecode.OpPushInt, 30), emit(bytecode.OpReturn, 0), // pc56, pc60
		emit(bytecode.OpReturnUndef, 0), // pc64 trailing implicit return
	)
}

// stringSwitchTemplate lowers `switch (x) { case "a": return 1; case "b":
// return 2; default: return 3; }` where the case tests are string constants.
func stringSwitchTemplate() *bytecode.FuncTemplate {
	tmpl := controlTemplate(1, 2,
		emit(bytecode.OpLoadLocal, 1),                           // pc0
		emit(bytecode.OpPushConst, 0),                           // pc4 ("a")
		emit(bytecode.OpStrictEq, 0),                            // pc8
		emitSigned(bytecode.OpJmpTruePop, 12, 40),               // pc12 -> case "a" body
		emit(bytecode.OpLoadLocal, 1),                           // pc16
		emit(bytecode.OpPushConst, 1),                           // pc20 ("b")
		emit(bytecode.OpStrictEq, 0),                            // pc24
		emitSigned(bytecode.OpJmpTruePop, 28, 48),               // pc28 -> case "b" body
		emitSigned(bytecode.OpJmp, 32, 56),                      // pc32 -> default body
		emitSigned(bytecode.OpJmp, 36, 64),                      // pc36 -> fall-through (unreachable)
		emit(bytecode.OpPushInt, 1), emit(bytecode.OpReturn, 0), // pc40, pc44
		emit(bytecode.OpPushInt, 2), emit(bytecode.OpReturn, 0), // pc48, pc52
		emit(bytecode.OpPushInt, 3), emit(bytecode.OpReturn, 0), // pc56, pc60
		emit(bytecode.OpReturnUndef, 0), // pc64
	)
	tmpl.Constants = []engine.Value{engine.Str("a"), engine.Str("b")}
	return tmpl
}

// shortCircuitTemplate lowers `f(a, b, c, d) { return a && b || c && d; }`
// exactly as the compiler's compileLogical does (inner && patches its jump to
// the position before the outer ||'s keep jump):
//
//	pc0  LOAD 1 (a)
//	pc4  JMP_FALSE_KEEP -> pc12 (skip b when a falsy, keep a)
//	pc8  LOAD 2 (b)
//	pc12 JMP_TRUE_KEEP -> pc28 (a&&b truthy -> keep, skip c&&d)
//	pc16 LOAD 3 (c)
//	pc20 JMP_FALSE_KEEP -> pc28 (skip d when c falsy, keep c)
//	pc24 LOAD 4 (d)
//	pc28 RETURN
func shortCircuitTemplate() *bytecode.FuncTemplate {
	return controlTemplate(4, 5,
		emit(bytecode.OpLoadLocal, 1), emitSigned(bytecode.OpJmpFalseKeep, 4, 12),
		emit(bytecode.OpLoadLocal, 2), emitSigned(bytecode.OpJmpTrueKeep, 12, 28),
		emit(bytecode.OpLoadLocal, 3), emitSigned(bytecode.OpJmpFalseKeep, 20, 28),
		emit(bytecode.OpLoadLocal, 4), emit(bytecode.OpReturn, 0))
}

func TestCompileLeafTernary(t *testing.T) {
	p, err := CompileLeaf(ternaryTemplate())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []engine.Value
		want float64
	}{
		{args: []engine.Value{engine.Number(1), engine.Number(2), engine.Number(3)}, want: 2},
		{args: []engine.Value{engine.Number(0), engine.Number(2), engine.Number(3)}, want: 3},
		{args: []engine.Value{engine.Boolean(false), engine.Number(2), engine.Number(3)}, want: 3},
		{args: []engine.Value{engine.Undefined(), engine.Number(2), engine.Number(3)}, want: 3},
	} {
		got, reason, err := p.Execute(engine.Undefined(), tt.args)
		if err != nil || reason != Executed {
			t.Fatalf("args=%v reason=%v err=%v", tt.args, reason, err)
		}
		n, ok := got.Float()
		if !ok || n != tt.want {
			t.Fatalf("args=%v got=%v want=%v", tt.args, got, tt.want)
		}
	}
}

func TestCompileLeafIntegerSwitchIfElseChain(t *testing.T) {
	p, err := CompileLeaf(integerSwitchTemplate())
	if err != nil {
		t.Fatal(err)
	}
	// The switch must lower to a strict-equality jump chain whose case bodies
	// merge at consistent stack depths; the implicit trailing return_undef
	// must be trimmed so the program ends at the final return.
	if len(p.Code) == 0 || p.Code[len(p.Code)-1].Op != OpReturn {
		t.Fatalf("IR did not end at the final return:\n%s", p.DumpIR())
	}
	for _, tt := range []struct {
		input float64
		want  float64
	}{
		{input: 1, want: 10},
		{input: 2, want: 20},
		{input: 3, want: 30},
		{input: -5, want: 30},
		{input: math.NaN(), want: 30},
	} {
		got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(tt.input)})
		if err != nil || reason != Executed {
			t.Fatalf("input=%v reason=%v err=%v", tt.input, reason, err)
		}
		n, ok := got.Float()
		if !ok || n != tt.want {
			t.Fatalf("input=%v got=%v want=%v", tt.input, got, tt.want)
		}
	}
}

func TestCompileLeafStringSwitch(t *testing.T) {
	p, err := CompileLeaf(stringSwitchTemplate())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		input string
		want  float64
	}{
		{input: "a", want: 1},
		{input: "b", want: 2},
		{input: "z", want: 3},
		{input: "ab", want: 3},
	} {
		got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Str(tt.input)})
		if err != nil || reason != Executed {
			t.Fatalf("input=%q reason=%v err=%v", tt.input, reason, err)
		}
		n, ok := got.Float()
		if !ok || n != tt.want {
			t.Fatalf("input=%q got=%v want=%v", tt.input, got, tt.want)
		}
	}
}

func TestCompileLeafNestedShortCircuit(t *testing.T) {
	p, err := CompileLeaf(shortCircuitTemplate())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []float64
		want float64
	}{
		{args: []float64{1, 2, 0, 4}, want: 2},          // a&&b short-circuits c&&d
		{args: []float64{0, 2, 3, 4}, want: 4},          // a falsy -> c&&d
		{args: []float64{1, 0, 3, 0}, want: 0},          // a&&b falsy (0), c&&d falsy (0)
		{args: []float64{0, 2, 0, 4}, want: 0},          // a falsy (0 kept), c falsy (0 kept)
		{args: []float64{math.NaN(), 2, 3, 4}, want: 4}, // NaN falsy
	} {
		got, reason, err := p.Execute(engine.Undefined(), []engine.Value{
			engine.Number(tt.args[0]), engine.Number(tt.args[1]),
			engine.Number(tt.args[2]), engine.Number(tt.args[3])})
		if err != nil || reason != Executed {
			t.Fatalf("args=%v reason=%v err=%v", tt.args, reason, err)
		}
		n, ok := got.Float()
		if !ok || n != tt.want {
			t.Fatalf("args=%v got=%v want=%v", tt.args, got, tt.want)
		}
	}
}

// TestVerifyRejectsSwitchInconsistentMerge builds a switch-shaped CFG whose
// case bodies leave different operand-stack depths at the shared join: the
// verifier must reject the inconsistent merge even though each case body is
// individually well-formed.
func TestVerifyRejectsSwitchInconsistentMerge(t *testing.T) {
	p := &Program{Code: []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpJumpFalse, Operand: 4}, // case 1: no match -> default body
		{Op: OpConst, Value: 10},      // case 1 body: leaves 1 value
		{Op: OpJump, Operand: 6},      // join
		{Op: OpConst, Value: 30},      // default body: leaves 1 value
		{Op: OpConst, Value: 40},      // extra value -> join depth 2 vs 1
		{Op: OpReturn},
	}}
	err := p.Verify()
	if err == nil || !strings.Contains(err.Error(), "inconsistent stack depth") {
		t.Fatalf("Verify = %v, want inconsistent stack depth", err)
	}
}

// TestVerifyRejectsTernaryKeepMerge builds a ternary lowered with a keep
// branch where the jump path keeps the condition while the fallthrough path
// leaves a different depth at the join.
func TestVerifyRejectsTernaryKeepMerge(t *testing.T) {
	p := &Program{Code: []Instr{
		{Op: OpConst, Value: 1},
		{Op: OpJumpTrueKeep, Operand: 4}, // jump keeps depth 1 at the join
		{Op: OpConst, Value: 2},
		{Op: OpConst, Value: 3}, // fallthrough depth 2 -> mismatch
		{Op: OpReturn},
	}}
	err := p.Verify()
	if err == nil || !strings.Contains(err.Error(), "inconsistent stack depth") {
		t.Fatalf("Verify = %v, want inconsistent stack depth", err)
	}
}

func TestVerifyRejectsStringConstantOutOfRange(t *testing.T) {
	p := &Program{Code: []Instr{{Op: OpConstString, Operand: 0}, {Op: OpReturn}}}
	if err := p.Verify(); err == nil || !strings.Contains(err.Error(), "string constant index out of range") {
		t.Fatalf("Verify = %v, want string constant index rejection", err)
	}
	p = &Program{Code: []Instr{{Op: OpConstString, Operand: 5}, {Op: OpReturn}}, stringConsts: []engine.Value{engine.Str("a")}}
	if err := p.Verify(); err == nil || !strings.Contains(err.Error(), "string constant index out of range") {
		t.Fatalf("Verify = %v, want string constant index rejection", err)
	}
}

func TestVerifyRejectsOversizedStringPool(t *testing.T) {
	pool := make([]engine.Value, maxQuickSlots+1)
	for i := range pool {
		pool[i] = engine.Str("x")
	}
	p := &Program{Code: []Instr{{Op: OpConstString, Operand: 0}, {Op: OpReturn}}, stringConsts: pool}
	if err := p.Verify(); err == nil || !strings.Contains(err.Error(), "string constant pool is too large") {
		t.Fatalf("Verify = %v, want pool rejection", err)
	}
}

// TestCompileLeafTrimsUnreachableTail verifies that the implicit trailing
// return_undef (and any dead code after the final return) is dropped, so the
// IR shape matches the canonical single-return form that inline targets and
// trivial-getter detection rely on.
func TestCompileLeafTrimsUnreachableTail(t *testing.T) {
	tmpl := controlTemplate(1, 2,
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpReturn, 0),
		emit(bytecode.OpReturnUndef, 0))
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Code) != 4 || p.Code[len(p.Code)-1].Op != OpReturn {
		t.Fatalf("IR not trimmed:\n%s", p.DumpIR())
	}
	if p.IsTrivialThisPropertyGetter() {
		t.Fatal("numeric leaf misdetected as trivial getter")
	}
}

// TestCompileTraceSwitchInLoop compiles a trace whose loop body contains a
// switch; the switch's per-case jumps stay inside the trace while the loop
// exit jump leaves the range, so the exit must carry a precise deopt map and
// the restored operand stack must be empty (the loop header starts with an
// empty stack).
func TestCompileTraceSwitchInLoop(t *testing.T) {
	// for (i = 0; i < n; i++) { switch (i) { case 0: i += 2; break;
	// case 1: i += 3; break; default: i += 1; } }
	// Locals: 1 = i, 2 = n. Trace range covers the header .. backedge (pc108).
	tmpl := controlTemplate(2, 3,
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpLt, 0), // 0,4,8
		emitSigned(bytecode.OpJmpFalsePop, 12, 112),                                              // loop exit -> out of range
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 0), emit(bytecode.OpStrictEq, 0), // 16,20,24
		emitSigned(bytecode.OpJmpTruePop, 28, 52),                                                // case 0 body
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1), emit(bytecode.OpStrictEq, 0), // 32,36,40
		emitSigned(bytecode.OpJmpTruePop, 44, 72),                                           // case 1 body
		emitSigned(bytecode.OpJmp, 48, 92),                                                  // default body
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 2), emit(bytecode.OpAdd, 0), // 52,56,60
		emit(bytecode.OpStoreLocal, 1), emitSigned(bytecode.OpJmp, 68, 108), // case 0 -> backedge
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 3), emit(bytecode.OpAdd, 0), // 72,76,80
		emit(bytecode.OpStoreLocal, 1), emitSigned(bytecode.OpJmp, 88, 108), // case 1 -> backedge
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1), emit(bytecode.OpAdd, 0), // 92,96,100
		emit(bytecode.OpStoreLocal, 1),                            // 104
		emitSigned(bytecode.OpJmp, 108, 0),                        // backedge -> loop header
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpReturn, 0), // 112, 116
	)
	trace, err := CompileTrace(tmpl, 0, 108)
	if err != nil {
		t.Fatal(err)
	}
	exits := trace.DeoptExits()
	if len(exits) != 1 {
		t.Fatalf("deopt exits=%+v, want exactly the loop-exit exit", exits)
	}
	if exits[0].ResumePC != 112 || exits[0].StackDepth != 0 {
		t.Fatalf("loop-exit deopt map=%+v, want ResumePC=112 depth=0", exits[0])
	}
	// i = 0: case 0 -> i = 2; then 2 !== 1 -> default: i = 3; exit at i = 3.
	locals := []engine.Value{engine.Undefined(), engine.Number(0), engine.Number(3)}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed {
		t.Fatalf("exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if exit.ResumePC != 112 || locals[1].String() != "3" {
		t.Fatalf("exit=%+v locals=%v", exit, locals)
	}
}

// TestCompileTraceNestedShortCircuit compiles a loop body containing nested
// && / || keep branches (`if (a && b || !a) s += 1;`); the merged depth at
// the backedge must stay consistent and the loop-exit exit must carry an
// empty operand stack.
func TestCompileTraceNestedShortCircuit(t *testing.T) {
	// Locals: 1 = i, 2 = n, 3 = a, 4 = b, 5 = s. Range covers pc0..pc76.
	tmpl := controlTemplate(3, 6,
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpLt, 0), // 0,4,8
		emitSigned(bytecode.OpJmpFalsePop, 12, 80),                // loop exit -> out of range
		emit(bytecode.OpLoadLocal, 3),                             // 16 (a)
		emitSigned(bytecode.OpJmpFalseKeep, 20, 28),               // a falsy -> keep a, jump to J1
		emit(bytecode.OpLoadLocal, 4),                             // 24 (b)
		emitSigned(bytecode.OpJmpTrueKeep, 28, 40),                // J1: (a&&b) truthy -> keep, skip !a
		emit(bytecode.OpLoadLocal, 3),                             // 32 (!a)
		emit(bytecode.OpNot, 0),                                   // 36
		emitSigned(bytecode.OpJmpFalsePop, 40, 60),                // J2: condition falsy -> skip body
		emit(bytecode.OpLoadLocal, 5),                             // 44 (s)
		emit(bytecode.OpPushInt, 1),                               // 48
		emit(bytecode.OpAdd, 0),                                   // 52
		emit(bytecode.OpStoreLocal, 5),                            // 56
		emit(bytecode.OpLoadLocal, 1),                             // 60 (i++)
		emit(bytecode.OpPushInt, 1),                               // 64
		emit(bytecode.OpAdd, 0),                                   // 68
		emit(bytecode.OpStoreLocal, 1),                            // 72
		emitSigned(bytecode.OpJmp, 76, 0),                         // backedge -> header
		emit(bytecode.OpLoadLocal, 5), emit(bytecode.OpReturn, 0), // 80, 84
	)
	trace, err := CompileTrace(tmpl, 0, 76)
	if err != nil {
		t.Fatal(err)
	}
	exits := trace.DeoptExits()
	if len(exits) != 1 || exits[0].ResumePC != 80 || exits[0].StackDepth != 0 {
		t.Fatalf("deopt exits=%+v", exits)
	}
	// a = 0, b = 1: (0 && 1) || !0 is always truthy -> every iteration adds 1.
	locals := []engine.Value{engine.Undefined(), engine.Number(0), engine.Number(5), engine.Number(0), engine.Number(1), engine.Number(0)}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed || exit.ResumePC != 80 {
		t.Fatalf("exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if locals[5].String() != "5" {
		t.Fatalf("accumulated s=%v, want 5", locals[5])
	}
}

// TestCompileTraceStringSwitch compiles a loop body whose switch compares a
// string constant; the string pool must be shared with the string locals so
// strict equality resolves by value, and the numeric default path must exit
// through the loop-exit deopt map.
func TestCompileTraceStringSwitch(t *testing.T) {
	// Locals: 1 = i, 2 = n. Both switch bodies merge into the shared i++
	// section; the loop exit is the only out-of-range jump. Range pc0..pc52.
	tmpl := controlTemplate(2, 3,
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpLt, 0), // 0,4,8
		emitSigned(bytecode.OpJmpFalsePop, 12, 56),                // loop exit -> out of range
		emit(bytecode.OpLoadLocal, 1),                             // 16
		emit(bytecode.OpPushConst, 0),                             // 20 ("a")
		emit(bytecode.OpStrictEq, 0),                              // 24
		emitSigned(bytecode.OpJmpTruePop, 28, 36),                 // case "a" -> merge
		emitSigned(bytecode.OpJmp, 32, 36),                        // default -> merge
		emit(bytecode.OpLoadLocal, 1),                             // 36 (i++)
		emit(bytecode.OpPushInt, 1),                               // 40
		emit(bytecode.OpAdd, 0),                                   // 44
		emit(bytecode.OpStoreLocal, 1),                            // 48
		emitSigned(bytecode.OpJmp, 52, 0),                         // backedge -> header
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpReturn, 0), // 56, 60
	)
	tmpl.Constants = []engine.Value{engine.Str("a")}
	trace, err := CompileTrace(tmpl, 0, 52)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.program.stringConsts) != 1 || trace.program.stringConsts[0] != engine.Str("a") {
		t.Fatalf("string pool=%v", trace.program.stringConsts)
	}
	// Numeric i never matches "a": every iteration takes the default body.
	locals := []engine.Value{engine.Undefined(), engine.Number(1), engine.Number(4)}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed || exit.ResumePC != 56 || locals[1].String() != "4" {
		t.Fatalf("exit=%+v reason=%v err=%v locals=%v", exit, reason, err, locals)
	}
}

// TestRejectLeafReasonAgreesWithCompileLeaf is the R3-7 drift gate: the cheap
// candidate filter must never reject a template that CompileLeaf accepts, and
// every filter rejection must also be a CompileLeaf rejection with the same
// reason.
func TestRejectLeafReasonAgreesWithCompileLeaf(t *testing.T) {
	templates := []*bytecode.FuncTemplate{
		ternaryTemplate(),
		integerSwitchTemplate(),
		stringSwitchTemplate(),
		shortCircuitTemplate(),
		controlTemplate(2, 3,
			emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
			emit(bytecode.OpMul, 0), emit(bytecode.OpReturn, 0)),
		// Rejected by both: unsupported opcode.
		controlTemplate(1, 2, emit(bytecode.OpLoadGlobal, 0), emit(bytecode.OpReturn, 0)),
		// Rejected by both: try region.
		controlTemplate(1, 2, emit(bytecode.OpTryEnter, 0), emit(bytecode.OpPushInt, 1), emit(bytecode.OpReturn, 0)),
		// Rejected by both: dynamic arguments object.
		{
			Name: "arguments", NumParams: 1, NumLocals: 3, ArgumentsSlot: 2,
			NoArgumentsObject: false,
			Code:              append(emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpReturn, 0)...),
		},
		// Rejected by both: unaligned jump target.
		controlTemplate(1, 2,
			emit(bytecode.OpLoadLocal, 1),
			emitSigned(bytecode.OpJmpFalsePop, 4, 9), // target 9 is unaligned
			emit(bytecode.OpPushInt, 1), emit(bytecode.OpReturn, 0),
			emit(bytecode.OpReturnUndef, 0)),
	}
	for i, tmpl := range templates {
		scanErr := RejectLeafReason(tmpl)
		program, compileErr := CompileLeaf(tmpl)
		if scanErr != nil {
			if compileErr == nil {
				t.Fatalf("template %d: pre-filter rejected (%v) but CompileLeaf accepted", i, scanErr)
			}
			if scanErr.Error() != compileErr.Error() {
				t.Fatalf("template %d: pre-filter reason %q != CompileLeaf reason %q", i, scanErr.Error(), compileErr.Error())
			}
			continue
		}
		if compileErr != nil {
			t.Fatalf("template %d: pre-filter accepted but CompileLeaf rejected: %v", i, compileErr)
		}
		if program == nil {
			t.Fatalf("template %d: accepted but no program", i)
		}
	}
}

// TestRejectTraceReasonAgreesWithCompileTrace is the trace drift gate: a
// pre-filter rejection must always be a CompileTrace rejection (one
// direction; the scan is deliberately more permissive about OpCall /
// OpCallMethod because their guards are decided at compile time).
func TestRejectTraceReasonAgreesWithCompileTrace(t *testing.T) {
	valid := controlTemplate(2, 3,
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpLt, 0), // 0,4,8
		emitSigned(bytecode.OpJmpFalsePop, 12, 36),                                          // loop exit -> out of range
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1), emit(bytecode.OpAdd, 0), // 16,20,24
		emit(bytecode.OpStoreLocal, 1),    // 28
		emitSigned(bytecode.OpJmp, 32, 0), // backedge
		emit(bytecode.OpReturnUndef, 0))   // 36 (exit resume)
	if scanErr := RejectTraceReason(valid, 0, 32); scanErr != nil {
		t.Fatalf("valid trace rejected by pre-filter: %v", scanErr)
	}
	if _, err := CompileTrace(valid, 0, 32); err != nil {
		t.Fatalf("valid trace rejected by compiler: %v", err)
	}
	bad := []struct {
		name    string
		tmpl    *bytecode.FuncTemplate
		startPC int
		endPC   int
	}{
		{
			name: "try region inside range",
			tmpl: controlTemplate(2, 3,
				emit(bytecode.OpTryEnter, 0),
				emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpLt, 0),
				emitSigned(bytecode.OpJmpFalsePop, 16, 32),
				emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1), emit(bytecode.OpAdd, 0),
				emit(bytecode.OpStoreLocal, 1),
				emitSigned(bytecode.OpJmp, 36, 4),
				emit(bytecode.OpReturnUndef, 0)),
			startPC: 0,
			endPC:   40,
		},
		{
			name: "return inside range",
			tmpl: controlTemplate(2, 3,
				emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpReturn, 0),
				emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpReturn, 0)),
			startPC: 0,
			endPC:   4,
		},
		{
			name:    "unsupported opcode",
			tmpl:    controlTemplate(2, 3, emit(bytecode.OpNewObject, 0), emit(bytecode.OpReturn, 0)),
			startPC: 0,
			endPC:   0,
		},
	}
	for _, tt := range bad {
		scanErr := RejectTraceReason(tt.tmpl, tt.startPC, tt.endPC)
		if scanErr == nil {
			t.Fatalf("%s: pre-filter accepted an unsupported range", tt.name)
		}
		if _, err := CompileTrace(tt.tmpl, tt.startPC, tt.endPC); err == nil {
			t.Fatalf("%s: pre-filter rejected but CompileTrace accepted", tt.name)
		}
	}
}

func TestRejectLeafReasonRejectsDynamicArguments(t *testing.T) {
	tmpl := &bytecode.FuncTemplate{
		Name: "args", NumParams: 1, NumLocals: 3,
		ArgumentsSlot: 2, NoArgumentsObject: false,
		Code: append(emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpReturn, 0)...),
	}
	if err := RejectLeafReason(tmpl); err == nil || err.Error() != "jit: function is not a leaf candidate" {
		t.Fatalf("RejectLeafReason = %v", err)
	}
}

func TestRejectLeafReasonRejectsTryRegion(t *testing.T) {
	tmpl := controlTemplate(1, 2, emit(bytecode.OpTryEnter, 0), emit(bytecode.OpPushInt, 1), emit(bytecode.OpReturn, 0))
	if err := RejectLeafReason(tmpl); err == nil || !strings.Contains(err.Error(), "unsupported opcode TRY_ENTER") {
		t.Fatalf("RejectLeafReason = %v", err)
	}
}

func TestRejectTraceReasonRejectsTryRegion(t *testing.T) {
	tmpl := controlTemplate(2, 3,
		emit(bytecode.OpTryEnter, 0),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2), emit(bytecode.OpLt, 0),
		emitSigned(bytecode.OpJmpFalsePop, 16, 32),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1), emit(bytecode.OpAdd, 0),
		emit(bytecode.OpStoreLocal, 1),
		emitSigned(bytecode.OpJmp, 36, 4),
		emit(bytecode.OpReturnUndef, 0))
	if err := RejectTraceReason(tmpl, 0, 40); err == nil || !strings.Contains(err.Error(), "unsupported opcode TRY_ENTER") {
		t.Fatalf("RejectTraceReason = %v", err)
	}
}

// TestTraceStringConstDeoptRestoresValue proves string values travel with the
// deopt exit: a falsy string kept by a keep-branch must be restored by value
// through the shared object buffer at the out-of-range jump.
func TestTraceStringConstDeoptRestoresValue(t *testing.T) {
	tmpl := controlTemplate(1, 2,
		emit(bytecode.OpLoadLocal, 1),              // 0 (x)
		emitSigned(bytecode.OpJmpFalseKeep, 4, 12), // x falsy -> keep x, jump to exit
		emit(bytecode.OpPushInt, 1),                // 8
	)
	trace, err := CompileTrace(tmpl, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Str("")}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed {
		t.Fatalf("exit=%+v reason=%v err=%v", exit, reason, err)
	}
	if exit.ResumePC != 12 || len(exit.StackValues) != 1 || exit.StackValues[0] != engine.Str("") {
		t.Fatalf("exit=%+v stack=%v", exit, exit.StackValues)
	}
}

// TestVerifyAgreesWithExecutorOnShortCircuitMerges runs a corpus of
// keep-branch CFGs through Verify and the Quick executor to prove the
// verifier's merge-depth rule matches execution (accepted CFGs execute
// without Malformed, rejected ones never run).
func TestVerifyAgreesWithExecutorOnShortCircuitMerges(t *testing.T) {
	cases := []struct {
		name string
		code []Instr
		want bool // true: Verify accepts and executor terminates
	}{
		{
			name: "and-chain",
			code: []Instr{
				{Op: OpConst, Value: 1},
				{Op: OpJumpFalseKeep, Operand: 3}, // falsy -> keep value, jump to return
				{Op: OpConst, Value: 2},
				{Op: OpReturn},
			},
			want: true,
		},
		{
			name: "nested-and-or",
			code: []Instr{
				{Op: OpConst, Value: 1},
				{Op: OpJumpFalseKeep, Operand: 3}, // skip (2 || 3) when falsy
				{Op: OpConst, Value: 2},
				{Op: OpJumpTrueKeep, Operand: 5}, // truthy -> keep 2, jump to return
				{Op: OpConst, Value: 3},
				{Op: OpReturn},
			},
			want: true,
		},
		{
			name: "keep-then-pop-mismatch",
			code: []Instr{
				{Op: OpConst, Value: 1},
				{Op: OpJumpTrueKeep, Operand: 4}, // jump path keeps depth 1
				{Op: OpConst, Value: 2},
				{Op: OpConst, Value: 3}, // fallthrough depth 2 -> mismatch at join
				{Op: OpReturn},
			},
			want: false,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := &Program{Code: tt.code}
			err := p.Verify()
			if err != nil != !tt.want {
				t.Fatalf("Verify = %v, want accept=%v", err, tt.want)
			}
			if err != nil {
				return
			}
			_, reason, err := p.Execute(engine.Undefined(), nil)
			if err != nil || reason != Executed {
				t.Fatalf("execute reason=%v err=%v", reason, err)
			}
		})
	}
}
