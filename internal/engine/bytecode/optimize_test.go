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
	op, operand := decodeAt(t, fn.Code, 0)
	if op != bytecode.OpPushConst {
		t.Fatalf("folded instruction = %s, want PUSH_CONST", op)
	}
	if fn.Constants[operand].Type() != engine.TypeNumber {
		t.Fatalf("folded constant type = %v, want number", fn.Constants[operand].Type())
	}
	value, _ := fn.Constants[operand].Float()
	if value != 3 {
		t.Fatalf("folded constant = %v, want 3", value)
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
			op, operand := decodeAt(t, fn.Code, 0)
			if op != bytecode.OpPushConst {
				t.Fatalf("folded = %s, want PUSH_CONST", op)
			}
			got, _ := fn.Constants[operand].Float()
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
