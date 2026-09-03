//go:build amd64 && (windows || linux)

package jit

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func TestNativeRejectsLocalNotAssignedOnEveryPath(t *testing.T) {
	p := &Program{
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
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	err := p.CompileNative()
	if err == nil || !strings.Contains(err.Error(), "not a proven number") {
		t.Fatalf("CompileNative error = %v", err)
	}
}

func TestNativeSwapAndNeg(t *testing.T) {
	p := &Program{
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
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := p.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got, reason, err := p.ExecuteNative(engine.Undefined(), nil)
	if err != nil || reason != Executed || got.String() != "-1" {
		t.Fatalf("got=%v reason=%v err=%v", got, reason, err)
	}
}

func TestNativeUnaryPlusPreservesNumberAndGuardsOtherTypes(t *testing.T) {
	p := &Program{
		NumParams: 1,
		NumLocals: 2,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpUnaryPlus},
			{Op: OpReturn},
		},
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := p.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	negativeZero := math.Copysign(0, -1)
	got, reason, err := p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Number(negativeZero)})
	if err != nil || reason != Executed {
		t.Fatalf("number result=%v reason=%v err=%v", got, reason, err)
	}
	number, _ := got.Float()
	if math.Float64bits(number) != math.Float64bits(negativeZero) {
		t.Fatalf("unary plus lost -0: bits=%x", math.Float64bits(number))
	}
	if _, reason, err := p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Str("7")}); err != nil || reason != GuardFailed {
		t.Fatalf("string reason=%v err=%v, want GuardFailed", reason, err)
	}
}

func TestNativeLogicalKeepBranchesMatchQuickTruthiness(t *testing.T) {
	inputs := []float64{0, math.Copysign(0, -1), math.NaN(), 2, -3, math.Inf(1)}
	for _, tt := range []struct {
		name string
		op   Op
	}{
		{name: "and", op: OpJumpFalseKeep},
		{name: "or", op: OpJumpTrueKeep},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &Program{
				NumParams: 1,
				NumLocals: 2,
				Code: []Instr{
					{Op: OpLoadLocal, Operand: 1},
					{Op: tt.op, Operand: 3},
					{Op: OpConst, Value: 7},
					{Op: OpReturn},
				},
			}
			if err := p.Verify(); err != nil {
				t.Fatal(err)
			}
			if err := p.CompileNative(); err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			for _, input := range inputs {
				args := []engine.Value{engine.Number(input)}
				quick, quickReason, quickErr := p.Execute(engine.Undefined(), args)
				native, nativeReason, nativeErr := p.ExecuteNative(engine.Undefined(), args)
				if quickErr != nil || nativeErr != nil || quickReason != Executed || nativeReason != Executed {
					t.Fatalf("input=%v quick=(%v,%v) native=(%v,%v)", input, quickReason, quickErr, nativeReason, nativeErr)
				}
				quickNumber, _ := quick.Float()
				nativeNumber, _ := native.Float()
				if math.IsNaN(quickNumber) && math.IsNaN(nativeNumber) {
					continue
				}
				if math.Float64bits(quickNumber) != math.Float64bits(nativeNumber) {
					t.Fatalf("input=%v quick=%v bits=%x native=%v bits=%x", input, quickNumber, math.Float64bits(quickNumber), nativeNumber, math.Float64bits(nativeNumber))
				}
			}
		})
	}
}

func TestNativeNullishKeepSpecializesNumbersAndGuardsNullish(t *testing.T) {
	p := &Program{
		NumParams: 1,
		NumLocals: 2,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpJumpNullishKeep, Operand: 3},
			{Op: OpConst, Value: 7},
			{Op: OpReturn},
		},
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := p.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, input := range []float64{0, math.Copysign(0, -1), math.NaN(), 2, math.Inf(1)} {
		args := []engine.Value{engine.Number(input)}
		quick, quickReason, quickErr := p.Execute(engine.Undefined(), args)
		native, nativeReason, nativeErr := p.ExecuteNative(engine.Undefined(), args)
		if quickErr != nil || nativeErr != nil || quickReason != Executed || nativeReason != Executed {
			t.Fatalf("input=%v quick=(%v,%v) native=(%v,%v)", input, quickReason, quickErr, nativeReason, nativeErr)
		}
		quickNumber, _ := quick.Float()
		nativeNumber, _ := native.Float()
		if math.IsNaN(quickNumber) && math.IsNaN(nativeNumber) {
			continue
		}
		if math.Float64bits(quickNumber) != math.Float64bits(nativeNumber) {
			t.Fatalf("input=%v quick=%v bits=%x native=%v bits=%x", input, quickNumber, math.Float64bits(quickNumber), nativeNumber, math.Float64bits(nativeNumber))
		}
	}
	for _, input := range []engine.Value{engine.Undefined(), engine.Null()} {
		quick, quickReason, quickErr := p.Execute(engine.Undefined(), []engine.Value{input})
		if quickErr != nil || quickReason != Executed || quick.String() != "7" {
			t.Fatalf("nullish quick input=%v got=%v reason=%v err=%v", input, quick, quickReason, quickErr)
		}
		if _, reason, err := p.ExecuteNative(engine.Undefined(), []engine.Value{input}); err != nil || reason != GuardFailed {
			t.Fatalf("nullish native input=%v reason=%v err=%v, want GuardFailed", input, reason, err)
		}
	}
}

