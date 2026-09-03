// Quick IR 程序结构：Instr/Program 定义、IR dump、调用目标绑定与内联。

package jit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

type Instr struct {
	Op      Op
	Operand uint32
	Value   float64
	Name    string
}

type Program struct {
	NumParams      int
	NumLocals      int
	MaxStack       int
	SelfUpvalue    int
	Code           []Instr
	nativeCode     *jitnative.Code
	propertyGuards []propertyGuard
	// hasSelfCall 标记程序含 OpSelfCall（F1 自递归 Native）：需要递归帧区
	// （Frame.RecBase/RecFP）与 R11 locals 基址发射。
	hasSelfCall bool
	// stringConsts is the per-program string constant pool. OpConstString
	// operands index it; the executors place the values at the front of the
	// object buffer so quickValue refs resolve through the same objects slice
	// as string locals (switch case tests, string comparisons).
	stringConsts      []engine.Value
	callTarget        *Program
	nativePlan        *nativeInputPlan
	nativeNumberArgs  uint16
	nativePreassigned uint64
	nativeTrace       bool
	traceExitDepths   []uint8
	// traceExceptionExits marks exit IDs whose OpTraceExit is an exception
	// exit: the executor pops the stack top into DeoptExit.PendingException
	// instead of restoring it, and the VM throws that value on resume. The
	// slice is aligned with traceExitDepths (nil means no exception exits).
	traceExceptionExits []bool
	traceCallGuards     []traceCallGuard
	traceMethodGuards   []traceMethodGuard
	// traceUpvalues are the Number-valued captured cells the trace may read and
	// write. OpLoadUpvalueNum / OpStoreUpvalueNum operands index it. Only trace
	// programs populate it; the cells are Go pointers, so a program with
	// upvalues is never published as machine code (see lowerNativeInputsForMode).
	traceUpvalues []traceUpvalue
}

