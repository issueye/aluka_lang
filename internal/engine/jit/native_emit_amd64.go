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
	// The compiler now lowers whole functions, so unreachable tails (e.g. the
	// implicit return_undef the bytecode compiler emits after the final
	// return) are part of the IR. Native code only needs reachable
	// instructions; skipping the rest keeps the linear depth tracking sound.
	reachable := reachableFromEntry(p.Code)
	targeted := make([]bool, len(p.Code))
	for i, in := range p.Code {
		if !reachable[i] {
			continue
		}
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
	var ripPool []ripConstRef
	sawTerminal := false
	for i := 0; i < len(p.Code); i++ {
		if !reachable[i] {
			continue
		}
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
		case OpAdd, OpSub, OpMul, OpDiv, OpMod:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			right, left := depth-1, depth-2
			if in.Op == OpMod {
				code = emitModF64(code, left, right, &ripPool)
			} else {
				code = emitBinaryF64(code, in.Op, left, right)
			}
			depth--
		case OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			right, left := depth-1, depth-2
			code = emitBitwiseI32(code, in.Op, left, right, &ripPool)
			depth--
		case OpBitNot:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			code = emitBitNotI32(code, depth-1, &ripPool)
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
		case OpReturnUndef:
			// Unreachable return_undef tails are skipped above; a reachable
			// one returns the JS undefined, which the numeric native frame
			// cannot represent. Reject so Auto falls back to the Quick tier
			// instead of silently returning a number.
			return nil, fmt.Errorf("jit: native cannot represent undefined return")
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
		case OpPow:
			// R4-7: math.Pow is a libm-quality function; no x86 instruction
			// computes it bit-identically (F2XM1-based exp-log differs in last
			// ulp and in special cases like pow(1, NaN) / pow(-1, ±Inf)), and a
			// callout into Go/libm would change the pointer-free native ABI.
			// Pow stays in the Quick tier; Auto falls back stably (the native
			// rejection is recorded once per program).
			return nil, fmt.Errorf("jit: native pow requires libm (kept in Quick tier)")
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
	code = finalizeRipConstPool(code, ripPool)
	for _, fixup := range fixups {
		if fixup.target < 0 || fixup.target >= len(offsets) {
			return nil, fmt.Errorf("jit: native jump target out of range")
		}
		relative := offsets[fixup.target] - (fixup.displacement + 4)
		binary.LittleEndian.PutUint32(code[fixup.displacement:fixup.displacement+4], uint32(int32(relative)))
	}
	return jitnative.Publish(code, retainDebugBytes...)
}

// nativeAssignedLocals computes, for every instruction, the set of locals
// that are proven Numbers on every path reaching it. assigned[0] starts from
// the native preassignment (numeric params and property input slots).
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

func appendInt64(code []byte, value int64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(value))
	return append(code, buf[:]...)
}

// emitBitwiseI32 emits XMM(left) = OP(ToInt32(XMM(left)), ToInt32(XMM(right)))
// for the & | ^ << >> >>> operators with exact ES semantics: both operands
// are ToInt32'ed (truncation + mod 2^32 wrap), the shift count is masked with
// 0x1F, >> is arithmetic and >>> is logical on the unsigned value. The result
// is the exact int32 converted back to a double, matching the Quick executor.
// The destination register (left) holds the result. Clobbers RAX, RCX, RDX.
func emitBitwiseI32(code []byte, op Op, left, right int, pool *[]ripConstRef) []byte {
	// R8 holds the right operand: emitToInt32 clobbers RAX/RCX/RDX internally,
	// so the right result cannot live in EDX across the left conversion.
	code = emitToInt32(code, right, pool) // EAX = ToInt32(right)
	code = append(code, 0x49, 0x89, 0xC0) // MOV R8,RAX
	code = emitToInt32(code, left, pool)  // EAX = ToInt32(left)
	switch op {
	case OpShl, OpShr, OpUShr:
		code = append(code, 0x41, 0x8B, 0xC8) // MOV ECX,R8D
		code = append(code, 0x83, 0xE1, 0x1F) // AND ECX,0x1F (ES shift mask)
		switch op {
		case OpShl:
			code = append(code, 0xD3, 0xE0) // SHL EAX,CL
		case OpShr:
			code = append(code, 0xD3, 0xF8) // SAR EAX,CL
		default:
			code = append(code, 0xD3, 0xE8) // SHR EAX,CL
		}
	case OpBitAnd:
		code = append(code, 0x41, 0x23, 0xC0) // AND EAX,R8D (23 /r: reg=dest EAX, rm=src R8D)
	case OpBitOr:
		code = append(code, 0x41, 0x0B, 0xC0) // OR EAX,R8D (0B /r: reg=dest EAX, rm=src R8D)
	case OpBitXor:
		code = append(code, 0x41, 0x33, 0xC0) // XOR EAX,R8D (33 /r: reg=dest EAX, rm=src R8D)
	}
	// EAX holds a signed int32 (UShr: an unsigned value in [0, 2^32)); both
	// convert exactly: MOVSXD sign-extends, and for UShr the write to EAX
	// already zero-extended RAX.
	if op != OpUShr {
		code = append(code, 0x48, 0x63, 0xC0) // MOVSXD RAX,EAX
	}
	code = append(code, 0xF2, 0x48, 0x0F, 0x2A, byte(0xC0|(left<<3))) // CVTSI2SD XMMleft,RAX
	return code
}

