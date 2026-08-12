package bytecode

import "fmt"

// OptimizationStats describes the changes made by OptimizeModule.
// Instructions are counted per function, including nested function templates.
type OptimizationStats struct {
	FunctionsBefore     int
	FunctionsAfter      int
	InstructionsBefore  int
	InstructionsAfter   int
	RemovedInstructions int
	FusedInstructions   int
	ThreadedJumps       int
}

// OptimizeModule applies semantics-preserving bytecode optimizations to every
// function in a module. The optimizer runs after AST transforms and before
// serialization, so it never changes the source-level module graph.
//
// The first pass is deliberately conservative:
//   - remove no-op instructions and literal/dup push-pop pairs;
//   - thread unconditional jump targets;
//   - fuse LOAD_LOCAL + GET_PROP into GET_PROP_LOCAL where operands fit.
//
// Any rewrite that changes instruction positions also relocates relative
// jumps, exception table PCs, and source line PCs.
func OptimizeModule(mod *Module) (OptimizationStats, error) {
	var stats OptimizationStats
	if mod == nil {
		return stats, fmt.Errorf("bytecode optimize: nil module")
	}
	stats.FunctionsBefore = len(mod.Functions)
	stats.FunctionsAfter = len(mod.Functions)
	for _, fn := range mod.Functions {
		if fn == nil {
			return stats, fmt.Errorf("bytecode optimize: nil function template")
		}
		before := len(fn.Code) / InstrSize
		stats.InstructionsBefore += before
		if err := validateFunc(fn); err != nil {
			return stats, err
		}
		fs, err := optimizeFunc(fn)
		if err != nil {
			return stats, err
		}
		stats.InstructionsAfter += len(fn.Code) / InstrSize
		stats.RemovedInstructions += fs.RemovedInstructions
		stats.FusedInstructions += fs.FusedInstructions
		stats.ThreadedJumps += fs.ThreadedJumps
	}
	return stats, ValidateModule(mod)
}

// ValidateModule checks bytecode structure and all PC-bearing metadata. It is
// exported so build and test code can validate optimizer output independently.
func ValidateModule(mod *Module) error {
	if mod == nil {
		return fmt.Errorf("bytecode validate: nil module")
	}
	for i, fn := range mod.Functions {
		if fn == nil {
			return fmt.Errorf("bytecode validate: function %d is nil", i)
		}
		if err := validateFunc(fn); err != nil {
			return err
		}
	}
	return nil
}

type decodedInstr struct {
	pc      int
	op      Opcode
	operand uint32
}

type rewriteGroup struct {
	oldPCs  []int
	ins     decodedInstr
	emit    bool
	oldJump int // target in the original stream; -1 means not a jump
}

type funcStats struct {
	RemovedInstructions int
	FusedInstructions   int
	ThreadedJumps       int
}

