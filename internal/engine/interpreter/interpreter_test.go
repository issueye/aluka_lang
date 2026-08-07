package interpreter

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// evalTest is a helper that creates an interpreter and evaluates code.
func evalTest(t *testing.T, code string) (engine.Value, error) {
	t.Helper()
	interp, err := NewInterpreter()
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	return interp.Eval(code, "test.js")
}

// evalStr evaluates code and returns the string representation of the result.
func evalStr(t *testing.T, code string) string {
	t.Helper()
	v, err := evalTest(t, code)
	if err != nil {
		t.Fatalf("Eval(%q) error: %v", code, err)
	}
	return v.String()
}

// === Phase 1A Acceptance Tests =============================================

func TestAcceptArithmetic(t *testing.T) {
	got := evalStr(t, "1+2*3")
	if got != "7" {
		t.Errorf(`Eval("1+2*3") = %q, want "7"`, got)
	}
}

func TestAcceptArrayMap(t *testing.T) {
	got := evalStr(t, "[1,2,3].map(x=>x*2)")
	// Result is [ 2, 4, 6 ] (with spaces, like Node.js console output)
	if !strings.Contains(got, "2") || !strings.Contains(got, "4") || !strings.Contains(got, "6") {
		t.Errorf(`Eval("[1,2,3].map(x=>x*2)") = %q, want array containing 2,4,6`, got)
	}
}

func TestAcceptObjectAccess(t *testing.T) {
	got := evalStr(t, "({a:1,b:2}).a")
	if got != "1" {
		t.Errorf(`Eval("({a:1,b:2}).a") = %q, want "1"`, got)
	}
}

func TestAcceptTryCatch(t *testing.T) {
	// Should not error out; should print the error message via console.log
	// We test that try/catch works by verifying no error is returned
	_, err := evalTest(t, "try{null.foo}catch(e){}")
	if err != nil {
		t.Errorf("try{null.foo}catch(e){} should not return error, got: %v", err)
	}
}

// === Arithmetic and Expressions ============================================

func TestArithmetic(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"1 + 1", "2"},
		{"2 * 3", "6"},
		{"10 - 4", "6"},
		{"20 / 4", "5"},
		{"2 + 3 * 4", "14"},
		{"(2 + 3) * 4", "20"},
		{"7 % 3", "1"},
		{"2 ** 3", "8"}, // if supported
	}
	for _, c := range cases {
		got := evalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestStringConcat(t *testing.T) {
	got := evalStr(t, `"Hello, " + "World"`)
	if got != "Hello, World" {
		t.Errorf("got %q, want %q", got, "Hello, World")
	}
}

func TestNumberParseAliases(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`Number.parseInt === parseInt`, "true"},
		{`Number.parseFloat === parseFloat`, "true"},
		{`Number.parseInt("10", 10)`, "10"},
		{`Number.parseFloat("3.5")`, "3.5"},
		{`Number.isSafeInteger(9007199254740991)`, "true"},
		{`Number.isSafeInteger(9007199254740992)`, "false"},
		{`Number.isSafeInteger(1.5)`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, c.code); got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestComparisons(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"1 < 2", true},
		{"2 < 1", false},
		{"1 == 1", true},
		{"1 === 1", true},
		{"1 === '1'", false},
		{"1 == '1'", true},
		{"true && false", false},
		{"true || false", true},
		{"!false", true},
	}
	for _, c := range cases {
		v, err := evalTest(t, c.code)
		if err != nil {
			t.Errorf("Eval(%q) error: %v", c.code, err)
			continue
		}
		b, _ := v.Bool()
		if b != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.code, b, c.want)
		}
	}
}

// === Variables and Scope ===================================================

func TestVarDecl(t *testing.T) {
	got := evalStr(t, "var x = 42; x")
	if got != "42" {
		t.Errorf("got %q, want 42", got)
	}
}

