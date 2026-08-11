package jit

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func emit(op bytecode.Opcode, operand uint32) []byte {
	return []byte{byte(op), byte(operand >> 16), byte(operand >> 8), byte(operand)}
}

func template(code ...[]byte) *bytecode.FuncTemplate {
	var flat []byte
	for _, in := range code {
		flat = append(flat, in...)
	}
	return &bytecode.FuncTemplate{
		NumParams: 2, NumLocals: 3, ArgumentsSlot: 3,
		NoArgumentsObject: true, Code: flat,
	}
}

func TestCompileLeafAndExecute(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpMul, 0), emit(bytecode.OpPushInt, 2),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpReturn, 0),
	)
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(3), engine.Number(4)})
	if err != nil || reason != Executed {
		t.Fatalf("execute: reason=%v err=%v", reason, err)
	}
	n, ok := got.Float()
	if !ok || n != 14 {
		t.Fatalf("got %v, want 14", got)
	}
}

func TestExecuteComparisonReturnsBoolean(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpLt, 0), emit(bytecode.OpReturn, 0),
	)
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(1), engine.Number(2)})
	if err != nil || reason != Executed || got.Type() != engine.TypeBoolean || got.String() != "true" {
		t.Fatalf("got=%v type=%v reason=%v err=%v", got, got.Type(), reason, err)
	}
}

func TestExecuteLogicalKeepBranchesPreserveNumberValue(t *testing.T) {
	for _, tt := range []struct {
		name string
		op   bytecode.Opcode
		args []float64
		want []float64
	}{
		{
			name: "and", op: bytecode.OpJmpFalseKeep,
			args: []float64{0, math.Copysign(0, -1), math.NaN(), 2},
			want: []float64{0, math.Copysign(0, -1), math.NaN(), 7},
		},
		{
			name: "or", op: bytecode.OpJmpTrueKeep,
			args: []float64{0, math.Copysign(0, -1), math.NaN(), 2},
			want: []float64{7, 7, 7, 2},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template(
				emit(bytecode.OpLoadLocal, 1), emit(tt.op, 4),
				emit(bytecode.OpPushInt, 7), emit(bytecode.OpReturn, 0),
			)
			p, err := CompileLeaf(tmpl)
			if err != nil {
				t.Fatal(err)
			}
			for i, input := range tt.args {
				got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(input)})
				if err != nil || reason != Executed {
					t.Fatalf("input=%v got=%v reason=%v err=%v", input, got, reason, err)
				}
				number, ok := got.Float()
				if !ok || math.IsNaN(number) != math.IsNaN(tt.want[i]) ||
					!math.IsNaN(number) && math.Float64bits(number) != math.Float64bits(tt.want[i]) {
					t.Fatalf("input=%v got=%v bits=%x want=%v bits=%x", input, number, math.Float64bits(number), tt.want[i], math.Float64bits(tt.want[i]))
				}
			}
		})
	}
}

func TestExecuteNullishKeepDistinguishesNullishAndReferenceValues(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpJmpNullishKeep, 4),
		emit(bytecode.OpPushInt, 7), emit(bytecode.OpReturn, 0),
	)
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	object := engine.NewObject()
	emptyString := engine.Str("")
	stringValue := engine.Str("value")
	zeroBigInt := engine.BigIntZero()
	bigIntValue := engine.BigIntFromInt(7)
	for _, tt := range []struct {
		name string
		args []engine.Value
		want engine.Value
	}{
		{name: "missing", want: engine.Number(7)},
		{name: "undefined", args: []engine.Value{engine.Undefined()}, want: engine.Number(7)},
		{name: "null", args: []engine.Value{engine.Null()}, want: engine.Number(7)},
		{name: "zero", args: []engine.Value{engine.Number(0)}, want: engine.Number(0)},
		{name: "negative-zero", args: []engine.Value{engine.Number(math.Copysign(0, -1))}, want: engine.Number(math.Copysign(0, -1))},
		{name: "false", args: []engine.Value{engine.Boolean(false)}, want: engine.Boolean(false)},
		{name: "object", args: []engine.Value{object}, want: object},
		{name: "empty-string", args: []engine.Value{emptyString}, want: emptyString},
		{name: "string", args: []engine.Value{stringValue}, want: stringValue},
		{name: "zero-bigint", args: []engine.Value{zeroBigInt}, want: zeroBigInt},
		{name: "bigint", args: []engine.Value{bigIntValue}, want: bigIntValue},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := p.Execute(engine.Undefined(), tt.args)
			if err != nil || reason != Executed {
				t.Fatalf("got=%v reason=%v err=%v", got, reason, err)
			}
			if got.Type() != tt.want.Type() || got.Type() == engine.TypeNumber && math.Float64bits(mustJITFloat(got)) != math.Float64bits(mustJITFloat(tt.want)) ||
				got.Type() != engine.TypeNumber && got != tt.want {
				t.Fatalf("got=%v type=%v want=%v type=%v", got, got.Type(), tt.want, tt.want.Type())
			}
		})
	}
}

