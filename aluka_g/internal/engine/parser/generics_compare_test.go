package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// describeExpr renders an expression tree as a compact string so tests can
// assert the exact parse shape (e.g. `((a<b)>c)` for a comparison chain vs
// `a(c)` for a generic call with erased type arguments).
func describeExpr(e ast.Expression) string {
	switch n := e.(type) {
	case *ast.Identifier:
		return n.Name
	case *ast.NumberLit:
		return n.Raw
	case *ast.BinaryExpr:
		return "(" + describeExpr(n.Left) + n.Op + describeExpr(n.Right) + ")"
	case *ast.LogicalExpr:
		return "(" + describeExpr(n.Left) + n.Op + describeExpr(n.Right) + ")"
	case *ast.UnaryExpr:
		return n.Op + describeExpr(n.Arg)
	case *ast.CallExpr:
		args := make([]string, len(n.Arguments))
		for i, a := range n.Arguments {
			args[i] = describeExpr(a)
		}
		return describeExpr(n.Callee) + "(" + strings.Join(args, ",") + ")"
	case *ast.NewExpr:
		args := make([]string, len(n.Arguments))
		for i, a := range n.Arguments {
			args[i] = describeExpr(a)
		}
		return "new " + describeExpr(n.Callee) + "(" + strings.Join(args, ",") + ")"
	case *ast.MemberExpr:
		prop := "?"
		if id, ok := n.Property.(*ast.Identifier); ok {
			prop = id.Name
		}
		return describeExpr(n.Object) + "." + prop
	case *ast.ConditionalExpr:
		return "(" + describeExpr(n.Test) + "?" + describeExpr(n.Consequent) + ":" + describeExpr(n.Alternate) + ")"
	}
	return fmt.Sprintf("%T", e)
}

// parseInit extracts the initializer of the first `var z = ...` statement.
func parseInit(t *testing.T, src string) ast.Expression {
	t.Helper()
	p, err := NewFromString(src)
	if err != nil {
		t.Fatalf("lex %q: %v", src, err)
	}
	prog, err := p.parseProgram()
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	for _, s := range prog.Body {
		if vd, ok := s.(*ast.VarDecl); ok {
			for _, d := range vd.Decls {
				if d.Init != nil {
					return d.Init
				}
			}
		}
	}
	t.Fatalf("no var init found in %q", src)
	return nil
}

// TestGenericCallArguments covers TypeScript generic type arguments on call
// and new expressions. Each case must parse as a CallExpr/NewExpr with the
// type arguments erased from the runtime AST.
func TestGenericCallArguments(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // describeExpr of the initializer
	}{
		{"simple", `var z = foo<T>(x);`, "foo(x)"},
		{"multi-param", `var z = foo<T, U>(x);`, "foo(x)"},
		{"nested-double-close", `var z = foo<Array<number>>(x);`, "foo(x)"},
		{"triple-nested", `var z = foo<A<B<C>>>(x);`, "foo(x)"},
		{"default", `var z = foo<T = Default>(x);`, "foo(x)"},
		{"extends", `var z = foo<T extends U>(x);`, "foo(x)"},
		{"function-type", `var z = foo<() => void>(x);`, "foo(x)"},
		{"object-type", `var z = foo<{ a: number }>(x);`, "foo(x)"},
		{"record-string", `var z = foo<Record<string, unknown>>(x);`, "foo(x)"},
		{"with-args", `var z = foo<T>(x, y);`, "foo(x,y)"},
		{"then-arithmetic", `var z = foo<T>(x) + 1;`, "(foo(x)+1)"},
		{"method", `var z = obj.method<T>(x);`, "obj.method(x)"},
		{"call-result", `var z = factory()<number>(3);`, "factory()(3)"},
		{"new-result", `var z = new Factory()<number>(3);`, "new Factory()(3)"},
		{"new-expr", `var z = new Box<number>(7);`, "new Box(7)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr := parseInit(t, tc.src)
			if got := describeExpr(expr); got != tc.want {
				t.Fatalf("parse %q = %s, want %s", tc.src, got, tc.want)
			}
		})
	}
}

