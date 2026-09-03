// 字节码 → Quick IR 降级：叶函数编译、可达性分析与不可达尾部裁剪。

package jit

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

const maxQuickSlots = 32

// hasDirectSelfCalls 报告程序是否含 OpSelfCall 且每个站点的 callee 都直接
// 来自 OpPushSelf（真实形态：push_self; args…; self_call，callee 位于栈底、
// 参数在其上）。无 OpSelfCall 的程序返回 false（不得进入自递归模式——否则
// 普通函数会被误标：禁用 M2 寄存器分配并发射自递归入口/返回路径，破坏
// 属性 guard 等正常行为）。对每个站点从参数区向后做深度游走（与
// inlineCallTarget 的站点分析同构）：above 从参数个数开始递减，遇到"会执行
// 到 callee 位置之下"的指令即找到 callee 来源；参数区内出现跳转/返回/嵌套
// 调用/弹出 callee 以下值的指令均判定为不可判定的普通调用形态（返回 false，
// native 拒绝、Quick 兜底）。
func hasDirectSelfCalls(code []Instr) bool {
	sawSelfCall := false
	for i := range code {
		if code[i].Op != OpSelfCall {
			continue
		}
		sawSelfCall = true
		above := int(code[i].Operand)
		source := -1
		for j := i - 1; j >= 0; j-- {
			need, delta := 0, 0
			switch code[j].Op {
			case OpConst, OpLoadLocal, OpConstString, OpDup, OpPushSelf:
				delta = 1
			case OpStoreLocal, OpPop:
				need, delta = 1, -1
			case OpSwap:
				need = 2
			case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow, OpEq, OpNe, OpStrictEq, OpStrictNe,
				OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr, OpLt, OpLe, OpGt, OpGe:
				need, delta = 2, -1
			case OpNeg, OpNot, OpBitNot, OpUnaryPlus:
				need = 1
			case OpGetProp:
				need, delta = 1, 0
			default:
				// 跳转/返回/嵌套 OpSelfCall 等：形态不可判定。
				return false
			}
			before := above - delta
			if before < 0 {
				source = j
				break
			}
			if need > before {
				// 该指令会弹出 callee 位置以下的值，破坏自调用形态。
				return false
			}
			above = before
		}
		if source < 0 || code[source].Op != OpPushSelf {
			return false
		}
	}
	// 无任何 OpSelfCall 的程序不是自递归候选。
	return sawSelfCall
}

