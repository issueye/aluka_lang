package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// ProxyValue is a JS Proxy: it wraps a target object and a handler whose
// trap functions intercept fundamental operations (get/set/has/delete/...).
type ProxyValue struct {
	obj     engine.Object // backing object (stores proto, rarely used directly)
	vm      *VM
	target  engine.Value
	handler engine.Object
}

// NewProxyValue creates a Proxy wrapping target with the given handler.
func NewProxyValue(vm *VM, target engine.Value, handler engine.Object) *ProxyValue {
	p := &ProxyValue{
		vm:      vm,
		target:  target,
		handler: handler,
	}
	obj := engine.NewObject()
	engine.SetProto(obj, vm.interp.objectProto)
	p.obj = obj
	return p
}

// --- engine.Value ---

func (p *ProxyValue) Type() engine.ValueType { return engine.TypeObject }
func (p *ProxyValue) String() string         { return p.target.String() }
func (p *ProxyValue) Int() (int, bool)       { return p.target.Int() }
func (p *ProxyValue) Float() (float64, bool) { return p.target.Float() }
func (p *ProxyValue) Bool() (bool, bool)     { return p.target.Bool() }
func (p *ProxyValue) IsUndefined() bool      { return p.target.IsUndefined() }
func (p *ProxyValue) IsNull() bool           { return p.target.IsNull() }
func (p *ProxyValue) IsObject() bool         { return true }
func (p *ProxyValue) IsFunction() bool       { return p.target.IsFunction() }
func (p *ProxyValue) AsObject() (engine.Object, bool) { return p, true }
func (p *ProxyValue) AsFunction() (engine.Function, bool) {
	if f, ok := p.target.AsFunction(); ok {
		return f, ok
	}
	return nil, false
}

// Get/Set/Keys/Delete on ProxyValue itself are only called by code paths that
// bypass VM.getProperty (e.g. AST interpreter or direct object method calls).
// For the VM path, we intercept in VM.getProperty/setProperty instead.
// Here we delegate to the target as a fallback.
func (p *ProxyValue) Get(key string) (engine.Value, error) {
	if p.hasTrap("get") {
		return p.callTrap("get", []engine.Value{p.target, engine.Str(key), p})
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Get(key)
	}
	return engine.Undefined(), nil
}

func (p *ProxyValue) Set(key string, val engine.Value) error {
	if p.hasTrap("set") {
		_, err := p.callTrap("set", []engine.Value{p.target, engine.Str(key), val, p})
		return err
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Set(key, val)
	}
	return nil
}

func (p *ProxyValue) Keys() []string {
	if p.hasTrap("ownKeys") {
		result, err := p.callTrap("ownKeys", []engine.Value{p.target})
		if err == nil {
			if arr, ok := result.(*engine.ArrayValue); ok {
				keys := make([]string, 0, len(arr.Elems()))
				for _, e := range arr.Elems() {
					keys = append(keys, e.String())
				}
				return keys
			}
		}
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Keys()
	}
	return nil
}

func (p *ProxyValue) Delete(key string) bool {
	if p.hasTrap("deleteProperty") {
		result, err := p.callTrap("deleteProperty", []engine.Value{p.target, engine.Str(key)})
		if err == nil {
			b, _ := result.Bool()
			return b
		}
		return false
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Delete(key)
	}
	return true
}

// --- Trap helpers ---

// hasTrap returns true if the handler defines a function for the named trap.
func (p *ProxyValue) hasTrap(name string) bool {
	if p.handler == nil {
		return false
	}
	trap, err := p.handler.Get(name)
	if err != nil {
		return false
	}
	return isCallable(trap)
}

// callTrap invokes the named handler trap with the given args.
func (p *ProxyValue) callTrap(name string, args []engine.Value) (engine.Value, error) {
	trap, err := p.handler.Get(name)
	if err != nil || !isCallable(trap) {
		return engine.Undefined(), fmt.Errorf("%w: handler is not callable", engine.ErrTypeError)
	}
	// Use the VM's invoke path so bytecode closures work.
	return p.vm.invoke(trap, engine.Undefined(), args, false)
}

// hasTrapSymbol returns true if the handler defines a function for the
// symbol-keyed trap (e.g. Symbol.hasInstance).
func (p *ProxyValue) hasTrapSymbol(sym *engine.SymbolValue) bool {
	if p.handler == nil {
		return false
	}
	trap, err := p.handler.Get(sym.SymbolKey())
	if err != nil {
		return false
	}
	return isCallable(trap)
}

// callTrapSymbol invokes a symbol-keyed handler trap with the given args.
func (p *ProxyValue) callTrapSymbol(sym *engine.SymbolValue, args []engine.Value) (engine.Value, error) {
	trap, err := p.handler.Get(sym.SymbolKey())
	if err != nil || !isCallable(trap) {
		return engine.Undefined(), fmt.Errorf("%w: handler is not callable", engine.ErrTypeError)
	}
	return p.vm.invoke(trap, engine.Undefined(), args, false)
}

// --- VM-aware trap dispatch (called from VM methods) ---

// proxyGet implements the [[Get]] internal method with the get trap.
func (p *ProxyValue) proxyGet(key string) (engine.Value, error) {
	if p.hasTrap("get") {
		return p.callTrap("get", []engine.Value{p.target, engine.Str(key), p})
	}
	return p.vm.getProperty(p.target, key)
}

