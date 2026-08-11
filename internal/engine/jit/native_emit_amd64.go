//go:build amd64 && (windows || linux)

package jit

import (
	"encoding/binary"
	"fmt"
	"math"

	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

const (
	nativeResultOffset = 64
	nativeStatusOffset = 72
	nativeLocalsOffset = 80
	nativeBudgetOffset = 336
	nativeResumeOffset = 344
)

type nativeFixup struct {
	displacement int
	target       int
}

func compileNativeProgram(p *Program, retainDebugBytes ...bool) (*jitnative.Code, error) {
	if p == nil || p.NumParams > 8 || p.NumLocals > 32 || p.MaxStack > 8 ||
		p.nativeTrace && p.NumLocals+p.MaxStack > 32 || len(p.Code) == 0 {
		return nil, fmt.Errorf("jit: native limits exceeded")
	}
	assigned, err := nativeAssignedLocals(p)
	if err != nil {
		return nil, err
	}
	targeted := make([]bool, len(p.Code))
	for _, in := range p.Code {
		switch in.Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			if int(in.Operand) < 0 || int(in.Operand) >= len(targeted) {
				return nil, fmt.Errorf("jit: native jump target out of range")
			}
			targeted[in.Operand] = true
		}
	}

	var code []byte
	depth := 0
	offsets := make([]int, len(p.Code))
	depths := make([]int, len(p.Code))
	fixups := make([]nativeFixup, 0, 8)
	sawTerminal := false
	for i := 0; i < len(p.Code); i++ {
		offsets[i] = len(code)
		depths[i] = depth
		in := p.Code[i]
		switch in.Op {
		case OpConst:
			code = emitConstF64(code, depth, in.Value)
			depth++
		case OpLoadLocal:
			slot := int(in.Operand)
			if slot <= 0 || slot >= p.NumLocals || assigned[i]&(uint64(1)<<slot) == 0 {
				return nil, fmt.Errorf("jit: native local %d is not a proven number", slot)
			}
			code = emitLoadF64(code, depth, nativeLocalOffset(slot, p.NumParams))
			depth++
		case OpStoreLocal:
			depth--
			slot := int(in.Operand)
			if slot <= 0 || slot >= p.NumLocals {
				return nil, fmt.Errorf("jit: native local out of range")
			}
			code = emitStoreF64(code, depth, nativeLocalOffset(slot, p.NumParams))
			if p.nativeTrace {
				code = emitNativeDirtyLocal(code, slot)
			}
		case OpAdd, OpSub, OpMul, OpDiv:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			right, left := depth-1, depth-2
			code = emitBinaryF64(code, in.Op, left, right)
			depth--
		case OpNeg:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			code = emitNegF64(code, depth-1)
		case OpUnaryPlus:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
		case OpDup:
			if depth < 1 || depth >= 8 {
				return nil, fmt.Errorf("jit: native dup stack")
			}
			code = emitMoveF64(code, depth, depth-1)
			depth++
		case OpSwap:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native swap stack")
			}
			code = emitSwapF64(code, depth-2, depth-1)
		case OpPop:
			depth--
		case OpEq, OpNe, OpStrictEq, OpStrictNe, OpLt, OpLe, OpGt, OpGe:
			if depth < 2 || i+1 >= len(p.Code) || targeted[i+1] {
				return nil, fmt.Errorf("jit: native comparison is not a branch condition")
			}
			branch := p.Code[i+1]
			if branch.Op != OpJumpTrue && branch.Op != OpJumpFalse {
				return nil, fmt.Errorf("jit: native comparison result escapes")
			}
			right, left := depth-1, depth-2
			code = emitComparisonTest(code, in.Op, left, right)
			depth -= 2
			offsets[i+1] = len(code)
			condition := byte(0x85) // JNE
			if branch.Op == OpJumpFalse {
				condition = 0x84 // JE
			}
			target := int(branch.Operand)
			if target < i {
				if depth != 0 || depths[target] != 0 {
					return nil, fmt.Errorf("jit: native backedge has live operand stack")
				}
				code = append(code, 0x0F, condition^1)
				skipFixup := len(code)
				code = append(code, 0, 0, 0, 0)
				code = emitNativeBudgetPoll(code, offsets[target])
				code = append(code, 0xE9)
				fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
				code = append(code, 0, 0, 0, 0)
				relative := len(code) - (skipFixup + 4)
				binary.LittleEndian.PutUint32(code[skipFixup:skipFixup+4], uint32(int32(relative)))
			} else {
				code = append(code, 0x0F, condition)
				fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
				code = append(code, 0, 0, 0, 0)
			}
			i++
		case OpJump:
			target := int(in.Operand)
			if target < i {
				if depth != 0 || depths[target] != 0 {
					return nil, fmt.Errorf("jit: native backedge has live operand stack")
				}
				code = emitNativeBudgetPoll(code, offsets[target])
			}
			code = append(code, 0xE9)
			fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
			code = append(code, 0, 0, 0, 0)
		case OpJumpTrue, OpJumpFalse:
			return nil, fmt.Errorf("jit: native branch lacks numeric comparison")
		case OpJumpTrueKeep, OpJumpFalseKeep:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native keep branch stack underflow")
			}
			target := int(in.Operand)
			if target <= i {
				return nil, fmt.Errorf("jit: native keep branch must be forward")
			}
			code = emitNumberTruthyTest(code, depth-1)
			condition := byte(0x85) // JNE
			if in.Op == OpJumpFalseKeep {
				condition = 0x84 // JE
			}
			code = append(code, 0x0F, condition)
			fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
			code = append(code, 0, 0, 0, 0)
			depth--
		case OpJumpNullishKeep:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native nullish branch stack underflow")
			}
			target := int(in.Operand)
			if target <= i {
				return nil, fmt.Errorf("jit: native nullish branch must be forward")
			}
			// Native inputs are guarded Numbers, so the left side is never
			// nullish and the fallback expression is unreachable.
			code = append(code, 0xE9)
			fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
			code = append(code, 0, 0, 0, 0)
			depth--
		case OpReturn:
			if depth != 1 {
				return nil, fmt.Errorf("jit: native return stack")
			}
			code = emitStoreF64(code, 0, nativeResultOffset)
			code = append(code, 0x31, 0xC0, 0xC3) // XOR EAX,EAX; RET
			depth--
			sawTerminal = true
		case OpTraceExit:
			exitID := int(in.Operand)
			if !p.nativeTrace || in.Operand > ^uint32(0)-3 {
				return nil, fmt.Errorf("jit: invalid native trace exit")
			}
			if len(p.traceExitDepths) == 0 {
				if depth != 0 {
					return nil, fmt.Errorf("jit: native trace exit stack lacks a deopt map")
				}
			} else {
				if exitID < 0 || exitID >= len(p.traceExitDepths) {
					return nil, fmt.Errorf("jit: invalid native trace exit")
				}
				depth = int(p.traceExitDepths[exitID])
			}
			if p.NumLocals+depth > 32 {
				return nil, fmt.Errorf("jit: native trace exit stack limit exceeded")
			}
			for stackIndex := 0; stackIndex < depth; stackIndex++ {
				code = emitStoreF64(code, stackIndex, nativeLocalOffset(p.NumLocals+stackIndex, p.NumParams))
			}
			code = append(code, 0xB8)
			code = appendInt32(code, int32(in.Operand+3))
			code = append(code, 0xC3)
			depth = 0
			sawTerminal = true
		default:
			return nil, fmt.Errorf("jit: native unsupported IR opcode %d", in.Op)
		}
		if depth < 0 || depth > 8 {
			return nil, fmt.Errorf("jit: native register stack overflow")
		}
	}
	if !sawTerminal {
		return nil, fmt.Errorf("jit: native program has no terminal")
	}
	for _, fixup := range fixups {
		if fixup.target < 0 || fixup.target >= len(offsets) {
			return nil, fmt.Errorf("jit: native jump target out of range")
		}
		relative := offsets[fixup.target] - (fixup.displacement + 4)
		binary.LittleEndian.PutUint32(code[fixup.displacement:fixup.displacement+4], uint32(int32(relative)))
	}
	return jitnative.Publish(code, retainDebugBytes...)
}