func TestNativeMatchesQuickForRandomNumericIR(t *testing.T) {
	rng := rand.New(rand.NewSource(0xA11CA))
	inputs := []float64{0, math.Copysign(0, -1), 1, -1, 0.5, -3.25, math.Inf(1), math.Inf(-1), math.NaN()}
	for programIndex := 0; programIndex < 40; programIndex++ {
		code := randomNumericExpression(rng, 3)
		code = append(code, Instr{Op: OpReturn})
		p := &Program{NumParams: 2, NumLocals: 3, SelfUpvalue: -1, Code: code}
		if err := p.Verify(); err != nil {
			t.Fatalf("program %d verify: %v", programIndex, err)
		}
		if err := p.CompileNative(); err != nil {
			t.Fatalf("program %d native compile: %v", programIndex, err)
		}
		for inputIndex := 0; inputIndex < 30; inputIndex++ {
			a := inputs[rng.Intn(len(inputs))]
			b := inputs[rng.Intn(len(inputs))]
			args := []engine.Value{engine.Number(a), engine.Number(b)}
			quick, quickReason, quickErr := p.Execute(engine.Undefined(), args)
			native, nativeReason, nativeErr := p.ExecuteNative(engine.Undefined(), args)
			if quickErr != nil || nativeErr != nil || quickReason != Executed || nativeReason != Executed {
				t.Fatalf("program=%d input=%d quick=(%v,%v) native=(%v,%v)", programIndex, inputIndex, quickReason, quickErr, nativeReason, nativeErr)
			}
			quickNumber, _ := quick.Float()
			nativeNumber, _ := native.Float()
			if math.IsNaN(quickNumber) && math.IsNaN(nativeNumber) {
				continue
			}
			if math.Float64bits(quickNumber) != math.Float64bits(nativeNumber) {
				t.Fatalf("program=%d input=(%v,%v) quick=%v native=%v", programIndex, a, b, quickNumber, nativeNumber)
			}
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeNumericComparisonBranches(t *testing.T) {
	tests := []struct {
		op     Op
		a, b   float64
		result float64
	}{
		{OpEq, 2, 2, 1},
		{OpEq, math.NaN(), math.NaN(), 0},
		{OpNe, 2, 2, 0},
		{OpNe, 2, 3, 1},
		{OpNe, math.NaN(), math.NaN(), 1},
		{OpStrictEq, 2, 2, 1},
		{OpStrictEq, math.NaN(), math.NaN(), 0},
		{OpStrictNe, 2, 3, 1},
		{OpStrictNe, math.NaN(), math.NaN(), 1},
		{OpLt, 1, 2, 1},
		{OpLt, math.NaN(), 2, 0},
		{OpLe, 2, 2, 1},
		{OpLe, 3, 2, 0},
		{OpGt, 3, 2, 1},
		{OpGt, math.NaN(), 2, 0},
		{OpGe, 2, 2, 1},
		{OpGe, 1, 2, 0},
	}
	for _, tt := range tests {
		p := &Program{
			NumParams: 2,
			NumLocals: 3,
			Code: []Instr{
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpLoadLocal, Operand: 2},
				{Op: tt.op},
				{Op: OpJumpFalse, Operand: 6},
				{Op: OpConst, Value: 1},
				{Op: OpReturn},
				{Op: OpConst, Value: 0},
				{Op: OpReturn},
			},
		}
		if err := p.Verify(); err != nil {
			t.Fatalf("op=%v verify: %v", tt.op, err)
		}
		if err := p.CompileNative(); err != nil {
			t.Fatalf("op=%v compile: %v", tt.op, err)
		}
		args := []engine.Value{engine.Number(tt.a), engine.Number(tt.b)}
		got, reason, err := p.ExecuteNative(engine.Undefined(), args)
		_ = p.Close()
		if err != nil || reason != Executed || got.String() != engine.Number(tt.result).String() {
			t.Fatalf("op=%v input=(%v,%v) got=%v reason=%v err=%v", tt.op, tt.a, tt.b, got, reason, err)
		}
	}
}

func TestNativeTraceReturnsPreciseExitAndDirtyLocals(t *testing.T) {
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
	if err := trace.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer trace.Close()

	firstLocals := []engine.Value{engine.Undefined(), engine.Number(10), engine.Number(5)}
	first, reason, yields, err := trace.ExecuteNativeBudgetDetailed(firstLocals, 3)
	if err != nil || reason != Executed || yields != 0 || first.ID != 0 || first.ResumePC != 52 || firstLocals[1].String() != "10" {
		t.Fatalf("first exit=%+v reason=%v yields=%d err=%v locals=%v", first, reason, yields, err, firstLocals)
	}

	secondLocals := []engine.Value{engine.Undefined(), engine.Number(0), engine.Number(5)}
	second, reason, yields, err := trace.ExecuteNativeBudgetDetailed(secondLocals, 1)
	if err != nil || reason != Executed || yields == 0 || second.ID != 1 || second.ResumePC != 56 || secondLocals[1].String() != "2" {
		t.Fatalf("second exit=%+v reason=%v yields=%d err=%v locals=%v", second, reason, yields, err, secondLocals)
	}
}

func TestNativeTraceExitRestoresExternalOperandStack(t *testing.T) {
	trace, err := CompileTrace(externalStackTraceTemplate(), 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	for _, input := range []float64{0, math.Copysign(0, -1), math.NaN()} {
		quickLocals := []engine.Value{engine.Undefined(), engine.Number(input), engine.Number(0)}
		nativeLocals := append([]engine.Value(nil), quickLocals...)
		quickExit, quickReason, quickErr := trace.ExecuteBudgetDetailed(quickLocals, 1)
		nativeExit, nativeReason, _, nativeErr := trace.ExecuteNativeBudgetDetailed(nativeLocals, 1)
		if quickErr != nil || nativeErr != nil || quickReason != Executed || nativeReason != Executed ||
			!SameDeoptExit(quickExit, nativeExit) {
			t.Fatalf("input=%v quick=(%+v,%v,%v) native=(%+v,%v,%v)",
				input, quickExit, quickReason, quickErr, nativeExit, nativeReason, nativeErr)
		}
	}
}

func TestNativePropertyWriteVerifyRestoresQuickResultOnMismatch(t *testing.T) {
	p := &Program{
		NumLocals:       3,
		traceExitDepths: []uint8{0}, // exit 0 reached with an empty operand stack
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpSetProp, Name: "a"},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpConst, Value: 1},
			{Op: OpAdd},
			{Op: OpStoreLocal, Operand: 2},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpConst, Value: 3},
			{Op: OpLt},
			{Op: OpJumpTrue, Operand: 0},
			{Op: OpTraceExit, Operand: 0},
		},
		propertyGuards: make([]propertyGuard, 12),
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	trace := &TraceProgram{
		program: p,
		written: []bool{false, false, true},
		exits:   []DeoptExit{{ID: 0, ResumePC: 48, LocalSlots: []uint16{2}}},
	}
	if err := trace.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	if !trace.HasPropertyWrites() || len(p.nativePlan.properties) != 1 {
		t.Fatalf("unexpected native property plan: %+v", p.nativePlan)
	}

	// The machine code still uses the original frame slot. Moving only the
	// writeback plan deliberately creates a verifier mismatch.
	p.nativePlan.properties[0].frameLocal++
	obj := engine.NewObject()
	if err := obj.Set("a", engine.Number(0)); err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{engine.Undefined(), obj, engine.Number(0)}
	exit, reason, _, checked, matched, err := trace.ExecuteNativeBudgetVerified(locals, 1)
	if err != nil || reason != Executed || !checked || matched || exit.ID != 0 || exit.ResumePC != 48 {
		t.Fatalf("exit=%+v reason=%v checked=%t matched=%t err=%v", exit, reason, checked, matched, err)
	}
	property, err := obj.Get("a")
	if err != nil || property.String() != "2" || locals[2].String() != "3" {
		t.Fatalf("property=%v local=%v err=%v", property, locals[2], err)
	}
}

