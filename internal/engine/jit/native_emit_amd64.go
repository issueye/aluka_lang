//go:build amd64 && (windows || linux)

package jit

import (
	"encoding/binary"
	"fmt"
	"math"

	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

const (
	nativeResultOffset  = 64
	nativeStatusOffset  = 72
	nativeLocalsOffset  = 80
	nativeBudgetOffset  = 336
	nativeResumeOffset  = 344
	nativeRecBaseOffset = 352
	nativeRecFPOffset   = 360
	// nativeRecFrameSize 是递归子帧的大小：locals（32×8B）+ 操作数栈保存（32×8B）
	// + 返回 PC（8B）+ status（8B）。用 JMP 而非 CALL 避免机器码返回地址进入
	// Go 栈导致精确 GC 栈扫描崩溃；status 槽让递归中途的深度超限（too_deep）
	// 能沿返回链透传 GuardFailed 到顶层。
	nativeRecFrameSize  = 528
	nativeRecLocalsSize = 256
	nativeRecStackSize  = 256
	nativeRecPCOffset   = 512
	nativeRecStatusOff  = 520
	nativeRecMaxFrames  = 256
)

type nativeFixup struct {
	displacement int
	target       int
}

func compileNativeProgram(p *Program, retainDebugBytes ...bool) (*jitnative.Code, error) {
	if p == nil || p.NumParams > 8 || p.NumLocals > 32 || p.MaxStack > 8 ||
		p.nativeTrace && p.NumLocals+p.MaxStack > 32 || len(p.Code) == 0 {
		return nil, fmt.Errorf("jit: native limits exceeded")
	}
	assigned, err := nativeAssignedLocals(p)
	if err != nil {
		return nil, err
	}
	// M2 寄存器分配：识别 straight-line 单循环内的热 local，映射到 XMM8-15。
	// 自递归模式（F1）禁用 M2（locals 走 R11 基址，寄存器化会干扰递归帧）。
	var plan *regallocPlan
	if !p.hasSelfCall {
		plan = tryPlanRegalloc(p, assigned)
	}
	// The compiler now lowers whole functions, so unreachable tails (e.g. the
	// implicit return_undef the bytecode compiler emits after the final
	// return) are part of the IR. Native code only needs reachable
	// instructions; skipping the rest keeps the linear depth tracking sound.
	reachable := reachableFromEntry(p.Code)
	targeted := make([]bool, len(p.Code))
	for i, in := range p.Code {
		if !reachable[i] {
			continue
		}
		switch in.Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			if int(in.Operand) < 0 || int(in.Operand) >= len(targeted) {
				return nil, fmt.Errorf("jit: native jump target out of range")
			}
			targeted[in.Operand] = true
		}
	}

	var code []byte
	depth := 0
	// F1 自递归入口：R11 = 当前帧 locals 基址（RecFP==0 → &Frame.Locals；
	// 否则 RecBase + RecFP*frameSize），并把 Frame.Args 拷入顶层 locals[1..n]。
	if p.hasSelfCall {
		code = emitNativeSelfCallEntry(code, p)
	}
	offsets := make([]int, len(p.Code))
	depths := make([]int, len(p.Code))
	// targetDepths 记录跳转目标的路径深度（发射时填充，发射循环开头重置用）。
	targetDepths := make(map[int]int, 4)
	fixups := make([]nativeFixup, 0, 8)
	var ripPool []ripConstRef
	sawTerminal := false
	for i := 0; i < len(p.Code); i++ {
		if !reachable[i] {
			continue
		}
		// loop header reload：在 header 指令的 offset 之前插入 reload 块，把热
		// local 从 Frame 加载到 XMM8-15。offsets[header] 指向 reload 块之后
		// （循环体开头），故 backedge 跳转自动跳过 reload；首次进入（fall-through）
		// 与 yield 恢复（resumeOffset 指向 reload 块开头）都会执行 reload。
		if plan != nil && i == plan.loop.header && plan.reloadSize == 0 {
			plan.reloadStart = len(code)
			for k, slot := range plan.hot {
				code = emitLoadF64(code, 8+k, nativeLocalOffset(slot, p.NumParams))
			}
			plan.reloadSize = len(code) - plan.reloadStart
		}
		// 跳转目标深度：跳转发射时记录目标路径的深度；发射循环开头重置，
		// 避免无条件跳转后的线性 fallthrough 深度污染跳转目标（fib 的
		// JMP 跳过递归段，目标由条件跳转以不同深度进入）。仅自递归模式需要
		// （普通函数线性深度即正确，重置反而破坏）。
		if p.hasSelfCall {
			if d, ok := targetDepths[i]; ok {
				depth = d
			}
		}
		offsets[i] = len(code)
		depths[i] = depth
		in := p.Code[i]
		switch in.Op {
		case OpConst:
			code = emitConstF64(code, depth, in.Value)
			depth++
		case OpLoadLocal:
			slot := int(in.Operand)
			if slot <= 0 || slot >= p.NumLocals || assigned[i]&(uint64(1)<<slot) == 0 {
				return nil, fmt.Errorf("jit: native local %d is not a proven number", slot)
			}
			if reg, ok := plan.regFor(slot, i); ok {
				code = emitMoveF64(code, depth, reg) // 寄存器 → 操作数栈
			} else if p.hasSelfCall {
				code = emitLocalLoadF64(code, depth, slot) // R11 基址（递归帧）
			} else {
				code = emitLoadF64(code, depth, nativeLocalOffset(slot, p.NumParams))
			}
			depth++
		case OpStoreLocal:
			depth--
			slot := int(in.Operand)
			if slot <= 0 || slot >= p.NumLocals {
				return nil, fmt.Errorf("jit: native local out of range")
			}
			if reg, ok := plan.regFor(slot, i); ok {
				code = emitMoveF64(code, reg, depth) // 操作数栈 → 寄存器
			} else if p.hasSelfCall {
				code = emitLocalStoreF64(code, depth, slot) // R11 基址（递归帧）
			} else {
				code = emitStoreF64(code, depth, nativeLocalOffset(slot, p.NumParams))
			}
			if p.nativeTrace {
				code = emitNativeDirtyLocal(code, slot)
			}
		case OpAdd, OpSub, OpMul, OpDiv, OpMod:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			right, left := depth-1, depth-2
			if in.Op == OpMod {
				code = emitModF64(code, left, right, &ripPool)
			} else {
				code = emitBinaryF64(code, in.Op, left, right)
			}
			depth--
		case OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			right, left := depth-1, depth-2
			code = emitBitwiseI32(code, in.Op, left, right, &ripPool)
			depth--
		case OpBitNot:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			code = emitBitNotI32(code, depth-1, &ripPool)
		case OpNeg:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
			code = emitNegF64(code, depth-1)
		case OpUnaryPlus:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native stack underflow")
			}
		case OpDup:
			if depth < 1 || depth >= 8 {
				return nil, fmt.Errorf("jit: native dup stack")
			}
			code = emitMoveF64(code, depth, depth-1)
			depth++
		case OpSwap:
			if depth < 2 {
				return nil, fmt.Errorf("jit: native swap stack")
			}
			code = emitSwapF64(code, depth-2, depth-1)
		case OpPop:
			depth--
		case OpEq, OpNe, OpStrictEq, OpStrictNe, OpLt, OpLe, OpGt, OpGe:
			if depth < 2 || i+1 >= len(p.Code) || targeted[i+1] {
				return nil, fmt.Errorf("jit: native comparison is not a branch condition")
			}
			branch := p.Code[i+1]
			if branch.Op != OpJumpTrue && branch.Op != OpJumpFalse {
				return nil, fmt.Errorf("jit: native comparison result escapes")
			}
			right, left := depth-1, depth-2
			code = emitComparisonTest(code, in.Op, left, right)
			depth -= 2
			offsets[i+1] = len(code)
			condition := byte(0x85) // JNE
			if branch.Op == OpJumpFalse {
				condition = 0x84 // JE
			}
			target := int(branch.Operand)
			if target > i {
				// 前向跳转：记录目标路径深度（比较已消费 2 操作数）。
				targetDepths[target] = depth
			}
			if target < i {
				if depth != 0 || depths[target] != 0 {
					return nil, fmt.Errorf("jit: native backedge has live operand stack")
				}
				// 回边 spill 移到冷路径（yield 分支 + 循环退出块），不每迭代执行。
				if plan != nil && i+1 == plan.loop.backedge {
					// 退出路径：Jcc 跳到 exit_spill 块（稍后 patch）。
					code = append(code, 0x0F, condition^1)
					exitSpillFixup := len(code)
					code = append(code, 0, 0, 0, 0)
					code = emitNativeBudgetPollWithSpill(code, plan.reloadStart, plan.hot, p.NumParams)
					code = append(code, 0xE9)
					fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
					code = append(code, 0, 0, 0, 0)
					// exit_spill 块：spill 热 local 后跳到循环退出代码（i+2）。
					exitSpill := len(code)
					binary.LittleEndian.PutUint32(code[exitSpillFixup:exitSpillFixup+4], uint32(int32(exitSpill-(exitSpillFixup+4))))
					for k, slot := range plan.hot {
						code = emitStoreF64(code, 8+k, nativeLocalOffset(slot, p.NumParams))
					}
					code = append(code, 0xE9)
					fixups = append(fixups, nativeFixup{displacement: len(code), target: i + 2})
					code = append(code, 0, 0, 0, 0)
				} else {
					code = append(code, 0x0F, condition^1)
					skipFixup := len(code)
					code = append(code, 0, 0, 0, 0)
					if p.hasSelfCall {
						// F1：布局 [poll][stub][jmp 循环头]。正常路径落进
						// stub（R11 重算无害）后 jmp；yield 恢复点 Resume
						// 指向 stub——Go 侧 CallAt(Resume) 重入时 R11 已被
						// Go 运行时 clobber，必须重算帧基址。Resume imm 是
						// 前向引用，stub 发射后 patch。
						var pollResume int
						code, pollResume = emitNativeBudgetPoll(code, 0)
						stub := len(code)
						code = emitNativeR11Reload(code)
						binary.LittleEndian.PutUint32(code[pollResume:pollResume+4], uint32(int32(stub)))
					} else {
						code, _ = emitNativeBudgetPoll(code, offsets[target])
					}
					code = append(code, 0xE9)
					fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
					code = append(code, 0, 0, 0, 0)
					relative := len(code) - (skipFixup + 4)
					binary.LittleEndian.PutUint32(code[skipFixup:skipFixup+4], uint32(int32(relative)))
				}
			} else {
				code = append(code, 0x0F, condition)
				fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
				code = append(code, 0, 0, 0, 0)
			}
			i++
		case OpJump:
			target := int(in.Operand)
			if target > i {
				targetDepths[target] = depth
			}
			if target < i {
				if depth != 0 || depths[target] != 0 {
					return nil, fmt.Errorf("jit: native backedge has live operand stack")
				}
				if plan != nil && i == plan.loop.backedge {
					code = emitNativeBudgetPollWithSpill(code, plan.reloadStart, plan.hot, p.NumParams)
				} else if p.hasSelfCall {
					// F1：布局 [poll][stub][jmp 循环头]，同比较分支——Resume
					// 指向 stub，重入时重算 R11（RecFP/RecBase 在 Frame 中
					// 跨 Go 边界保留，寄存器不保留）。
					var pollResume int
					code, pollResume = emitNativeBudgetPoll(code, 0)
					stub := len(code)
					code = emitNativeR11Reload(code)
					binary.LittleEndian.PutUint32(code[pollResume:pollResume+4], uint32(int32(stub)))
				} else {
					code, _ = emitNativeBudgetPoll(code, offsets[target])
				}
			}
			code = append(code, 0xE9)
			fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
			code = append(code, 0, 0, 0, 0)
		case OpJumpTrue, OpJumpFalse:
			return nil, fmt.Errorf("jit: native branch lacks numeric comparison")
		case OpJumpTrueKeep, OpJumpFalseKeep:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native keep branch stack underflow")
			}
			target := int(in.Operand)
			if target <= i {
				return nil, fmt.Errorf("jit: native keep branch must be forward")
			}
			targetDepths[target] = depth // keep 分支跳转时栈顶保留
			code = emitNumberTruthyTest(code, depth-1)
			condition := byte(0x85) // JNE
			if in.Op == OpJumpFalseKeep {
				condition = 0x84 // JE
			}
			code = append(code, 0x0F, condition)
			fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
			code = append(code, 0, 0, 0, 0)
			depth--
		case OpJumpNullishKeep:
			if depth < 1 {
				return nil, fmt.Errorf("jit: native nullish branch stack underflow")
			}
			target := int(in.Operand)
			if target <= i {
				return nil, fmt.Errorf("jit: native nullish branch must be forward")
			}
			// Native inputs are guarded Numbers, so the left side is never
			// nullish and the fallback expression is unreachable.
			targetDepths[target] = depth // keep：跳转时栈顶保留
			code = append(code, 0xE9)
			fixups = append(fixups, nativeFixup{displacement: len(code), target: target})
			code = append(code, 0, 0, 0, 0)
			depth--
		case OpReturn:
			if depth != 1 {
				return nil, fmt.Errorf("jit: native return stack: depth=%d at i=%d", depth, i)
			}
			code = emitStoreF64(code, 0, nativeResultOffset)
			if p.hasSelfCall {
				// 子帧返回（机器码显式栈）：RecFP--、R11=父帧基址、JMP 父帧返回 PC；
				// 顶层（RecFP==0）正常 RET（无机器码返回地址，GC 安全）。
				// MOV RAX,[R10+RecFP]; TEST RAX,RAX; JZ top_ret
				code = append(code, 0x49, 0x8B, 0x82)
				code = appendInt32(code, nativeRecFPOffset)
				code = append(code, 0x48, 0x85, 0xC0)
				code = append(code, 0x0F, 0x84)
				topRetFixup := len(code)
				code = append(code, 0, 0, 0, 0)
				// SUB RAX,1; MOV [R10+RecFP],RAX（RecFP--）
				code = append(code, 0x48, 0x83, 0xE8, 0x01)
				code = append(code, 0x49, 0x89, 0x82)
				code = appendInt32(code, nativeRecFPOffset)
				// R11 = 父帧基址 = RecBase + RecFP*528
				code = append(code, 0x49, 0x8B, 0x8A) // MOV RCX,[R10+RecBase]
				code = appendInt32(code, nativeRecBaseOffset)
				code = append(code, 0x48, 0x69, 0xC0) // IMUL RAX,RAX,528
				code = appendInt32(code, nativeRecFrameSize)
				code = append(code, 0x4C, 0x8D, 0x1C, 0x01) // LEA R11,[RCX+RAX]
				// MOV QWORD [R11+statusOff],0（父帧 status=0：正常返回；imm32 sign-extend）
				code = append(code, 0x49, 0xC7, 0x83)
				code = appendInt32(code, nativeRecStatusOff)
				code = appendInt32(code, 0)
				// MOV RAX,[R11+pcOffset]（父帧返回 PC）
				code = append(code, 0x49, 0x8B, 0x83)
				code = appendInt32(code, nativeRecPCOffset)
				// JMP RAX（跳回父帧调用点，无机器码返回地址）
				code = append(code, 0xFF, 0xE0)
				topRet := len(code)
				binary.LittleEndian.PutUint32(code[topRetFixup:topRetFixup+4], uint32(int32(topRet-(topRetFixup+4))))
				code = append(code, 0x31, 0xC0, 0xC3) // XOR EAX,EAX; RET
			} else {
				code = append(code, 0x31, 0xC0, 0xC3) // XOR EAX,EAX; RET
			}
			depth--
			sawTerminal = true
		case OpReturnUndef:
			// Unreachable return_undef tails are skipped above; a reachable
			// one returns the JS undefined, which the numeric native frame
			// cannot represent. Reject so Auto falls back to the Quick tier
			// instead of silently returning a number.
			return nil, fmt.Errorf("jit: native cannot represent undefined return")
		case OpPushSelf:
			// F1 自递归：callee 是隐式的，push_self 不发射机器码，但深度追踪
			// 占位 +1（与 Quick 语义对齐，保证跳转路径深度一致）。
			if !p.hasSelfCall {
				return nil, fmt.Errorf("jit: native push_self requires self-call mode")
			}
			depth++
		case OpSelfCall:
			// F1 自递归：机器码显式栈——JMP 到函数入口（不用 CALL，避免机器码
			// 返回地址进入 Go 栈导致精确 GC 栈扫描崩溃），返回经父帧保存的
			// 返回 PC 由子帧 OpReturn JMP 回来。
			// 栈：[callee占位, arg0..argN-1]（参数在 depth-n..depth-1）。
			if !p.hasSelfCall {
				return nil, fmt.Errorf("jit: native self_call requires self-call mode")
			}
			n := int(in.Operand)
			if depth < n+1 {
				return nil, fmt.Errorf("jit: native self_call stack underflow: depth=%d n=%d", depth, n)
			}
			// 深度检查：RecFP >= 256 → GuardFailed（status=1）
			code = append(code, 0x49, 0x8B, 0x82)
			code = appendInt32(code, nativeRecFPOffset)
			code = append(code, 0x48, 0x3D)
			code = appendInt32(code, nativeRecMaxFrames-1)
			code = append(code, 0x0F, 0x83)
			tooDeepFixup := len(code)
			code = append(code, 0, 0, 0, 0)
			// 保存父帧操作数栈（XMM[0..depth-1] → [R11+localsSize + i*8]）
			for i := 0; i < depth; i++ {
				code = emitLocalStoreF64Off(code, i, nativeRecLocalsSize+i*8)
			}
			// 保存返回 PC：[R11+pcOffset] = 本指令之后的机器码地址（RIP 相对）
			// LEA RAX,[RIP+disp]；disp 稍后 patch（指向返回点，即本 self_call 末尾）
			code = append(code, 0x48, 0x8D, 0x05)
			rcFixup := len(code)
			code = append(code, 0, 0, 0, 0)
			code = append(code, 0x49, 0x89, 0x83) // MOV [R11+pcOffset],RAX
			code = appendInt32(code, nativeRecPCOffset)
			// 子帧 locals 基址 = RecBase + (RecFP+1)*528 → R11
			code = append(code, 0x49, 0x8B, 0x8A) // MOV RCX,[R10+RecBase]
			code = appendInt32(code, nativeRecBaseOffset)
			code = append(code, 0x49, 0x8B, 0x82) // MOV RAX,[R10+RecFP]（重读，LEA 覆盖过）
			code = appendInt32(code, nativeRecFPOffset)
			code = append(code, 0x48, 0x69, 0xC0) // IMUL RAX,RAX,528
			code = appendInt32(code, nativeRecFrameSize)
			code = append(code, 0x4C, 0x8D, 0x9C, 0x01) // LEA R11,[RCX+RAX+disp32]
			code = appendInt32(code, nativeRecFrameSize)
			// 参数拷贝：XMM[depth-n+i] → [R11+(i+1)*8]
			for i := 0; i < n; i++ {
				code = emitLocalStoreF64(code, depth-n+i, i+1)
			}
			// RecFP+1 写回
			code = append(code, 0x49, 0x8B, 0x82) // MOV RAX,[R10+RecFP]
			code = appendInt32(code, nativeRecFPOffset)
			code = append(code, 0x48, 0x8D, 0x40, 0x01) // LEA RAX,[RAX+1]
			code = append(code, 0x49, 0x89, 0x82)       // MOV [R10+RecFP],RAX
			code = appendInt32(code, nativeRecFPOffset)
			// JMP 函数入口（rel32；目标 = 机器码入口 0）——不压返回地址，GC 安全
			code = append(code, 0xE9)
			jmpFixup := len(code)
			code = append(code, 0, 0, 0, 0)
			// 返回点（子帧 OpReturn/too_deep JMP 回来）：R11 已是父帧基址。
			retPos := len(code)
			// patch LEA RAX,[RIP+disp]：disp = retPos - (rcFixup+4)
			binary.LittleEndian.PutUint32(code[rcFixup:rcFixup+4], uint32(int32(retPos-(rcFixup+4))))
			// patch JMP：目标 = 0（入口）
			binary.LittleEndian.PutUint32(code[jmpFixup:jmpFixup+4], uint32(int32(-(jmpFixup + 4))))
			// 检查父帧 status 槽：非 0（子帧深度超限）→ 透传给更上层
			// MOV EAX,[R11+statusOff]; TEST EAX,EAX; JZ ok
			code = append(code, 0x49, 0x8B, 0x83)
			code = appendInt32(code, nativeRecStatusOff)
			code = append(code, 0x48, 0x85, 0xC0)
			code = append(code, 0x0F, 0x84)
			statusOKFixup := len(code)
			code = append(code, 0, 0, 0, 0)
			// status 非 0：透传——RecFP--、R11=更上层、写 status、JMP 更上层返回 PC
			code = append(code, 0x49, 0x8B, 0x82) // MOV RAX,[R10+RecFP]
			code = appendInt32(code, nativeRecFPOffset)
			code = append(code, 0x48, 0x85, 0xC0)
			code = append(code, 0x0F, 0x84)
			topStatusFixup := len(code)
			code = append(code, 0, 0, 0, 0)
			code = append(code, 0x48, 0x83, 0xE8, 0x01) // SUB RAX,1
			code = append(code, 0x49, 0x89, 0x82)       // MOV [R10+RecFP],RAX
			code = appendInt32(code, nativeRecFPOffset)
			code = append(code, 0x49, 0x8B, 0x8A) // MOV RCX,[R10+RecBase]
			code = appendInt32(code, nativeRecBaseOffset)
			code = append(code, 0x48, 0x69, 0xC0) // IMUL RAX,RAX,528
			code = appendInt32(code, nativeRecFrameSize)
			code = append(code, 0x4C, 0x8D, 0x1C, 0x01) // LEA R11,[RCX+RAX]
			code = append(code, 0x49, 0xC7, 0x83)       // MOV QWORD [R11+statusOff],1
			code = appendInt32(code, nativeRecStatusOff)
			code = appendInt32(code, 1)           // imm32（sign-extend）
			code = append(code, 0x49, 0x8B, 0x83) // MOV RAX,[R11+pcOffset]
			code = appendInt32(code, nativeRecPCOffset)
			code = append(code, 0xFF, 0xE0) // JMP RAX
			topStatusPos := len(code)
			binary.LittleEndian.PutUint32(code[topStatusFixup:topStatusFixup+4], uint32(int32(topStatusPos-(topStatusFixup+4))))
			// 顶层 status 非 0：XOR EAX,EAX; INC EAX; RET（透传 GuardFailed 给 Go）
			code = append(code, 0x31, 0xC0, 0xFF, 0xC0, 0xC3)
			// ok：status==0，恢复父帧操作数栈 + 加载结果。
			statusOKPos := len(code)
			binary.LittleEndian.PutUint32(code[statusOKFixup:statusOKFixup+4], uint32(int32(statusOKPos-(statusOKFixup+4))))
			// 恢复父帧操作数栈（[R11+localsSize + i*8] → XMM[0..depth-1]）
			for i := 0; i < depth; i++ {
				code = emitLocalLoadF64Off(code, i, nativeRecLocalsSize+i*8)
			}
			code = emitLoadF64(code, depth-n-1, nativeResultOffset) // 结果 → 父帧栈顶
			// JMP 跳过 too_deep 块（正常返回路径不得 fallthrough 进入）
			code = append(code, 0xE9)
			skipTooDeepFixup := len(code)
			code = append(code, 0, 0, 0, 0)
			// too_deep：走子帧返回路径（RecFP--、R11=父帧、写 status=1、JMP 父帧 PC）
			tooDeepEnd := len(code)
			binary.LittleEndian.PutUint32(code[tooDeepFixup:tooDeepFixup+4], uint32(int32(tooDeepEnd-(tooDeepFixup+4))))
			code = append(code, 0x48, 0x83, 0xE8, 0x01) // SUB RAX,1（RAX=RecFP）
			code = append(code, 0x49, 0x89, 0x82)       // MOV [R10+RecFP],RAX
			code = appendInt32(code, nativeRecFPOffset)
			code = append(code, 0x49, 0x8B, 0x8A) // MOV RCX,[R10+RecBase]
			code = appendInt32(code, nativeRecBaseOffset)
			code = append(code, 0x48, 0x69, 0xC0) // IMUL RAX,RAX,528
			code = appendInt32(code, nativeRecFrameSize)
			code = append(code, 0x4C, 0x8D, 0x1C, 0x01) // LEA R11,[RCX+RAX]
			code = append(code, 0x49, 0xC7, 0x83)       // MOV QWORD [R11+statusOff],1
			code = appendInt32(code, nativeRecStatusOff)
			code = appendInt32(code, 1)           // imm32（sign-extend）
			code = append(code, 0x49, 0x8B, 0x83) // MOV RAX,[R11+pcOffset]
			code = appendInt32(code, nativeRecPCOffset)
			code = append(code, 0xFF, 0xE0) // JMP RAX
			skipTooDeepPos := len(code)
			binary.LittleEndian.PutUint32(code[skipTooDeepFixup:skipTooDeepFixup+4], uint32(int32(skipTooDeepPos-(skipTooDeepFixup+4))))
			depth -= n + 1 // 弹 n 参数 + callee 占位
			depth += 1     // 压结果
			sawTerminal = false
		case OpTraceExit:
			exitID := int(in.Operand)
			if !p.nativeTrace || in.Operand > ^uint32(0)-3 {
				return nil, fmt.Errorf("jit: invalid native trace exit")
			}
			if len(p.traceExitDepths) == 0 {
				if depth != 0 {
					return nil, fmt.Errorf("jit: native trace exit stack lacks a deopt map")
				}
			} else {
				if exitID < 0 || exitID >= len(p.traceExitDepths) {
					return nil, fmt.Errorf("jit: invalid native trace exit")
				}
				depth = int(p.traceExitDepths[exitID])
			}
			if p.NumLocals+depth > 32 {
				return nil, fmt.Errorf("jit: native trace exit stack limit exceeded")
			}
			for stackIndex := 0; stackIndex < depth; stackIndex++ {
				code = emitStoreF64(code, stackIndex, nativeLocalOffset(p.NumLocals+stackIndex, p.NumParams))
			}
			code = append(code, 0xB8)
			code = appendInt32(code, int32(in.Operand+3))
			code = append(code, 0xC3)
			depth = 0
			sawTerminal = true
		case OpPow:
			// R4-7: math.Pow is a libm-quality function; no x86 instruction
			// computes it bit-identically (F2XM1-based exp-log differs in last
			// ulp and in special cases like pow(1, NaN) / pow(-1, ±Inf)), and a
			// callout into Go/libm would change the pointer-free native ABI.
			// Pow stays in the Quick tier; Auto falls back stably (the native
			// rejection is recorded once per program).
			return nil, fmt.Errorf("jit: native pow requires libm (kept in Quick tier)")
		default:
			return nil, fmt.Errorf("jit: native unsupported IR opcode %d", in.Op)
		}
		if depth < 0 || depth > 8 {
			return nil, fmt.Errorf("jit: native register stack overflow")
		}
	}
	if !sawTerminal {
		return nil, fmt.Errorf("jit: native program has no terminal")
	}
	code = finalizeRipConstPool(code, ripPool)
	for _, fixup := range fixups {
		if fixup.target < 0 || fixup.target >= len(offsets) {
			return nil, fmt.Errorf("jit: native jump target out of range")
		}
		relative := offsets[fixup.target] - (fixup.displacement + 4)
		binary.LittleEndian.PutUint32(code[fixup.displacement:fixup.displacement+4], uint32(int32(relative)))
	}
	return jitnative.Publish(code, retainDebugBytes...)
}

