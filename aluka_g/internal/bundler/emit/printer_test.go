package emit

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// 语法矩阵：覆盖 printer 支持的全部语法形态。
var corpus = []string{
	// 字面量
	`var x = 1;`,
	`var x = 1.5e10;`,
	`var x = 0xff;`,
	`var x = 123n;`,
	`var s = "hello\nworld";`,
	`var b = true, n = null, u = undefined;`,
	`var r = /ab+c/gi;`,
	"var t = `a${1 + 2}b\\`c`;",
	`var arr = [1, 2, , 4, ...rest];`,
	// 对象
	`var o = {a: 1, "b c": 2, 3: 4, [k]: 5, m(){return 1}, get g(){return 2}, set s(v){}, ...spread};`,
	// 运算符与优先级
	`var y = (a + b) * (c - -d) ** e / f % g;`,
	`var y = a || b && c ?? d | e ^ f & g;`,
	`var y = a << b >> c >>> d;`,
	`var y = a < b <= c > d >= e in f instanceof g;`,
	`var y = !~+(-x) + typeof y + void z + delete o.p;`,
	`var y = (a, b, c);`,
	`var y = a ? b : c ? d : e;`,
	`y += 1; y -= 1; y **= 2; y ??= q;`,
	`a++; ++a; a--; --a;`,
	// 控制流
	`if (a) { f() } else if (b) g(); else { h() }`,
	`while (a) { b() }`,
	`do { body() } while (cond);`,
	`for (var i = 0; i < 10; i++) { work(i) }`,
	`for (var k in o) { use(k) }`,
	`for (const [a, b = 2, ...r] of list) { use(a) }`,
	`for (const {a, b: c, ...rest} of items) {}`,
	`async function run() { for await (const x of gen()) {} }`,
	`switch (x) { case 1: a(); break; case 2: b(); default: c() }`,
	`try { risky() } catch (e) { handle(e) } finally { cleanup() }`,
	`try { a() } catch { b() }`,
	`outer: for (;;) { break outer; }`,
	`inner: while (1) { continue inner; }`,
	`function f() { return; }`,
	`function g() { return 42 }`,
	`throw new Error("boom");`,
	`;`,
	// 函数
	`function f(a, b = 2, {c, d: e = 3}, [g, ...h], ...rest) { return a }`,
	`async function* gen() { yield 1; yield* it; await x; }`,
	`var arrow1 = (a) => a * 2;`,
	`var arrow2 = async (a, b) => ({a, b});`,
	`var arrow3 = () => { return 1 };`,
	`var fe = function named(n) { return n };`,
	// 类
	`class A extends B { constructor(a) { super(a); this.x = a } static s = 1; f = 2;
		get p() { return this.x } set p(v) { this.x = v }
		static m() { return A.s } async am() { await 1 } *g() { yield 1 } static { A.s = 9 } }`,
	`var CE = class Named extends Base {};`,
	// 解构
	`var [a, , b = 1, ...r] = arr;`,
	`var {p, q: renamed, s = 3, ...t} = obj;`,
	`({a} = src);`,
	`({a: x.y} = src);`,
	// 成员/调用
	`a?.b?.[c]?.(d);`,
	`new C();`,
	`new (f())(1, 2);`,
	`obj.method(arg).chained?.opt();`,
	// 其他
	`var nt = new.target;`,
	`function sup() { super.m(); }`,
	`label: { break label }`,
}

// TestPrintIdempotent：parse → print → parse → print，两次输出必须逐字节一致。
// 打印丢失任何信息都会在第二轮输出中显现（语法错误或文本漂移）。
func TestPrintIdempotent(t *testing.T) {
	for i, src := range corpus {
		out1, err := roundOnce(src)
		if err != nil {
			t.Errorf("case %d: first print: %v\nsrc: %s", i, err, src)
			continue
		}
		out2, err := roundOnce(out1)
		if err != nil {
			t.Errorf("case %d: re-parse of printed output failed: %v\nprinted: %s", i, err, out1)
			continue
		}
		if out1 != out2 {
			t.Errorf("case %d: print not idempotent:\n 1st: %s\n 2nd: %s", i, out1, out2)
		}
	}
}

