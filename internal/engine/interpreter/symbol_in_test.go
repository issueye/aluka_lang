package interpreter

import "testing"

func TestSymbolInOperator(t *testing.T) {
	code := `
const brand = Symbol.for("brand.test");
const value = { [brand]: true };
String(brand in value) + ":" + String(Symbol.for("other") in value);
`
	if got := vmEvalStr(t, code); got != "true:false" {
		t.Fatalf("VM symbol in result = %q, want true:false", got)
	}
	if got := evalStr(t, code); got != "true:false" {
		t.Fatalf("AST symbol in result = %q, want true:false", got)
	}
}
