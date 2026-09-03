// AST 解释器语句执行：块/声明/分支/循环/try/switch 与控制流信号。

package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// controlFlow carries return/break/continue signals up the call stack.
type controlFlow struct {
	kind  string // "return", "break", "continue"
	value engine.Value
	label string
}

func (e *controlFlow) Error() string { return e.kind }

func isControlFlow(err error) (*controlFlow, bool) {
	cf, ok := err.(*controlFlow)
	return cf, ok
}

// execStmt executes a statement, returning the expression result (or nil).
func (interp *Interpreter) execStmt(stmt ast.Statement, scope *Scope) (engine.Value, error) {
	switch s := stmt.(type) {
	case *ast.VarDecl:
		return interp.execVarDecl(s, scope)
	case *ast.FunctionDecl:
		// Already hoisted; just ensure it's in scope
		if s.Name != nil {
			if !scope.HasOwn(s.Name.Name) {
				c := newClosure(interp, scope, s.Params, s.Defaults, s.RestParam, s.Body, s.Name.Name, false)
				scope.Declare(s.Name.Name, c)
			}
		}
		return nil, nil
	case *ast.BlockStmt:
		return interp.execBlock(s, scope.NewChild())
	case *ast.ExprStmt:
		return interp.evalExpr(s.Expr, scope)
	case *ast.EmptyStmt:
		return nil, nil
	case *ast.IfStmt:
		return interp.execIf(s, scope)
	case *ast.WhileStmt:
		return interp.execWhile(s, scope)
	case *ast.DoWhileStmt:
		return interp.execDoWhile(s, scope)
	case *ast.ForStmt:
		return interp.execFor(s, scope)
	case *ast.ForInStmt:
		return interp.execForIn(s, scope)
	case *ast.ForOfStmt:
		return interp.execForOf(s, scope)
	case *ast.ReturnStmt:
		var val engine.Value = engine.Undefined()
		if s.Arg != nil {
			v, err := interp.evalExpr(s.Arg, scope)
			if err != nil {
				return nil, err
			}
			val = v
		}
		return nil, &controlFlow{kind: "return", value: val}
	case *ast.BreakStmt:
		return nil, &controlFlow{kind: "break", label: s.Label}
	case *ast.ContinueStmt:
		return nil, &controlFlow{kind: "continue", label: s.Label}
	case *ast.ThrowStmt:
		v, err := interp.evalExpr(s.Arg, scope)
		if err != nil {
			return nil, err
		}
		return nil, &jsError{value: v}
	case *ast.TryStmt:
		return interp.execTry(s, scope)
	case *ast.SwitchStmt:
		return interp.execSwitch(s, scope)
	case *ast.LabeledStmt:
		// Execute body; break with matching label exits the statement
		_, err := interp.execStmt(s.Body, scope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok && cf.kind == "break" && cf.label == s.Label {
				return nil, nil
			}
		}
		return nil, err
	}
	return nil, fmt.Errorf("%w: unexpected statement type %T", engine.ErrNotImplemented, stmt)
}

func (interp *Interpreter) execBlock(block *ast.BlockStmt, scope *Scope) (engine.Value, error) {
	interp.hoist(block.Body, scope)
	var result engine.Value
	for _, stmt := range block.Body {
		val, err := interp.execStmt(stmt, scope)
		if err != nil {
			return nil, err
		}
		if val != nil {
			result = val
		}
	}
	return result, nil
}

func (interp *Interpreter) execVarDecl(d *ast.VarDecl, scope *Scope) (engine.Value, error) {
	for _, decl := range d.Decls {
		var val engine.Value = engine.Undefined()
		if decl.Init != nil {
			v, err := interp.evalExpr(decl.Init, scope)
			if err != nil {
				return nil, err
			}
			val = v
		}
		if d.Kind == "var" {
			// var is function-scoped; walk up to find existing binding
			if !scope.Set(decl.Name.Name, val) {
				scope.Declare(decl.Name.Name, val)
			}
		} else {
			// let/const are block-scoped
			scope.Declare(decl.Name.Name, val)
		}
	}
	return nil, nil
}

