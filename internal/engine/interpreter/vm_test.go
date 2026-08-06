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

func TestVMStringConcatRopeSemantics(t *testing.T) {
	got := vmEvalStr(t, `
let s = "";
for (let i = 0; i < 10000; i++) s += "chunk" + i;
[s.length, s === ("chunk0" + s.slice(6)), s.slice(-9)].join("|");
`)
	if got != "88890|true|chunk9999" {
		t.Fatalf("rope string semantics = %q, want %q", got, "88890|true|chunk9999")
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

func TestVMTaggedTemplate(t *testing.T) {
	// 基本：tag 接收 TemplateStringsArray + 插值。
	got := vmEvalStr(t, "function tag(s, v) { return s[0] + \"|\" + v + \"|\" + s[1]; } tag`a${1+1}b`")
	if got != "a|2|b" {
		t.Errorf("tagged template = %q, want a|2|b", got)
	}
	// cooked vs raw：cooked 处理转义，raw 保留原文。
	got = vmEvalStr(t, "function c(s){ return s[0]; } c`\\n`")
	if got != "\n" {
		t.Errorf("cooked quasi = %q, want newline", got)
	}
	got = vmEvalStr(t, "function r(s){ return s.raw[0]; } r`\\n`")
	if got != `\n` {
		t.Errorf("raw quasi = %q, want literal backslash-n", got)
	}
	// 转义 ${ 不产生伪插值（拆分边界以 raw 文本为准）。
	got = vmEvalStr(t, "function r(s){ return s.raw[0] + \"|\" + s[0]; } r`\\${foo}`")
	if got != `\${foo}|${foo}` {
		t.Errorf("escaped ${ quasi = %q, want \\${foo}|${foo}", got)
	}
	// 成员访问 tag：this 为接收者。
	got = vmEvalStr(t, "var o = { n: 7, tag: function(s) { return this.n + s[0]; } }; o.tag`hi`")
	if got != "7hi" {
		t.Errorf("member tagged template = %q, want 7hi", got)
	}
	// 多插值顺序。
	got = vmEvalStr(t, "function j(s, a, b, c) { return s[0]+a+s[1]+b+s[2]+c+s[3]; } j`${1},${2},${3}`")
	if got != "1,2,3" {
		t.Errorf("multi-interp tagged = %q, want 1,2,3", got)
	}
	// String.raw：按 .raw 数组拼接，保留转义原文。
	got = vmEvalStr(t, `String.raw`+"`"+`a\nb`+"`")
	if got != `a\nb` {
		t.Errorf("String.raw = %q, want a\\nb", got)
	}
	got = vmEvalStr(t, `String.raw`+"`"+`x${1+2}y`+"`")
	if got != "x3y" {
		t.Errorf("String.raw interp = %q, want x3y", got)
	}
	// String.raw 普通调用：首参为含 .raw 的对象。
	got = vmEvalStr(t, `String.raw({raw: ["a", "b"]}, "X")`)
	if got != "aXb" {
		t.Errorf("String.raw plain call = %q, want aXb", got)
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

func TestVMFunctionLoopControlFlowIsIsolated(t *testing.T) {
	got := vmEvalStr(t, `
var total = 0;
for (var outer = 0; outer < 1; outer++) {
  function nested() {
    for (var i = 0; i < 2; i++) {
      for (var j = 0; j < 2; j++) {
        if (j === 0) continue;
        total++;
      }
    }
  }
  nested();
}
total
`)
	if got != "2" {
		t.Errorf("nested function loop control flow = %q, want 2", got)
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

func TestVMObjectLiteralBatchConstruction(t *testing.T) {
	got := vmEvalStr(t, `
let order = "";
function value(label, result) { order += label; return result; }
const o = { a: value("a", 1), b: value("b", 2), a: value("c", 3) };
[order, o.a, o.b, Object.keys(o).join("")].join("|");
`)
	if got != "abc|3|2|ab" {
		t.Fatalf("batch object literal semantics = %q, want %q", got, "abc|3|2|ab")
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

func TestVMObjectDestructuringComputedProperties(t *testing.T) {
	got := vmEvalStr(t, `
const first = Symbol("first");
const second = "second";
const source = { [first]: 20, second: 22, keep: 1 };
const { [first]: a, [second]: b, ...rest } = source;
a + b + ":" + rest.keep + ":" + Object.keys(rest).length;
`)
	if got != "42:1:1" {
		t.Errorf("computed object destructuring = %q, want 42:1:1", got)
	}
}

func TestVMClassMethodDestructuringParameters(t *testing.T) {
	got := vmEvalStr(t, `
function fallback() { return 40; }
class Example {
  constructor({ factory = fallback } = {}) { this.factory = factory; }
  add({ value }, [extra]) { return this.factory() + value + extra; }
}
new Example().add({ value: 1 }, [1]);
`)
	if got != "42" {
		t.Errorf("class method destructuring parameters = %q, want 42", got)
	}
}

// === Class syntax (ES2015) ===============================================

func TestVMClassBasic(t *testing.T) {
	// Simple class with a constructor and method.
	got := vmEvalStr(t, `
		class Point {
			constructor(x, y) { this.x = x; this.y = y; }
			sum() { return this.x + this.y; }
		}
		var p = new Point(3, 4);
		p.sum()
	`)
	if got != "7" {
		t.Errorf("class basic = %q, want 7", got)
	}
}

func TestVMClassInstanceProperties(t *testing.T) {
	got := vmEvalStr(t, `
		class Box {
			constructor(v) { this.val = v; }
		}
		var b = new Box(42);
		b.val
	`)
	if got != "42" {
		t.Errorf("class instance prop = %q, want 42", got)
	}
}

func TestVMClassStaticMethod(t *testing.T) {
	got := vmEvalStr(t, `
		class Math2 {
			static double(n) { return n * 2; }
		}
		Math2.double(21)
	`)
	if got != "42" {
		t.Errorf("class static method = %q, want 42", got)
	}
}

func TestVMClassGetterSetter(t *testing.T) {
	got := vmEvalStr(t, `
		class Temp {
			constructor(c) { this._c = c; }
			get f() { return this._c * 9 / 5 + 32; }
			set f(v) { this._c = (v - 32) * 5 / 9; }
		}
		var t = new Temp(100);
		t.f
	`)
	if got != "212" {
		t.Errorf("class getter = %q, want 212", got)
	}

	got = vmEvalStr(t, `
		class Temp {
			constructor(c) { this._c = c; }
			get f() { return this._c * 9 / 5 + 32; }
			set f(v) { this._c = (v - 32) * 5 / 9; }
		}
		var t = new Temp(0);
		t.f = 212;
		t._c
	`)
	if got != "100" {
		t.Errorf("class setter = %q, want 100", got)
	}
}

func TestVMClassGetterOnly(t *testing.T) {
	// Read-only getter (no setter).
	got := vmEvalStr(t, `
		class Circle {
			constructor(r) { this.r = r; }
			get area() { return 3 * this.r * this.r; }
		}
		var c = new Circle(5);
		c.area
	`)
	if got != "75" {
		t.Errorf("class getter only = %q, want 75", got)
	}
}

func TestVMClassInheritance(t *testing.T) {
	got := vmEvalStr(t, `
		class Animal {
			constructor(name) { this.name = name; }
			speak() { return this.name + " makes a sound"; }
		}
		class Dog extends Animal {
			constructor(name) { super(name); }
			speak() { return this.name + " barks"; }
		}
		var d = new Dog("Rex");
		d.speak()
	`)
	if got != "Rex barks" {
		t.Errorf("class inheritance = %q, want 'Rex barks'", got)
	}
}

func TestVMClassSuperMethod(t *testing.T) {
	got := vmEvalStr(t, `
		class Base {
			greet() { return "hello"; }
		}
		class Sub extends Base {
			greet() { return super.greet() + " world"; }
		}
		var s = new Sub();
		s.greet()
	`)
	if got != "hello world" {
		t.Errorf("super.method() = %q, want 'hello world'", got)
	}
}

func TestVMClassSuperConstructor(t *testing.T) {
	got := vmEvalStr(t, `
		class A {
			constructor(x) { this.x = x; }
		}
		class B extends A {
			constructor(x, y) { super(x); this.y = y; }
		}
		var b = new B(10, 20);
		b.x + b.y
	`)
	if got != "30" {
		t.Errorf("super() ctor = %q, want 30", got)
	}
}

func TestVMClassDefaultConstructor(t *testing.T) {
	// Derived class with no explicit constructor gets a default that calls super(...args).
	got := vmEvalStr(t, `
	 class A {
		 constructor(x) { this.x = x; }
	 }
	 class B extends A {}
	 var b = new B(99);
	 b.x
	`)
	if got != "99" {
		t.Errorf("default derived ctor = %q, want 99", got)
	}

	// Base class with no constructor works too.
	got = vmEvalStr(t, `
		class Empty {}
		var e = new Empty();
		typeof e
	`)
	if got != "object" {
		t.Errorf("default base ctor = %q, want object", got)
	}
}

func TestVMClassInstanceof(t *testing.T) {
	got := vmEvalStr(t, `
		class A {}
		class B extends A {}
		var b = new B();
		b instanceof B ? "B" : "notB"
	`)
	if got != "B" {
		t.Errorf("instanceof B = %q, want B", got)
	}

	got = vmEvalStr(t, `
		class A {}
		class B extends A {}
		var b = new B();
		b instanceof A ? "A" : "notA"
	`)
	if got != "A" {
		t.Errorf("instanceof A (inherited) = %q, want A", got)
	}
}

func TestVMClassExpression(t *testing.T) {
	got := vmEvalStr(t, `
		var Foo = class {
			constructor(v) { this.v = v; }
			get() { return this.v; }
		};
		var f = new Foo(123);
		f.get()
	`)
	if got != "123" {
		t.Errorf("class expr = %q, want 123", got)
	}
}

func TestVMNamedClassExpressionSelfReference(t *testing.T) {
	got := vmEvalStr(t, `
		var Inner = "outer";
		var Factory = class Inner {
			static create() { return new Inner(); }
			constructor() { this.ctor = Inner; }
		};
		var value = Factory.create();
		(value instanceof Factory) + ":" + (value.ctor === Factory) + ":" + Inner
	`)
	if got != "true:true:outer" {
		t.Errorf("named class expression self reference = %q, want true:true:outer", got)
	}
}

func TestVMArrayBufferIsView(t *testing.T) {
	got := vmEvalStr(t, `
		ArrayBuffer.isView(new Uint8Array(2)) + ":" +
		ArrayBuffer.isView(new DataView(new ArrayBuffer(2))) + ":" +
		ArrayBuffer.isView(new ArrayBuffer(2))
	`)
	if got != "true:true:false" {
		t.Errorf("ArrayBuffer.isView = %q, want true:true:false", got)
	}
}

func TestVMTypedArrayIterator(t *testing.T) {
	got := vmEvalStr(t, `
		var bytes = new Uint8Array([1, 2, 3]);
		[...bytes].join(",")
	`)
	if got != "1,2,3" {
		t.Errorf("typed array iterator = %q, want 1,2,3", got)
	}
}

func TestVMClassStaticInheritance(t *testing.T) {
	got := vmEvalStr(t, `
		class Base {
			static create() { return "base"; }
		}
		class Sub extends Base {}
		Sub.create()
	`)
	if got != "base" {
		t.Errorf("static inheritance = %q, want base", got)
	}
}

func TestVMClassComputedMethodsPreserveInheritance(t *testing.T) {
	got := vmEvalStr(t, `
		const method = "computed";
		const symbolMethod = Symbol("symbolMethod");
		class Base {
			base() { return "base"; }
		}
		class Sub extends Base {
			constructor() { super(); }
			[method]() { return this.base(); }
			[symbolMethod]() { return "symbol"; }
		}
		const value = new Sub();
		typeof value.base + ":" + value.computed() + ":" + value[symbolMethod]() + ":" + (value instanceof Sub)
	`)
	if got != "function:base:symbol:true" {
		t.Errorf("computed methods with inheritance = %q, want function:base:symbol:true", got)
	}
}

func TestVMClassMethodChaining(t *testing.T) {
	// Methods on prototype are shared across instances.
	got := vmEvalStr(t, `
		class Counter {
			constructor() { this.count = 0; }
			inc() { this.count++; return this; }
			dec() { this.count--; return this; }
		}
		var c = new Counter();
		c.inc().inc().inc().dec();
		c.count
	`)
	if got != "2" {
		t.Errorf("method chaining = %q, want 2", got)
	}
}

func TestVMClassSuperWithArgs(t *testing.T) {
	got := vmEvalStr(t, `
		class Shape {
			constructor(type, size) { this.type = type; this.size = size; }
			describe() { return this.type + ":" + this.size; }
		}
		class Square extends Shape {
			constructor(size) { super("square", size); }
		}
		var s = new Square(10);
		s.describe()
	`)
	if got != "square:10" {
		t.Errorf("super with args = %q, want 'square:10'", got)
	}
}

func TestVMClassConstructorReturn(t *testing.T) {
	// Constructor returning a non-object: the `this` is used.
	got := vmEvalStr(t, `
		class Foo {
			constructor() { this.x = 1; return 42; }
		}
		var f = new Foo();
		f.x
	`)
	if got != "1" {
		t.Errorf("ctor return non-object = %q, want 1", got)
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

// === Iterator protocol & generators ======================================

func TestVMGeneratorBasic(t *testing.T) {
	got := vmEvalStr(t, `
function* gen() {
  yield 1;
  yield 2;
  yield 3;
}
var g = gen();
var r = "";
var n;
n = g.next(); r += n.value + "," + n.done + "|";
n = g.next(); r += n.value + "," + n.done + "|";
n = g.next(); r += n.value + "," + n.done + "|";
n = g.next(); r += n.value + "," + n.done;
r`)
	want := "1,false|2,false|3,false|undefined,true"
	if got != want {
		t.Errorf("generator basic = %q, want %q", got, want)
	}
}

func TestVMGeneratorForOf(t *testing.T) {
	got := vmEvalStr(t, `
function* range(n) {
  for (var i = 1; i <= n; i++) {
    yield i;
  }
}
var s = 0;
for (var x of range(5)) { s += x; }
s`)
	if got != "15" {
		t.Errorf("generator for-of sum = %q, want 15", got)
	}
}

func TestVMGeneratorReturnValue(t *testing.T) {
	got := vmEvalStr(t, `
function* gen() {
  yield 1;
  return 99;
  yield 2;
}
var g = gen();
var r = "";
var n;
n = g.next(); r += n.value + "," + n.done + "|";
n = g.next(); r += n.value + "," + n.done;
r`)
	want := "1,false|99,true"
	if got != want {
		t.Errorf("generator return = %q, want %q", got, want)
	}
}

func TestVMGeneratorDoneAfterReturn(t *testing.T) {
	got := vmEvalStr(t, `
function* gen() { yield 1; return 2; }
var g = gen();
g.next(); g.next();
var n = g.next();
n.value + "," + n.done`)
	if got != "undefined,true" {
		t.Errorf("generator done after return = %q, want undefined,true", got)
	}
}

func TestVMGeneratorExpression(t *testing.T) {
	got := vmEvalStr(t, `
var gen = function*() {
  yield 10;
  yield 20;
};
var g = gen();
var s = 0;
for (var x of g) { s += x; }
s`)
	if got != "30" {
		t.Errorf("generator expression = %q, want 30", got)
	}
}

func TestVMGeneratorYieldStar(t *testing.T) {
	got := vmEvalStr(t, `
function* inner() {
  yield 1;
  yield 2;
}
function* outer() {
  yield 0;
  yield* inner();
  yield 3;
}
var s = "";
for (var x of outer()) { s += x; }
s`)
	if got != "0123" {
		t.Errorf("yield* = %q, want 0123", got)
	}
}

func TestVMGeneratorYieldStarArray(t *testing.T) {
	got := vmEvalStr(t, `
function* gen() {
  yield* [10, 20, 30];
}
var s = 0;
for (var x of gen()) { s += x; }
s`)
	if got != "60" {
		t.Errorf("yield* array = %q, want 60", got)
	}
}

func TestVMGeneratorSendValue(t *testing.T) {
	got := vmEvalStr(t, `
function* counter() {
  var x = 0;
  while (true) {
    var sent = yield x;
    x += 1;
    if (sent === "stop") return x;
  }
}
var g = counter();
var r = "";
r += g.next().value + "|";
r += g.next().value + "|";
r += g.next("stop").value;
r`)
	want := "0|1|2"
	if got != want {
		t.Errorf("generator send value = %q, want %q", got, want)
	}
}

func TestVMForOfString(t *testing.T) {
	got := vmEvalStr(t, `
var s = "";
for (var c of "abc") { s += c; }
s`)
	if got != "abc" {
		t.Errorf("for-of string = %q, want abc", got)
	}
}

func TestVMCustomIterator(t *testing.T) {
	got := vmEvalStr(t, `
var obj = {
  [Symbol.iterator]: function*() {
    yield 1;
    yield 2;
  }
};
var s = 0;
for (var x of obj) { s += x; }
s`)
	if got != "3" {
		t.Errorf("custom iterator = %q, want 3", got)
	}
}

func TestVMSpreadWithIterator(t *testing.T) {
	got := vmEvalStr(t, `
function* gen() { yield 1; yield 2; yield 3; }
var arr = [...gen()];
arr[0] + arr[1] + arr[2]`)
	if got != "6" {
		t.Errorf("spread with iterator = %q, want 6", got)
	}
}

func TestVMGeneratorClosureCapture(t *testing.T) {
	got := vmEvalStr(t, `
function makeGen() {
  var count = 0;
  return function*() {
    while (true) {
      count += 1;
      yield count;
    }
  };
}
var g = makeGen()();
var r = g.next().value + "," + g.next().value + "," + g.next().value;
r`)
	if got != "1,2,3" {
		t.Errorf("generator closure capture = %q, want 1,2,3", got)
	}
}

func TestVMGeneratorNestedForOf(t *testing.T) {
	got := vmEvalStr(t, `
function* pairs(n) {
  for (var i of [1, 2, 3]) {
    for (var j of [10, 20]) {
      yield i * 100 + j;
    }
  }
}
var s = "";
for (var x of pairs(0)) { s += x + ","; }
s`)
	want := "110,120,210,220,310,320,"
	if got != want {
		t.Errorf("nested for-of generator = %q, want %q", got, want)
	}
}

// === Promise + microtask tests =============================================
//
// Promise reactions run as microtasks, which are drained after the top-level
// code returns. Tests use globalThis.__r to capture results from callbacks.

// vmEvalPromise evaluates code and returns the string value of globalThis.__r.
func vmEvalPromise(t *testing.T, code string) string {
	t.Helper()
	vm, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	_, err = vm.Eval(code, "test.js")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	val, _ := vm.Global().Get("__r")
	return val.String()
}

func TestVMPromiseBasicResolve(t *testing.T) {
	got := vmEvalPromise(t, `
new Promise(function(resolve) { resolve(42) }).then(function(v) { globalThis.__r = v });
`)
	if got != "42" {
		t.Errorf("basic resolve = %q, want %q", got, "42")
	}
}

func TestVMPromiseBasicReject(t *testing.T) {
	got := vmEvalPromise(t, `
new Promise(function(_, reject) { reject("error") }).catch(function(e) { globalThis.__r = e });
`)
	if got != "error" {
		t.Errorf("basic reject = %q, want %q", got, "error")
	}
}

func TestVMPromiseChaining(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(1).then(function(v) { return v + 1 }).then(function(v) { return v + 1 }).then(function(v) { globalThis.__r = v });
`)
	if got != "3" {
		t.Errorf("chaining = %q, want %q", got, "3")
	}
}

func TestVMPromiseResolveStatic(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(42).then(function(v) { globalThis.__r = v });
`)
	if got != "42" {
		t.Errorf("Promise.resolve = %q, want %q", got, "42")
	}
}

func TestVMPromiseRejectStatic(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.reject("err").catch(function(e) { globalThis.__r = e });
`)
	if got != "err" {
		t.Errorf("Promise.reject = %q, want %q", got, "err")
	}
}

func TestVMPromiseAll(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.all([1, 2, 3]).then(function(arr) { globalThis.__r = arr.join(",") });
`)
	if got != "1,2,3" {
		t.Errorf("Promise.all = %q, want %q", got, "1,2,3")
	}
}

func TestVMPromiseAllWithPromises(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.all([Promise.resolve(10), Promise.resolve(20)]).then(function(arr) { globalThis.__r = arr.join(",") });
`)
	if got != "10,20" {
		t.Errorf("Promise.all with promises = %q, want %q", got, "10,20")
	}
}

func TestVMPromiseAllReject(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.all([Promise.resolve(1), Promise.reject("fail")]).catch(function(e) { globalThis.__r = e });
`)
	if got != "fail" {
		t.Errorf("Promise.all reject = %q, want %q", got, "fail")
	}
}

func TestVMPromiseAllEmpty(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.all([]).then(function(arr) { globalThis.__r = arr.length });
`)
	if got != "0" {
		t.Errorf("Promise.all empty = %q, want %q", got, "0")
	}
}

func TestVMPromiseRace(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.race([Promise.resolve("a"), Promise.resolve("b")]).then(function(v) { globalThis.__r = v });
`)
	if got != "a" {
		t.Errorf("Promise.race = %q, want %q", got, "a")
	}
}

func TestVMPromiseAllSettled(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.allSettled([Promise.resolve(1), Promise.reject("e")]).then(function(arr) {
  globalThis.__r = arr[0].status + "," + arr[1].status;
});
`)
	if got != "fulfilled,rejected" {
		t.Errorf("Promise.allSettled = %q, want %q", got, "fulfilled,rejected")
	}
}

func TestVMPromiseFinally(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(42).finally(function() { }).then(function(v) { globalThis.__r = v });
`)
	if got != "42" {
		t.Errorf("finally passthrough = %q, want %q", got, "42")
	}
}

func TestVMPromiseFinallyRejectPassthrough(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.reject("err").finally(function() { }).catch(function(e) { globalThis.__r = e });
`)
	if got != "err" {
		t.Errorf("finally reject passthrough = %q, want %q", got, "err")
	}
}

func TestVMPromiseFinallyThrowOverrides(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(42).finally(function() { throw "override" }).catch(function(e) { globalThis.__r = e });
`)
	if got != "override" {
		t.Errorf("finally throw overrides = %q, want %q", got, "override")
	}
}

func TestVMPromiseThenReturnsPromise(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(1).then(function(v) { return Promise.resolve(v + 1) }).then(function(v) { globalThis.__r = v });
`)
	if got != "2" {
		t.Errorf("then returns promise = %q, want %q", got, "2")
	}
}

func TestVMPromiseThenPassthrough(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(42).then().then(function(v) { globalThis.__r = v });
`)
	if got != "42" {
		t.Errorf("then passthrough = %q, want %q", got, "42")
	}
}

func TestVMPromiseCatchPassthrough(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.reject("err").catch().catch(function(e) { globalThis.__r = e });
`)
	if got != "err" {
		t.Errorf("catch passthrough = %q, want %q", got, "err")
	}
}

func TestVMPromiseExecutorThrows(t *testing.T) {
	got := vmEvalPromise(t, `
new Promise(function() { throw "exec error" }).catch(function(e) { globalThis.__r = e });
`)
	if got != "exec error" {
		t.Errorf("executor throws = %q, want %q", got, "exec error")
	}
}

func TestVMPromiseThenHandlerThrows(t *testing.T) {
	got := vmEvalPromise(t, `
Promise.resolve(1).then(function() { throw "handler error" }).catch(function(e) { globalThis.__r = e });
`)
	if got != "handler error" {
		t.Errorf("then handler throws = %q, want %q", got, "handler error")
	}
}

func TestVMPromiseAdoption(t *testing.T) {
	got := vmEvalPromise(t, `
var outer = new Promise(function(resolve) { resolve(42) });
new Promise(function(resolve) { resolve(outer) }).then(function(v) { globalThis.__r = v });
`)
	if got != "42" {
		t.Errorf("promise adoption = %q, want %q", got, "42")
	}
}

func TestVMPromiseMicrotaskOrdering(t *testing.T) {
	got := vmEvalPromise(t, `
var log = [];
Promise.resolve(1).then(function() { log.push("a") });
Promise.resolve(2).then(function() { log.push("b") });
queueMicrotask(function() { globalThis.__r = log.join(",") });
`)
	if got != "a,b" {
		t.Errorf("microtask ordering = %q, want %q", got, "a,b")
	}
}

func TestVMPromiseQueueMicrotask(t *testing.T) {
	got := vmEvalPromise(t, `
globalThis.__r = "before";
queueMicrotask(function() { globalThis.__r = "after" });
`)
	if got != "after" {
		t.Errorf("queueMicrotask = %q, want %q", got, "after")
	}
}

func TestVMPromiseSharedUpvalue(t *testing.T) {
	// Tests that closures capturing the same local variable share state
	// after the enclosing frame exits (upvalue sharing fix).
	got := vmEvalPromise(t, `
var r;
Promise.resolve(42).then(function(v) { r = v });
queueMicrotask(function() { globalThis.__r = r });
`)
	if got != "42" {
		t.Errorf("shared upvalue = %q, want %q", got, "42")
	}
}

func TestVMPromiseConstructorProperty(t *testing.T) {
	got := vmEvalStr(t, `typeof Promise`)
	if got != "function" {
		t.Errorf("typeof Promise = %q, want %q", got, "function")
	}
	got = vmEvalStr(t, `typeof queueMicrotask`)
	if got != "function" {
		t.Errorf("typeof queueMicrotask = %q, want %q", got, "function")
	}
	got = vmEvalStr(t, `Promise.name`)
	if got != "Promise" {
		t.Errorf("Promise.name = %q, want %q", got, "Promise")
	}
}

func TestVMPromiseInstanceof(t *testing.T) {
	got := vmEvalStr(t, `Promise.resolve(1) instanceof Promise`)
	if got != "true" {
		t.Errorf("instanceof Promise = %q, want %q", got, "true")
	}
}

// === Symbol enhancements (1C.5) ===========================================

func TestVMSymbolFor(t *testing.T) {
	got := vmEvalStr(t, `Symbol.for("foo") === Symbol.for("foo")`)
	if got != "true" {
		t.Errorf("Symbol.for identity = %q, want true", got)
	}
}

func TestVMSymbolKeyFor(t *testing.T) {
	got := vmEvalStr(t, `Symbol.keyFor(Symbol.for("bar"))`)
	if got != "bar" {
		t.Errorf("Symbol.keyFor = %q, want bar", got)
	}
}

func TestVMSymbolKeyForUnregistered(t *testing.T) {
	got := vmEvalStr(t, `Symbol.keyFor(Symbol("baz"))`)
	if got != "undefined" {
		t.Errorf("Symbol.keyFor unregistered = %q, want undefined", got)
	}
}

func TestVMSymbolUnique(t *testing.T) {
	got := vmEvalStr(t, `Symbol("x") === Symbol("x")`)
	if got != "false" {
		t.Errorf("Symbol uniqueness = %q, want false", got)
	}
}

func TestVMSymbolWellKnown(t *testing.T) {
	got := vmEvalStr(t, `Symbol.iterator === Symbol.iterator`)
	if got != "true" {
		t.Errorf("Symbol.iterator identity = %q, want true", got)
	}
	got = vmEvalStr(t, `typeof Symbol.hasInstance`)
	if got != "symbol" {
		t.Errorf("typeof Symbol.hasInstance = %q, want symbol", got)
	}
}

// === Map (1C.5) ===========================================================

func TestVMMapBasic(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map();
m.set("a", 1);
m.set("b", 2);
m.get("a") + m.get("b")`)
	if got != "3" {
		t.Errorf("Map basic = %q, want 3", got)
	}
}

func TestVMMapSize(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map();
m.set("a", 1); m.set("b", 2); m.set("c", 3);
m.size`)
	if got != "3" {
		t.Errorf("Map size = %q, want 3", got)
	}
}

func TestVMMapHas(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map(); m.set("x", 42);
m.has("x") + "," + m.has("y")`)
	if got != "true,false" {
		t.Errorf("Map has = %q, want true,false", got)
	}
}

func TestVMMapDelete(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map(); m.set("x", 1);
m.delete("x") + "," + m.has("x") + "," + m.size`)
	if got != "true,false,0" {
		t.Errorf("Map delete = %q, want true,false,0", got)
	}
}

func TestVMMapClear(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map(); m.set("a", 1); m.set("b", 2);
m.clear(); m.size`)
	if got != "0" {
		t.Errorf("Map clear = %q, want 0", got)
	}
}

