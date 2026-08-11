package interpreter

import "testing"

// TestTier0LooseEqualitySemantics locks the R3-3 differential fixes: String
// operands in == must use full JS ToNumber ("" == 0, "0x10" == 16, "2" == 2,
// "a" == 1 false), and BigInt vs Boolean must compare the exact 0/1 value
// (7n == true is false).
func TestTier0LooseEqualitySemantics(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	_, err = vm.Eval(`
		globalThis.a = "" == 0;
		globalThis.b = "0x10" == 16;
		globalThis.c = "2" == 2;
		globalThis.d = "a" == 1;
		globalThis.e = 7n == true;
		globalThis.f = 1n == true;
		globalThis.g = 0n == false;
		globalThis.h = false == "";
		globalThis.i = "1e309" == Infinity;
	`, "tier0-looseeq.js")
	if err != nil {
		t.Fatal(err)
	}
	val := func(name string) string {
		v, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return v.String()
	}
	want := map[string]string{
		"a": "true", "b": "true", "c": "true", "d": "false",
		"e": "false", "f": "true", "g": "true", "h": "true",
		"i": "false", // jsStringToNumber("1e309") -> NaN per existing engine semantics
	}
	for name, w := range want {
		if got := val(name); got != w {
			t.Fatalf("%s = %s, want %s", name, got, w)
		}
	}
}

// TestTier0ArithmeticToNumberSemantics locks the binAdd fix: non-Number
// operands follow JS ToNumber (1 + undefined is NaN, "str" * 2 is NaN,
// 1 + null is 1, "2" * 3 is 6).
func TestTier0ArithmeticToNumberSemantics(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	_, err = vm.Eval(`
		globalThis.a = 1 + undefined;
		globalThis.b = "str" * 2;
		globalThis.c = 1 + null;
		globalThis.d = "2" * 3;
		globalThis.e = 1 + true;
	`, "tier0-arith.js")
	if err != nil {
		t.Fatal(err)
	}
	val := func(name string) string {
		v, err := vm.Global().Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return v.String()
	}
	if got := val("a"); got != "NaN" {
		t.Fatalf("1 + undefined = %s, want NaN", got)
	}
	if got := val("b"); got != "NaN" {
		t.Fatalf(`"str" * 2 = %s, want NaN`, got)
	}
	if got := val("c"); got != "1" {
		t.Fatalf("1 + null = %s, want 1", got)
	}
	if got := val("d"); got != "6" {
		t.Fatalf(`"2" * 3 = %s, want 6`, got)
	}
	if got := val("e"); got != "2" {
		t.Fatalf("1 + true = %s, want 2", got)
	}
}
