package compiler

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func (c *Compiler) declarePatternSlots(p ast.Pattern) {
	switch pat := p.(type) {
	case *ast.Identifier:
		c.declareLocal(pat.Name)
	case *ast.ArrayPattern:
		for _, el := range pat.Elements {
			if el.Target != nil {
				c.declarePatternSlots(el.Target)
			}
		}
	case *ast.ObjectPattern:
		for _, prop := range pat.Properties {
			if prop.Value != nil {
				c.declarePatternSlots(prop.Value)
			} else if id, ok := prop.Key.(*ast.Identifier); ok {
				c.declareLocal(id.Name)
			}
		}
	}
}

func (c *Compiler) compileBindPattern(p ast.Pattern, srcSlot int, kind string) error {
	switch pat := p.(type) {
	case *ast.Identifier:
		var slot int
		if kind == "var" {
			slot = c.declareVar(pat.Name)
		} else {
			slot = c.declareLocal(pat.Name)
		}
		c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
		c.emit(bytecode.OpStoreLocal, uint32(slot))

	case *ast.ArrayPattern:
		for i, el := range pat.Elements {
			if el.Target == nil {
				continue // hole
			}
			tmpSlot := c.newSlot()
			if el.IsRest {
				// rest: result = src.slice(i) — method call with this=src
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot)) // receiver
				c.emit(bytecode.OpPushInt, uint32(i))         // arg
				sliceNameIdx := c.cur().tmpl.AddStringConst("slice")
				operand := uint32(1)<<16 | uint32(sliceNameIdx&0xFFFF)
				c.emit(bytecode.OpCallMethod, operand)
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			} else {
				// element: result = src[i]
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
				c.emit(bytecode.OpPushInt, uint32(i))
				c.emit(bytecode.OpGetElem, 0)
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			}
			// Apply default if value is undefined.
			if el.Default != nil {
				c.emit(bytecode.OpLoadLocal, uint32(tmpSlot))
				c.emit(bytecode.OpPushUndefined, 0)
				c.emit(bytecode.OpStrictEq, 0)
				jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
				if err := c.compileExpr(el.Default); err != nil {
					return err
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.patchJumpToHere(jSkip)
			}
			if err := c.compileBindPattern(el.Target, tmpSlot, kind); err != nil {
				return err
			}
		}

	case *ast.ObjectPattern:
		for propIndex, prop := range pat.Properties {
			tmpSlot := c.newSlot()
			if prop.IsRest {
				// Object rest: create { ...src } then delete already-bound keys.
				c.emit(bytecode.OpNewObject, 0)
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
				c.emit(bytecode.OpSpreadObject, 0)
				// Delete each already-bound key from the rest object.
				for _, bound := range pat.Properties[:propIndex] {
					c.emit(bytecode.OpDup, 0) // dup rest obj
					if bound.Computed {
						if err := c.compileExpr(bound.Key); err != nil {
							return err
						}
						c.emit(bytecode.OpDelElem, 0)
					} else {
						nameIdx := c.cur().tmpl.AddStringConst(propKey(bound.Key))
						c.emit(bytecode.OpDelProp, uint32(nameIdx))
					}
					c.emit(bytecode.OpPop, 0) // discard bool
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.compileBindPattern(prop.Value, tmpSlot, kind)
				continue
			}
			// result = src[key]
			c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
			if prop.Computed {
				if err := c.compileExpr(prop.Key); err != nil {
					return err
				}
				c.emit(bytecode.OpGetElem, 0)
			} else {
				key := propKey(prop.Key)
				nameIdx := c.cur().tmpl.AddStringConst(key)
				c.emit(bytecode.OpGetProp, uint32(nameIdx))
			}
			c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			// Apply default if value is undefined.
			if prop.Default != nil {
				c.emit(bytecode.OpLoadLocal, uint32(tmpSlot))
				c.emit(bytecode.OpPushUndefined, 0)
				c.emit(bytecode.OpStrictEq, 0)
				jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
				if err := c.compileExpr(prop.Default); err != nil {
					return err
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.patchJumpToHere(jSkip)
			}
			if err := c.compileBindPattern(prop.Value, tmpSlot, kind); err != nil {
				return err
			}
		}
	}
	return nil
}


func (c *Compiler) storePattern(p ast.Node) error {
	switch pat := p.(type) {
	case *ast.Identifier:
		return c.assignTo(pat)
	case *ast.MemberExpr:
		return c.assignTo(pat)
	case *ast.ArrayPattern:
		tmp := c.newSlot()
		c.emit(bytecode.OpStoreLocal, uint32(tmp)) // 右值入临时槽
		for i, el := range pat.Elements {
			if el.Target == nil {
				continue // 空洞
			}
			if el.IsRest {
				// rest: src.slice(i)
				c.emit(bytecode.OpLoadLocal, uint32(tmp))
				c.emit(bytecode.OpPushInt, uint32(i))
				sliceNameIdx := c.cur().tmpl.AddStringConst("slice")
				operand := uint32(1)<<16 | uint32(sliceNameIdx&0xFFFF)
				c.emit(bytecode.OpCallMethod, operand)
			} else {
				c.emit(bytecode.OpLoadLocal, uint32(tmp))
				c.emit(bytecode.OpPushInt, uint32(i))
				c.emit(bytecode.OpGetElem, 0)
			}
			if err := c.compileDefaultGuard(el.Default); err != nil {
				return err
			}
			if err := c.storePattern(el.Target); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpLoadLocal, uint32(tmp))
		return nil
	case *ast.ObjectPattern:
		tmp := c.newSlot()
		c.emit(bytecode.OpStoreLocal, uint32(tmp)) // 右值入临时槽
		for propIndex, prop := range pat.Properties {
			if prop.Value == nil {
				return fmt.Errorf("invalid assignment target in destructuring")
			}
			if prop.IsRest {
				// rest: { ...src } 再删除已绑定键
				c.emit(bytecode.OpNewObject, 0)
				c.emit(bytecode.OpLoadLocal, uint32(tmp))
				c.emit(bytecode.OpSpreadObject, 0)
				for _, bound := range pat.Properties[:propIndex] {
					c.emit(bytecode.OpDup, 0)
					if bound.Computed {
						if err := c.compileExpr(bound.Key); err != nil {
							return err
						}
						c.emit(bytecode.OpDelElem, 0)
					} else {
						nameIdx := c.cur().tmpl.AddStringConst(propKey(bound.Key))
						c.emit(bytecode.OpDelProp, uint32(nameIdx))
					}
					c.emit(bytecode.OpPop, 0)
				}
				if err := c.storePattern(prop.Value); err != nil {
					return err
				}
				continue
			}
			// result = src[key]
			c.emit(bytecode.OpLoadLocal, uint32(tmp))
			if prop.Computed {
				if err := c.compileExpr(prop.Key); err != nil {
					return err
				}
				c.emit(bytecode.OpGetElem, 0)
			} else {
				nameIdx := c.cur().tmpl.AddStringConst(propKey(prop.Key))
				c.emit(bytecode.OpGetProp, uint32(nameIdx))
			}
			if err := c.compileDefaultGuard(prop.Default); err != nil {
				return err
			}
			if err := c.storePattern(prop.Value); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpLoadLocal, uint32(tmp))
		return nil
	}
	return fmt.Errorf("invalid assignment target %T", p)
}

// compileDefaultGuard 编译解构默认值守卫：栈顶值 === undefined 时用
// 默认值替换，否则保持栈顶原值。defaultExpr 为 nil 时无操作。
// 栈序：[v] → (undefined 分支) pop v → push default；
// (非 undefined 分支) 跳过后仍为 [v]。
func (c *Compiler) compileDefaultGuard(defaultExpr ast.Expression) error {
	if defaultExpr == nil {
		return nil
	}
	c.emit(bytecode.OpDup, 0)
	c.emit(bytecode.OpPushUndefined, 0)
	c.emit(bytecode.OpStrictEq, 0)
	jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
	// 走到这里说明 v === undefined：丢弃 v，压入默认值。
	c.emit(bytecode.OpPop, 0)
	if err := c.compileExpr(defaultExpr); err != nil {
		return err
	}
	c.patchJumpToHere(jSkip)
	return nil
}

