package bytecode_test

import (
	"math"
	"math/big"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func makeCode(ops ...struct {
	op      bytecode.Opcode
	operand uint32
}) []byte {
	code := make([]byte, 0, len(ops)*bytecode.InstrSize)
	for _, in := range ops {
		bytecode.Encode(&code, in.op, in.operand)
	}
	return code
}

func TestOptimizeRemovesPurePushPopAndRelocatesPCs(t *testing.T) {
	// PUSH_TRUE; JMP_TRUE_POP -> old pc 16; PUSH_INT; POP; RETURN_UNDEF.
	// The literal expression is removed and the conditional jump is relocated
	// to the new end of the function.
	fn := &bytecode.FuncTemplate{
		SourceFile: "fixture.js",
		Code: makeCode(
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpPushTrue, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpJmpTruePop, operand: 8}, // 8 + (4 + 4) = old pc 16
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpPushInt, operand: 42},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpPop, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpReturnUndef, operand: 0},
		),
		LineStarts: []bytecode.LineEntry{
			{PC: 0, Line: 1},
			{PC: 8, Line: 2},
			{PC: 16, Line: 3},
		},
	}
	mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}
	stats, err := bytecode.OptimizeModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RemovedInstructions != 2 {
		t.Fatalf("removed instructions = %d, want 2", stats.RemovedInstructions)
	}
	if got := len(fn.Code) / bytecode.InstrSize; got != 3 {
		t.Fatalf("instruction count = %d, want 3", got)
	}
	op, operand, _ := bytecode.Decode(fn.Code, bytecode.InstrSize)
	if op != bytecode.OpJmpTruePop || bytecode.SignedOperand(operand) != 0 {
		t.Fatalf("relocated conditional jump = %s/%d, want JmpTruePop/0", op, bytecode.SignedOperand(operand))
	}
	if len(fn.LineStarts) != 2 || fn.LineStarts[0].PC != 0 || fn.LineStarts[1].PC != 8 {
		t.Fatalf("line table after relocation = %#v, want PCs 0 and 8", fn.LineStarts)
	}
	if err := bytecode.ValidateModule(mod); err != nil {
		t.Fatalf("optimized module failed validation: %v", err)
	}
}

func TestOptimizeFusesLocalPropertyAccess(t *testing.T) {
	fn := &bytecode.FuncTemplate{
		SourceFile: "fixture.js",
		NumLocals:  8,
		Constants:  make([]engine.Value, 20),
		Code: makeCode(
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpLoadLocal, operand: 7},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpGetProp, operand: 19},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpReturn, operand: 0},
		),
	}
	mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}
	stats, err := bytecode.OptimizeModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FusedInstructions != 1 {
		t.Fatalf("fused instructions = %d, want 1", stats.FusedInstructions)
	}
	op, operand, _ := bytecode.Decode(fn.Code, 0)
	if op != bytecode.OpGetPropLocal || operand != 7<<16|19 {
		t.Fatalf("fused instruction = %s/%d, want GET_PROP_LOCAL/%d", op, operand, 7<<16|19)
	}
}

func TestOptimizeRelocatesTryTable(t *testing.T) {
	fn := &bytecode.FuncTemplate{
		SourceFile: "try.js",
		Code: makeCode(
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpTryEnter, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpPushInt, operand: 1},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpPop, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpTryExit, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpReturnUndef, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpReturnUndef, operand: 0},
		),
		TryTable: []bytecode.TryEntry{{StartPC: 0, HasCatch: true, CatchPC: 20}},
	}
	mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}
	if _, err := bytecode.OptimizeModule(mod); err != nil {
		t.Fatal(err)
	}
	if fn.TryTable[0].StartPC != 0 || fn.TryTable[0].CatchPC != 12 {
		t.Fatalf("try table = %#v, want start 0/catch 12", fn.TryTable)
	}
	if err := bytecode.ValidateModule(mod); err != nil {
		t.Fatalf("optimized try function failed validation: %v", err)
	}
}

