package jit

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// F3 专项 benchmark：fib(30) 的 Quick 自递归执行，用于精确定位 executeQuick
// 递归路径的开销热点（配合 -cpuprofile 分析）。

func fibQuickProgram() *Program {
	return &Program{
		NumParams:   1,
		NumLocals:   4,
		SelfUpvalue: 0,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 2},
			{Op: OpLt},
			{Op: OpJumpFalse, Operand: 6},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpJump, Operand: 17},
			{Op: OpPushSelf},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpSub},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpPushSelf},
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 2},
			{Op: OpSub},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpAdd},
			{Op: OpReturn},
		},
	}
}

func TestFibQuickProgram(t *testing.T) {
	p := fibQuickProgram()
	result, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(30)})
	if err != nil || reason != Executed || result.String() != "832040" {
		t.Fatalf("fib(30) = %v reason=%v err=%v (want 832040)", result, reason, err)
	}
}

// TestFibNativeSelfCall 验证 F1 Native 自递归：fib 的 IR 含 OpSelfCall，
// hasDirectSelfCalls 识别真实形态（push_self; args…; self_call），
// CompileNative 生成机器码递归 call，执行结果与 Quick 一致。
func TestFibNativeSelfCall(t *testing.T) {
	p := fibQuickProgram()
	if !hasDirectSelfCalls(p.Code) {
		t.Fatal("fib IR must be detected as direct self-call shape")
	}
	p.hasSelfCall = hasDirectSelfCalls(p.Code)
	if err := p.CompileNative(); err != nil {
		t.Fatalf("CompileNative: %v", err)
	}
	if !p.HasNative() {
		t.Fatal("fib was not compiled to native")
	}
	result, reason, err := p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Number(30)})
	if err != nil || reason != Executed {
		t.Fatalf("ExecuteNative: reason=%v err=%v", reason, err)
	}
	if got := result.String(); got != "832040" {
		t.Fatalf("native fib(30) = %s, want 832040", got)
	}
}

// TestFibNativeSelfCallDeep 验证递归深度限制（>256 层回退 GuardFailed）。
func TestFibNativeSelfCallDeep(t *testing.T) {
	// 构造深度递归：f(n) = f(n-1) + 1，n=500 超过 256 上限。
	p := &Program{
		NumParams:   1,
		NumLocals:   3,
		SelfUpvalue: 0,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpLt},
			{Op: OpJumpFalse, Operand: 6},
			{Op: OpConst, Value: 1},
			{Op: OpReturn},
			{Op: OpPushSelf}, // 标准布局：callee 先压，参数后压
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpSub},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpAdd},
			{Op: OpReturn},
		},
	}
	if !hasDirectSelfCalls(p.Code) {
		t.Fatal("deep IR must be detected as direct self-call shape")
	}
	p.hasSelfCall = hasDirectSelfCalls(p.Code)
	if err := p.CompileNative(); err != nil {
		t.Fatalf("CompileNative: %v", err)
	}
	// 深度 200 < 256：首轮帧区直接容纳，成功，结果 201（deep(n) = n+1）。
	result, reason, err := p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Number(200)})
	if err != nil || reason != Executed || result.String() != "201" {
		t.Fatalf("deep(200) = %v reason=%v err=%v (want 201)", result, reason, err)
	}
	// 深度 500 > 256：触发扩容重试（256→1024→4096→16384 帧），结果仍正确。
	result, reason, err = p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Number(500)})
	if err != nil || reason != Executed || result.String() != "501" {
		t.Fatalf("deep(500) = %v reason=%v err=%v (want 501 via grow-retry)", result, reason, err)
	}
	// 深度 30000 > 全局上限 16384：GuardFailed 回退 Tier 0。
	_, reason, err = p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Number(30000)})
	if err != nil || reason != GuardFailed {
		t.Fatalf("deep(30000) reason=%v err=%v (want GuardFailed)", reason, err)
	}
}

