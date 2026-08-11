package jit

import (
	"math/big"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// flatCode concatenates bytecode fragments into one byte slice (multiple
// `...` spreads in a single call are not valid Go).
func flatCode(parts ...[]byte) []byte {
	var flat []byte
	for _, part := range parts {
		flat = append(flat, part...)
	}
	return flat
}

// executeBinary compiles a two-arg leaf `return l op r` and runs it.
func executeBinary(t *testing.T, op bytecode.Opcode, left, right engine.Value) (engine.Value, ExitReason, error) {
	t.Helper()
	p, err := CompileLeaf(template(
		emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
		emit(op, 0), emit(bytecode.OpReturn, 0),
	))
	if err != nil {
		t.Fatal(err)
	}
	return p.Execute(engine.Undefined(), []engine.Value{left, right})
}

func TestExecuteStringConcat(t *testing.T) {
	// R3-4: same-type String `+` must concatenate in Quick with Tier 0's
	// ConcatStrings semantics (flat short strings, empty operands, ropes for
	// long results), and mixed operands must guard back to Tier 0.
	rope := strings.Repeat("a", 40) + strings.Repeat("b", 40) // > flatConcatLimit
	cases := []struct {
		name        string
		left, right engine.Value
		want        string
	}{
		{name: "short", left: engine.Str("a"), right: engine.Str("b"), want: "ab"},
		{name: "empty-left", left: engine.Str(""), right: engine.Str("x"), want: "x"},
		{name: "empty-right", left: engine.Str("x"), right: engine.Str(""), want: "x"},
		{name: "both-empty", left: engine.Str(""), right: engine.Str(""), want: ""},
		{name: "rope", left: engine.Str(strings.Repeat("a", 40)), right: engine.Str(strings.Repeat("b", 40)), want: rope},
		{name: "chain", left: engine.Str("ab"), right: engine.Str("cd"), want: "abcd"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := executeBinary(t, bytecode.OpAdd, tt.left, tt.right)
			if err != nil || reason != Executed {
				t.Fatalf("reason=%v err=%v", reason, err)
			}
			if got.Type() != engine.TypeString || got.String() != tt.want {
				t.Fatalf("got %q (type=%v), want %q", got, got.Type(), tt.want)
			}
		})
	}
	// Mixed operands: String + Number concat requires Tier 0 coercion.
	for _, tt := range []struct {
		name        string
		left, right engine.Value
	}{
		{name: "string-number", left: engine.Str("a"), right: engine.Number(1)},
		{name: "number-string", left: engine.Number(1), right: engine.Str("a")},
		{name: "string-bigint", left: engine.Str("a"), right: engine.BigIntFromInt(1)},
		{name: "string-undefined", left: engine.Str("a"), right: engine.Undefined()},
	} {
		t.Run("mixed-"+tt.name, func(t *testing.T) {
			_, reason, err := executeBinary(t, bytecode.OpAdd, tt.left, tt.right)
			if err != nil || reason != GuardFailed {
				t.Fatalf("reason=%v err=%v, want GuardFailed", reason, err)
			}
		})
	}
}