func TestOptimizeThreadsUnconditionalJump(t *testing.T) {
	// pc 0 -> pc 8 -> pc 16. The middle jump remains a valid target for other
	// entries, while the first jump can go directly to the return.
	fn := &bytecode.FuncTemplate{
		SourceFile: "fixture.js",
		Code: makeCode(
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpJmp, operand: 4}, // pc 0 -> pc 8
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpNop, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpJmp, operand: 4}, // pc 8 -> pc 16
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpReturnUndef, operand: 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{op: bytecode.OpReturnUndef, operand: 0},
		),
	}
	mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}
	stats, err := bytecode.OptimizeModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ThreadedJumps == 0 {
		t.Fatal("expected at least one threaded jump")
	}
	op, operand, _ := bytecode.Decode(fn.Code, 0)
	if op != bytecode.OpJmp || bytecode.SignedOperand(operand) != 8 {
		t.Fatalf("first jump = %s/%d, want JMP/8", op, bytecode.SignedOperand(operand))
	}
}

func TestValidateRejectsMisalignedJump(t *testing.T) {
	fn := &bytecode.FuncTemplate{
		SourceFile: "bad.js",
		Code: makeCode(struct {
			op      bytecode.Opcode
			operand uint32
		}{op: bytecode.OpJmp, operand: 1}),
	}
	if err := bytecode.ValidateModule(&bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}); err == nil {
		t.Fatal("expected misaligned jump to fail validation")
	}
}

// === 常量折叠 / 不可达删除 / store-load 消除（meta 驱动的扩展 pass）===

type ci struct {
	op      bytecode.Opcode
	operand uint32
}

func mkCode(ops ...ci) []byte {
	code := make([]byte, 0, len(ops)*bytecode.InstrSize)
	for _, in := range ops {
		bytecode.Encode(&code, in.op, in.operand)
	}
	return code
}

func optimizeFixture(t *testing.T, fn *bytecode.FuncTemplate) bytecode.OptimizationStats {
	t.Helper()
	mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}
	stats, err := bytecode.OptimizeModule(mod)
	if err != nil {
		t.Fatalf("OptimizeModule: %v", err)
	}
	return stats
}

func decodeAt(t *testing.T, code []byte, idx int) (bytecode.Opcode, uint32) {
	t.Helper()
	op, operand, _ := bytecode.Decode(code, idx*bytecode.InstrSize)
	return op, operand
}

// foldedNumberAt 读取第 idx 条指令的数值，统一处理 PUSH_INT/PUSH_NEG_INT/
// PUSH_CONST 三种编码——因为 numberPush 会按 24 位范围选择最紧凑的形态，
// 断言折叠结果时不应耦合于具体编码。
func foldedNumberAt(t *testing.T, fn *bytecode.FuncTemplate, idx int) (float64, bytecode.Opcode) {
	t.Helper()
	op, operand := decodeAt(t, fn.Code, idx)
	switch op {
	case bytecode.OpPushInt:
		return float64(operand), op
	case bytecode.OpPushNegInt:
		return -float64(operand), op
	case bytecode.OpPushConst:
		f, _ := fn.Constants[operand].Float()
		return f, op
	}
	t.Fatalf("instruction %d is %s, not a number push", idx, op)
	return 0, op
}

func TestOptimizeFoldsNumberArithmetic(t *testing.T) {
	fn := &bytecode.FuncTemplate{
		SourceFile: "fold.js",
		Constants:  []engine.Value{engine.Number(1), engine.Number(2)},
		Code: mkCode(
			ci{op: bytecode.OpPushConst, operand: 0},
			ci{op: bytecode.OpPushConst, operand: 1},
			ci{op: bytecode.OpAdd},
			ci{op: bytecode.OpReturn},
		),
	}
	stats := optimizeFixture(t, fn)
	if stats.RemovedInstructions != 2 {
		t.Fatalf("removed = %d, want 2", stats.RemovedInstructions)
	}
	if got := len(fn.Code) / bytecode.InstrSize; got != 2 {
		t.Fatalf("instruction count = %d, want 2", got)
	}
	// 结果 3 落在 24 位范围，numberPush 选 PUSH_INT（更紧凑，无常量池）。
	value, op := foldedNumberAt(t, fn, 0)
	if op != bytecode.OpPushInt {
		t.Fatalf("folded instruction = %s, want PUSH_INT (3 fits 24-bit)", op)
	}
	if value != 3 {
		t.Fatalf("folded value = %v, want 3", value)
	}
}