func TestVMMapConstructorIterable(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map([["a", 1], ["b", 2]]);
m.get("a") + m.get("b")`)
	if got != "3" {
		t.Errorf("Map from iterable = %q, want 3", got)
	}
}

func TestVMMapObjectKeys(t *testing.T) {
	got := vmEvalStr(t, `
var k1 = {}, k2 = {};
var m = new Map();
m.set(k1, "v1"); m.set(k2, "v2");
m.get(k1) + "," + m.get(k2)`)
	if got != "v1,v2" {
		t.Errorf("Map object keys = %q, want v1,v2", got)
	}
}

func TestVMMapForEach(t *testing.T) {
	got := vmEvalStr(t, `
var sum = 0;
var m = new Map([["a", 1], ["b", 2], ["c", 3]]);
m.forEach(function(v) { sum += v; });
sum`)
	if got != "6" {
		t.Errorf("Map forEach = %q, want 6", got)
	}
}

func TestVMMapForOf(t *testing.T) {
	got := vmEvalStr(t, `
var pairs = [];
var m = new Map([["a", 1], ["b", 2]]);
for (var entry of m) { pairs.push(entry[0] + "=" + entry[1]); }
pairs.join(",")`)
	if got != "a=1,b=2" {
		t.Errorf("Map for-of = %q, want a=1,b=2", got)
	}
}

func TestVMMapKeys(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map([["a", 1], ["b", 2]]);
var ks = [];
for (var k of m.keys()) { ks.push(k); }
ks.join(",")`)
	if got != "a,b" {
		t.Errorf("Map keys = %q, want a,b", got)
	}
}

