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
		// 顶层箭头引用 `arguments`：解析为 undefined 全局（OpLoadGlobal，
		// 不在内联白名单）→ 不标记（回退普通调用，行为一致且更安全）。
		{"arguments reference (top-level)", `const r = (x) => arguments.length;`, false},
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

// TestInlineExpandsCallSite: const 绑定的可内联函数调用点被展开——
// 顶层程序 Code 中不再出现对 add 的 OpCall（被内联体指令替换）。
func TestInlineExpandsCallSite(t *testing.T) {
	mod := compileFn(t, `
const add = (a, b) => a + b;
globalThis.__r = add(1, 2);
`)
	top := mod.Functions[0]
	for pc := 0; pc+4 <= len(top.Code); pc += bytecode.InstrSize {
		op := bytecode.Opcode(top.Code[pc])
		if op == bytecode.OpCall || op == bytecode.OpCallMethod || op == bytecode.OpMakeClosure {
			// OpMakeClosure 出现在 const add 的初始化处（合法）；
			// OpCall 应被内联替换。
			if op == bytecode.OpCall {
				t.Errorf("call site was not inlined: OpCall at pc=%d", pc)
			}
		}
	}
	// 内联体指令应出现：LoadLocal 参数槽 + OpAdd。
	seenAdd := false
	for pc := 0; pc+4 <= len(top.Code); pc += bytecode.InstrSize {
		if bytecode.Opcode(top.Code[pc]) == bytecode.OpAdd {
			seenAdd = true
			break
		}
	}
	if !seenAdd {
		t.Error("inlined body (OpAdd) not found in caller code")
	}
}
