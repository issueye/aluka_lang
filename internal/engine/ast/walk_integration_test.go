package ast_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// parse 用模块上下文解析源码（允许顶层 await）。
func parse(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.ParseModule(src)
	if err != nil {
		t.Fatalf("parse error: %v\nsrc:\n%s", err, src)
	}
	return prog
}

// collectWalkTypes 用 Walk 遍历并收集访问到的节点类型名。
func collectWalkTypes(prog *ast.Program) map[string]int {
	counts := make(map[string]int)
	ast.Walk(prog, func(n ast.Node) bool {
		counts[reflect.TypeOf(n).Elem().Name()]++
		return true
	})
	return counts
}

// TestWalkVisitsAllNodeTypes 用覆盖全语法的真实源码验证 Walk 可达全部
// 节点类型（防止 ForEachChild 漏枚举导致某些类型永远不被访问）。
func TestWalkVisitsAllNodeTypes(t *testing.T) {
	src := `
import def, {x} from 'mod';
export {x};
debugger;
export default function main() {
  const obj = {key: val, shorthand, [comp]: cv, method(a) { return a; }, ...sp};
  const arr = [1, 2, ...spread];
  const tpl = ` + "`a${x}b`" + `;
  const tagged = tag` + "`x${y}z`" + `;
  if (cond) { run(); } else { other(); }
  while (w) { wb(); continue; }
  do { db(); } while (dc);
  for (let i = 0; i < n; i++) { fb(); }
  for (const k in kobj) { kin(); }
  for (const [k2, v2] of entries) { ofb(); }
  switch (disc) { case 1: sw(); break; default: swd(); }
  try { tb(); } catch (e) { cb(e); } finally { fin(); }
  lab: { labeled(); }
  throw err;
  return sum;
}
function* gen() { yield 1; yield* inner(); return ret; }
async function af() { await promise; }
function nt() { return new.target; }
class C extends B {
  field = init;
  static s = 1;
  get g() { return this.v; }
  set s2(v) { this.v = v; }
  [ck]() {}
  method(a, {b = c} = {}) { return super.f + a + b; }
}
const ce = class extends X { m() {} };
const ne = new Foo(1, 2);
const n = 1, s = 'str', b = true, nl = null, u = undefined, r = /x/g, bi = 10n;
const ue = -neg, up = ++inc, bin = a + b, lg = l ?? r, cd = ctest ? c1 : c2, sq = (s1, s2);
const arrow = (a, [bb, ...rest]) => a + bb;
const {p1, p2: al = dflt} = src;
const [, , hole] = harr;
({dest} = src2);
obj.a = 1;
`
	got := collectWalkTypes(parse(t, src))
	// 期望被访问到的全部节点类型（类型名 = ast.go 中的 struct 名）。
	expect := []string{
		"Program", "ImportDecl", "ExportDecl", "ExportDefaultDecl",
		"VarDecl", "FunctionDecl", "BlockStmt", "ExprStmt", "EmptyStmt",
		"IfStmt", "WhileStmt", "DoWhileStmt", "ForStmt", "ForInStmt", "ForOfStmt",
		"ReturnStmt", "BreakStmt", "ContinueStmt", "ThrowStmt", "TryStmt",
		"SwitchStmt", "LabeledStmt", "ClassDecl", "ClassExpr",
		"Identifier", "NumberLit", "BigIntLit", "StringLit", "BoolLit",
		"NullLit", "RegexLit",
		"TemplateLit", "TaggedTemplateExpr", "ArrayLit", "ObjectLit",
		"ThisExpr", "SuperExpr", "MemberExpr", "CallExpr", "NewExpr",
		"UnaryExpr", "UpdateExpr", "BinaryExpr", "LogicalExpr", "AssignExpr",
		"ConditionalExpr", "SequenceExpr", "SpreadElement",
		"FunctionExpr", "ArrowFunc", "NewTargetExpr", "YieldExpr", "AwaitExpr",
		"ArrayPattern", "ObjectPattern",
	}
	var missing []string
	for _, name := range expect {
		if got[name] == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("Walk 未访问以下节点类型: %v (visited: %v)", missing, keysOf(got))
	}
}

