package jit

import "fmt"

// regalloc.go 实现 M2 Native 寄存器分配的基础设施：
//
// 阶段 1（本文件）：局部变量活跃区间分析（liveness）与"热 local"识别。
// 阶段 2（后续）：基于 liveness 把热 local 分配到 XMM8-XMM15（操作数栈占用
// XMM0-XMM7），并在 yield / deopt 出口前 spill 回无指针 Frame。
//
// 当前 Native 模型：操作数栈映射到 XMM0-XMM7（depth 即寄存器号），locals
// 存放在无指针 Frame 的固定 offset（nativeLocalOffset），每次 LOAD_LOCAL /
// STORE_LOCAL 都是 Frame 内存往返。寄存器化的目标是消除循环内热 local 的
// 内存往返。
//
// 约束（继承自 R2 平台门禁）：Native 不保存 Go 指针、不调用 Go、不分配对象；
// 寄存器化的 local 必须是无符号数值（proven Number，见 nativeAssignedLocals），
// 且在所有 Go 可见出口（yield / deopt exit / 正常返回）前写回 Frame，保证
// VM 状态恢复与 Tier 0 一致。

// localLiveness 计算每个 IR 指令的"出口活跃 local 位图"（反向数据流分析）。
// live[i] 的第 j 位为 1 表示 local j 在指令 i 执行之后（沿任一后继路径）
// 仍会被读取，即其值在 i 处必须存活。
//
// def：OpStoreLocal 写入的 local；use：OpLoadLocal 读取的 local。
// 操作数栈上的值（非 local）不在本分析范围——它们由 depth 追踪管理。
func localLiveness(p *Program) ([]uint64, error) {
	if p == nil {
		return nil, fmt.Errorf("jit: nil program in liveness")
	}
	if p.NumLocals > 64 {
		return nil, fmt.Errorf("jit: liveness local limit exceeded (%d > 64)", p.NumLocals)
	}
	n := len(p.Code)
	live := make([]uint64, n)
	// 控制流：前驱 -> 后继。
	succ := make([][]int, n)
	for i, in := range p.Code {
		switch in.Op {
		case OpReturn, OpReturnUndef, OpTraceExit:
			// 无后继。
		case OpJump:
			succ[i] = append(succ[i], int(in.Operand))
		case OpJumpTrue, OpJumpFalse:
			succ[i] = append(succ[i], i+1, int(in.Operand))
		case OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			// Keep 分支保留栈顶，但 local 活跃性只与 LoadLocal/StoreLocal 有关。
			succ[i] = append(succ[i], i+1, int(in.Operand))
		default:
			succ[i] = append(succ[i], i+1)
		}
	}
	// 反向迭代直到不动点。IR 是 CFG（含回边），需 worklist。
	work := make([]int, 0, n)
	inWork := make([]bool, n)
	for i := n - 1; i >= 0; i-- {
		work = append(work, i)
		inWork[i] = true
	}
	// 快速路径：先按逆序做一轮传播（处理大部分直行），再用 worklist 收敛回边。
	changed := true
	iter := 0
	for changed {
		changed = false
		iter++
		if iter > n+1 {
			return nil, fmt.Errorf("jit: liveness did not converge")
		}
		for i := n - 1; i >= 0; i-- {
			out := uint64(0)
			for _, s := range succ[i] {
				if s < 0 || s >= n {
					return nil, fmt.Errorf("jit: liveness successor out of range")
				}
				out |= live[s]
			}
			// live[i] = (out - def[i]) ∪ use[i]
			in := p.Code[i]
			switch in.Op {
			case OpStoreLocal:
				out &^= uint64(1) << in.Operand
			case OpLoadLocal:
				out |= uint64(1) << in.Operand
			}
			if out != live[i] {
				live[i] = out
				changed = true
			}
		}
	}
	return live, nil
}

// hotLocalReport 汇总每个 local 的"活跃区间内读取次数"，用于识别值得
// 寄存器化的热 local。读次数越多，寄存器化收益越大。
//
// useCounts[j] 统计 local j 被 OpLoadLocal 读取的总次数；liveAcross[j]
// 统计 local j 在多少条指令处处于活跃态（近似活跃区间长度）。
type hotLocalReport struct {
	useCounts  []int
	liveAcross []int
}

// analyzeHotLocals 对程序做 liveness 分析并统计每个 local 的热度。
func analyzeHotLocals(p *Program) ([]uint64, hotLocalReport, error) {
	live, err := localLiveness(p)
	if err != nil {
		return nil, hotLocalReport{}, err
	}
	rep := hotLocalReport{
		useCounts:  make([]int, p.NumLocals),
		liveAcross: make([]int, p.NumLocals),
	}
	for i, in := range p.Code {
		if in.Op == OpLoadLocal {
			rep.useCounts[int(in.Operand)]++
		}
		for j := 0; j < p.NumLocals; j++ {
			if live[i]&(uint64(1)<<j) != 0 {
				rep.liveAcross[j]++
			}
		}
	}
	return live, rep, nil
}

