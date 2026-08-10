package compiler

// I-2：小函数内联调用点展开。
//
// 编译期把 `const f = (a,b)=>expr;` 这类"可内联函数"的调用点 `f(x,y)` 展开
// 为内联体指令（槽位/常量索引平移），消除 OpCall/建帧/拆帧/一次递归 run()。
// 未命中（非 const 绑定/遮蔽/实参超限/白名单外指令/字段溢出）回退普通 OpCall，
// 语义不受影响。
//
// 安全边界：
//   - 白名单指令：operand 字段明确（槽 / 常量索引 / 立即数），复制时可平移；
//     含跳转/调用/闭包/对象操作的表达式不内联（回退）。
//   - 内联体无闭包/this/arguments（I-1 判定保证），复制到调用者帧后
//     LoadLocal/StoreLocal 槽号相对调用者帧重映射。
//   - 常量池合并：内联体 Constants 追加到调用者，常量/属性名索引统一平移。

import (
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// inlineSafeOp 报告 op 是否在可内联白名单内（operand 字段可安全重映射）。
func inlineSafeOp(op bytecode.Opcode) bool {
	switch op {
	case bytecode.OpLoadLocal, bytecode.OpStoreLocal,
		bytecode.OpPushConst, bytecode.OpPushInt, bytecode.OpPushNegInt,
		bytecode.OpPushUndefined, bytecode.OpPushNull, bytecode.OpPushTrue, bytecode.OpPushFalse,
		bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow,
		bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor,
		bytecode.OpShl, bytecode.OpShr, bytecode.OpUShr,
		bytecode.OpNeg, bytecode.OpNot, bytecode.OpBitNot, bytecode.OpUnaryPlus,
		bytecode.OpEq, bytecode.OpStrictEq, bytecode.OpStrictNe, bytecode.OpNe,
		bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe,
		bytecode.OpGetProp, bytecode.OpGetPropLocal, bytecode.OpCallMethod,
		bytecode.OpReturn, bytecode.OpReturnUndef:
		return true
	}
	return false
}

// isInlinableCode 扫描函数体指令，全部在白名单内才可内联。
func isInlinableCode(code []byte) bool {
	for pc := 0; pc+bytecode.InstrSize <= len(code); pc += bytecode.InstrSize {
		if !inlineSafeOp(bytecode.Opcode(code[pc])) {
			return false
		}
	}
	return true
}

// canInlineCode 预检：指令在白名单内且槽位/常量索引平移后不溢出 24 位 operand。
func canInlineCode(code []byte, slotBase, constBase int) bool {
	for pc := 0; pc+bytecode.InstrSize <= len(code); pc += bytecode.InstrSize {
		op := bytecode.Opcode(code[pc])
		if !inlineSafeOp(op) {
			return false
		}
		operand := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		switch op {
		case bytecode.OpLoadLocal, bytecode.OpStoreLocal:
			if int(operand)+slotBase > 0xFFFFFF {
				return false
			}
		case bytecode.OpPushConst, bytecode.OpGetProp:
			if int(operand)+constBase > 0xFFFF {
				return false
			}
		case bytecode.OpGetPropLocal:
			// slot 在 operand 高 16 位（编译期 slot ≤0xFF 不溢出 24 位）。
			if int(operand>>16)+slotBase > 0xFF || int(operand&0xFFFF)+constBase > 0xFFFF {
				return false
			}
		case bytecode.OpCallMethod:
			if int(operand&0xFFFF)+constBase > 0xFFFF {
				return false
			}
		}
	}
	return true
}

// inlineRebaseOperand 平移内联体指令 operand：槽字段 += slotBase，
// 常量/属性名索引字段 += constBase。
func inlineRebaseOperand(op bytecode.Opcode, operand uint32, slotBase, constBase int) uint32 {
	switch op {
	case bytecode.OpLoadLocal, bytecode.OpStoreLocal:
		return operand + uint32(slotBase)
	case bytecode.OpPushConst, bytecode.OpGetProp:
		return operand + uint32(constBase)
	case bytecode.OpGetPropLocal:
		slot := (int(operand>>16) + slotBase) & 0xFF
		nameIdx := (int(operand&0xFFFF) + constBase) & 0xFFFF
		return uint32(slot)<<16 | uint32(nameIdx)
	case bytecode.OpCallMethod:
		nameIdx := (int(operand&0xFFFF) + constBase) & 0xFFFF
		return (operand & 0xFFFF0000) | uint32(nameIdx)
	}
	return operand
}

// tryInlineCall 尝试把 `id(args)` 展开为可内联函数体。成功返回 true。
// 返回 false 时调用方走普通 OpCall 路径。
//
// 展开顺序（实参求值可能在调用者帧分配临时槽/常量，必须先于基址确定）：
//  1. 依次求值实参 → 压栈
//  2. 确定槽位基址（slotBase = 当前 NumLocals）并分配 this+参数槽
//  3. 倒序 StoreLocal 把栈上实参存入参数槽
//  4. 确定常量基址并复制内联体常量 → 复制内联体指令（槽/常量平移）
func (c *Compiler) tryInlineCall(id *ast.Identifier, args []ast.Expression) bool {
	fc := c.cur()
	funcIdx, ok := fc.inlineCandidates[id.Name]
	if !ok || funcIdx < 0 || funcIdx >= len(c.module.Functions) {
		return false
	}
	inlineFn := c.module.Functions[funcIdx]
	if !inlineFn.Inlinable || len(args) > inlineFn.NumParams {
		return false
	}
	// 1. 求值实参（压栈；可能增长调用者 NumLocals / 常量池）。
	for _, a := range args {
		if err := c.compileExpr(a); err != nil {
			return false
		}
	}
	// 2. 槽位基址 = 分配前的 NumLocals（含实参临时槽）；分配 this+参数槽。
	slotBase := fc.tmpl.NumLocals
	for i := 0; i < inlineFn.NumParams+1; i++ {
		c.newSlot()
	}
	// 3. 倒序存实参到参数槽（栈顶是最后一个实参）。
	for i := len(args) - 1; i >= 0; i-- {
		c.emit(bytecode.OpStoreLocal, uint32(slotBase+1+i))
	}
	// 4. 常量基址 = 复制前的常量池大小（实参常量已并入）；预检 + 复制。
	constBase := len(fc.tmpl.Constants)
	if !canInlineCode(inlineFn.Code, slotBase, constBase) {
		return false
	}
	for _, v := range inlineFn.Constants {
		fc.tmpl.AddConst(v)
	}
	emitInlinedCode(c, inlineFn.Code, slotBase, constBase)
	return true
}

// emitInlinedCode 复制内联体指令到当前函数，跳过末尾 OpReturn/OpReturnUndef。
func emitInlinedCode(c *Compiler, code []byte, slotBase, constBase int) {
	end := len(code)
	for end >= bytecode.InstrSize {
		op := bytecode.Opcode(code[end-bytecode.InstrSize])
		if op == bytecode.OpReturn || op == bytecode.OpReturnUndef {
			end -= bytecode.InstrSize
		} else {
			break
		}
	}
	for pc := 0; pc < end; pc += bytecode.InstrSize {
		op := bytecode.Opcode(code[pc])
		operand := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		c.emit(op, inlineRebaseOperand(op, operand, slotBase, constBase))
	}
}