// TestExecuteStringConcatResultFeedsConsumers proves the R3-4 requirement that
// a concat result participates in truthiness, nullish, Return and strict
// equality exactly like any other String value.
func TestExecuteStringConcatResultFeedsConsumers(t *testing.T) {
	// let s = a + b; if (s) { return s; } s = a; return s;
	// pc0 LoadLocal1, pc4 LoadLocal2, pc8 Add, pc12 StoreLocal3, pc16 LoadLocal3,
	// pc20 JmpTrueKeep 36, pc24 LoadLocal1, pc28 StoreLocal3, pc32 LoadLocal3,
	// pc36 Return. The keep-branch lands directly on Return with the concat
	// result on the stack; both paths join at depth 1.
	truthyTmpl := &bytecode.FuncTemplate{
		NumParams: 2, NumLocals: 4, ArgumentsSlot: 4, NoArgumentsObject: true,
		Code: flatCode(
			emit(bytecode.OpLoadLocal, 1),
			emit(bytecode.OpLoadLocal, 2),
			emit(bytecode.OpAdd, 0),
			emit(bytecode.OpStoreLocal, 3),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpJmpTrueKeep, 12),
			emit(bytecode.OpLoadLocal, 1),
			emit(bytecode.OpStoreLocal, 3),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpReturn, 0),
		),
	}
	p, err := CompileLeaf(truthyTmpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name        string
		left, right engine.Value
		want        string
	}{
		{name: "nonempty-result", left: engine.Str("a"), right: engine.Str("b"), want: "ab"},
		{name: "empty-result-falsy", left: engine.Str(""), right: engine.Str(""), want: ""},
		{name: "empty-right", left: engine.Str("x"), right: engine.Str(""), want: "x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := p.Execute(engine.Undefined(), []engine.Value{tt.left, tt.right})
			if err != nil || reason != Executed || got.String() != tt.want {
				t.Fatalf("got=%q reason=%v err=%v want=%q", got, reason, err, tt.want)
			}
		})
	}

	// (a + b) === c : a concat result compared strictly with a param.
	eqTmpl := &bytecode.FuncTemplate{
		NumParams: 3, NumLocals: 4, ArgumentsSlot: 4, NoArgumentsObject: true,
		Code: flatCode(
			emit(bytecode.OpLoadLocal, 1),
			emit(bytecode.OpLoadLocal, 2),
			emit(bytecode.OpAdd, 0),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpStrictEq, 0),
			emit(bytecode.OpReturn, 0),
		),
	}
	p2, err := CompileLeaf(eqTmpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		a, b engine.Value
		c    engine.Value
		want bool
	}{
		{name: "equal", a: engine.Str("a"), b: engine.Str("b"), c: engine.Str("ab"), want: true},
		{name: "not-equal", a: engine.Str("a"), b: engine.Str("b"), c: engine.Str("x"), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := p2.Execute(engine.Undefined(), []engine.Value{tt.a, tt.b, tt.c})
			if err != nil || reason != Executed {
				t.Fatalf("reason=%v err=%v", reason, err)
			}
			v, _ := got.Bool()
			if v != tt.want {
				t.Fatalf("got=%v want=%v", v, tt.want)
			}
		})
	}
}

func TestExecuteStringRelational(t *testing.T) {
	// R3-4: same-type String < <= > >= ordered exactly like Tier 0's
	// compareValues (strings.Compare on the flattened values).
	for _, opCase := range []struct {
		op   bytecode.Opcode
		want [4]bool
	}{
		{op: bytecode.OpLt, want: [4]bool{true, false, true, false}},
		{op: bytecode.OpLe, want: [4]bool{true, true, true, false}},
		{op: bytecode.OpGt, want: [4]bool{false, false, false, true}},
		{op: bytecode.OpGe, want: [4]bool{false, true, false, true}},
	} {
		p, err := CompileLeaf(template(
			emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
			emit(opCase.op, 0), emit(bytecode.OpReturn, 0),
		))
		if err != nil {
			t.Fatal(err)
		}
		pairs := []struct {
			name        string
			left, right string
		}{
			{name: "lt", left: "a", right: "b"},
			{name: "eq", left: "a", right: "a"},
			{name: "prefix", left: "ab", right: "b"},
			{name: "longer", left: "b", right: "ab"},
		}
		for i, pair := range pairs {
			t.Run(opCase.op.String()+"-"+pair.name, func(t *testing.T) {
				got, reason, err := p.Execute(engine.Undefined(),
					[]engine.Value{engine.Str(pair.left), engine.Str(pair.right)})
				if err != nil || reason != Executed {
					t.Fatalf("reason=%v err=%v", reason, err)
				}
				v, _ := got.Bool()
				if v != opCase.want[i] {
					t.Fatalf("(%q %s %q) = %v, want %v", pair.left, opCase.op, pair.right, v, opCase.want[i])
				}
			})
		}
	}
	// Mixed operands fall back to Tier 0 coercion.
	_, reason, err := executeBinary(t, bytecode.OpLt, engine.Str("a"), engine.Number(1))
	if err != nil || reason != GuardFailed {
		t.Fatalf("mixed reason=%v err=%v, want GuardFailed", reason, err)
	}
}