// nativeAssignedLocals computes, for every instruction, the set of locals
// that are proven Numbers on every path reaching it. assigned[0] starts from
// the native preassignment (numeric params and property input slots).
func nativeAssignedLocals(p *Program) ([]uint64, error) {
	if p.NumLocals > 64 {
		return nil, fmt.Errorf("jit: native local proof limit exceeded")
	}
	assigned := make([]uint64, len(p.Code))
	reachable := make([]bool, len(p.Code))
	initial := p.nativePreassigned
	for i := 1; i <= p.NumParams && i < p.NumLocals; i++ {
		if p.nativeNumberArgs&(uint16(1)<<(i-1)) != 0 {
			initial |= uint64(1) << i
		}
	}
	assigned[0], reachable[0] = initial, true
	work := []int{0}
	for len(work) > 0 {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		out := assigned[i]
		in := p.Code[i]
		if in.Op == OpStoreLocal {
			out |= uint64(1) << in.Operand
		}
		var successors []int
		switch in.Op {
		case OpReturn, OpReturnUndef, OpTraceExit:
		case OpJump:
			successors = append(successors, int(in.Operand))
		case OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			successors = append(successors, i+1, int(in.Operand))
		default:
			successors = append(successors, i+1)
		}
		for _, next := range successors {
			if next < 0 || next >= len(p.Code) {
				return nil, fmt.Errorf("jit: native control flow leaves program")
			}
			if !reachable[next] {
				reachable[next], assigned[next] = true, out
				work = append(work, next)
				continue
			}
			merged := assigned[next] & out
			if merged != assigned[next] {
				assigned[next] = merged
				work = append(work, next)
			}
		}
	}
	return assigned, nil
}