// TestHasDirectSelfCallsShapes 表驱动验证 hasDirectSelfCalls 的形态判定：
// 真实 fib 形态（push_self; args…; self_call）为真；无自调用/混合形态/
// 嵌套调用/callee 来自局部变量为假。
func TestHasDirectSelfCallsShapes(t *testing.T) {
	cases := []struct {
		name string
		code []Instr
		want bool
	}{
		{
			name: "fib direct shape",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpConst, Value: 2},
				{Op: OpLt},
				{Op: OpJumpFalse, Operand: 6},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpJump, Operand: 17},
				{Op: OpPushSelf},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpConst, Value: 1},
				{Op: OpSub},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpPushSelf},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpConst, Value: 2},
				{Op: OpSub},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpAdd},
				{Op: OpReturn},
			},
			want: true,
		},
		{
			name: "zero-arg direct",
			code: []Instr{
				{Op: OpPushSelf},
				{Op: OpSelfCall, Operand: 0},
				{Op: OpReturn},
			},
			want: true,
		},
		{
			name: "mixed direct and param callee",
			code: []Instr{
				{Op: OpPushSelf},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpLoadLocal, Operand: 2},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpAdd},
				{Op: OpReturn},
			},
			want: false,
		},
		{
			name: "callee from local only",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 2},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpReturn},
			},
			want: false,
		},
		{
			name: "nested self call in args",
			code: []Instr{
				{Op: OpPushSelf},
				{Op: OpPushSelf},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpReturn},
			},
			want: false,
		},
		{
			name: "jump inside args",
			code: []Instr{
				{Op: OpPushSelf},
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpConst, Value: 0},
				{Op: OpGt},
				{Op: OpJumpTrue, Operand: 8},
				{Op: OpConst, Value: 0},
				{Op: OpJump, Operand: 9},
				{Op: OpConst, Value: 1},
				{Op: OpSelfCall, Operand: 1},
				{Op: OpReturn},
			},
			want: false,
		},
		{
			name: "no self calls",
			code: []Instr{
				{Op: OpLoadLocal, Operand: 1},
				{Op: OpConst, Value: 1},
				{Op: OpAdd},
				{Op: OpReturn},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDirectSelfCalls(tc.code); got != tc.want {
				t.Fatalf("hasDirectSelfCalls = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMixedSelfCallShapeRejectedNative 验证混合形态（一个直接自递归站点 +
// 一个 callee 来自参数的站点）不得进入 Native 自递归模式：hasSelfCall 为
// false，CompileNative 拒绝（回退 Quick），Quick 在非 quickSelf 的 callee
// 处 GuardFailed（再回退 Tier 0 语义正确）。
func TestMixedSelfCallShapeRejectedNative(t *testing.T) {
	p := &Program{
		NumParams:   2,
		NumLocals:   4,
		SelfUpvalue: 0,
		Code: []Instr{
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpLe},
			{Op: OpJumpTrue, Operand: 17},
			{Op: OpPushSelf}, // 直接形态：f(n-1, g)
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpSub},
			{Op: OpLoadLocal, Operand: 2},
			{Op: OpSelfCall, Operand: 2},
			{Op: OpLoadLocal, Operand: 2}, // 非直接形态：g(n-1)
			{Op: OpLoadLocal, Operand: 1},
			{Op: OpConst, Value: 1},
			{Op: OpSub},
			{Op: OpSelfCall, Operand: 1},
			{Op: OpAdd},
			{Op: OpReturn},
			{Op: OpConst, Value: 1},
			{Op: OpReturn},
		},
	}
	if p.hasSelfCall {
		t.Fatal("mixed shape must not enable self-call mode")
	}
	if err := p.CompileNative(); err == nil {
		t.Fatal("CompileNative must reject a program with non-direct OpSelfCall")
	}
	// Quick：非直接站点 callee（参数值）不是 quickSelf → GuardFailed（Tier 0 兜底）。
	_, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(10), engine.Number(5)})
	if err != nil || reason != GuardFailed {
		t.Fatalf("Quick mixed shape: reason=%v err=%v (want GuardFailed)", reason, err)
	}
}

