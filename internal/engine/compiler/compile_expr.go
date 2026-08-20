package compiler

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func (c *Compiler) compileExpr(e ast.Expression) error {
	switch n := e.(type) {
	case *ast.NumberLit:
		f := n.Value
		if f == float64(int64(f)) {
			iv := int64(f)
			if iv >= 0 && iv < (1<<24) {
				c.emit(bytecode.OpPushInt, uint32(iv))
				return nil
			}
			if iv < 0 && iv > -(1<<24) {
				c.emit(bytecode.OpPushNegInt, uint32(-iv))
				return nil
			}
		}
		idx := c.cur().tmpl.AddConst(engine.Number(f))
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	case *ast.BigIntLit:
		// BigInt 字面量：解析十进制字符串为 *big.Int，放入常量池。
		bi, ok := new(big.Int).SetString(n.Text, 10)
		if !ok {
			return fmt.Errorf("invalid BigInt literal: %s", n.Text)
		}
		idx := c.cur().tmpl.AddConst(engine.BigInt(bi))
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	case *ast.StringLit:
		idx := c.cur().tmpl.AddStringConst(n.Value)
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	case *ast.BoolLit:
		if n.Value {
			c.emit(bytecode.OpPushTrue, 0)
		} else {
			c.emit(bytecode.OpPushFalse, 0)
		}
		return nil
	case *ast.NullLit:
		c.emit(bytecode.OpPushNull, 0)
		return nil
	case *ast.UndefinedLit:
		c.emit(bytecode.OpPushUndefined, 0)
		return nil
	case *ast.Identifier:
		return c.compileIdentifier(n)
	case *ast.JSXElement:
		return c.compileExpr(lowerJSXElement(n))
	case *ast.JSXFragment:
		return c.compileExpr(lowerJSXFragment(n))
	case *ast.ThisExpr:
		// `this` 按词法解析 `__this__`：普通函数声明为 local slot 0；
		// 箭头函数未声明，经 upvalue 链解析为外层函数的 `this`（P0-2）。
		kind, idx := c.resolve("__this__")
		switch kind {
		case "local":
			c.emit(bytecode.OpLoadLocal, uint32(idx))
		case "upvalue":
			c.emit(bytecode.OpLoadUpvalue, uint32(idx))
		default:
			c.emit(bytecode.OpPushUndefined, 0)
		}
		return nil
	case *ast.NewTargetExpr:
		// `new.target` 按词法解析 `__newTarget__`：非箭头函数为 local 槽位
		// （VM 在 new 调用时填入构造器），箭头函数经 upvalue 链继承外层。
		kind, idx := c.resolve("__newTarget__")
		switch kind {
		case "local", "upvalue":
			if kind == "local" {
				c.emit(bytecode.OpLoadLocal, uint32(idx))
			} else {
				c.emit(bytecode.OpLoadUpvalue, uint32(idx))
			}
		default:
			c.emit(bytecode.OpPushUndefined, 0)
		}
		return nil
	case *ast.ArrayLit:
		// Fast path: no spread → use OpNewArray.
		hasSpread := false
		for _, el := range n.Elements {
			if _, ok := el.(*ast.SpreadElement); ok {
				hasSpread = true
				break
			}
		}
		if !hasSpread {
			for _, el := range n.Elements {
				if el == nil {
					// Preserve sparse-array position and length. The current array
					// representation stores holes as undefined values.
					c.emit(bytecode.OpPushUndefined, 0)
					continue
				}
				if err := c.compileExpr(el); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpNewArray, uint32(len(n.Elements)))
			return nil
		}
		// Slow path: build incrementally with spread support.
		c.emit(bytecode.OpBuildArray, 0)
		for _, el := range n.Elements {
			if el == nil {
				c.emit(bytecode.OpPushUndefined, 0)
				c.emit(bytecode.OpArrayPush, 0)
				continue
			}
			if sp, ok := el.(*ast.SpreadElement); ok {
				if err := c.compileExpr(sp.Arg); err != nil {
					return err
				}
				c.emit(bytecode.OpArraySpread, 0)
			} else {
				if err := c.compileExpr(el); err != nil {
					return err
				}
				c.emit(bytecode.OpArrayPush, 0)
			}
		}
		return nil
	case *ast.ObjectLit:
		if isSimpleObjectLiteral(n) {
			for _, prop := range n.Properties {
				keyIdx := c.cur().tmpl.AddStringConst(propKey(prop.Key))
				c.emit(bytecode.OpPushConst, uint32(keyIdx))
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpNewObject, uint32(len(n.Properties)))
			return nil
		}
		c.emit(bytecode.OpNewObject, 0)
		for _, prop := range n.Properties {
			if prop.Kind == ast.PropertySpread {
				// `{...obj}`: copy own enumerable props into the target.
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
				c.emit(bytecode.OpSpreadObject, 0)
				continue
			}
			// get/set 访问器：把函数注册为对象上的 accessor。
			if prop.Kind == ast.PropertyGet || prop.Kind == ast.PropertySet {
				if prop.Computed {
					if err := c.compileExpr(prop.Key); err != nil {
						return err
					}
					if err := c.compileExpr(prop.Value); err != nil {
						return err
					}
					if prop.Kind == ast.PropertyGet {
						c.emit(bytecode.OpSetGetterComputedObj, 0)
					} else {
						c.emit(bytecode.OpSetSetterComputedObj, 0)
					}
					continue
				}
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
				key := propKey(prop.Key)
				nameIdx := c.cur().tmpl.AddStringConst(key)
				if prop.Kind == ast.PropertyGet {
					c.emit(bytecode.OpSetGetterObj, uint32(nameIdx))
				} else {
					c.emit(bytecode.OpSetSetterObj, uint32(nameIdx))
				}
				continue
			}
			if prop.Computed {
				// { [expr]: value } — evaluate key at runtime.
				if err := c.compileExpr(prop.Key); err != nil {
					return err
				}
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
				c.emit(bytecode.OpSetPropComputedObj, 0)
				continue
			}
			if err := c.compileExpr(prop.Value); err != nil {
				return err
			}
			key := propKey(prop.Key)
			nameIdx := c.cur().tmpl.AddStringConst(key)
			c.emit(bytecode.OpSetPropObj, uint32(nameIdx))
		}
		return nil
	case *ast.BinaryExpr:
		return c.compileBinary(n)
	case *ast.LogicalExpr:
		return c.compileLogical(n)
	case *ast.UnaryExpr:
		return c.compileUnary(n)
	case *ast.UpdateExpr:
		return c.compileUpdate(n)
	case *ast.AssignExpr:
		return c.compileAssign(n)
	case *ast.CallExpr:
		return c.compileCall(n)
	case *ast.NewExpr:
		return c.compileNew(n)
	case *ast.MemberExpr:
		return c.compileMember(n)
	case *ast.FunctionExpr:
		// 具名函数表达式（NFE）：名字在函数体内绑定为不可变自引用。
		return c.compileFunction(funcNameFromExpr(n), n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, n.IsAsync, n.IsGenerator, false, n.Name != nil)
	case *ast.ArrowFunc:
		return c.compileFunction("", n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, n.IsAsync, false, true, false)
	case *ast.ClassExpr:
		return c.compileClassExpr(n)
	case *ast.ConditionalExpr:
		return c.compileConditional(n)
	case *ast.SequenceExpr:
		for i, e := range n.Expressions {
			if err := c.compileExpr(e); err != nil {
				return err
			}
			if i < len(n.Expressions)-1 {
				c.emit(bytecode.OpPop, 0)
			}
		}
		return nil
	case *ast.RegexLit:
		// 正则字面量：压入 pattern 与 flags，OpMakeRegexp 构造 RegExp 实例。
		patIdx := c.cur().tmpl.AddStringConst(n.Pattern)
		flagsIdx := c.cur().tmpl.AddStringConst(n.Flags)
		c.emit(bytecode.OpPushConst, uint32(patIdx))
		c.emit(bytecode.OpPushConst, uint32(flagsIdx))
		c.emit(bytecode.OpMakeRegexp, 0)
		return nil
	case *ast.TemplateLit:
		return c.compileTemplateLit(n)
	case *ast.TaggedTemplateExpr:
		return c.compileTaggedTemplate(n)
	case *ast.YieldExpr:
		return c.compileYield(n)
	case *ast.AwaitExpr:
		return c.compileAwait(n)
	}
	return fmt.Errorf("unsupported expression %T", e)
}

