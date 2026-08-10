package bytecode_test

import (
	"testing"

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
			}{bytecode.OpPushTrue, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpJmpTruePop, 8}, // 8 + (4 + 4) = old pc 16
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpPushInt, 42},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpPop, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpReturnUndef, 0},
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
		Code: makeCode(
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpLoadLocal, 7},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpGetProp, 19},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpReturn, 0},
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
			}{bytecode.OpTryEnter, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpPushInt, 1},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpPop, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpTryExit, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpReturnUndef, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpReturnUndef, 0},
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
			}{bytecode.OpJmp, 4}, // pc 0 -> pc 8
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpNop, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpJmp, 4}, // pc 8 -> pc 16
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpReturnUndef, 0},
			struct {
				op      bytecode.Opcode
				operand uint32
			}{bytecode.OpReturnUndef, 0},
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
		}{bytecode.OpJmp, 1}),
	}
	if err := bytecode.ValidateModule(&bytecode.Module{Functions: []*bytecode.FuncTemplate{fn}}); err == nil {
		t.Fatal("expected misaligned jump to fail validation")
	}
}