func (p *Program) DumpIR() string {
	if p == nil {
		return "<nil>\n"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "params=%d locals=%d maxStack=%d selfUpvalue=%d\n", p.NumParams, p.NumLocals, p.MaxStack, p.SelfUpvalue)
	for i, in := range p.Code {
		fmt.Fprintf(&out, "%04d  %s", i, in.Op.String())
		switch in.Op {
		case OpConst:
			out.WriteByte(' ')
			out.WriteString(strconv.FormatFloat(in.Value, 'g', -1, 64))
		case OpConstString:
			fmt.Fprintf(&out, " %q", in.Name)
		case OpLoadLocal, OpStoreLocal, OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep,
			OpSelfCall, OpTraceExit, OpGuardNoopCall, OpLoadUpvalueNum, OpStoreUpvalueNum:
			fmt.Fprintf(&out, " %d", in.Operand)
			if in.Op == OpTraceExit && int(in.Operand) < len(p.traceExceptionExits) && p.traceExceptionExits[in.Operand] {
				out.WriteString(" (exception)")
			}
			if (in.Op == OpLoadUpvalueNum || in.Op == OpStoreUpvalueNum) && int(in.Operand) < len(p.traceUpvalues) {
				fmt.Fprintf(&out, " (upvalue %d)", p.traceUpvalues[in.Operand].index)
			}
		case OpGetProp, OpSetProp:
			fmt.Fprintf(&out, " %q", in.Name)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func (p *Program) ReturnsUndefined() bool {
	return p != nil && len(p.Code) == 1 && p.Code[0].Op == OpReturnUndef
}

func (p *Program) IsTrivialThisPropertyGetter() bool {
	return p != nil && len(p.Code) == 3 &&
		p.Code[0].Op == OpLoadLocal && p.Code[0].Operand == 0 &&
		p.Code[1].Op == OpGetProp && p.Code[2].Op == OpReturn
}

func (p *Program) RequiresSelf() (int, bool) {
	return p.SelfUpvalue, p != nil && p.SelfUpvalue >= 0
}

// UsesNativeSelfCall 报告程序的机器码是否运行在 F1 自递归模式（直接形态
// OpSelfCall 站点硬编码为 JMP entry 0 的机器码自递归）。该模式的执行前提
// 是分发侧已验证 upvalue == 当前闭包（quickCallSelf）；quickCallBound 时
// upvalue 是另一闭包，自递归模式会把"调用 upvalue"错执行为自递归，必须
// 留在 Quick。内联成功的程序（无 OpSelfCall 站点）恒为 false。
func (p *Program) UsesNativeSelfCall() bool {
	return p != nil && p.hasSelfCall
}

// BindCallTarget specializes OpSelfCall sites to a guarded monomorphic
// callee. The interpreter bridge owns the closure identity guard.
func (p *Program) BindCallTarget(target *Program) (bool, error) {
	if p == nil || target == nil {
		return false, fmt.Errorf("jit: invalid call target")
	}
	if _, required := p.RequiresSelf(); !required {
		return false, fmt.Errorf("jit: program has no call target upvalue")
	}
	if _, nested := target.RequiresSelf(); nested {
		return false, fmt.Errorf("jit: nested specialized calls are unsupported")
	}
	// A specialized (non-inlined) callee executes recursively against the
	// caller's object buffer, which only carries the caller's string
	// constants. Reject callees with their own constant pool instead of
	// running OpConstString against misaligned refs.
	if len(target.stringConsts) != 0 {
		return false, fmt.Errorf("jit: callee string constants unsupported in specialized calls")
	}
	if p.inlineCallTarget(target) {
		// 内联后不再有任何 OpSelfCall 站点（含直接形态），F1 Native 自递归
		// 模式关闭：机器码入口/返回按普通模式发射，避免 RecBase 徒劳初始化。
		p.hasSelfCall = false
		// 内联改变了 IR：旧的机器码（可能按自递归模式发射）已失效——继续使用
		// 会让执行侧按 hasSelfCall=false 跳过 recBuf 分配，而旧机器码仍按
		// RecBase 寻址（RecBase=0 崩溃）。只清引用不 Close——释放与记账由
		// 调用方（bridge 的 dropNative）在调用本方法前完成。
		p.nativeCode = nil
		p.nativePlan = nil
		return true, nil
	}
	p.callTarget = target
	return false, nil
}

// inlineCallTarget inlines the callee into every OpSelfCall site of the
// caller. R4-1 extends the site analysis beyond "one OpPushSelf directly
// before each call": a caller may also route the self callee token through a
// local (`let target = callee; ...; target(a, b); target(c, d);` compiles to
// OpPushSelf; OpStoreLocal X; ...; OpLoadLocal X; args; OpSelfCall at each
// site). Both shapes are inlined; every other OpSelfCall shape (callee from a
// parameter, a reassigned local, a nested call result, a different argument
// count) falls back to the guarded non-inlined callTarget path, which remains
// correct because the self token round-trips through locals at runtime.
func (p *Program) inlineCallTarget(target *Program) bool {
	if len(target.Code) < 2 || target.Code[len(target.Code)-1].Op != OpReturn ||
		target.NumParams > 8 || p.NumLocals+target.NumParams > maxQuickSlots {
		return false
	}
	for i, in := range target.Code {
		if i == len(target.Code)-1 {
			continue
		}
		switch in.Op {
		case OpConst, OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow, OpNeg, OpNot, OpBitNot, OpUnaryPlus,
			OpEq, OpNe, OpStrictEq, OpStrictNe, OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr,
			OpLt, OpLe, OpGt, OpGe, OpPop, OpDup, OpSwap, OpGetProp:
		case OpConstString:
			if int(in.Operand) >= len(target.stringConsts) {
				return false
			}
		case OpLoadLocal:
			if in.Operand == 0 || int(in.Operand) > target.NumParams {
				return false
			}
		default:
			return false
		}
	}
	// --- R4-1 site analysis -------------------------------------------------
	// selfLocals marks locals that provably hold the self callee token: a
	// local stored directly from OpPushSelf (the bytecode compiler emits
	// LOAD_UPVALUE; STORE_LOCAL for `let target = callee;`). A store from any
	// other source removes the local (a reassigned callee local can hold an
	// arbitrary closure at the call site). selfStores records those exact
	// store instructions so the rewrite below can drop them together with
	// their OpPushSelf (an inlined site never pushes the callee).
	selfLocals := make([]bool, p.NumLocals)
	selfStores := make([]bool, len(p.Code))
	pendingSelf := false
	for i, in := range p.Code {
		switch in.Op {
		case OpPushSelf:
			pendingSelf = true
		case OpStoreLocal:
			if pendingSelf && int(in.Operand) < len(selfLocals) {
				selfLocals[in.Operand] = true
				selfStores[i] = true
			} else if int(in.Operand) < len(selfLocals) {
				selfLocals[in.Operand] = false
			}
			pendingSelf = false
		default:
			pendingSelf = false
		}
	}
	// Every OpSelfCall must be a provable self call with the exact callee
	// arity: the callee is pushed first, then the argument expressions are
	// evaluated on top of it. A backward depth walk over the argument region
	// locates the callee source: `above` counts the values above the callee
	// position (starting at the argument count at the call); an instruction
	// that would execute below that position (before < 0) is the callee
	// source, and an instruction that would pop below it (need > before) is
	// rejected — e.g. `target(x + 1, x * 2)` is fine, a nested call or a
	// jump inside the region is not. The callee source must be either a
	// direct OpPushSelf or a load of a selfLocal. calleeLoad records the
	// selfLocal loads that feed sites; they become dead code once the store
	// and the sites are inlined.
	calleeLoad := make(map[int]bool)
	sites := 0
	for i, in := range p.Code {
		if in.Op != OpSelfCall {
			continue
		}
		if int(in.Operand) != target.NumParams {
			return false
		}
		above := int(in.Operand)
		source := -1
		for j := i - 1; j >= 0; j-- {
			need, delta := 0, 0
			switch p.Code[j].Op {
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
				return false
			}
			before := above - delta
			if before < 0 {
				// This instruction pushed the value occupying the callee
				// position: it is the callee source.
				source = j
				break
			}
			if p.Code[j].Op == OpPushSelf || need > before {
				// A second OpPushSelf inside the argument region is impossible
				// (a leaf caller has exactly one callee upvalue), and an
				// instruction that pops below the callee position breaks the
				// self-call shape.
				return false
			}
			above = before
		}
		if source < 0 {
			return false
		}
		switch p.Code[source].Op {
		case OpPushSelf:
		case OpLoadLocal:
			slot := int(p.Code[source].Operand)
			if slot >= len(selfLocals) || !selfLocals[slot] {
				return false
			}
			calleeLoad[source] = true
		default:
			return false
		}
		sites++
	}
	if sites == 0 {
		return false
	}
	// Dead-code safety: a selfLocal store is only dropped when every load of
	// the local feeds an inlined site. Any other use (an argument, a store to
	// another local, arithmetic) observes the self token and forbids the
	// inlining.
	for i, in := range p.Code {
		if in.Op == OpLoadLocal && int(in.Operand) < len(selfLocals) &&
			selfLocals[in.Operand] && !calleeLoad[i] {
			return false
		}
	}

	base := p.NumLocals
	oldToNew := make([]int, len(p.Code))
	type jumpFixup struct {
		index     int
		oldTarget int
	}
	fixups := make([]jumpFixup, 0, 4)
	code := make([]Instr, 0, len(p.Code)+sites*len(target.Code))
	for oldIndex, in := range p.Code {
		oldToNew[oldIndex] = len(code)
		if in.Op == OpPushSelf {
			// Direct call-site self push, dropped: an inlined site never
			// needs the callee on the operand stack.
			continue
		}
		if in.Op == OpStoreLocal && selfStores[oldIndex] {
			// The store consumed the dropped OpPushSelf; it is dead once its
			// selfLocal loads are inlined.
			continue
		}
		if calleeLoad[oldIndex] {
			// A selfLocal load feeding an inlined site; the value it would
			// push is never observed by the inlined computation.
			continue
		}
		if in.Op != OpSelfCall {
			code = append(code, in)
			switch in.Op {
			case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
				fixups = append(fixups, jumpFixup{index: len(code) - 1, oldTarget: int(in.Operand)})
			}
			continue
		}
		for arg := target.NumParams - 1; arg >= 0; arg-- {
			code = append(code, Instr{Op: OpStoreLocal, Operand: uint32(base + arg)})
		}
		constBase := len(p.stringConsts)
		for _, targetInstr := range target.Code[:len(target.Code)-1] {
			if targetInstr.Op == OpLoadLocal {
				targetInstr.Operand = uint32(base + int(targetInstr.Operand) - 1)
			}
			if targetInstr.Op == OpConstString {
				targetInstr.Operand = uint32(constBase + int(targetInstr.Operand))
			}
			code = append(code, targetInstr)
		}
	}
	for _, fixup := range fixups {
		if fixup.oldTarget < 0 || fixup.oldTarget >= len(oldToNew) {
			return false
		}
		code[fixup.index].Operand = uint32(oldToNew[fixup.oldTarget])
	}
	oldCode, oldLocals, oldGuards, oldConsts := p.Code, p.NumLocals, p.propertyGuards, p.stringConsts
	p.Code = code
	p.NumLocals += target.NumParams
	p.stringConsts = append(p.stringConsts, target.stringConsts...)
	p.propertyGuards = make([]propertyGuard, len(code))
	if err := p.Verify(); err != nil {
		p.Code, p.NumLocals, p.propertyGuards, p.stringConsts = oldCode, oldLocals, oldGuards, oldConsts
		return false
	}
	return true
}