func TestExecuteBigIntArithmetic(t *testing.T) {
	// R3-5: same-type BigInt + - * / % and unary -.
	arithCases := []struct {
		name        string
		op          bytecode.Opcode
		left, right engine.Value
		want        int64
		wantStr     string
	}{
		{name: "add", op: bytecode.OpAdd, left: engine.BigIntFromInt(5), right: engine.BigIntFromInt(3), want: 8},
		{name: "sub", op: bytecode.OpSub, left: engine.BigIntFromInt(5), right: engine.BigIntFromInt(3), want: 2},
		{name: "mul", op: bytecode.OpMul, left: engine.BigIntFromInt(5), right: engine.BigIntFromInt(3), want: 15},
		{name: "div", op: bytecode.OpDiv, left: engine.BigIntFromInt(7), right: engine.BigIntFromInt(2), want: 3},
		{name: "div-negative", op: bytecode.OpDiv, left: engine.BigIntFromInt(-7), right: engine.BigIntFromInt(2), want: -3},
		{name: "mod", op: bytecode.OpMod, left: engine.BigIntFromInt(7), right: engine.BigIntFromInt(2), want: 1},
		{name: "mod-negative", op: bytecode.OpMod, left: engine.BigIntFromInt(-7), right: engine.BigIntFromInt(3), want: -1},
		{name: "big", op: bytecode.OpMul, left: engine.BigIntFromInt(1 << 40), right: engine.BigIntFromInt(1 << 40), wantStr: "1208925819614629174706176"},
	}
	for _, tt := range arithCases {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := executeBinary(t, tt.op, tt.left, tt.right)
			if err != nil || reason != Executed || got.Type() != engine.TypeBigInt {
				t.Fatalf("got=%v type=%v reason=%v err=%v", got, got.Type(), reason, err)
			}
			bi, _ := engine.BigIntValue(got)
			if tt.wantStr != "" {
				if bi.String() != tt.wantStr {
					t.Fatalf("got %v, want %s", bi, tt.wantStr)
				}
			} else if bi.Int64() != tt.want {
				t.Fatalf("got %v, want %d", bi, tt.want)
			}
		})
	}
	// Division/modulo by zero must fall back so Tier 0 raises RangeError.
	for _, op := range []bytecode.Opcode{bytecode.OpDiv, bytecode.OpMod} {
		t.Run("div-zero-"+op.String(), func(t *testing.T) {
			_, reason, err := executeBinary(t, op, engine.BigIntFromInt(1), engine.BigIntZero())
			if err != nil || reason != GuardFailed {
				t.Fatalf("reason=%v err=%v, want GuardFailed", reason, err)
			}
		})
	}
	// Mixed BigInt/Number must fall back so Tier 0 raises TypeError.
	_, reason, err := executeBinary(t, bytecode.OpAdd, engine.BigIntFromInt(1), engine.Number(1))
	if err != nil || reason != GuardFailed {
		t.Fatalf("mixed reason=%v err=%v, want GuardFailed", reason, err)
	}
	// BigInt `**` is not part of R3-5: falls back to Tier 0 (bigintPow).
	_, reason, err = executeBinary(t, bytecode.OpPow, engine.BigIntFromInt(2), engine.BigIntFromInt(3))
	if err != nil || reason != GuardFailed {
		t.Fatalf("pow reason=%v err=%v, want GuardFailed", reason, err)
	}

	// Unary minus on BigInt.
	negTmpl := template(emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpNeg, 0), emit(bytecode.OpReturn, 0))
	p, err := CompileLeaf(negTmpl)
	if err != nil {
		t.Fatal(err)
	}
	got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.BigIntFromInt(5)})
	if err != nil || reason != Executed || got.Type() != engine.TypeBigInt {
		t.Fatalf("got=%v type=%v reason=%v err=%v", got, got.Type(), reason, err)
	}
	bi, _ := engine.BigIntValue(got)
	if bi.Int64() != -5 {
		t.Fatalf("neg = %v, want -5", bi)
	}
}