// TestGenericTypeDeclarations covers generic parameters on type declarations
// and arrow functions (compile-time erased).
func TestGenericTypeDeclarations(t *testing.T) {
	sources := []string{
		`function f<T>(x: T): T { return x; }`,
		`const f = <T>(x: T) => x;`,
		`interface I<T> { value: T }`,
		`type A<T> = T[];`,
		`class Box<T> { value: T; constructor(v: T) { this.value = v; } }`,
		`function f<T extends U = Default>(x: T): T { return x; }`,
		`foo<T extends () => void>(cb);`,
		`foo<(x: Array<number>) => number>(cb);`,
		`foo<() => Array<number>>(cb);`,
		`foo<Array<Array<number>>>(x);`,
	}
	for _, src := range sources {
		t.Run(src, func(t *testing.T) {
			p, err := NewFromString(src)
			if err != nil {
				t.Fatalf("lex: %v", err)
			}
			if _, err := p.parseProgram(); err != nil {
				t.Fatalf("parse: %v", err)
			}
		})
	}
}

// TestComparisonChainsParseAsComparisons covers JS relational/shift chains
// where the same tokens must NOT be treated as type arguments. Each case must
// produce a comparison BinaryExpr chain with JS semantics.
func TestComparisonChainsParseAsComparisons(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"simple-lt", `var z = a < b;`, "(a<b)"},
		{"simple-gt", `var z = a > b;`, "(a>b)"},
		{"chain-lt-gt", `var z = a < b > c;`, "((a<b)>c)"},
		{"chain-three", `var z = a < b > c < d;`, "(((a<b)>c)<d)"},
		{"right-shift", `var z = a < b >> c;`, "(a<(b>>c))"},
		{"unsigned-right-shift", `var z = a < b >>> c;`, "(a<(b>>>c))"},
		{"ge-le", `var z = a >= b <= c;`, "((a>=b)<=c)"},
		{"numbers", `var z = 5 < 3 > 1e308;`, "((5<3)>1e308)"},
		{"paren-left", `var z = (a) > (b);`, "(a>b)"},
		{"paren-right-expr", `var z = a < (b + 1);`, "(a<(b+1))"},
		{"ternary", `var z = a < b ? c : d;`, "((a<b)?c:d)"},
		{"result-plus", `var z = (a < b) + 1;`, "((a<b)+1)"},
		{"and-or", `var z = a > b && c < d || e >= f;`, "(((a>b)&&(c<d))||(e>=f))"},
		{"shift-paren", `var z = a < b >> (c);`, "(a<(b>>c))"},
		{"relational-in-paren", `var z = ((a) > (b)) < c;`, "((a>b)<c)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr := parseInit(t, tc.src)
			if got := describeExpr(expr); got != tc.want {
				t.Fatalf("parse %q = %s, want %s", tc.src, got, tc.want)
			}
		})
	}
}

// TestGenericVsComparisonDisambiguation documents the engine's ambiguity
// rule: `ident < ident > (args)` is treated as a TypeScript generic call
// (matching TSC), while the same tokens without a following `(` are a JS
// comparison chain. Both must parse without errors.
func TestGenericVsComparisonDisambiguation(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		// TS-style generic call (type args erased, callee invoked).
		{"generic-wins-with-paren", `var z = foo < bar > (baz);`, "foo(baz)"},
		{"generic-wins-method", `var z = obj.a < obj.b > (c);`, "obj.a(c)"},
		{"generic-wins-arithmetic-after", `var z = a < b > (c) + 1;`, "(a(c)+1)"},
		// Without a following `(` the `<` is a comparison operator.
		{"comparison-without-paren", `var z = foo < bar > baz;`, "((foo<bar)>baz)"},
		{"comparison-chain", `var z = a < b > c;`, "((a<b)>c)"},
		// `>` inside parentheses is a comparison, not a closer.
		{"paren-right-gt", `var z = 5 < ((SYM1) > (2147483648));`, "(5<(SYM1>2147483648))"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr := parseInit(t, tc.src)
			if got := describeExpr(expr); got != tc.want {
				t.Fatalf("parse %q = %s, want %s", tc.src, got, tc.want)
			}
		})
	}
}

// TestComparisonInControlFlow guards the statement-level contexts that
// previously mis-triggered the generic skip (comparisons in if/for bodies).
func TestComparisonInControlFlow(t *testing.T) {
	sources := []string{
		`function f(v) { if (1 / v < 0) return 1; }`,
		`for (let i = 0; i < n; i++) { foo(i); }`,
		`for (let i = 0; i < n; i++) {} function f<T>(x: T): T { return x; }`,
		`while (a < b) { break; }`,
		`if (a < b && c > d) { x(); }`,
		`do { i++; } while (i < n);`,
		`var z = a < b ? c < d : e > f;`,
	}
	for _, src := range sources {
		t.Run(src, func(t *testing.T) {
			p, err := NewFromString(src)
			if err != nil {
				t.Fatalf("lex: %v", err)
			}
			if _, err := p.parseProgram(); err != nil {
				t.Fatalf("parse: %v", err)
			}
		})
	}
}
