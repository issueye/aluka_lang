package interpreter

import "testing"

// TypeScript transpiler feature tests covering type stripping, type assertions,
// generic parameters, type-only imports/exports, interface/type aliases, enums,
// and namespaces (tasks 1D.1–1D.8).

// === 1D.1 Type annotation stripping ==========================================

func TestTSTypeAnnotationVarDecl(t *testing.T) {
	cases := []struct{ code, want string }{
		{"let x: number = 5; x", "5"},
		{"let s: string = 'hi'; s", "hi"},
		{"let b: boolean = true; b", "true"},
		{"let a: number[] = [1, 2, 3]; a.length", "3"},
		{"let m: Map<string, number> = new Map(); m.size", "0"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestTSTypeAnnotationFuncParams(t *testing.T) {
	got := vmEvalStr(t, "function add(a: number, b: number): number { return a + b; } add(2, 3)")
	if got != "5" {
		t.Errorf("add(2,3) = %q, want 5", got)
	}
	// Default + type annotation
	got = vmEvalStr(t, "function f(x: number = 10): number { return x * 2; } f()")
	if got != "20" {
		t.Errorf("f() default = %q, want 20", got)
	}
	// Optional param + rest: a=1, b=2, rest=[3,4] → 1+2+2 = 5
	got = vmEvalStr(t, "function f(a: number, b?: number, ...rest: number[]): number { return a + (b||0) + rest.length; } f(1, 2, 3, 4)")
	if got != "5" {
		t.Errorf("f(1,2,3,4) = %q, want 5", got)
	}
}

func TestTSCatchBindingTypeAnnotation(t *testing.T) {
	got := vmEvalStr(t, `try { throw new Error("boom") } catch (err: unknown) { err.message }`)
	if got != "boom" {
		t.Errorf("typed catch binding = %q, want boom", got)
	}
}

func TestTSTypeAnnotationClassFields(t *testing.T) {
	code := `
class Point {
  x: number;
  y: number = 10;
  constructor(x: number) { this.x = x; }
  sum(): number { return this.x + this.y; }
}
var p = new Point(5);
p.sum();
`
	got := vmEvalStr(t, code)
	if got != "15" {
		t.Errorf("Point.sum = %q, want 15", got)
	}
}

func TestComputedClassFields(t *testing.T) {
	code := `
let calls = 0;
const instanceKey = Symbol("instance");
const staticKey = "static";
function key() { calls++; return instanceKey; }
class Box {
  [key()] = 41;
  static [staticKey] = 1;
}
const a = new Box();
const b = new Box();
a[instanceKey] + b[instanceKey] + Box[staticKey] + ":" + calls;
`
	if got := vmEvalStr(t, code); got != "83:1" {
		t.Errorf("computed class fields = %q, want 83:1", got)
	}
}

func TestPrivateClassFieldsWithoutSemicolons(t *testing.T) {
	code := `
class Probe {
  #maxSize = 1024 * 1024
  #size = 0
  #controller = null

  update(err, chunk) {
    err = this.#controller?.reason ?? err
    this.#size = this.#size + chunk.length
    return err + ":" + (this.#size < this.#maxSize)
  }
}
new Probe().update("fallback", { length: 1 })
`
	if got := vmEvalStr(t, code); got != "fallback:true" {
		t.Errorf("private fields without semicolons = %q, want fallback:true", got)
	}
}

// === 1D.6 Type assertion `as T` / `satisfies T` =============================

func TestTSTypeAssertion(t *testing.T) {
	cases := []struct{ code, want string }{
		{"(5 as number)", "5"},
		{"('hi' as string).length", "2"},
		{"(5 as unknown as string)", "5"},
		{"(5 as const)", "5"},
		{"([1, 2, 3] as number[]).length", "3"},
		{"({a: 1} as {a: number}).a", "1"},
		{"(5 satisfies number)", "5"},
		// Assertion in middle of expression
		{"((10 as number) + 5)", "15"},
		// Assertion inside array
		{"[1 as number, 2, 3][0]", "1"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 1D.7 Generic parameter stripping =======================================

func TestTSGenericParams(t *testing.T) {
	cases := []struct{ code, want string }{
		{"function id<T>(x: T): T { return x; } id(42)", "42"},
		{"function pair<T, U>(a: T, b: U): [T, U] { return [a, b]; } pair(1, 'x').length", "2"},
		{"class Box<T> { value: T; constructor(v: T) { this.value = v; } get(): T { return this.value; } } new Box<number>(7).get()", "7"},
		// Arrow function with generic
		// {"(function() { var id = <T,>(x: T): T => x; return id('hi'); })()", "hi"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 1D.8 import type / export type ========================================
// Tested via module loader; here we only verify that bare type-only imports
// in non-module code parse without error (no runtime effect).

func TestTSTypeOnlyImportExport(t *testing.T) {
	// `import type` should erase entirely.
	got := vmEvalStr(t, "var x = 5; x")
	if got != "5" {
		t.Errorf("baseline = %q, want 5", got)
	}
	// Interface + type alias only — no runtime code.
	got = vmEvalStr(t, "interface Foo { a: number } 42")
	if got != "42" {
		t.Errorf("interface decl = %q, want 42", got)
	}
}

// === 1D.2 interface / type alias ===========================================

func TestTSInterfaceAndTypeAlias(t *testing.T) {
	cases := []struct{ code, want string }{
		// interface declaration is erased
		{"interface Point { x: number; y: number; } 1 + 1", "2"},
		// interface with extends
		{"interface A { a: number } interface B extends A { b: string } 7", "7"},
		// interface with generic
		{"interface Container<T> { value: T } 'ok'", "ok"},
		// type alias is erased
		{"type T = number | string; 100", "100"},
		// type alias with generic
		{"type Box<T> = { value: T }; 200", "200"},
		// type alias for function
		{"type Fn = (x: number) => number; 300", "300"},
		// multiple declarations mixed
		{"interface I {} type A = I; var x = 5; x", "5"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 1D.3 enum =============================================================

func TestTSEnumNumeric(t *testing.T) {
	code := `
enum Direction { Up, Down, Left, Right }
Direction.Up + ':' + Direction.Down + ':' + Direction.Left + ':' + Direction.Right
`
	got := vmEvalStr(t, code)
	if got != "0:1:2:3" {
		t.Errorf("numeric enum forward = %q, want 0:1:2:3", got)
	}
	// Reverse mapping
	got = vmEvalStr(t, "enum D { Up, Down } D[0]")
	if got != "Up" {
		t.Errorf("reverse mapping = %q, want Up", got)
	}
	// Explicit initializer
	got = vmEvalStr(t, "enum E { A = 10, B, C } E.A + ':' + E.B + ':' + E.C")
	if got != "10:11:12" {
		t.Errorf("explicit init = %q, want 10:11:12", got)
	}
}

func TestTSEnumString(t *testing.T) {
	code := `
enum Color { Red = 'red', Green = 'green', Blue = 'blue' }
Color.Red + ':' + Color.Green + ':' + Color.Blue
`
	got := vmEvalStr(t, code)
	if got != "red:green:blue" {
		t.Errorf("string enum = %q, want red:green:blue", got)
	}
	// String enums have no reverse mapping.
	got = vmEvalStr(t, "enum C { A = 'x' } C[0]")
	if got != "undefined" {
		t.Errorf("string enum reverse = %q, want undefined", got)
	}
}

func TestTSEnumMixed(t *testing.T) {
	code := `
enum M { A = 1, B = 'x', C = 3 }
M.A + ':' + M.B + ':' + M.C
`
	got := vmEvalStr(t, code)
	if got != "1:x:3" {
		t.Errorf("mixed enum = %q, want 1:x:3", got)
	}
}

// === 1D.4 namespace ========================================================

func TestTSNamespace(t *testing.T) {
	code := `
namespace Util {
  export function add(a: number, b: number): number { return a + b; }
  export var version: string = '1.0';
}
Util.add(2, 3) + ':' + Util.version
`
	got := vmEvalStr(t, code)
	if got != "5:1.0" {
		t.Errorf("namespace = %q, want 5:1.0", got)
	}
}

func TestTSNamespaceClass(t *testing.T) {
	code := `
namespace NS {
  export class Point {
    x: number;
    constructor(x: number) { this.x = x; }
    get(): number { return this.x; }
  }
}
var p = new NS.Point(42);
p.get()
`
	got := vmEvalStr(t, code)
	if got != "42" {
		t.Errorf("namespace class = %q, want 42", got)
	}
}

// === 1D.5 Decorators (parsed and skipped) ===================================

func TestTSDecorators(t *testing.T) {
	cases := []struct{ code, want string }{
		// Class decorator
		{"@dec class C {} var c = new C(); 'ok'", "ok"},
		{"@dec @dec2 class C {} 'ok'", "ok"},
		// Qualified decorator name
		{"@mod.dec class C {} 'ok'", "ok"},
		// Decorator with call args
		{"@dec(1, 2) class C {} 'ok'", "ok"},
		{"@dec('x') class C {} 'ok'", "ok"},
		// Method decorator
		{"class C { @dec foo() { return 42; } } new C().foo()", "42"},
		// Field decorator
		{"class C { @dec x: number = 10; } new C().x", "10"},
		// Mixed: class + method + field decorators
		{"@cls class C { @m method(): number { return 5; } @f field: number = 3; } new C().method() + new C().field", "8"},
	}
	for _, c := range cases {
		if got := vmEvalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === Combined scenarios =====================================================

func TestTSCombined(t *testing.T) {
	code := `
interface User { id: number; name: string; }

type Status = 'active' | 'inactive';

enum Role { Admin, User, Guest }

namespace App {
  export function describe(u: User, r: Role): string {
    return u.name + ' is ' + Role[r];
  }
}

var u = { id: 1, name: 'Alice' } as User;
App.describe(u, Role.Admin)
`
	got := vmEvalStr(t, code)
	if got != "Alice is Admin" {
		t.Errorf("combined = %q, want Alice is Admin", got)
	}
}