func TestVMMapValues(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map([["a", 1], ["b", 2]]);
var vs = [];
for (var v of m.values()) { vs.push(v); }
vs.join(",")`)
	if got != "1,2" {
		t.Errorf("Map values = %q, want 1,2", got)
	}
}

func TestVMMapEntries(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map([["a", 1], ["b", 2]]);
var r = [];
for (var e of m.entries()) { r.push(e[0] + ":" + e[1]); }
r.join(",")`)
	if got != "a:1,b:2" {
		t.Errorf("Map entries = %q, want a:1,b:2", got)
	}
}

func TestVMMapInstanceof(t *testing.T) {
	got := vmEvalStr(t, `new Map() instanceof Map`)
	if got != "true" {
		t.Errorf("Map instanceof = %q, want true", got)
	}
}

func TestVMMapSetReturnsMap(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map();
(m.set("a", 1) === m) + ""`)
	if got != "true" {
		t.Errorf("Map.set returns map = %q, want true", got)
	}
}

func TestVMMapOverwrite(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map(); m.set("k", 1); m.set("k", 2);
m.get("k") + "," + m.size`)
	if got != "2,1" {
		t.Errorf("Map overwrite = %q, want 2,1", got)
	}
}

func TestVMMapSpread(t *testing.T) {
	got := vmEvalStr(t, `
var m = new Map([["a", 1], ["b", 2]]);
var arr = [...m];
arr.length + "," + arr[0][0] + arr[0][1]`)
	if got != "2,a1" {
		t.Errorf("Map spread = %q, want 2,a1", got)
	}
}

