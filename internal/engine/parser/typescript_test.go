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
