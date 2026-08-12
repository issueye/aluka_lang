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
		// 优化改变指令形态（如 STORE_LOCAL;LOAD_LOCAL→DUP;STORE_LOCAL 使峰值 +1），
		// 须在最终字节码上重算 MaxStack，覆盖 compiler.Compile 末尾基于优化前
		// 产物算出的值。EvalProgram 路径不经 OptimizeModule，沿用编译器算的值。
		ms, err := ComputeMaxStack(mod, fn)
		if err != nil {
			return stats, err
		}
		fn.MaxStack = ms
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
	// fold[i] 记录常量折叠产物（二元 PUSH a;PUSH b;<binop> 或单目 PUSH a;<unop>）：
	// out 为合成后的单条压栈指令，consume 为被消费的原指令数（二元 3 / 单目 2）。
	fold := make(map[int]foldMatch, 4)

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
		// 常量折叠：PUSH a; PUSH b; <binop>（二元）或 PUSH a; <unop>（单目）。
		// 仅折叠 VM 分派可证明一致的情形（见 tryFoldBinary/tryFoldUnary 注释）。
		// controlTargets 守卫确保无跳转落入序列中间（i+1 由循环顶部 line 178
		// 已保证有效且非控制入口；此处仅额外守护二元的 i+2）。
		if i+2 < len(ins) && !controlTargets[ins[i+2].pc] {
			if m, ok := tryFoldBinary(fn, ins[i], ins[i+1], ins[i+2].op); ok {
				fold[i] = m
				remove[i] = true
				remove[i+1] = true
				remove[i+2] = true
				i += 2
				continue
			}
		}
		if m, ok := tryFoldUnary(fn, ins[i], ins[i+1].op); ok {
			fold[i] = m
			remove[i] = true
			remove[i+1] = true
			i++
			continue
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
		if m, ok := fold[i]; ok {
			oldPCs := make([]int, m.consume)
			for k := 0; k < m.consume; k++ {
				oldPCs[k] = ins[i+k].pc
			}
			groups = append(groups, rewriteGroup{
				oldPCs: oldPCs,
				ins: decodedInstr{
					pc:      ins[i].pc,
					op:      m.out.op,
					operand: m.out.operand,
				},
				emit:    true,
				oldJump: -1,
			})
			// consume 条旧指令合成 1 条：净删 consume-1 条。
			stats.RemovedInstructions += m.consume - 1
			i += m.consume
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
	groupPCs := make([]int, len(groups))
	newPC := 0
	for gi := range groups {
		g := &groups[gi]
		groupPCs[gi] = newPC
		for _, oldPC := range g.oldPCs {
			// 非 emit 组不推进 newPC，故其 oldPC 映射到「下一个 emit 组」的
			// 起始 newPC——这正是被删指令向后续存活指令的 PC 转移点（行号表用）。
			oldToNew[oldPC] = newPC
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
	if err := relocateMetadata(fn, oldToNew); err != nil {
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

// foldMatch 描述一次常量折叠的产物：out 为合成后的单条压栈指令，
// consume 为被消费的原指令数（二元=3：PUSH a;PUSH b;<binop>，单目=2：
// PUSH a;<unop>）。out.op 可能是 PUSH_INT/PUSH_NEG_INT（结果是小整数，
// 无常量池开销）、PUSH_CONST（结果入池）、PUSH_TRUE/PUSH_FALSE（布尔）。
type foldMatch struct {
	out     decodedInstr
	consume int
}

// pushNumber 读取一条「数值压栈」指令的 float64 值，覆盖 PUSH_INT /
// PUSH_NEG_INT / PUSH_CONST(number)。非数值（string/bigint/undefined/nil
// 或越界常量索引）返回 ok=false。
func pushNumber(in decodedInstr, fn *FuncTemplate) (float64, bool) {
	switch in.op {
	case OpPushInt:
		return float64(in.operand), true
	case OpPushNegInt:
		return -float64(in.operand), true
	case OpPushConst:
		if int(in.operand) >= len(fn.Constants) {
			return 0, false
		}
		v := fn.Constants[in.operand]
		if v == nil || v.Type() != engine.TypeNumber {
			return 0, false
		}
		f, _ := v.Float()
		return f, true
	}
	return 0, false
}

// numberPush 把 float64 结果封装为最紧凑的压栈指令：
//   - 整数且落在 24 位有符号范围 [-(2²³-1)? ...]  → PUSH_INT/PUSH_NEG_INT；
//   - 否则（非整数、|值|≥2²⁴、NaN/Inf）→ 入常量池 PUSH_CONST。
//
// 与编译器 NumberLit 的发射口径完全一致（compiler.go:1641-1652），
// 故折叠产物可被 VM 与后续优化轮当作普通字面量处理。
func numberPush(fn *FuncTemplate, result float64) decodedInstr {
	// -0 不能用 PUSH_INT 0 表示：JS 中 -0 ≠ +0（1/-0 === -Inf，Object.is 区分），
	// VM 的 NEG(0) 产生 IEEE754 -0。故负零必须经 PUSH_CONST 保留符号位。
	if result == 0 && math.Signbit(result) {
		idx := fn.AddConst(engine.Number(result))
		return decodedInstr{op: OpPushConst, operand: uint32(idx)}
	}
	const bound = float64(int64(1) << 24) // 2^24
	if !math.IsNaN(result) && !math.IsInf(result, 0) &&
		result > -bound && result < bound && result == float64(int64(result)) {
		iv := int64(result)
		if iv >= 0 {
			return decodedInstr{op: OpPushInt, operand: uint32(iv)}
		}
		return decodedInstr{op: OpPushNegInt, operand: uint32(-iv)}
	}
	idx := fn.AddConst(engine.Number(result))
	return decodedInstr{op: OpPushConst, operand: uint32(idx)}
}

// evalNumberBinary 对两个 IEEE754 double 执行算术，返回结果与是否可折。
// + - * / 直接、% 用 math.Mod、** 用 math.Pow，均与 JS 数值语义逐位一致
// （含 -0/NaN/Infinity；如 1/0=+Inf、0/0=NaN 均可折叠）。位运算/比较
// 不在本路径（涉及 ToInt32/类型转换，见 tryFoldUnary 的 BitNot）。
func evalNumberBinary(lf, rf float64, op Opcode) (float64, bool) {
	switch op {
	case OpAdd:
		return lf + rf, true
	case OpSub:
		return lf - rf, true
	case OpMul:
		return lf * rf, true
	case OpDiv:
		return lf / rf, true
	case OpMod:
		return math.Mod(lf, rf), true
	case OpPow:
		return math.Pow(lf, rf), true
	}
	return 0, false
}

// tryFoldBinary 尝试折叠 PUSH a; PUSH b; <binop> → 单条 push，仅在结果与
// VM 运行时分派完全一致时执行：
//   - number × number（PUSH_INT/PUSH_NEG_INT/PUSH_CONST(number) 任意组合）；
//   - string + string（仅 OpAdd，两侧 PUSH_CONST 字符串，无类型转换）；
//   - bigint × bigint（+ - * / %，Quo/Rem 截断语义；除零抛 RangeError 故不折）。
//
// 混合类型（string+number 等）一律不折叠。不可折叠返回 ok=false。
func tryFoldBinary(fn *FuncTemplate, a, b decodedInstr, op Opcode) (foldMatch, bool) {
	// number × number：PUSH_INT/PUSH_NEG_INT/PUSH_CONST(number) 任意组合。
	if af, ok := pushNumber(a, fn); ok {
		if bf, ok := pushNumber(b, fn); ok {
			if r, ok := evalNumberBinary(af, bf, op); ok {
				return foldMatch{out: numberPush(fn, r), consume: 3}, true
			}
			return foldMatch{}, false
		}
	}
	// string/bigint 仅经 PUSH_CONST；非 PUSH_CONST 的 a/b 已被 number 路径排除。
	if a.op != OpPushConst || b.op != OpPushConst {
		return foldMatch{}, false
	}
	la, rb := fn.Constants[a.operand], fn.Constants[b.operand]
	if la == nil || rb == nil {
		return foldMatch{}, false
	}
	// string + string（仅 OpAdd）。
	if op == OpAdd && la.Type() == engine.TypeString && rb.Type() == engine.TypeString {
		idx := fn.AddStringConst(la.String() + rb.String())
		return foldMatch{out: decodedInstr{op: OpPushConst, operand: uint32(idx)}, consume: 3}, true
	}
	// bigint × bigint。
	if la.Type() == engine.TypeBigInt && rb.Type() == engine.TypeBigInt {
		if idx, ok := foldBigInt(fn, la, rb, op); ok {
			return foldMatch{out: decodedInstr{op: OpPushConst, operand: uint32(idx)}, consume: 3}, true
		}
	}
	return foldMatch{}, false
}

// foldBigInt 折叠 bigint × bigint 算术，返回新常量池索引。除零（JS 抛
// RangeError）与 ** （负指数抛错/结果可能过大）不折叠。
func foldBigInt(fn *FuncTemplate, la, rb engine.Value, op Opcode) (int, bool) {
	lb, ok1 := engine.BigIntValue(la)
	rbv, ok2 := engine.BigIntValue(rb)
	if !ok1 || !ok2 {
		return 0, false
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
			return 0, false
		}
		result.Quo(lb, rbv)
	case OpMod:
		if rbv.Sign() == 0 {
			return 0, false
		}
		result.Rem(lb, rbv)
	default:
		return 0, false
	}
	return fn.AddConst(engine.BigInt(result)), true
}

// tryFoldUnary 尝试折叠 PUSH a; <unop> → 单条 push，仅在结果与 VM 运行时
// 分派完全一致时执行：
//   - NEG：number 取负（-5 → PUSH_NEG_INT 5）；bigint 取负 → PUSH_CONST；
//   - NOT：仅确定布尔（PUSH_TRUE/FALSE）取反 → PUSH_FALSE/TRUE；
//   - BitNot：number 的 ~ToInt32（JS ToInt32 截断/wrap 语义）；
//   - UNARY_PLUS：number 的恒等（+5 → 5），消除冗余转换。
//
// 涉及 ToBoolean/ToString/ToNumber 转换的（如 !"str"、~undefined）不折叠。
func tryFoldUnary(fn *FuncTemplate, a decodedInstr, op Opcode) (foldMatch, bool) {
	switch op {
	case OpNeg:
		if f, ok := pushNumber(a, fn); ok {
			return foldMatch{out: numberPush(fn, -f), consume: 2}, true
		}
		// bigint 取反：结果仍 PUSH_CONST。
		if a.op == OpPushConst {
			if v := fn.Constants[a.operand]; v != nil && v.Type() == engine.TypeBigInt {
				if b, ok := engine.BigIntValue(v); ok {
					idx := fn.AddConst(engine.BigInt(new(big.Int).Neg(b)))
					return foldMatch{out: decodedInstr{op: OpPushConst, operand: uint32(idx)}, consume: 2}, true
				}
			}
		}
	case OpNot:
		// 仅确定布尔；其他类型需 ToBoolean 转换，不折。
		switch a.op {
		case OpPushTrue:
			return foldMatch{out: decodedInstr{op: OpPushFalse}, consume: 2}, true
		case OpPushFalse:
			return foldMatch{out: decodedInstr{op: OpPushTrue}, consume: 2}, true
		}
	case OpBitNot:
		// ~x = ^ToInt32(x)；ToInt32 对 NaN/±Inf 返回 0。
		if f, ok := pushNumber(a, fn); ok {
			return foldMatch{out: numberPush(fn, float64(^toInt32(f))), consume: 2}, true
		}
	case OpUnaryPlus:
		// +number = number（恒等）；非 number 需 ToNumber 转换，不折。
		if f, ok := pushNumber(a, fn); ok {
			return foldMatch{out: numberPush(fn, f), consume: 2}, true
		}
	}
	return foldMatch{}, false
}

// toInt32 实现 ECMAScript ToInt32（abstract operation）：
// 截断小数 → 数学 mod 2³² → 落入 [-2³¹, 2³¹) 的带符号 int32。
// NaN/±Inf 返回 0。供 BitNot 折叠使用。
func toInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	n := math.Trunc(f)
	if n == 0 {
		return 0
	}
	const two32 = float64(int64(1) << 32)
	posInt := math.Mod(n, two32) // math.Mod 结果符号同被除数
	if posInt < 0 {
		posInt += two32
	}
	if posInt >= float64(int64(1)<<31) {
		posInt -= two32
	}
	return int32(posInt)
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

func relocateMetadata(fn *FuncTemplate, oldToNew map[int]int) error {
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
		// 即便该 PC 的指令被删除（折叠/消除/不可达），也保留并重定位行号条目：
		// 被删 PC 经 mapPC 映射到「下一个存活指令」的 newPC，使行号转移给同行的
		// 存活指令（如 `1+2; foo()` —— 折叠删掉 1+2 的行起始指令，但同行的 foo()
		// 存活，仍需正确归因到该行）。若直接丢弃，存活同行动态指令会丢失行号。
		// 多个条目塌缩到同一 newPC 时「后者覆盖」：源 PC 顺序与 newPC 单调一致，
		// 存活行起始恒位于被删行起始之后，覆盖结果正确。
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