func TestNativePropertyWriteVerifyDoesNotDoublePollSafepoint(t *testing.T) {
	trace, err := CompileTrace(sideEffectTraceTemplate(), 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer trace.Close()

	obj := engine.NewObject()
	if err := obj.Set("x", engine.Number(0)); err != nil {
		t.Fatal(err)
	}
	locals := []engine.Value{
		engine.Undefined(), engine.Number(0), engine.Number(3), obj, engine.Number(0),
	}
	polls := 0
	exit, reason, yields, checked, matched, err := trace.ExecuteNativeBudgetVerifiedWithSafepoint(
		locals, 1, func() error { polls++; return nil })
	if err != nil || reason != Executed || !checked || !matched || exit.ResumePC != 68 || yields == 0 {
		t.Fatalf("exit=%+v reason=%v yields=%d checked=%t matched=%t err=%v",
			exit, reason, yields, checked, matched, err)
	}
	if polls != int(yields) {
		t.Fatalf("safepoint polls = %d, native yields = %d; verification must not invoke embedding callbacks", polls, yields)
	}
}

func randomNumericExpression(rng *rand.Rand, depth int) []Instr {
	if depth == 0 {
		switch rng.Intn(3) {
		case 0:
			return []Instr{{Op: OpLoadLocal, Operand: 1}}
		case 1:
			return []Instr{{Op: OpLoadLocal, Operand: 2}}
		default:
			return []Instr{{Op: OpConst, Value: float64(rng.Intn(17)-8) / 2}}
		}
	}
	code := randomNumericExpression(rng, depth-1)
	code = append(code, randomNumericExpression(rng, depth-1)...)
	ops := [...]Op{OpAdd, OpSub, OpMul, OpDiv}
	code = append(code, Instr{Op: ops[rng.Intn(len(ops))]})
	if rng.Intn(4) == 0 {
		code = append(code, Instr{Op: OpNeg})
	}
	return code
}

// r4_7EdgeInputs is the R4-7 value sweep: every ToInt32/fmod corner class
// (NaN, ±Inf, ±0, subnormals, fractions, int32/int64 boundaries, huge
// magnitudes) plus a few benign values for the random generator.
func r4_7EdgeInputs() []float64 {
	return []float64{
		0, math.Copysign(0, -1), 1, -1, 2, -3, 0.5, -3.25, 1e-320, -1e-320,
		2147483647, -2147483648, 2147483648, 4294967295, 4294967296,
		4294967297, 9007199254740992, 9007199254740993, 1.5e7,
		math.Ldexp(1, 63), -math.Ldexp(1, 63), math.Ldexp(1, 63) + 2048,
		math.Ldexp(1, 64), math.Ldexp(1, 84), -math.Ldexp(1, 84),
		math.Ldexp(1, 100), -math.Ldexp(1, 100), 1e300, -1e300,
		math.Inf(1), math.Inf(-1), math.NaN(),
	}
}

func assertQuickNativeParity(t *testing.T, p *Program, args []engine.Value) {
	t.Helper()
	quick, quickReason, quickErr := p.Execute(engine.Undefined(), args)
	native, nativeReason, nativeErr := p.ExecuteNative(engine.Undefined(), args)
	if quickErr != nil || nativeErr != nil || quickReason != Executed || nativeReason != Executed {
		t.Fatalf("args=%v quick=(%v,%v) native=(%v,%v)", args, quickReason, quickErr, nativeReason, nativeErr)
	}
	quickNumber, _ := quick.Float()
	nativeNumber, _ := native.Float()
	if math.IsNaN(quickNumber) && math.IsNaN(nativeNumber) {
		return
	}
	if math.Float64bits(quickNumber) != math.Float64bits(nativeNumber) {
		t.Fatalf("args=%v quick=%v bits=%x native=%v bits=%x", args, quickNumber, math.Float64bits(quickNumber), nativeNumber, math.Float64bits(nativeNumber))
	}
}

// TestNativeModMatchesQuick proves the R4-7 native fmod is bit-identical to
// the Quick executor (math.Mod) across the whole corner-value sweep.
func TestNativeModMatchesQuick(t *testing.T) {
	p := &Program{
		NumParams: 2,
		NumLocals: 3,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpMod},
			{Op: OpReturn},
		},
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := p.CompileNative(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	inputs := r4_7EdgeInputs()
	for _, a := range inputs {
		for _, b := range inputs {
			assertQuickNativeParity(t, p, []engine.Value{engine.Number(a), engine.Number(b)})
		}
	}
}

// TestNativeBitwiseMatchesQuick proves the R4-7 native bitwise ops are
// bit-identical to the Quick executor for every op and corner value.
func TestNativeBitwiseMatchesQuick(t *testing.T) {
	ops := [...]Op{OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr, OpBitNot}
	for _, op := range ops {
		t.Run(op.String(), func(t *testing.T) {
			code := []Instr{
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpLoadLocal, Operand: 2},
			}
			if op == OpBitNot {
				code = []Instr{{Op: OpLoadLocal, Operand: 1}}
			}
			code = append(code, Instr{Op: op}, Instr{Op: OpReturn})
			p := &Program{NumParams: 2, NumLocals: 3, SelfUpvalue: -1, Code: code}
			if err := p.Verify(); err != nil {
				t.Fatal(err)
			}
			if err := p.CompileNative(); err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			inputs := r4_7EdgeInputs()
			for _, a := range inputs {
				for _, b := range inputs {
					assertQuickNativeParity(t, p, []engine.Value{engine.Number(a), engine.Number(b)})
				}
			}
		})
	}
}

