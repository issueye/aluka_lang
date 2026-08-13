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