func nativeLocalOffset(slot, numParams int) int {
	if slot >= 1 && slot <= numParams {
		return (slot - 1) * 8
	}
	return nativeLocalsOffset + slot*8
}

func emitLoadF64(code []byte, xmm, offset int) []byte {
	// movsd xmmN, [r10+disp32]。r10 需要 REX.B（0x41），xmm8-15 需要 REX.R（0x04）。
	rex := byte(0x41)
	if xmm >= 8 {
		rex |= 0x04
	}
	code = append(code, 0xF2, rex, 0x0F, 0x10, byte(0x82|((xmm&7)<<3)))
	return appendInt32(code, int32(offset))
}

// emitLocalLoadF64 从 R11 基址的 locals[slot] 加载（F1 自递归模式）。
// movsd xmmN, [r11+disp32]。R11 需要 REX.B（0x41）。
func emitLocalLoadF64(code []byte, xmm, slot int) []byte {
	rex := byte(0x41) // REX.B（r11）
	if xmm >= 8 {
		rex |= 0x04 // REX.R
	}
	// mod=10(disp32), reg=xmm&7, rm=011(r11)
	code = append(code, 0xF2, rex, 0x0F, 0x10, byte(0x83|((xmm&7)<<3)))
	return appendInt32(code, int32(slot*8))
}

