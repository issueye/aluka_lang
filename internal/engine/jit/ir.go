// Package jit contains the portable, conservative JIT tiers.
//
// Tier 1 intentionally uses a small typed IR and a Go executor. It is a real
// compilation boundary (bytecode is lowered and verified before execution),
// while keeping the machine-code backend optional and platform-specific.
package jit

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

type Op uint8

const (
	OpConst Op = iota
	OpConstString
	OpLoadLocal
	OpStoreLocal
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpNeg
	OpNot
	OpBitNot
	OpUnaryPlus
	OpEq
	OpNe
	OpStrictEq
	OpStrictNe
	OpBitAnd
	OpBitOr
	OpBitXor
	OpShl
	OpShr
	OpUShr
	OpLt
	OpLe
	OpGt
	OpGe
	OpPop
	OpReturn
	OpReturnUndef
	OpJump
	OpJumpTrue
	OpJumpFalse
	OpJumpTrueKeep
	OpJumpFalseKeep
	OpJumpNullishKeep
	OpPushSelf
	OpSelfCall
	OpDup
	OpSwap
	OpGetProp
	OpTraceExit
	OpSetProp
	OpGuardNoopCall
	OpGuardMethodGet
)

type Instr struct {
	Op      Op
	Operand uint32
	Value   float64
	Name    string
}

func (op Op) String() string {
	names := [...]string{
		"const", "const_string", "load_local", "store_local", "add_f64", "sub_f64", "mul_f64", "div_f64", "mod_f64", "pow_f64",
		"neg_f64", "not_bool", "bit_not_i32", "number_identity",
		"eq_f64", "ne_f64", "strict_eq", "strict_ne", "bit_and_i32", "bit_or_i32", "bit_xor_i32", "shl_i32", "shr_i32", "ushr_u32", "lt_f64", "le_f64", "gt_f64", "ge_f64", "pop", "return", "return_undef", "jump", "jump_true",
		"jump_false", "jump_true_keep", "jump_false_keep", "jump_nullish_keep", "push_self", "self_call", "dup", "swap", "get_prop", "trace_exit", "set_prop", "guard_noop_call", "guard_method_get",
	}
	if int(op) >= len(names) {
		return fmt.Sprintf("op_%d", op)
	}
	return names[op]
}