// emitBitNotI32 emits XMM d = ~ToInt32(XMM d) as a double (ES unary ~).
func emitBitNotI32(code []byte, d int, pool *[]ripConstRef) []byte {
	code = emitToInt32(code, d, pool)
	code = append(code, 0xF7, 0xD0)                                // NOT EAX
	code = append(code, 0x48, 0x63, 0xC0)                          // MOVSXD RAX,EAX
	code = append(code, 0xF2, 0x48, 0x0F, 0x2A, byte(0xC0|(d<<3))) // CVTSI2SD XMMd,RAX
	return code
}

// localJump is an instruction-internal forward jump (a Jcc/JMP rel32 whose
// target is the current end of the emitted block when patch runs). Unlike
// nativeFixup it is resolved inside a single emit helper, not against IR
// instruction offsets.
type localJump struct{ pos int }

func appendLocalCondJump(code []byte, condition byte) ([]byte, *localJump) {
	code = append(code, 0x0F, condition)
	jump := &localJump{pos: len(code)}
	return append(code, 0, 0, 0, 0), jump
}

func appendLocalJump(code []byte) ([]byte, *localJump) {
	code = append(code, 0xE9)
	jump := &localJump{pos: len(code)}
	return append(code, 0, 0, 0, 0), jump
}

func (j *localJump) patch(code []byte) {
	if j == nil {
		return
	}
	relative := len(code) - (j.pos + 4)
	binary.LittleEndian.PutUint32(code[j.pos:j.pos+4], uint32(int32(relative)))
}

// ripConstRef records a RIP-relative constant reference: the instruction's
// disp32 placeholder position and the constant bits. The constants are
// appended as a literal pool after the last instruction (unreachable: every
// program ends in RET), and each disp32 is patched to reach its constant.
type ripConstRef struct {
	dispPos int
	bits    uint64
}

// finalizeRipConstPool appends the literal constants after the last
// instruction and patches every disp32 to reach its constant. Each entry is
// 16-byte aligned: the UCOMISD/MOVSD m64 operands need natural 8-byte
// alignment and the ANDPD m128 operand needs 16-byte alignment, and a
// misaligned SSE memory access raises #GP.
func finalizeRipConstPool(code []byte, pool []ripConstRef) []byte {
	for i := range pool {
		ref := &pool[i]
		for len(code)%16 != 0 {
			code = append(code, 0)
		}
		pos := len(code)
		code = appendInt64(code, int64(ref.bits))
		relative := pos - (ref.dispPos + 4)
		binary.LittleEndian.PutUint32(code[ref.dispPos:ref.dispPos+4], uint32(int32(relative)))
	}
	return code
}

// emitUCOMISDRipConst appends UCOMISD XMMd, [RIP+const] (compare d with a
// floating constant; ZF=1 when equal or unordered, CF=1 when d < const).
func emitUCOMISDRipConst(code []byte, d int, bits uint64, pool *[]ripConstRef) []byte {
	code = append(code, 0x66, 0x0F, 0x2E, byte(0x05|(d<<3)))
	*pool = append(*pool, ripConstRef{dispPos: len(code), bits: bits})
	return append(code, 0, 0, 0, 0)
}

// emitANDpdRipConst appends ANDPD XMMd, [RIP+const] (bitwise AND with a
// constant, used to clear the sign bit: |x|).
func emitANDpdRipConst(code []byte, d int, bits uint64, pool *[]ripConstRef) []byte {
	code = append(code, 0x66, 0x0F, 0x54, byte(0x05|(d<<3)))
	*pool = append(*pool, ripConstRef{dispPos: len(code), bits: bits})
	return append(code, 0, 0, 0, 0)
}