// emitLocalStoreF64 写 R11 基址的 locals[slot]（F1 自递归模式）。
func emitLocalStoreF64(code []byte, xmm, slot int) []byte {
	rex := byte(0x41)
	if xmm >= 8 {
		rex |= 0x04
	}
	code = append(code, 0xF2, rex, 0x0F, 0x11, byte(0x83|((xmm&7)<<3)))
	return appendInt32(code, int32(slot*8))
}

// emitLocalStoreF64Off 写 [R11+off]（F1 自递归：操作数栈保存区，off 为字节偏移）。
func emitLocalStoreF64Off(code []byte, xmm, off int) []byte {
	rex := byte(0x41)
	if xmm >= 8 {
		rex |= 0x04
	}
	code = append(code, 0xF2, rex, 0x0F, 0x11, byte(0x83|((xmm&7)<<3)))
	return appendInt32(code, int32(off))
}

// emitLocalLoadF64Off 从 [R11+off] 加载（F1 自递归：操作数栈恢复）。
func emitLocalLoadF64Off(code []byte, xmm, off int) []byte {
	rex := byte(0x41)
	if xmm >= 8 {
		rex |= 0x04
	}
	code = append(code, 0xF2, rex, 0x0F, 0x10, byte(0x83|((xmm&7)<<3)))
	return appendInt32(code, int32(off))
}

