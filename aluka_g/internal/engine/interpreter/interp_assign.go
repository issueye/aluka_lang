// AST 解释器赋值与解构：赋值目标解析、引用写入与解构模式绑定。

package interpreter

import (
	"fmt"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
)

func (interp *Interpreter) evalAssign(e *ast.AssignExpr, scope *Scope) (engine.Value, error) {
	right, err := interp.evalExpr(e.Right, scope)
	if err != nil {
		return nil, err
	}
	if e.Op == "=" {
		if err := interp.assignToRef(e.Left, right, scope); err != nil {
			return nil, err
		}
		return right, nil
	}
	// Compound assignment: += -= etc.
	leftExpr, ok := e.Left.(ast.Expression)
	if !ok {
		// 解构模式不能作为复合赋值左值（JS 语法错误）。
		return nil, fmt.Errorf("%w: invalid compound assignment target %T", engine.ErrSyntaxError, e.Left)
	}
	cur, err := interp.evalExpr(leftExpr, scope)
	if err != nil {
		return nil, err
	}
	baseOp := e.Op[:len(e.Op)-1] // strip trailing '='
	result := applyBinaryOp(baseOp, cur, right)
	if err := interp.assignToRef(e.Left, result, scope); err != nil {
		return nil, err
	}
	return result, nil
}

func (interp *Interpreter) assignToRef(target ast.Node, val engine.Value, scope *Scope) error {
	switch t := target.(type) {
	case *ast.Identifier:
		if !scope.Set(t.Name, val) {
			// Implicit global
			_ = interp.globalObj.Set(t.Name, val)
		}
		return nil
	case *ast.MemberExpr:
		obj, err := interp.evalExpr(t.Object, scope)
		if err != nil {
			return err
		}
		var key string
		if t.Computed {
			kv, err := interp.evalExpr(t.Property, scope)
			if err != nil {
				return err
			}
			key = kv.String()
		} else if id, ok := t.Property.(*ast.Identifier); ok {
			key = id.Name
		}
		if o, ok := obj.AsObject(); ok {
			return o.Set(key, val)
		}
		return fmt.Errorf("%w: cannot set property on %s", engine.ErrTypeError, obj.Type())
	case *ast.ObjectPattern, *ast.ArrayPattern:
		return interp.storePatternValue(t, val, scope)
	}
	return fmt.Errorf("%w: invalid assignment target %T", engine.ErrSyntaxError, target)
}

// storePatternValue 按解构模式把 val 存入已有引用（ES2015 解构赋值）。
// 与声明解构（bindPattern）不同：不声明新变量，标识符经 scope.Set 解析
// （未声明时落入隐式全局），成员表达式走属性存储。
func (interp *Interpreter) storePatternValue(target ast.Node, val engine.Value, scope *Scope) error {
	switch pat := target.(type) {
	case *ast.Identifier:
		return interp.assignToRef(pat, val, scope)
	case *ast.MemberExpr:
		return interp.assignToRef(pat, val, scope)
	case *ast.ArrayPattern:
		for i, el := range pat.Elements {
			if el.Target == nil {
				continue // 空洞
			}
			var v engine.Value
			if el.IsRest {
				if arr, ok := val.(*engine.ArrayValue); ok {
					rest := arr.Elems()
					if i < len(rest) {
						rest = rest[i:]
					} else {
						rest = []engine.Value{}
					}
					v = engine.NewArray(rest)
				} else if o, ok := val.AsObject(); ok {
					v = engine.NewArray([]engine.Value{})
					_ = o // 非数组对象按空数组处理（保守）
				} else {
					v = engine.Undefined()
				}
			} else {
				key := strconv.Itoa(i)
				if o, ok := val.AsObject(); ok {
					tmp, err := o.Get(key)
					if err != nil {
						return err
					}
					v = tmp
				} else {
					v = engine.Undefined()
				}
			}
			if el.Default != nil && v.IsUndefined() {
				dv, err := interp.evalExpr(el.Default, scope)
				if err != nil {
					return err
				}
				v = dv
			}
			if err := interp.storePatternValue(el.Target, v, scope); err != nil {
				return err
			}
		}
		return nil
	case *ast.ObjectPattern:
		o, ok := val.AsObject()
		if !ok {
			return fmt.Errorf("%w: cannot destructure %s", engine.ErrTypeError, val.Type())
		}
		boundKeys := make(map[string]bool)
		for propIndex, prop := range pat.Properties {
			if prop.Value == nil {
				return fmt.Errorf("%w: invalid assignment target in destructuring", engine.ErrSyntaxError)
			}
			if prop.IsRest {
				// rest: 复制除已绑定键外的所有键
				rest := engine.NewObject()
				for _, k := range o.Keys() {
					if boundKeys[k] {
						continue
					}
					kv, err := o.Get(k)
					if err != nil {
						return err
					}
					if err := rest.Set(k, kv); err != nil {
						return err
					}
				}
				if err := interp.storePatternValue(prop.Value, rest, scope); err != nil {
					return err
				}
				_ = propIndex
				continue
			}
			var key string
			if prop.Computed {
				kv, err := interp.evalExpr(prop.Key, scope)
				if err != nil {
					return err
				}
				key = kv.String()
			} else if id, ok := prop.Key.(*ast.Identifier); ok {
				key = id.Name
			} else if s, ok := prop.Key.(*ast.StringLit); ok {
				key = s.Value
			}
			boundKeys[key] = true
			v, err := o.Get(key)
			if err != nil {
				return err
			}
			if prop.Default != nil && v.IsUndefined() {
				dv, err := interp.evalExpr(prop.Default, scope)
				if err != nil {
					return err
				}
				v = dv
			}
			if err := interp.storePatternValue(prop.Value, v, scope); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%w: invalid assignment target %T", engine.ErrSyntaxError, target)
}