// isSimpleObjectLiteral reports whether a literal can be built in one batch.
// Computed keys, spread, and accessors need the partially-built object and keep
// using the incremental instruction sequence.
func isSimpleObjectLiteral(n *ast.ObjectLit) bool {
	for _, prop := range n.Properties {
		if prop.Computed || prop.Kind == ast.PropertySpread ||
			prop.Kind == ast.PropertyGet || prop.Kind == ast.PropertySet {
			return false
		}
	}
	return true
}

// compileTaggedTemplate compiles a tagged template `tag`a${x}b“.
// 栈布局（复用现有指令，无需新 opcode）：
//
//	非成员 tag：compile tag → [tag]
//	成员 tag obj.tag：compile obj → [obj]（OpCallMethod 内部取方法并绑定 this=obj）
//	计算成员 tag obj[k]：OpDup/GetElem/Swap → [method, obj]
//	再压 cooked quasis + OpNewArray → [..., stringsArr]
//	压 raw quasis + OpNewArray + OpSetPropObj("raw") → [..., stringsArr]
//	编译插值表达式 → [..., stringsArr, e1..eM]
//	OpCall/OpCallMethod/OpCallWithThis（参数数 = 1+插值数）
func (c *Compiler) compileTaggedTemplate(n *ast.TaggedTemplateExpr) error {
	tmpl := n.Template
	switch m := n.Tag.(type) {
	case *ast.MemberExpr:
		if m.Computed {
			// [obj] → OpDup → [obj, obj] → compile key → [obj, obj, key]
			// → OpGetElem → [obj, method] → OpSwap → [method, obj(this)]
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
			c.emit(bytecode.OpDup, 0)
			if err := c.compileExpr(m.Property); err != nil {
				return err
			}
			c.emit(bytecode.OpGetElem, 0)
			c.emit(bytecode.OpSwap, 0)
		} else {
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
		}
	default:
		if err := c.compileExpr(n.Tag); err != nil {
			return err
		}
	}
	// TemplateStringsArray：cooked quasis。
	for _, q := range tmpl.Quasis {
		idx := c.cur().tmpl.AddStringConst(q)
		c.emit(bytecode.OpPushConst, uint32(idx))
	}
	c.emit(bytecode.OpNewArray, uint32(len(tmpl.Quasis)))
	// strings.raw 数组：raw quasis。
	for _, rq := range tmpl.RawQuasis {
		idx := c.cur().tmpl.AddStringConst(rq)
		c.emit(bytecode.OpPushConst, uint32(idx))
	}
	c.emit(bytecode.OpNewArray, uint32(len(tmpl.RawQuasis)))
	rawIdx := c.cur().tmpl.AddStringConst("raw")
	c.emit(bytecode.OpSetPropObj, uint32(rawIdx))
	// 插值表达式。
	for _, e := range tmpl.Expressions {
		if err := c.compileExpr(e); err != nil {
			return err
		}
	}
	numArgs := uint32(1 + len(tmpl.Expressions))
	switch m := n.Tag.(type) {
	case *ast.MemberExpr:
		if m.Computed {
			c.emit(bytecode.OpCallWithThis, numArgs)
		} else {
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpCallMethod, numArgs<<16|uint32(nameIdx&0xFFFF))
		}
	default:
		c.emit(bytecode.OpCall, numArgs)
	}
	return nil
}