func TestExecuteStrictEqualityAcrossQuickValues(t *testing.T) {
	object := engine.NewObject()
	otherObject := engine.NewObject()
	cases := []struct {
		name        string
		left, right engine.Value
		equal       bool
	}{
		{name: "undefined", left: engine.Undefined(), right: engine.Undefined(), equal: true},
		{name: "null", left: engine.Null(), right: engine.Null(), equal: true},
		{name: "null-undefined", left: engine.Null(), right: engine.Undefined()},
		{name: "boolean", left: engine.Boolean(false), right: engine.Boolean(false), equal: true},
		{name: "boolean-number", left: engine.Boolean(false), right: engine.Number(0)},
		{name: "zero-negative-zero", left: engine.Number(0), right: engine.Number(math.Copysign(0, -1)), equal: true},
		{name: "nan", left: engine.Number(math.NaN()), right: engine.Number(math.NaN())},
		{name: "string", left: engine.Str("same"), right: engine.Str("same"), equal: true},
		{name: "different-string", left: engine.Str("left"), right: engine.Str("right")},
		{name: "bigint", left: engine.BigIntFromInt(7), right: engine.BigIntFromInt(7), equal: true},
		{name: "different-bigint", left: engine.BigIntFromInt(7), right: engine.BigIntFromInt(8)},
		{name: "bigint-number", left: engine.BigIntFromInt(7), right: engine.Number(7)},
		{name: "object-identity", left: object, right: object, equal: true},
		{name: "different-object", left: object, right: otherObject},
	}
	for _, opCase := range []struct {
		name   string
		op     bytecode.Opcode
		invert bool
	}{
		{name: "equal", op: bytecode.OpStrictEq},
		{name: "not-equal", op: bytecode.OpStrictNe, invert: true},
	} {
		t.Run(opCase.name, func(t *testing.T) {
			p, err := CompileLeaf(template(
				emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
				emit(opCase.op, 0), emit(bytecode.OpReturn, 0),
			))
			if err != nil {
				t.Fatal(err)
			}
			for _, tt := range cases {
				t.Run(tt.name, func(t *testing.T) {
					got, reason, err := p.Execute(engine.Undefined(), []engine.Value{tt.left, tt.right})
					if err != nil || reason != Executed {
						t.Fatalf("got=%v reason=%v err=%v", got, reason, err)
					}
					value, ok := got.Bool()
					want := tt.equal != opCase.invert
					if !ok || value != want {
						t.Fatalf("got=%v want=%v", got, want)
					}
				})
			}
		})
	}
}

func mustJITFloat(value engine.Value) float64 {
	number, _ := value.Float()
	return number
}

func TestExecuteNotEqualTreatsNaNAsUnequal(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpNe, 0), emit(bytecode.OpReturn, 0),
	)
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name        string
		left, right float64
		want        bool
	}{
		{name: "equal", left: 4, right: 4, want: false},
		{name: "different", left: 4, right: 5, want: true},
		{name: "nan", left: math.NaN(), right: math.NaN(), want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(tt.left), engine.Number(tt.right)})
			if err != nil || reason != Executed {
				t.Fatalf("reason=%v err=%v", reason, err)
			}
			value, ok := got.Bool()
			if !ok || value != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestExecuteGuardFailure(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpReturn, 0),
	)
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]engine.Value{{engine.Str("1")}, nil} {
		_, reason, err := p.Execute(engine.Undefined(), args)
		if err != nil || reason != GuardFailed {
			t.Fatalf("reason=%v err=%v", reason, err)
		}
	}
}

