package bytecode

import (
	"fmt"
	"math"
	"math/big"

	"github.com/aluka-lang/aluka/internal/engine"
)

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
		stats.InstructionsBefore += len(fn.Code) / InstrSize
		if err := validateFunc(fn); err != nil {
			return stats, err
		}
		// 多轮迭代直到收敛（上限 4 轮）：一轮的折叠/融合产物可能为下一轮
		// 提供新的删除机会（如 `1+2;` 表达式语句折叠后留下 PUSH_CONST; POP，
		// 由第二轮按纯 push-pop 对消除）。每轮指令数单调不增，必然收敛。
		for round := 0; round < 4; round++ {
			fs, err := optimizeFunc(fn)
			if err != nil {
				return stats, err
			}
			stats.RemovedInstructions += fs.RemovedInstructions
			stats.FusedInstructions += fs.FusedInstructions
			stats.ThreadedJumps += fs.ThreadedJumps
			if fs.RemovedInstructions == 0 && fs.FusedInstructions == 0 && fs.ThreadedJumps == 0 {
				break
			}
			if err := validateFunc(fn); err != nil {
				return stats, err
			}
		}
		stats.InstructionsAfter += len(fn.Code) / InstrSize
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
	oldPCs []int
	ins    decodedInstr
	// ins2 是融合产物的后续指令（如 STORE; LOAD → DUP; STORE 的两指令产物）；
	// 旧 PC 只映射到第一条，后续指令占据相邻的新 PC。
	ins2    []decodedInstr
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
	// fuse[i] 记录以 i 为起点的两指令融合产物序列（第一条 + 后续指令）。
	fuse := make(map[int][]decodedInstr, 4)
	// fold[i] 记录常量折叠产物：i 为 PUSH_CONST 起点，三条指令合一。
	fold := make(map[int]int, 4)

	// 不可达代码删除：RETURN/RETURN_UNDEF/THROW（IsTerminal）之后的顺序指令
	// 若不是控制入口（跳转目标/try 表 PC）则不可达。编译器会发出 return 后
	// 的隐式 return_undef 等死代码；try 表各入口均在 controlTargets 中，
	// finally/catch 块不会误删。
	inDead := false
	for i := range ins {
		if inDead && !controlTargets[ins[i].pc] {
			remove[i] = true
			continue
		}
		if m := Meta(ins[i].op); m != nil && m.IsTerminal {
			inDead = true
		} else {
			inDead = false
		}
	}

	for i := 0; i < len(ins); i++ {
		if remove[i] {
			continue
		}
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
				fuse[i] = []decodedInstr{{op: OpGetPropLocal, operand: slot<<16 | nameIdx}}
				remove[i+1] = true
				i++
				continue
			}
		}
		// STORE_LOCAL X; LOAD_LOCAL X → DUP; STORE_LOCAL X：
		// 赋值表达式的值回传（x = expr 后 LOAD 刚存的值），净栈效果等价
		// （DUP 复制栈顶、STORE 弹一份存入 X，栈上保留一份）。
		if ins[i].op == OpStoreLocal && ins[i+1].op == OpLoadLocal &&
			ins[i].operand == ins[i+1].operand {
			fuse[i] = []decodedInstr{
				{op: OpDup},
				{op: OpStoreLocal, operand: ins[i].operand},
			}
			remove[i+1] = true
			i++
			continue
		}
		// 常量折叠：PUSH_CONST a; PUSH_CONST b; 二元算术 → PUSH_CONST(结果)。
		// 仅折叠 VM 分派可证明一致的情形（见 foldConstants 注释）。
		if i+2 < len(ins) && !controlTargets[ins[i+1].pc] && !controlTargets[ins[i+2].pc] {
			if idx := foldConstants(fn, ins[i], ins[i+1], ins[i+2].op); idx >= 0 {
				fold[i] = idx
				remove[i] = true
				remove[i+1] = true
				remove[i+2] = true
				i += 2
				continue
			}
		}
	}

	groups := make([]rewriteGroup, 0, len(ins))
	for i := 0; i < len(ins); {
		if fused, ok := fuse[i]; ok {
			groups = append(groups, rewriteGroup{
				oldPCs:  []int{ins[i].pc, ins[i+1].pc},
				ins:     fused[0],
				ins2:    fused[1:],
				emit:    true,
				oldJump: -1,
			})
			stats.FusedInstructions++
			i += 2
			continue
		}
		if idx, ok := fold[i]; ok {
			groups = append(groups, rewriteGroup{
				oldPCs: []int{ins[i].pc, ins[i+1].pc, ins[i+2].pc},
				ins: decodedInstr{
					pc:      ins[i].pc,
					op:      OpPushConst,
					operand: uint32(idx),
				},
				emit:    true,
				oldJump: -1,
			})
			// 3 条旧指令合成 1 条：净删 2 条（与融合的净计口径一致）。
			stats.RemovedInstructions += 2
			i += 3
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
			newPC += InstrSize * (1 + len(g.ins2))
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
		for _, extra := range g.ins2 {
			Encode(&newCode, extra.op, extra.operand)
		}
	}
	fn.Code = newCode
	if err := relocateMetadata(fn, oldToNew, emittedAt); err != nil {
		return stats, err
	}
	return stats, nil
}

