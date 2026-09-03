package engine

import (
	"math"
	"math/big"
	"testing"
)

// TestStringToNumber covers the engine's ToNumber string rules: trim, empty
// string -> 0, hex/octal prefixes, decimal floats, and NaN for unparseable
// input.
func TestStringToNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"7", 7},
		{"-7", -7},
		{" 7 ", 7},
		{"0x10", 16},
		{"0X10", 16},
		{"0xFFFFFFFFFFFFFFFF", math.NaN()}, // overflows ParseInt -> NaN
		{"0o10", 8},
		{"0O10", 8},
		{"3.5", 3.5},
		{"1e3", 1000},
		{"Infinity", math.Inf(1)},
		{"-Infinity", math.Inf(-1)},
		{"a", math.NaN()},
		// ParseFloat returns ErrRange for out-of-range input; the engine's
		// ToNumber maps that to NaN (Node gives Infinity here — recorded
		// engine-wide quirk; kept consistent with the interpreter's
		// jsStringToNumber).
		{"1e309", math.NaN()},
		{"0x", math.NaN()},
		{"1.5.5", math.NaN()},
	}
	for _, c := range cases {
		got := StringToNumber(c.in)
		wantNaN := math.IsNaN(c.want)
		if wantNaN != math.IsNaN(got) {
			t.Errorf("StringToNumber(%q) = %v, want NaN=%v", c.in, got, wantNaN)
			continue
		}
		if !wantNaN && got != c.want {
			t.Errorf("StringToNumber(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestLooseEqualsEdgeCases is the R3-3 boundary matrix (checked against Node):
// null/undefined, Number/String coercion, Boolean coercion, BigInt across
// types, NaN, signed zero, Symbol identity, and object identity. The one
// deliberate divergence from Tier 0 is recorded separately: Tier 0's
// looseEquals parses String operands with ParseFloat, so `"" == 0` and
// `"0x10" == 16` are false there (recorded Tier 0 bug); the shared helper
// implements the JS-correct results below.
func TestLooseEqualsEdgeCases(t *testing.T) {
	symA := NewSymbol("k")
	symB := NewSymbol("k")
	objA := NewObject()
	objB := NewObject()
	cases := []struct {
		name string
		l, r Value
		want bool
	}{
		{"null==undefined", Null(), Undefined(), true},
		{"undefined==null", Undefined(), Null(), true},
		{"null==null", Null(), Null(), true},
		{"undefined==undefined", Undefined(), Undefined(), true},
		{"null==0", Null(), Number(0), false},
		{"null==''", Null(), Str(""), false},
		{"undefined==0", Undefined(), Number(0), false},
		{"undefined==''", Undefined(), Str(""), false},
		{"''==0", Str(""), Number(0), true},
		{"0==''", Number(0), Str(""), true},
		{"' '==0", Str(" "), Number(0), true},
		{"'2'==2", Str("2"), Number(2), true},
		{"'2'==2.5", Str("2"), Number(2.5), false},
		{"'a'==1", Str("a"), Number(1), false},
		{"'0x10'==16", Str("0x10"), Number(16), true},
		{"'0o10'==8", Str("0o10"), Number(8), true},
		{"'1e3'==1000", Str("1e3"), Number(1000), true},
		{"' 7 '==7", Str(" 7 "), Number(7), true},
		{"'NaN'==NaN", Str("NaN"), Number(math.NaN()), false},
		{"'Infinity'==Infinity", Str("Infinity"), Number(math.Inf(1)), true},
		{"'1'==true", Str("1"), Boolean(true), true},
		{"''==false", Str(""), Boolean(false), true},
		{"''==true", Str(""), Boolean(true), false},
		{"'a'==true", Str("a"), Boolean(true), false},
		{"true==1", Boolean(true), Number(1), true},
		{"false==0", Boolean(false), Number(0), true},
		{"true==2", Boolean(true), Number(2), false},
		{"true==true", Boolean(true), Boolean(true), true},
		{"false==''", Boolean(false), Str(""), true},
		{"NaN==NaN", Number(math.NaN()), Number(math.NaN()), false},
		{"0==-0", Number(0), Number(-0), true},
		{"-0==0", Number(-0), Number(0), true},
		{"1==2", Number(1), Number(2), false},
		{"Infinity==Infinity", Number(math.Inf(1)), Number(math.Inf(1)), true},
		{"Infinity==-Infinity", Number(math.Inf(1)), Number(math.Inf(-1)), false},
		{"1e-320==0", Number(1e-320), Number(0), false},
		{"7n==7n", BigIntFromInt(7), BigIntFromInt(7), true},
		{"7n==8n", BigIntFromInt(7), BigIntFromInt(8), false},
		{"7n==7", BigIntFromInt(7), Number(7), true},
		{"7n==7.5", BigIntFromInt(7), Number(7.5), false},
		{"7n==NaN", BigIntFromInt(7), Number(math.NaN()), false},
		{"7n==Infinity", BigIntFromInt(7), Number(math.Inf(1)), false},
		{"-7n==-Infinity", BigIntFromInt(-7), Number(math.Inf(-1)), false},
		{"7n=='7'", BigIntFromInt(7), Str("7"), true},
		{"7n=='7.0'", BigIntFromInt(7), Str("7.0"), false},
		{"7n==''", BigIntFromInt(7), Str(""), false},
		{"7n=='0x7'", BigIntFromInt(7), Str("0x7"), false},   // StringToBigInt is decimal-only
		{"7n==true", BigIntFromInt(7), Boolean(true), false}, // ToNumber(true)=1; Tier 0 wrongly says true (recorded bug)
		{"0n==false", BigIntZero(), Boolean(false), true},
		{"1n==true", BigIntFromInt(1), Boolean(true), true},
		{"0n==true", BigIntZero(), Boolean(true), false},
		{"7n==false", BigIntFromInt(7), Boolean(false), false},
		{"7n==null", BigIntFromInt(7), Null(), false},
		{"7n==undefined", BigIntFromInt(7), Undefined(), false},
		// The Number literal 9007199254740993 rounds to 9007199254740992 in
		// float64, so the exact BigInt 9007199254740993n is NOT equal (Node
		// agrees); the BigInt 9007199254740992n IS equal to the rounded value.
		{"big==9007199254740993", bigFromString(t, "9007199254740993"), Number(9007199254740993), false},
		{"big2==9007199254740993", bigFromString(t, "9007199254740992"), Number(9007199254740993), true},
		{"big-huge==0", bigFromString(t, "18446744073709551616"), Number(0), false},
		{"sym==sym", symA, symA, true},
		{"sym==symB", symA, symB, false},
		{"sym==7", symA, Number(7), false},
		{"sym=='a'", symA, Str("a"), false},
		{"sym==null", symA, Null(), false},
		{"sym==undefined", symA, Undefined(), false},
		{"sym==true", symA, Boolean(true), false},
		{"sym==7n", symA, BigIntFromInt(7), false},
		{"obj==obj", objA, objA, true},
		{"obj==objB", objA, objB, false},
		{"obj==1", objA, Number(1), false},
		{"obj==''", objA, Str(""), false},
		{"obj==null", objA, Null(), false},
		{"1==obj", Number(1), objA, false},
		{"'a'=='a'", Str("a"), Str("a"), true},
		{"'a'=='b'", Str("a"), Str("b"), false},
		{"'ab'==rope", Str("ab"), ConcatStrings(Str("a"), Str("b")), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LooseEquals(c.l, c.r)
			if got != c.want {
				t.Fatalf("LooseEquals(%v, %v) = %v, want %v", c.l, c.r, got, c.want)
			}
		})
	}
}

func bigFromString(t *testing.T, s string) Value {
	t.Helper()
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad test bigint %q", s)
	}
	return BigInt(bi)
}

// TestLooseEqualsNilOperands proves the helper is nil-safe (defensive; the VM
// never produces nil values on the stack).
func TestLooseEqualsNilOperands(t *testing.T) {
	if LooseEquals(nil, Undefined()) {
		t.Fatal("nil == undefined must be false")
	}
	if LooseEquals(Number(1), nil) {
		t.Fatal("1 == nil must be false")
	}
	if LooseEquals(nil, nil) {
		t.Fatal("nil == nil must be false")
	}
}

// TestLooseEqualsSymmetric re-runs the boundary matrix with operands swapped:
// == is commutative, so every row must agree with its mirror.
func TestLooseEqualsSymmetric(t *testing.T) {
	symA := NewSymbol("k")
	objA := NewObject()
	rows := []struct {
		l, r Value
	}{
		{Null(), Undefined()},
		{Null(), Number(0)},
		{Str(""), Number(0)},
		{Str("2"), Number(2)},
		{Str("a"), Number(1)},
		{Str("0x10"), Number(16)},
		{Boolean(true), Number(1)},
		{Boolean(false), Str("")},
		{Number(math.NaN()), Number(math.NaN())},
		{Number(0), Number(-0)},
		{BigIntFromInt(7), Number(7)},
		{BigIntFromInt(7), Str("7")},
		{BigIntFromInt(7), Boolean(true)},
		{BigIntZero(), Boolean(false)},
		{symA, symA},
		{symA, Number(7)},
		{objA, objA},
		{objA, Number(1)},
		{Str("ab"), ConcatStrings(Str("a"), Str("b"))},
	}
	for _, row := range rows {
		ab := LooseEquals(row.l, row.r)
		ba := LooseEquals(row.r, row.l)
		if ab != ba {
			t.Fatalf("asymmetry: LooseEquals(%v, %v)=%v but LooseEquals(%v, %v)=%v",
				row.l, row.r, ab, row.r, row.l, ba)
		}
	}
}