// === Set (1C.5) ===========================================================

func TestVMSetBasic(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set();
s.add(1); s.add(2); s.add(3);
s.size`)
	if got != "3" {
		t.Errorf("Set basic size = %q, want 3", got)
	}
}

func TestVMSetHas(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set(); s.add("x");
s.has("x") + "," + s.has("y")`)
	if got != "true,false" {
		t.Errorf("Set has = %q, want true,false", got)
	}
}

func TestVMSetUnique(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set();
s.add(1); s.add(1); s.add(2);
s.size`)
	if got != "2" {
		t.Errorf("Set uniqueness = %q, want 2", got)
	}
}

func TestVMSetDelete(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set(); s.add(1); s.add(2);
s.delete(1) + "," + s.has(1) + "," + s.size`)
	if got != "true,false,1" {
		t.Errorf("Set delete = %q, want true,false,1", got)
	}
}

func TestVMSetClear(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set(); s.add(1); s.add(2);
s.clear(); s.size`)
	if got != "0" {
		t.Errorf("Set clear = %q, want 0", got)
	}
}

func TestVMSetConstructorIterable(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set([1, 2, 3, 2, 1]);
s.size`)
	if got != "3" {
		t.Errorf("Set from iterable = %q, want 3", got)
	}
}

func TestVMSetForEach(t *testing.T) {
	got := vmEvalStr(t, `
var sum = 0;
var s = new Set([1, 2, 3]);
s.forEach(function(v) { sum += v; });
sum`)
	if got != "6" {
		t.Errorf("Set forEach = %q, want 6", got)
	}
}