func TestOptimizeFoldsNumberEdgeCases(t *testing.T) {
	// IEEE754 语义保真：0/0=NaN、1/0=+Inf、-0+-0=-0、5%2=1、2**10=1024。
	cases := []struct {
		name string
		ops  []ci
		want float64
	}{
		{"div zero", []ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpDiv}}, math.NaN()},
		{"div by zero", []ci{{op: bytecode.OpPushConst, operand: 2}, {op: bytecode.OpPushConst, operand: 3}, {op: bytecode.OpDiv}}, math.Inf(1)},
		{"neg zero add", []ci{{op: bytecode.OpPushConst, operand: 4}, {op: bytecode.OpPushConst, operand: 5}, {op: bytecode.OpAdd}}, math.Copysign(0, -1)},
		{"mod", []ci{{op: bytecode.OpPushConst, operand: 6}, {op: bytecode.OpPushConst, operand: 7}, {op: bytecode.OpMod}}, 1},
		{"pow", []ci{{op: bytecode.OpPushConst, operand: 8}, {op: bytecode.OpPushConst, operand: 9}, {op: bytecode.OpPow}}, 1024},
	}
	// 注意：Go 的 untyped 常量 -0.0 数学值为 0（无负零），
	// 必须用 math.Copysign 在运行时构造 -0。
	negZero := math.Copysign(0, -1)
	consts := []engine.Value{
		engine.Number(0), engine.Number(0), // 0/0
		engine.Number(1), engine.Number(0), // 1/0
		engine.Number(negZero), engine.Number(negZero), // -0 + -0
		engine.Number(5), engine.Number(2), // 5 % 2
		engine.Number(2), engine.Number(10), // 2 ** 10
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{
				SourceFile: "fold.js",
				Constants:  consts,
				Code:       mkCode(append(c.ops, ci{op: bytecode.OpReturn})...),
			}
			optimizeFixture(t, fn)
			// 整数结果（mod=1、pow=1024）会发 PUSH_INT；NaN/Inf/-0 发 PUSH_CONST。
			// foldedNumberAt 统一读取，断言不耦合于具体编码。
			got, _ := foldedNumberAt(t, fn, 0)
			switch {
			case math.IsNaN(c.want):
				if !math.IsNaN(got) {
					t.Fatalf("folded = %v, want NaN", got)
				}
			case math.IsInf(c.want, 1):
				if !math.IsInf(got, 1) {
					t.Fatalf("folded = %v, want +Inf", got)
				}
			case c.want == 0 && math.Signbit(c.want):
				if got != 0 || !math.Signbit(got) {
					t.Fatalf("folded = %v, want -0", got)
				}
			default:
				if got != c.want {
					t.Fatalf("folded = %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestOptimizeFoldsStringConcat(t *testing.T) {
	fn := &bytecode.FuncTemplate{
		SourceFile: "fold.js",
		Constants:  []engine.Value{engine.Str("he"), engine.Str("llo")},
		Code: mkCode(
			ci{op: bytecode.OpPushConst, operand: 0},
			ci{op: bytecode.OpPushConst, operand: 1},
			ci{op: bytecode.OpAdd},
			ci{op: bytecode.OpReturn},
		),
	}
	optimizeFixture(t, fn)
	op, operand := decodeAt(t, fn.Code, 0)
	if op != bytecode.OpPushConst {
		t.Fatalf("folded = %s, want PUSH_CONST", op)
	}
	if got := fn.Constants[operand].String(); got != "hello" {
		t.Fatalf("folded constant = %q, want \"hello\"", got)
	}
}

func TestOptimizeFoldsBigInt(t *testing.T) {
	// 5n + 3n、5n / 2n（截断）、-5n % 2n（截断余数，与被除数同号）。
	cases := []struct {
		name string
		ops  []ci
		want string
	}{
		{"add", []ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpAdd}}, "8"},
		{"div trunc", []ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 2}, {op: bytecode.OpDiv}}, "2"},
		{"mod trunc", []ci{{op: bytecode.OpPushConst, operand: 3}, {op: bytecode.OpPushConst, operand: 2}, {op: bytecode.OpMod}}, "-1"},
	}
	consts := []engine.Value{
		engine.BigInt(big.NewInt(5)), engine.BigInt(big.NewInt(3)),
		engine.BigInt(big.NewInt(2)),
		engine.BigInt(big.NewInt(-5)),
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{
				SourceFile: "fold.js",
				Constants:  consts,
				Code:       mkCode(append(c.ops, ci{op: bytecode.OpReturn})...),
			}
			optimizeFixture(t, fn)
			op, operand := decodeAt(t, fn.Code, 0)
			if op != bytecode.OpPushConst {
				t.Fatalf("folded = %s, want PUSH_CONST", op)
			}
			bi, ok := engine.BigIntValue(fn.Constants[operand])
			if !ok {
				t.Fatalf("folded constant is not BigInt: %v", fn.Constants[operand].Type())
			}
			if got := bi.String(); got != c.want {
				t.Fatalf("folded = %s, want %s", got, c.want)
			}
		})
	}
}