// emitNativeSelfCallEntry 发射 F1 自递归入口：
//
//	MOV RAX,[R10+RecFP]; TEST RAX,RAX; JNZ rec_entry   （递归调用直接跳 rec_entry）
//	MOV RCX,[R10+RecBase]; LEA R11,[RCX]                    （顶层：R11=帧 0）
//	顶层参数拷贝：Frame.Args[i] → [R11+(i+1)*8]
//	JMP body
//
// rec_entry:                                           （递归子帧入口）
//
//	MOV RCX,[R10+RecBase]; IMUL RAX,RAX,528; LEA R11,[RCX+RAX]
//
// body:
// 返回的 fixups 用于把 rec_entry 的 JMP 重定向到 body 位置（入口发射完成后
// 由调用方 patch）。
func emitNativeSelfCallEntry(code []byte, p *Program) []byte {
	// MOV RAX,[R10+RecFP]
	code = append(code, 0x49, 0x8B, 0x82)
	code = appendInt32(code, nativeRecFPOffset)
	// TEST RAX,RAX
	code = append(code, 0x48, 0x85, 0xC0)
	// JNZ rec_entry（相对跳转，稍后 patch）
	code = append(code, 0x0F, 0x85)
	recEntryFixup := len(code)
	code = append(code, 0, 0, 0, 0)
	// 顶层也走 RecBase 帧 0（locals 在 RecBase[0..]，操作数栈保存区在
	// RecBase[256..]），与子帧统一布局，避免顶层栈保存区与 Frame 字段冲突。
	code = append(code, 0x49, 0x8B, 0x8A) // MOV RCX,[R10+RecBase]
	code = appendInt32(code, nativeRecBaseOffset)
	code = append(code, 0x4C, 0x8D, 0x19) // LEA R11,[RCX]
	// 顶层参数拷贝：Args[i] → [R11+(i+1)*8]，用 XMM15 中转。
	for i := 0; i < p.NumParams; i++ {
		// MOVSD XMM15,[R10+i*8]（REX.R+REX.B：0x45）
		code = append(code, 0xF2, 0x45, 0x0F, 0x10, 0xBA)
		code = appendInt32(code, int32(i*8))
		// MOVSD [R11+(i+1)*8],XMM15（用 emitLocalStoreF64，REX.R/B 正确）
		code = emitLocalStoreF64(code, 15, i+1)
	}
	// JMP body（稍后 patch 到主循环开头）
	code = append(code, 0xE9)
	bodyFixup := len(code)
	code = append(code, 0, 0, 0, 0)
	// rec_entry：
	// MOV RCX,[R10+RecBase]
	code = append(code, 0x49, 0x8B, 0x8A)
	code = appendInt32(code, nativeRecBaseOffset)
	// IMUL RAX,RAX,528
	code = append(code, 0x48, 0x69, 0xC0)
	code = appendInt32(code, nativeRecFrameSize)
	// LEA R11,[RCX+RAX]（SIB: base=RCX, index=RAX, scale=1, disp=0）
	code = append(code, 0x4C, 0x8D, 0x1C, 0x01)
	// body：
	bodyPos := len(code)
	binary.LittleEndian.PutUint32(code[recEntryFixup:recEntryFixup+4], uint32(int32(bodyPos-(recEntryFixup+4))))
	binary.LittleEndian.PutUint32(code[bodyFixup:bodyFixup+4], uint32(int32(bodyPos-(bodyFixup+4))))
	return code
}

func emitStoreF64(code []byte, xmm, offset int) []byte {
	rex := byte(0x41)
	if xmm >= 8 {
		rex |= 0x04
	}
	code = append(code, 0xF2, rex, 0x0F, 0x11, byte(0x82|((xmm&7)<<3)))
	return appendInt32(code, int32(offset))
}

func emitConstF64(code []byte, xmm int, value float64) []byte {
	code = append(code, 0x48, 0xB8)
	var bits [8]byte
	binary.LittleEndian.PutUint64(bits[:], math.Float64bits(value))
	code = append(code, bits[:]...)
	code = append(code, 0x66, 0x48, 0x0F, 0x6E, byte(0xC0|(xmm<<3)))
	return code
}

func emitBinaryF64(code []byte, op Op, left, right int) []byte {
	opcode := byte(0)
	switch op {
	case OpAdd:
		opcode = 0x58
	case OpSub:
		opcode = 0x5C
	case OpMul:
		opcode = 0x59
	case OpDiv:
		opcode = 0x5E
	}
	return append(code, 0xF2, 0x0F, opcode, byte(0xC0|(left<<3)|right))
}

func emitMoveF64(code []byte, dst, src int) []byte {
	// movapd xmm[dst], xmm[src]。dst 需要 REX.R，src 需要 REX.B（0-7 无 REX 前缀）。
	if dst >= 8 || src >= 8 {
		rex := byte(0x40)
		if dst >= 8 {
			rex |= 0x04
		}
		if src >= 8 {
			rex |= 0x01
		}
		return append(code, 0x66, rex, 0x0F, 0x28, byte(0xC0|((dst&7)<<3)|(src&7)))
	}
	return append(code, 0x66, 0x0F, 0x28, byte(0xC0|(dst<<3)|src))
}