func TestLetConst(t *testing.T) {
	got := evalStr(t, "let a = 1; const b = 2; a + b")
	if got != "3" {
		t.Errorf("got %q, want 3", got)
	}
}

func TestClosureCapture(t *testing.T) {
	got := evalStr(t, `
		function makeAdder(x) {
			return function(y) { return x + y; };
		}
		var add5 = makeAdder(5);
		add5(3)
	`)
	if got != "8" {
		t.Errorf("got %q, want 8", got)
	}
}

func TestArrowClosure(t *testing.T) {
	got := evalStr(t, `
		var add = (a, b) => a + b;
		add(3, 4)
	`)
	if got != "7" {
		t.Errorf("got %q, want 7", got)
	}
}

// === Control Flow ==========================================================

func TestIfElse(t *testing.T) {
	got := evalStr(t, "var x = 10; if (x > 5) { 'big' } else { 'small' }")
	if got != "big" {
		t.Errorf("got %q, want big", got)
	}
}

func TestForLoop(t *testing.T) {
	got := evalStr(t, `
		var sum = 0;
		for (var i = 0; i < 5; i++) {
			sum += i;
		}
		sum
	`)
	if got != "10" {
		t.Errorf("got %q, want 10", got)
	}
}

func TestWhileLoop(t *testing.T) {
	got := evalStr(t, `
		var n = 0;
		var i = 0;
		while (i < 5) {
			n += i;
			i++;
		}
		n
	`)
	if got != "10" {
		t.Errorf("got %q, want 10", got)
	}
}

func TestBreakContinue(t *testing.T) {
	got := evalStr(t, `
		var sum = 0;
		for (var i = 0; i < 10; i++) {
			if (i === 5) break;
			if (i % 2 === 0) continue;
			sum += i;
		}
		sum
	`)
	// i=1,3 → sum=4 (skips evens, breaks at 5)
	if got != "4" {
		t.Errorf("got %q, want 4", got)
	}
}

func TestTryCatchFinally(t *testing.T) {
	got := evalStr(t, `
		var result = '';
		try {
			result += 'try';
			throw 'error';
		} catch(e) {
			result += '-' + e;
		} finally {
			result += '-finally';
		}
		result
	`)
	if got != "try-error-finally" {
		t.Errorf("got %q, want try-error-finally", got)
	}
}

// === Array Methods =========================================================

func TestArrayPush(t *testing.T) {
	got := evalStr(t, "var a = [1,2]; a.push(3); a.length")
	if got != "3" {
		t.Errorf("got %q, want 3", got)
	}
}

func TestArrayMap(t *testing.T) {
	v, err := evalTest(t, "[1,2,3].map(x => x * x)")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	elems := arr.Elems()
	if len(elems) != 3 {
		t.Fatalf("len = %d, want 3", len(elems))
	}
	for i, e := range elems {
		n, _ := e.Float()
		want := float64((i + 1) * (i + 1))
		if n != want {
			t.Errorf("elem[%d] = %v, want %v", i, n, want)
		}
	}
}

func TestArrayFilter(t *testing.T) {
	v, err := evalTest(t, "[1,2,3,4,5].filter(x => x > 2)")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	elems := arr.Elems()
	if len(elems) != 3 {
		t.Fatalf("len = %d, want 3", len(elems))
	}
}

func TestArrayReduce(t *testing.T) {
	got := evalStr(t, "[1,2,3,4].reduce((a, b) => a + b, 0)")
	if got != "10" {
		t.Errorf("got %q, want 10", got)
	}
}

func TestArrayJoin(t *testing.T) {
	got := evalStr(t, "['a','b','c'].join('-')")
	if got != "a-b-c" {
		t.Errorf("got %q, want a-b-c", got)
	}
}

func TestArrayIndexOf(t *testing.T) {
	got := evalStr(t, "[10,20,30].indexOf(20)")
	if got != "1" {
		t.Errorf("got %q, want 1", got)
	}
}