func TestOptimizeDoesNotFoldUnsafe(t *testing.T) {
	// 以下情形折叠会改变语义，必须保持原样：
	//   - BigInt 除零（JS 抛 RangeError）
	//   - BigInt 幂（负指数抛错；结果可能过大）
	//   - 混合类型 string + number（ToString 语义）
	//   - 常量比较/位运算（宽松相等与 ToInt32 转换语义）
	cases := []struct {
		name   string
		consts []engine.Value
		ops    []ci
	}{
		{"bigint div zero", []engine.Value{engine.BigInt(big.NewInt(1)), engine.BigInt(big.NewInt(0))},
			[]ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpDiv}}},
		{"bigint pow", []engine.Value{engine.BigInt(big.NewInt(2)), engine.BigInt(big.NewInt(3))},
			[]ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpPow}}},
		{"mixed string number", []engine.Value{engine.Number(1), engine.Str("x")},
			[]ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpAdd}}},
		{"strict eq", []engine.Value{engine.Number(1), engine.Number(2)},
			[]ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpStrictEq}}},
		{"bit and", []engine.Value{engine.Number(1), engine.Number(2)},
			[]ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpPushConst, operand: 1}, {op: bytecode.OpBitAnd}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{
				SourceFile: "fold.js",
				Constants:  c.consts,
				Code:       mkCode(append(c.ops, ci{op: bytecode.OpReturn})...),
			}
			stats := optimizeFixture(t, fn)
			if got := len(fn.Code) / bytecode.InstrSize; got != len(c.ops)+1 {
				t.Fatalf("instruction count = %d, want %d (no folding)", got, len(c.ops)+1)
			}
			if stats.RemovedInstructions != 0 {
				t.Fatalf("removed = %d, want 0 (no folding)", stats.RemovedInstructions)
			}
		})
	}
}

func TestOptimizeRemovesDeadCodeAfterTerminal(t *testing.T) {
	// RETURN 之后的隐式 return_undef 等死代码被删除。
	fn := &bytecode.FuncTemplate{
		SourceFile: "dead.js",
		Code: mkCode(
			ci{op: bytecode.OpPushInt, operand: 1},
			ci{op: bytecode.OpReturn},
			ci{op: bytecode.OpPushInt, operand: 2},
			ci{op: bytecode.OpPop},
			ci{op: bytecode.OpReturnUndef},
		),
	}
	stats := optimizeFixture(t, fn)
	if stats.RemovedInstructions != 3 {
		t.Fatalf("removed = %d, want 3", stats.RemovedInstructions)
	}
	if got := len(fn.Code) / bytecode.InstrSize; got != 2 {
		t.Fatalf("instruction count = %d, want 2", got)
	}
}

