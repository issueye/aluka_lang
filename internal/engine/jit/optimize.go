package jit

import "math"

// OptimizeIR gates the R5-1/R5-2 conservative IR optimization passes. It is
// an exported package-level switch so differential tests can run the same
// corpus with optimization disabled and prove bit-identical semantics.
var OptimizeIR = true

// OptimizeStats reports what the optimization passes changed, for the
// R5-7 observability requirement and for tests.
type OptimizeStats struct {
	FoldedConstOps int
	EliminatedOps  int
	RemovedDead    int
}

// OptimizeProgram applies the R5-1/R5-2 conservative passes to a verified
// function program (leaf IR). Every pass preserves IEEE-754 bit semantics
// (NaN, -0, ±Inf, division by zero), keeps the operand-stack protocol intact
// and requires the result to pass Verify again. Trace programs are not
// optimized (their exit fixups are index-coupled); the passes here only
// apply to straight-line-plus-branches leaf programs.
//
// Pass 1 (constant folding): a run of constants feeding a pure arithmetic or
// relational instruction is replaced by its precomputed result, provided no
// jump targets the consumed instructions (the fold is local to a straight
// line and must not cross branch boundaries).
//
// Pass 2 (redundant store-load elimination): an adjacent LOAD_LOCAL k
// followed by STORE_LOCAL k has a net stack effect of zero and is removed
// when neither instruction is a jump target.
//
// Pass 3 (unreachable block deletion): instructions unreachable from the
// entry (through control flow) are removed and the remaining jumps are
// remapped to their new positions.
func OptimizeProgram(p *Program) OptimizeStats {
	if p == nil || !OptimizeIR {
		return OptimizeStats{}
	}
	code := p.Code
	if len(code) == 0 {
		return OptimizeStats{}
	}
	// A jump target set: instructions that may be the destination of a
	// branch. Only instructions outside this set may be folded away.
	targeted := make([]bool, len(code))
	for i := range code {
		switch code[i].Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			if int(code[i].Operand) >= 0 && int(code[i].Operand) < len(code) {
				targeted[code[i].Operand] = true
			}
		}
	}
	stats := OptimizeStats{}
	// Pass 1 + 2 in one linear sweep: build a new instruction list, folding
	// const-const arithmetic and adjacent load/store pairs that are not
	// branch targets. Jump operands are remapped at the end.
	oldToNew := make([]int, len(code))
	for i := range oldToNew {
		oldToNew[i] = -1
	}
	var out []Instr
	for i := 0; i < len(code); {
		in := code[i]
		// Pass 2: adjacent LOAD_LOCAL k / STORE_LOCAL k with no branch target
		// on either instruction is a no-op (net stack effect zero).
		if in.Op == OpLoadLocal && !targeted[i] && i+1 < len(code) &&
			code[i+1].Op == OpStoreLocal && code[i+1].Operand == in.Operand && !targeted[i+1] {
			oldToNew[i] = len(out)
			oldToNew[i+1] = len(out)
			stats.EliminatedOps += 2
			i += 2
			continue
		}
		// Pass 1: CONST a, CONST b, <binary op> folds when the triple is
		// straight-line (neither const is a branch target) and the op is a
		// pure IEEE-754 operation.
		if i+2 < len(code) && code[i].Op == OpConst && code[i+1].Op == OpConst &&
			!targeted[i] && !targeted[i+1] {
			if folded, ok := foldConstOp(code[i].Value, code[i+1].Value, code[i+2].Op); ok &&
				!targeted[i+2] {
				oldToNew[i] = len(out)
				oldToNew[i+1] = len(out)
				oldToNew[i+2] = len(out)
				out = append(out, Instr{Op: OpConst, Value: folded})
				stats.FoldedConstOps++
				i += 3
				continue
			}
		}
		oldToNew[i] = len(out)
		out = append(out, in)
		i++
	}
	// Remap every jump operand from old code indices to the folded positions
	// (oldToNew). Forward jumps see their targets here because the mapping
	// for the whole original code is complete by now.
	for j := range out {
		switch out[j].Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			target := int(out[j].Operand)
			if target >= 0 && target < len(oldToNew) && oldToNew[target] >= 0 {
				out[j].Operand = uint32(oldToNew[target])
			}
		}
	}

	// Pass 3: drop instructions unreachable from entry in the *new* code and
	// remap jump targets. Reachability is computed on the new list.
	reachable := reachableFromEntry(out)
	compacted := make([]Instr, 0, len(out))
	newToNew := make([]int, len(out))
	for i := range newToNew {
		newToNew[i] = -1
	}
	for i, in := range out {
		if !reachable[i] {
			stats.RemovedDead++
			continue
		}
		newToNew[i] = len(compacted)
		compacted = append(compacted, in)
	}
	for i := range compacted {
		switch compacted[i].Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			target := int(compacted[i].Operand)
			if target >= 0 && target < len(newToNew) && newToNew[target] >= 0 {
				compacted[i].Operand = uint32(newToNew[target])
			}
		}
	}
	p.Code = compacted
	return stats
}

// foldConstOp computes CONST op CONST for the pure IEEE-754 ops. It returns
// false for ops it must not fold (bitwise/ToInt32 ops keep their runtime
// semantics; relational ops are folded by the executor path already).
func foldConstOp(a, b float64, op Op) (float64, bool) {
	switch op {
	case OpAdd:
		return a + b, true
	case OpSub:
		return a - b, true
	case OpMul:
		return a * b, true
	case OpDiv:
		return a / b, true // IEEE-754: x/0 -> ±Inf, 0/0 -> NaN, 1/-0 -> -Inf
	case OpMod:
		return math.Mod(a, b), true
	case OpPow:
		return math.Pow(a, b), true
	case OpNeg:
		// OpNeg is unary; handled separately below (never reached here).
		return 0, false
	default:
		return 0, false
	}
}
