// O1-C2 基准矩阵扩展（docs/test-bundle-optimize-plan.md §5.3）：
// 对象属性访问（读/写 IC 命中）、方法调用（CallMethod IC）、字符串拼接、
// 数组操作、调用开销、GC 压力。
//
// 跑法：go test ./bench -bench . -benchmem
package bench

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// runJS 在独立 VM 中执行 JS 源码（每个基准迭代新建 VM，编译缓存命中）。
func runJS(b *testing.B, code string) {
	b.Helper()
	vm, err := interpreter.NewVM()
	if err != nil {
		b.Fatal(err)
	}
	defer vm.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Eval(code, "bench.js"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPropAccess 对象属性读（隐藏类 IC GetCached 命中路径）。
const propAccessCode = `
let s = 0;
const o = { a: 1, b: 2, c: 3 };
for (let i = 0; i < 100000; i++) { s += o.a + o.b + o.c; }
`

func BenchmarkPropAccess(b *testing.B) { runJS(b, propAccessCode) }

// BenchmarkPropSet 对象属性写（隐藏类 IC SetCached 命中路径）。
const propSetCode = `
const o = { a: 0 };
for (let i = 0; i < 100000; i++) { o.a = i; }
`

func BenchmarkPropSet(b *testing.B) { runJS(b, propSetCode) }

// BenchmarkMethodCall 方法调用（CallMethod IC per-PC 命中路径）。
const methodCallCode = `
const o = { v: 1, get() { return this.v; } };
let s = 0;
for (let i = 0; i < 100000; i++) { s += o.get(); }
`

func BenchmarkMethodCall(b *testing.B) { runJS(b, methodCallCode) }

// BenchmarkStrConcat 字符串拼接（+ 循环，动态长度）。
const strConcatCode = `
let s = "";
for (let i = 0; i < 10000; i++) { s += "x" + i; }
`

func BenchmarkStrConcat(b *testing.B) { runJS(b, strConcatCode) }

// BenchmarkArrayPush 数组追加 + length 同步。
const arrayPushCode = `
const a = [];
for (let i = 0; i < 100000; i++) { a.push(i); }
`

func BenchmarkArrayPush(b *testing.B) { runJS(b, arrayPushCode) }

// BenchmarkArrayMap 数组高阶方法（回调调用开销）。
const arrayMapCode = `
const a = [];
for (let i = 0; i < 10000; i++) { a.push(i); }
let s = 0;
for (let j = 0; j < 20; j++) { s += a.map(x => x * 2).length; }
`

func BenchmarkArrayMap(b *testing.B) { runJS(b, arrayMapCode) }

// BenchmarkCallOverhead 空函数调用开销。
const callOverheadCode = `
function noop() {}
let s = 0;
for (let i = 0; i < 100000; i++) { noop(); s++; }
`

func BenchmarkCallOverhead(b *testing.B) { runJS(b, callOverheadCode) }

// BenchmarkClosureCall 闭包调用（upvalue 访问）。
const closureCallCode = `
function make() { let n = 0; return () => ++n; }
const f = make();
let s = 0;
for (let i = 0; i < 100000; i++) { s += f(); }
`

func BenchmarkClosureCall(b *testing.B) { runJS(b, closureCallCode) }

// --- R4-1 / R4-2 专项基准（docs/jit-follow-up-development-plan.md §9.3
// 证据 6）：专用基准和综合基准。每个基准在 Auto 模式下运行，统计中必须
// 实际命中目标 tier（CalleeSpecialized/ClosureUpvalueSites）。

// runJSAuto 在 Auto 模式（Threshold=1）的独立 VM 中执行 JS 源码，用于 R4
// 专项基准。
func runJSAuto(b *testing.B, code string) {
	b.Helper()
	for i := 0; i < b.N; i++ {
		vm, err := interpreter.NewVM()
		if err != nil {
			b.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, TraceBudget: 65536, Stats: true})
		if _, err := vm.Eval(code, "bench-r4.js"); err != nil {
			b.Fatal(err)
		}
		stats := vm.JITStats()
		if err := vm.Close(); err != nil {
			b.Fatal(err)
		}
		if stats.CalleeSpecialized == 0 && stats.ClosureUpvalueSites == 0 {
			b.Fatalf("R4 benchmark did not hit the target tier: %+v", stats)
		}
	}
}

// BenchmarkCalleeInline4Args 四参数数值叶子 + 同一函数体两个调用点（R4-1）。
const calleeInline4ArgsCode = `
function leaf(a, b, c, d) { return (a + b) * (c - d); }
function run(n) {
  let s = 0;
  for (let i = 0; i < n; i++) {
    s += leaf(i, i + 1, 10, 3);
    s += leaf(i + 1, i, 5, 2);
  }
  return s;
}
run(100000);
`

func BenchmarkCalleeInline4Args(b *testing.B) { runJSAuto(b, calleeInline4ArgsCode) }

// BenchmarkCalleeInlineBoolean 返回 Boolean 的叶子驱动分支（R4-1）。
const calleeInlineBooleanCode = `
function pos(x) { return x > 0; }
function run(n) {
  let c = 0;
  for (let i = 0; i < n; i++) { if (pos(i)) c++; }
  return c;
}
run(100000);
`

func BenchmarkCalleeInlineBoolean(b *testing.B) { runJSAuto(b, calleeInlineBooleanCode) }

// BenchmarkClosureMultiUpvalue 多个 numeric upvalue 读/写闭包（R4-2）。
const closureMultiUpvalueCode = `
function make() { let a = 0; let b = 0; return () => { a++; b += a; return b; }; }
const f = make();
function run(fn, n) {
  let s = 0;
  for (let i = 0; i < n; i++) { s += fn(); }
  return s;
}
run(f, 100000);
`

func BenchmarkClosureMultiUpvalue(b *testing.B) { runJSAuto(b, closureMultiUpvalueCode) }

// BenchmarkClosureReadOnly 只读捕获闭包（R4-2，无写回）。
const closureReadOnlyCode = `
function make() { let a = 3; let b = 4; return () => a + b; }
const f = make();
function run(fn, n) {
  let s = 0;
  for (let i = 0; i < n; i++) { s += fn(); }
  return s;
}
run(f, 100000);
`

func BenchmarkClosureReadOnly(b *testing.B) { runJSAuto(b, closureReadOnlyCode) }

// BenchmarkGCPressure 短生命周期对象压力（GC 回收路径）。
const gcPressureCode = `
let keep = [];
for (let i = 0; i < 50000; i++) {
  const o = { x: i, y: { z: i }, arr: [i, i + 1] };
  if (i % 100 === 0) { keep.push(o); }
}
`

func BenchmarkGCPressure(b *testing.B) { runJS(b, gcPressureCode) }
