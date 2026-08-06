package engine

import (
	"strconv"
	"testing"
)

func TestConcatStringsLongChain(t *testing.T) {
	var got Value = Str("")
	var want string
	for i := 0; i < 10_000; i++ {
		part := "chunk" + strconv.Itoa(i)
		got = ConcatStrings(got, Str(part))
		want += part
	}

	if got.Type() != TypeString {
		t.Fatalf("Type() = %v, want string", got.Type())
	}
	if n, ok := StringLen(got); !ok || n != len(want) {
		t.Fatalf("StringLen() = (%d, %v), want (%d, true)", n, ok, len(want))
	}
	if got.String() != want {
		t.Fatal("flattened rope does not match flat concatenation")
	}
	if got.String() != want {
		t.Fatal("cached rope does not match flat concatenation")
	}
}

func TestConcatStringsCoercionAndConversion(t *testing.T) {
	got := ConcatStrings(Str("value="), IntValue(42))
	if got.String() != "value=42" {
		t.Fatalf("String() = %q, want value=42", got.String())
	}

	numeric := ConcatStrings(Str("12"), Str(".5"))
	if n, ok := numeric.Float(); !ok || n != 12.5 {
		t.Fatalf("Float() = (%v, %v), want (12.5, true)", n, ok)
	}
	if truthy, ok := numeric.Bool(); !ok || !truthy {
		t.Fatalf("Bool() = (%v, %v), want (true, true)", truthy, ok)
	}
}