// emitMovsdRipConst appends MOVSD XMMd, [RIP+const].
func emitMovsdRipConst(code []byte, d int, bits uint64, pool *[]ripConstRef) []byte {
	code = append(code, 0xF2, 0x0F, 0x10, byte(0x05|(d<<3)))
	*pool = append(*pool, ripConstRef{dispPos: len(code), bits: bits})
	return append(code, 0, 0, 0, 0)
}

// emitModF64 emits XMM(left) = fmod(XMM(left), XMM(right)) with the exact
// math.Mod semantics (the JS % on Numbers): NaN when the divisor is ±0 or any
// operand is NaN/±Inf; the dividend when the divisor is ±Inf or the dividend
// is ±0; otherwise the true remainder with the dividend's sign.
//
// The general case uses the x87 FPREM partial-remainder loop: FPREM computes
// fmod (quotient truncated toward zero) and is the same instruction Go's
// amd64 math.Mod is built on, so the result is bit-identical to Quick's
// math.Mod for every input (both are the exact mathematical remainder). The
// special cases mirror the Go pre-checks. The 8-byte [R10+Budget] frame slot
// is used as the XMM→x87 transfer scratch and restored before the instruction
// ends, so no ABI-visible frame state is disturbed. Clobbers RAX, RCX, RDX.
func emitModF64(code []byte, left, right int, pool *[]ripConstRef) []byte {
	// (1) y == ±0 or y is NaN → NaN.
	code = emitUCOMISDRipConst(code, right, 0x0000000000000000, pool) // +0.0
	code, jNaN1 := appendLocalCondJump(code, 0x84)                    // JE
	// (2) x == +Inf or x is NaN → NaN.
	code = emitUCOMISDRipConst(code, left, 0x7FF0000000000000, pool) // +Inf
	code, jNaN2 := appendLocalCondJump(code, 0x84)
	// (3) x == −Inf → NaN.
	code = emitUCOMISDRipConst(code, left, 0xFFF0000000000000, pool) // −Inf
	code, jNaN3 := appendLocalCondJump(code, 0x84)
	// (4) y == ±Inf → x.
	code = emitUCOMISDRipConst(code, right, 0x7FF0000000000000, pool)
	code, jDone1 := appendLocalCondJump(code, 0x84)
	code = emitUCOMISDRipConst(code, right, 0xFFF0000000000000, pool)
	code, jDone2 := appendLocalCondJump(code, 0x84)
	// (5) x == ±0 → x.
	code = emitUCOMISDRipConst(code, left, 0x0000000000000000, pool)
	code, jDone3 := appendLocalCondJump(code, 0x84)

	// (6) General case: FPREM loop. x87 memory operands cannot use REX
	// prefixes, so the frame pointer is copied into RDX and the scratch is
	// the 8-byte [RDX+Budget] slot (saved in RCX first; FSTSW AX clobbers
	// the low word of RAX, which is not used here).
	code = append(code, 0x4C, 0x89, 0xD2) // MOV RDX,R10
	code = append(code, 0x48, 0x8B, 0x8A) // MOV RCX,[RDX+disp32]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xF2, 0x0F, 0x11, byte(0x82|(right<<3))) // MOVSD [RDX+disp],XMMright
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xDD, 0x82) // FLD m64 [RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xF2, 0x0F, 0x11, byte(0x82|(left<<3))) // MOVSD [RDX+disp],XMMleft
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xDD, 0x82) // FLD m64 [RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	fprem := len(code)
	code = append(code, 0xD9, 0xF8)                      // FPREM
	code = append(code, 0xDF, 0xE0)                      // FSTSW AX
	code = append(code, 0xF6, 0xC4, 0x04)                // TEST AH,0x04 (C2 set: partial remainder)
	code = append(code, 0x75, byte(fprem-(len(code)+2))) // JNE rel8 back to FPREM
	code = append(code, 0xDD, 0x9A)                      // FSTP m64 [RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xDD, 0xD8)                             // FSTP ST(0): discard the divisor, rebalancing the x87 stack
	code = append(code, 0xF2, 0x0F, 0x10, byte(0x82|(left<<3))) // MOVSD XMMleft,[RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0x48, 0x89, 0x8A) // MOV [RDX+disp],RCX (restore)
	code = appendInt32(code, nativeBudgetOffset)
	code, jDone := appendLocalJump(code)

	// NaN result: load a canonical NaN into the result register. The NaN
	// jumps are patched before the MOVSD is appended so they land on it; the
	// rest jump past it to the shared done point.
	jNaN1.patch(code)
	jNaN2.patch(code)
	jNaN3.patch(code)
	code = emitMovsdRipConst(code, left, 0x7FF8000000000000, pool)
	jDone1.patch(code)
	jDone2.patch(code)
	jDone3.patch(code)
	jDone.patch(code)
	return code
}

