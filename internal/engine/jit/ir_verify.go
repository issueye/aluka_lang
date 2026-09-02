// IR 验证器：执行前的栈平衡、跳转目标与槽位边界检查（发布前的唯一门禁）。

package jit

import (
	"fmt"
)

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