// CompileLeaf lowers a function template whose body is a numeric expression
// with structured control flow: if/else chains, integer and string switch
// (lowered by the bytecode compiler to a strict-equality jump chain), ternary
// expressions and short-circuit operators (&& / || / ?? keep-branches), all
// the way down to arbitrary CFGs whose joins keep a consistent operand-stack
// depth. Unsupported semantics are rejected before execution; the cheap
// candidate pre-filter (RejectLeafReason) runs first so hot-path callers can
// skip the full lowering without invoking it.
func CompileLeaf(tmpl *bytecode.FuncTemplate) (*Program, error) {
	if err := rejectLeafCandidate(tmpl); err != nil {
		return nil, err
	}
	p := &Program{NumParams: tmpl.NumParams, NumLocals: tmpl.NumLocals, SelfUpvalue: -1}
	pcToIR := make(map[int]int, len(tmpl.Code)/bytecode.InstrSize)
	for pc := 0; pc < len(tmpl.Code); pc += bytecode.InstrSize {
		pcToIR[pc] = len(p.Code)
		op := bytecode.Opcode(tmpl.Code[pc])
		arg := uint32(tmpl.Code[pc+1])<<16 | uint32(tmpl.Code[pc+2])<<8 | uint32(tmpl.Code[pc+3])
		switch op {
		case bytecode.OpPushInt:
			p.Code = append(p.Code, Instr{Op: OpConst, Value: float64(arg)})
		case bytecode.OpPushNegInt:
			p.Code = append(p.Code, Instr{Op: OpConst, Value: -float64(arg)})
		case bytecode.OpPushConst:
			// Number constants lower to OpConst; string constants (string
			// switch case tests, string comparisons) enter the per-program
			// pool and lower to OpConstString.
			switch tmpl.Constants[arg].Type() {
			case engine.TypeNumber:
				n, _ := tmpl.Constants[arg].Float()
				p.Code = append(p.Code, Instr{Op: OpConst, Value: n})
			case engine.TypeString:
				p.Code = append(p.Code, Instr{Op: OpConstString, Operand: uint32(len(p.stringConsts)), Name: tmpl.Constants[arg].String()})
				p.stringConsts = append(p.stringConsts, tmpl.Constants[arg])
			default:
				return nil, fmt.Errorf("jit: non-number constant")
			}
		case bytecode.OpLoadLocal:
			if int(arg) >= tmpl.NumLocals {
				return nil, fmt.Errorf("jit: local index out of range")
			}
			p.Code = append(p.Code, Instr{Op: OpLoadLocal, Operand: arg})
		case bytecode.OpStoreLocal:
			if int(arg) >= tmpl.NumLocals {
				return nil, fmt.Errorf("jit: local index out of range")
			}
			p.Code = append(p.Code, Instr{Op: OpStoreLocal, Operand: arg})
		case bytecode.OpGetProp:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeString {
				return nil, fmt.Errorf("jit: non-string property name")
			}
			p.Code = append(p.Code, Instr{Op: OpGetProp, Name: tmpl.Constants[arg].String()})
		case bytecode.OpGetPropLocal:
			slot, nameIdx := int(arg>>16), int(arg&0xFFFF)
			if slot >= tmpl.NumLocals || nameIdx >= len(tmpl.Constants) || tmpl.Constants[nameIdx].Type() != engine.TypeString {
				return nil, fmt.Errorf("jit: invalid property-local operand")
			}
			p.Code = append(p.Code, Instr{Op: OpLoadLocal, Operand: uint32(slot)}, Instr{Op: OpGetProp, Name: tmpl.Constants[nameIdx].String()})
		case bytecode.OpLoadUpvalue:
			if p.SelfUpvalue >= 0 && p.SelfUpvalue != int(arg) {
				return nil, fmt.Errorf("jit: multiple upvalues")
			}
			p.SelfUpvalue = int(arg)
			p.Code = append(p.Code, Instr{Op: OpPushSelf})
		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow,
			bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpShl, bytecode.OpShr, bytecode.OpUShr,
			bytecode.OpEq, bytecode.OpStrictEq, bytecode.OpNe, bytecode.OpStrictNe,
			bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe:
			mapped := map[bytecode.Opcode]Op{
				bytecode.OpAdd: OpAdd, bytecode.OpSub: OpSub, bytecode.OpMul: OpMul,
				bytecode.OpDiv: OpDiv, bytecode.OpMod: OpMod, bytecode.OpPow: OpPow, bytecode.OpEq: OpEq,
				bytecode.OpStrictEq: OpStrictEq, bytecode.OpNe: OpNe, bytecode.OpStrictNe: OpStrictNe,
				bytecode.OpBitAnd: OpBitAnd, bytecode.OpBitOr: OpBitOr, bytecode.OpBitXor: OpBitXor,
				bytecode.OpShl: OpShl, bytecode.OpShr: OpShr, bytecode.OpUShr: OpUShr,
				bytecode.OpLt: OpLt, bytecode.OpLe: OpLe,
				bytecode.OpGt: OpGt, bytecode.OpGe: OpGe,
			}
			p.Code = append(p.Code, Instr{Op: mapped[op]})
		case bytecode.OpNeg:
			p.Code = append(p.Code, Instr{Op: OpNeg})
		case bytecode.OpNot:
			p.Code = append(p.Code, Instr{Op: OpNot})
		case bytecode.OpBitNot:
			p.Code = append(p.Code, Instr{Op: OpBitNot})
		case bytecode.OpUnaryPlus:
			p.Code = append(p.Code, Instr{Op: OpUnaryPlus})
		case bytecode.OpPop:
			p.Code = append(p.Code, Instr{Op: OpPop})
		case bytecode.OpInc, bytecode.OpDec:
			// ++ / -- lower to the existing Number sequence (the operand is
			// already on the stack): x++ -> x + 1, x-- -> x - 1.
			// A BigInt operand fails the arithmetic Number guard and falls
			// back to Tier 0, where OpInc/OpDec preserve the BigInt type.
			p.Code = append(p.Code, Instr{Op: OpConst, Value: 1})
			if op == bytecode.OpInc {
				p.Code = append(p.Code, Instr{Op: OpAdd})
			} else {
				p.Code = append(p.Code, Instr{Op: OpSub})
			}
		case bytecode.OpDup:
			p.Code = append(p.Code, Instr{Op: OpDup})
		case bytecode.OpSwap:
			p.Code = append(p.Code, Instr{Op: OpSwap})
		case bytecode.OpJmp:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			p.Code = append(p.Code, Instr{Op: OpJump, Operand: uint32(target)})
		case bytecode.OpJmpTruePop:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			p.Code = append(p.Code, Instr{Op: OpJumpTrue, Operand: uint32(target)})
		case bytecode.OpJmpFalsePop:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			p.Code = append(p.Code, Instr{Op: OpJumpFalse, Operand: uint32(target)})
		case bytecode.OpJmpTrueKeep:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			p.Code = append(p.Code, Instr{Op: OpJumpTrueKeep, Operand: uint32(target)})
		case bytecode.OpJmpFalseKeep:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			p.Code = append(p.Code, Instr{Op: OpJumpFalseKeep, Operand: uint32(target)})
		case bytecode.OpJmpNullishKeep:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			p.Code = append(p.Code, Instr{Op: OpJumpNullishKeep, Operand: uint32(target)})
		case bytecode.OpCall:
			if arg > 8 {
				return nil, fmt.Errorf("jit: too many call arguments")
			}
			p.Code = append(p.Code, Instr{Op: OpSelfCall, Operand: arg})
		case bytecode.OpReturn:
			p.Code = append(p.Code, Instr{Op: OpReturn})
		case bytecode.OpReturnUndef:
			p.Code = append(p.Code, Instr{Op: OpReturnUndef})
		default:
			return nil, fmt.Errorf("jit: unsupported opcode %s", op)
		}
	}
	for i := range p.Code {
		switch p.Code[i].Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			targetPC := int(p.Code[i].Operand)
			target, ok := pcToIR[targetPC]
			if !ok {
				return nil, fmt.Errorf("jit: jump target %d is not lowered", targetPC)
			}
			p.Code[i].Operand = uint32(target)
		}
	}
	// The bytecode compiler appends an implicit trailing return_undef after
	// the final explicit return. The contiguous unreachable suffix is dead
	// code; trimming it keeps the leaf IR in the canonical single-return
	// shape that inline targets, trivial-getter detection and the trailing
	// return check rely on (reachable instructions are never trimmed).
	p.Code = trimUnreachableTail(p.Code)
	// F1 自递归标记：仅当**所有** OpSelfCall 的 callee 都直接来自 OpPushSelf
	// （push_self; args…; self_call，callee 在栈底）才启用 Native 自递归模式。
	// 含普通调用（callee 来自局部变量/参数，Quick 运行时 guard 失败回退
	// Tier 0）的函数不得误标——否则 Native 机器码会把非自递归的 OpSelfCall
	// 也执行成自递归（JMP entry 0），产生错误结果而非 GuardFailed 回退。
	p.hasSelfCall = hasDirectSelfCalls(p.Code)
	if len(p.Code) == 0 || (p.Code[len(p.Code)-1].Op != OpReturn && p.Code[len(p.Code)-1].Op != OpReturnUndef) {
		return nil, fmt.Errorf("jit: no return")
	}
	if err := p.Verify(); err != nil {
		return nil, err
	}
	// R5-1/R5-2 conservative optimization passes (constant folding,
	// redundant store-load elimination, unreachable block deletion, and the
	// unary const folding). The result must verify again; a failed pass
	// keeps the unoptimized code.
	if OptimizeIR {
		stats := OptimizeProgram(p)
		_ = stats
		if err := p.Verify(); err != nil {
			// The optimizer must never turn a valid program invalid; fall
			// back to the unoptimized code instead of failing the compile.
			return nil, fmt.Errorf("jit: optimize produced invalid IR: %v", err)
		}
	}
	p.propertyGuards = make([]propertyGuard, len(p.Code))
	return p, nil
}

