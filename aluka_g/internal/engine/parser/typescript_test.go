package parser

import "testing"

func TestComparisonBeforeNestedGenericFunction(t *testing.T) {
	src := `
function scan(values: string[]): void {
  for (let index = 0; index < values.length; index++) {}
}
export function parse<T = Record<string, unknown>>(value: string | undefined): T {
  return {} as T;
}
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestTypeofImportTypeAlias(t *testing.T) {
	src := `
type Package = typeof import("some-package");
type Exported = import("some-package").Named<string>;
const loaded = true;
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestGenericObjectLiteralMethods(t *testing.T) {
	src := `
const registry = {
  register<T>(name: string, value: T): void { values.set(name, value); },
  async load<T>(name: string): Promise<T> { return await read(name); },
};
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestObjectLiteralGeneratorMethods(t *testing.T) {
	src := `
const iterators = {
  *values<T>(items: T[]) { for (const item of items) yield item; },
  async *load(items) { for (const item of items) yield await read(item); },
};
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestAssertionSignatureReturnTypes(t *testing.T) {
	src := `
type Success = { ok: true };
function assertPresent(value: unknown): asserts value {
  if (!value) throw new Error("missing");
}
function assertSuccess(output: unknown): asserts output is Success {
  if (!output) throw new Error("invalid");
}
class Validator {
  assertReady(): asserts this is Validator & { ready: true } {}
}
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

// TestComparisonInsideIfBeforeRelationalExpression is a regression test for
// skipAngleBraces: a comparison `<` inside an if-condition (`1 / v < 0`) must
// not be mis-skipped as TypeScript generic type arguments. Previously the
// skip consumed arbitrary source up to the next `>` and, when that happened
// to be followed by `(`, corrupted the parse of the whole program.
func TestComparisonInsideIfBeforeRelationalExpression(t *testing.T) {
	src := `
function SV(v) { if (1 / v < 0) return 1; }
var z = (300) > (2);
var w = (1e308) > (2);
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

// TestGenericFunctionTypeKeepsWorking guards the balanced-paren tracking added
// to skipAngleBraces: function-type generics still skip correctly.
func TestGenericFunctionTypeKeepsWorking(t *testing.T) {
	src := `declare function on<T extends () => void>(callback: T): T;`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

// TestComparisonWithParenthesizedRightOperand guards the skipAngleBraces
// paren-depth rule: a `>` inside parentheses is a comparison, not a generic
// closer, so `< ((SYM1) > (2147483648))` must parse as a relational chain.
func TestComparisonWithParenthesizedRightOperand(t *testing.T) {
	src := `
var z = 5 < ((SYM1) > (2147483648));
var w = a < ((b) > (c));
var v = ((false) ** (-0)) < ((SYM1) > (5));
`
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := p.parseProgram(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}