// TestNativeMatchesQuickForRandomIRWithModBitwise runs random expression
// trees over add/sub/mul/div/mod and every bitwise op through both executors.
func TestNativeMatchesQuickForRandomIRWithModBitwise(t *testing.T) {
	rng := rand.New(rand.NewSource(0xB17F0F))
	inputs := r4_7EdgeInputs()
	for programIndex := 0; programIndex < 40; programIndex++ {
		code := randomNumericExpressionWithModBitwise(rng, 3)
		code = append(code, Instr{Op: OpReturn})
		p := &Program{NumParams: 2, NumLocals: 3, SelfUpvalue: -1, Code: code}
		if err := p.Verify(); err != nil {
			t.Fatalf("program %d verify: %v", programIndex, err)
		}
		if err := p.CompileNative(); err != nil {
			t.Fatalf("program %d native compile: %v", programIndex, err)
		}
		for inputIndex := 0; inputIndex < 40; inputIndex++ {
			a := inputs[rng.Intn(len(inputs))]
			b := inputs[rng.Intn(len(inputs))]
			assertQuickNativeParity(t, p, []engine.Value{engine.Number(a), engine.Number(b)})
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func randomNumericExpressionWithModBitwise(rng *rand.Rand, depth int) []Instr {
	if depth == 0 {
		switch rng.Intn(3) {
		case 0:
			return []Instr{{Op: OpLoadLocal, Operand: 1}}
		case 1:
			return []Instr{{Op: OpLoadLocal, Operand: 2}}
		default:
			return []Instr{{Op: OpConst, Value: float64(rng.Intn(17)-8) / 2}}
		}
	}
	code := randomNumericExpressionWithModBitwise(rng, depth-1)
	code = append(code, randomNumericExpressionWithModBitwise(rng, depth-1)...)
	ops := [...]Op{OpAdd, OpSub, OpMul, OpDiv, OpMod, OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr}
	code = append(code, Instr{Op: ops[rng.Intn(len(ops))]})
	if rng.Intn(5) == 0 {
		switch rng.Intn(2) {
		case 0:
			code = append(code, Instr{Op: OpNeg})
		default:
			code = append(code, Instr{Op: OpBitNot})
		}
	}
	return code
}

// TestNativePowRejectedKeepsQuick proves the R4-7 pow decision: the native
// compiler rejects `**` with a descriptive error, the program stays on the
// Quick tier, and both executors agree on pow results.
func TestNativePowRejectedKeepsQuick(t *testing.T) {
	p := &Program{
		NumParams: 2,
		NumLocals: 3,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpPow},
			{Op: OpReturn},
		},
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	err := p.CompileNative()
	if err == nil || !strings.Contains(err.Error(), "pow requires libm") {
		t.Fatalf("CompileNative error = %v, want the descriptive pow rejection", err)
	}
	if p.HasNative() {
		t.Fatal("pow program must not install native code")
	}
	for _, tt := range []struct{ a, b float64 }{
		{2, 10}, {-0, 3}, {1, math.Inf(1)}, {-1, math.Inf(-1)}, {math.NaN(), 0}, {0, -1},
	} {
		args := []engine.Value{engine.Number(tt.a), engine.Number(tt.b)}
		got, reason, err := p.Execute(engine.Undefined(), args)
		if err != nil || reason != Executed {
			t.Fatalf("pow (%v,%v): reason=%v err=%v", tt.a, tt.b, reason, err)
		}
		want := math.Pow(tt.a, tt.b)
		g, _ := got.Float()
		if !math.IsNaN(want) && math.Float64bits(g) != math.Float64bits(want) {
			t.Fatalf("pow (%v,%v): quick=%v want=%v", tt.a, tt.b, g, want)
		}
	}
}