func TestExecuteBigIntBitwise(t *testing.T) {
	// R3-5: same-type BigInt & | ^ << >>. Unary ~ uses the correct ES
	// semantics (~x = -x-1); see quickBigIntNot for the recorded Tier 0 bug.
	bitCases := []struct {
		name        string
		op          bytecode.Opcode
		left, right engine.Value
		want        int64
		wantStr     string
	}{
		{name: "and", op: bytecode.OpBitAnd, left: engine.BigIntFromInt(6), right: engine.BigIntFromInt(3), want: 2},
		{name: "or", op: bytecode.OpBitOr, left: engine.BigIntFromInt(4), right: engine.BigIntFromInt(3), want: 7},
		{name: "xor", op: bytecode.OpBitXor, left: engine.BigIntFromInt(5), right: engine.BigIntFromInt(3), want: 6},
		{name: "shl", op: bytecode.OpShl, left: engine.BigIntFromInt(1), right: engine.BigIntFromInt(10), want: 1024},
		{name: "shr", op: bytecode.OpShr, left: engine.BigIntFromInt(1024), right: engine.BigIntFromInt(3), want: 128},
		{name: "shr-negative-value", op: bytecode.OpShr, left: engine.BigIntFromInt(-1024), right: engine.BigIntFromInt(3), want: -128},
	}
	for _, tt := range bitCases {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := executeBinary(t, tt.op, tt.left, tt.right)
			if err != nil || reason != Executed || got.Type() != engine.TypeBigInt {
				t.Fatalf("got=%v type=%v reason=%v err=%v", got, got.Type(), reason, err)
			}
			bi, _ := engine.BigIntValue(got)
			if bi.Int64() != tt.want {
				t.Fatalf("got %v, want %d", bi, tt.want)
			}
		})
	}
	// `>>>` on BigInt falls back (Tier 0 TypeError) and the Number path is
	// unaffected.
	_, reason, err := executeBinary(t, bytecode.OpUShr, engine.BigIntFromInt(8), engine.BigIntFromInt(1))
	if err != nil || reason != GuardFailed {
		t.Fatalf("ushr bigint reason=%v err=%v, want GuardFailed", reason, err)
	}
	got, reason, err := executeBinary(t, bytecode.OpUShr, engine.Number(8), engine.Number(1))
	if err != nil || reason != Executed || got.String() != "4" {
		t.Fatalf("ushr number got=%v reason=%v err=%v", got, reason, err)
	}
	// Negative shifts fall back (Tier 0 RangeError).
	_, reason, err = executeBinary(t, bytecode.OpShl, engine.BigIntFromInt(1), engine.BigIntFromInt(-1))
	if err != nil || reason != GuardFailed {
		t.Fatalf("negative shift reason=%v err=%v, want GuardFailed", reason, err)
	}
	// Mixed BigInt/Number falls back (Tier 0 TypeError).
	_, reason, err = executeBinary(t, bytecode.OpBitAnd, engine.BigIntFromInt(1), engine.Number(1))
	if err != nil || reason != GuardFailed {
		t.Fatalf("mixed reason=%v err=%v, want GuardFailed", reason, err)
	}

	// Unary ~ on BigInt: correct ES semantics.
	notTmpl := template(emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpBitNot, 0), emit(bytecode.OpReturn, 0))
	p, err := CompileLeaf(notTmpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		input int64
		want  int64
	}{
		{input: 5, want: -6},
		{input: 0, want: -1},
		{input: -3, want: 2},
		{input: 1 << 60, want: -(1 << 60) - 1},
	} {
		got, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.BigIntFromInt(tt.input)})
		if err != nil || reason != Executed || got.Type() != engine.TypeBigInt {
			t.Fatalf("~%dn: got=%v type=%v reason=%v err=%v", tt.input, got, got.Type(), reason, err)
		}
		bi, _ := engine.BigIntValue(got)
		if bi.Cmp(big.NewInt(tt.want)) != 0 {
			t.Fatalf("~%dn = %v, want %d", tt.input, bi, tt.want)
		}
	}
	// Number ~ is unchanged.
	got, reason, err = p.Execute(engine.Undefined(), []engine.Value{engine.Number(5)})
	if err != nil || reason != Executed || got.Type() != engine.TypeNumber || got.String() != "-6" {
		t.Fatalf("~5 got=%v type=%v reason=%v err=%v", got, got.Type(), reason, err)
	}
}