func TestVMSetForOf(t *testing.T) {
	got := vmEvalStr(t, `
var r = [];
var s = new Set([10, 20, 30]);
for (var v of s) { r.push(v); }
r.join(",")`)
	if got != "10,20,30" {
		t.Errorf("Set for-of = %q, want 10,20,30", got)
	}
}

func TestVMSetEntries(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set([1, 2]);
var r = [];
for (var e of s.entries()) { r.push(e[0] + "=" + e[1]); }
r.join(",")`)
	if got != "1=1,2=2" {
		t.Errorf("Set entries = %q, want 1=1,2=2", got)
	}
}

func TestVMSetInstanceof(t *testing.T) {
	got := vmEvalStr(t, `new Set() instanceof Set`)
	if got != "true" {
		t.Errorf("Set instanceof = %q, want true", got)
	}
}

func TestVMSetAddReturnsSet(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set();
(s.add(1) === s) + ""`)
	if got != "true" {
		t.Errorf("Set.add returns set = %q, want true", got)
	}
}

func TestVMSetSpread(t *testing.T) {
	got := vmEvalStr(t, `
var s = new Set([1, 2, 3]);
var arr = [...s];
arr.join(",")`)
	if got != "1,2,3" {
		t.Errorf("Set spread = %q, want 1,2,3", got)
	}
}