// compileTemplateLit compiles a template literal by concatenating string
// quasis with interpolated expression values using the binary + operator.
// `Hello, ${name}!` compiles as: "" + "Hello, " + name + "!" (with the
// leading empty quasi when the template starts with ${).
func (c *Compiler) compileTemplateLit(n *ast.TemplateLit) error {
	// Start with the first quasi (may be empty).
	idx := c.cur().tmpl.AddStringConst(n.Quasis[0])
	c.emit(bytecode.OpPushConst, uint32(idx))
	// Interleave: expression + next quasi.
	for i, expr := range n.Expressions {
		if err := c.compileExpr(expr); err != nil {
			return err
		}
		c.emit(bytecode.OpAdd, 0)
		quasiIdx := c.cur().tmpl.AddStringConst(n.Quasis[i+1])
		c.emit(bytecode.OpPushConst, uint32(quasiIdx))
		c.emit(bytecode.OpAdd, 0)
	}
	return nil
}

// compileYield compiles a `yield` / `yield*` expression.
//
//	yield        -> push undefined; OpYield
//	yield expr   -> compile expr; OpYield
//	yield* expr  -> compile expr; OpGetIterator; loop { next(); if done push
//	               value & break; else OpYield (discard sent value) }
//
// OpYield pops the yielded value and suspends; on resume it pushes the value
// passed to .next(v), which becomes the result of the yield expression.
func (c *Compiler) compileYield(n *ast.YieldExpr) error {
	if n.Delegate {
		return c.compileYieldStar(n)
	}
	if n.Argument == nil {
		c.emit(bytecode.OpPushUndefined, 0)
	} else {
		if err := c.compileExpr(n.Argument); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpYield, 0)
	return nil
}

// compileYieldStar compiles `yield* expr`: iterate the iterable produced by
// expr and yield each value in turn. The result of the yield* expression is
// the iterator's final {value} (when done becomes true).
func (c *Compiler) compileYieldStar(n *ast.YieldExpr) error {
	c.pushBlock()
	defer c.popBlock()

	// Evaluate the iterable and obtain its iterator.
	if err := c.compileExpr(n.Argument); err != nil {
		return err
	}
	c.emit(bytecode.OpGetIterator, 0)
	tmpIter := c.declareLocal("__yield_star_iter__")
	c.emit(bytecode.OpStoreLocal, uint32(tmpIter))

	tmpResult := c.declareLocal("__yield_star_result__")

	nameNext := c.cur().tmpl.AddStringConst("next")
	nameDone := c.cur().tmpl.AddStringConst("done")
	nameValue := c.cur().tmpl.AddStringConst("value")

	loopStart := c.curPC()

	// iter.next()
	c.emit(bytecode.OpLoadLocal, uint32(tmpIter))
	c.emit(bytecode.OpCallMethod, uint32(nameNext)) // 0 args
	c.emit(bytecode.OpStoreLocal, uint32(tmpResult))

	// if result.done -> push result.value and exit loop.
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameDone))
	jExit := c.emit(bytecode.OpJmpTruePop, 0)

	c.pushLoop(loopStart, 0)
	defer c.popLoop()

	// yield result.value (discard the value sent back by .next()).
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameValue))
	c.emit(bytecode.OpYield, 0)
	c.emit(bytecode.OpPop, 0) // discard sent value

	// continue -> jump back to loopStart.
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))

	// exit: push the final value as the result of yield*.
	exit := c.curPC()
	c.patchJumpToHere(jExit)
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameValue))
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
	return nil
}

// compileAwait compiles `await expr`: evaluate the argument, then emit OpAwait
// which suspends the async function until the (promise-wrapped) value settles.
// On resume, OpAwait pushes the resolved value (or throws the rejection).
func (c *Compiler) compileAwait(n *ast.AwaitExpr) error {
	if n.Argument == nil {
		c.emit(bytecode.OpPushUndefined, 0)
	} else {
		if err := c.compileExpr(n.Argument); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpAwait, 0)
	return nil
}

func (c *Compiler) compileIdentifier(n *ast.Identifier) error {
	// 私有字段名（#field，lexer 产出的值为 "#field"）在表达式位置作为属性键
	// 字面量：`#x in obj` → `"#x" in obj`（私有字段存在性检查，undici 等库
	// 依赖）。`this.#x` 走成员访问（compileMember 用 "#x" 属性名），不走这里。
	if strings.HasPrefix(n.Name, "#") {
		fmt.Printf("DEBUG compileIdentifier # %q\n", n.Name)
		idx := c.cur().tmpl.AddStringConst(n.Name)
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	}
	kind, idx := c.resolve(n.Name)
	switch kind {
	case "local":
		c.emit(bytecode.OpLoadLocal, uint32(idx))
	case "upvalue":
		c.emit(bytecode.OpLoadUpvalue, uint32(idx))
	case "global":
		c.emit(bytecode.OpLoadGlobal, uint32(idx))
	}
	return nil
}

func (c *Compiler) compileBinary(n *ast.BinaryExpr) error {
	// `#name in obj`：私有字段存在性检查（ES2022）。左操作数 `#name` 是属性键
	// 而非读取（`this.#name` 读取返回字段值，`42 in obj` 恒 false）。编译为
	// PUSH_CONST "#name" 使 in 检查属性存在（undici 迭代器 brand 依赖）。
	if n.Op == "in" {
		if id, ok := n.Left.(*ast.Identifier); ok && strings.HasPrefix(id.Name, "#") {
			idx := c.cur().tmpl.AddStringConst(id.Name)
			c.emit(bytecode.OpPushConst, uint32(idx))
			if err := c.compileExpr(n.Right); err != nil {
				return err
			}
			c.emit(bytecode.OpIn, 0)
			return nil
		}
	}
	if err := c.compileExpr(n.Left); err != nil {
		return err
	}
	if err := c.compileExpr(n.Right); err != nil {
		return err
	}
	op, err := binaryOpcode(n.Op)
	if err != nil {
		return err
	}
	c.emit(op, 0)
	return nil
}

