// O1-C2 基准矩阵扩展（docs/test-bundle-optimize-plan.md §5.3）：
// 对象属性访问（读/写 IC 命中）、方法调用（CallMethod IC）、字符串拼接、
// 数组操作、调用开销、GC 压力。
//
// 跑法：go test ./bench -bench . -benchmem
package bench

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
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

// BenchmarkGCPressure 短生命周期对象压力（GC 回收路径）。
const gcPressureCode = `
let keep = [];
for (let i = 0; i < 50000; i++) {
  const o = { x: i, y: { z: i }, arr: [i, i + 1] };
  if (i % 100 === 0) { keep.push(o); }
}
`

func BenchmarkGCPressure(b *testing.B) { runJS(b, gcPressureCode) }