func TestOptimizeDeadCodePreservesControlTargets(t *testing.T) {
	// JMP 从 pc0 直达 pc12（控制入口，不可删）；pc4 的 RETURN_UNDEF 是
	// terminal 本身（保留）；pc8 的 PUSH_INT 是 terminal 后非控制入口的
	// 死代码（删除）。JMP 目标同时验证跳转重定位到新位置。
	fn := &bytecode.FuncTemplate{
		SourceFile: "dead.js",
		Code: mkCode(
			ci{op: bytecode.OpJmp, operand: 8}, // pc0 -> pc12
			ci{op: bytecode.OpReturnUndef},
			ci{op: bytecode.OpPushInt, operand: 42},
			ci{op: bytecode.OpReturnUndef},
		),
	}
	stats := optimizeFixture(t, fn)
	if stats.RemovedInstructions != 1 {
		t.Fatalf("removed = %d, want 1", stats.RemovedInstructions)
	}
	if got := len(fn.Code) / bytecode.InstrSize; got != 3 {
		t.Fatalf("instruction count = %d, want 3", got)
	}
	op, _ := decodeAt(t, fn.Code, 1)
	if op != bytecode.OpReturnUndef {
		t.Fatalf("second instruction = %s, want RETURN_UNDEF", op)
	}
	// 重定位后的 JMP 指向新 pc8（原来 pc12）。
	op, operand := decodeAt(t, fn.Code, 0)
	if op != bytecode.OpJmp || bytecode.SignedOperand(operand) != 4 {
		t.Fatalf("relocated jump = %s/%d, want JMP/4", op, bytecode.SignedOperand(operand))
	}
}

func TestOptimizeFusesStoreLoad(t *testing.T) {
	// STORE_LOCAL 0; LOAD_LOCAL 0 → DUP; STORE_LOCAL 0（赋值表达式值回传）。
	fn := &bytecode.FuncTemplate{
		SourceFile: "store.js",
		NumLocals:  1,
		Code: mkCode(
			ci{op: bytecode.OpStoreLocal, operand: 0},
			ci{op: bytecode.OpLoadLocal, operand: 0},
			ci{op: bytecode.OpPop},
		),
	}
	stats := optimizeFixture(t, fn)
	if stats.FusedInstructions != 1 {
		t.Fatalf("fused = %d, want 1", stats.FusedInstructions)
	}
	op, _ := decodeAt(t, fn.Code, 0)
	if op != bytecode.OpDup {
		t.Fatalf("first instruction = %s, want DUP", op)
	}
	op, operand := decodeAt(t, fn.Code, 1)
	if op != bytecode.OpStoreLocal || operand != 0 {
		t.Fatalf("second instruction = %s/%d, want STORE_LOCAL/0", op, operand)
	}
}

func TestOptimizeFoldThenEliminateStatementValue(t *testing.T) {
	// `1 + 2;` 表达式语句：第一轮折叠为 PUSH_CONST(3); POP，第二轮按
	// 纯 push-pop 对完全消除（多轮迭代）。
	fn := &bytecode.FuncTemplate{
		SourceFile: "fold.js",
		Constants:  []engine.Value{engine.Number(1), engine.Number(2)},
		Code: mkCode(
			ci{op: bytecode.OpPushConst, operand: 0},
			ci{op: bytecode.OpPushConst, operand: 1},
			ci{op: bytecode.OpAdd},
			ci{op: bytecode.OpPop},
			ci{op: bytecode.OpReturnUndef},
		),
	}
	stats := optimizeFixture(t, fn)
	if stats.RemovedInstructions != 4 {
		t.Fatalf("removed = %d, want 4", stats.RemovedInstructions)
	}
	if got := len(fn.Code) / bytecode.InstrSize; got != 1 {
		t.Fatalf("instruction count = %d, want 1 (only RETURN_UNDEF)", got)
	}
}

