package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// Closure is a JS function defined in source (function decl/expr/arrow).
// It captures its lexical scope and implements engine.Function.
type Closure struct {
	obj       engine.Object // function object properties (name, length, etc.)
	params    []*ast.Identifier
	defaults  []ast.Expression // ES2015 default values; nil entry = no default
	restParam *ast.Identifier  // ES2015 rest param; nil if none
	body      ast.Node         // *ast.BlockStmt or ast.Expression (arrow concise body)
	scope     *Scope           // captured lexical environment
	interp    *Interpreter
	name      string
	isArrow   bool
	thisVal   engine.Value // arrow: captured this; regular: ignored (set at call)
}

// newClosure creates a Closure from an AST function node.
func newClosure(interp *Interpreter, scope *Scope, params []*ast.Identifier, defaults []ast.Expression, rest *ast.Identifier, body ast.Node, name string, isArrow bool) *Closure {
	thisVal := engine.Undefined()
	if isArrow && scope != nil {
		// Arrow captures this from current context (stored in scope under "__this__").
		if v, ok := scope.Get("__this__"); ok {
			thisVal = v
		}
	}
	c := &Closure{
		obj:       engine.NewObject(),
		params:    params,
		defaults:  defaults,
		restParam: rest,
		body:      body,
		scope:     scope,
		interp:    interp,
		name:      name,
		isArrow:   isArrow,
		thisVal:   thisVal,
	}
	_ = c.obj.Set("name", engine.Str(name))
	_ = c.obj.Set("length", engine.IntValue(len(params)))
	// Set [[Prototype]] of the function object to Function.prototype
	if fp := interp.functionProto; fp != nil {
		engine.SetProto(c.obj, fp)
	}
	return c
}

// --- engine.Value ---

func (c *Closure) Type() engine.ValueType { return engine.TypeFunction }

func (c *Closure) String() string {
	if c.name == "" {
		return "[Function (anonymous)]"
	}
	return "[Function: " + c.name + "]"
}

func (c *Closure) Int() (int, bool)                    { return 0, false }
func (c *Closure) Float() (float64, bool)              { return 0, false }
func (c *Closure) Bool() (bool, bool)                  { return true, true }
func (c *Closure) IsUndefined() bool                   { return false }
func (c *Closure) IsNull() bool                        { return false }
func (c *Closure) IsObject() bool                      { return true }
func (c *Closure) IsFunction() bool                    { return true }
func (c *Closure) AsObject() (engine.Object, bool)     { return c, true }
func (c *Closure) AsFunction() (engine.Function, bool) { return c, true }

// --- engine.Object ---

func (c *Closure) Get(key string) (engine.Value, error) { return c.obj.Get(key) }
func (c *Closure) Set(key string, v engine.Value) error { return c.obj.Set(key, v) }
func (c *Closure) Keys() []string                       { return c.obj.Keys() }
func (c *Closure) Delete(key string) bool               { return c.obj.Delete(key) }
func (c *Closure) Proto() engine.Object                 { return engine.GetProto(c.obj) }
func (c *Closure) SetProto(proto engine.Object)         { engine.SetProto(c.obj, proto) }

// --- engine.Function ---

// Call invokes the function with this=undefined (strict mode).
func (c *Closure) Call(args []engine.Value) (engine.Value, error) {
	return c.callWith(engine.Undefined(), args)
}

// callWith invokes the function with an explicit this binding.
// For arrow functions, the passed thisVal is ignored in favor of the captured one.
func (c *Closure) callWith(thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	if c.isArrow {
		thisVal = c.thisVal
	}
	return c.interp.callClosure(c, thisVal, args)
}

// construct invokes the function as a constructor (new operator).
func (c *Closure) construct(args []engine.Value) (engine.Value, error) {
	// Create new object with proto set to function.prototype
	newObj := engine.NewObject()
	// Try to get function.prototype
	if proto, err := c.obj.Get("prototype"); err == nil && !proto.IsUndefined() {
		if ov, ok := proto.(engine.Object); ok {
			if setter, ok := newObj.(interface{ SetProto(engine.Object) }); ok {
				setter.SetProto(ov)
			}
		}
	}
	// Call the function with this=newObj
	result, err := c.callWith(newObj, args)
	if err != nil {
		return engine.Undefined(), err
	}
	// If the function returns an object, use that; otherwise use newObj
	if result.IsObject() {
		return result, nil
	}
	return newObj, nil
}

// GoFunction wraps an engine.Func (Go-backed function) so the interpreter
// can treat it uniformly.
type goFunction struct {
	engine.Function
}

func (g *goFunction) callWith(thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	return g.Function.Call(args)
}

func (g *goFunction) construct(args []engine.Value) (engine.Value, error) {
	return g.Function.Call(args)
}

// NativeMethod is a built-in function that receives `this`.
type NativeMethod struct {
	obj  engine.Object
	fn   func(this engine.Value, args []engine.Value) (engine.Value, error)
	name string
}

// NewNativeMethod creates a built-in method that receives `this`.
func NewNativeMethod(name string, fn func(this engine.Value, args []engine.Value) (engine.Value, error)) *NativeMethod {
	m := &NativeMethod{
		obj:  engine.NewObject(),
		fn:   fn,
		name: name,
	}
	_ = m.obj.Set("name", engine.Str(name))
	_ = m.obj.Set("length", engine.IntValue(1))
	return m
}

func (m *NativeMethod) callWith(thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	return m.fn(thisVal, args)
}

func (m *NativeMethod) construct(args []engine.Value) (engine.Value, error) {
	return m.fn(engine.Undefined(), args)
}

func (m *NativeMethod) Type() engine.ValueType { return engine.TypeFunction }

func (m *NativeMethod) String() string {
	if m.name == "" {
		return "function () { [native code] }"
	}
	return "function " + m.name + "() { [native code] }"
}

func (m *NativeMethod) Int() (int, bool)                    { return 0, false }
func (m *NativeMethod) Float() (float64, bool)              { return 0, false }
func (m *NativeMethod) Bool() (bool, bool)                  { return true, true }
func (m *NativeMethod) IsUndefined() bool                   { return false }
func (m *NativeMethod) IsNull() bool                        { return false }
func (m *NativeMethod) IsObject() bool                      { return true }
func (m *NativeMethod) IsFunction() bool                    { return true }
func (m *NativeMethod) AsObject() (engine.Object, bool)     { return m, true }
func (m *NativeMethod) AsFunction() (engine.Function, bool) { return m, true }

func (m *NativeMethod) Get(key string) (engine.Value, error) { return m.obj.Get(key) }
func (m *NativeMethod) Set(key string, v engine.Value) error { return m.obj.Set(key, v) }
func (m *NativeMethod) Keys() []string                       { return m.obj.Keys() }
func (m *NativeMethod) Delete(key string) bool               { return m.obj.Delete(key) }

func (m *NativeMethod) Call(args []engine.Value) (engine.Value, error) {
	return m.fn(engine.Undefined(), args)
}

// callableValue is the interface for values that can be called by the interpreter.
type callableValue interface {
	callWith(thisVal engine.Value, args []engine.Value) (engine.Value, error)
	construct(args []engine.Value) (engine.Value, error)
}

// asCallable returns a callableValue if v is callable.
func asCallable(v engine.Value) (callableValue, error) {
	switch cv := v.(type) {
	case *Closure:
		return cv, nil
	case *NativeMethod:
		return cv, nil
	}
	if f, ok := v.AsFunction(); ok {
		return &goFunction{Function: f}, nil
	}
	return nil, fmt.Errorf("%w: %s is not a function", engine.ErrTypeError, v.Type())
}