type Program struct {
	NumParams      int
	NumLocals      int
	MaxStack       int
	SelfUpvalue    int
	Code           []Instr
	nativeCode     *jitnative.Code
	propertyGuards []propertyGuard
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
			OpSelfCall, OpTraceExit, OpGuardNoopCall:
			fmt.Fprintf(&out, " %d", in.Operand)
			if in.Op == OpTraceExit && int(in.Operand) < len(p.traceExceptionExits) && p.traceExceptionExits[in.Operand] {
				out.WriteString(" (exception)")
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

const maxQuickSlots = 32

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

// Verify validates stack effects and control-flow joins before a program runs.
func (p *Program) Verify() error {
	if len(p.Code) == 0 {
		return fmt.Errorf("jit: empty program")
	}
	if p.traceExceptionExits != nil && len(p.traceExceptionExits) != len(p.traceExitDepths) {
		return fmt.Errorf("jit: exception map size %d != deopt map size %d", len(p.traceExceptionExits), len(p.traceExitDepths))
	}
	depthAt := make([]int, len(p.Code))
	for i := range depthAt {
		depthAt[i] = -1
	}
	depthAt[0] = 0
	work := []int{0}
	maxDepth := 0
	reachableReturn := false
	reachableFunctionReturn := false
	reachableTraceExit := false
	for len(work) > 0 {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		depth := depthAt[i]
		in := p.Code[i]
		need, delta := 0, 0
		switch in.Op {
		case OpConst, OpLoadLocal, OpPushSelf:
			delta = 1
		case OpConstString:
			// The operand indexes the per-program string constant pool; a
			// missing or out-of-range entry is a malformed program even when
			// the pool is empty (the pool is only populated by the compilers
			// before Verify runs).
			if int(in.Operand) >= len(p.stringConsts) {
				return fmt.Errorf("jit: string constant index out of range")
			}
			delta = 1
		case OpGetProp:
			need, delta = 1, 0
		case OpGuardNoopCall:
			// Trace-only protocol op: the callee identity guard is part of the
			// deopt/commit protocol and must reference a recorded call guard.
			if p.traceExitDepths == nil {
				return fmt.Errorf("jit: guard_noop_call requires a trace program with deopt exits")
			}
			if int(in.Operand) >= len(p.traceCallGuards) {
				return fmt.Errorf("jit: guard_noop_call references missing call guard %d", in.Operand)
			}
			need, delta = 1, 0
		case OpGuardMethodGet:
			if p.traceExitDepths == nil {
				return fmt.Errorf("jit: guard_method_get requires a trace program with deopt exits")
			}
			if int(in.Operand) >= len(p.traceMethodGuards) {
				return fmt.Errorf("jit: guard_method_get references missing method guard %d", in.Operand)
			}
			need, delta = 1, 0
		case OpSetProp:
			// A property write is an irreversible side effect. It is only
			// legal in trace programs, whose exits and budget yields commit
			// deferred writes through the two-phase protocol; a function-level
			// program has no commit points and must be side-effect free.
			if p.traceExitDepths == nil {
				return fmt.Errorf("jit: side effect set_prop requires a trace program with deopt exits")
			}
			need, delta = 2, -2
		case OpStoreLocal, OpPop:
			need, delta = 1, -1
		case OpDup:
			need, delta = 1, 1
		case OpSwap:
			need = 2
		case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow, OpEq, OpNe, OpStrictEq, OpStrictNe,
			OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr, OpLt, OpLe, OpGt, OpGe:
			need, delta = 2, -1
		case OpNeg, OpNot, OpBitNot, OpUnaryPlus:
			need = 1
		case OpSelfCall:
			need = int(in.Operand) + 1
			delta = -int(in.Operand)
		case OpJump:
		case OpJumpTrue, OpJumpFalse:
			need, delta = 1, -1
		case OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			// The fallthrough path discards the left operand before evaluating
			// the right side; the jump path keeps it as the expression result.
			need, delta = 1, -1
		case OpReturn:
			need, delta = 1, -1
			reachableReturn = true
			reachableFunctionReturn = true
		case OpReturnUndef:
			reachableReturn = true
			reachableFunctionReturn = true
		case OpTraceExit:
			reachableTraceExit = true
			exitID := int(in.Operand)
			// A trace exit must reference an entry in the deopt map. Missing,
			// out-of-range or negative exit IDs are malformed IR and must be
			// rejected even when the operand stack happens to be empty; the
			// map is only ever populated by CompileTraceWithGuards before
			// Verify runs.
			if exitID < 0 || exitID >= len(p.traceExitDepths) {
				return fmt.Errorf("jit: trace exit %d has no deopt map", exitID)
			}
			// A non-nil exception map must cover every exit; nil means the
			// program has no exception exits (all exits are normal side
			// exits). A truncated map is a missing exception-state mapping.
			isException := false
			if p.traceExceptionExits != nil {
				isException = p.traceExceptionExits[exitID]
			}
			// An exception exit carries the thrown value on the stack top,
			// which the executor moves into DeoptExit.PendingException; the
			// recoverable stack depth is therefore one less, and the value
			// must be present (stack underflow is a malformed exception
			// exit).
			recoverDepth := depth
			if isException {
				if depth < 1 {
					return fmt.Errorf("jit: exception exit %d stack underflow at %d", exitID, i)
				}
				recoverDepth = depth - 1
			}
			if p.traceExitDepths[exitID] != ^uint8(0) && p.traceExitDepths[exitID] > 8 {
				return fmt.Errorf("jit: trace exit %d deopt map stack depth is too deep", exitID)
			}
			if p.traceExitDepths[exitID] == ^uint8(0) {
				if recoverDepth > 8 {
					return fmt.Errorf("jit: trace exit stack is too deep at %d", i)
				}
				p.traceExitDepths[exitID] = uint8(recoverDepth)
			} else if int(p.traceExitDepths[exitID]) != recoverDepth {
				return fmt.Errorf("jit: trace exit stack depth mismatch at %d", i)
			}
			reachableReturn = true
		default:
			return fmt.Errorf("jit: invalid IR opcode at %d", i)
		}
		if depth < need {
			return fmt.Errorf("jit: stack underflow at %d", i)
		}
		depth += delta
		if depth > maxDepth {
			maxDepth = depth
		}
		if maxDepth > maxQuickSlots {
			return fmt.Errorf("jit: operand stack is too deep")
		}
		// The executors place the string constant pool at the front of the
		// object buffer; a pool larger than the buffer is unrepresentable.
		if len(p.stringConsts) > maxQuickSlots {
			return fmt.Errorf("jit: string constant pool is too large")
		}
		type successor struct {
			instruction int
			depth       int
		}
		var successors []successor
		switch in.Op {
		case OpReturn, OpReturnUndef, OpTraceExit:
		case OpJump:
			successors = append(successors, successor{instruction: int(in.Operand), depth: depth})
		case OpJumpTrue, OpJumpFalse:
			successors = append(successors,
				successor{instruction: i + 1, depth: depth},
				successor{instruction: int(in.Operand), depth: depth})
		case OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			successors = append(successors,
				successor{instruction: i + 1, depth: depth},
				successor{instruction: int(in.Operand), depth: depth + 1})
		default:
			successors = append(successors, successor{instruction: i + 1, depth: depth})
		}
		for _, successor := range successors {
			next, nextDepth := successor.instruction, successor.depth
			if next < 0 || next >= len(p.Code) {
				return fmt.Errorf("jit: control flow leaves program at %d", i)
			}
			if depthAt[next] == -1 {
				depthAt[next] = nextDepth
				work = append(work, next)
			} else if depthAt[next] != nextDepth {
				return fmt.Errorf("jit: inconsistent stack depth at %d", next)
			}
		}
	}
	if !reachableReturn {
		return fmt.Errorf("jit: no reachable return")
	}
	if p.traceExitDepths != nil {
		if reachableFunctionReturn {
			return fmt.Errorf("jit: trace program reaches function return outside the side-effect commit protocol")
		}
		if !reachableTraceExit {
			return fmt.Errorf("jit: trace program has no reachable deopt exit")
		}
	}
	p.MaxStack = maxDepth
	return nil
}

type ExitReason uint8

const (
	Executed ExitReason = iota
	Yielded
	Interrupted
	GuardFailed
	Malformed
)

// Execute runs the typed program. false means a guard failed and the caller
// must execute the original bytecode. It never mutates JS objects.
func (p *Program) Execute(thisVal engine.Value, args []engine.Value) (engine.Value, ExitReason, error) {
	return p.ExecuteWithSafepoint(thisVal, args, 0, nil)
}

// ExecuteWithSafepoint runs the typed program and polls at loop backedges and
// recursive calls after each budget interval.
func (p *Program) ExecuteWithSafepoint(thisVal engine.Value, args []engine.Value, budget uint32, poll Safepoint) (engine.Value, ExitReason, error) {
	if p == nil {
		return engine.Undefined(), Malformed, nil
	}
	var argBuf [8]quickValue
	var objectBuf [maxQuickSlots]engine.Value
	objectCount := 0
	// The string constant pool occupies the front of the object buffer so
	// OpConstString refs and string locals share one objects slice.
	for _, constant := range p.stringConsts {
		if objectCount >= len(objectBuf) {
			return engine.Undefined(), GuardFailed, nil
		}
		objectBuf[objectCount] = constant
		objectCount++
	}
	if len(args) > len(argBuf) {
		return engine.Undefined(), GuardFailed, nil
	}
	for i, arg := range args {
		value := fromEngine(arg, &objectBuf, &objectCount)
		if value.kind == quickInvalid || value.kind == quickSelf {
			return engine.Undefined(), GuardFailed, nil
		}
		argBuf[i] = value
	}
	quickThis := fromEngine(thisVal, &objectBuf, &objectCount)
	var safepoint *quickSafepoint
	if budget != 0 || poll != nil {
		if budget == 0 {
			budget = 65536
		}
		safepoint = &quickSafepoint{interval: budget, remaining: budget, poll: poll}
	}
	result, reason, err := p.executeQuick(quickThis, argBuf[:len(args)], &objectBuf, &objectCount, 0, safepoint)
	if err != nil || reason != Executed {
		return engine.Undefined(), reason, err
	}
	return result.toEngine(objectBuf[:objectCount]), Executed, nil
}

type quickSafepoint struct {
	interval  uint32
	remaining uint32
	poll      Safepoint
}

func (s *quickSafepoint) tick() error {
	if s == nil {
		return nil
	}
	if s.remaining > 1 {
		s.remaining--
		return nil
	}
	s.remaining = s.interval
	return runSafepoint(s.poll)
}

func runSafepoint(poll Safepoint) error {
	runtime.Gosched()
	if poll != nil {
		return poll()
	}
	return nil
}

// executeQuick runs the typed program. `objects` is the shared object buffer:
// args and `this` populate it through fromEngine, and R3-4/R3-5 results
// (String concats, BigInt results) are appended to it via quickAlloc. The
// fixed-size buffer forces a GuardFailed fallback to Tier 0 when it is
// exhausted; Tier 0 never observes the buffer.
func (p *Program) executeQuick(thisVal quickValue, args []quickValue, objects *[maxQuickSlots]engine.Value, objectCount *int, depth int, safepoint *quickSafepoint) (quickValue, ExitReason, error) {
	if depth > 4096 {
		return quickValue{}, GuardFailed, nil
	}
	var localBuf [maxQuickSlots]quickValue
	locals := localBuf[:p.NumLocals]
	if p.NumLocals > 0 {
		locals[0] = thisVal
	}
	for i := 1; i <= p.NumParams && i < len(locals); i++ {
		locals[i] = quickValue{kind: quickUndefined}
	}
	for i, arg := range args {
		if i+1 >= len(locals) {
			break
		}
		locals[i+1] = arg
	}
	var stackBuf [maxQuickSlots]quickValue
	stack := stackBuf[:0]
	push := func(n quickValue) { stack = append(stack, n) }
	pop := func() quickValue { n := stack[len(stack)-1]; stack = stack[:len(stack)-1]; return n }
	for ip := 0; ip < len(p.Code); {
		in := p.Code[ip]
		ip++
		switch in.Op {
		case OpConst:
			push(numberValue(in.Value))
		case OpConstString:
			// The pool is prepended by ExecuteWithSafepoint; a ref beyond the
			// buffer is a defensive guard (Verify bounds the pool already).
			if int(in.Operand) >= len(objects) {
				return quickValue{}, GuardFailed, nil
			}
			truthy, _ := objects[in.Operand].Bool()
			push(quickValue{kind: quickString, ref: uint8(in.Operand), b: truthy})
		case OpLoadLocal:
			push(locals[in.Operand])
		case OpStoreLocal:
			locals[in.Operand] = pop()
		case OpGetProp:
			object := pop()
			if object.kind != quickObject {
				return quickValue{}, GuardFailed, nil
			}
			guard := &p.propertyGuards[ip-1]
			number, ok := guard.loadNumber(objects[object.ref], in.Name)
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(number))
		case OpPushSelf:
			push(quickValue{kind: quickSelf})
		case OpAdd:
			r, l := pop(), pop()
			switch {
			case l.isNumber() && r.isNumber():
				push(numberValue(l.num + r.num))
			case l.kind == quickString && r.kind == quickString:
				// R3-4: both operands guarded to String; the concat allocates
				// only here in Quick and the result stays a quickString so it
				// can feed truthiness / nullish / Return / strict equality.
				result, ok := quickStringConcat(l, r, objects, objectCount)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			case l.kind == quickBigInt && r.kind == quickBigInt:
				// R3-5: same-type BigInt addition.
				result, ok := quickBigIntArith(l, r, objects, objectCount, OpAdd)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			default:
				// Mixed types (String+Number coercion, BigInt+Number TypeError,
				// ...) fall back to Tier 0.
				return quickValue{}, GuardFailed, nil
			}
		case OpSub, OpMul, OpDiv, OpMod:
			r, l := pop(), pop()
			if l.isNumber() && r.isNumber() {
				var n float64
				switch in.Op {
				case OpSub:
					n = l.num - r.num
				case OpMul:
					n = l.num * r.num
				case OpDiv:
					n = l.num / r.num
				case OpMod:
					n = floatMod(l.num, r.num)
				}
				push(numberValue(n))
			} else if l.kind == quickBigInt && r.kind == quickBigInt {
				// R3-5: same-type BigInt arithmetic; division/modulo by zero
				// falls back so Tier 0 raises the identical RangeError.
				result, ok := quickBigIntArith(l, r, objects, objectCount, in.Op)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			} else {
				return quickValue{}, GuardFailed, nil
			}
		case OpPow:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(math.Pow(l.num, r.num)))
		case OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr:
			r, l := pop(), pop()
			if l.isNumber() && r.isNumber() {
				left, right := quickInt32(l.num), quickUint32(r.num)
				switch in.Op {
				case OpBitAnd:
					push(numberValue(float64(left & quickInt32(r.num))))
				case OpBitOr:
					push(numberValue(float64(left | quickInt32(r.num))))
				case OpBitXor:
					push(numberValue(float64(left ^ quickInt32(r.num))))
				case OpShl:
					push(numberValue(float64(left << (right & 31))))
				case OpShr:
					push(numberValue(float64(left >> (right & 31))))
				case OpUShr:
					push(numberValue(float64(quickUint32(l.num) >> (right & 31))))
				}
			} else if l.kind == quickBigInt && r.kind == quickBigInt {
				// R3-5: same-type BigInt bitwise ops. `>>>` and negative shifts
				// fall back so Tier 0 raises the identical TypeError/RangeError.
				result, ok := quickBigIntBitwise(l, r, objects, objectCount, in.Op)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			} else {
				return quickValue{}, GuardFailed, nil
			}
		case OpNeg:
			n := pop()
			switch {
			case n.isNumber():
				push(numberValue(-n.num))
			case n.kind == quickBigInt:
				// R3-5: unary minus on BigInt.
				result, ok := quickBigIntNeg(n, objects, objectCount)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			default:
				return quickValue{}, GuardFailed, nil
			}
		case OpNot:
			value := pop()
			truth, ok := value.truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			push(booleanValue(!truth))
		case OpBitNot:
			n := pop()
			switch {
			case n.isNumber():
				push(numberValue(float64(^quickInt32(n.num))))
			case n.kind == quickBigInt:
				// R3-5: BigInt bitwise NOT with the correct ES semantics
				// (~x = -x-1). Tier 0's OpBitNot does not dispatch BigInt and
				// yields Number(-1) for every BigInt input (recorded Tier 0
				// bug); Quick intentionally computes the correct result, so
				// differential generators must not route BigInt through `~`.
				result, ok := quickBigIntNot(n, objects, objectCount)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			default:
				return quickValue{}, GuardFailed, nil
			}
		case OpUnaryPlus:
			n := pop()
			if !n.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(n)
		case OpEq, OpNe:
			r, l := pop(), pop()
			// R3-3: == / != on primitives execute here per JS semantics via
			// the shared helper; object operands (and the recorded Tier 0
			// string-parse / BigInt-Boolean divergences) guard-fail so Tier 0
			// computes the identical result.
			equal, ok := quickLooseEqual(l, r, objects[:*objectCount])
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if in.Op == OpNe {
				equal = !equal
			}
			push(booleanValue(equal))
		case OpLt, OpLe, OpGt, OpGe:
			r, l := pop(), pop()
			var b bool
			switch {
			case l.isNumber() && r.isNumber():
				switch in.Op {
				case OpLt:
					b = l.num < r.num
				case OpLe:
					b = l.num <= r.num
				case OpGt:
					b = l.num > r.num
				case OpGe:
					b = l.num >= r.num
				}
			case l.kind == quickString && r.kind == quickString:
				// R3-4: same-type String relational comparison, ordered exactly
				// like Tier 0's compareValues.
				cmp, ok := quickStringCompare(l, r, objects)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				b = quickRelational(cmp, in.Op)
			case l.kind == quickBigInt && r.kind == quickBigInt:
				// R3-5: same-type BigInt relational comparison.
				cmp, ok := quickBigIntCompare(l, r, objects)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				b = quickRelational(cmp, in.Op)
			default:
				// Mixed / coerced comparisons (BigInt vs Number, String vs
				// Number, ...) fall back to Tier 0.
				return quickValue{}, GuardFailed, nil
			}
			push(booleanValue(b))
		case OpStrictEq, OpStrictNe:
			r, l := pop(), pop()
			equal, ok := strictQuickEqual(l, r, objects[:*objectCount])
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if in.Op == OpStrictNe {
				equal = !equal
			}
			push(booleanValue(equal))
		case OpPop:
			_ = pop()
		case OpDup:
			push(stack[len(stack)-1])
		case OpSwap:
			n := len(stack) - 1
			stack[n], stack[n-1] = stack[n-1], stack[n]
		case OpJump:
			if int(in.Operand) < ip-1 {
				if err := safepoint.tick(); err != nil {
					return quickValue{}, Interrupted, err
				}
			}
			ip = int(in.Operand)
		case OpJumpTrue, OpJumpFalse:
			truth, ok := pop().truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if (in.Op == OpJumpTrue && truth) || (in.Op == OpJumpFalse && !truth) {
				if int(in.Operand) < ip-1 {
					if err := safepoint.tick(); err != nil {
						return quickValue{}, Interrupted, err
					}
				}
				ip = int(in.Operand)
			}
		case OpJumpTrueKeep, OpJumpFalseKeep:
			truth, ok := stack[len(stack)-1].truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			take := in.Op == OpJumpTrueKeep && truth || in.Op == OpJumpFalseKeep && !truth
			if take {
				if int(in.Operand) < ip-1 {
					if err := safepoint.tick(); err != nil {
						return quickValue{}, Interrupted, err
					}
				}
				ip = int(in.Operand)
			} else {
				_ = pop()
			}
		case OpJumpNullishKeep:
			nullish, ok := stack[len(stack)-1].nullish()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if !nullish {
				if int(in.Operand) < ip-1 {
					if err := safepoint.tick(); err != nil {
						return quickValue{}, Interrupted, err
					}
				}
				ip = int(in.Operand)
			} else {
				_ = pop()
			}
		case OpSelfCall:
			if err := safepoint.tick(); err != nil {
				return quickValue{}, Interrupted, err
			}
			n := int(in.Operand)
			var recursiveArgs [8]quickValue
			for i := n - 1; i >= 0; i-- {
				recursiveArgs[i] = pop()
			}
			callee := pop()
			if callee.kind != quickSelf {
				return quickValue{}, GuardFailed, nil
			}
			target := p
			if p.callTarget != nil {
				target = p.callTarget
			}
			result, reason, err := target.executeQuick(quickValue{}, recursiveArgs[:n], objects, objectCount, depth+1, safepoint)
			if err != nil || reason != Executed {
				return quickValue{}, reason, err
			}
			push(result)
		case OpReturn:
			return pop(), Executed, nil
		case OpReturnUndef:
			return quickValue{kind: quickUndefined}, Executed, nil
		default:
			return quickValue{}, Malformed, fmt.Errorf("jit: invalid IR opcode")
		}
	}
	return quickValue{}, Malformed, fmt.Errorf("jit: fell off program")
}