func TestExecuteBigIntRelational(t *testing.T) {
	// R3-5: same-type BigInt < <= > >=, mirroring Tier 0's bigintCompare.
	for _, opCase := range []struct {
		op   bytecode.Opcode
		want [4]bool
	}{
		{op: bytecode.OpLt, want: [4]bool{true, false, false, true}},
		{op: bytecode.OpLe, want: [4]bool{true, true, false, true}},
		{op: bytecode.OpGt, want: [4]bool{false, false, true, false}},
		{op: bytecode.OpGe, want: [4]bool{false, true, true, false}},
	} {
		p, err := CompileLeaf(template(
			emit(bytecode.OpLoadLocal, 1), emit(bytecode.OpLoadLocal, 2),
			emit(opCase.op, 0), emit(bytecode.OpReturn, 0),
		))
		if err != nil {
			t.Fatal(err)
		}
		pairs := []struct {
			name        string
			left, right int64
		}{
			{name: "lt", left: 5, right: 8},
			{name: "eq", left: 7, right: 7},
			{name: "gt", left: 8, right: 5},
			{name: "negative", left: -3, right: 1},
		}
		for i, pair := range pairs {
			t.Run(opCase.op.String()+"-"+pair.name, func(t *testing.T) {
				got, reason, err := p.Execute(engine.Undefined(),
					[]engine.Value{engine.BigIntFromInt(pair.left), engine.BigIntFromInt(pair.right)})
				if err != nil || reason != Executed {
					t.Fatalf("reason=%v err=%v", reason, err)
				}
				v, _ := got.Bool()
				if v != opCase.want[i] {
					t.Fatalf("(%dn %s %dn) = %v, want %v", pair.left, opCase.op, pair.right, v, opCase.want[i])
				}
			})
		}
	}
	// BigInt vs Number comparisons (mixed) fall back to Tier 0 (which allows
	// them); same for BigInt vs String.
	_, reason, err := executeBinary(t, bytecode.OpLt, engine.BigIntFromInt(5), engine.Number(8))
	if err != nil || reason != GuardFailed {
		t.Fatalf("bigint-number reason=%v err=%v, want GuardFailed", reason, err)
	}
	_, reason, err = executeBinary(t, bytecode.OpLt, engine.BigIntFromInt(5), engine.Str("x"))
	if err != nil || reason != GuardFailed {
		t.Fatalf("bigint-string reason=%v err=%v, want GuardFailed", reason, err)
	}
}

// concatLoopTemplate is a trace-compilable loop:
//
//	for (i = 0; i < n; i++) { if (s < a) { s = s + b; } i++; }
//	return s;
//
// locals: 0=this 1=a 2=b 3=s 4=i 5=n. Layout: pc0 LoadLocal4, pc4 LoadLocal5,
// pc8 Lt, pc12 JmpFalsePop 68, pc16 LoadLocal3, pc20 LoadLocal1, pc24 Lt,
// pc28 JmpFalsePop 48, pc32 LoadLocal3, pc36 LoadLocal2, pc40 Add,
// pc44 StoreLocal3, pc48 LoadLocal4, pc52 PushInt1, pc56 Add, pc60 StoreLocal4,
// pc64 Jmp backedge, pc68 LoadLocal3, pc72 Return.
func concatLoopTemplate() *bytecode.FuncTemplate {
	return &bytecode.FuncTemplate{
		NumParams: 2, NumLocals: 6, ArgumentsSlot: 6, NoArgumentsObject: true,
		Code: flatCode(
			emit(bytecode.OpLoadLocal, 4),
			emit(bytecode.OpLoadLocal, 5),
			emit(bytecode.OpLt, 0),
			emit(bytecode.OpJmpFalsePop, 52),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpLoadLocal, 1),
			emit(bytecode.OpLt, 0),
			emit(bytecode.OpJmpFalsePop, 16),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpLoadLocal, 2),
			emit(bytecode.OpAdd, 0),
			emit(bytecode.OpStoreLocal, 3),
			emit(bytecode.OpLoadLocal, 4),
			emit(bytecode.OpPushInt, 1),
			emit(bytecode.OpAdd, 0),
			emit(bytecode.OpStoreLocal, 4),
			emit(bytecode.OpJmp, (1<<24)-68),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpReturn, 0),
		),
	}
}