func TestArrayIncludes(t *testing.T) {
	v, err := evalTest(t, "[1,2,3].includes(2)")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	b, _ := v.Bool()
	if !b {
		t.Error("includes(2) = false, want true")
	}
}

func TestArraySlice(t *testing.T) {
	v, err := evalTest(t, "[1,2,3,4,5].slice(1, 3)")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	elems := arr.Elems()
	if len(elems) != 2 {
		t.Fatalf("len = %d, want 2", len(elems))
	}
	n0, _ := elems[0].Float()
	n1, _ := elems[1].Float()
	if n0 != 2 || n1 != 3 {
		t.Errorf("got [%v, %v], want [2, 3]", n0, n1)
	}
}

// === String Methods ========================================================

func TestStringUpper(t *testing.T) {
	got := evalStr(t, "'hello'.toUpperCase()")
	if got != "HELLO" {
		t.Errorf("got %q, want HELLO", got)
	}
}

func TestStringLower(t *testing.T) {
	got := evalStr(t, "'WORLD'.toLowerCase()")
	if got != "world" {
		t.Errorf("got %q, want world", got)
	}
}

func TestStringSlice(t *testing.T) {
	got := evalStr(t, "'hello world'.slice(0, 5)")
	if got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestStringSplit(t *testing.T) {
	v, err := evalTest(t, "'a,b,c'.split(',')")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	elems := arr.Elems()
	if len(elems) != 3 {
		t.Fatalf("len = %d, want 3", len(elems))
	}
}

func TestStringIndexOf(t *testing.T) {
	got := evalStr(t, "'hello world'.indexOf('world')")
	if got != "6" {
		t.Errorf("got %q, want 6", got)
	}
}

func TestStringTrim(t *testing.T) {
	got := evalStr(t, "'  hi  '.trim()")
	if got != "hi" {
		t.Errorf("got %q, want hi", got)
	}
}

func TestStringReplace(t *testing.T) {
	got := evalStr(t, "'hello'.replace('l', 'L')")
	if got != "heLlo" {
		t.Errorf("got %q, want heLlo", got)
	}
}

func TestStringIncludes(t *testing.T) {
	v, err := evalTest(t, "'hello world'.includes('world')")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	b, _ := v.Bool()
	if !b {
		t.Error("includes('world') = false, want true")
	}
}

func TestStringRepeat(t *testing.T) {
	got := evalStr(t, "'ab'.repeat(3)")
	if got != "ababab" {
		t.Errorf("got %q, want ababab", got)
	}
}

func TestStringCharAt(t *testing.T) {
	got := evalStr(t, "'hello'.charAt(1)")
	if got != "e" {
		t.Errorf("got %q, want e", got)
	}
}

func TestStringCharCodeAt(t *testing.T) {
	got := evalStr(t, "'A'.charCodeAt(0)")
	if got != "65" {
		t.Errorf("got %q, want 65", got)
	}
}

// === Object Methods ========================================================

func TestObjectKeys(t *testing.T) {
	v, err := evalTest(t, "Object.keys({a:1, b:2, c:3})")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	elems := arr.Elems()
	if len(elems) != 3 {
		t.Fatalf("len = %d, want 3", len(elems))
	}
}

func TestObjectValues(t *testing.T) {
	v, err := evalTest(t, "Object.values({a:1, b:2})")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	if len(arr.Elems()) != 2 {
		t.Fatalf("len = %d, want 2", len(arr.Elems()))
	}
}

func TestObjectAssign(t *testing.T) {
	got := evalStr(t, "var o = Object.assign({}, {a:1}, {b:2}); o.a + o.b")
	if got != "3" {
		t.Errorf("got %q, want 3", got)
	}
}

func TestHasOwnProperty(t *testing.T) {
	v, err := evalTest(t, "({a:1}).hasOwnProperty('a')")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	b, _ := v.Bool()
	if !b {
		t.Error("hasOwnProperty('a') = false, want true")
	}
}

// === Number Methods ========================================================

func TestNumberToFixed(t *testing.T) {
	got := evalStr(t, "(3.14159).toFixed(2)")
	if got != "3.14" {
		t.Errorf("got %q, want 3.14", got)
	}
}

// === Math ==================================================================

func TestMathAbs(t *testing.T) {
	got := evalStr(t, "Math.abs(-5)")
	if got != "5" {
		t.Errorf("got %q, want 5", got)
	}
}

func TestMathMax(t *testing.T) {
	got := evalStr(t, "Math.max(1, 5, 3)")
	if got != "5" {
		t.Errorf("got %q, want 5", got)
	}
}

func TestMathFloor(t *testing.T) {
	got := evalStr(t, "Math.floor(3.7)")
	if got != "3" {
		t.Errorf("got %q, want 3", got)
	}
}

func TestMathPow(t *testing.T) {
	got := evalStr(t, "Math.pow(2, 10)")
	if got != "1024" {
		t.Errorf("got %q, want 1024", got)
	}
}

// === JSON ==================================================================

func TestJSONParse(t *testing.T) {
	got := evalStr(t, `JSON.parse('{"a":1,"b":2}').a`)
	if got != "1" {
		t.Errorf("got %q, want 1", got)
	}
}

func TestJSONStringify(t *testing.T) {
	got := evalStr(t, `JSON.stringify({a:1, b:[1,2]})`)
	// JSON output should contain the expected fields
	if !strings.Contains(got, `"a":1`) || !strings.Contains(got, `"b"`) {
		t.Errorf("got %q, want JSON containing a:1 and b:[1,2]", got)
	}
}

// === Error Handling ========================================================

func TestThrowCatch(t *testing.T) {
	got := evalStr(t, `
		try {
			throw new Error('test error');
		} catch(e) {
			e.message
		}
	`)
	if got != "test error" {
		t.Errorf("got %q, want test error", got)
	}
}

func TestTypeErrorOnNull(t *testing.T) {
	v, err := evalTest(t, `
		try {
			null.foo;
		} catch(e) {
			e.name
		}
	`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if v.String() != "TypeError" {
		t.Errorf("got %q, want TypeError", v.String())
	}
}

// === Function Methods ======================================================

func TestFunctionCall(t *testing.T) {
	got := evalStr(t, `
		function greet(greeting) { return greeting + ', ' + this.name; }
		var obj = {name: 'World'};
		greet.call(obj, 'Hello')
	`)
	if got != "Hello, World" {
		t.Errorf("got %q, want Hello, World", got)
	}
}

func TestFunctionApply(t *testing.T) {
	got := evalStr(t, `
		function sum(a, b) { return a + b; }
		sum.apply(null, [3, 4])
	`)
	if got != "7" {
		t.Errorf("got %q, want 7", got)
	}
}

func TestFunctionBind(t *testing.T) {
	got := evalStr(t, `
		function greet(greeting) { return greeting + ', ' + this.name; }
		var obj = {name: 'World'};
		var bound = greet.bind(obj, 'Hi');
		bound()
	`)
	if got != "Hi, World" {
		t.Errorf("got %q, want Hi, World", got)
	}
}

// === For...of ===============================================================

func TestForOf(t *testing.T) {
	got := evalStr(t, `
		var sum = 0;
		for (var x of [1, 2, 3]) {
			sum += x;
		}
		sum
	`)
	if got != "6" {
		t.Errorf("got %q, want 6", got)
	}
}

func TestForOfString(t *testing.T) {
	got := evalStr(t, `
		var s = '';
		for (var ch of 'abc') {
			s += ch;
		}
		s
	`)
	if got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
}

// === typeof =================================================================

func TestTypeof(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"typeof 42", "number"},
		{"typeof 'hi'", "string"},
		{"typeof true", "boolean"},
		{"typeof undefined", "undefined"},
		{"typeof null", "object"},
		{"typeof function(){}", "function"},
		{"typeof {}", "object"},
		{"typeof []", "object"},
	}
	for _, c := range cases {
		got := evalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === Ternary ================================================================

func TestTernary(t *testing.T) {
	got := evalStr(t, "5 > 3 ? 'yes' : 'no'")
	if got != "yes" {
		t.Errorf("got %q, want yes", got)
	}
}

// === Optional chaining (?.) — AST interpreter path ==========================

func TestOptionalChainingMember(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`null?.x`, "undefined"},
		{`undefined?.x`, "undefined"},
		{`({x: 1})?.x`, "1"},
		{`({})?.x`, "undefined"},
		{`var a = {b: {c: 7}}; a?.b?.c`, "7"},
		{`var a = {b: null}; a?.b?.c`, "undefined"},
		{`var a = null; a?.b?.c?.d`, "undefined"},
	}
	for _, c := range cases {
		got := evalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestOptionalChainingComputed(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`null?.[0]`, "undefined"},
		{`([10, 20, 30])?.[1]`, "20"},
		{`null?.[0]?.[1]`, "undefined"},
	}
	for _, c := range cases {
		got := evalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === 循环引用防护（回归） ================================================
//
// 自引用结构在 JSON.stringify / 对象打印（String）时若无限递归会导致
// Go 栈溢出崩溃、内存暴涨。此处验证已加环检测，且共享非循环引用不误判。

func TestJSONStringifyCircularThrows(t *testing.T) {
	code := `var a = {}; a.self = a; try { JSON.stringify(a); 'no-throw' } catch (e) { e.name }`
	got := evalStr(t, code)
	if got != "TypeError" {
		t.Errorf("JSON.stringify(circular) err name = %q, want TypeError", got)
	}
}

func TestJSONStringifySharedNotCircular(t *testing.T) {
	code := `var x = {a: 1}; JSON.stringify({p: x, q: x})`
	got := evalStr(t, code)
	if got != `{"p":{"a":1},"q":{"a":1}}` {
		t.Errorf("JSON.stringify(shared) = %q", got)
	}
}

func TestObjectStringCircular(t *testing.T) {
	code := `var a = {n: 1}; a.self = a; String(a)`
	got := evalStr(t, code)
	if !strings.Contains(got, "Circular") {
		t.Errorf("String(circular) = %q, want contain [Circular]", got)
	}
}

func TestArrayStringCircular(t *testing.T) {
	code := `var a = [1, 2]; a.push(a); String(a)`
	got := evalStr(t, code)
	if !strings.Contains(got, "Circular") {
		t.Errorf("String(circular array) = %q, want contain [Circular]", got)
	}
}

func TestOptionalChainingCall(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`null?.()`, "undefined"},
		{`(function() { return 42 })?.()`, "42"},
		{`var f = function(a, b) { return a + b }; f?.(2, 3)`, "5"},
	}
	for _, c := range cases {
		got := evalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestOptionalChainingMethodCall(t *testing.T) {
	got := evalStr(t, `var o = {greet: function() { return "hi" }}; o?.greet()`)
	if got != "hi" {
		t.Errorf("o?.greet() = %q, want hi", got)
	}
	got = evalStr(t, `var o = null; o?.greet()`)
	if got != "undefined" {
		t.Errorf("null?.greet() = %q, want undefined", got)
	}
	// a.b?.() — method nullish, skip call
	got = evalStr(t, `var o = {greet: null}; o.greet?.()`)
	if got != "undefined" {
		t.Errorf("o.greet?.() with null method = %q, want undefined", got)
	}
	got = evalStr(t, `var o = {greet: function() { return this.name }, name: "x"}; o?.greet?.()`)
	if got != "x" {
		t.Errorf("o?.greet?.() = %q, want x", got)
	}
}