type quickKind uint8

const (
	quickInvalid quickKind = iota
	quickUndefined
	quickNull
	quickNumber
	quickBoolean
	quickSelf
	quickObject
	quickString
	quickBigInt
	quickSymbol
)

type quickValue struct {
	num  float64
	kind quickKind
	ref  uint8
	b    bool
}

const quickUint32Range = 4294967296.0

func quickUint32(n float64) uint32 {
	if math.IsNaN(n) || math.IsInf(n, 0) || n == 0 {
		return 0
	}
	n = math.Mod(math.Trunc(n), quickUint32Range)
	if n < 0 {
		n += quickUint32Range
	}
	return uint32(n)
}

func quickInt32(n float64) int32 { return int32(quickUint32(n)) }

func numberValue(n float64) quickValue { return quickValue{kind: quickNumber, num: n} }
func booleanValue(b bool) quickValue   { return quickValue{kind: quickBoolean, b: b} }
func (v quickValue) isNumber() bool    { return v.kind == quickNumber }

func (v quickValue) truthy() (bool, bool) {
	switch v.kind {
	case quickBoolean:
		return v.b, true
	case quickNumber:
		return v.num != 0 && !math.IsNaN(v.num), true
	case quickUndefined, quickNull:
		return false, true
	case quickObject:
		return true, true
	case quickString, quickBigInt:
		return v.b, true
	case quickSymbol:
		// Symbols are always truthy (no falsy Symbol exists).
		return true, true
	default:
		return false, false
	}
}