func nativeAssignedLocals(p *Program) ([]uint64, error) {
	if p.NumLocals > 64 {
		return nil, fmt.Errorf("jit: native local proof limit exceeded")
	}
	assigned := make([]uint64, len(p.Code))
	reachable := make([]bool, len(p.Code))
	initial := p.nativePreassigned
	for i := 1; i <= p.NumParams && i < p.NumLocals; i++ {
		if p.nativeNumberArgs&(uint16(1)<<(i-1)) != 0 {
			initial |= uint64(1) << i
		}
	}
	assigned[0], reachable[0] = initial, true
	work := []int{0}
	for len(work) > 0 {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		out := assigned[i]
		in := p.Code[i]
		if in.Op == OpStoreLocal {
			out |= uint64(1) << in.Operand
		}
		var successors []int
		switch in.Op {
		case OpReturn, OpReturnUndef, OpTraceExit:
		case OpJump:
			successors = append(successors, int(in.Operand))
		case OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			successors = append(successors, i+1, int(in.Operand))
		default:
			successors = append(successors, i+1)
		}
		for _, next := range successors {
			if next < 0 || next >= len(p.Code) {
				return nil, fmt.Errorf("jit: native control flow leaves program")
			}
			if !reachable[next] {
				reachable[next], assigned[next] = true, out
				work = append(work, next)
				continue
			}
			merged := assigned[next] & out
			if merged != assigned[next] {
				assigned[next] = merged
				work = append(work, next)
			}
		}
	}
	return assigned, nil
}

func nativeLocalOffset(slot, numParams int) int {
	if slot >= 1 && slot <= numParams {
		return (slot - 1) * 8
	}
	return nativeLocalsOffset + slot*8
}

func emitLoadF64(code []byte, xmm, offset int) []byte {
	code = append(code, 0xF2, 0x41, 0x0F, 0x10, byte(0x82|(xmm<<3)))
	return appendInt32(code, int32(offset))
}

