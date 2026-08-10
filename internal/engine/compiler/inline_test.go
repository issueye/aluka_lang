package compiler

// I-1 可内联判定测试：纯箭头函数（单表达式体、无闭包、无 arguments、
// 非 async/generator/rest/默认值/解构、参数 ≤8）标记 FuncTemplate.Inlinable；
// 其余保持 false（调用点展开见 compileCall，未展开走正常调用）。

import (
	"bytes"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// roundTripModule 序列化→反序列化，验证缓存格式字段保留。
func roundTripModule(t *testing.T, mod *bytecode.Module) *bytecode.Module {
	t.Helper()
	var buf bytes.Buffer
	if err := bytecode.Serialize(&buf, mod); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	got, err := bytecode.Deserialize(&buf)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	return got
}

// findInlinable 返回模块中 Inlinable=true 的函数模板索引集合。
func findInlinable(mod *bytecode.Module) []int {
	var idxs []int
	for i, fn := range mod.Functions {
		if fn.Inlinable {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

func TestInlinableMarking(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool // 是否恰好有一个 Inlinable 函数
	}{
		{"arrow single expr", `const add = (a, b) => a + b;`, true},
		{"arrow single expr multi op", `const f = (x) => x * 2 + 1;`, true},
		{"arrow block single return", `const g = (x) => { return x + 1; };`, true},
		{"arrow no params", `const z = () => 42;`, true},
		{"closure capture", `const y = 5; const h = (x) => x + y;`, false},
		// 顶层箭头引用 `arguments`：resolve 为 undefined 全局（无 upvalue，
		// aluka 顶层程序无 arguments 绑定），内联展开行为一致，故可内联。
		{"arguments reference (top-level)", `const r = (x) => arguments.length;`, true},
		{"recursive self", `const rec = (n) => n <= 0 ? 0 : rec(n - 1);`, false},
		{"multi statement", `const m = (x) => { x++; return x; };`, false},
		{"async", `const a = async (x) => x + 1;`, false},
		{"generator arrow invalid syntax", ``, false}, // 箭头函数无 generator 形式，跳过
		{"rest params", `const s = (...xs) => xs.length;`, false},
		{"default params", `const d = (x = 1) => x;`, false},
		{"normal function", `function nf(a, b) { return a + b; }`, false},
		{"arrow this ref", `const t = (x) => this.v + x;`, false},
		{"arrow nested outer local", `function outer() { const k = 1; const inner = (x) => x + k; return inner; }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod := compileFn(t, tc.code)
			idxs := findInlinable(mod)
			got := len(idxs) == 1
			if got != tc.want {
				t.Errorf("code %q: Inlinable count = %d (indices %v), want exactly-one=%v",
					tc.code, len(idxs), idxs, tc.want)
			}
		})
	}
}

// TestInlinableRoundTrip: Inlinable 标记经字节码序列化往返后保留。
func TestInlinableRoundTrip(t *testing.T) {
	mod := compileFn(t, `const add = (a, b) => a + b;`)
	if len(findInlinable(mod)) != 1 {
		t.Fatalf("expected one Inlinable function, got %v", findInlinable(mod))
	}
	// 序列化→反序列化后标记保留。
	round := roundTripModule(t, mod)
	if len(findInlinable(round)) != 1 {
		t.Fatalf("Inlinable lost after round trip: %v", findInlinable(round))
	}
}
