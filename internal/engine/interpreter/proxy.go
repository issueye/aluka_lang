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

func (p *ProxyValue) Type() engine.ValueType          { return engine.TypeObject }
func (p *ProxyValue) String() string                  { return p.target.String() }
func (p *ProxyValue) Int() (int, bool)                { return p.target.Int() }
func (p *ProxyValue) Float() (float64, bool)          { return p.target.Float() }
func (p *ProxyValue) Bool() (bool, bool)              { return p.target.Bool() }
func (p *ProxyValue) IsUndefined() bool               { return p.target.IsUndefined() }
func (p *ProxyValue) IsNull() bool                    { return p.target.IsNull() }
func (p *ProxyValue) IsObject() bool                  { return true }
func (p *ProxyValue) IsFunction() bool                { return p.target.IsFunction() }
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
					if sym, ok := e.(*engine.SymbolValue); ok {
						keys = append(keys, sym.SymbolKey())
					} else {
						keys = append(keys, e.String())
					}
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
func (p *ProxyValue) proxyGet(key string, receiver ...engine.Value) (engine.Value, error) {
	rec := engine.Value(p)
	if len(receiver) > 0 && receiver[0] != nil {
		rec = receiver[0]
	}
	if p.hasTrap("get") {
		return p.callTrap("get", []engine.Value{p.target, p.propertyKeyValue(key), rec})
	}
	return p.vm.getPropertyWithReceiver(p.target, key, rec)
}

// proxySet implements the [[Set]] internal method with the set trap.
func (p *ProxyValue) proxySet(key string, val engine.Value, receiver ...engine.Value) error {
	rec := engine.Value(p)
	if len(receiver) > 0 && receiver[0] != nil {
		rec = receiver[0]
	}
	if p.hasTrap("set") {
		result, err := p.callTrap("set", []engine.Value{p.target, p.propertyKeyValue(key), val, rec})
		if err != nil {
			return err
		}
		ok, _ := result.Bool()
		if !ok {
			return nil
		}
		if target, targetOK := p.target.AsObject(); targetOK {
			if current, exists := engine.OwnPropertyDescriptor(target, key); exists && !current.Configurable {
				if current.HasValue && !current.Writable && !engine.SameValue(val, current.Value) || current.HasSet && orUndefinedValue(current.Set).IsUndefined() {
					return fmt.Errorf("%w: Proxy set trap violated target invariant", engine.ErrTypeError)
				}
			}
		}
		return nil
	}
	return p.vm.setProperty(p.target, key, val)
}

// proxyHas implements the [[HasProperty]] internal method with the has trap.
func (p *ProxyValue) proxyHas(key string) (bool, error) {
	if p.hasTrap("has") {
		result, err := p.callTrap("has", []engine.Value{p.target, p.propertyKeyValue(key)})
		if err != nil {
			return false, err
		}
		b, _ := result.Bool()
		if !b {
			if target, ok := p.target.AsObject(); ok {
				if d, exists := engine.OwnPropertyDescriptor(target, key); exists && (!d.Configurable || !engine.IsExtensible(target)) {
					return false, fmt.Errorf("%w: Proxy has trap cannot hide target property", engine.ErrTypeError)
				}
			}
		}
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
		if b {
			if target, ok := p.target.AsObject(); ok {
				if d, exists := engine.OwnPropertyDescriptor(target, key); exists && !d.Configurable {
					return false, fmt.Errorf("%w: Proxy delete trap cannot delete non-configurable property", engine.ErrTypeError)
				}
			}
		}
		return b, nil
	}
	if o, ok := p.target.AsObject(); ok {
		return o.Delete(key), nil
	}
	return true, nil
}