// === PUSH_INT/PUSH_NEG_INT 常量折叠 + 单目折叠（Tier 1.1/1.2 扩展）===

// TestOptimizeFoldsPushIntBinary 验证 PUSH_INT;PUSH_INT;binop 折叠为单条
// PUSH_INT（结果落在 24 位正整数范围）。覆盖 + - * / % ** 六种算术。
func TestOptimizeFoldsPushIntBinary(t *testing.T) {
	cases := []struct {
		name      string
		a, b      uint32
		op        bytecode.Opcode
		want      uint32
		wantInstr int // 折叠后指令数（仅 push 产物）
	}{
		{"add", 1, 2, bytecode.OpAdd, 3, 1},
		{"sub", 10, 3, bytecode.OpSub, 7, 1},
		{"mul", 6, 7, bytecode.OpMul, 42, 1},
		{"div", 8, 2, bytecode.OpDiv, 4, 1},
		{"mod", 17, 5, bytecode.OpMod, 2, 1},
		{"pow", 2, 10, bytecode.OpPow, 1024, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{
				SourceFile: "fold.js",
				Code: mkCode(
					ci{op: bytecode.OpPushInt, operand: c.a},
					ci{op: bytecode.OpPushInt, operand: c.b},
					ci{op: c.op},
				),
			}
			optimizeFixture(t, fn)
			if got := len(fn.Code) / bytecode.InstrSize; got != c.wantInstr {
				t.Fatalf("instruction count = %d, want %d", got, c.wantInstr)
			}
			op, operand := decodeAt(t, fn.Code, 0)
			if op != bytecode.OpPushInt {
				t.Fatalf("folded = %s, want PUSH_INT", op)
			}
			if operand != c.want {
				t.Fatalf("folded operand = %d, want %d", operand, c.want)
			}
		})
	}
}

// TestOptimizeFoldsPushIntResultShape 验证折叠结果按 24 位范围回发为
// PUSH_INT（正）/ PUSH_NEG_INT（负）/ PUSH_CONST（超出范围）。
func TestOptimizeFoldsPushIntResultShape(t *testing.T) {
	cases := []struct {
		name string
		ops  []ci
		// wantOp 为折叠产物的 opcode；wantConst 校验 PUSH_CONST 路径的数值。
		wantOp       bytecode.Opcode
		wantOperand  uint32
		wantConstVal float64
	}{
		// 3 - 5 = -2 → PUSH_NEG_INT 2
		{"neg result", []ci{{op: bytecode.OpPushInt, operand: 3}, {op: bytecode.OpPushInt, operand: 5}, {op: bytecode.OpSub}},
			bytecode.OpPushNegInt, 2, 0},
		// 0 - 7 = -7 → PUSH_NEG_INT 7
		{"neg result 2", []ci{{op: bytecode.OpPushInt, operand: 0}, {op: bytecode.OpPushInt, operand: 7}, {op: bytecode.OpSub}},
			bytecode.OpPushNegInt, 7, 0},
		// 100000 * 100000 = 10^10（≥2^24）→ PUSH_CONST(1e10)
		{"overflow to const", []ci{{op: bytecode.OpPushInt, operand: 100000}, {op: bytecode.OpPushInt, operand: 100000}, {op: bytecode.OpMul}},
			bytecode.OpPushConst, 0, 1e10},
		// 5 / 0 = +Inf → PUSH_CONST(+Inf)
		{"div by zero inf", []ci{{op: bytecode.OpPushInt, operand: 5}, {op: bytecode.OpPushInt, operand: 0}, {op: bytecode.OpDiv}},
			bytecode.OpPushConst, 0, math.Inf(1)},
		// 0 / 0 = NaN → PUSH_CONST(NaN)
		{"nan", []ci{{op: bytecode.OpPushInt, operand: 0}, {op: bytecode.OpPushInt, operand: 0}, {op: bytecode.OpDiv}},
			bytecode.OpPushConst, 0, math.NaN()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{SourceFile: "fold.js", Code: mkCode(c.ops...)}
			optimizeFixture(t, fn)
			op, operand := decodeAt(t, fn.Code, 0)
			if op != c.wantOp {
				t.Fatalf("folded = %s, want %s", op, c.wantOp)
			}
			if c.wantOp == bytecode.OpPushInt || c.wantOp == bytecode.OpPushNegInt {
				if operand != c.wantOperand {
					t.Fatalf("operand = %d, want %d", operand, c.wantOperand)
				}
				return
			}
			// PUSH_CONST 路径：校验常量池数值。
			got, _ := fn.Constants[operand].Float()
			switch {
			case math.IsNaN(c.wantConstVal):
				if !math.IsNaN(got) {
					t.Fatalf("const = %v, want NaN", got)
				}
			case math.IsInf(c.wantConstVal, 1):
				if !math.IsInf(got, 1) {
					t.Fatalf("const = %v, want +Inf", got)
				}
			default:
				if got != c.wantConstVal {
					t.Fatalf("const = %v, want %v", got, c.wantConstVal)
				}
			}
		})
	}
}