func emitSwapF64(code []byte, left, right int) []byte {
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC0|(left<<3)))
	code = emitMoveF64(code, left, right)
	return append(code, 0x66, 0x48, 0x0F, 0x6E, byte(0xC0|(right<<3)))
}

func emitNegF64(code []byte, xmm int) []byte {
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC0|(xmm<<3)))
	code = append(code, 0x48, 0x0F, 0xBA, 0xF8, 0x3F) // BTC RAX,63
	return append(code, 0x66, 0x48, 0x0F, 0x6E, byte(0xC0|(xmm<<3)))
}

func emitComparisonTest(code []byte, op Op, left, right int) []byte {
	code = append(code, 0x66, 0x0F, 0x2E, byte(0xC0|(left<<3)|right)) // UCOMISD
	if op == OpNe || op == OpStrictNe {
		// UCOMISD sets ZF for both equality and unordered. JS Number != must
		// also be true for NaN, so combine SETNE with SETP.
		code = append(code, 0x0F, 0x95, 0xC0) // SETNE AL
		code = append(code, 0x0F, 0x9A, 0xC2) // SETP DL
		code = append(code, 0x08, 0xD0)       // OR AL,DL
		return append(code, 0x84, 0xC0)       // TEST AL,AL
	}
	condition := byte(0)
	ordered := false
	switch op {
	case OpEq, OpStrictEq:
		condition, ordered = 0x94, true // SETE
	case OpLt:
		condition, ordered = 0x92, true // SETB
	case OpLe:
		condition, ordered = 0x96, true // SETBE
	case OpGt:
		condition = 0x97 // SETA
	case OpGe:
		condition = 0x93 // SETAE
	}
	code = append(code, 0x0F, condition, 0xC0) // SETcc AL
	if ordered {
		code = append(code, 0x0F, 0x9B, 0xC2, 0x20, 0xD0) // SETNP DL; AND AL,DL
	}
	return append(code, 0x84, 0xC0) // TEST AL,AL
}

func emitNumberTruthyTest(code []byte, xmm int) []byte {
	// JavaScript Numbers are truthy when ordered and non-zero. UCOMISD sets
	// ZF for both zero and unordered (NaN), so SETNE handles 0, -0 and NaN.
	code = append(code, 0x66, 0x45, 0x0F, 0xEF, 0xFF)              // PXOR XMM15,XMM15
	code = append(code, 0x66, 0x41, 0x0F, 0x2E, byte(0xC7|xmm<<3)) // UCOMISD XMMn,XMM15
	code = append(code, 0x0F, 0x95, 0xC0)                          // SETNE AL
	return append(code, 0x84, 0xC0)                                // TEST AL,AL
}

// emitNativeBudgetPoll 发射预算轮询：SUB [R10+Budget],1；未耗尽 JNZ 跳过
// yield 块；耗尽时把 Resume 设为 resumeOffset、EAX=2、RET（Go 侧恢复后
// CallAt(Resume) 重入）。返回新的 code 与 Resume imm32 的位置——F1 自递归
// 的恢复点 stub 在 poll 之后发射，调用方需 patch 该 imm。
func emitNativeBudgetPoll(code []byte, resumeOffset int) ([]byte, int) {
	// SUB QWORD PTR [R10+Budget],1; JNZ continue.
	code = append(code, 0x49, 0x83, 0xAA)
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0x01, 0x0F, 0x85)
	continueFixup := len(code)
	code = append(code, 0, 0, 0, 0)
	// MOV QWORD PTR [R10+Resume],resumeOffset; MOV EAX,2; RET.
	code = append(code, 0x49, 0xC7, 0x82)
	code = appendInt32(code, nativeResumeOffset)
	resumeImmPos := len(code)
	code = appendInt32(code, int32(resumeOffset))
	code = append(code, 0xB8, 0x02, 0x00, 0x00, 0x00, 0xC3)
	relative := len(code) - (continueFixup + 4)
	binary.LittleEndian.PutUint32(code[continueFixup:continueFixup+4], uint32(int32(relative)))
	return code, resumeImmPos
}

// emitNativeR11Reload 重算 R11 = RecBase + RecFP*528（F1 自递归模式）。
// Go 侧 yield 处理（预算轮询 RET 返回 Go、Go 恢复后 CallAt(resume) 重入）期间
// R11 会被 Go 运行时 clobber（caller-saved），恢复点必须重算帧基址才能继续
// 经 R11 访问递归帧 locals。Clobbers RAX、RCX。
func emitNativeR11Reload(code []byte) []byte {
	// MOV RAX,[R10+RecFP]
	code = append(code, 0x49, 0x8B, 0x82)
	code = appendInt32(code, nativeRecFPOffset)
	// MOV RCX,[R10+RecBase]
	code = append(code, 0x49, 0x8B, 0x8A)
	code = appendInt32(code, nativeRecBaseOffset)
	// IMUL RAX,RAX,528
	code = append(code, 0x48, 0x69, 0xC0)
	code = appendInt32(code, nativeRecFrameSize)
	// LEA R11,[RCX+RAX]（SIB: base=RCX, index=RAX, scale=1, disp=0）
	code = append(code, 0x4C, 0x8D, 0x1C, 0x01)
	return code
}

// emitNativeBudgetPollWithSpill 是带寄存器 spill 的预算轮询：yield 分支在返回
// Go 前先把 hot[0..]（寄存器化在 XMM8-15）写回 Frame，保证 Go 侧恢复时读到
// 最新 local 值。continue 分支（预算未耗尽）不 spill，故 spill 仅在每 65536 次
// 迭代的 yield 时执行一次，不抵消寄存器化的收益。
func emitNativeBudgetPollWithSpill(code []byte, resumeOffset int, hot []int, numParams int) []byte {
	// SUB QWORD PTR [R10+Budget],1; JNZ continue.
	code = append(code, 0x49, 0x83, 0xAA)
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0x01, 0x0F, 0x85)
	continueFixup := len(code)
	code = append(code, 0, 0, 0, 0)
	// yield 分支：先 spill 热 local 到 Frame。
	for k, slot := range hot {
		code = emitStoreF64(code, 8+k, nativeLocalOffset(slot, numParams))
	}
	// MOV QWORD PTR [R10+Resume],resumeOffset; MOV EAX,2; RET.
	code = append(code, 0x49, 0xC7, 0x82)
	code = appendInt32(code, nativeResumeOffset)
	code = appendInt32(code, int32(resumeOffset))
	code = append(code, 0xB8, 0x02, 0x00, 0x00, 0x00, 0xC3)
	relative := len(code) - (continueFixup + 4)
	binary.LittleEndian.PutUint32(code[continueFixup:continueFixup+4], uint32(int32(relative)))
	return code
}

func emitNativeDirtyLocal(code []byte, slot int) []byte {
	// BTS QWORD PTR [R10+Status],slot records stores completed on this path.
	code = append(code, 0x49, 0x0F, 0xBA, 0xAA)
	code = appendInt32(code, nativeStatusOffset)
	return append(code, byte(slot))
}

func appendInt32(code []byte, value int32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(value))
	return append(code, buf[:]...)
}

func appendInt64(code []byte, value int64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(value))
	return append(code, buf[:]...)
}