func TestCompileLeafRejectsUnsupported(t *testing.T) {
	tmpl := template(emit(bytecode.OpLoadGlobal, 0), emit(bytecode.OpReturn, 0))
	if _, err := CompileLeaf(tmpl); err == nil {
		t.Fatal("expected unsupported opcode rejection")
	}
}

func TestVerifyRejectsNonEmptyTraceExitStack(t *testing.T) {
	p := &Program{Code: []Instr{{Op: OpConst, Value: 1}, {Op: OpTraceExit}}}
	if err := p.Verify(); err == nil {
		t.Fatal("expected non-empty trace exit stack rejection")
	}
}

func TestVerifyRejectsLogicalKeepBranchWithMismatchedJoinDepth(t *testing.T) {
	p := &Program{Code: []Instr{
		{Op: OpConst, Value: 1},
		{Op: OpJumpFalseKeep, Operand: 4},
		{Op: OpConst, Value: 2},
		{Op: OpConst, Value: 3},
		{Op: OpReturn},
	}}
	if err := p.Verify(); err == nil || !strings.Contains(err.Error(), "inconsistent stack depth") {
		t.Fatalf("Verify error=%v, want inconsistent stack depth", err)
	}
}

// TestVerifyRejectsInvalidDeoptMaps covers the R1-4 requirement that the
// verifier rejects missing, ambiguous, out-of-range and invalid deopt maps
// (trace exits without a valid entry in traceExitDepths).
func TestVerifyRejectsInvalidDeoptMaps(t *testing.T) {
	tests := []struct {
		name   string
		code   []Instr
		depths []uint8
		want   string // substring of the expected error; "" means Verify passes
	}{
		{
			// Missing map: an OpTraceExit with no traceExitDepths at all must
			// be rejected even though the operand stack is empty.
			name: "missing deopt map at empty stack",
			code: []Instr{{Op: OpConst, Value: 1}, {Op: OpPop}, {Op: OpTraceExit}},
			want: "no deopt map",
		},
		{
			// Out-of-range exit ID (no entry for exit 5).
			name:   "out of range exit id",
			code:   []Instr{{Op: OpConst, Value: 1}, {Op: OpPop}, {Op: OpTraceExit, Operand: 5}},
			depths: []uint8{1},
			want:   "no deopt map",
		},
		{
			// Negative exit ID encoded in the operand.
			name:   "negative exit id",
			code:   []Instr{{Op: OpConst, Value: 1}, {Op: OpPop}, {Op: OpTraceExit, Operand: 0xFFFFFFFF}},
			depths: []uint8{1},
			want:   "no deopt map",
		},
		{
			// Ambiguous: the same exit ID is reached at two different stack
			// depths on reachable paths.
			name: "ambiguous exit depth",
			code: []Instr{
				{Op: OpConst, Value: 1},
				{Op: OpJumpFalseKeep, Operand: 5}, // fallthrough depth 1; jump depth 2
				{Op: OpTraceExit, Operand: 0},     // exit 0 at depth 1
				{Op: OpConst, Value: 9},
				{Op: OpConst, Value: 9},
				{Op: OpTraceExit, Operand: 0}, // exit 0 at depth 2 -> mismatch
			},
			depths: []uint8{^uint8(0)},
			want:   "depth mismatch",
		},
		{
			// Valid: one exit reached at a consistent depth.
			name:   "valid deopt map",
			code:   []Instr{{Op: OpConst, Value: 1}, {Op: OpTraceExit}},
			depths: []uint8{^uint8(0)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Program{Code: tc.code, traceExitDepths: tc.depths}
			err := p.Verify()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Verify = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestTraceBudgetYieldsCompletedIterations(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpLt, 0), emit(bytecode.OpJmpFalsePop, 20),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpStoreLocal, 1),
		emit(bytecode.OpJmp, (1<<24)-36), emit(bytecode.OpLoadLocal, 1),
		emit(bytecode.OpReturn, 0),
	)
	trace, err := CompileTrace(tmpl, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), engine.Number(0), engine.Number(10)}
	pc, reason, err := trace.ExecuteBudget(locals, 3)
	if err != nil || reason != Yielded || pc != 0 || locals[1].String() != "3" {
		t.Fatalf("first slice: pc=%d reason=%v err=%v local=%v", pc, reason, err, locals[1])
	}
	pc, reason, err = trace.Execute(locals)
	if err != nil || reason != Executed || pc != 36 || locals[1].String() != "10" {
		t.Fatalf("completion: pc=%d reason=%v err=%v local=%v", pc, reason, err, locals[1])
	}
}