func binaryOpcode(op string) (bytecode.Opcode, error) {
	switch op {
	case "+":
		return bytecode.OpAdd, nil
	case "-":
		return bytecode.OpSub, nil
	case "*":
		return bytecode.OpMul, nil
	case "/":
		return bytecode.OpDiv, nil
	case "%":
		return bytecode.OpMod, nil
	case "**":
		return bytecode.OpPow, nil
	case "&":
		return bytecode.OpBitAnd, nil
	case "|":
		return bytecode.OpBitOr, nil
	case "^":
		return bytecode.OpBitXor, nil
	case "<<":
		return bytecode.OpShl, nil
	case ">>":
		return bytecode.OpShr, nil
	case ">>>":
		return bytecode.OpUShr, nil
	case "==":
		return bytecode.OpEq, nil
	case "!=":
		return bytecode.OpNe, nil
	case "===":
		return bytecode.OpStrictEq, nil
	case "!==":
		return bytecode.OpStrictNe, nil
	case "<":
		return bytecode.OpLt, nil
	case "<=":
		return bytecode.OpLe, nil
	case ">":
		return bytecode.OpGt, nil
	case ">=":
		return bytecode.OpGe, nil
	case "instanceof":
		return bytecode.OpInstanceof, nil
	case "in":
		return bytecode.OpIn, nil
	}
	return 0, fmt.Errorf("unsupported binary op %s", op)
}

func (c *Compiler) compileLogical(n *ast.LogicalExpr) error {
	if err := c.compileExpr(n.Left); err != nil {
		return err
	}
	switch n.Op {
	case "&&":
		j := c.emit(bytecode.OpJmpFalseKeep, 0)
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.patchJumpToHere(j)
	case "||":
		j := c.emit(bytecode.OpJmpTrueKeep, 0)
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.patchJumpToHere(j)
	case "??":
		j := c.emit(bytecode.OpJmpNullishKeep, 0)
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.patchJumpToHere(j)
	default:
		return fmt.Errorf("unsupported logical op %s", n.Op)
	}
	return nil
}

func (c *Compiler) compileUnary(n *ast.UnaryExpr) error {
	if n.Op == "typeof" {
		if id, ok := n.Arg.(*ast.Identifier); ok {
			kind, _ := c.resolve(id.Name)
			if kind == "global" {
				nameIdx := c.cur().tmpl.AddStringConst(id.Name)
				c.emit(bytecode.OpTypeofGlobal, uint32(nameIdx))
				return nil
			}
		}
	}
	if n.Op == "delete" {
		if member, ok := n.Arg.(*ast.MemberExpr); ok {
			if err := c.compileExpr(member.Object); err != nil {
				return err
			}
			if member.Computed {
				if err := c.compileExpr(member.Property); err != nil {
					return err
				}
				c.emit(bytecode.OpDelElem, 0)
				return nil
			}
			propName := ""
			if id, ok := member.Property.(*ast.Identifier); ok {
				propName = id.Name
			} else if s, ok := member.Property.(*ast.StringLit); ok {
				propName = s.Value
			}
			nameIdx := c.cur().tmpl.AddStringConst(propName)
			c.emit(bytecode.OpDelProp, uint32(nameIdx))
			return nil
		}
		// Fallback: evaluate and discard, return true.

		if err := c.compileExpr(n.Arg); err != nil {
			return err
		}
		c.emit(bytecode.OpPop, 0)
		c.emit(bytecode.OpPushTrue, 0)
		return nil
	}
	if err := c.compileExpr(n.Arg); err != nil {
		return err
	}
	switch n.Op {
	case "!":
		c.emit(bytecode.OpNot, 0)
	case "-":
		c.emit(bytecode.OpNeg, 0)
	case "+":
		c.emit(bytecode.OpUnaryPlus, 0)
	case "typeof":
		c.emit(bytecode.OpTypeof, 0)
	case "void":
		c.emit(bytecode.OpPop, 0)
		c.emit(bytecode.OpPushUndefined, 0)
	case "~":
		c.emit(bytecode.OpBitNot, 0)
	default:
		return fmt.Errorf("unsupported unary op %s", n.Op)
	}
	return nil
}

func (c *Compiler) compileUpdate(n *ast.UpdateExpr) error {
	// Get current value
	if err := c.compileExpr(n.Arg); err != nil {
		return err
	}
	if !n.Prefix {
		// postfix: we need the OLD value as the result. Dup it.
		c.emit(bytecode.OpDup, 0)
	}
	// Compute new value. OpInc/OpDec pick the successor/predecessor at
	// runtime, preserving the operand type: BigInt stays BigInt (x++ adds
	// 1n, per ES), Number stays Number. A plain `x + 1` must still throw on
	// BigInt, so this cannot reuse OpAdd/OpSub with an inline 1.
	if n.Op == "++" {
		c.emit(bytecode.OpInc, 0)
	} else {
		c.emit(bytecode.OpDec, 0)
	}
	// prefix 需要把"新值"作为表达式结果留在栈上，赋值前 Dup 一份。
	if n.Prefix {
		c.emit(bytecode.OpDup, 0)
	}
	// Store back (consumes the new value).
	if err := c.assignTo(n.Arg); err != nil {
		return err
	}
	// For postfix, the old value remains on the stack (we Dup'd it).
	return nil
}

func (c *Compiler) compileAssign(n *ast.AssignExpr) error {
	if n.Op == "=" {
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.emit(bytecode.OpDup, 0)
		return c.assignTo(n.Left)
	}
	// 逻辑赋值运算符（ES2021）：||= / &&= / ??= 有短路语义。
	// 左值只求值一次：读取 → 短路判断 → 条件成立才编译右值并赋值。
	// 栈布局：[leftVal] → (跳过时保留) / (赋值时为 [rightVal]，赋值后保留)
	switch n.Op {
	case "||=":
		return c.compileLogicalAssign(n.Left, n.Right, bytecode.OpJmpTrueKeep)
	case "&&=":
		return c.compileLogicalAssign(n.Left, n.Right, bytecode.OpJmpFalseKeep)
	case "??=":
		return c.compileLogicalAssign(n.Left, n.Right, bytecode.OpJmpNullishKeep)
	}
	// compound: desugar a OP= b → a = a OP b
	leftExpr, ok := n.Left.(ast.Expression)
	if !ok {
		// 解构模式不能作为复合赋值左值（JS 语法错误）。
		return fmt.Errorf("invalid compound assignment target %T", n.Left)
	}
	if err := c.compileExpr(leftExpr); err != nil {
		return err
	}
	if err := c.compileExpr(n.Right); err != nil {
		return err
	}
	op, err := binaryOpcode(stripAssignSuffix(n.Op))
	if err != nil {
		return err
	}
	c.emit(op, 0)
	c.emit(bytecode.OpDup, 0)
	return c.assignTo(n.Left)
}