func (v quickValue) nullish() (bool, bool) {
	switch v.kind {
	case quickUndefined, quickNull:
		return true, true
	case quickNumber, quickBoolean, quickObject, quickString, quickBigInt, quickSymbol:
		return false, true
	default:
		return false, false
	}
}

func fromEngine(v engine.Value, objects *[maxQuickSlots]engine.Value, objectCount *int) quickValue {
	if v == nil {
		return quickValue{}
	}
	if v.IsUndefined() {
		return quickValue{kind: quickUndefined}
	}
	if v.IsNull() {
		return quickValue{kind: quickNull}
	}
	if v.Type() == engine.TypeNumber {
		n, _ := v.Float()
		return numberValue(n)
	}
	if v.Type() == engine.TypeBoolean {
		b, _ := v.Bool()
		return booleanValue(b)
	}
	if v.IsObject() {
		if *objectCount >= len(objects) {
			return quickValue{}
		}
		ref := *objectCount
		objects[ref] = v
		*objectCount++
		return quickValue{kind: quickObject, ref: uint8(ref)}
	}
	if v.Type() == engine.TypeString || v.Type() == engine.TypeBigInt {
		if *objectCount >= len(objects) {
			return quickValue{}
		}
		ref := *objectCount
		objects[ref] = v
		*objectCount++
		truthy, _ := v.Bool()
		kind := quickString
		if v.Type() == engine.TypeBigInt {
			kind = quickBigInt
		}
		return quickValue{kind: kind, ref: uint8(ref), b: truthy}
	}
	if v.Type() == engine.TypeSymbol {
		// R3-1: Symbols join the Quick value domain as opaque references
		// (identity is the only observable property: always truthy, never
		// nullish, strict equality by pointer identity).
		if *objectCount >= len(objects) {
			return quickValue{}
		}
		ref := *objectCount
		objects[ref] = v
		*objectCount++
		return quickValue{kind: quickSymbol, ref: uint8(ref)}
	}
	return quickValue{}
}