// emitToInt32 converts the JS Number in XMM d to its ES ToInt32 bits in EAX
// (truncate toward zero, wrap mod 2^32; NaN, ±Inf and |x| ≥ 2^63 handled by a
// rare slow path), bit-identical to quickInt32/quickUint32. Clobbers RAX,
// RCX, RDX; the caller continues with EAX valid on every path.
func emitToInt32(code []byte, d int, pool *[]ripConstRef) []byte {
	// Fast path: the truncated int64 conversion is exact for −2^63 < x < 2^63,
	// and the low 32 bits equal ToInt32 (mod 2^32 wrap). Out-of-range values,
	// NaN and ±Inf all produce INT64_MIN, which is the slow-path trigger
	// (x == −2^63 lands there too and computes the correct 0).
	code = append(code, 0xF2, 0x48, 0x0F, 0x2C, byte(0xC0|d)) // CVTTSD2SI RAX,XMMd
	code = append(code, 0x48, 0xB9)                           // MOV RCX,imm64
	code = appendInt64(code, -0x8000000000000000)
	code = append(code, 0x48, 0x39, 0xC8)          // CMP RAX,RCX
	code, jFast := appendLocalCondJump(code, 0x85) // JNE done (fast path)

	// Slow path: x is NaN, ±Inf, |x| ≥ 2^63, or exactly −2^63. For NaN/±Inf
	// ToInt32 is 0. For finite |x| ≥ 2^63 the double is already an integer
	// with bits x = m·2^s (s = exponent−52 ∈ [11,31]); x mod 2^32 is the low
	// 32 bits of (bits<<s), and a negative x negates the unsigned value.
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC2|(d<<3))) // MOVQ RDX,XMMd (original bits, sign kept)
	code = emitANDpdRipConst(code, d, 0x7FFFFFFFFFFFFFFF, pool)    // ANDPD XMMd,[RIP+absMask]
	code = emitUCOMISDRipConst(code, d, 0x7FF0000000000000, pool)  // UCOMISD XMMd,[RIP++Inf]
	code, jZero := appendLocalCondJump(code, 0x84)                 // JZ zero (NaN or +Inf)
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC1|(d<<3))) // MOVQ RCX,XMMd
	code = append(code, 0x48, 0xC1, 0xE9, 0x34)                    // SHR RCX,52
	code = append(code, 0x81, 0xE1, 0xFF, 0x07, 0x00, 0x00)        // AND ECX,0x7FF
	code = append(code, 0x81, 0xE9, 0x33, 0x04, 0x00, 0x00)        // SUB ECX,1075 (exponent−52)
	code = append(code, 0x83, 0xF9, 0x20)                          // CMP ECX,32
	code, jBig := appendLocalCondJump(code, 0x83)                  // JAE zero (|x| ≥ 2^84, multiple of 2^32)
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC0|(d<<3))) // MOVQ RAX,XMMd
	code = append(code, 0x48, 0xD3, 0xE0)                          // SHL RAX,CL
	code = append(code, 0x25, 0xFF, 0xFF, 0xFF, 0xFF)              // AND EAX,0xFFFFFFFF
	code, jSign := appendLocalJump(code)                           // JMP sign
	// The zero and big-quotient jumps are patched before the XOR so they land
	// on it; the sign jump lands on the TEST below.
	jZero.patch(code)
	jBig.patch(code)
	code = append(code, 0x31, 0xC0) // XOR EAX,EAX (zero path)
	jSign.patch(code)
	// sign: negate the unsigned magnitude when the original x was negative.
	code = append(code, 0x48, 0x85, 0xD2) // TEST RDX,RDX
	code, jDone := appendLocalCondJump(code, 0x89)
	code = append(code, 0xF7, 0xD8) // NEG EAX
	jDone.patch(code)
	jFast.patch(code)
	return code
}
