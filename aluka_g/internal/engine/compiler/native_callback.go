package compiler

// O-6：简单回调检测（数组高阶方法原生执行）。
//
// 箭头函数体为单一表达式、参数 ≤2 且无闭包依赖时，编译器把表达式翻译成
// bytecode.CBInstr 微指令序列（小栈求值）；运行时数组高阶方法
// （map/filter/reduce/forEach）在 Go 侧直接求值，跳过每元素完整调用链
// （帧 + 解释）。
//
// 覆盖表达式（可嵌套组合）：
//   - x => x                      （恒等）
//   - x => K                      （字面量）
//   - x => -x                     （一元负）
//   - x => x OP K / K OP x / x OP x / x.prop OP K / acc OP x.prop …
//     （二元算术/位/比较，操作数为参数/字面量/单层属性读，可嵌套）
//   - x => x.name                 （属性读，链深 1）
// 其余表达式一律返回 nil（走正常调用路径，语义不受影响）。

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// analyzeSimpleCallback 检测箭头函数是否为 O-6 简单回调。
// 非简单（多语句体/解构参数/默认值/rest/复杂表达式/闭包引用）返回 nil。
func (c *Compiler) analyzeSimpleCallback(params []*ast.Identifier, body ast.Node) *bytecode.NativeCallbackDesc {
	if len(params) == 0 || len(params) > 2 {
		return nil
	}
	for _, p := range params {
		if p == nil || p.Name == "" {
			return nil
		}
	}
	var expr ast.Expression
	switch b := body.(type) {
	case ast.Expression:
		expr = b
	case *ast.BlockStmt:
		if len(b.Body) != 1 {
			return nil
		}
		rs, ok := b.Body[0].(*ast.ReturnStmt)
		if !ok || rs.Arg == nil {
			return nil
		}
		expr = rs.Arg
	default:
		return nil
	}
	instrs, maxParam := c.cbTranslate(params, expr)
	if instrs == nil || len(instrs) == 0 {
		return nil
	}
	return &bytecode.NativeCallbackDesc{ParamCount: uint8(maxParam), Instrs: instrs}
}

// hasNonNilDefaults 报告 defaults 中是否存在非 nil 条目（parser 对多参数
// 箭头会填 nil 条目数组，须按条目判断而非数组长度）。
func hasNonNilDefaults(defaults []ast.Expression) bool {
	for _, d := range defaults {
		if d != nil {
			return true
		}
	}
	return false
}

// hasNonNilPatterns 报告 patterns（解构参数）中是否存在非 nil 条目。
func hasNonNilPatterns(patterns []ast.Pattern) bool {
	for _, p := range patterns {
		if p != nil {
			return true
		}
	}
	return false
}

// cbParamIdx 返回标识符是否为回调参数（0/1）；-1 非参数。
func cbParamIdx(params []*ast.Identifier, name string) int {
	for i, p := range params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// cbTranslate 把表达式翻译为微指令序列。返回 (nil, 0) 表示不支持。
// maxParam 是引用到的最大参数索引（0/1 → ParamCount 1/2）。
func (c *Compiler) cbTranslate(params []*ast.Identifier, expr ast.Expression) ([]bytecode.CBInstr, int) {
	fc := c.cur()
	var out []bytecode.CBInstr
	maxParam := 0
	push := func(in bytecode.CBInstr) {
		out = append(out, in)
	}
	// paramOp 压入参数（0/1）。
	paramOp := func(pi int) bool {
		switch pi {
		case 0:
			push(bytecode.CBInstr{Op: bytecode.CBPushParam0})
			return true
		case 1:
			push(bytecode.CBInstr{Op: bytecode.CBPushParam1})
			if 1 > maxParam {
				maxParam = 1
			}
			return true
		}
		return false
	}
	// 属性操作数：param.prop。
	propOp := func(pi int, propName string) bool {
		switch pi {
		case 0:
			push(bytecode.CBInstr{Op: bytecode.CBPushProp0, Operand: uint32(fc.tmpl.AddStringConst(propName))})
			return true
		case 1:
			push(bytecode.CBInstr{Op: bytecode.CBPushProp1, Operand: uint32(fc.tmpl.AddStringConst(propName))})
			if 1 > maxParam {
				maxParam = 1
			}
			return true
		}
		return false
	}

	// translate 递归翻译子表达式（二元/一元/属性/参数/字面量）。
	var translate func(e ast.Expression) bool
	translate = func(e ast.Expression) bool {
		switch n := e.(type) {
		case *ast.Identifier:
			return paramOp(cbParamIdx(params, n.Name))
		case *ast.MemberExpr:
			if n.Computed || n.Optional {
				return false
			}
			if id, ok := n.Object.(*ast.Identifier); ok {
				if pn, ok := n.Property.(*ast.Identifier); ok {
					return propOp(cbParamIdx(params, id.Name), pn.Name)
				}
			}
			return false
		case *ast.UnaryExpr:
			if n.Op == "-" {
				if !translate(n.Arg) {
					return false
				}
				push(bytecode.CBInstr{Op: bytecode.CBNeg})
				return true
			}
			return false
		case *ast.BinaryExpr:
			op, kind, ok := cbBinaryOp(n.Op)
			if !ok {
				return false
			}
			if !translate(n.Left) || !translate(n.Right) {
				return false
			}
			push(bytecode.CBInstr{Op: kind, Operand: uint32(op)})
			return true
		}
		// 字面量常量。
		if v, ok := cbConstValue(e); ok {
			push(bytecode.CBInstr{Op: bytecode.CBPushConst, Operand: uint32(fc.tmpl.AddConst(v))})
			return true
		}
		return false
	}

	if !translate(expr) {
		return nil, 0
	}
	return out, maxParam + 1
}

// cbConstValue 识别字面量 → engine.Value。
func cbConstValue(expr ast.Expression) (engine.Value, bool) {
	switch lit := expr.(type) {
	case *ast.NumberLit:
		return engine.Number(lit.Value), true
	case *ast.StringLit:
		return engine.Str(lit.Value), true
	case *ast.BoolLit:
		return engine.Boolean(lit.Value), true
	case *ast.NullLit:
		return engine.Null(), true
	}
	return nil, false
}

// cbBinaryOp 映射运算符 → (opcode, 指令种类)。
func cbBinaryOp(op string) (bytecode.Opcode, bytecode.CBOpcode, bool) {
	if o, ok := binaryOps[op]; ok {
		return o, bytecode.CBBinOp, true
	}
	if o, ok := compareOps[op]; ok {
		return o, bytecode.CBCmp, true
	}
	return 0, 0, false
}

// binaryOps 算术/位运算符 → bytecode opcode。
var binaryOps = map[string]bytecode.Opcode{
	"+": bytecode.OpAdd, "-": bytecode.OpSub, "*": bytecode.OpMul,
	"/": bytecode.OpDiv, "%": bytecode.OpMod, "**": bytecode.OpPow,
	"<<": bytecode.OpShl, ">>": bytecode.OpShr, ">>>": bytecode.OpUShr,
	"&": bytecode.OpBitAnd, "|": bytecode.OpBitOr, "^": bytecode.OpBitXor,
}

// compareOps 比较运算符 → bytecode opcode。
var compareOps = map[string]bytecode.Opcode{
	"==": bytecode.OpEq, "===": bytecode.OpStrictEq,
	"!=": bytecode.OpNe, "!==": bytecode.OpStrictNe,
	"<": bytecode.OpLt, "<=": bytecode.OpLe,
	">": bytecode.OpGt, ">=": bytecode.OpGe,
}
