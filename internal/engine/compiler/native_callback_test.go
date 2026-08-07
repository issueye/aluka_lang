package compiler

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// O-6 编译器检测：简单箭头回调（单表达式、参数 ≤2、无闭包依赖）生成
// NativeCallbackDesc；复杂/不安全回调保持 nil（走正常调用链）。

func compileFn(t *testing.T, code string) *bytecode.Module {
	t.Helper()
	prog, err := parser.Parse(code)
	if err != nil {
		t.Fatalf("parse %q: %v", code, err)
	}
	c := New()
	mod, err := c.Compile(prog, "test.js")
	if err != nil {
		t.Fatalf("compile %q: %v", code, err)
	}
	return mod
}

// findCallback 在编译产物中查找 NativeCallback 非 nil 的函数模板。
func findCallback(mod *bytecode.Module) *bytecode.NativeCallbackDesc {
	for _, fn := range mod.Functions {
		if fn.NativeCallback != nil {
			return fn.NativeCallback
		}
	}
	return nil
}

// findNoCallback 统计编译产物中 NativeCallback 为 nil 的函数模板数。
func countPlain(mod *bytecode.Module) int {
	n := 0
	for _, fn := range mod.Functions {
		if fn.NativeCallback == nil {
			n++
		}
	}
	return n
}

func TestNativeCallbackSimple(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"binary", "[1,2,3].map(x => x * 2)"},
		{"prop+mod+cmp", "arr.filter(x => x.v % 3 === 0)"},
		{"two-param", "arr.map((x, i) => x + i)"},
		{"literal", "arr.map(x => 7)"},
		{"neg", "arr.map(x => -x)"},
		{"prop-read", "arr.map(x => x.v)"},
		{"bitwise", "arr.map(x => x << 1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod := compileFn(t, tc.code)
			if findCallback(mod) == nil {
				t.Errorf("%q: expected a NativeCallback template, got none", tc.code)
			}
		})
	}
}

func TestNativeCallbackRejected(t *testing.T) {
	// 这些回调必须走慢路径（NativeCallback == nil），语义不改变。
	cases := []struct {
		name string
		code string
	}{
		{"closure", "arr.map(x => x * k)"},                       // 闭包引用
		{"block-body", "arr.map(x => { const y = x; return y; })"}, // 多语句体
		{"named-fn", "arr.map(function (x) { return x; })"},      // 非箭头
		{"call-expr", "arr.map(x => String(x))"},                 // 函数调用
		{"computed", "arr.map(x => x[k])"},                       // 计算属性
		{"ternary", "arr.map(x => x > 1 ? x : 0)"},               // 三元
		{"async", "arr.map(async x => x)"},                       // async
		{"rest", "arr.map((...a) => a[0])"},                      // rest 参数
		{"default", "arr.map((x = 1) => x)"},                     // 默认值
		{"destructure", "arr.map(({v}) => v)"},                   // 解构
		{"logical", "arr.map(x => x && 1)"},                      // 逻辑运算
		{"string-method", "arr.map(x => x.trim())"},              // 方法调用
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod := compileFn(t, tc.code)
			if findCallback(mod) != nil {
				t.Errorf("%q: NativeCallback should be nil (slow path), got a desc", tc.code)
			}
		})
	}
}

func TestNativeCallbackNotClosureDependent(t *testing.T) {
	// 顶层模块的 map 回调应生成描述（不依赖运行时作用域）。
	mod := compileFn(t, "const r = [1,2,3].map(x => x * 2);")
	nc := findCallback(mod)
	if nc == nil {
		t.Fatal("expected NativeCallback desc for top-level map callback")
	}
	if nc.ParamCount != 1 {
		t.Errorf("ParamCount = %d, want 1", nc.ParamCount)
	}
	if len(nc.Instrs) == 0 {
		t.Error("expected at least one micro-instruction")
	}
	// 最后一个指令必须是求值结果（栈顶）。
	last := nc.Instrs[len(nc.Instrs)-1]
	if last.Op != bytecode.CBBinOp {
		t.Errorf("last instr op = %v, want CBBinOp", last.Op)
	}
}