// loopInfo 描述一个自然循环：header 是回边目标，backedge 是回跳指令下标。
type loopInfo struct {
	header   int
	backedge int
}

// findLoop 识别程序中的第一个回边循环（backedge = 跳转到前面指令的控制流）。
// 数值循环 `for(i<n) sum+=...` 在 lowering 后呈现为 header（循环体入口）+ 尾部
// 条件回跳。多循环/嵌套循环暂不处理（返回第一个，后续可扩展）。
func findLoop(p *Program) (loopInfo, bool) {
	for i, in := range p.Code {
		var target int
		switch in.Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			target = int(in.Operand)
		default:
			continue
		}
		if target >= 0 && target < i {
			return loopInfo{header: target, backedge: i}, true
		}
	}
	return loopInfo{}, false
}

// selectHotLocals 选出循环体内值得寄存器化的热 local：
//   - proven Number（assigned 位图，来自 nativeAssignedLocals）——Native 只处理数值；
//   - 循环体内被读取（useCounts > 0）；
//   - 最多 maxRegs 个（XMM8-XMM15 共 8 个），按读取次数降序。
//
// 返回热 local 的 slot 列表（按热度降序）。
func selectHotLocals(p *Program, assigned []uint64, loop loopInfo, maxRegs int) []int {
	type candidate struct {
		slot int
		uses int
	}
	var cands []candidate
	for slot := 1; slot < p.NumLocals; slot++ {
		if slot > 63 {
			break
		}
		// 循环体内至少有一条指令证明该 slot 是 Number 且被读取。
		proven := false
		uses := 0
		for i := loop.header; i <= loop.backedge; i++ {
			if i >= len(assigned) {
				break
			}
			if assigned[i]&(uint64(1)<<slot) != 0 {
				proven = true
			}
			if p.Code[i].Op == OpLoadLocal && int(p.Code[i].Operand) == slot {
				uses++
			}
		}
		if proven && uses > 0 {
			cands = append(cands, candidate{slot: slot, uses: uses})
		}
	}
	// 按 uses 降序排序（简单插入排序，候选数 ≤ 64）。
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && cands[j].uses > cands[j-1].uses; j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	if len(cands) > maxRegs {
		cands = cands[:maxRegs]
	}
	out := make([]int, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.slot)
	}
	return out
}

// regallocPlan 描述一次寄存器分配的结果：循环 + 热 local 到 XMM8-15 的映射。
type regallocPlan struct {
	loop        loopInfo
	hot         []int       // 热 local slot 列表（按热度降序）
	reg         map[int]int // slot → XMM 寄存器号（8-15）
	reloadStart int         // loop header 前 reload 块的机器码起始 offset
	reloadSize  int         // reload 块字节数（backedge 跳转需跳过）
}

// tryPlanRegalloc 尝试为程序规划寄存器分配。返回 nil 表示不适用：
//   - 无回边循环；
//   - 循环体内有内部分支（break/continue/if）——本阶段仅支持 straight-line
//     循环体（数值累加/计数循环的典型形态），多出口 spill 留后续；
//   - 无 proven-Number 热 local。
func tryPlanRegalloc(p *Program, assigned []uint64) *regallocPlan {
	loop, ok := findLoop(p)
	if !ok {
		return nil
	}
	// 循环体 straight-line 检查：除 backedge 本身外无跳转指令。
	for i := loop.header; i <= loop.backedge; i++ {
		switch p.Code[i].Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			if i != loop.backedge {
				return nil
			}
		}
	}
	hot := selectHotLocals(p, assigned, loop, 8)
	if len(hot) == 0 {
		return nil
	}
	reg := make(map[int]int, len(hot))
	for k, slot := range hot {
		reg[slot] = 8 + k
	}
	return &regallocPlan{loop: loop, hot: hot, reg: reg}
}

// regFor 返回 slot 在指令 i 处的寄存器号（若该 slot 被寄存器化且 i 位于循环
// 体内）。plan 为 nil 或 slot 未寄存器化/在循环体外时返回 ok=false。
func (plan *regallocPlan) regFor(slot, i int) (int, bool) {
	if plan == nil {
		return 0, false
	}
	reg, ok := plan.reg[slot]
	return reg, ok && i >= plan.loop.header && i <= plan.loop.backedge
}