func (v quickValue) toEngine(objects []engine.Value) engine.Value {
	switch v.kind {
	case quickNumber:
		return engine.Number(v.num)
	case quickBoolean:
		return engine.Boolean(v.b)
	case quickNull:
		return engine.Null()
	case quickUndefined:
		return engine.Undefined()
	case quickObject:
		if int(v.ref) < len(objects) {
			return objects[v.ref]
		}
		return engine.Undefined()
	case quickString, quickBigInt, quickSymbol:
		if int(v.ref) < len(objects) {
			return objects[v.ref]
		}
		return engine.Undefined()
	default:
		return engine.Undefined()
	}
}

func strictQuickEqual(left, right quickValue, values []engine.Value) (bool, bool) {
	if left.kind == quickInvalid || right.kind == quickInvalid || left.kind == quickSelf || right.kind == quickSelf {
		return false, false
	}
	if left.kind != right.kind {
		return false, true
	}
	switch left.kind {
	case quickUndefined, quickNull:
		return true, true
	case quickNumber:
		return left.num == right.num, true
	case quickBoolean:
		return left.b == right.b, true
	case quickString:
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		return values[left.ref].String() == values[right.ref].String(), true
	case quickBigInt:
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		leftInt, leftOK := engine.BigIntValue(values[left.ref])
		rightInt, rightOK := engine.BigIntValue(values[right.ref])
		return leftOK && rightOK && leftInt.Cmp(rightInt) == 0, leftOK && rightOK
	case quickSymbol:
		// R3-2: Symbol strict equality is pure identity — the same symbol
		// object is equal only to itself, never to another symbol (even with
		// the same description) and never to any other type. There is no
		// coercion: different kinds already returned false above.
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		return values[left.ref] == values[right.ref], true
	case quickObject:
		if int(left.ref) >= len(values) || int(right.ref) >= len(values) {
			return false, false
		}
		return values[left.ref] == values[right.ref], true
	default:
		return false, false
	}
}

func floatMod(a, b float64) float64 {
	// math.Mod is kept in a helper so the executor's operation semantics stay
	// explicit and easy to compare with the interpreter.
	return math.Mod(a, b)
}
