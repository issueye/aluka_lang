package interpreter

import "testing"

// TestBitwiseNotBigInt is the Tier 0 regression for the R3-5 differential
// finding: `~x` on a BigInt must return a BigInt (-x-1), not a Number.
func TestBitwiseNotBigInt(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	_, err = vm.Eval(`
		globalThis.a = ~5n;
		globalThis.b = ~0n;
		globalThis.c = ~-3n;
	`, "bigint-not.js")
	if err != nil {
		t.Fatal(err)
	}
	val := func(name string) (string, string) {
		v, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return v.Type().String(), v.String()
	}
	if typ, got := val("a"); typ != "bigint" || got != "-6" {
		t.Fatalf("~5n = %s %s, want bigint -6", typ, got)
	}
	if typ, got := val("b"); typ != "bigint" || got != "-1" {
		t.Fatalf("~0n = %s %s, want bigint -1", typ, got)
	}
	if typ, got := val("c"); typ != "bigint" || got != "2" {
		t.Fatalf("~-3n = %s %s, want bigint 2", typ, got)
	}
}
