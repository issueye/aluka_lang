package interpreter

import "testing"

// TestComparisonChainRuntimeSemantics verifies that JS relational/shift
// chains parse AND execute with ECMAScript semantics. These expressions are
// the ones that historically collided with the TypeScript generic-type-args
// skip in the parser, so they lock in both parse shape (see the parser
// generics_compare_test.go) and runtime behavior.
func TestComparisonChainRuntimeSemantics(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`1 < 2`, "true"},
		{`1 < 2 > 3`, "false"},  // (1<2)>3 → true>3 → 1>3
		{`1 < 2 > 0`, "true"},   // true>0 → 1>0
		{`1 < 2 >> 3`, "false"}, // 1 < (2>>3) → 1<0
		{`1 < 2 >>> 3`, "false"},
		{`3 > 2 > 1`, "false"}, // true>1 → 1>1
		{`2 > 1 > 0`, "true"},
		{`5 >= 5 <= 5`, "true"},
		{`NaN < 1`, "false"},
		{`1 < NaN`, "false"},
		{`-0 < 0`, "false"},
		{`1e308 < Infinity`, "true"},
		{`(1 < 2) > (0)`, "true"},
		{`1 < 2 && 3 > 4`, "false"},
		{`3 > 2 && 4 > 3`, "true"},
		{`1 < 2 ? "lt" : "ge"`, "lt"},
		{`1 < 2 < 3`, "true"}, // (1<2)<3 → true<3 → 1<3
		{`3 < 2 < 1`, "true"}, // false<1 → 0<1（布尔强转数字）
		{`1 < 2 == true`, "true"},
		{`let a=1, b=2, c=3; a < b > c`, "false"},
		{`let a=1, b=2, c=3; a < b >> c`, "false"},
		{`let a=1, b=2, c=3; (a < b) > (c)`, "false"},
		{`let a=1, b=2, c=3; a > b && c < 3 || a <= b`, "true"},
		{`let i = 0; for (; i < 5 > 2; i++) {} i`, "0"}, // (i<5)>2 对 i=0 为 false，循环不执行
		{`let s = 0; for (let i = 0; i < 10; i++) { if (i > 2 && i < 8) s++; } s`, "5"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestGenericAmbiguityRuntimeSemantics documents the engine's TSC-compatible
// ambiguity rule at runtime: `foo < bar > (baz)` is a generic call
// `foo<bar>(baz)` (type args erased, foo(baz) invoked), NOT the JS comparison
// chain `(foo < bar) > (baz)`. Node (no TS) evaluates the comparison; aluka
// evaluates the generic call. Both behaviors are locked in here.
func TestGenericAmbiguityRuntimeSemantics(t *testing.T) {
	// TS semantics (this engine): foo<bar>(baz) → foo(3) = 13.
	ts := vmEvalStr(t, `function foo(x) { return x + 10; } var bar = 5, baz = 3; foo < bar > (baz)`)
	if ts != "13" {
		t.Fatalf("generic parse produced %q, want 13 (foo(3)+10)", ts)
	}
	// Node/JS semantics: (foo < bar) > (baz) → (function<5) > 3 → false.
	if got := vmEvalStr(t, `function foo(x) { return x + 10; } var bar = 5, baz = 3; (foo < bar) > (baz)`); got != "false" {
		t.Fatalf("parenthesized comparison produced %q, want false", got)
	}
	// Numbers never trigger the generic path: `5 < 3 > (2)` is a comparison.
	if got := vmEvalStr(t, `5 < 3 > (2)`); got != "false" {
		t.Fatalf("numeric comparison produced %q, want false", got)
	}
}

func TestGenericCallResultRuntimeSemantics(t *testing.T) {
	got := vmEvalStr(t, `
		function factory() { return function(x) { return x; }; }
		factory()<number>(3)
	`)
	if got != "3" {
		t.Fatalf("generic call result = %q, want 3", got)
	}

	got = vmEvalStr(t, `
		function use(x) { return x; }
		function cb(x) { return x; }
		use<(x: Array<number>) => number>(cb)(3)
	`)
	if got != "3" {
		t.Fatalf("nested generic function type = %q, want 3", got)
	}
}