func TestTraceSupportsMultiplePreciseDeoptExits(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpLt, 0), emit(bytecode.OpJmpFalsePop, 36),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 1),
		emit(bytecode.OpAdd, 0), emit(bytecode.OpStoreLocal, 1),
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpPushInt, 2),
		emit(bytecode.OpEq, 0), emit(bytecode.OpJmpTruePop, 8),
		emit(bytecode.OpJmp, (1<<24)-52),
		emit(bytecode.OpReturnUndef, 0), emit(bytecode.OpReturnUndef, 0),
	)
	trace, err := CompileTrace(tmpl, 0, 48)
	if err != nil {
		t.Fatal(err)
	}
	wantExits := []DeoptExit{
		{ID: 0, ResumePC: 52, LocalSlots: []uint16{1}},
		{ID: 1, ResumePC: 56, LocalSlots: []uint16{1}},
	}
	if got := trace.DeoptExits(); !reflect.DeepEqual(got, wantExits) {
		t.Fatalf("deopt exits=%+v want=%+v", got, wantExits)
	}

	firstLocals := []engine.Value{engine.Undefined(), engine.Number(10), engine.Number(5)}
	first, reason, err := trace.ExecuteBudgetDetailed(firstLocals, 0)
	if err != nil || reason != Executed || first.ID != 0 || first.ResumePC != 52 || firstLocals[1].String() != "10" {
		t.Fatalf("first exit=%+v reason=%v err=%v locals=%v", first, reason, err, firstLocals)
	}
	secondLocals := []engine.Value{engine.Undefined(), engine.Number(1), engine.Number(5)}
	second, reason, err := trace.ExecuteBudgetDetailed(secondLocals, 0)
	if err != nil || reason != Executed || second.ID != 1 || second.ResumePC != 56 || secondLocals[1].String() != "2" {
		t.Fatalf("second exit=%+v reason=%v err=%v locals=%v", second, reason, err, secondLocals)
	}
}

func externalStackTraceTemplate() *bytecode.FuncTemplate {
	return template(
		emit(bytecode.OpLoadLocal, 1),
		emit(bytecode.OpJmpFalseKeep, 16),
		emit(bytecode.OpPushInt, 7),
		emit(bytecode.OpStoreLocal, 2),
		emit(bytecode.OpJmp, (1<<24)-20),
		emit(bytecode.OpReturnUndef, 0),
		emit(bytecode.OpStoreLocal, 2),
		emit(bytecode.OpReturnUndef, 0),
	)
}

func TestTraceExitRestoresExternalOperandStack(t *testing.T) {
	trace, err := CompileTrace(externalStackTraceTemplate(), 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	exits := trace.DeoptExits()
	if len(exits) != 1 || exits[0].ResumePC != 24 || exits[0].StackDepth != 1 || exits[0].StackValues != nil {
		t.Fatalf("deopt exits=%+v", exits)
	}
	for _, input := range []float64{0, math.Copysign(0, -1), math.NaN()} {
		locals := []engine.Value{engine.Undefined(), engine.Number(input), engine.Number(0)}
		exit, reason, err := trace.ExecuteBudgetDetailed(locals, 1)
		if err != nil || reason != Executed || exit.ResumePC != 24 || len(exit.StackValues) != 1 {
			t.Fatalf("input=%v exit=%+v reason=%v err=%v", input, exit, reason, err)
		}
		got, _ := exit.StackValues[0].Float()
		if math.IsNaN(input) && math.IsNaN(got) {
			continue
		}
		if math.Float64bits(got) != math.Float64bits(input) {
			t.Fatalf("input=%v bits=%x restored=%v bits=%x", input, math.Float64bits(input), got, math.Float64bits(got))
		}
	}
}

func TestNumberEdgeCases(t *testing.T) {
	tmpl := template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(bytecode.OpDiv, 0), emit(bytecode.OpReturn, 0),
	)
	p, err := CompileLeaf(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(0), engine.Number(0)})
	if err != nil || reason != Executed {
		t.Fatalf("reason=%v err=%v", reason, err)
	}
	n, _ := got.Float()
	if !math.IsNaN(n) {
		t.Fatalf("got %v, want NaN", n)
	}
}