// TestOptimizeFoldsMixedPushIntConst 验证 PUSH_INT 与 PUSH_CONST(number)
// 混搭也能折叠（pushNumber 统一处理三种数值压栈）。
func TestOptimizeFoldsMixedPushIntConst(t *testing.T) {
	fn := &bytecode.FuncTemplate{
		SourceFile: "fold.js",
		Constants:  []engine.Value{engine.Number(100)}, // 常量池：100
		Code: mkCode(
			ci{op: bytecode.OpPushInt, operand: 1},     // 1
			ci{op: bytecode.OpPushConst, operand: 0},   // 100
			ci{op: bytecode.OpAdd},                      // → 101
		),
	}
	optimizeFixture(t, fn)
	if got := len(fn.Code) / bytecode.InstrSize; got != 1 {
		t.Fatalf("instruction count = %d, want 1", got)
	}
	op, operand := decodeAt(t, fn.Code, 0)
	if op != bytecode.OpPushInt || operand != 101 {
		t.Fatalf("folded = %s %d, want PUSH_INT 101", op, operand)
	}
}

// TestOptimizeFoldsUnary 验证单目折叠：NEG/NOT/BitNot/UnaryPlus。
func TestOptimizeFoldsUnary(t *testing.T) {
	cases := []struct {
		name   string
		ops    []ci
		wantOp bytecode.Opcode
		// wantOperand 用于 PUSH_INT/PUSH_NEG_INT；wantConst 用于 PUSH_CONST(bigint 取反)。
		wantOperand  uint32
		wantConstStr string
	}{
		{"neg int", []ci{{op: bytecode.OpPushInt, operand: 5}, {op: bytecode.OpNeg}}, bytecode.OpPushNegInt, 5, ""},
		{"not true", []ci{{op: bytecode.OpPushTrue}, {op: bytecode.OpNot}}, bytecode.OpPushFalse, 0, ""},
		{"not false", []ci{{op: bytecode.OpPushFalse}, {op: bytecode.OpNot}}, bytecode.OpPushTrue, 0, ""},
		{"bitnot 5", []ci{{op: bytecode.OpPushInt, operand: 5}, {op: bytecode.OpBitNot}}, bytecode.OpPushNegInt, 6, ""}, // ~5=-6
		{"bitnot 0", []ci{{op: bytecode.OpPushInt, operand: 0}, {op: bytecode.OpBitNot}}, bytecode.OpPushNegInt, 1, ""}, // ~0=-1
		{"unary plus", []ci{{op: bytecode.OpPushInt, operand: 7}, {op: bytecode.OpUnaryPlus}}, bytecode.OpPushInt, 7, ""},
		{"neg bigint", []ci{{op: bytecode.OpPushConst, operand: 0}, {op: bytecode.OpNeg}}, bytecode.OpPushConst, 0, "-5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{
				SourceFile: "fold.js",
				Constants:  []engine.Value{engine.BigInt(big.NewInt(5))},
				Code:       mkCode(c.ops...),
			}
			optimizeFixture(t, fn)
			if got := len(fn.Code) / bytecode.InstrSize; got != 1 {
				t.Fatalf("instruction count = %d, want 1", got)
			}
			op, operand := decodeAt(t, fn.Code, 0)
			if op != c.wantOp {
				t.Fatalf("folded = %s, want %s", op, c.wantOp)
			}
			if c.wantOp == bytecode.OpPushConst {
				bi, ok := engine.BigIntValue(fn.Constants[operand])
				if !ok || bi.String() != c.wantConstStr {
					t.Fatalf("const = %v, want bigint %s", fn.Constants[operand], c.wantConstStr)
				}
				return
			}
			if operand != c.wantOperand {
				t.Fatalf("operand = %d, want %d", operand, c.wantOperand)
			}
		})
	}
}

