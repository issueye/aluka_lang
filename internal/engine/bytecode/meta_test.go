package bytecode_test

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// 一致性断言：集中式元数据表（meta.go）与历史手写表/既有语义必须完全一致，
// 防止新增指令时漏登记元数据或登记错误。

func TestMetaEveryOpcodeRegistered(t *testing.T) {
	for op := bytecode.Opcode(0); op <= bytecode.OpEnd; op++ {
		if bytecode.Meta(op) == nil {
			t.Fatalf("opcode %d has no metadata entry; register it in meta.go", op)
		}
		if bytecode.Meta(op).Name == "" {
			t.Fatalf("opcode %d has empty name", op)
		}
	}
}

func TestMetaStringMatchesHistoricalNames(t *testing.T) {
	// 关键指令名称抽查（历史 opNames 的权威值）。
	names := map[bytecode.Opcode]string{
		bytecode.OpNop:                "NOP",
		bytecode.OpPushUndefined:      "PUSH_UNDEFINED",
		bytecode.OpPushNegInt:         "PUSH_NEG_INT",
		bytecode.OpLoadUpvalue:        "LOAD_UPVALUE",
		bytecode.OpUShr:               "USHR",
		bytecode.OpStrictNe:           "STRICT_NE",
		bytecode.OpJmpNullishKeep:     "JMP_NULLISH_KEEP",
		bytecode.OpCallMethod:         "CALL_METHOD",
		bytecode.OpReturnUndef:        "RETURN_UNDEF",
		bytecode.OpGetPropLocal:       "GET_PROP_LOCAL",
		bytecode.OpSetPropComputedObj: "SET_PROP_COMPUTED_OBJ",
		bytecode.OpCallArgs:           "CALL_ARGS",
		bytecode.OpTypeofGlobal:       "TYPEOF_GLOBAL",
		bytecode.OpTryExitJmp:         "TRY_EXIT_JMP",
		bytecode.OpInstanceof:         "INSTANCEOF",
		bytecode.OpForInNext:          "FOR_IN_NEXT",
		bytecode.OpConstructThis:      "CONSTRUCT_THIS",
		bytecode.OpGetAsyncIterator:   "GET_ASYNC_ITERATOR",
		bytecode.OpMakeRegexp:         "MAKE_REGEXP",
		bytecode.OpCloseUpvalues:      "CLOSE_UPVALUES",
		bytecode.OpDec:                "DEC",
		bytecode.OpEnd:                "END",
	}
	for op, want := range names {
		if got := op.String(); got != want {
			t.Errorf("%s: String() = %q, want %q", want, got, want)
		}
	}
	if bytecode.Opcode(250).String() != "OP_UNKNOWN" {
		t.Errorf("unregistered opcode String() should be OP_UNKNOWN")
	}
}

func TestMetaIsJumpMatchesHistoricalSet(t *testing.T) {
	historical := map[bytecode.Opcode]bool{
		bytecode.OpJmp:            true,
		bytecode.OpJmpTruePop:     true,
		bytecode.OpJmpFalsePop:    true,
		bytecode.OpJmpTrueKeep:    true,
		bytecode.OpJmpFalseKeep:   true,
		bytecode.OpJmpNullishKeep: true,
		bytecode.OpOptionalJump:   true,
		bytecode.OpForInNext:      true,
		bytecode.OpTryExitJmp:     true,
	}
	for op := bytecode.Opcode(0); op <= bytecode.OpEnd; op++ {
		m := bytecode.Meta(op)
		if m == nil {
			continue
		}
		if m.IsJump != historical[op] {
			t.Errorf("opcode %s IsJump = %v, want %v", op, m.IsJump, historical[op])
		}
	}
}