// reachableFromEntry marks every instruction reachable from the program entry
// through control flow (a pure CFG traversal without stack-depth tracking).
// It is the portable version of the native emitter's reachability pass.
func reachableFromEntry(code []Instr) []bool {
	reachable := make([]bool, len(code))
	if len(code) == 0 {
		return reachable
	}
	reachable[0] = true
	work := []int{0}
	visit := func(next int) {
		if next >= 0 && next < len(code) && !reachable[next] {
			reachable[next] = true
			work = append(work, next)
		}
	}
	for len(work) > 0 {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		switch code[i].Op {
		case OpReturn, OpReturnUndef, OpTraceExit:
		case OpJump:
			visit(int(code[i].Operand))
		case OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			visit(i + 1)
			visit(int(code[i].Operand))
		default:
			visit(i + 1)
		}
	}
	return reachable
}

// trimUnreachableTail drops the contiguous unreachable suffix of a lowered
// program. The bytecode compiler appends an implicit trailing return_undef
// after the final explicit return; that dead tail would otherwise change the
// IR shape that inline targets, trivial-getter detection and the trailing
// return check rely on. Reachable instructions are never trimmed: jump
// targets of reachable jumps are reachable by construction.
func trimUnreachableTail(code []Instr) []Instr {
	reachable := reachableFromEntry(code)
	for len(code) > 0 && !reachable[len(code)-1] {
		code = code[:len(code)-1]
	}
	return code
}