// isPurePush 报告指令是否为无副作用的纯字面量压栈。
// 语义来自集中式元数据表（meta.go 的 PurePush 标记）。
func isPurePush(op Opcode) bool {
	m := Meta(op)
	return m != nil && m.PurePush
}

// foldConstants 尝试将 PUSH_CONST a; PUSH_CONST b; 二元算术折叠为单个
// PUSH_CONST，仅在折叠结果与 VM 运行时分派完全一致的情形执行：
//
//   - number × number：IEEE754 double 运算（+ - * / 直接、% 用 math.Mod、
//     ** 用 math.Pow，均与 JS 数值语义逐位一致，含 -0/NaN/Infinity）；
//   - string + string（仅 OpAdd）：直接拼接（无类型转换）；
//   - bigint × bigint：+ - * / %（Quo/Rem 截断语义与 JS BigInt 一致；
//     除零在 JS 抛 RangeError，故不折叠；** 不折叠——负指数抛错且
//     结果可能超出合理内存）。
//
// 混合类型（string+number 等涉及 ToString/ToNumber 转换）一律不折叠。
// 返回新常量池索引；不可折叠返回 -1。
func foldConstants(fn *FuncTemplate, a, b decodedInstr, op Opcode) int {
	if a.op != OpPushConst || b.op != OpPushConst {
		return -1
	}
	la := fn.Constants[a.operand]
	rb := fn.Constants[b.operand]
	if la == nil || rb == nil {
		return -1
	}
	if op == OpAdd && la.Type() == engine.TypeString && rb.Type() == engine.TypeString {
		return fn.AddStringConst(la.String() + rb.String())
	}
	if la.Type() == engine.TypeNumber && rb.Type() == engine.TypeNumber {
		lf, _ := la.Float()
		rf, _ := rb.Float()
		var result float64
		switch op {
		case OpAdd:
			result = lf + rf
		case OpSub:
			result = lf - rf
		case OpMul:
			result = lf * rf
		case OpDiv:
			result = lf / rf
		case OpMod:
			result = math.Mod(lf, rf)
		case OpPow:
			result = math.Pow(lf, rf)
		default:
			return -1
		}
		return fn.AddConst(engine.Number(result))
	}
	if la.Type() == engine.TypeBigInt && rb.Type() == engine.TypeBigInt {
		lb, lbOK := engine.BigIntValue(la)
		rbv, rbOK := engine.BigIntValue(rb)
		if !lbOK || !rbOK {
			return -1
		}
		result := new(big.Int)
		switch op {
		case OpAdd:
			result.Add(lb, rbv)
		case OpSub:
			result.Sub(lb, rbv)
		case OpMul:
			result.Mul(lb, rbv)
		case OpDiv:
			if rbv.Sign() == 0 {
				return -1
			}
			result.Quo(lb, rbv)
		case OpMod:
			if rbv.Sign() == 0 {
				return -1
			}
			result.Rem(lb, rbv)
		default:
			return -1
		}
		return fn.AddConst(engine.BigInt(result))
	}
	return -1
}

// isRelativeJump 报告指令是否为带相对偏移的跳转类指令。
// 语义来自集中式元数据表（meta.go 的 IsJump 标记）。
func isRelativeJump(op Opcode) bool {
	m := Meta(op)
	return m != nil && m.IsJump
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
		if err := validateOperand(fn, op, operand, pc); err != nil {
			return err
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

// validateOperand 按集中式元数据表（meta.go 的 OperandKind）校验操作数范围。
// 仅校验有函数内上下文可判定的 kind；跳转目标/对齐校验由调用方负责，
// 模板索引与 try 表索引需要模块级上下文，不做函数级校验。
func validateOperand(fn *FuncTemplate, op Opcode, operand uint32, pc int) error {
	kind := OperandNone
	if m := Meta(op); m != nil {
		kind = m.Operand
	}
	bad := func(format string, args ...any) error {
		args = append([]any{fn.SourceFile, op.String()}, args...)
		return fmt.Errorf("bytecode validate: %q %s "+format+" at pc %d", args...)
	}
	switch kind {
	case OperandConstIdx:
		if uint32(len(fn.Constants)) <= operand {
			return bad("const index %d out of range (pool size %d)", operand, len(fn.Constants), pc)
		}
	case OperandSlot:
		if uint32(fn.NumLocals) <= operand {
			return bad("local slot %d out of range (NumLocals %d)", operand, fn.NumLocals, pc)
		}
	case OperandUpvalueIdx:
		if uint32(len(fn.Upvalues)) <= operand {
			return bad("upvalue index %d out of range (count %d)", operand, len(fn.Upvalues), pc)
		}
	case OperandPackedSlotName:
		slot := operand >> 16
		nameIdx := operand & 0xFFFF
		if uint32(fn.NumLocals) <= slot {
			return bad("packed local slot %d out of range (NumLocals %d)", slot, fn.NumLocals, pc)
		}
		if uint32(len(fn.Constants)) <= nameIdx {
			return bad("packed name index %d out of range (pool size %d)", nameIdx, len(fn.Constants), pc)
		}
	case OperandPackedCall:
		nameIdx := operand & 0xFFFF
		if uint32(len(fn.Constants)) <= nameIdx {
			return bad("packed name index %d out of range (pool size %d)", nameIdx, len(fn.Constants), pc)
		}
	}
	return nil
}

func validPC(pc, codeLen int) bool {
	return pc >= 0 && pc <= codeLen && pc%InstrSize == 0
}