// === WeakMap (1C.5) =======================================================

func TestVMWeakMapBasic(t *testing.T) {
	got := vmEvalStr(t, `
var k = {};
var wm = new WeakMap();
wm.set(k, "value");
wm.get(k)`)
	if got != "value" {
		t.Errorf("WeakMap basic = %q, want value", got)
	}
}

func TestVMWeakMapHas(t *testing.T) {
	got := vmEvalStr(t, `
var k = {};
var wm = new WeakMap();
wm.set(k, 1);
wm.has(k) + "," + wm.has({})`)
	if got != "true,false" {
		t.Errorf("WeakMap has = %q, want true,false", got)
	}
}

func TestVMWeakMapDelete(t *testing.T) {
	got := vmEvalStr(t, `
var k = {};
var wm = new WeakMap();
wm.set(k, 1);
wm.delete(k) + "," + wm.has(k)`)
	if got != "true,false" {
		t.Errorf("WeakMap delete = %q, want true,false", got)
	}
}

func TestVMWeakMapInstanceof(t *testing.T) {
	got := vmEvalStr(t, `new WeakMap() instanceof WeakMap`)
	if got != "true" {
		t.Errorf("WeakMap instanceof = %q, want true", got)
	}
}

func TestVMWeakMapGetMissing(t *testing.T) {
	got := vmEvalStr(t, `
var wm = new WeakMap();
wm.get({})`)
	if got != "undefined" {
		t.Errorf("WeakMap get missing = %q, want undefined", got)
	}
}

// === WeakSet (1C.5) =======================================================

func TestVMWeakSetBasic(t *testing.T) {
	got := vmEvalStr(t, `
var o = {};
var ws = new WeakSet();
ws.add(o);
ws.has(o) + ""`)
	if got != "true" {
		t.Errorf("WeakSet basic = %q, want true", got)
	}
}

func TestVMWeakSetDelete(t *testing.T) {
	got := vmEvalStr(t, `
var o = {};
var ws = new WeakSet();
ws.add(o);
ws.delete(o) + "," + ws.has(o)`)
	if got != "true,false" {
		t.Errorf("WeakSet delete = %q, want true,false", got)
	}
}

func TestVMWeakSetInstanceof(t *testing.T) {
	got := vmEvalStr(t, `new WeakSet() instanceof WeakSet`)
	if got != "true" {
		t.Errorf("WeakSet instanceof = %q, want true", got)
	}
}

func TestVMWeakSetHasMissing(t *testing.T) {
	got := vmEvalStr(t, `
var ws = new WeakSet();
ws.has({}) + ""`)
	if got != "false" {
		t.Errorf("WeakSet has missing = %q, want false", got)
	}
}

// === Proxy ===============================================================

func TestVMProxyGetTrap(t *testing.T) {
	got := vmEvalStr(t, `
var obj = {a: 1};
var p = new Proxy(obj, {
  get: function(target, key) { return key.toUpperCase(); }
});
p.foo`)
	if got != "FOO" {
		t.Errorf("Proxy get trap = %q, want FOO", got)
	}
}

func TestVMProxyGetForward(t *testing.T) {
	got := vmEvalStr(t, `
var obj = {a: 1, b: 2};
var p = new Proxy(obj, {});
p.a + p.b`)
	if got != "3" {
		t.Errorf("Proxy forward get = %q, want 3", got)
	}
}

func TestVMProxySetTrap(t *testing.T) {
	got := vmEvalStr(t, `
var log = [];
var target = {};
var p = new Proxy(target, {
  set: function(obj, key, val) { log.push(key + "=" + val); obj[key] = val * 10; return true; }
});
p.x = 5;
log.join(",") + "|" + p.x`)
	if got != "x=5|50" {
		t.Errorf("Proxy set trap = %q, want x=5|50", got)
	}
}

func TestVMProxySetForward(t *testing.T) {
	got := vmEvalStr(t, `
var target = {};
var p = new Proxy(target, {});
p.x = 42;
target.x`)
	if got != "42" {
		t.Errorf("Proxy forward set = %q, want 42", got)
	}
}

func TestVMProxyHasTrap(t *testing.T) {
	got := vmEvalStr(t, `
var target = {a: 1};
var p = new Proxy(target, {
  has: function(t, key) { return key === "a"; }
});
("a" in p) + "," + ("b" in p)`)
	if got != "true,false" {
		t.Errorf("Proxy has trap = %q, want true,false", got)
	}
}

func TestVMProxyDeleteTrap(t *testing.T) {
	got := vmEvalStr(t, `
var deleted = [];
var target = {a: 1, b: 2};
var p = new Proxy(target, {
  deleteProperty: function(t, key) { deleted.push(key); return true; }
});
delete p.a;
deleted.join(",")`)
	if got != "a" {
		t.Errorf("Proxy delete trap = %q, want a", got)
	}
}