func TestMetaPurePushMatchesHistoricalSet(t *testing.T) {
	historical := map[bytecode.Opcode]bool{
		bytecode.OpPushUndefined: true,
		bytecode.OpPushNull:      true,
		bytecode.OpPushTrue:      true,
		bytecode.OpPushFalse:     true,
		bytecode.OpPushConst:     true,
		bytecode.OpPushInt:       true,
		bytecode.OpPushNegInt:    true,
	}
	for op := bytecode.Opcode(0); op <= bytecode.OpEnd; op++ {
		m := bytecode.Meta(op)
		if m == nil {
			continue
		}
		if m.PurePush != historical[op] {
			t.Errorf("opcode %s PurePush = %v, want %v", op, m.PurePush, historical[op])
		}
	}
}

func TestMetaOperandKindDrivesHasOperand(t *testing.T) {
	// HasOperand 必须与 OperandKind 一致：None → false，其余 → true。
	for op := bytecode.Opcode(0); op <= bytecode.OpEnd; op++ {
		m := bytecode.Meta(op)
		if m == nil {
			continue
		}
		want := m.Operand != bytecode.OperandNone
		if got := op.HasOperand(); got != want {
			t.Errorf("opcode %s HasOperand = %v, want %v (OperandKind %d)", op, got, want, m.Operand)
		}
	}
}

func TestMetaStackEffect(t *testing.T) {
	cases := []struct {
		op           bytecode.Opcode
		pops, pushes uint8
		known        bool
	}{
		{bytecode.OpNop, 0, 0, true},
		{bytecode.OpPushConst, 0, 1, true},
		{bytecode.OpPop, 1, 0, true},
		{bytecode.OpDup, 0, 1, true},
		{bytecode.OpSwap, 2, 2, true},
		{bytecode.OpLoadLocal, 0, 1, true},
		{bytecode.OpStoreLocal, 1, 0, true},
		{bytecode.OpAdd, 2, 1, true},
		{bytecode.OpUShr, 2, 1, true},
		{bytecode.OpNeg, 1, 1, true},
		{bytecode.OpTypeof, 1, 1, true},
		{bytecode.OpEq, 2, 1, true},
		{bytecode.OpJmpTruePop, 1, 0, true},
		{bytecode.OpReturn, 1, 0, true},
		{bytecode.OpReturnUndef, 0, 0, true},
		{bytecode.OpGetProp, 1, 1, true},
		{bytecode.OpGetPropLocal, 0, 1, true},
		{bytecode.OpSetProp, 2, 1, true},
		{bytecode.OpSetPropObj, 1, 0, true}, // 弹 value，保留 obj（对象字面量连续 set）
		{bytecode.OpSetElem, 3, 1, true},
		{bytecode.OpSetElemTop, 3, 0, true},
		{bytecode.OpDelProp, 1, 1, true},
		{bytecode.OpDelElem, 2, 1, true},
		{bytecode.OpBuildArray, 0, 1, true},
		{bytecode.OpArrayPush, 1, 0, true},
		{bytecode.OpThrow, 1, 0, true},
		{bytecode.OpInstanceof, 2, 1, true},
		{bytecode.OpGetProto, 1, 1, true},
		{bytecode.OpGetIterator, 1, 1, true},
		{bytecode.OpGetAsyncIterator, 1, 1, true},
		{bytecode.OpYield, 1, 0, true},
		{bytecode.OpAwait, 1, 1, true},
		{bytecode.OpMakeRegexp, 2, 1, true},
		{bytecode.OpSetGetterObj, 1, 0, true},
		{bytecode.OpSetGetterComputedObj, 2, 0, true},
		{bytecode.OpSetSetterComputedObj, 2, 0, true},
		{bytecode.OpCloseUpvalues, 0, 0, true},
		{bytecode.OpInc, 1, 1, true},
		{bytecode.OpDec, 1, 1, true},
		// 条件/变长栈效果：known=false。
		{bytecode.OpJmpTrueKeep, 0, 0, false},
		{bytecode.OpJmpFalseKeep, 0, 0, false},
		{bytecode.OpJmpNullishKeep, 0, 0, false},
		{bytecode.OpOptionalJump, 0, 0, false},
		{bytecode.OpCall, 0, 0, false},
		{bytecode.OpCallMethod, 0, 0, false},
		{bytecode.OpCallWithThisArgs, 0, 0, false},
		{bytecode.OpNew, 0, 0, false},
		{bytecode.OpNewObject, 0, 0, false},
		{bytecode.OpNewArray, 0, 0, false},
		{bytecode.OpCallArgs, 0, 0, false},
		{bytecode.OpForInNext, 0, 0, false},
		{bytecode.OpMakeClass, 0, 0, false},
		{bytecode.OpCallThis, 0, 0, false},
		{bytecode.OpConstructThisArgs, 0, 0, false},
	}
	for _, c := range cases {
		m := bytecode.Meta(c.op)
		if m == nil {
			t.Fatalf("opcode %d has no metadata", c.op)
		}
		pops, pushes, known := m.StackEffect()
		if known != c.known || pops != c.pops || pushes != c.pushes {
			t.Errorf("opcode %s StackEffect = (%d,%d,%v), want (%d,%d,%v)",
				c.op, pops, pushes, known, c.pops, c.pushes, c.known)
		}
	}
}

