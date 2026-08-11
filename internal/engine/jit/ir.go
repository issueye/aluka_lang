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
		"const", "load_local", "store_local", "add_f64", "sub_f64", "mul_f64", "div_f64", "mod_f64", "pow_f64",
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
	NumParams         int
	NumLocals         int
	MaxStack          int
	SelfUpvalue       int
	Code              []Instr
	nativeCode        *jitnative.Code
	propertyGuards    []propertyGuard
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
	if p.inlineCallTarget(target) {
		return true, nil
	}
	p.callTarget = target
	return false, nil
}

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
		case OpLoadLocal:
			if in.Operand == 0 || int(in.Operand) > target.NumParams {
				return false
			}
		default:
			return false
		}
	}
	callCount, tokenCount := 0, 0
	for _, in := range p.Code {
		if in.Op == OpSelfCall {
			if int(in.Operand) != target.NumParams {
				return false
			}
			callCount++
		} else if in.Op == OpPushSelf {
			tokenCount++
		}
	}
	if callCount == 0 || tokenCount != callCount {
		return false
	}

	base := p.NumLocals
	oldToNew := make([]int, len(p.Code))
	type jumpFixup struct {
		index     int
		oldTarget int
	}
	fixups := make([]jumpFixup, 0, 4)
	code := make([]Instr, 0, len(p.Code)+callCount*len(target.Code))
	for oldIndex, in := range p.Code {
		oldToNew[oldIndex] = len(code)
		if in.Op == OpPushSelf {
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
		for _, targetInstr := range target.Code[:len(target.Code)-1] {
			if targetInstr.Op == OpLoadLocal {
				targetInstr.Operand = uint32(base + int(targetInstr.Operand) - 1)
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
	oldCode, oldLocals, oldGuards := p.Code, p.NumLocals, p.propertyGuards
	p.Code = code
	p.NumLocals += target.NumParams
	p.propertyGuards = make([]propertyGuard, len(code))
	if err := p.Verify(); err != nil {
		p.Code, p.NumLocals, p.propertyGuards = oldCode, oldLocals, oldGuards
		return false
	}
	return true
}

const maxQuickSlots = 32

// CompileLeaf lowers a function template when its body is a straight-line
// numeric expression. Unsupported semantics are rejected before execution.
func CompileLeaf(tmpl *bytecode.FuncTemplate) (*Program, error) {
	if tmpl == nil || tmpl.IsAsync || tmpl.IsGenerator || tmpl.IsVarArgs ||
		(tmpl.ArgumentsSlot >= 0 && !tmpl.NoArgumentsObject) {
		return nil, fmt.Errorf("jit: function is not a leaf candidate")
	}
	if len(tmpl.Code) == 0 || len(tmpl.Code)%bytecode.InstrSize != 0 {
		return nil, fmt.Errorf("jit: malformed bytecode")
	}
	p := &Program{NumParams: tmpl.NumParams, NumLocals: tmpl.NumLocals, SelfUpvalue: -1}
	if p.NumLocals > maxQuickSlots {
		return nil, fmt.Errorf("jit: too many locals")
	}
	pcToIR := make(map[int]int, len(tmpl.Code)/bytecode.InstrSize)
lower:
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
			if int(arg) >= len(tmpl.Constants) {
				return nil, fmt.Errorf("jit: constant index out of range")
			}
			if tmpl.Constants[arg].Type() != engine.TypeNumber {
				return nil, fmt.Errorf("jit: non-number constant")
			}
			n, _ := tmpl.Constants[arg].Float()
			p.Code = append(p.Code, Instr{Op: OpConst, Value: n})
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
			break lower
		case bytecode.OpReturnUndef:
			p.Code = append(p.Code, Instr{Op: OpReturnUndef})
			break lower
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
	if len(p.Code) == 0 || (p.Code[len(p.Code)-1].Op != OpReturn && p.Code[len(p.Code)-1].Op != OpReturnUndef) {
		return nil, fmt.Errorf("jit: no return")
	}
	if err := p.Verify(); err != nil {
		return nil, err
	}
	p.propertyGuards = make([]propertyGuard, len(p.Code))
	return p, nil
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
	result, reason, err := p.executeQuick(quickThis, argBuf[:len(args)], objectBuf[:objectCount], 0, safepoint)
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

func (p *Program) executeQuick(thisVal quickValue, args []quickValue, objects []engine.Value, depth int, safepoint *quickSafepoint) (quickValue, ExitReason, error) {
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
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(l.num + r.num))
		case OpSub:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(l.num - r.num))
		case OpMul:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(l.num * r.num))
		case OpDiv:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(l.num / r.num))
		case OpMod:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(floatMod(l.num, r.num)))
		case OpPow:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(math.Pow(l.num, r.num)))
		case OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
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
		case OpNeg:
			n := pop()
			if !n.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(-n.num))
		case OpNot:
			value := pop()
			truth, ok := value.truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			push(booleanValue(!truth))
		case OpBitNot:
			n := pop()
			if !n.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(float64(^quickInt32(n.num))))
		case OpUnaryPlus:
			n := pop()
			if !n.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(n)
		case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			var b bool
			switch in.Op {
			case OpEq:
				b = l.num == r.num
			case OpNe:
				b = l.num != r.num
			case OpLt:
				b = l.num < r.num
			case OpLe:
				b = l.num <= r.num
			case OpGt:
				b = l.num > r.num
			case OpGe:
				b = l.num >= r.num
			}
			push(booleanValue(b))
		case OpStrictEq, OpStrictNe:
			r, l := pop(), pop()
			equal, ok := strictQuickEqual(l, r, objects)
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
			result, reason, err := target.executeQuick(quickValue{}, recursiveArgs[:n], objects, depth+1, safepoint)
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

type propertyGuardEntry struct {
	shapeID uint64
	slot    int
}

type propertyGuard struct {
	entries [2]propertyGuardEntry
	count   uint8
}

func (g *propertyGuard) loadNumber(object engine.Value, name string) (float64, bool) {
	for i := uint8(0); i < g.count; i++ {
		entry := g.entries[i]
		if number, ok := engine.GuardedNumericOwnProperty(object, name, entry.shapeID, entry.slot); ok {
			return number, true
		}
	}
	number, shapeID, slot, ok := engine.NumericOwnProperty(object, name)
	if !ok || g.count >= uint8(len(g.entries)) {
		return 0, false
	}
	g.entries[g.count] = propertyGuardEntry{shapeID: shapeID, slot: slot}
	g.count++
	return number, true
}

func (g *propertyGuard) storeNumber(object engine.Value, name string, number float64) bool {
	for i := uint8(0); i < g.count; i++ {
		entry := g.entries[i]
		if engine.GuardedSetNumericOwnProperty(object, name, entry.shapeID, entry.slot, number) {
			return true
		}
	}
	return false
}

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
	default:
		return false, false
	}
}

func (v quickValue) nullish() (bool, bool) {
	switch v.kind {
	case quickUndefined, quickNull:
		return true, true
	case quickNumber, quickBoolean, quickObject, quickString, quickBigInt:
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
	case quickString, quickBigInt:
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