func proxyKeyList(result engine.Value) ([]string, error) {
	ro, ok := result.AsObject()
	if !ok {
		return nil, fmt.Errorf("%w: ownKeys trap must return an object", engine.ErrTypeError)
	}
	lengthVal, err := ro.Get("length")
	if err != nil {
		return nil, err
	}
	length, ok := lengthVal.Int()
	if !ok || length < 0 {
		return nil, fmt.Errorf("%w: invalid ownKeys result length", engine.ErrTypeError)
	}
	keys := make([]string, 0, length)
	seen := make(map[string]bool, length)
	for i := 0; i < length; i++ {
		e, err := ro.Get(fmt.Sprintf("%d", i))
		if err != nil {
			return nil, err
		}
		var key string
		if sym, ok := e.(*engine.SymbolValue); ok {
			key = sym.SymbolKey()
		} else if e.Type() == engine.TypeString {
			key = e.String()
		} else {
			return nil, fmt.Errorf("%w: ownKeys entries must be strings or symbols", engine.ErrTypeError)
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: ownKeys trap returned duplicate key", engine.ErrTypeError)
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys, nil
}

// proxyOwnKeys implements the [[OwnPropertyKeys]] internal method with the ownKeys trap.
func (p *ProxyValue) proxyOwnKeys() ([]string, error) {
	if p.hasTrap("ownKeys") {
		result, err := p.callTrap("ownKeys", []engine.Value{p.target})
		if err != nil {
			return nil, err
		}
		keys, err := proxyKeyList(result)
		if err != nil {
			return nil, err
		}
		if target, ok := p.target.AsObject(); ok {
			targetKeys := engine.AllOwnKeys(target)
			seen := make(map[string]bool, len(keys))
			for _, key := range keys {
				seen[key] = true
			}
			for _, key := range targetKeys {
				d, _ := engine.OwnPropertyDescriptor(target, key)
				if !d.Configurable && !seen[key] {
					return nil, fmt.Errorf("%w: ownKeys trap omitted non-configurable key", engine.ErrTypeError)
				}
				if !engine.IsExtensible(target) && !seen[key] {
					return nil, fmt.Errorf("%w: ownKeys trap omitted target key", engine.ErrTypeError)
				}
			}
			if !engine.IsExtensible(target) && len(keys) != len(targetKeys) {
				return nil, fmt.Errorf("%w: ownKeys trap added key to non-extensible target", engine.ErrTypeError)
			}
		}
		return keys, nil
	}
	if o, ok := p.target.AsObject(); ok {
		return engine.AllOwnKeys(o), nil
	}
	return nil, nil
}

func (p *ProxyValue) propertyKeyValue(key string) engine.Value {
	if sym, ok := engine.SymbolFromKey(key); ok {
		return sym
	}
	return engine.Str(key)
}

func (p *ProxyValue) descriptorObject(d engine.Descriptor) engine.Object {
	o := engine.NewObject()
	engine.SetProto(o, p.vm.interp.objectProto)
	if d.HasValue {
		_ = o.Set("value", d.Value)
	}
	if d.HasWritable {
		_ = o.Set("writable", engine.Boolean(d.Writable))
	}
	if d.HasEnumerable {
		_ = o.Set("enumerable", engine.Boolean(d.Enumerable))
	}
	if d.HasConfigurable {
		_ = o.Set("configurable", engine.Boolean(d.Configurable))
	}
	if d.HasGet {
		_ = o.Set("get", orUndefinedValue(d.Get))
	}
	if d.HasSet {
		_ = o.Set("set", orUndefinedValue(d.Set))
	}
	return o
}

// proxyDefineProperty implements the Proxy [[DefineOwnProperty]] internal method.
func (p *ProxyValue) proxyDefineProperty(key string, d engine.Descriptor) (bool, error) {
	if p.hasTrap("defineProperty") {
		result, err := p.callTrap("defineProperty", []engine.Value{p.target, p.propertyKeyValue(key), p.descriptorObject(d)})
		if err != nil {
			return false, err
		}
		ok, _ := result.Bool()
		if !ok {
			return false, nil
		}
		target, targetOK := p.target.AsObject()
		if !targetOK || !engine.IsCompatibleDescriptor(target, key, d) {
			return false, fmt.Errorf("%w: Proxy defineProperty trap violated target invariant", engine.ErrTypeError)
		}
		if current, exists := engine.OwnPropertyDescriptor(target, key); exists && current.Configurable && d.HasConfigurable && !d.Configurable ||
			!exists && d.HasConfigurable && !d.Configurable {
			return false, fmt.Errorf("%w: Proxy defineProperty trap cannot create a new non-configurable property", engine.ErrTypeError)
		}
		return true, nil
	}
	o, ok := p.target.AsObject()
	if !ok {
		return false, nil
	}
	if err := engine.DefineOwnProperty(o, key, d); err != nil {
		return false, err
	}
	return true, nil
}

// proxyGetOwnPropertyDescriptor implements the Proxy [[GetOwnProperty]] method.
func (p *ProxyValue) proxyGetOwnPropertyDescriptor(key string) (engine.Value, error) {
	if p.hasTrap("getOwnPropertyDescriptor") {
		result, err := p.callTrap("getOwnPropertyDescriptor", []engine.Value{p.target, p.propertyKeyValue(key)})
		if err != nil {
			return engine.Undefined(), err
		}
		target, _ := p.target.AsObject()
		current, exists := engine.OwnPropertyDescriptor(target, key)
		if result.IsUndefined() {
			if exists && (!current.Configurable || !engine.IsExtensible(target)) {
				return engine.Undefined(), fmt.Errorf("%w: getOwnPropertyDescriptor trap cannot hide target property", engine.ErrTypeError)
			}
			return engine.Undefined(), nil
		}
		resultObj, ok := result.AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: getOwnPropertyDescriptor trap must return an object or undefined", engine.ErrTypeError)
		}
		d, err := descriptorFrom(resultObj, p.vm)
		if err != nil {
			return engine.Undefined(), err
		}
		if !engine.IsCompatibleDescriptor(target, key, d) || !exists && d.HasConfigurable && !d.Configurable {
			return engine.Undefined(), fmt.Errorf("%w: getOwnPropertyDescriptor trap violated target invariant", engine.ErrTypeError)
		}
		return p.vm.interp.descriptorObject(d), nil
	}
	o, ok := p.target.AsObject()
	if !ok {
		return engine.Undefined(), nil
	}
	return p.vm.interp.ownPropertyDescriptor(o, key), nil
}

func (p *ProxyValue) proxySetPrototypeOf(proto engine.Object) (engine.Value, error) {
	if p.hasTrap("setPrototypeOf") {
		arg := engine.Value(engine.Null())
		if proto != nil {
			arg = proto
		}
		result, err := p.callTrap("setPrototypeOf", []engine.Value{p.target, arg})
		if err != nil {
			return engine.Boolean(false), err
		}
		ok, _ := result.Bool()
		if ok {
			if target, targetOK := p.target.AsObject(); targetOK && !engine.IsExtensible(target) && engine.GetProto(target) != proto {
				return engine.Boolean(false), fmt.Errorf("%w: setPrototypeOf trap violated target invariant", engine.ErrTypeError)
			}
		}
		return engine.Boolean(ok), nil
	}
	target, ok := p.target.AsObject()
	return engine.Boolean(ok && engine.TrySetProto(target, proto)), nil
}

func (p *ProxyValue) proxyIsExtensible() (bool, error) {
	target, ok := p.target.AsObject()
	if !ok {
		return false, nil
	}
	actual := engine.IsExtensible(target)
	if !p.hasTrap("isExtensible") {
		return actual, nil
	}
	result, err := p.callTrap("isExtensible", []engine.Value{p.target})
	if err != nil {
		return false, err
	}
	reported, _ := result.Bool()
	if reported != actual {
		return false, fmt.Errorf("%w: isExtensible trap violated target invariant", engine.ErrTypeError)
	}
	return reported, nil
}

func (p *ProxyValue) proxyPreventExtensions() (bool, error) {
	target, ok := p.target.AsObject()
	if !ok {
		return false, nil
	}
	if !p.hasTrap("preventExtensions") {
		return engine.PreventExtensions(target), nil
	}
	result, err := p.callTrap("preventExtensions", []engine.Value{p.target})
	if err != nil {
		return false, err
	}
	reported, _ := result.Bool()
	if reported && engine.IsExtensible(target) {
		return false, fmt.Errorf("%w: preventExtensions trap violated target invariant", engine.ErrTypeError)
	}
	return reported, nil
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