func TestVMProxyOwnKeysTrap(t *testing.T) {
	got := vmEvalStr(t, `
var target = {a: 1, b: 2};
var p = new Proxy(target, {
  ownKeys: function(t) { return ["a", "b", "c"]; }
});
Reflect.ownKeys(p).join(",")`)
	if got != "a,b,c" {
		t.Errorf("Proxy ownKeys trap = %q, want a,b,c", got)
	}
}

func TestVMProxyGetPrototypeOfTrap(t *testing.T) {
	got := vmEvalStr(t, `
var fakeProto = {tag: "fake"};
var p = new Proxy({}, {
  getPrototypeOf: function(t) { return fakeProto; }
});
Object.getPrototypeOf(p).tag`)
	if got != "fake" {
		t.Errorf("Proxy getPrototypeOf trap = %q, want fake", got)
	}
}

func TestVMProxyRevocable(t *testing.T) {
	got := vmEvalStr(t, `
var obj = {a: 1};
var rev = Proxy.revocable(obj, {
  get: function(t, k) { return t[k] + 100; }
});
rev.proxy.a + "|" + (typeof rev.revoke)`)
	if got != "101|function" {
		t.Errorf("Proxy.revocable = %q, want 101|function", got)
	}
}

func TestVMProxyMethodCall(t *testing.T) {
	got := vmEvalStr(t, `
var target = { greet: function() { return "hello"; } };
var p = new Proxy(target, {
  get: function(t, k) { if (k === "greet") return function() { return "proxied"; }; return t[k]; }
});
p.greet()`)
	if got != "proxied" {
		t.Errorf("Proxy method call = %q, want proxied", got)
	}
}

func TestVMProxySpread(t *testing.T) {
	got := vmEvalStr(t, `
var target = {a: 1, b: 2};
var p = new Proxy(target, {
  ownKeys: function() { return ["a", "b"]; },
  get: function(t, k) { return t[k] !== undefined ? t[k] * 10 : undefined; }
});
var copy = {...p};
copy.a + copy.b`)
	if got != "30" {
		t.Errorf("Proxy spread = %q, want 30", got)
	}
}

func TestVMProxyInstanceofHasInstance(t *testing.T) {
	got := vmEvalStr(t, `
var ctor = function() {};
var p = new Proxy(ctor, {
  get: function(t, k) {
    if (k === Symbol.hasInstance) return function(v) { return v === 42; };
    return t[k];
  }
});
(42 instanceof p) + "," + (99 instanceof p)`)
	if got != "true,false" {
		t.Errorf("Proxy instanceof = %q, want true,false", got)
	}
}

// === Reflect =============================================================

func TestVMReflectGet(t *testing.T) {
	got := vmEvalStr(t, `Reflect.get({a: 1}, "a")`)
	if got != "1" {
		t.Errorf("Reflect.get = %q, want 1", got)
	}
}

func TestVMReflectSet(t *testing.T) {
	got := vmEvalStr(t, `
var o = {};
Reflect.set(o, "x", 5);
o.x`)
	if got != "5" {
		t.Errorf("Reflect.set = %q, want 5", got)
	}
}

func TestVMReflectHas(t *testing.T) {
	got := vmEvalStr(t, `Reflect.has({a: 1}, "a") + "," + Reflect.has({a: 1}, "b")`)
	if got != "true,false" {
		t.Errorf("Reflect.has = %q, want true,false", got)
	}
}

func TestVMReflectDeleteProperty(t *testing.T) {
	got := vmEvalStr(t, `
var o = {a: 1};
Reflect.deleteProperty(o, "a");
("a" in o) + ""`)
	if got != "false" {
		t.Errorf("Reflect.deleteProperty = %q, want false", got)
	}
}

func TestVMReflectOwnKeys(t *testing.T) {
	got := vmEvalStr(t, `Reflect.ownKeys({a: 1, b: 2}).join(",")`)
	if got != "a,b" {
		t.Errorf("Reflect.ownKeys = %q, want a,b", got)
	}
}

func TestVMReflectGetPrototypeOf(t *testing.T) {
	got := vmEvalStr(t, `
var proto = {tag: "p"};
var o = {};
Reflect.setPrototypeOf(o, proto);
Reflect.getPrototypeOf(o).tag`)
	if got != "p" {
		t.Errorf("Reflect.getPrototypeOf = %q, want p", got)
	}
}

func TestVMReflectApply(t *testing.T) {
	got := vmEvalStr(t, `
function sum(a, b) { return a + b; }
Reflect.apply(sum, null, [3, 4])`)
	if got != "7" {
		t.Errorf("Reflect.apply = %q, want 7", got)
	}
}

func TestVMReflectConstruct(t *testing.T) {
	got := vmEvalStr(t, `
function Box(v) { this.v = v; }
Reflect.construct(Box, [99]).v`)
	if got != "99" {
		t.Errorf("Reflect.construct = %q, want 99", got)
	}
}

func TestVMReflectDefineProperty(t *testing.T) {
	got := vmEvalStr(t, `
var o = {};
Reflect.defineProperty(o, "x", {value: 42});
o.x`)
	if got != "42" {
		t.Errorf("Reflect.defineProperty = %q, want 42", got)
	}
}

func TestVMReflectGetOwnPropertyDescriptor(t *testing.T) {
	got := vmEvalStr(t, `
var o = {x: 7};
var d = Reflect.getOwnPropertyDescriptor(o, "x");
d.value + "," + d.writable`)
	if got != "7,true" {
		t.Errorf("Reflect.getOwnPropertyDescriptor = %q, want 7,true", got)
	}
}

func TestVMReflectWithProxy(t *testing.T) {
	got := vmEvalStr(t, `
var p = new Proxy({a: 1}, {
  get: function(t, k) { return "got:" + k; }
});
Reflect.get(p, "test")`)
	if got != "got:test" {
		t.Errorf("Reflect.get with Proxy = %q, want got:test", got)
	}
}

func TestVMReflectHasWithProxy(t *testing.T) {
	got := vmEvalStr(t, `
var p = new Proxy({}, {
  has: function(t, k) { return k === "magic"; }
});
Reflect.has(p, "magic") + "," + Reflect.has(p, "other")`)
	if got != "true,false" {
		t.Errorf("Reflect.has with Proxy = %q, want true,false", got)
	}
}

// === Optional chaining (?.) ===============================================

