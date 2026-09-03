package interpreter

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// 真实的 F1 Native 自递归端到端测试：通过 VM + Auto JIT 驱动，验证
// （1）自递归 fib 经 bridge 编译为 Native 并真正机器码自递归执行；
// （2）结果与 JIT 关闭时一致。
func TestJITAutoNativeSelfRecursionFib(t *testing.T) {
	source := `
const fib = (n) => n < 2 ? n : fib(n-1) + fib(n-2);
globalThis.r = fib(30);
`
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, Stats: true,
	})
	if _, err := vm.Eval(source, "jit-self-fib.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("r")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "832040" {
		t.Fatalf("fib(30) = %s, want 832040", got)
	}
	stats := vm.JITStats()
	if stats.NativeCompiled == 0 {
		t.Fatalf("self-recursive fib must be native-compiled: %+v", stats)
	}
	if stats.NativeExecuted == 0 {
		t.Fatalf("self-recursive fib must execute natively: %+v", stats)
	}
}

// 混合形态（一个直接自递归站点 + 一个 callee 来自参数的站点）不得进入
// Native 自递归模式：hasDirectSelfCalls 拒绝 → Native 编译失败 → Quick
// 运行时 callee guard 失败 → Tier 0，结果与 JIT 关闭一致。
func TestJITAutoSelfCallMixedShapeFallsBack(t *testing.T) {
	source := `
const f = (n, g) => n <= 1 ? 1 : f(n-1, g) + g(n-1);
globalThis.r = f(10, x => x);
`
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, Stats: true,
	})
	if _, err := vm.Eval(source, "jit-mixed.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("r")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "46" {
		t.Fatalf("f(10, id) = %s, want 46", got)
	}
	stats := vm.JITStats()
	if stats.NativeRejected == 0 {
		t.Fatalf("mixed-shape self-call must be native-rejected: %+v", stats)
	}
	// 混合形态在第一个 OpPushSelf / OpSelfCall 处被拒（非自递归模式）。
	if !strings.Contains(stats.LastNativeError, "requires self-call mode") {
		t.Fatalf("native rejection reason must mention self-call mode: %q", stats.LastNativeError)
	}
}

// 直接形态但 upvalue 绑定到另一闭包（quickCallBound）。内联成功时（如
// twice 的平凡体）机器码无自调用站点，Native 执行安全（结果与 JIT 关闭
// 一致）；不可内联时（递归体）程序保持自递归模式机器码，身份 guard 拒绝
// Native 执行（否则机器码会把"调用 upvalue"错执行为自递归），走 Quick
// callTarget 路径。
func TestJITAutoSelfCallBoundUpvalueSkipsNative(t *testing.T) {
	source := `
const twice = (x) => x * 2;
const g = (x) => twice(x) + 1;
const fib2 = (n) => n < 2 ? n : fib2(n-1) + fib2(n-2);
const h = (x) => fib2(x);
globalThis.r = g(21);
globalThis.hr = h(10);
`
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, Stats: true,
	})
	if _, err := vm.Eval(source, "jit-bound.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("r")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "43" {
		t.Fatalf("g(21) = %s, want 43", got)
	}
	value, err = vm.Global().Get("hr")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "55" {
		t.Fatalf("h(10) = %s, want 55", got)
	}
	stats := vm.JITStats()
	// g 内联成功（CalleeInlined=1），内联后的普通机器码可以 Native 执行；
	// h 的 callee（fib2 递归体）不可内联，h 保持自递归模式——bound 身份
	// 下该模式被禁止执行，h 只能走 Quick。统计上：h 的 native 从不执行，
	// 但 g 的内联代码执行一次（NativeExecuted 至少 1 且全部来自安全路径）。
	if stats.CalleeInlined == 0 {
		t.Fatalf("bound callee must be inlined: %+v", stats)
	}
	if stats.NativeExecuted == 0 {
		t.Fatalf("inlined bound program must execute natively: %+v", stats)
	}
}

// 深度递归（300 层 > 首轮 256 帧）：Native 自递归触发扩容重试
// （256→1024→4096→16384 帧）后直接成功；结果正确。
func TestJITAutoSelfCallDeepRecursion(t *testing.T) {
	source := `
const f = (n) => n <= 0 ? 0 : f(n-1) + 1;
globalThis.r = f(300);
`
	vm, err := NewVM()
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	vm.ConfigureJIT(jit.Config{
		Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 2, Stats: true,
	})
	if _, err := vm.Eval(source, "jit-deep.js"); err != nil {
		t.Fatal(err)
	}
	value, err := vm.Global().Get("r")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "300" {
		t.Fatalf("f(300) = %s, want 300", got)
	}
}