// TestOptimizeFoldPreservesNegZero 验证折叠保留 IEEE754 负零：
// JS 中 -0 ≠ +0（1/-0 === -Inf、Object.is(-0,0)===false），故 NEG(0) 与
// (-0)+(-0) 等产生 -0 的折叠必须经 PUSH_CONST 保留符号位，不能发 PUSH_INT 0。
func TestOptimizeFoldPreservesNegZero(t *testing.T) {
	cases := []struct {
		name string
		ops  []ci
	}{
		// NEG(0) → -0（JS 中 1/-0 === -Inf）。注：0-0=+0、-0+-0=-0 但后者需 -0 操作数。
		{"neg of zero", []ci{{op: bytecode.OpPushInt, operand: 0}, {op: bytecode.OpNeg}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{SourceFile: "fold.js", Code: mkCode(c.ops...)}
			optimizeFixture(t, fn)
			if got := len(fn.Code) / bytecode.InstrSize; got != 1 {
				t.Fatalf("instruction count = %d, want 1", got)
			}
			op, operand := decodeAt(t, fn.Code, 0)
			if op != bytecode.OpPushConst {
				t.Fatalf("folded = %s, want PUSH_CONST (preserve -0)", op)
			}
			v, _ := fn.Constants[operand].Float()
			if v != 0 || !math.Signbit(v) {
				t.Fatalf("folded value = %v (%v signbit), want -0", v, math.Signbit(v))
			}
		})
	}
}

// TestOptimizeDoesNotFoldPushIntUnsafe 验证 PUSH_INT 上不可折叠的算术形态
// 保持原样：位运算（涉及 ToInt32 两步转换但保留双压栈语义——本优化器当前
// 不折位运算）、比较运算（结果布尔，当前不折）。
func TestOptimizeDoesNotFoldPushIntUnsafe(t *testing.T) {
	cases := []struct {
		name string
		ops  []ci
	}{
		{"shl", []ci{{op: bytecode.OpPushInt, operand: 1}, {op: bytecode.OpPushInt, operand: 3}, {op: bytecode.OpShl}}},
		{"bitand", []ci{{op: bytecode.OpPushInt, operand: 1}, {op: bytecode.OpPushInt, operand: 2}, {op: bytecode.OpBitAnd}}},
		{"lt", []ci{{op: bytecode.OpPushInt, operand: 1}, {op: bytecode.OpPushInt, operand: 2}, {op: bytecode.OpLt}}},
		{"stricteq", []ci{{op: bytecode.OpPushInt, operand: 1}, {op: bytecode.OpPushInt, operand: 2}, {op: bytecode.OpStrictEq}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &bytecode.FuncTemplate{SourceFile: "fold.js", Code: mkCode(c.ops...)}
			stats := optimizeFixture(t, fn)
			if got := len(fn.Code) / bytecode.InstrSize; got != len(c.ops) {
				t.Fatalf("instruction count = %d, want %d (no folding)", got, len(c.ops))
			}
			if stats.RemovedInstructions != 0 {
				t.Fatalf("removed = %d, want 0 (no folding)", stats.RemovedInstructions)
			}
		})
	}
}