func optimizeFunc(fn *FuncTemplate) (funcStats, error) {
	var stats funcStats
	if len(fn.Code) == 0 {
		return stats, nil
	}
	ins := make([]decodedInstr, 0, len(fn.Code)/InstrSize)
	byPC := make(map[int]int, len(fn.Code)/InstrSize)
	for pc := 0; pc < len(fn.Code); pc += InstrSize {
		op, operand, _ := Decode(fn.Code, pc)
		ins = append(ins, decodedInstr{pc: pc, op: op, operand: operand})
		byPC[pc] = len(ins) - 1
	}

	// Control-entry PCs must remain valid when a two-instruction sequence is
	// removed or fused. Jumping into the second instruction would otherwise
	// change the stack shape at the target.
	controlTargets := make(map[int]bool)
	for _, in := range ins {
		if target, ok := relativeTarget(in); ok {
			if target >= 0 && target <= len(fn.Code) {
				controlTargets[target] = true
			}
		}
	}
	for _, te := range fn.TryTable {
		controlTargets[te.StartPC] = true
		if te.HasCatch {
			controlTargets[te.CatchPC] = true
		}
		if te.HasFinally {
			controlTargets[te.FinallyPC] = true
		}
	}

	// First decide local rewrites. We do not remove a pair if the second
	// instruction is a control entry; the pair may be entered from a jump.
	remove := make([]bool, len(ins))
	fuse := make([]bool, len(ins))
	for i := 0; i < len(ins); i++ {
		if ins[i].op == OpNop || (ins[i].op == OpJmp && SignedOperand(ins[i].operand) == 0) {
			remove[i] = true
			continue
		}
		if i+1 >= len(ins) || controlTargets[ins[i+1].pc] {
			continue
		}
		if isPurePush(ins[i].op) && ins[i+1].op == OpPop {
			remove[i] = true
			remove[i+1] = true
			i++
			continue
		}
		if ins[i].op == OpDup && ins[i+1].op == OpPop {
			remove[i] = true
			remove[i+1] = true
			i++
			continue
		}
		if ins[i].op == OpLoadLocal && ins[i+1].op == OpGetProp {
			slot := ins[i].operand
			nameIdx := ins[i+1].operand
			if slot <= 0xFF && nameIdx <= 0xFFFF {
				fuse[i] = true
				remove[i+1] = true
				i++
			}
		}
	}

	groups := make([]rewriteGroup, 0, len(ins))
	for i := 0; i < len(ins); {
		if fuse[i] {
			groups = append(groups, rewriteGroup{
				oldPCs: []int{ins[i].pc, ins[i+1].pc},
				ins: decodedInstr{
					pc:      ins[i].pc,
					op:      OpGetPropLocal,
					operand: ins[i].operand<<16 | (ins[i+1].operand & 0xFFFF),
				},
				emit:    true,
				oldJump: -1,
			})
			stats.FusedInstructions++
			i += 2
			continue
		}
		if remove[i] {
			groups = append(groups, rewriteGroup{oldPCs: []int{ins[i].pc}, emit: false})
			stats.RemovedInstructions++
			i++
			continue
		}
		groups = append(groups, rewriteGroup{
			oldPCs:  []int{ins[i].pc},
			ins:     ins[i],
			emit:    true,
			oldJump: jumpTargetOrMinusOne(ins[i]),
		})
		i++
	}
	// A removed pair is represented by two adjacent non-emitting groups. Count
	// the second instruction here because the loop above counts each one.

	oldToNew := make(map[int]int, len(ins)+1)
	emittedAt := make(map[int]bool, len(ins))
	groupPCs := make([]int, len(groups))
	newPC := 0
	for gi := range groups {
		g := &groups[gi]
		groupPCs[gi] = newPC
		for _, oldPC := range g.oldPCs {
			oldToNew[oldPC] = newPC
			emittedAt[oldPC] = g.emit
		}
		if g.emit {
			newPC += InstrSize
		}
	}
	oldToNew[len(fn.Code)] = newPC

	// Relocate and thread jumps after all old-to-new positions are known.
	for gi := range groups {
		g := &groups[gi]
		if !g.emit || g.oldJump < 0 {
			continue
		}
		target := g.oldJump
		if isUnconditionalJump(byPC, ins, target) {
			threaded := followUnconditionalJump(byPC, ins, target)
			if threaded != target {
				target = threaded
				stats.ThreadedJumps++
			}
		}
		newTarget, ok := oldToNew[target]
		if !ok {
			return stats, fmt.Errorf("bytecode optimize: jump target %d has no relocation", target)
		}
		currentPC := groupPCs[gi]
		offset := newTarget - (currentPC + InstrSize)
		if offset < -0x800000 || offset > 0x7FFFFF {
			return stats, fmt.Errorf("bytecode optimize: jump offset out of range at pc %d", currentPC)
		}
		g.ins.operand = uint32(offset)
	}

	newCode := make([]byte, 0, newPC)
	for _, g := range groups {
		if !g.emit {
			continue
		}
		Encode(&newCode, g.ins.op, g.ins.operand)
	}
	fn.Code = newCode
	if err := relocateMetadata(fn, oldToNew, emittedAt); err != nil {
		return stats, err
	}
	return stats, nil
}

func isPurePush(op Opcode) bool {
	switch op {
	case OpPushUndefined, OpPushNull, OpPushTrue, OpPushFalse, OpPushConst, OpPushInt, OpPushNegInt:
		return true
	default:
		return false
	}
}

func isRelativeJump(op Opcode) bool {
	switch op {
	case OpJmp, OpJmpTruePop, OpJmpFalsePop, OpJmpTrueKeep, OpJmpFalseKeep, OpJmpNullishKeep, OpOptionalJump, OpForInNext, OpTryExitJmp:
		return true
	default:
		return false
	}
}

func relativeTarget(in decodedInstr) (int, bool) {
	if !isRelativeJump(in.op) {
		return 0, false
	}
	return in.pc + InstrSize + SignedOperand(in.operand), true
}