// proxySet implements the [[Set]] internal method with the set trap.
func (p *ProxyValue) proxySet(key string, val engine.Value) error {
	if p.hasTrap("set") {
		_, err := p.callTrap("set", []engine.Value{p.target, engine.Str(key), val, p})
		return err
	}
	return p.vm.setProperty(p.target, key, val)
}

// proxyHas implements the [[HasProperty]] internal method with the has trap.
func (p *ProxyValue) proxyHas(key string) (bool, error) {
	if p.hasTrap("has") {
		result, err := p.callTrap("has", []engine.Value{p.target, engine.Str(key)})
		if err != nil {
			return false, err
		}
		b, _ := result.Bool()
		return b, nil
	}
	// Default: forward to target via inOp (walks proto chain correctly).
	return p.vm.inOp(engine.Str(key), p.target), nil
}

// proxyDelete implements the [[Delete]] internal method with the deleteProperty trap.
func (p *ProxyValue) proxyDelete(key string) (bool, error) {
	if p.hasTrap("deleteProperty") {
		result, err := p.callTrap("deleteProperty", []engine.Value{p.target, engine.Str(key)})
		if err != nil {
			return false, err
		}
		b, _ := result.Bool()
		return b, nil
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Delete(key), nil
	}
	return true, nil
}

// proxyOwnKeys implements the [[OwnPropertyKeys]] internal method with the ownKeys trap.
func (p *ProxyValue) proxyOwnKeys() ([]string, error) {
	if p.hasTrap("ownKeys") {
		result, err := p.callTrap("ownKeys", []engine.Value{p.target})
		if err != nil {
			return nil, err
		}
		if arr, ok := result.(*engine.ArrayValue); ok {
			keys := make([]string, 0, len(arr.Elems()))
			for _, e := range arr.Elems() {
				keys = append(keys, e.String())
			}
			return keys, nil
		}
		// Handle array-like objects.
		if result.IsObject() {
			ro, _ := result.AsObject()
			lengthVal, _ := ro.Get("length")
			length, _ := lengthVal.Int()
			keys := make([]string, 0, length)
			for i := 0; i < length; i++ {
				e, _ := ro.Get(fmt.Sprintf("%d", i))
				keys = append(keys, e.String())
			}
			return keys, nil
		}
		return nil, nil
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Keys(), nil
	}
	return nil, nil
}

// proxyGetProto implements the [[GetPrototypeOf]] internal method with the
// getPrototypeOf trap.
func (p *ProxyValue) proxyGetProto() (engine.Object, error) {
	if p.hasTrap("getPrototypeOf") {
		result, err := p.callTrap("getPrototypeOf", []engine.Value{p.target})
		if err != nil {
			return nil, err
		}
		if result.IsNull() {
			return nil, nil
		}
		if obj, ok := result.(engine.Object); ok {
			return obj, nil
		}
		return nil, nil
	}
	return p.vm.getProto(p.target), nil
}

// proxyGetSymbol reads a symbol-keyed property from the proxy, passing the
// actual Symbol value (not the internal string key) to the get trap. This is
// needed for traps like Symbol.hasInstance where the handler compares
// `key === Symbol.hasInstance`.
func (p *ProxyValue) proxyGetSymbol(sym *engine.SymbolValue) (engine.Value, error) {
	if p.hasTrap("get") {
		return p.callTrap("get", []engine.Value{p.target, sym, p})
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Get(sym.SymbolKey())
	}
	return engine.Undefined(), nil
}

// --- setupProxy: register Proxy constructor and Revocable API --------------

func (interp *Interpreter) setupProxy() {
	proxyCtor := interp.makeFunc("Proxy", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: Proxy requires target and handler", engine.ErrTypeError)
		}
		target := args[0]
		if !target.IsObject() {
			return engine.Undefined(), fmt.Errorf("%w: Proxy target must be an object", engine.ErrTypeError)
		}
		handler, ok := args[1].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Proxy handler must be an object", engine.ErrTypeError)
		}
		// The VM is needed for trap invocation. We obtain it from the
		// current call context via a thread-local on the interpreter.
		vm := interp.currentVM
		if vm == nil {
			return engine.Undefined(), fmt.Errorf("aluka: internal error: Proxy requires VM context")
		}
		return NewProxyValue(vm, target, handler), nil
	})

	// Proxy.revocable(target, handler) — returns { proxy, revoke }.
	_ = proxyCtor.Set("revocable", interp.makeFunc("revocable", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: Proxy.revocable requires target and handler", engine.ErrTypeError)
		}
		target := args[0]
		handler, ok := args[1].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: Proxy handler must be an object", engine.ErrTypeError)
		}
		vm := interp.currentVM
		if vm == nil {
			return engine.Undefined(), fmt.Errorf("aluka: internal error: Proxy requires VM context")
		}
		p := NewProxyValue(vm, target, handler)
		// Create result { proxy, revoke }.
		result := engine.NewObject()
		engine.SetProto(result, interp.objectProto)
		_ = result.Set("proxy", p)
		revoke := interp.nativeMethod("revoke", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			// Mark the proxy as revoked by replacing target/handler with null.
			p.target = engine.Null()
			p.handler = nil
			return engine.Undefined(), nil
		})
		_ = result.Set("revoke", revoke)
		return result, nil
	}))

	_ = interp.globalObj.Set("Proxy", proxyCtor)
	interp.constructors["Proxy"] = proxyCtor
}