// TestSelfCallLoopBudgetYield 验证自递归 + 循环组合：小 budget 强制在递归
// 中途 yield（预算轮询 RET 回 Go、Go 恢复后 CallAt(resume) 重入），恢复点
// 的 R11 reload stub 必须重算帧基址——否则 locals 经被 clobber 的 R11 访问
// 得到错误结果。执行多次以覆盖递归不同深度的恢复。
func TestSelfCallLoopBudgetYield(t *testing.T) {
	// f(n) = n<=0 ? 1 : sum(1..n) + f(n-1)；f(0)=1 → f(3)=11。
	code := []Instr{
		{Op: OpLoadLocal, Operand: 1}, // n
		{Op: OpConst, Value: 0},
		{Op: OpGt},
		{Op: OpJumpFalse, Operand: 28},
		{Op: OpConst, Value: 0},
		{Op: OpStoreLocal, Operand: 2}, // acc = 0
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpStoreLocal, Operand: 3}, // i = n
		{Op: OpLoadLocal, Operand: 2},  // loop head：acc
		{Op: OpLoadLocal, Operand: 3},
		{Op: OpAdd},
		{Op: OpStoreLocal, Operand: 2}, // acc += i
		{Op: OpLoadLocal, Operand: 3},
		{Op: OpConst, Value: 1},
		{Op: OpSub},
		{Op: OpStoreLocal, Operand: 3}, // i--
		{Op: OpLoadLocal, Operand: 3},
		{Op: OpConst, Value: 0},
		{Op: OpGt},
		{Op: OpJumpTrue, Operand: 8}, // backedge（预算轮询）
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConst, Value: 1},
		{Op: OpSub},
		{Op: OpSelfCall, Operand: 1}, // f(n-1)
		{Op: OpLoadLocal, Operand: 2},
		{Op: OpAdd},
		{Op: OpReturn},
		{Op: OpConst, Value: 1}, // base：n<=0 → 1
		{Op: OpReturn},
	}
	p := &Program{NumParams: 1, NumLocals: 4, SelfUpvalue: 0, Code: code}
	if !hasDirectSelfCalls(p.Code) {
		t.Fatal("loop+self-call IR must be detected as direct shape")
	}
	p.hasSelfCall = hasDirectSelfCalls(p.Code)
	if err := p.CompileNative(); err != nil {
		t.Fatalf("CompileNative: %v", err)
	}
	// 期望值来自 Quick 执行（独立实现路径）。
	quick, reason, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(5)})
	if err != nil || reason != Executed {
		t.Fatalf("Quick f(5): reason=%v err=%v", reason, err)
	}
	for _, budget := range []uint32{1, 2, 3, 7, 65536} {
		result, reason, yields, err := p.ExecuteNativeBudget(engine.Undefined(), []engine.Value{engine.Number(5)}, budget)
		if err != nil || reason != Executed {
			t.Fatalf("Native f(5) budget=%d: reason=%v err=%v", budget, reason, err)
		}
		if result.String() != quick.String() {
			t.Fatalf("Native f(5) budget=%d = %s, Quick = %s", budget, result, quick)
		}
		if budget < 65536 && yields == 0 {
			t.Fatalf("budget=%d must yield mid-recursion (yields=%d)", budget, yields)
		}
	}
}

func BenchmarkFibQuickRecursion(b *testing.B) {
	p := fibQuickProgram()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := p.Execute(engine.Undefined(), []engine.Value{engine.Number(30)}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFibNativeSelfCall(b *testing.B) {
	p := fibQuickProgram()
	p.hasSelfCall = hasDirectSelfCalls(p.Code)
	if err := p.CompileNative(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := p.ExecuteNative(engine.Undefined(), []engine.Value{engine.Number(30)}); err != nil {
			b.Fatal(err)
		}
	}
}