func emitStoreF64(code []byte, xmm, offset int) []byte {
	code = append(code, 0xF2, 0x41, 0x0F, 0x11, byte(0x82|(xmm<<3)))
	return appendInt32(code, int32(offset))
}

func emitConstF64(code []byte, xmm int, value float64) []byte {
	code = append(code, 0x48, 0xB8)
	var bits [8]byte
	binary.LittleEndian.PutUint64(bits[:], math.Float64bits(value))
	code = append(code, bits[:]...)
	code = append(code, 0x66, 0x48, 0x0F, 0x6E, byte(0xC0|(xmm<<3)))
	return code
}

func emitBinaryF64(code []byte, op Op, left, right int) []byte {
	opcode := byte(0)
	switch op {
	case OpAdd:
		opcode = 0x58
	case OpSub:
		opcode = 0x5C
	case OpMul:
		opcode = 0x59
	case OpDiv:
		opcode = 0x5E
	}
	return append(code, 0xF2, 0x0F, opcode, byte(0xC0|(left<<3)|right))
}

func emitMoveF64(code []byte, dst, src int) []byte {
	return append(code, 0x66, 0x0F, 0x28, byte(0xC0|(dst<<3)|src))
}

func emitSwapF64(code []byte, left, right int) []byte {
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC0|(left<<3)))
	code = emitMoveF64(code, left, right)
	return append(code, 0x66, 0x48, 0x0F, 0x6E, byte(0xC0|(right<<3)))
}

func emitNegF64(code []byte, xmm int) []byte {
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC0|(xmm<<3)))
	code = append(code, 0x48, 0x0F, 0xBA, 0xF8, 0x3F) // BTC RAX,63
	return append(code, 0x66, 0x48, 0x0F, 0x6E, byte(0xC0|(xmm<<3)))
}

func emitComparisonTest(code []byte, op Op, left, right int) []byte {
	code = append(code, 0x66, 0x0F, 0x2E, byte(0xC0|(left<<3)|right)) // UCOMISD
	if op == OpNe || op == OpStrictNe {
		// UCOMISD sets ZF for both equality and unordered. JS Number != must
		// also be true for NaN, so combine SETNE with SETP.
		code = append(code, 0x0F, 0x95, 0xC0) // SETNE AL
		code = append(code, 0x0F, 0x9A, 0xC2) // SETP DL
		code = append(code, 0x08, 0xD0)       // OR AL,DL
		return append(code, 0x84, 0xC0)       // TEST AL,AL
	}
	condition := byte(0)
	ordered := false
	switch op {
	case OpEq, OpStrictEq:
		condition, ordered = 0x94, true // SETE
	case OpLt:
		condition, ordered = 0x92, true // SETB
	case OpLe:
		condition, ordered = 0x96, true // SETBE
	case OpGt:
		condition = 0x97 // SETA
	case OpGe:
		condition = 0x93 // SETAE
	}
	code = append(code, 0x0F, condition, 0xC0) // SETcc AL
	if ordered {
		code = append(code, 0x0F, 0x9B, 0xC2, 0x20, 0xD0) // SETNP DL; AND AL,DL
	}
	return append(code, 0x84, 0xC0) // TEST AL,AL
}

func emitNumberTruthyTest(code []byte, xmm int) []byte {
	// JavaScript Numbers are truthy when ordered and non-zero. UCOMISD sets
	// ZF for both zero and unordered (NaN), so SETNE handles 0, -0 and NaN.
	code = append(code, 0x66, 0x45, 0x0F, 0xEF, 0xFF)              // PXOR XMM15,XMM15
	code = append(code, 0x66, 0x41, 0x0F, 0x2E, byte(0xC7|xmm<<3)) // UCOMISD XMMn,XMM15
	code = append(code, 0x0F, 0x95, 0xC0)                          // SETNE AL
	return append(code, 0x84, 0xC0)                                // TEST AL,AL
}

func emitNativeBudgetPoll(code []byte, resumeOffset int) []byte {
	// SUB QWORD PTR [R10+Budget],1; JNZ continue.
	code = append(code, 0x49, 0x83, 0xAA)
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0x01, 0x0F, 0x85)
	continueFixup := len(code)
	code = append(code, 0, 0, 0, 0)
	// MOV QWORD PTR [R10+Resume],resumeOffset; MOV EAX,2; RET.
	code = append(code, 0x49, 0xC7, 0x82)
	code = appendInt32(code, nativeResumeOffset)
	code = appendInt32(code, int32(resumeOffset))
	code = append(code, 0xB8, 0x02, 0x00, 0x00, 0x00, 0xC3)
	relative := len(code) - (continueFixup + 4)
	binary.LittleEndian.PutUint32(code[continueFixup:continueFixup+4], uint32(int32(relative)))
	return code
}

func emitNativeDirtyLocal(code []byte, slot int) []byte {
	// BTS QWORD PTR [R10+Status],slot records stores completed on this path.
	code = append(code, 0x49, 0x0F, 0xBA, 0xAA)
	code = appendInt32(code, nativeStatusOffset)
	return append(code, byte(slot))
}

func appendInt32(code []byte, value int32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(value))
	return append(code, buf[:]...)
}
