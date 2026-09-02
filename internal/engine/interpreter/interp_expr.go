// AST 解释器表达式求值：字面量/成员/调用/new/一元/二元/逻辑/条件/序列。

package interpreter

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
)

func (interp *Interpreter) evalExpr(expr ast.Expression, scope *Scope) (engine.Value, error) {
	switch e := expr.(type) {
	case *ast.NumberLit:
		return engine.Number(e.Value), nil
	case *ast.StringLit:
		return engine.Str(e.Value), nil
	case *ast.BoolLit:
		return engine.Boolean(e.Value), nil
	case *ast.NullLit:
		return engine.Null(), nil
	case *ast.UndefinedLit:
		return engine.Undefined(), nil
	case *ast.TemplateLit:
		// Concatenate quasis with interpolated expression values.
		var b strings.Builder
		b.WriteString(e.Quasis[0])
		for i, expr := range e.Expressions {
			val, err := interp.evalExpr(expr, scope)
			if err != nil {
				return nil, err
			}
			b.WriteString(val.String())
			b.WriteString(e.Quasis[i+1])
		}
		return engine.Str(b.String()), nil
	case *ast.TaggedTemplateExpr:
		// tag`a${x}b`：构造 TemplateStringsArray（含 .raw），以 undefined 为 this 调用 tag。
		tag, err := interp.evalExpr(e.Tag, scope)
		if err != nil {
			return nil, err
		}
		quasis := make([]engine.Value, 0, len(e.Template.Quasis))
		for _, q := range e.Template.Quasis {
			quasis = append(quasis, engine.Str(q))
		}
		strs := engine.NewArray(quasis)
		rawQuasis := make([]engine.Value, 0, len(e.Template.RawQuasis))
		for _, rq := range e.Template.RawQuasis {
			rawQuasis = append(rawQuasis, engine.Str(rq))
		}
		if err := strs.Set("raw", engine.NewArray(rawQuasis)); err != nil {
			return nil, err
		}
		args := []engine.Value{strs}
		for _, ex := range e.Template.Expressions {
			v, err := interp.evalExpr(ex, scope)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		callable, err := asCallable(tag)
		if err != nil {
			return nil, err
		}
		return callable.callWith(engine.Undefined(), args)
	case *ast.RegexLit:
		// 正则字面量：构造真实 RegExp 实例（Go regexp 翻译层内核）。
		return interp.makeRegexp(e.Pattern, e.Flags)
	case *ast.Identifier:
		if v, ok := scope.Get(e.Name); ok {
			return v, nil
		}
		// Check global object
		if v, err := interp.globalObj.Get(e.Name); err == nil && !v.IsUndefined() {
			return v, nil
		}
		// Get 对缺失属性也返回 Undefined：globalObj 上显式存在但值为
		// undefined 的属性（如全局 undefined 本身）需与"缺失"区分。
		if interp.globalHas(e.Name) {
			return engine.Undefined(), nil
		}
		return nil, fmt.Errorf("%w: %s is not defined", engine.ErrReferenceError, e.Name)
	case *ast.ThisExpr:
		if v, ok := scope.Get("__this__"); ok {
			return v, nil
		}
		return engine.Undefined(), nil
	case *ast.ArrayLit:
		return interp.evalArrayLit(e, scope)
	case *ast.ObjectLit:
		return interp.evalObjectLit(e, scope)
	case *ast.MemberExpr:
		return interp.evalMember(e, scope)
	case *ast.CallExpr:
		return interp.evalCall(e, scope)
	case *ast.NewExpr:
		return interp.evalNew(e, scope)
	case *ast.UnaryExpr:
		return interp.evalUnary(e, scope)
	case *ast.UpdateExpr:
		return interp.evalUpdate(e, scope)
	case *ast.BinaryExpr:
		return interp.evalBinary(e, scope)
	case *ast.LogicalExpr:
		return interp.evalLogical(e, scope)
	case *ast.AssignExpr:
		return interp.evalAssign(e, scope)
	case *ast.ConditionalExpr:
		return interp.evalConditional(e, scope)
	case *ast.SequenceExpr:
		return interp.evalSequence(e, scope)
	case *ast.FunctionExpr:
		c := newClosure(interp, scope, e.Params, e.Defaults, e.RestParam, e.Body, funcName(e.Name), false)
		return c, nil
	case *ast.ArrowFunc:
		c := newClosure(interp, scope, e.Params, e.Defaults, e.RestParam, e.Body, "", true)
		return c, nil
	case *ast.SuperExpr:
		return nil, fmt.Errorf("%w: super is not supported in Phase 1A", engine.ErrNotImplemented)
	case *ast.NewTargetExpr:
		return nil, fmt.Errorf("%w: new.target is not supported in Phase 1A", engine.ErrNotImplemented)
	}
	return nil, fmt.Errorf("%w: unexpected expression type %T", engine.ErrNotImplemented, expr)
}

func funcName(id *ast.Identifier) string {
	if id == nil {
		return ""
	}
	return id.Name
}

func (interp *Interpreter) evalArrayLit(e *ast.ArrayLit, scope *Scope) (engine.Value, error) {
	var elems []engine.Value
	for _, el := range e.Elements {
		if el == nil {
			elems = append(elems, engine.Undefined())
			continue
		}
		v, err := interp.evalExpr(el, scope)
		if err != nil {
			return nil, err
		}
		elems = append(elems, v)
	}
	arr := engine.NewArray(elems)
	engine.SetProto(arr, interp.arrayProto)
	return arr, nil
}

func (interp *Interpreter) evalObjectLit(e *ast.ObjectLit, scope *Scope) (engine.Value, error) {
	obj := engine.NewObject()
	engine.SetProto(obj, interp.objectProto)
	for _, prop := range e.Properties {
		if prop.Kind == ast.PropertySpread {
			// Spread: copy enumerable own properties
			v, err := interp.evalExpr(prop.Value, scope)
			if err != nil {
				return nil, err
			}
			if src, ok := v.AsObject(); ok {
				for _, k := range src.Keys() {
					sv, _ := src.Get(k)
					_ = obj.Set(k, sv)
				}
			}
			continue
		}
		var key string
		if prop.Computed {
			kv, err := interp.evalExpr(prop.Key, scope)
			if err != nil {
				return nil, err
			}
			key = propertyKeyOf(kv)
		} else {
			switch k := prop.Key.(type) {
			case *ast.Identifier:
				key = k.Name
			case *ast.StringLit:
				key = k.Value
			case *ast.NumberLit:
				key = k.Raw
			default:
				return nil, fmt.Errorf("%w: invalid object key", engine.ErrSyntaxError)
			}
		}
		v, err := interp.evalExpr(prop.Value, scope)
		if err != nil {
			return nil, err
		}
		// get/set 访问器：注册为 accessor（而非普通属性）。
		if prop.Kind == ast.PropertyGet || prop.Kind == ast.PropertySet {
			engine.UpdateAccessor(obj, key, prop.Kind == ast.PropertyGet, v)
			continue
		}
		_ = obj.Set(key, v)
	}
	return obj, nil
}

func (interp *Interpreter) evalMember(e *ast.MemberExpr, scope *Scope) (engine.Value, error) {
	obj, err := interp.evalExpr(e.Object, scope)
	if err != nil {
		return nil, err
	}
	// Optional chaining: if the object is null/undefined, short-circuit to
	// undefined without attempting property access (no TypeError thrown).
	if e.Optional && (obj.IsNull() || obj.IsUndefined()) {
		return engine.Undefined(), nil
	}
	var key string
	if e.Computed {
		kv, err := interp.evalExpr(e.Property, scope)
		if err != nil {
			return nil, err
		}
		key = propertyKeyOf(kv)
	} else {
		if id, ok := e.Property.(*ast.Identifier); ok {
			key = id.Name
		} else {
			return nil, fmt.Errorf("%w: invalid member property", engine.ErrTypeError)
		}
	}
	return interp.getProperty(obj, key)
}

// getProperty retrieves a property from a value, handling primitives and prototype chain.
func (interp *Interpreter) getProperty(obj engine.Value, key string) (engine.Value, error) {
	// null and undefined throw TypeError on property access
	if obj.IsNull() || obj.IsUndefined() {
		return nil, fmt.Errorf("%w: Cannot read properties of %s (reading '%s')", engine.ErrTypeError, obj.String(), key)
	}
	if o, ok := obj.AsObject(); ok {
		return o.Get(key)
	}
	// Primitive string/number/boolean: look up on prototype
	switch obj.Type() {
	case engine.TypeString:
		if key == "length" {
			n, _ := engine.StringLen(obj)
			return engine.IntValue(n), nil
		}
		if n, err := strconv.Atoi(key); err == nil {
			if unit, ok := jsStringUnitAt(obj.String(), n); ok {
				return engine.Str(unit), nil
			}
			return engine.Undefined(), nil
		}
		if interp.stringProto != nil {
			return interp.stringProto.Get(key)
		}
	case engine.TypeNumber:
		if interp.numberProto != nil {
			return interp.numberProto.Get(key)
		}
	case engine.TypeBoolean:
		if interp.booleanProto != nil {
			return interp.booleanProto.Get(key)
		}
	case engine.TypeBigInt:
		if interp.bigintProto != nil {
			return interp.bigintProto.Get(key)
		}
	}
	return engine.Undefined(), nil
}

// globalHas 判断 name 是否为 globalObj 的自有属性。
// 用于区分"属性缺失"与"属性存在但值为 undefined"（Get 对两者都返回 Undefined）。
// 用 GetOwnSlot（不感知可枚举性）判断，globalThis/undefined/NaN/Infinity 收口为
// 不可枚举后，Keys() 会漏判这些全局名并误抛 ReferenceError。
func (interp *Interpreter) globalHas(name string) bool {
	_, ok := engine.GetOwnSlot(interp.globalObj, name)
	return ok
}

func (interp *Interpreter) evalCall(e *ast.CallExpr, scope *Scope) (engine.Value, error) {
	// Determine `this` and the function
	var thisVal engine.Value = engine.Undefined()
	var callee engine.Value
	var err error

	if member, ok := e.Callee.(*ast.MemberExpr); ok {
		receiver, err := interp.evalExpr(member.Object, scope)
		if err != nil {
			return nil, err
		}
		// Optional member access short-circuit: a?.b() / a?.b?.()
		if member.Optional && (receiver.IsNull() || receiver.IsUndefined()) {
			return engine.Undefined(), nil
		}
		var key string
		if member.Computed {
			kv, err := interp.evalExpr(member.Property, scope)
			if err != nil {
				return nil, err
			}
			key = kv.String()
		} else if id, ok := member.Property.(*ast.Identifier); ok {
			key = id.Name
		}
		fn, err := interp.getProperty(receiver, key)
		if err != nil {
			return nil, err
		}
		// Optional call short-circuit: a.b?.() — skip call if method is nullish
		if e.Optional && (fn.IsNull() || fn.IsUndefined()) {
			return engine.Undefined(), nil
		}
		callee = fn
		thisVal = receiver
	} else {
		callee, err = interp.evalExpr(e.Callee, scope)
		if err != nil {
			return nil, err
		}
		// Optional call short-circuit: f?.()
		if e.Optional && (callee.IsNull() || callee.IsUndefined()) {
			return engine.Undefined(), nil
		}
	}

	callable, err := asCallable(callee)
	if err != nil {
		return nil, err
	}

	args, err := interp.evalArgs(e.Arguments, scope)
	if err != nil {
		return nil, err
	}

	return callable.callWith(thisVal, args)
}

func (interp *Interpreter) evalNew(e *ast.NewExpr, scope *Scope) (engine.Value, error) {
	callee, err := interp.evalExpr(e.Callee, scope)
	if err != nil {
		return nil, err
	}
	callable, err := asCallable(callee)
	if err != nil {
		return nil, err
	}
	args, err := interp.evalArgs(e.Arguments, scope)
	if err != nil {
		return nil, err
	}
	return callable.construct(args)
}

func (interp *Interpreter) evalArgs(args []ast.Expression, scope *Scope) ([]engine.Value, error) {
	var result []engine.Value
	for _, arg := range args {
		if spread, ok := arg.(*ast.SpreadElement); ok {
			v, err := interp.evalExpr(spread.Arg, scope)
			if err != nil {
				return nil, err
			}
			if arr, ok := v.(*engine.ArrayValue); ok {
				result = append(result, arr.Elems()...)
			} else {
				result = append(result, v)
			}
			continue
		}
		v, err := interp.evalExpr(arg, scope)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

func (interp *Interpreter) evalUnary(e *ast.UnaryExpr, scope *Scope) (engine.Value, error) {
	if e.Op == "typeof" {
		// typeof doesn't throw on undefined identifiers
		if id, ok := e.Arg.(*ast.Identifier); ok {
			if v, ok := scope.Get(id.Name); ok {
				return engine.Str(v.Type().String()), nil
			}
			// Fall back to the global object (built-ins like Error, Object)
			if v, err := interp.globalObj.Get(id.Name); err == nil && !v.IsUndefined() {
				return engine.Str(v.Type().String()), nil
			}
			return engine.Str("undefined"), nil
		}
	}
	arg, err := interp.evalExpr(e.Arg, scope)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case "!":
		b, _ := arg.Bool()
		return engine.Boolean(!b), nil
	case "-":
		f, _ := arg.Float()
		return engine.Number(-f), nil
	case "+":
		return engine.Number(jsToNumber(arg)), nil
	case "~":
		if bi, ok := engine.BigIntValue(arg); ok {
			return engine.BigInt(new(big.Int).Not(bi)), nil
		}
		n := toInt32(arg)
		return engine.Number(float64(^n)), nil
	case "typeof":
		return engine.Str(arg.Type().String()), nil
	case "void":
		return engine.Undefined(), nil
	case "delete":
		// delete obj.prop / delete obj[key]
		if member, ok := e.Arg.(*ast.MemberExpr); ok {
			obj, err := interp.evalExpr(member.Object, scope)
			if err != nil {
				return nil, err
			}
			if o, ok := obj.AsObject(); ok {
				var key string
				if id, ok := member.Property.(*ast.Identifier); ok {
					key = id.Name
				} else {
					kv, err := interp.evalExpr(member.Property, scope)
					if err != nil {
						return nil, err
					}
					key = kv.String()
				}
				o.Delete(key)
			}
		}
		return engine.Boolean(true), nil
	}
	return nil, fmt.Errorf("%w: unsupported unary operator %s", engine.ErrNotImplemented, e.Op)
}

func (interp *Interpreter) evalUpdate(e *ast.UpdateExpr, scope *Scope) (engine.Value, error) {
	// Get current value
	cur, err := interp.evalExpr(e.Arg, scope)
	if err != nil {
		return nil, err
	}
	delta := int64(1)
	if e.Op == "--" {
		delta = -1
	}
	newVal, err := updateNumeric(cur, delta)
	if err != nil {
		return nil, err
	}
	// Assign back
	if err := interp.assignToRef(e.Arg, newVal, scope); err != nil {
		return nil, err
	}
	if e.Prefix {
		return newVal, nil
	}
	return cur, nil
}

func (interp *Interpreter) evalBinary(e *ast.BinaryExpr, scope *Scope) (engine.Value, error) {
	left, err := interp.evalExpr(e.Left, scope)
	if err != nil {
		return nil, err
	}
	right, err := interp.evalExpr(e.Right, scope)
	if err != nil {
		return nil, err
	}
	return applyBinaryOp(e.Op, left, right), nil
}

func (interp *Interpreter) evalLogical(e *ast.LogicalExpr, scope *Scope) (engine.Value, error) {
	left, err := interp.evalExpr(e.Left, scope)
	if err != nil {
		return nil, err
	}
	if e.Op == "&&" {
		b, _ := left.Bool()
		if !b {
			return left, nil
		}
		return interp.evalExpr(e.Right, scope)
	}
	// ||
	b, _ := left.Bool()
	if b {
		return left, nil
	}
	return interp.evalExpr(e.Right, scope)
}

func (interp *Interpreter) evalConditional(e *ast.ConditionalExpr, scope *Scope) (engine.Value, error) {
	test, err := interp.evalExpr(e.Test, scope)
	if err != nil {
		return nil, err
	}
	cond, _ := test.Bool()
	if cond {
		return interp.evalExpr(e.Consequent, scope)
	}
	return interp.evalExpr(e.Alternate, scope)
}

func (interp *Interpreter) evalSequence(e *ast.SequenceExpr, scope *Scope) (engine.Value, error) {
	var result engine.Value = engine.Undefined()
	for _, expr := range e.Expressions {
		v, err := interp.evalExpr(expr, scope)
		if err != nil {
			return nil, err
		}
		result = v
	}
	return result, nil
}

// callClosure executes a closure's body with the given this and args.
func (interp *Interpreter) callClosure(c *Closure, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	fnScope := c.scope.NewChild()
	// Bind `this`
	fnScope.Declare("__this__", thisVal)
	// Bind parameters (with ES2015 default values).
	for i, param := range c.params {
		var val engine.Value = engine.Undefined()
		if i < len(args) {
			val = args[i]
		}
		// Apply default if the argument is undefined.
		if val.IsUndefined() && c.defaults != nil && i < len(c.defaults) && c.defaults[i] != nil {
			v, err := interp.evalExpr(c.defaults[i], fnScope)
			if err != nil {
				return engine.Undefined(), err
			}
			val = v
		}
		fnScope.Declare(param.Name, val)
	}
	// Bind rest parameter: collect remaining args into an array.
	if c.restParam != nil {
		var restElems []engine.Value
		if len(args) > len(c.params) {
			restElems = append(restElems, args[len(c.params):]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, interp.arrayProto)
		fnScope.Declare(c.restParam.Name, restArr)
	}
	// Bind arguments object for non-arrow functions (simplified)
	if !c.isArrow && interp.argumentsSupported {
		argsObj := engine.NewArray(args)
		fnScope.Declare("arguments", argsObj)
	}

	// Execute body
	switch body := c.body.(type) {
	case *ast.BlockStmt:
		_, err := interp.execBlock(body, fnScope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok && cf.kind == "return" {
				return cf.value, nil
			}
			return engine.Undefined(), err
		}
		return engine.Undefined(), nil
	case ast.Expression:
		// Arrow concise body
		return interp.evalExpr(body, fnScope)
	default:
		return engine.Undefined(), nil
	}
}