func keysOf(m map[string]int) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// refNames 收集 ForEachRef 命中的标识符名（排序去重）。
func refNames(n ast.Node) []string {
	set := make(map[string]bool)
	ast.ForEachRef(n, func(id *ast.Identifier) {
		set[id.Name] = true
	})
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertRefs(t *testing.T, src string, want ...string) {
	t.Helper()
	got := refNames(parse(t, src))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ForEachRef(%q) = %v, want %v", src, got, want)
	}
}

// TestForEachRefSemantics 验证引用位置语义：声明名/非计算属性键跳过，
// 计算属性键/真实引用/赋值语境模式目标命中。
//
// 说明：参数名在函数体内的引用（如 `return a` 引用参数 a）当前按保守
// 语义计入——scope 感知（区分参数绑定与外层绑定）留作后续任务。
func TestForEachRefSemantics(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{`obj.method;`, []string{"obj"}},
		{`obj[a];`, []string{"a", "obj"}},
		{`obj[comp].x;`, []string{"comp", "obj"}},
		{`({a: x});`, []string{"x"}},
		{`({a});`, []string{"a"}}, // 简写命中
		{`const {a, b: c = d} = obj;`, []string{"d", "obj"}},
		{`const {[k]: v} = obj;`, []string{"k", "obj"}},
		{`function f(a, {b = c} = {}) { return a + b; }`, []string{"a", "b", "c"}},
		{`class C { m() { return x; } [k]() {} field = y; }`, []string{"k", "x", "y"}},
		{`try {} catch (e) { g(e); }`, []string{"e", "g"}},
		{`({a} = obj);`, []string{"a", "obj"}}, // 赋值模式目标命中
		{`({a: b = dflt} = src);`, []string{"b", "dflt", "src"}},
		{`[a1, a2] = arr;`, []string{"a1", "a2", "arr"}},
		{`for (let i = 0; i < n; i++) { body(); }`, []string{"body", "i", "n"}},
		{`for (const k in kobj) { use(k); }`, []string{"k", "kobj", "use"}},
		{`for (const [k2, v2] of entries) { use2(k2); }`, []string{"entries", "k2", "use2"}},
		{`for (existing of iterable) { use(existing); }`, []string{"existing", "iterable", "use"}},
		{`export function h() { return q; }`, []string{"q"}},
		{`export default class D extends E { m() { return super.f; } }`, []string{"E"}},
		{`import def, {x} from 'm'; def(x);`, []string{"def", "x"}},
		{`function outer() { function inner(a) { return b; } }`, []string{"b"}}, // 形参声明 a 跳过
		{`const f = function named(c) { return c; }; f();`, []string{"c", "f"}}, // 函数表达式名跳过
		{`tag` + "`a${x}b`" + `;`, []string{"tag", "x"}},
		{`new Foo(1, 2);`, []string{"Foo"}},
		{`function* g() { yield* gen(); }`, []string{"gen"}},
		{`class A extends B { m() { return super.x; } }`, []string{"B"}}, // super 非标识符，x 属性名
		{`const t = this, su = new T();`, []string{"T"}},
		{`switch (disc) { case c1: case2(); break; default: swd(); }`, []string{"c1", "case2", "disc", "swd"}},
		{`do { body2(); } while (wcond);`, []string{"body2", "wcond"}},
		{`lab: { labeled(); }`, []string{"labeled"}},
		{`const arrow = (a, [bb, ...rest]) => a + bb;`, []string{"a", "bb"}},
		{`let x = 1;`, nil}, // 声明名与字面量均非引用
		{`const o = { get g() { return getterBody(); }, set s(v) { setterBody(v); } };`,
			[]string{"getterBody", "setterBody", "v"}}, // 方法名/键跳过
		{`await promise;`, []string{"promise"}},
		{`const seq = (s1, s2);`, []string{"s1", "s2"}},
		{`const {a3: {b3}} = nested;`, []string{"nested"}},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			assertRefs(t, tc.src, tc.want...)
		})
	}
}
