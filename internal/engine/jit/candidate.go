package jit

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// This file implements the R3-7 compile-time candidate filter: a cheap static
// scan over the bytecode that rejects unsupported shapes BEFORE the full
// lowering / verification work of CompileLeaf / CompileTrace runs. The bridge
// calls the exported RejectLeafReason / RejectTraceReason on every hot-path
// candidate so a loop whose body contains, say, a try/catch region or an
// unsupported opcode is rejected once per (template, backedge) and the
// structured rejection cache (state.rejected + reason in jit_bridge.go) then
// serves every subsequent backedge without re-entering the compilers.
//
// The scan mirrors the static checks of the compilers exactly, so the error
// messages are identical whether the rejection comes from the pre-filter or
// from the lowering itself. Verify-time failures (inconsistent stack depths,
// invalid deopt maps) are deliberately NOT part of the scan: they are dynamic
// and must keep flowing through the verifier.

// RejectLeafReason reports why tmpl cannot be compiled by CompileLeaf, or nil
// when the template passes the static candidate filter. It never mutates the
// template and never allocates per-opcode IR, so hot-path callers can run it
// before every compile attempt.
func RejectLeafReason(tmpl *bytecode.FuncTemplate) error {
	return rejectLeafCandidate(tmpl)
}

// RejectTraceReason reports why the bytecode range [startPC, backedgePC]
// cannot be compiled by CompileTraceWithGuards, or nil when the range passes
// the static candidate filter. Call-guard / method-guard requirements are
// decided at compile time (guards are supplied by the bridge), so OpCall and
// OpCallMethod are not filtered here.
func RejectTraceReason(tmpl *bytecode.FuncTemplate, startPC, backedgePC int) error {
	return rejectTraceCandidate(tmpl, startPC, backedgePC)
}

func rejectLeafCandidate(tmpl *bytecode.FuncTemplate) error {
	if tmpl == nil || tmpl.IsAsync || tmpl.IsGenerator || tmpl.IsVarArgs ||
		(tmpl.ArgumentsSlot >= 0 && !tmpl.NoArgumentsObject) {
		return fmt.Errorf("jit: function is not a leaf candidate")
	}
	if len(tmpl.Code) == 0 || len(tmpl.Code)%bytecode.InstrSize != 0 {
		return fmt.Errorf("jit: malformed bytecode")
	}
	if tmpl.NumLocals > maxQuickSlots {
		return fmt.Errorf("jit: too many locals")
	}
	upvalue := -1
	for pc := 0; pc < len(tmpl.Code); pc += bytecode.InstrSize {
		op := bytecode.Opcode(tmpl.Code[pc])
		arg := uint32(tmpl.Code[pc+1])<<16 | uint32(tmpl.Code[pc+2])<<8 | uint32(tmpl.Code[pc+3])
		switch op {
		case bytecode.OpPushInt, bytecode.OpPushNegInt:
		case bytecode.OpPushConst:
			if int(arg) >= len(tmpl.Constants) {
				return fmt.Errorf("jit: constant index out of range")
			}
			switch tmpl.Constants[arg].Type() {
			case engine.TypeNumber, engine.TypeString:
			default:
				return fmt.Errorf("jit: non-number constant")
			}
		case bytecode.OpLoadLocal, bytecode.OpStoreLocal:
			if int(arg) >= tmpl.NumLocals {
				return fmt.Errorf("jit: local index out of range")
			}
		case bytecode.OpGetProp:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeString {
				return fmt.Errorf("jit: non-string property name")
			}
		case bytecode.OpGetPropLocal:
			slot, nameIdx := int(arg>>16), int(arg&0xFFFF)
			if slot >= tmpl.NumLocals || nameIdx >= len(tmpl.Constants) || tmpl.Constants[nameIdx].Type() != engine.TypeString {
				return fmt.Errorf("jit: invalid property-local operand")
			}
		case bytecode.OpLoadUpvalue:
			if upvalue >= 0 && upvalue != int(arg) {
				return fmt.Errorf("jit: multiple upvalues")
			}
			upvalue = int(arg)
		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow,
			bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpShl, bytecode.OpShr, bytecode.OpUShr,
			bytecode.OpEq, bytecode.OpStrictEq, bytecode.OpNe, bytecode.OpStrictNe,
			bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe,
			bytecode.OpNeg, bytecode.OpNot, bytecode.OpBitNot, bytecode.OpUnaryPlus,
			bytecode.OpPop, bytecode.OpInc, bytecode.OpDec, bytecode.OpDup, bytecode.OpSwap:
		case bytecode.OpJmp, bytecode.OpJmpTruePop, bytecode.OpJmpFalsePop,
			bytecode.OpJmpTrueKeep, bytecode.OpJmpFalseKeep, bytecode.OpJmpNullishKeep:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			if target < 0 || target >= len(tmpl.Code) || target%bytecode.InstrSize != 0 {
				return fmt.Errorf("jit: jump target %d is not lowered", target)
			}
		case bytecode.OpCall:
			if arg > 8 {
				return fmt.Errorf("jit: too many call arguments")
			}
		case bytecode.OpReturn, bytecode.OpReturnUndef:
		default:
			return fmt.Errorf("jit: unsupported opcode %s", op)
		}
	}
	last := bytecode.Opcode(tmpl.Code[len(tmpl.Code)-bytecode.InstrSize])
	if last != bytecode.OpReturn && last != bytecode.OpReturnUndef {
		return fmt.Errorf("jit: no return")
	}
	return nil
}

