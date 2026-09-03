// 本文件度量「字节码优化器默认化」对性能的净影响：
//
//   - BenchmarkOptCompile：优化开/关的冷编译耗时（解析+编译+优化），
//     量化把 OptimizeModule 设为编译管线默认步骤引入的编译开销。
//   - BenchmarkOptRun：用「优化产物」vs「未优化产物」执行同一负载的耗时，
//     隔离优化对运行期速度的影响。
//
// 所有基准关闭 JIT（ConfigureJIT Mode=Off），避免 JIT 热点优化掩盖字节码
// 优化器的效果。注意 foldConstants 仅折叠 PUSH_CONST（常量池）对，不折叠
// PUSH_INT 立即数，因此负载特意用字符串拼接（常量池）触发折叠 + 死代码消除。
//
// 跑法：go test ./bench -bench BenchmarkOpt -benchmem -count=3
package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// fib30Src：经典递归 fib。热路径无可折叠常量、几乎无死代码，作为
// 「典型计算负载上优化器近乎无收益」的诚实基线。
var fib30Src = "function fib(n){if(n<2)return n;return fib(n-1)+fib(n-2)}fib(30)"

// loopArithSrc：紧凑数值循环（算术 + 数组写）。编译产物以 PUSH_INT 为主，
// foldConstants 不介入，度量优化器在普通算术循环上的边际影响。
var loopArithSrc = `let a=new Array(1000);for(let i=0;i<1000;i++){a[i]=i*i+i-i/2;}a[999]`

// loopFoldSrc：热循环体内含可折叠整数表达式（1+2+3+4）。Tier 1.1 前 PUSH_INT
// 不折，每次迭代都执行 4 加法；Tier 1.1 后折叠为 PUSH_INT 10，单条压栈。
// 用于度量 PUSH_INT 折叠在热路径上的真实收益。
var loopFoldSrc = `function f(n){let s=0;for(let i=0;i<n;i++){s+=1+2+3+4+5+6;}return s;}f(200000)`

// deadExprSrc 构造一个函数体为大量字符串拼接表达式语句（"sN"+"tN";）的源码：
// 每条语句编译为 PUSH_CONST;PUSH_CONST;ADD;POP，优化器先折叠为 PUSH_CONST;POP，
// 次轮按纯 push-pop 对消除。优化后 f() 退化为 return 1；未优化时每次调用都要
// 执行全部拼接。这是新 pass（常量折叠 + 不可达/push-pop 消除）的最大受益形态。
func deadExprSrc(blocks int) string {
	var b strings.Builder
	b.Grow(blocks * 24 + 64)
	b.WriteString("function f(){\n")
	for i := 0; i < blocks; i++ {
		fmt.Fprintf(&b, "  \"s%d\"+\"t%d\";\n", i, i)
	}
	b.WriteString("  return 1;\n}\nlet s=0;for(let k=0;k<200;k++){s+=f();}s;\n")
	return b.String()
}

var deadExpr300 = deadExprSrc(300)

// newBenchVM 返回关闭 JIT 的 VM，隔离字节码优化器效果。
func newBenchVM(b *testing.B) *interpreter.VM {
	b.Helper()
	vm, err := interpreter.NewVM()
	if err != nil {
		b.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Off})
	return vm
}

// BenchmarkOptCompile 对比优化开/关的冷编译耗时。
// 每次迭代独立 Compile（不含磁盘缓存），直接量化解优化引入的编译成本。
func BenchmarkOptCompile(b *testing.B) {
	cases := []struct{ name, src string }{
		{"fib30", fib30Src},
		{"loopArith", loopArithSrc},
		{"loopFold", loopFoldSrc},
		{"deadExpr300", deadExpr300},
	}
	for _, c := range cases {
		for _, opt := range []bool{true, false} {
			opt, tag := opt, "opt"
			if !opt {
				tag = "noopt"
			}
			b.Run(c.name+"_"+tag, func(b *testing.B) {
				vm := newBenchVM(b)
				defer vm.Close()
				vm.SetOptimizeBytecode(opt)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := vm.Compile(c.src, "b.js"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkOptRun 对比「优化产物」与「未优化产物」的运行耗时。
// 每个负载预编译两份 module（opt/noopt），先断言二者结果一致（优化保持语义），
// 再在 b.N 循环中只测 RunModule，隔离优化对执行速度的影响。
func BenchmarkOptRun(b *testing.B) {
	cases := []struct{ name, src string }{
		{"fib30", fib30Src},
		{"loopArith", loopArithSrc},
		{"loopFold", loopFoldSrc},
		{"deadExpr300", deadExpr300},
	}
	for _, c := range cases {
		// 预编译：opt / noopt 各一份 module，分别绑定各自 VM。
		vmOpt := newBenchVM(b)
		vmOpt.SetOptimizeBytecode(true)
		modOpt, err := vmOpt.Compile(c.src, "b.js")
		if err != nil {
			b.Fatalf("compile(opt) %s: %v", c.name, err)
		}
		vmNoopt := newBenchVM(b)
		vmNoopt.SetOptimizeBytecode(false)
		modNoopt, err := vmNoopt.Compile(c.src, "b.js")
		if err != nil {
			b.Fatalf("compile(noopt) %s: %v", c.name, err)
		}
		// 正确性门：优化必须保持语义，opt 与 noopt 结果一致。
		wantV, err := vmOpt.RunModule(modOpt)
		if err != nil {
			b.Fatalf("run(opt) %s: %v", c.name, err)
		}
		gotV, err := vmNoopt.RunModule(modNoopt)
		if err != nil {
			b.Fatalf("run(noopt) %s: %v", c.name, err)
		}
		if wantV.String() != gotV.String() {
			b.Fatalf("%s: opt=%s noopt=%s (优化改变了语义)", c.name, wantV.String(), gotV.String())
		}

		for _, tc := range []struct {
			tag string
			vm  *interpreter.VM
			mod *bytecode.Module
		}{
			{"opt", vmOpt, modOpt},
			{"noopt", vmNoopt, modNoopt},
		} {
			tc := tc
			b.Run(c.name+"_"+tc.tag, func(b *testing.B) {
				defer tc.vm.Close()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := tc.vm.RunModule(tc.mod); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