func jumpTargetOrMinusOne(in decodedInstr) int {
	target, ok := relativeTarget(in)
	if !ok {
		return -1
	}
	return target
}

func isUnconditionalJump(byPC map[int]int, ins []decodedInstr, pc int) bool {
	i, ok := byPC[pc]
	return ok && ins[i].op == OpJmp
}

func followUnconditionalJump(byPC map[int]int, ins []decodedInstr, pc int) int {
	seen := make(map[int]bool)
	for isUnconditionalJump(byPC, ins, pc) && !seen[pc] {
		seen[pc] = true
		i := byPC[pc]
		target, ok := relativeTarget(ins[i])
		if !ok || target == pc {
			break
		}
		pc = target
	}
	return pc
}

func relocateMetadata(fn *FuncTemplate, oldToNew map[int]int, emittedAt map[int]bool) error {
	mapPC := func(oldPC int) (int, error) {
		newPC, ok := oldToNew[oldPC]
		if !ok {
			return 0, fmt.Errorf("bytecode optimize: metadata PC %d has no relocation", oldPC)
		}
		return newPC, nil
	}
	for i := range fn.TryTable {
		te := &fn.TryTable[i]
		var err error
		if te.StartPC, err = mapPC(te.StartPC); err != nil {
			return err
		}
		if te.HasCatch {
			if te.CatchPC, err = mapPC(te.CatchPC); err != nil {
				return err
			}
		}
		if te.HasFinally {
			if te.FinallyPC, err = mapPC(te.FinallyPC); err != nil {
				return err
			}
		}
		// 区域边界 PC（v18 起参与 try 展开判定）必须一并重定位。
		if te.EndPC, err = mapPC(te.EndPC); err != nil {
			return err
		}
		if te.HasCatch && te.CatchEndPC != 0 {
			if te.CatchEndPC, err = mapPC(te.CatchEndPC); err != nil {
				return err
			}
		}
		if te.HasFinally && te.FinallyEndPC != 0 {
			if te.FinallyEndPC, err = mapPC(te.FinallyEndPC); err != nil {
				return err
			}
		}
	}

	lines := make([]LineEntry, 0, len(fn.LineStarts))
	for _, line := range fn.LineStarts {
		if !emittedAt[line.PC] {
			continue
		}
		newPC, err := mapPC(line.PC)
		if err != nil {
			return err
		}
		entry := LineEntry{PC: newPC, Line: line.Line}
		if len(lines) > 0 && lines[len(lines)-1].PC == entry.PC {
			lines[len(lines)-1] = entry
		} else {
			lines = append(lines, entry)
		}
	}
	fn.LineStarts = lines
	return nil
}

func validateFunc(fn *FuncTemplate) error {
	if len(fn.Code)%InstrSize != 0 {
		return fmt.Errorf("bytecode validate: %q code length %d is not instruction-aligned", fn.SourceFile, len(fn.Code))
	}
	for pc := 0; pc < len(fn.Code); pc += InstrSize {
		op, operand, _ := Decode(fn.Code, pc)
		if op > OpEnd {
			return fmt.Errorf("bytecode validate: %q unknown opcode %d at pc %d", fn.SourceFile, op, pc)
		}
		if target, ok := relativeTarget(decodedInstr{pc: pc, op: op, operand: operand}); ok {
			if target < 0 || target > len(fn.Code) || target%InstrSize != 0 {
				return fmt.Errorf("bytecode validate: %q invalid jump target %d at pc %d", fn.SourceFile, target, pc)
			}
		}
	}
	for i, te := range fn.TryTable {
		if !validPC(te.StartPC, len(fn.Code)) || (te.HasCatch && !validPC(te.CatchPC, len(fn.Code))) || (te.HasFinally && !validPC(te.FinallyPC, len(fn.Code))) {
			return fmt.Errorf("bytecode validate: %q invalid try table entry %d", fn.SourceFile, i)
		}
	}
	lastPC := -1
	for _, line := range fn.LineStarts {
		if !validPC(line.PC, len(fn.Code)) || line.PC < lastPC {
			return fmt.Errorf("bytecode validate: %q invalid line PC %d", fn.SourceFile, line.PC)
		}
		lastPC = line.PC
	}
	return nil
}

func validPC(pc, codeLen int) bool {
	return pc >= 0 && pc <= codeLen && pc%InstrSize == 0
}
