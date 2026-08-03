package interpreter

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// vmEvalTest is a helper that creates a VM and evaluates code.
func vmEvalTest(t *testing.T, code string) (engine.Value, error) {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	return vm.Eval(code, "test.js")
}

// vmEvalStr evaluates code and returns the string representation of the result.
func vmEvalStr(t *testing.T, code string) string {
	t.Helper()
	v, err := vmEvalTest(t, code)
	if err != nil {
		t.Fatalf("VM.Eval(%q) error: %v", code, err)
	}
	return v.String()
}

// vmEvalStrErr evaluates code and returns the result string or error.
func vmEvalStrErr(t *testing.T, code string) (string, error) {
	t.Helper()
	v, err := vmEvalTest(t, code)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// === Basic arithmetic ====================================================

func TestVMArithmetic(t *testing.T) {
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
		{"2 ** 3", "8"},
		{"-5", "-5"},
		{"+5", "5"},
		{"10 / 3", "3.3333333333333335"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMStringConcat(t *testing.T) {
	got := vmEvalStr(t, `"hello" + " " + "world"`)
	if got != "hello world" {
		t.Errorf(`VM.Eval("hello" + " " + "world") = %q, want "hello world"`, got)
	}
}

func TestVMTemplateLiteral(t *testing.T) {
	got := vmEvalStr(t, "`hello world`")
	if got != "hello world" {
		t.Errorf("template literal = %q, want hello world", got)
	}
	got = vmEvalStr(t, "var n = 'world'; `Hello, ${n}!`")
	if got != "Hello, world!" {
		t.Errorf("template interp = %q, want Hello, world!", got)
	}
	got = vmEvalStr(t, "var a = 10, b = 20; `${a} + ${b} = ${a+b}`")
	if got != "10 + 20 = 30" {
		t.Errorf("template multi = %q, want 10 + 20 = 30", got)
	}
	got = vmEvalStr(t, "var x = 5; `nested: ${`inner ${x}`}`")
	if got != "nested: inner 5" {
		t.Errorf("template nested = %q, want nested: inner 5", got)
	}
}

// === Variables and scope ==================================================

func TestVMVariables(t *testing.T) {
	got := vmEvalStr(t, "var x = 42; x")
	if got != "42" {
		t.Errorf("var x = 42; x = %q, want 42", got)
	}
	got = vmEvalStr(t, "let y = 10; y + 5")
	if got != "15" {
		t.Errorf("let y = 10; y + 5 = %q, want 15", got)
	}
	got = vmEvalStr(t, "const z = 100; z * 2")
	if got != "200" {
		t.Errorf("const z = 100; z * 2 = %q, want 200", got)
	}
}

// === Control flow =========================================================

func TestVMIfElse(t *testing.T) {
	got := vmEvalStr(t, "if (true) { 1 } else { 2 }")
	if got != "1" {
		t.Errorf("if(true){1}else{2} = %q, want 1", got)
	}
	got = vmEvalStr(t, "if (false) { 1 } else { 2 }")
	if got != "2" {
		t.Errorf("if(false){1}else{2} = %q, want 2", got)
	}
}

func TestVMWhile(t *testing.T) {
	got := vmEvalStr(t, "var i = 0; var sum = 0; while (i < 5) { sum = sum + i; i = i + 1; } sum")
	if got != "10" {
		t.Errorf("while loop sum = %q, want 10", got)
	}
}

func TestVMFor(t *testing.T) {
	got := vmEvalStr(t, "var s = 0; for (var i = 0; i < 5; i++) { s = s + i; } s")
	if got != "10" {
		t.Errorf("for loop sum = %q, want 10", got)
	}
}

func TestVMBreakSimple(t *testing.T) {
	got := vmEvalStr(t, "for (var i = 0; i < 3; i++) { break; } i")
	if got != "0" {
		t.Errorf("break simple = %q, want 0", got)
	}
}

func TestVMBreakIf(t *testing.T) {
	got := vmEvalStr(t, "for (var i = 0; i < 10; i++) { if (i == 5) break; } i")
	if got != "5" {
		t.Errorf("break if = %q, want 5", got)
	}
}

func TestVMBreakIfWithAssign(t *testing.T) {
	got := vmEvalStr(t, "var s = 0; for (var i = 0; i < 3; i++) { if (i == 1) break; s = s + i; } s")
	if got != "0" {
		t.Errorf("break if+assign = %q, want 0", got)
	}
}

func TestVMBreakIfWithAssignLarge(t *testing.T) {
	got := vmEvalStr(t, "var s = 0; for (var i = 0; i < 10; i++) { if (i == 5) break; s = s + i; } s")
	if got != "10" {
		t.Errorf("break if+assign large = %q, want 10", got)
	}
}

func TestVMContinue(t *testing.T) {
	got := vmEvalStr(t, "var s = 0; for (var i = 0; i < 5; i++) { if (i == 2) continue; s = s + i; } s")
	if got != "8" {
		t.Errorf("continue: sum = %q, want 8", got)
	}
}

func TestVMBreakContinue(t *testing.T) {
	got := vmEvalStr(t, "var s = 0; for (var i = 0; i < 10; i++) { if (i == 5) break; s = s + i; } s")
	if got != "10" {
		t.Errorf("break: sum = %q, want 10", got)
	}
	got = vmEvalStr(t, "var s = 0; for (var i = 0; i < 5; i++) { if (i == 2) continue; s = s + i; } s")
	if got != "8" {
		t.Errorf("continue: sum = %q, want 8", got)
	}
}

// === Functions and closures ===============================================

func TestVMFunction(t *testing.T) {
	got := vmEvalStr(t, "function add(a, b) { return a + b; } add(3, 4)")
	if got != "7" {
		t.Errorf("add(3,4) = %q, want 7", got)
	}
}

func TestVMRecursiveFib(t *testing.T) {
	got := vmEvalStr(t, `function fib(n) { if (n < 2) return n; return fib(n-1) + fib(n-2); } fib(10)`)
	if got != "55" {
		t.Errorf("fib(10) = %q, want 55", got)
	}
}

func TestVMClosure(t *testing.T) {
	got := vmEvalStr(t, `
function makeCounter() {
  var count = 0;
  return function() { count = count + 1; return count; };
}
var c = makeCounter();
c(); c(); c()`)
	if got != "3" {
		t.Errorf("closure counter = %q, want 3", got)
	}
}

func TestVMArrowFunction(t *testing.T) {
	got := vmEvalStr(t, "var sq = (x) => x * x; sq(5)")
	if got != "25" {
		t.Errorf("arrow sq(5) = %q, want 25", got)
	}
}

// === Objects and arrays ===================================================

func TestVMObjectLiteral(t *testing.T) {
	got := vmEvalStr(t, "var o = {a: 1, b: 2}; o.a + o.b")
	if got != "3" {
		t.Errorf("object access = %q, want 3", got)
	}
}

func TestVMArrayLiteral(t *testing.T) {
	got := vmEvalStr(t, "var arr = [10, 20, 30]; arr[0] + arr[1] + arr[2]")
	if got != "60" {
		t.Errorf("array access = %q, want 60", got)
	}
	got = vmEvalStr(t, "[1,2,3].length")
	if got != "3" {
		t.Errorf("array length = %q, want 3", got)
	}
}

func TestVMArrayMethods(t *testing.T) {
	got := vmEvalStr(t, "[1,2,3].join('-')")
	if got != "1-2-3" {
		t.Errorf("array join = %q, want 1-2-3", got)
	}
	got = vmEvalStr(t, "[1,2,3].push(4); [1,2,3,4].length")
	// push returns new length
	_ = got
}

func TestVMStringMethods(t *testing.T) {
	got := vmEvalStr(t, `"hello".length`)
	if got != "5" {
		t.Errorf(`"hello".length = %q, want 5`, got)
	}
	got = vmEvalStr(t, `"hello".toUpperCase()`)
	if got != "HELLO" {
		t.Errorf(`"hello".toUpperCase() = %q, want HELLO`, got)
	}
	got = vmEvalStr(t, `"hello"[1]`)
	if got != "e" {
		t.Errorf(`"hello"[1] = %q, want e`, got)
	}
}

// === Logical operators ====================================================

func TestVMLogical(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"true && false", "false"},
		{"true && true", "true"},
		{"false || true", "true"},
		{"false || false", "false"},
		{"1 && 2", "2"},
		{"0 || 'default'", "default"},
		{"null ?? 'fallback'", "fallback"},
		{"'x' ?? 'y'", "x"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === Comparisons ==========================================================

func TestVMComparisons(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"1 === 1", "true"},
		{"1 === 2", "false"},
		{"1 !== 2", "true"},
		{"1 == '1'", "true"},
		{"1 === '1'", "false"},
		{"3 > 2", "true"},
		{"3 < 2", "false"},
		{"3 >= 3", "true"},
		{"2 <= 1", "false"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === Try/catch ============================================================

func TestVMTryCatch(t *testing.T) {
	got := vmEvalStr(t, `
var msg = "none";
try { throw "error1"; } catch (e) { msg = e; }
msg`)
	if got != "error1" {
		t.Errorf("try/catch throw string = %q, want error1", got)
	}
}

func TestVMTryCatchTypeError(t *testing.T) {
	got := vmEvalStr(t, `
var msg = "none";
try { null.foo; } catch (e) { msg = "caught"; }
msg`)
	if got != "caught" {
		t.Errorf("try/catch TypeError = %q, want caught", got)
	}
}

func TestVMTryFinally(t *testing.T) {
	got := vmEvalStr(t, `
var log = "";
try { log += "try;"; } finally { log += "finally;"; }
log`)
	if got != "try;finally;" {
		t.Errorf("try/finally = %q, want try;finally;", got)
	}
}

func TestVMTryCatchFinally(t *testing.T) {
	got := vmEvalStr(t, `
var log = "";
try { throw "x"; } catch (e) { log += "catch;"; } finally { log += "finally;"; }
log`)
	if got != "catch;finally;" {
		t.Errorf("try/catch/finally = %q, want catch;finally;", got)
	}
}

// === typeof and instanceof ================================================

func TestVMTypeof(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"typeof 42", "number"},
		{"typeof 'str'", "string"},
		{"typeof true", "boolean"},
		{"typeof undefined", "undefined"},
		{"typeof null", "object"},
		{"typeof {}", "object"},
		{"typeof function(){}", "function"},
		{"typeof undeclaredVar", "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// === Built-in objects ====================================================

func TestVMObjectKeys(t *testing.T) {
	got := vmEvalStr(t, `Object.keys({a:1, b:2}).join(',')`)
	if got != "a,b" {
		t.Errorf("Object.keys = %q, want a,b", got)
	}
}

func TestVMMath(t *testing.T) {
	got := vmEvalStr(t, "Math.max(1, 2, 3)")
	if got != "3" {
		t.Errorf("Math.max = %q, want 3", got)
	}
	got = vmEvalStr(t, "Math.floor(3.7)")
	if got != "3" {
		t.Errorf("Math.floor = %q, want 3", got)
	}
}

func TestVMJSON(t *testing.T) {
	got := vmEvalStr(t, `JSON.stringify({a:1})`)
	if !strings.Contains(got, "a") && !strings.Contains(got, "1") {
		t.Errorf("JSON.stringify = %q, want to contain a and 1", got)
	}
}

// === Error handling ======================================================

func TestVMErrorConstructor(t *testing.T) {
	got := vmEvalStr(t, `new Error("test").message`)
	if got != "test" {
		t.Errorf(`new Error("test").message = %q, want test`, got)
	}
}

func TestVMThrowError(t *testing.T) {
	got := vmEvalStr(t, `
var msg = "";
try { throw new Error("boom"); } catch (e) { msg = e.message; }
msg`)
	if got != "boom" {
		t.Errorf("throw Error = %q, want boom", got)
	}
}

// === Nested closures =====================================================

func TestVMNestedClosures(t *testing.T) {
	got := vmEvalStr(t, `
function outer() {
  var x = 10;
  function middle() {
    var y = 20;
    function inner() {
      return x + y;
    }
    return inner;
  }
  return middle();
}
outer()()`)
	if got != "30" {
		t.Errorf("nested closures = %q, want 30", got)
	}
}

// === For-of ===============================================================

func TestVMForOf(t *testing.T) {
	got := vmEvalStr(t, `
var s = 0;
for (var x of [1, 2, 3, 4]) { s = s + x; }
s`)
	if got != "10" {
		t.Errorf("for-of sum = %q, want 10", got)
	}
}

// === Switch ==============================================================

func TestVMSwitch(t *testing.T) {
	got := vmEvalStr(t, `
function test(n) {
  switch (n) {
    case 1: return "one";
    case 2: return "two";
    default: return "other";
  }
}
test(2)`)
	if got != "two" {
		t.Errorf("switch(2) = %q, want two", got)
	}
}

// === Rest / spread (Phase 1C.7) =========================================

func TestVMRestParam(t *testing.T) {
	// function declaration with rest
	got := vmEvalStr(t, `function sum(...nums) { var t = 0; for (var i = 0; i < nums.length; i++) { t += nums[i]; } return t; } sum(1,2,3,4)`)
	if got != "10" {
		t.Errorf("rest sum = %q, want 10", got)
	}
	// mixed regular + rest params
	got = vmEvalStr(t, `function f(a, b, ...rest) { return a + b + rest.length; } f(1, 2, 3, 4, 5)`)
	if got != "6" {
		t.Errorf("mixed rest = %q, want 6", got)
	}
	// rest with zero extra args
	got = vmEvalStr(t, `function f(a, ...rest) { return rest.length; } f(1)`)
	if got != "0" {
		t.Errorf("empty rest = %q, want 0", got)
	}
}

func TestVMArrowRestParam(t *testing.T) {
	got := vmEvalStr(t, `var sum = (...nums) => { var t = 0; for (var i = 0; i < nums.length; i++) t += nums[i]; return t; }; sum(1,2,3)`)
	if got != "6" {
		t.Errorf("arrow rest sum = %q, want 6", got)
	}
	got = vmEvalStr(t, `var f = (a, ...r) => a + r.length; f(10, 20, 30)`)
	if got != "12" {
		t.Errorf("arrow mixed rest = %q, want 12", got)
	}
}

func TestVMSpreadCall(t *testing.T) {
	// f(...arr)
	got := vmEvalStr(t, `function sum(a, b, c) { return a + b + c; } var arr = [1, 2, 3]; sum(...arr)`)
	if got != "6" {
		t.Errorf("spread call = %q, want 6", got)
	}
	// mixed spread + regular args
	got = vmEvalStr(t, `function f(a, b, c, d) { return a + b + c + d; } var arr = [2, 3]; f(1, ...arr, 4)`)
	if got != "10" {
		t.Errorf("mixed spread call = %q, want 10", got)
	}
	// spread + rest combo
	got = vmEvalStr(t, `function sum(...nums) { var t = 0; for (var i = 0; i < nums.length; i++) t += nums[i]; return t; } var a = [1,2,3], b = [4,5]; sum(...a, ...b)`)
	if got != "15" {
		t.Errorf("spread+rest combo = %q, want 15", got)
	}
}

func TestVMSpreadMethodCall(t *testing.T) {
	got := vmEvalStr(t, `var obj = { add: function(a, b, c) { return a + b + c; } }; var arr = [10, 20, 30]; obj.add(...arr)`)
	if got != "60" {
		t.Errorf("spread method call = %q, want 60", got)
	}
}

func TestVMSpreadArrayLit(t *testing.T) {
	got := vmEvalStr(t, `var a = [1, 2]; var b = [...a, 3, 4]; b.length`)
	if got != "4" {
		t.Errorf("spread array lit length = %q, want 4", got)
	}
	got = vmEvalStr(t, `var a = [1, 2]; var b = [0, ...a, 3]; b[2]`)
	if got != "2" {
		t.Errorf("spread array lit elem = %q, want 2", got)
	}
	// nested spread
	got = vmEvalStr(t, `var a = [1,2], b = [3,4]; var c = [...a, ...b]; c.length`)
	if got != "4" {
		t.Errorf("nested spread = %q, want 4", got)
	}
}

func TestVMSpreadObjectLit(t *testing.T) {
	got := vmEvalStr(t, `var a = { x: 1, y: 2 }; var b = { ...a, z: 3 }; b.x + b.y + b.z`)
	if got != "6" {
		t.Errorf("spread object lit = %q, want 6", got)
	}
	// override spread prop
	got = vmEvalStr(t, `var a = { x: 1 }; var b = { ...a, x: 99 }; b.x`)
	if got != "99" {
		t.Errorf("spread object override = %q, want 99", got)
	}
}

// === Default parameters (Phase 1C.7) =====================================

func TestVMDefaultParam(t *testing.T) {
	// single default
	got := vmEvalStr(t, `function greet(name, greeting) { greeting = greeting || 'hi'; return greeting + ' ' + name; } greet('bob')`)
	_ = got
	got = vmEvalStr(t, `function f(a, b = 10) { return a + b; } f(5)`)
	if got != "15" {
		t.Errorf("default param = %q, want 15", got)
	}
	// default overridden by explicit arg
	got = vmEvalStr(t, `function f(a, b = 10) { return a + b; } f(5, 20)`)
	if got != "25" {
		t.Errorf("default overridden = %q, want 25", got)
	}
	// multiple defaults
	got = vmEvalStr(t, `function f(a, b = 2, c = 3) { return a + b + c; } f(1)`)
	if got != "6" {
		t.Errorf("multiple defaults = %q, want 6", got)
	}
	// default uses expression
	got = vmEvalStr(t, `function f(a, b = a * 2) { return a + b; } f(3)`)
	if got != "9" {
		t.Errorf("default expr = %q, want 9", got)
	}
	// falsy-but-defined value (0) should NOT trigger default
	got = vmEvalStr(t, `function f(a, b = 99) { return b; } f(1, 0)`)
	if got != "0" {
		t.Errorf("falsy defined = %q, want 0", got)
	}
}

func TestVMArrowDefaultParam(t *testing.T) {
	got := vmEvalStr(t, `var f = (a, b = 5) => a + b; f(2)`)
	if got != "7" {
		t.Errorf("arrow default = %q, want 7", got)
	}
	got = vmEvalStr(t, `var f = (a = 1, b = 2) => a + b; f()`)
	if got != "3" {
		t.Errorf("arrow all defaults = %q, want 3", got)
	}
}

func TestVMDefaultPlusRest(t *testing.T) {
	// default + rest combined
	got := vmEvalStr(t, `function f(a = 1, ...rest) { return a + rest.length; } f()`)
	if got != "1" {
		t.Errorf("default+rest = %q, want 1", got)
	}
	got = vmEvalStr(t, `function f(a = 1, ...rest) { return a + rest.length; } f(10, 20, 30)`)
	if got != "12" {
		t.Errorf("default+rest args = %q, want 12", got)
	}
}

// === Destructuring (ES2015) ===============================================

func TestVMArrayDestructuring(t *testing.T) {
	got := vmEvalStr(t, `var [a, b] = [1, 2]; a + b`)
	if got != "3" {
		t.Errorf("array destr basic = %q, want 3", got)
	}
	// fewer elements than values
	got = vmEvalStr(t, `var [a, b] = [10, 20, 30]; a + b`)
	if got != "30" {
		t.Errorf("array destr fewer = %q, want 30", got)
	}
	// more elements than values → undefined for missing
	got = vmEvalStr(t, `var [a, b, c] = [1, 2]; "" + a + b + c`)
	if got != "12undefined" {
		t.Errorf("array destr more = %q, want 12undefined", got)
	}
}

func TestVMArrayDestructuringHoles(t *testing.T) {
	got := vmEvalStr(t, `var [a, , c] = [1, 2, 3]; a + c`)
	if got != "4" {
		t.Errorf("array destr holes = %q, want 4", got)
	}
}

func TestVMArrayDestructuringDefault(t *testing.T) {
	got := vmEvalStr(t, `var [a = 10, b = 20] = [1]; a + b`)
	if got != "21" {
		t.Errorf("array destr default = %q, want 21", got)
	}
	// default applies when value is undefined
	got = vmEvalStr(t, `var [a = 99] = [undefined]; a`)
	if got != "99" {
		t.Errorf("array destr default undefined = %q, want 99", got)
	}
}

func TestVMArrayDestructuringRest(t *testing.T) {
	got := vmEvalStr(t, `var [a, ...rest] = [1, 2, 3, 4]; a + rest.length`)
	if got != "4" {
		t.Errorf("array destr rest = %q, want 4", got)
	}
	got = vmEvalStr(t, `var [a, ...rest] = [1, 2, 3]; rest[0] + rest[1]`)
	if got != "5" {
		t.Errorf("array destr rest elems = %q, want 5", got)
	}
	// rest with no remaining elements
	got = vmEvalStr(t, `var [a, ...rest] = [1]; rest.length`)
	if got != "0" {
		t.Errorf("array destr rest empty = %q, want 0", got)
	}
}

func TestVMArrayDestructuringDefaultPlusRest(t *testing.T) {
	got := vmEvalStr(t, `var [a = 1, ...rest] = []; a + rest.length`)
	if got != "1" {
		t.Errorf("array destr default+rest = %q, want 1", got)
	}
}

func TestVMObjectDestructuring(t *testing.T) {
	got := vmEvalStr(t, `var {a, b} = {a: 1, b: 2}; a + b`)
	if got != "3" {
		t.Errorf("obj destr shorthand = %q, want 3", got)
	}
	// renamed binding
	got = vmEvalStr(t, `var {a: x, b: y} = {a: 10, b: 20}; x + y`)
	if got != "30" {
		t.Errorf("obj destr renamed = %q, want 30", got)
	}
	// missing property → undefined
	got = vmEvalStr(t, `var {a, b} = {a: 1}; "" + a + b`)
	if got != "1undefined" {
		t.Errorf("obj destr missing = %q, want 1undefined", got)
	}
}

func TestVMObjectDestructuringDefault(t *testing.T) {
	got := vmEvalStr(t, `var {a = 10, b = 20} = {a: 1}; a + b`)
	if got != "21" {
		t.Errorf("obj destr default = %q, want 21", got)
	}
	// default applies when undefined
	got = vmEvalStr(t, `var {a = 99} = {a: undefined}; a`)
	if got != "99" {
		t.Errorf("obj destr default undefined = %q, want 99", got)
	}
	// renamed binding with default
	got = vmEvalStr(t, `var {a: x = 5, b: y = 6} = {a: 1}; x + y`)
	if got != "7" {
		t.Errorf("obj destr renamed default = %q, want 7", got)
	}
}

func TestVMObjectDestructuringRest(t *testing.T) {
	got := vmEvalStr(t, `var {a, ...rest} = {a: 1, b: 2, c: 3}; a + rest.b + rest.c`)
	if got != "6" {
		t.Errorf("obj destr rest = %q, want 6", got)
	}
	// rest should NOT contain bound keys
	got = vmEvalStr(t, `var {a, b, ...rest} = {a: 1, b: 2, c: 3, d: 4}; "" + rest.a + rest.b + rest.c + rest.d`)
	if got != "undefinedundefined34" {
		t.Errorf("obj destr rest excludes = %q, want undefinedundefined34", got)
	}
	// rest with no remaining properties
	got = vmEvalStr(t, `var {a, ...rest} = {a: 1}; Object.keys(rest).length`)
	if got != "0" {
		t.Errorf("obj destr rest empty = %q, want 0", got)
	}
}

func TestVMNestedDestructuring(t *testing.T) {
	// nested array in array
	got := vmEvalStr(t, `var [[a, b], c] = [[1, 2], 3]; a + b + c`)
	if got != "6" {
		t.Errorf("nested array in array = %q, want 6", got)
	}
	// nested object in array
	got = vmEvalStr(t, `var [{a, b}, c] = [{a: 1, b: 2}, 3]; a + b + c`)
	if got != "6" {
		t.Errorf("nested object in array = %q, want 6", got)
	}
	// nested array in object
	got = vmEvalStr(t, `var {a: [x, y], b} = {a: [1, 2], b: 3}; x + y + b`)
	if got != "6" {
		t.Errorf("nested array in object = %q, want 6", got)
	}
	// nested object in object
	got = vmEvalStr(t, `var {a: {x, y}, b} = {a: {x: 1, y: 2}, b: 3}; x + y + b`)
	if got != "6" {
		t.Errorf("nested object in object = %q, want 6", got)
	}
}

func TestVMDestructuringLetConst(t *testing.T) {
	got := vmEvalStr(t, `let [a, b] = [10, 20]; a + b`)
	if got != "30" {
		t.Errorf("let array destr = %q, want 30", got)
	}
	got = vmEvalStr(t, `const {a, b} = {a: 1, b: 2}; a + b`)
	if got != "3" {
		t.Errorf("const obj destr = %q, want 3", got)
	}
}

func TestVMDestructuringForOf(t *testing.T) {
	got := vmEvalStr(t, `var s = 0; for (var [a, b] of [[1, 2], [3, 4]]) { s += a + b; } s`)
	if got != "10" {
		t.Errorf("for-of array destr = %q, want 10", got)
	}
	got = vmEvalStr(t, `var s = 0; for (var {x, y} of [{x: 1, y: 2}, {x: 3, y: 4}]) { s += x + y; } s`)
	if got != "10" {
		t.Errorf("for-of obj destr = %q, want 10", got)
	}
}

func TestVMDestructuringMultiple(t *testing.T) {
	got := vmEvalStr(t, `var [a, b] = [1, 2], {c, d} = {c: 3, d: 4}; a + b + c + d`)
	if got != "10" {
		t.Errorf("multiple destr = %q, want 10", got)
	}
}

// === delete operator =====================================================

func TestVMDeleteProp(t *testing.T) {
	got := vmEvalStr(t, `var o = {a: 1, b: 2}; delete o.a; "" + o.a + o.b`)
	if got != "undefined2" {
		t.Errorf("delete prop = %q, want undefined2", got)
	}
	// delete returns true
	got = vmEvalStr(t, `var o = {a: 1}; delete o.a`)
	if got != "true" {
		t.Errorf("delete returns = %q, want true", got)
	}
	// delete non-existent property returns true
	got = vmEvalStr(t, `var o = {}; delete o.nope`)
	if got != "true" {
		t.Errorf("delete non-existent = %q, want true", got)
	}
	// Object.keys should not include deleted key
	got = vmEvalStr(t, `var o = {a: 1, b: 2, c: 3}; delete o.b; Object.keys(o).length`)
	if got != "2" {
		t.Errorf("delete keys count = %q, want 2", got)
	}
}