// compileLogicalAssign 编译逻辑赋值（||= / &&= / ??=）。
// jmpOp 决定短路条件：OpJmpTrueKeep（||=，truthy 跳过）、OpJmpFalseKeep（&&=，falsy 跳过）、
// OpJmpNullishKeep（??=，null/undefined 跳过）。
//
// 这些 Keep 跳转指令的语义：满足条件时跳过并保留栈顶值；不满足时 pop 栈顶值。
// 字节码序列：
//
//	compileExpr(left)          // push 当前左值
//	OpJmpTrueKeep end          // 满足短路条件 → 保留 left 跳到 end；否则 pop left
//	compileExpr(right)         // push 右值（left 已被跳转指令 pop）
//	OpDup                      // 复制（一份赋值，一份作为结果保留）
//	assignTo(left)             // 赋值给左值
//	end:                       // 栈顶为结果值（left 或 right）
func (c *Compiler) compileLogicalAssign(left ast.Node, right ast.Expression, jmpOp bytecode.Opcode) error {
	if _, ok := left.(*ast.ObjectPattern); ok {
		// 解构模式不能作为逻辑赋值左值（JS 语法错误）。
		return fmt.Errorf("invalid logical assignment target %T", left)
	}
	if _, ok := left.(*ast.ArrayPattern); ok {
		// 解构模式不能作为逻辑赋值左值（JS 语法错误）。
		return fmt.Errorf("invalid logical assignment target %T", left)
	}
	leftExpr, ok := left.(ast.Expression)
	if !ok {
		return fmt.Errorf("invalid logical assignment target %T", left)
	}
	if err := c.compileExpr(leftExpr); err != nil {
		return err
	}
	jumpSkip := c.emit(jmpOp, 0)
	// 不满足短路条件：跳转指令已 pop 左值，直接编译右值并赋值。
	if err := c.compileExpr(right); err != nil {
		return err
	}
	c.emit(bytecode.OpDup, 0)
	if err := c.assignTo(left); err != nil {
		return err
	}
	c.patchJumpToHere(jumpSkip)
	return nil
}

func stripAssignSuffix(op string) string {
	if len(op) > 1 && op[len(op)-1] == '=' {
		return op[:len(op)-1]
	}
	return op
}

// assignTo emits a store into the given reference (identifier / member).
// The value to store must already be on top of the stack.
func (c *Compiler) assignTo(ref ast.Node) error {
	switch r := ref.(type) {
	case *ast.Identifier:
		kind, idx := c.resolve(r.Name)
		switch kind {
		case "local":
			c.emit(bytecode.OpStoreLocal, uint32(idx))
		case "upvalue":
			c.emit(bytecode.OpStoreUpvalue, uint32(idx))
		case "global":
			c.emit(bytecode.OpStoreGlobal, uint32(idx))
		}
		return nil
	case *ast.MemberExpr:
		if r.Computed {
			if err := c.compileExpr(r.Object); err != nil {
				return err
			}
			if err := c.compileExpr(r.Property); err != nil {
				return err
			}
			c.emit(bytecode.OpSetElemTop, 0)
		} else {
			key := r.Property.(*ast.Identifier).Name
			nameIdx := c.cur().tmpl.AddStringConst(key)
			if err := c.compileExpr(r.Object); err != nil {
				return err
			}
			c.emit(bytecode.OpSetPropTop, uint32(nameIdx))
		}
		return nil
	case *ast.ObjectPattern, *ast.ArrayPattern:
		// 解构赋值：({a, b} = x) / [a, b] = x。栈顶值为右值，
		// storePattern 会按模式展开存入已有引用并保留右值。
		return c.storePattern(ref)
	}
	return fmt.Errorf("invalid assignment target %T", ref)
}

// storePattern 把栈顶值按模式解构并存储到已有引用（ES2015 解构赋值）。
// 与声明解构（compileBindPattern）不同：不声明新变量，标识符按
// local/upvalue/global 解析，成员表达式走属性存储；值经临时槽展开后
// 重新压回栈顶，保证赋值表达式的结果（右值）保留。