// TestTraceStringConcatAndCompare executes a trace whose loop body both
// concatenates strings (allocations inside the trace executor) and compares
// them relationally, then commits the string local.
func TestTraceStringConcatAndCompare(t *testing.T) {
	tmpl := concatLoopTemplate()
	trace, err := CompileTrace(tmpl, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	locals := []engine.Value{
		engine.Undefined(), engine.Str("a"), engine.Str("b"), engine.Str(""), engine.Number(0), engine.Number(3),
	}
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed {
		t.Fatalf("reason=%v err=%v", reason, err)
	}
	// iter1: "" < "a" -> s = "b"; iter2/3: "b" < "a" is false, so the concat
	// is skipped. The committed string local is "b" and the loop counter is 3.
	if exit.ResumePC != 68 {
		t.Fatalf("ResumePC=%d, want 68", exit.ResumePC)
	}
	got := locals[3]
	if got.Type() != engine.TypeString || got.String() != "b" {
		t.Fatalf("committed s=%v, want %q", got, "b")
	}
	if locals[4].String() != "3" {
		t.Fatalf("committed i=%v, want 3", locals[4])
	}
}

// TestTraceBigIntArithmetic executes a trace whose loop both compares BigInts
// relationally and accumulates BigInts (allocations inside the trace
// executor), then proves a division-by-zero BigInt operation inside a trace
// falls back (GuardFailed) so Tier 0 raises the identical RangeError.
func TestTraceBigIntArithmetic(t *testing.T) {
	tmpl := concatLoopTemplate()
	trace, err := CompileTrace(tmpl, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	locals := []engine.Value{
		engine.Undefined(), engine.BigIntFromInt(5), engine.BigIntFromInt(2), engine.BigIntZero(), engine.Number(0), engine.Number(3),
	}
	// s: 0n -> 2n -> 4n -> 6n across the three iterations (2n < 5n stays true).
	exit, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != Executed || exit.ResumePC != 68 {
		t.Fatalf("exit=%+v reason=%v err=%v", exit, reason, err)
	}
	bi, ok := engine.BigIntValue(locals[3])
	if !ok || bi.Int64() != 6 {
		t.Fatalf("committed s=%v, want 6n", locals[3])
	}

	// Division by zero in a BigInt loop: s = a / b; i < n; i++ with b = 0n
	// must guard-fail inside the trace so Tier 0 raises the RangeError.
	// Layout: pc0 LoadLocal1, pc4 LoadLocal2, pc8 Div, pc12 StoreLocal3,
	// pc16 LoadLocal4, pc20 LoadLocal5, pc24 Lt, pc28 JmpFalsePop 52,
	// pc32 LoadLocal4, pc36 PushInt1, pc40 Add, pc44 StoreLocal4,
	// pc48 Jmp backedge, pc52 LoadLocal3, pc56 Return.
	divProgram := &bytecode.FuncTemplate{
		NumParams: 2, NumLocals: 6, ArgumentsSlot: 6, NoArgumentsObject: true,
		Code: flatCode(
			emit(bytecode.OpLoadLocal, 1),
			emit(bytecode.OpLoadLocal, 2),
			emit(bytecode.OpDiv, 0),
			emit(bytecode.OpStoreLocal, 3),
			emit(bytecode.OpLoadLocal, 4),
			emit(bytecode.OpLoadLocal, 5),
			emit(bytecode.OpLt, 0),
			emit(bytecode.OpJmpFalsePop, 20),
			emit(bytecode.OpLoadLocal, 4),
			emit(bytecode.OpPushInt, 1),
			emit(bytecode.OpAdd, 0),
			emit(bytecode.OpStoreLocal, 4),
			emit(bytecode.OpJmp, (1<<24)-52),
			emit(bytecode.OpLoadLocal, 3),
			emit(bytecode.OpReturn, 0),
		),
	}
	trace2, err := CompileTrace(divProgram, 0, 48)
	if err != nil {
		t.Fatal(err)
	}
	defer trace2.Close()
	divLocals := []engine.Value{
		engine.Undefined(), engine.BigIntFromInt(10), engine.BigIntZero(), engine.BigIntZero(), engine.Number(0), engine.Number(5),
	}
	_, reason, err = trace2.ExecuteBudgetDetailed(divLocals, 0)
	if err != nil || reason != GuardFailed {
		t.Fatalf("div-zero trace reason=%v err=%v, want GuardFailed", reason, err)
	}
}

// TestTraceStringGuardFallsBack proves a mixed String+Number add inside a
// trace aborts the whole slice (no partial commit) and returns GuardFailed.
func TestTraceStringGuardFallsBack(t *testing.T) {
	tmpl := concatLoopTemplate()
	trace, err := CompileTrace(tmpl, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	locals := []engine.Value{
		engine.Undefined(), engine.Str("a"), engine.Number(1), engine.Str(""), engine.Number(0), engine.Number(3),
	}
	_, reason, err := trace.ExecuteBudgetDetailed(locals, 0)
	if err != nil || reason != GuardFailed {
		t.Fatalf("reason=%v err=%v, want GuardFailed", reason, err)
	}
}