func TestVMOptionalChainingMember(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// null/undefined head → undefined (no throw)
		{`null?.x`, "undefined"},
		{`undefined?.x`, "undefined"},
		// object head → property value
		{`({x: 1})?.x`, "1"},
		{`({x: 5})?.x + 1`, "6"},
		// missing property on non-null → undefined
		{`({})?.x`, "undefined"},
		// chained head not null continues normally
		{`var a = {b: {c: 7}}; a?.b?.c`, "7"},
		{`var a = {b: {c: 7}}; a?.b.c`, "7"},
		// short-circuit in the middle
		{`var a = {b: null}; a?.b?.c`, "undefined"},
		{`var a = null; a?.b?.c?.d`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMOptionalChainingComputed(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`null?.[0]`, "undefined"},
		{`undefined?.["x"]`, "undefined"},
		{`([10, 20, 30])?.[1]`, "20"},
		{`var a = {k: 42}; a?.["k"]`, "42"},
		{`null?.[0]?.[1]`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMOptionalChainingCall(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// optional call on null/undefined callee
		{`null?.()`, "undefined"},
		{`undefined?.()`, "undefined"},
		// optional call on a function
		{`(function() { return 42 })?.()`, "42"},
		{`var f = function(a, b) { return a + b }; f?.(2, 3)`, "5"},
		// callee exists but is not called when nullish
		{`var n = null; n?.(999)`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMOptionalChainingMethodCall(t *testing.T) {
	// a?.b() — member access optional, call not optional
	got := vmEvalStr(t, `var o = {greet: function() { return "hi" }}; o?.greet()`)
	if got != "hi" {
		t.Errorf("o?.greet() = %q, want hi", got)
	}
	got = vmEvalStr(t, `var o = null; o?.greet()`)
	if got != "undefined" {
		t.Errorf("null?.greet() = %q, want undefined", got)
	}

	// a.b?.() — method itself optional (nullish method not called)
	got = vmEvalStr(t, `var o = {greet: null}; o.greet?.()`)
	if got != "undefined" {
		t.Errorf("o.greet?.() with null method = %q, want undefined", got)
	}
	got = vmEvalStr(t, `var o = {greet: function() { return "hello" }}; o.greet?.()`)
	if got != "hello" {
		t.Errorf("o.greet?.() with method = %q, want hello", got)
	}

	// a?.b?.() — both optional
	got = vmEvalStr(t, `var o = null; o?.greet?.()`)
	if got != "undefined" {
		t.Errorf("null?.greet?.() = %q, want undefined", got)
	}
	got = vmEvalStr(t, `var o = {greet: function() { return "both" }}; o?.greet?.()`)
	if got != "both" {
		t.Errorf("o?.greet?.() = %q, want both", got)
	}
	got = vmEvalStr(t, `var o = {greet: null}; o?.greet?.()`)
	if got != "undefined" {
		t.Errorf("o?.greet?.() with null method = %q, want undefined", got)
	}
}

// TestVMOptionalChainingThisBinding verifies `this` is correctly bound to the
// receiver when calling a method through an optional chain.
func TestVMOptionalChainingThisBinding(t *testing.T) {
	got := vmEvalStr(t, `
var o = {
  name: "obj",
  whoami: function() { return this.name; }
};
o?.whoami()`)
	if got != "obj" {
		t.Errorf("o?.whoami() this binding = %q, want obj", got)
	}

	got = vmEvalStr(t, `
var o = {
  name: "obj2",
  whoami: function() { return this.name; }
};
o.whoami?.()`)
	if got != "obj2" {
		t.Errorf("o.whoami?.() this binding = %q, want obj2", got)
	}

	got = vmEvalStr(t, `
var o = {
  name: "obj3",
  whoami: function() { return this.name; }
};
o?.whoami?.()`)
	if got != "obj3" {
		t.Errorf("o?.whoami?.() this binding = %q, want obj3", got)
	}
}

func TestVMOptionalChainingDeepShortCircuit(t *testing.T) {
	// Short-circuit at different points of a deep chain.
	cases := []struct {
		code string
		want string
	}{
		// head null
		{`var a = null; a?.b.c.d.e`, "undefined"},
		// middle null
		{`var a = {b: null}; a?.b?.c?.d`, "undefined"},
		{`var a = {b: {c: null}}; a?.b?.c?.d`, "undefined"},
		// all present
		{`var a = {b: {c: {d: {e: "deep"}}}}; a?.b?.c?.d?.e`, "deep"},
		// continuation after optional (non-optional) is skipped too
		{`var a = null; a?.b.c`, "undefined"},
		// optional call in the middle of a chain (?.() makes the call optional too)
		{`var a = null; a?.m?.()?.x`, "undefined"},
		{`var a = {m: function() { return {x: 9} }}; a?.m?.()?.x`, "9"},
		{`var a = {m: null}; a?.m?.()?.x`, "undefined"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMOptionalChainingWithNullishCoalescing(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`null?.x ?? "default"`, "default"},
		{`undefined?.y ?? 42`, "42"},
		{`({x: 1})?.x ?? 99`, "1"},
		{`null?.m?.() ?? "fallback"`, "fallback"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("VM.Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMOptionalChainingWithArguments(t *testing.T) {
	// Optional method call with arguments + this binding
	got := vmEvalStr(t, `
var calc = {
  base: 100,
  add: function(x, y) { return this.base + x + y; }
};
calc?.add?.(1, 2)`)
	if got != "103" {
		t.Errorf("optional method with args = %q, want 103", got)
	}

	// Spread args in optional call
	got = vmEvalStr(t, `
var sum = function(...nums) {
  var s = 0;
  for (var i = 0; i < nums.length; i++) s += nums[i];
  return s;
};
var args = [1, 2, 3];
sum?.(...args)`)
	if got != "6" {
		t.Errorf("optional call with spread = %q, want 6", got)
	}
}

func TestVMOptionalChainingNoShortCircuit(t *testing.T) {
	// When the head is present, the chain must NOT short-circuit and must
	// actually access the property (which may throw if it doesn't exist on
	// a non-nullish intermediate that is not an object). Here we just verify
	// normal property access works end-to-end.
	got := vmEvalStr(t, `
var calls = 0;
var obj = {
  a: { b: { c: function() { calls++; return calls; } } }
};
obj?.a?.b?.c()`)
	if got != "1" {
		t.Errorf("chain with call = %q, want 1", got)
	}
}

// TestArrayThisArg：Array.prototype 方法 thisArg 对非箭头函数生效（N22-A2）。
func TestArrayThisArg(t *testing.T) {
	got := vmEvalPromise(t, `
var o = { m: 2 };
globalThis.__r = [
  [1, 2].map(function (x) { return x * this.m; }, o).join(','),
  [1, 2, 3].find(function (x) { return x > this.m; }, o),
  [1, 2, 3].reduce(function (a, x) { return a + x; }, 0).toString(),
].join('|');
`)
	if got != "2,4|3|6" {
		t.Errorf("array thisArg = %q, want 2,4|3|6", got)
	}
}