func (c *Compiler) compileCall(n *ast.CallExpr) error {
	// Detect spread in args to choose fast vs slow path.
	hasSpread := false
	for _, a := range n.Arguments {
		if _, ok := a.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}

	// 编译一个调用实参。参数本身是链表达式（含 ?.）时按链正常编译并计数；
	// 否则暂停链计数（参数内部的非链调用——如 ternary 分支里的 method call——
	// 的中间值会在 CALL_METHOD 时消费，不应计入外层链残留），再为参数结果
	// 计 1。修复 `m.get(a ? x.f() : y)?.v` 短路清理 POP 过多导致帧栈越界。
	compileCallArg := func(a ast.Expression) error {
		if hasOptionalAccess(a) {
			return c.compileExpr(a)
		}
		saved := c.optChainPushActive
		c.optChainPushActive = false
		err := c.compileExpr(a)
		c.optChainPushActive = saved
		if err == nil {
			c.optPushValue()
		}
		return err
	}

	// super(args) — construct the parent class with this = current slot 0.

	if _, ok := n.Callee.(*ast.SuperExpr); ok {
		c.emitSuperCtor()
		if !hasSpread {
			for _, a := range n.Arguments {
				if err := c.compileExpr(a); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpConstructThis, uint32(len(n.Arguments)))
			return nil
		}
		c.compileArgsArray(n.Arguments)
		c.emit(bytecode.OpConstructThisArgs, 0)
		return nil
	}

	// super.method(args) — call method from parent prototype with this = slot 0.
	if m, ok := n.Callee.(*ast.MemberExpr); ok {
		if _, isSuper := m.Object.(*ast.SuperExpr); isSuper {
			c.emitSuperProto()
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := c.compileExpr(a); err != nil {
						return err
					}
				}
				c.emit(bytecode.OpCallThis, uint32(len(n.Arguments)))
				return nil
			}
			c.compileArgsArray(n.Arguments)
			c.emit(bytecode.OpCallThisArgs, 0)
			return nil
		}
	}

	// === Optional chaining support ===
	// Determine if this CallExpr is the head of an optional chain.
	chainHead := hasOptionalAccess(n) && !c.inOptionalChain()
	if chainHead {
		c.beginOptionalChain()
	}

	// Optional call with MemberExpr callee: a.b?.() or a?.b?.()
	// Need to get the method, check nullish, then call with this=receiver.
	if n.Optional {
		if m, ok := n.Callee.(*ast.MemberExpr); ok && !m.Computed {
			// 1. Compile receiver (may contain inner optional nodes)
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
			// receiver 压入；m.Object 若是 member（a.b / o?.x?.y），链首已在
			// 最内层计数（member 级联 GET_PROP 净 0），不重复 +1。
			if !optionalExprTracksResult(m.Object) {
				c.optPushValue()
			}
			// 2. If m.Optional, check if receiver is nullish (short-circuit)
			if m.Optional {
				c.emitOptionalJump()
			}
			// 3. Store receiver in temp for `this` binding
			tempSlot := c.newSlot()
			c.emit(bytecode.OpStoreLocal, uint32(tempSlot))
			c.optChainDelta(-1) // receiver 弹入 temp
			// 4. Get method from receiver
			c.emit(bytecode.OpLoadLocal, uint32(tempSlot))
			c.optPushValue() // receiver 重压（短路残留含 this）
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
			// 5. Check if method is nullish (n.Optional short-circuit)
			c.emitOptionalJump()
			// 6. Call with this=receiver
			c.emit(bytecode.OpLoadLocal, uint32(tempSlot)) // stack: method receiver
			c.optPushValue()                               // receiver 重压（this 绑定）
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := compileCallArg(a); err != nil {
						return err
					}
				}
				c.emit(bytecode.OpCallWithThis, uint32(len(n.Arguments)))
				c.optChainDelta(-(len(n.Arguments) + 1))
		} else {
			c.compileArgsArray(n.Arguments)
			c.optPushValue()
			c.emit(bytecode.OpCallArgs, 0)
			// callee(+1) + 数组(+1)，CALL_ARGS 弹 callee+数组压结果：链计数
			// 净 +1（tracks=true，父层不再补 +1）。
			c.optChainDelta(-1)
		}
		if chainHead {
			c.endOptionalChain()
		}
		return nil
	}

	// Optional call with non-MemberExpr callee: a?.()
		if err := c.compileExpr(n.Callee); err != nil {
			return err
		}
		if !optionalExprTracksResult(n.Callee) {
			c.optPushValue() // callee 压入（已计数的子链不重复）
		}
		c.emitOptionalJump()
		if !hasSpread {
			for _, a := range n.Arguments {
				if err := compileCallArg(a); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpCall, uint32(len(n.Arguments)))
			c.optChainDelta(-len(n.Arguments))
		} else {
			c.compileArgsArray(n.Arguments)
			c.optPushValue()
			c.emit(bytecode.OpCallArgs, 0)
			c.optChainDelta(-1)
		}
		if chainHead {
			c.endOptionalChain()
		}
		return nil
	}

	// Method call: foo.bar(args) or foo?.bar(args) or foo[expr](args) — keep receiver as `this`.
	if m, ok := n.Callee.(*ast.MemberExpr); ok {
		// 计算成员调用 obj[expr](args)：编译 obj，dup，取 method，swap 使栈为
		// [method, obj(this), args]，用 OpCallWithThis 调用（保留 this 绑定）。
		if m.Computed {
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
			if !optionalExprTracksResult(m.Object) {
				c.optPushValue() // obj 压入（已计数的子链不重复）
			}
			if m.Optional {
				c.emitOptionalJump()
			}
			c.emit(bytecode.OpDup, 0) // [obj, obj]
			c.optPushValue()          // obj 副本
			if err := c.compileExpr(m.Property); err != nil {
				return err
			}
			c.optPushValue() // key 压入
			// [obj, obj, key]
			c.emit(bytecode.OpGetElem, 0) // [obj, method]
			c.optChainDelta(-1)           // 弹 obj 副本 + key，压 method
			c.emit(bytecode.OpSwap, 0)    // [method, obj(this)]
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := compileCallArg(a); err != nil {
						return err
					}
				}
				c.emit(bytecode.OpCallWithThis, uint32(len(n.Arguments)))
				c.optChainDelta(-(len(n.Arguments) + 1))
			} else {
				c.compileArgsArray(n.Arguments)
				c.optPushValue()
				c.emit(bytecode.OpCallWithThisArgs, 0)
				c.optChainDelta(-2)
			}
			if chainHead {
				c.endOptionalChain()
			}
			return nil
		}
		if err := c.compileExpr(m.Object); err != nil {
			return err
		}
		if !optionalExprTracksResult(m.Object) {
			c.optPushValue() // receiver 压入（已计数的子链不重复）
		}
		// If m.Optional, check if receiver is nullish (short-circuit)
		if m.Optional {
			c.emitOptionalJump()
		}
		if !hasSpread {
			// Fast path: args inline.
			for _, a := range n.Arguments {
				if err := compileCallArg(a); err != nil {
					return err
				}
			}
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			operand := uint32(len(n.Arguments))<<16 | uint32(nameIdx&0xFFFF)
			c.emit(bytecode.OpCallMethod, operand)
			c.optChainDelta(-len(n.Arguments))
		} else {
			// Slow path: build args array, then OpCallMethodArgs.
			c.compileArgsArray(n.Arguments)
			c.optPushValue()
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpCallMethodArgs, uint32(nameIdx))
			// receiver(+1) + 数组(+1)，CALL_METHOD_ARGS 弹 receiver+数组压结果：
			// 链计数净 +1（tracks=true，父层不再补 +1）。
			c.optChainDelta(-1)
		}
		if chainHead {
			c.endOptionalChain()
		}
		return nil
	}

	// Regular call: f(args)
	// I-2 内联展开：const 绑定的可内联函数调用（非 optional、非 spread）。
	if !n.Optional && !hasSpread {
		if id, ok := n.Callee.(*ast.Identifier); ok {
			if c.tryInlineCall(id, n.Arguments) {
				if chainHead {
					c.endOptionalChain()
				}
				return nil
			}
		}
	}
	if err := c.compileExpr(n.Callee); err != nil {
		return err
	}
	if !hasSpread {
		for _, a := range n.Arguments {
			if err := compileCallArg(a); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpCall, uint32(len(n.Arguments)))
		// compileCallArg 已为每个非链实参 +1；OpCall 弹 callee+args 压结果，
		// 链计数须同步 -args（否则 `f(a)?.x` 短路时残留多记 N 个，清理块
		// 多弹 N 槽，把局部槽当操作数弹掉——帧栈越界 panic）。
		c.optChainDelta(-len(n.Arguments))
	} else {
		c.compileArgsArray(n.Arguments)
		c.emit(bytecode.OpCallArgs, 0)
	}
	if chainHead {
		c.endOptionalChain()
	}
	return nil
}