func rejectTraceCandidate(tmpl *bytecode.FuncTemplate, startPC, backedgePC int) error {
	if tmpl == nil || startPC < 0 || backedgePC < startPC || backedgePC+bytecode.InstrSize > len(tmpl.Code) {
		return fmt.Errorf("jit: invalid trace range")
	}
	if startPC%bytecode.InstrSize != 0 || backedgePC%bytecode.InstrSize != 0 {
		return fmt.Errorf("jit: unaligned trace range")
	}
	if tmpl.NumLocals > maxQuickSlots {
		return fmt.Errorf("jit: too many trace locals")
	}
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		op := bytecode.Opcode(tmpl.Code[pc])
		arg := uint32(tmpl.Code[pc+1])<<16 | uint32(tmpl.Code[pc+2])<<8 | uint32(tmpl.Code[pc+3])
		switch op {
		case bytecode.OpPushInt, bytecode.OpPushNegInt:
		case bytecode.OpPushConst:
			if int(arg) >= len(tmpl.Constants) {
				return fmt.Errorf("jit: trace non-number constant")
			}
			switch tmpl.Constants[arg].Type() {
			case engine.TypeNumber, engine.TypeString:
			default:
				return fmt.Errorf("jit: trace non-number constant")
			}
		case bytecode.OpLoadLocal, bytecode.OpStoreLocal:
			if int(arg) >= tmpl.NumLocals {
				return fmt.Errorf("jit: trace local out of range")
			}
		case bytecode.OpGetPropLocal:
			slot, nameIdx := int(arg>>16), int(arg&0xFFFF)
			if slot >= tmpl.NumLocals || nameIdx >= len(tmpl.Constants) || tmpl.Constants[nameIdx].Type() != engine.TypeString {
				return fmt.Errorf("jit: trace property operand")
			}
		case bytecode.OpGetProp, bytecode.OpSetPropTop:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeString {
				return fmt.Errorf("jit: trace property name")
			}
		case bytecode.OpCall, bytecode.OpCallMethod:
			// Guarded forms compile; unguarded forms are rejected at compile
			// time once the bridge supplies (or omits) the guards.
		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow,
			bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpShl, bytecode.OpShr, bytecode.OpUShr,
			bytecode.OpEq, bytecode.OpStrictEq, bytecode.OpNe, bytecode.OpStrictNe,
			bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe,
			bytecode.OpNeg, bytecode.OpNot, bytecode.OpBitNot, bytecode.OpUnaryPlus,
			bytecode.OpPop, bytecode.OpInc, bytecode.OpDec, bytecode.OpDup, bytecode.OpSwap,
			bytecode.OpJmp, bytecode.OpJmpTruePop, bytecode.OpJmpFalsePop,
			bytecode.OpJmpTrueKeep, bytecode.OpJmpFalseKeep, bytecode.OpJmpNullishKeep,
			bytecode.OpThrow:
			// OpThrow is a supported exception exit; OpTryEnter/OpTryExit are
			// deliberately absent so try/catch regions inside the trace range
			// are rejected here (CompileTrace cannot represent handler frames).
		default:
			return fmt.Errorf("jit: trace unsupported opcode %s", op)
		}
	}
	return nil
}