func TestMetaIsTerminal(t *testing.T) {
	terminal := []bytecode.Opcode{
		bytecode.OpReturn,
		bytecode.OpReturnUndef,
		bytecode.OpThrow,
	}
	for _, op := range terminal {
		if m := bytecode.Meta(op); m == nil || !m.IsTerminal {
			t.Errorf("opcode %s must be IsTerminal", op)
		}
	}
	// 非终端的代表性指令。
	nonTerminal := []bytecode.Opcode{
		bytecode.OpNop, bytecode.OpAdd, bytecode.OpJmp, bytecode.OpCall, bytecode.OpTryExitJmp,
	}
	for _, op := range nonTerminal {
		if m := bytecode.Meta(op); m != nil && m.IsTerminal {
			t.Errorf("opcode %s must not be IsTerminal", op)
		}
	}
}

// 操作数范围校验：validateOperand 通过 ValidateModule 暴露。
func TestValidateOperandRanges(t *testing.T) {
	mk := func(ops ...struct {
		op      bytecode.Opcode
		operand uint32
	}) *bytecode.FuncTemplate {
		code := make([]byte, 0, len(ops)*bytecode.InstrSize)
		for _, in := range ops {
			bytecode.Encode(&code, in.op, in.operand)
		}
		return &bytecode.FuncTemplate{SourceFile: "bad.js", Code: code}
	}
	encode := func(op bytecode.Opcode, operand uint32) struct {
		op      bytecode.Opcode
		operand uint32
	} {
		return struct {
			op      bytecode.Opcode
			operand uint32
		}{op, operand}
	}
	cases := []struct {
		name string
		fn   *bytecode.FuncTemplate
		ok   bool
	}{
		{"const index out of range", mk(encode(bytecode.OpPushConst, 3)), false},
		{"const index in range", &bytecode.FuncTemplate{
			SourceFile: "ok.js",
			Constants:  []engine.Value{engine.Number(1), engine.Number(2)},
			Code:       mk(encode(bytecode.OpPushConst, 1)).Code,
		}, true},
		{"local slot out of range", mk(encode(bytecode.OpLoadLocal, 4)), false},
		{"upvalue out of range", mk(encode(bytecode.OpLoadUpvalue, 1)), false},
		{"packed slot out of range", mk(encode(bytecode.OpGetPropLocal, 1<<16)), false},
		{"packed name out of range", mk(encode(bytecode.OpGetPropLocal, 5)), false},
		{"packed call name out of range", mk(encode(bytecode.OpCallMethod, 3)), false},
		{"valid packed", &bytecode.FuncTemplate{
			SourceFile: "ok.js",
			NumLocals:  2,
			Constants:  []engine.Value{engine.Number(0)},
			Code:       mk(encode(bytecode.OpGetPropLocal, 1<<16|0)).Code,
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mod := &bytecode.Module{Functions: []*bytecode.FuncTemplate{c.fn}}
			err := bytecode.ValidateModule(mod)
			if c.ok && err != nil {
				t.Fatalf("expected valid module, got: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