// compileArgsArray emits code that builds an array of arguments on top of the
// stack, expanding any SpreadElement nodes. Used for spread-call slow paths.
func (c *Compiler) compileArgsArray(args []ast.Expression) {
	c.emit(bytecode.OpBuildArray, 0)
	for _, a := range args {
		if sp, ok := a.(*ast.SpreadElement); ok {
			_ = c.compileExpr(sp.Arg)
			c.emit(bytecode.OpArraySpread, 0)
		} else {
			_ = c.compileExpr(a)
			c.emit(bytecode.OpArrayPush, 0)
		}
	}
}

func (c *Compiler) compileNew(n *ast.NewExpr) error {
	hasSpread := false
	for _, a := range n.Arguments {
		if _, ok := a.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}
	if err := c.compileExpr(n.Callee); err != nil {
		return err
	}
	if !hasSpread {
		for _, a := range n.Arguments {
			if err := c.compileExpr(a); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpNew, uint32(len(n.Arguments)))
		return nil
	}
	c.compileArgsArray(n.Arguments)
	c.emit(bytecode.OpNewArgs, 0)
	return nil
}

func (c *Compiler) compileMember(n *ast.MemberExpr) error {
	// super.prop — look up on parent prototype.
	if _, isSuper := n.Object.(*ast.SuperExpr); isSuper {
		c.emitSuperProto()
		if n.Computed {
			if err := c.compileExpr(n.Property); err != nil {
				return err
			}
			c.emit(bytecode.OpGetElem, 0)
		} else {
			key := n.Property.(*ast.Identifier).Name
			nameIdx := c.cur().tmpl.AddStringConst(key)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
		}
		return nil
	}

	// Determine if this is the head of an optional chain. The head is the
	// outermost MemberExpr/CallExpr containing an optional access, compiled
	// from the top down. Nested nodes see inOptionalChain() == true.
	chainHead := hasOptionalAccess(n) && !c.inOptionalChain()
	if chainHead {
		c.beginOptionalChain()
	}

	// O2-D1 superinstruction：`localVar.prop`（非计算、非可选、对象为局部
	// 变量）合并为单条 OpGetPropLocal（slot<<16 | nameIdx），省 1 次
	// dispatch 与压栈/弹栈。slot 打包在高 8 位（OperandPackedSlotName），
	// 仅支持 0-255；超限（巨型函数局部槽 >255，如 @aws-sdk 生成代码）回退
	// 普通 LoadLocal+GetProp，避免高位截断读到错误槽位。
	if !n.Computed && !n.Optional && !chainHead {
		if id, ok := n.Object.(*ast.Identifier); ok {
			if kind, idx := c.resolve(id.Name); kind == "local" && idx <= 0xFF {
				nameIdx := c.cur().tmpl.AddStringConst(n.Property.(*ast.Identifier).Name)
				c.emit(bytecode.OpGetPropLocal, uint32(idx)<<16|uint32(nameIdx&0xFFFF))
				return nil
			}
		}
	}

	if err := c.compileExpr(n.Object); err != nil {
		return err
	}
	// 链值压入（对象）：member 以及成员/可选 call 已在子表达式中维护链栈
	// 计数，不重复 +1。否则 `map.get(k)?.prop` 会把调用结果计两次，短路
	// 清理块多发一个 POP，把临时 local 槽误当操作数弹掉。
	if !optionalExprTracksResult(n.Object) {
		c.optPushValue()
	}

	// If this node is optional, emit short-circuit jump after evaluating
	// the object: if nullish, pop + push undefined + jump to chain end.
	if n.Optional {
		c.emitOptionalJump()
	}

	if n.Computed {
		if err := c.compileExpr(n.Property); err != nil {
			return err
		}
		c.optPushValue() // key 压入
		c.emit(bytecode.OpGetElem, 0)
		c.optChainDelta(-1) // 弹对象 + key，压结果
	} else {
		key := n.Property.(*ast.Identifier).Name
		nameIdx := c.cur().tmpl.AddStringConst(key)
		c.emit(bytecode.OpGetProp, uint32(nameIdx))
	}

	if chainHead {
		c.endOptionalChain()
	}
	return nil
}

// optionalExprTracksResult reports whether a child expression already accounts
// for its result in the active optional-chain stack counter.
func optionalExprTracksResult(expr ast.Expression) bool {
	switch n := expr.(type) {
	case *ast.MemberExpr:
		return true
	case *ast.CallExpr:
		if n.Optional {
			return true
		}
		_, isMethodCall := n.Callee.(*ast.MemberExpr)
		return isMethodCall
	case *ast.AwaitExpr:
		return optionalExprTracksResult(n.Argument)
	default:
		return false
	}
}