// emitBitwiseI32 emits XMM(left) = OP(ToInt32(XMM(left)), ToInt32(XMM(right)))
// for the & | ^ << >> >>> operators with exact ES semantics: both operands
// are ToInt32'ed (truncation + mod 2^32 wrap), the shift count is masked with
// 0x1F, >> is arithmetic and >>> is logical on the unsigned value. The result
// is the exact int32 converted back to a double, matching the Quick executor.
// The destination register (left) holds the result. Clobbers RAX, RCX, RDX.
func emitBitwiseI32(code []byte, op Op, left, right int, pool *[]ripConstRef) []byte {
	// R8 holds the right operand: emitToInt32 clobbers RAX/RCX/RDX internally,
	// so the right result cannot live in EDX across the left conversion.
	code = emitToInt32(code, right, pool) // EAX = ToInt32(right)
	code = append(code, 0x49, 0x89, 0xC0) // MOV R8,RAX
	code = emitToInt32(code, left, pool)  // EAX = ToInt32(left)
	switch op {
	case OpShl, OpShr, OpUShr:
		code = append(code, 0x41, 0x8B, 0xC8) // MOV ECX,R8D
		code = append(code, 0x83, 0xE1, 0x1F) // AND ECX,0x1F (ES shift mask)
		switch op {
		case OpShl:
			code = append(code, 0xD3, 0xE0) // SHL EAX,CL
		case OpShr:
			code = append(code, 0xD3, 0xF8) // SAR EAX,CL
		default:
			code = append(code, 0xD3, 0xE8) // SHR EAX,CL
		}
	case OpBitAnd:
		code = append(code, 0x41, 0x23, 0xC0) // AND EAX,R8D (23 /r: reg=dest EAX, rm=src R8D)
	case OpBitOr:
		code = append(code, 0x41, 0x0B, 0xC0) // OR EAX,R8D (0B /r: reg=dest EAX, rm=src R8D)
	case OpBitXor:
		code = append(code, 0x41, 0x33, 0xC0) // XOR EAX,R8D (33 /r: reg=dest EAX, rm=src R8D)
	}
	// EAX holds a signed int32 (UShr: an unsigned value in [0, 2^32)); both
	// convert exactly: MOVSXD sign-extends, and for UShr the write to EAX
	// already zero-extended RAX.
	if op != OpUShr {
		code = append(code, 0x48, 0x63, 0xC0) // MOVSXD RAX,EAX
	}
	code = append(code, 0xF2, 0x48, 0x0F, 0x2A, byte(0xC0|(left<<3))) // CVTSI2SD XMMleft,RAX
	return code
}

// emitBitNotI32 emits XMM d = ~ToInt32(XMM d) as a double (ES unary ~).
func emitBitNotI32(code []byte, d int, pool *[]ripConstRef) []byte {
	code = emitToInt32(code, d, pool)
	code = append(code, 0xF7, 0xD0)                                // NOT EAX
	code = append(code, 0x48, 0x63, 0xC0)                          // MOVSXD RAX,EAX
	code = append(code, 0xF2, 0x48, 0x0F, 0x2A, byte(0xC0|(d<<3))) // CVTSI2SD XMMd,RAX
	return code
}

// localJump is an instruction-internal forward jump (a Jcc/JMP rel32 whose
// target is the current end of the emitted block when patch runs). Unlike
// nativeFixup it is resolved inside a single emit helper, not against IR
// instruction offsets.
type localJump struct{ pos int }

func appendLocalCondJump(code []byte, condition byte) ([]byte, *localJump) {
	code = append(code, 0x0F, condition)
	jump := &localJump{pos: len(code)}
	return append(code, 0, 0, 0, 0), jump
}

func appendLocalJump(code []byte) ([]byte, *localJump) {
	code = append(code, 0xE9)
	jump := &localJump{pos: len(code)}
	return append(code, 0, 0, 0, 0), jump
}

func (j *localJump) patch(code []byte) {
	if j == nil {
		return
	}
	relative := len(code) - (j.pos + 4)
	binary.LittleEndian.PutUint32(code[j.pos:j.pos+4], uint32(int32(relative)))
}

// ripConstRef records a RIP-relative constant reference: the instruction's
// disp32 placeholder position and the constant bits. The constants are
// appended as a literal pool after the last instruction (unreachable: every
// program ends in RET), and each disp32 is patched to reach its constant.
type ripConstRef struct {
	dispPos int
	bits    uint64
}

// finalizeRipConstPool appends the literal constants after the last
// instruction and patches every disp32 to reach its constant. Each entry is
// 16-byte aligned: the UCOMISD/MOVSD m64 operands need natural 8-byte
// alignment and the ANDPD m128 operand needs 16-byte alignment, and a
// misaligned SSE memory access raises #GP.
func finalizeRipConstPool(code []byte, pool []ripConstRef) []byte {
	for i := range pool {
		ref := &pool[i]
		for len(code)%16 != 0 {
			code = append(code, 0)
		}
		pos := len(code)
		code = appendInt64(code, int64(ref.bits))
		relative := pos - (ref.dispPos + 4)
		binary.LittleEndian.PutUint32(code[ref.dispPos:ref.dispPos+4], uint32(int32(relative)))
	}
	return code
}

// emitUCOMISDRipConst appends UCOMISD XMMd, [RIP+const] (compare d with a
// floating constant; ZF=1 when equal or unordered, CF=1 when d < const).
func emitUCOMISDRipConst(code []byte, d int, bits uint64, pool *[]ripConstRef) []byte {
	code = append(code, 0x66, 0x0F, 0x2E, byte(0x05|(d<<3)))
	*pool = append(*pool, ripConstRef{dispPos: len(code), bits: bits})
	return append(code, 0, 0, 0, 0)
}

// emitANDpdRipConst appends ANDPD XMMd, [RIP+const] (bitwise AND with a
// constant, used to clear the sign bit: |x|).
func emitANDpdRipConst(code []byte, d int, bits uint64, pool *[]ripConstRef) []byte {
	code = append(code, 0x66, 0x0F, 0x54, byte(0x05|(d<<3)))
	*pool = append(*pool, ripConstRef{dispPos: len(code), bits: bits})
	return append(code, 0, 0, 0, 0)
}

// emitMovsdRipConst appends MOVSD XMMd, [RIP+const].
func emitMovsdRipConst(code []byte, d int, bits uint64, pool *[]ripConstRef) []byte {
	code = append(code, 0xF2, 0x0F, 0x10, byte(0x05|(d<<3)))
	*pool = append(*pool, ripConstRef{dispPos: len(code), bits: bits})
	return append(code, 0, 0, 0, 0)
}