func roundOnce(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	return Print(prog), nil
}

// 语义可执行语料：print 后必须能跑出与源码相同的结果。
var semanticCorpus = []string{
	`console.log((1 + 2) * 3 ** 2 / 2 % 7);`,
	`var a = 5, b = 3; console.log(a - -b, a % b, a ** b);`,
	`var s = "x"; for (var i = 0; i < 3; i++) { s += i } console.log(s);`,
	`function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2) } console.log(fib(10));`,
	`var o = { a: 1, m() { return this.a * 2 } }; console.log(o.m());`,
	`class P { constructor(x) { this.x = x } get double() { return this.x * 2 } }
	 class Q extends P { constructor() { super(21); this.y = 2 } }
	 console.log(new Q().double);`,
	`var [a, b = 10, ...r] = [1]; console.log(a, b, r.length);`,
	`var {p, q: n, s = 5} = {p: 1, q: 2}; console.log(p, n, s);`,
	`const f = (x) => x * x; console.log(f(7));`,
	`let t = ""; try { throw new Error("e") } catch (e) { t = e.message } finally { t += "!" } console.log(t);`,
	`var sum = 0; for (const k of [1,2,3]) sum += k; console.log(sum);`,
	`var m = {}; for (var key in {a:1,b:2}) m[key] = 1; console.log(Object.keys(m).sort().join(","));`,
	`console.log(typeof undefined, typeof 1, typeof "s");`,
	`var n = 0; switch (2) { case 1: n = 1; break; case 2: n = 2; break; default: n = 9 } console.log(n);`,
	"var tpl = `a${1+1}b`; console.log(tpl);",
	`var obj = {get g(){return 42}, set s(v){this._v=v}}; obj.s = 7; console.log(obj.g, obj._v);`,
	`(async () => { const v = await Promise.resolve(8); console.log(v); })();`,
	`var reg = /o/g; console.log("foo".replace(reg, "0"));`,
	`console.log((1, 2, 3));`,
	`var counter = {n:0}; var inc = () => ++counter.n; inc(); inc(); console.log(counter.n);`,
}

// TestPrintSemantic：用本引擎执行原源码与打印产物，输出必须一致。
func TestPrintSemantic(t *testing.T) {
	for i, src := range semanticCorpus {
		want := evalCapture(t, i, src)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Errorf("case %d: parse: %v", i, err)
			continue
		}
		printed := Print(prog)
		got := evalCapture(t, i, printed)
		if want != got {
			t.Errorf("case %d: semantic mismatch\n src: %s\n printed: %s\n want: %q\n got: %q", i, src, printed, want, got)
		}
	}
}

// evalCapture 在独立引擎中执行并把 console.log 输出拼接返回。
func evalCapture(t *testing.T, caseIdx int, src string) string {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("case %d: NewContext: %v", caseIdx, err)
	}
	defer ctx.Close()

	// console 重定向 shim
	const shim = `globalThis.__out = []; globalThis.console = { log: function() { __out.push(Array.prototype.join.call(arguments, " ")) } };`
	if _, err := ctx.Eval(shim, "shim.js"); err != nil {
		t.Fatalf("case %d: shim: %v", caseIdx, err)
	}
	if _, err := ctx.Eval(src, "case.js"); err != nil {
		t.Errorf("case %d: eval error: %v; src: %s", caseIdx, err, src)
		return "EVAL_ERROR: " + err.Error()
	}
	// 排空异步任务（async 用例）
	if rl, ok := ctx.(interface{ RunLoop() }); ok {
		rl.RunLoop()
	}
	outVal, err := ctx.Global().Get("__out")
	if err != nil {
		t.Fatalf("case %d: read __out: %v", caseIdx, err)
	}
	return outVal.String()
}