func (interp *Interpreter) execIf(s *ast.IfStmt, scope *Scope) (engine.Value, error) {
	test, err := interp.evalExpr(s.Test, scope)
	if err != nil {
		return nil, err
	}
	cond, _ := test.Bool()
	if cond {
		return interp.execStmt(s.Consequent, scope)
	}
	if s.Alternate != nil {
		return interp.execStmt(s.Alternate, scope)
	}
	return nil, nil
}

func (interp *Interpreter) execWhile(s *ast.WhileStmt, scope *Scope) (engine.Value, error) {
	for {
		test, err := interp.evalExpr(s.Test, scope)
		if err != nil {
			return nil, err
		}
		cond, _ := test.Bool()
		if !cond {
			break
		}
		_, err = interp.execStmt(s.Body, scope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					continue
				}
				return nil, err
			}
			return nil, err
		}
	}
	return nil, nil
}

func (interp *Interpreter) execDoWhile(s *ast.DoWhileStmt, scope *Scope) (engine.Value, error) {
	for {
		_, err := interp.execStmt(s.Body, scope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					// fall through to test
				} else {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
		test, err := interp.evalExpr(s.Test, scope)
		if err != nil {
			return nil, err
		}
		cond, _ := test.Bool()
		if !cond {
			break
		}
	}
	return nil, nil
}

func (interp *Interpreter) execFor(s *ast.ForStmt, scope *Scope) (engine.Value, error) {
	loopScope := scope.NewChild()
	if s.Init != nil {
		switch init := s.Init.(type) {
		case *ast.VarDecl:
			if _, err := interp.execVarDecl(init, loopScope); err != nil {
				return nil, err
			}
		case ast.Expression:
			if _, err := interp.evalExpr(init, loopScope); err != nil {
				return nil, err
			}
		}
	}
	for {
		if s.Test != nil {
			test, err := interp.evalExpr(s.Test, loopScope)
			if err != nil {
				return nil, err
			}
			cond, _ := test.Bool()
			if !cond {
				break
			}
		}
		_, err := interp.execStmt(s.Body, loopScope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					// fall through to update
				} else {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
		if s.Update != nil {
			if _, err := interp.evalExpr(s.Update, loopScope); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (interp *Interpreter) execForIn(s *ast.ForInStmt, scope *Scope) (engine.Value, error) {
	right, err := interp.evalExpr(s.Right, scope)
	if err != nil {
		return nil, err
	}
	// EnumerateObjectProperties：原型链可枚举键（与 VM 的 OpEnumKeys 同一 helper）。
	for _, key := range engine.EnumerateForInKeys(right) {
		loopScope := scope.NewChild()
		interp.assignForLeft(s.Left, engine.Str(key), loopScope)
		_, err := interp.execStmt(s.Body, loopScope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					continue
				}
				return nil, err
			}
			return nil, err
		}
	}
	return nil, nil
}

func (interp *Interpreter) execForOf(s *ast.ForOfStmt, scope *Scope) (engine.Value, error) {
	right, err := interp.evalExpr(s.Right, scope)
	if err != nil {
		return nil, err
	}
	// Support arrays and strings
	switch v := right.(type) {
	case *engine.ArrayValue:
		for _, elem := range v.Elems() {
			loopScope := scope.NewChild()
			interp.assignForLeft(s.Left, elem, loopScope)
			_, err := interp.execStmt(s.Body, loopScope)
			if err != nil {
				if cf, ok := isControlFlow(err); ok {
					if cf.kind == "break" {
						break
					}
					if cf.kind == "continue" {
						continue
					}
					return nil, err
				}
				return nil, err
			}
		}
	default:
		if right.Type() == engine.TypeString {
			str := right.String()
			for _, ch := range str {
				loopScope := scope.NewChild()
				interp.assignForLeft(s.Left, engine.Str(string(ch)), loopScope)
				_, err := interp.execStmt(s.Body, loopScope)
				if err != nil {
					if cf, ok := isControlFlow(err); ok {
						if cf.kind == "break" {
							break
						}
						if cf.kind == "continue" {
							continue
						}
						return nil, err
					}
					return nil, err
				}
			}
		}
	}
	return nil, nil
}

func (interp *Interpreter) assignForLeft(left ast.Node, val engine.Value, scope *Scope) {
	switch l := left.(type) {
	case *ast.VarDecl:
		if len(l.Decls) > 0 {
			scope.Declare(l.Decls[0].Name.Name, val)
		}
	case *ast.Identifier:
		scope.Declare(l.Name, val)
	}
}

func (interp *Interpreter) execTry(s *ast.TryStmt, scope *Scope) (engine.Value, error) {
	result, err := interp.execBlock(s.Block, scope.NewChild())
	if err != nil {
		// Skip control flow errors (return/break/continue)
		if _, isCF := isControlFlow(err); isCF {
			if s.Finally != nil {
				_, ferr := interp.execBlock(s.Finally, scope.NewChild())
				if ferr != nil {
					return nil, ferr
				}
			}
			return nil, err
		}
		// Convert to JS error value if not already
		var jsErrVal engine.Value
		if je, ok := err.(*jsError); ok {
			jsErrVal = je.value
		} else {
			jsErrVal = interp.goErrorToJSValue(err)
		}
		// If there's a handler, catch the error
		if s.Handler != nil {
			catchScope := scope.NewChild()
			if s.Handler.Param != nil {
				catchScope.Declare(s.Handler.Param.Name, jsErrVal)
			}
			hresult, herr := interp.execBlock(s.Handler.Body, catchScope)
			if s.Finally != nil {
				_, ferr := interp.execBlock(s.Finally, scope.NewChild())
				if ferr != nil {
					return nil, ferr
				}
			}
			return hresult, herr
		}
		// No handler: run finally and re-throw
		if s.Finally != nil {
			_, ferr := interp.execBlock(s.Finally, scope.NewChild())
			if ferr != nil {
				return nil, ferr
			}
		}
		return nil, &jsError{value: jsErrVal}
	}
	if s.Finally != nil {
		_, ferr := interp.execBlock(s.Finally, scope.NewChild())
		if ferr != nil {
			return nil, ferr
		}
	}
	return result, nil
}

func (interp *Interpreter) execSwitch(s *ast.SwitchStmt, scope *Scope) (engine.Value, error) {
	disc, err := interp.evalExpr(s.Disc, scope)
	if err != nil {
		return nil, err
	}
	matched := false
	for i, c := range s.Cases {
		if !matched {
			if c.Test == nil {
				// default: only match if no case matched and we're past all cases
				// (simplified: match default only if no prior case matched)
				// Check if any later case matches
				anyMatch := false
				for j := i + 1; j < len(s.Cases); j++ {
					if s.Cases[j].Test != nil {
						tv, err := interp.evalExpr(s.Cases[j].Test, scope)
						if err != nil {
							return nil, err
						}
						if strictEqual(disc, tv) {
							anyMatch = true
							break
						}
					}
				}
				if anyMatch {
					continue
				}
				matched = true
			} else {
				tv, err := interp.evalExpr(c.Test, scope)
				if err != nil {
					return nil, err
				}
				if strictEqual(disc, tv) {
					matched = true
				}
			}
		}
		if matched {
			for _, stmt := range c.Consequent {
				_, err := interp.execStmt(stmt, scope)
				if err != nil {
					if _, ok := isControlFlow(err); ok {
						return nil, err
					}
					return nil, err
				}
			}
		}
	}
	return nil, nil
}
