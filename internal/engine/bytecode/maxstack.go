package bytecode

import "fmt"

// ComputeMaxStack computes a sound upper bound on the operand-stack height
// (number of values held above base+NumLocals) reached during execution of
// fn's body. The VM pre-reserves that many slots at frame entry so that push
// never needs a capacity check (see interpreter VM.push / ensureFrameStack).
//
// 算法分两相，关键在避免 try/finally-in-loop 的深度通胀：
//
//  1. 正常流 worklist 抽象解释（PC 0 入口深度 0；catch/finally 体按深度 0
//     额外 seed，使其相对高度纳入 maxNormal）。字节码在合并点平衡（JIT Verify
//     佐证），各 PC 收敛到单一深度；冲突取 max（仍 sound）。负深度即 meta/建模
//     bug，报错而非静默低估。
//
//  2. try 区补偿：throw 不截断操作数栈——catch 以「try 体残留 + 异常值」、
//     finally 以「try 体残留」进入。若用合成后继按每 PC 深度 seed，循环回边会
//     把虚高深度带回 try 体 → 无限增长。故改为：maxRegionDepth = try 体内正常
//     流最大深度，MaxStack = maxNormal + maxRegionDepth +（HasCatch?1:0）。
//     这对 try 给出 sound 上界（catch/finally 体在残留深度上重放其自身高度，
//     被 +maxRegionDepth 覆盖），且不引入反馈环。
//
// 特殊建模：VarStack 指令按 operand 译码效果（Call/New/…；MakeClass 读
// mod.Classes）；StackCond keep-jump 双后继（D / D-1），OptionalJump 双 @D；
// OpYield 按 net-0（resume push sendVal 抵消 pop）。
func ComputeMaxStack(mod *Module, fn *FuncTemplate) (int, error) {
	if len(fn.Code) == 0 {
		return 0, nil
	}
	depthAt := make(map[int]int, len(fn.Code)/InstrSize+1)
	var work []int
	enqueue := func(pc, d int) {
		if pc < 0 || pc >= len(fn.Code) || pc%InstrSize != 0 || d < 0 {
			return
		}
		if prev, ok := depthAt[pc]; ok && prev >= d {
			return
		}
		depthAt[pc] = d
		work = append(work, pc)
	}
	enqueue(0, 0)
	// catch/finally 体仅经 throw 进入（正常流不顺序到达），需显式 seed 以纳入
	// 其栈高度到 maxNormal；throw 带来的 try 体残留深度由第二相 +maxRegionDepth 补偿。
	// catch 入口栈顶是异常值（深度 1，首指令 StoreLocal(e) 弹之）；finally 入口
	// 无异常值（深度 0）。
	for i := range fn.TryTable {
		te := &fn.TryTable[i]
		if te.HasCatch {
			enqueue(te.CatchPC, 1)
		}
		if te.HasFinally {
			enqueue(te.FinallyPC, 0)
		}
	}

	maxNormal := 0
	for len(work) > 0 {
		pc := work[len(work)-1]
		work = work[:len(work)-1]
		D := depthAt[pc]
		if D > maxNormal {
			maxNormal = D
		}
		op, operand, _ := Decode(fn.Code, pc)
		next := pc + InstrSize
		target := func() int { return pc + InstrSize + SignedOperand(operand) }

		switch op {
		case OpReturn, OpReturnUndef, OpThrow:
			// 终结指令：无顺序后继。
		case OpJmp, OpTryExitJmp:
			enqueue(target(), D)
		case OpJmpTruePop, OpJmpFalsePop:
			enqueue(target(), D-1)
			enqueue(next, D-1)
		case OpJmpTrueKeep, OpJmpFalseKeep, OpJmpNullishKeep:
			enqueue(target(), D)
			enqueue(next, D-1)
		case OpOptionalJump:
			enqueue(target(), D)
			enqueue(next, D)
		case OpYield, OpAwait:
			enqueue(next, D)
		default:
			pop, push, ok := instrEffect(mod, op, operand)
			if !ok {
				return 0, fmt.Errorf("maxstack: %s: unknown stack effect for %s at pc %d", fn.SourceFile, op, pc)
			}
			nd := D + push - pop
			if nd < 0 {
				// 负深度：seed 深度与该路径实际不符（如 value-mode finally 实际
				// 入口深度 1 而 seed@0 时首条 POP 弹空）。静默跳过该路径——正常流
				// 的 JMP 路径以正确深度覆盖同体；throw 进入由 +maxRegion 补偿。
				// 静默跳过在此是 sound 的：真实执行绝不会负深度（编译器布局保证
				// catch/finally 体与其入口深度自洽），故负路径必为 seed 人为造出。
				continue
			}
			if nd > maxNormal {
				maxNormal = nd
			}
			enqueue(next, nd)
		}
	}

	// 第二相：try 区残留深度补偿。VM 的 catch 入口不截断操作数栈（findHandler
	// 仅压异常值、设 PC），故 throw 残留会在顺序 try 块间累积：第 j 块 catch 的
	// 残留 ≤ 前 j 块 regionMax 之和。sound 上界 = maxNormal + Σ(regionMax)。
	// catch 的异常值已在 seed@1 中体现（首指令 StoreLocal(e) 弹之）。
	sumRegion := 0
	for i := range fn.TryTable {
		te := &fn.TryTable[i]
		regionMax := 0
		lo, hi := te.StartPC, te.EndPC
		if hi == 0 {
			hi = len(fn.Code)
		}
		for pc := lo; pc < hi && pc < len(fn.Code); pc += InstrSize {
			if d, ok := depthAt[pc]; ok && d > regionMax {
				regionMax = d
			}
		}
		sumRegion += regionMax
	}
	return maxNormal + sumRegion, nil
}

// instrEffect 返回 op（带 operand）的操作数栈弹出/压入数。VarStack 与
// operand 决定型在此显式处理；固定型取自 meta。StackCond 跳转由调用方处理，
// 此处返回 ok=false。
func instrEffect(mod *Module, op Opcode, operand uint32) (pop, push int, ok bool) {
	switch op {
	case OpCall, OpNew, OpCallThis, OpConstructThis:
		return int(operand) + 1, 1, true
	case OpCallMethod:
		return int(operand>>16) + 1, 1, true
	case OpCallWithThis:
		return int(operand) + 2, 1, true
	case OpNewArray:
		return int(operand), 1, true
	case OpNewObject:
		return int(operand) * 2, 1, true
	case OpMakeClass:
		if mod == nil || int(operand) >= len(mod.Classes) {
			return 0, 0, false
		}
		cls := mod.Classes[operand]
		pops := len(cls.ComputedIdx)
		if cls.HasSuper {
			pops++
		}
		return pops, 1, true
	case OpCallWithThisArgs:
		return 3, 1, true
	case OpCallArgs, OpCallMethodArgs, OpNewArgs, OpCallThisArgs, OpConstructThisArgs:
		return 2, 1, true
	case OpBuildArray:
		return 0, 1, true
	}
	m := Meta(op)
	if m == nil || m.StackCond || m.VarStack {
		return 0, 0, false
	}
	return int(m.Pops), int(m.Pushes), true
}