func (c *Compiler) compileConditional(n *ast.ConditionalExpr) error {
	if err := c.compileExpr(n.Test); err != nil {
		return err
	}
	jFalse := c.emit(bytecode.OpJmpFalsePop, 0)
	if err := c.compileExpr(n.Consequent); err != nil {
		return err
	}
	jEnd := c.emit(bytecode.OpJmp, 0)
	c.patchJumpToHere(jFalse)
	if err := c.compileExpr(n.Alternate); err != nil {
		return err
	}
	c.patchJumpToHere(jEnd)
	return nil
}

// === function compilation =================================================

// compileFunction compiles a function (decl, expr, or arrow) into a
// FuncTemplate and emits OpMakeClosure in the enclosing function.
// `defaults[i]` is the default expression for params[i] (nil = no default).
// `rest` is the ES2015 rest parameter name (or "" if none).
// `isArrow` 为 true 时编译箭头函数：不声明本函数级 `this` 槽位，
// `this` 经 upvalue 链解析为外层函数的 `this`（P0-2）。

func hasOptionalAccess(expr ast.Expression) bool {
	switch n := expr.(type) {
	case *ast.MemberExpr:
		return n.Optional || hasOptionalAccess(n.Object)
	case *ast.CallExpr:
		return n.Optional || hasOptionalAccess(n.Callee)
	}
	return false
}

// beginOptionalChain starts a new optional chain context.
func (c *Compiler) beginOptionalChain() {
	c.optionalChainStack = append(c.optionalChainStack, nil)
	c.optionalChainResiduals = append(c.optionalChainResiduals, nil)
	// 嵌套链继承外层计数（内层短路残留包含外层未消费值，如 f?.(x?.y)
	// 中内层链短路时栈上还有外层 callee f）。
	c.optChainPushSaved = append(c.optChainPushSaved, c.optChainPushCount)
	c.optChainPushActive = true
}

// optChainDelta 维护当前链的栈上链值数（链内指令净栈效应，非链时 no-op）。
func (c *Compiler) optChainDelta(d int) {
	if c.optChainPushActive && len(c.optionalChainStack) > 0 {
		c.optChainPushCount += d
	}
}

// optPushValue 记录链内又压入一个值（对象/callee/参数/属性 key 等子表达式）。
func (c *Compiler) optPushValue() { c.optChainDelta(1) }

// emitOptionalJump emits an OpOptionalJump and records it in the current chain.
// 短路时 VM 弹栈顶压 undefined 后跳链尾；链内残留（本次短路跳过的中间值）
// 由 endOptionalChain 生成的清理块弹出（operand 只有 24 位，残留数无法编码）。
func (c *Compiler) emitOptionalJump() {
	pc := c.emit(bytecode.OpOptionalJump, 0)
	chain := &c.optionalChainStack[len(c.optionalChainStack)-1]
	*chain = append(*chain, pc)
	residual := c.optChainPushCount - 1
	if residual < 0 {
		residual = 0
	}
	res := &c.optionalChainResiduals[len(c.optionalChainResiduals)-1]
	*res = append(*res, residual)
}

// endOptionalChain 在链尾生成短路清理块：每个 OpOptionalJump 短路时栈上
// 残留 r 个链内中间值（VM 只弹栈顶压 undefined），清理块 POP × r 后跳汇聚
// 点，保证短路路径与正常路径在链尾后栈深一致（否则残留污染后续栈）。
func (c *Compiler) endOptionalChain() {
	idx := len(c.optionalChainStack) - 1
	jumps := c.optionalChainStack[idx]
	residuals := c.optionalChainResiduals[idx]
	// 正常路径（链尾）：跳汇聚点。
	tailJmp := c.emit(bytecode.OpJmp, 0)
	// 各级短路清理块：短路后栈为 [残留..., 结果]（VM 弹链尾值压 undefined，
	// 结果在栈顶）——先经 temp slot 暂存结果，POP × residual 弹掉残留，再
	// 压回结果，保证汇聚点栈深与正常路径一致（否则残留污染后续栈、结果
	// 丢失导致短路路径条件错误）。
	cleanupJmps := make([]int, len(jumps))
	var tempSlot uint32
	haveTemp := false
	for i := range jumps {
		if residuals[i] == 0 {
			// 无残留：短路后栈即 [结果]，无需清理。
			cleanup := c.curPC()
			cleanupJmps[i] = c.emit(bytecode.OpJmp, 0)
			c.patchOptionalJumpToHere(jumps[i], cleanup)
			continue
		}
		if !haveTemp {
			tempSlot = uint32(c.newSlot())
			haveTemp = true
		}
		cleanup := c.curPC()
		c.emit(bytecode.OpStoreLocal, tempSlot) // 弹结果暂存
		for j := 0; j < residuals[i]; j++ {
			c.emit(bytecode.OpPop, 0) // 弹残留
		}
		c.emit(bytecode.OpLoadLocal, tempSlot) // 压回结果
		cleanupJmps[i] = c.emit(bytecode.OpJmp, 0)
		c.patchOptionalJumpToHere(jumps[i], cleanup)
	}
	// 汇聚点：patch 正常路径与各清理块的 JMP。
	c.patchJumpToHere(tailJmp)
	for _, jmp := range cleanupJmps {
		c.patchJumpToHere(jmp)
	}
	c.optionalChainStack = c.optionalChainStack[:idx]
	c.optionalChainResiduals = c.optionalChainResiduals[:idx]
	// 恢复外层计数：内层链结果也在栈上压入 1 个值。
	outer := c.optChainPushSaved[idx]
	c.optChainPushSaved = c.optChainPushSaved[:idx]
	c.optChainPushCount = outer + 1
	if len(c.optionalChainStack) == 0 {
		c.optChainPushActive = false
		c.optChainPushCount = 0
	}
}

// patchOptionalJumpToHere 将 OpOptionalJump 的偏移 patch 到目标 PC。
func (c *Compiler) patchOptionalJumpToHere(pc, target int) {
	delta := target - (pc + bytecode.InstrSize)
	bytecode.PatchOperand(c.cur().tmpl.Code, pc, uint32(delta))
}

// inOptionalChain returns true if there's an active optional chain context.
func (c *Compiler) inOptionalChain() bool {
	return len(c.optionalChainStack) > 0
}

// propKey returns the property name for a literal key.