// emitModF64 emits XMM(left) = fmod(XMM(left), XMM(right)) with the exact
// math.Mod semantics (the JS % on Numbers): NaN when the divisor is ±0 or any
// operand is NaN/±Inf; the dividend when the divisor is ±Inf or the dividend
// is ±0; otherwise the true remainder with the dividend's sign.
//
// The general case uses the x87 FPREM partial-remainder loop: FPREM computes
// fmod (quotient truncated toward zero) and is the same instruction Go's
// amd64 math.Mod is built on, so the result is bit-identical to Quick's
// math.Mod for every input (both are the exact mathematical remainder). The
// special cases mirror the Go pre-checks. The 8-byte [R10+Budget] frame slot
// is used as the XMM→x87 transfer scratch and restored before the instruction
// ends, so no ABI-visible frame state is disturbed. Clobbers RAX, RCX, RDX.
func emitModF64(code []byte, left, right int, pool *[]ripConstRef) []byte {
	// (1) y == ±0 or y is NaN → NaN.
	code = emitUCOMISDRipConst(code, right, 0x0000000000000000, pool) // +0.0
	code, jNaN1 := appendLocalCondJump(code, 0x84)                    // JE
	// (2) x == +Inf or x is NaN → NaN.
	code = emitUCOMISDRipConst(code, left, 0x7FF0000000000000, pool) // +Inf
	code, jNaN2 := appendLocalCondJump(code, 0x84)
	// (3) x == −Inf → NaN.
	code = emitUCOMISDRipConst(code, left, 0xFFF0000000000000, pool) // −Inf
	code, jNaN3 := appendLocalCondJump(code, 0x84)
	// (4) y == ±Inf → x.
	code = emitUCOMISDRipConst(code, right, 0x7FF0000000000000, pool)
	code, jDone1 := appendLocalCondJump(code, 0x84)
	code = emitUCOMISDRipConst(code, right, 0xFFF0000000000000, pool)
	code, jDone2 := appendLocalCondJump(code, 0x84)
	// (5) x == ±0 → x.
	code = emitUCOMISDRipConst(code, left, 0x0000000000000000, pool)
	code, jDone3 := appendLocalCondJump(code, 0x84)

	// (6) General case: FPREM loop. x87 memory operands cannot use REX
	// prefixes, so the frame pointer is copied into RDX and the scratch is
	// the 8-byte [RDX+Budget] slot (saved in RCX first; FSTSW AX clobbers
	// the low word of RAX, which is not used here).
	code = append(code, 0x4C, 0x89, 0xD2) // MOV RDX,R10
	code = append(code, 0x48, 0x8B, 0x8A) // MOV RCX,[RDX+disp32]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xF2, 0x0F, 0x11, byte(0x82|(right<<3))) // MOVSD [RDX+disp],XMMright
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xDD, 0x82) // FLD m64 [RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xF2, 0x0F, 0x11, byte(0x82|(left<<3))) // MOVSD [RDX+disp],XMMleft
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xDD, 0x82) // FLD m64 [RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	fprem := len(code)
	code = append(code, 0xD9, 0xF8)                      // FPREM
	code = append(code, 0xDF, 0xE0)                      // FSTSW AX
	code = append(code, 0xF6, 0xC4, 0x04)                // TEST AH,0x04 (C2 set: partial remainder)
	code = append(code, 0x75, byte(fprem-(len(code)+2))) // JNE rel8 back to FPREM
	code = append(code, 0xDD, 0x9A)                      // FSTP m64 [RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0xDD, 0xD8)                             // FSTP ST(0): discard the divisor, rebalancing the x87 stack
	code = append(code, 0xF2, 0x0F, 0x10, byte(0x82|(left<<3))) // MOVSD XMMleft,[RDX+disp]
	code = appendInt32(code, nativeBudgetOffset)
	code = append(code, 0x48, 0x89, 0x8A) // MOV [RDX+disp],RCX (restore)
	code = appendInt32(code, nativeBudgetOffset)
	code, jDone := appendLocalJump(code)

	// NaN result: load a canonical NaN into the result register. The NaN
	// jumps are patched before the MOVSD is appended so they land on it; the
	// rest jump past it to the shared done point.
	jNaN1.patch(code)
	jNaN2.patch(code)
	jNaN3.patch(code)
	code = emitMovsdRipConst(code, left, 0x7FF8000000000000, pool)
	jDone1.patch(code)
	jDone2.patch(code)
	jDone3.patch(code)
	jDone.patch(code)
	return code
}

// emitToInt32 converts the JS Number in XMM d to its ES ToInt32 bits in EAX
// (truncate toward zero, wrap mod 2^32; NaN, ±Inf and |x| ≥ 2^63 handled by a
// rare slow path), bit-identical to quickInt32/quickUint32. Clobbers RAX,
// RCX, RDX; the caller continues with EAX valid on every path.
func emitToInt32(code []byte, d int, pool *[]ripConstRef) []byte {
	// Fast path: the truncated int64 conversion is exact for −2^63 < x < 2^63,
	// and the low 32 bits equal ToInt32 (mod 2^32 wrap). Out-of-range values,
	// NaN and ±Inf all produce INT64_MIN, which is the slow-path trigger
	// (x == −2^63 lands there too and computes the correct 0).
	code = append(code, 0xF2, 0x48, 0x0F, 0x2C, byte(0xC0|d)) // CVTTSD2SI RAX,XMMd
	code = append(code, 0x48, 0xB9)                           // MOV RCX,imm64
	code = appendInt64(code, -0x8000000000000000)
	code = append(code, 0x48, 0x39, 0xC8)          // CMP RAX,RCX
	code, jFast := appendLocalCondJump(code, 0x85) // JNE done (fast path)

	// Slow path: x is NaN, ±Inf, |x| ≥ 2^63, or exactly −2^63. For NaN/±Inf
	// ToInt32 is 0. For finite |x| ≥ 2^63 the double is already an integer
	// with bits x = m·2^s (s = exponent−52 ∈ [11,31]); x mod 2^32 is the low
	// 32 bits of (bits<<s), and a negative x negates the unsigned value.
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC2|(d<<3))) // MOVQ RDX,XMMd (original bits, sign kept)
	code = emitANDpdRipConst(code, d, 0x7FFFFFFFFFFFFFFF, pool)    // ANDPD XMMd,[RIP+absMask]
	code = emitUCOMISDRipConst(code, d, 0x7FF0000000000000, pool)  // UCOMISD XMMd,[RIP++Inf]
	code, jZero := appendLocalCondJump(code, 0x84)                 // JZ zero (NaN or +Inf)
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC1|(d<<3))) // MOVQ RCX,XMMd
	code = append(code, 0x48, 0xC1, 0xE9, 0x34)                    // SHR RCX,52
	code = append(code, 0x81, 0xE1, 0xFF, 0x07, 0x00, 0x00)        // AND ECX,0x7FF
	code = append(code, 0x81, 0xE9, 0x33, 0x04, 0x00, 0x00)        // SUB ECX,1075 (exponent−52)
	code = append(code, 0x83, 0xF9, 0x20)                          // CMP ECX,32
	code, jBig := appendLocalCondJump(code, 0x83)                  // JAE zero (|x| ≥ 2^84, multiple of 2^32)
	code = append(code, 0x66, 0x48, 0x0F, 0x7E, byte(0xC0|(d<<3))) // MOVQ RAX,XMMd
	code = append(code, 0x48, 0xD3, 0xE0)                          // SHL RAX,CL
	code = append(code, 0x25, 0xFF, 0xFF, 0xFF, 0xFF)              // AND EAX,0xFFFFFFFF
	code, jSign := appendLocalJump(code)                           // JMP sign
	// The zero and big-quotient jumps are patched before the XOR so they land
	// on it; the sign jump lands on the TEST below.
	jZero.patch(code)
	jBig.patch(code)
	code = append(code, 0x31, 0xC0) // XOR EAX,EAX (zero path)
	jSign.patch(code)
	// sign: negate the unsigned magnitude when the original x was negative.
	code = append(code, 0x48, 0x85, 0xD2) // TEST RDX,RDX
	code, jDone := appendLocalCondJump(code, 0x89)
	code = append(code, 0xF7, 0xD8) // NEG EAX
	jDone.patch(code)
	jFast.patch(code)
	return code
}
