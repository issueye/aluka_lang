package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupReflect registers the Reflect global object with methods mirroring the
// Proxy traps and other reflection operations. Methods that need VM-level
// dispatch (so they honor Proxy traps on the target) obtain the active VM via
// interp.currentVM.
func (interp *Interpreter) setupReflect() {
	reflectObj := engine.NewObject()
	engine.SetProto(reflectObj, interp.objectProto)

	// helper: require a non-null object argument, return its VM.
	requireObj := func(args []engine.Value, name string, idx int) (engine.Value, *VM, error) {
		if len(args) <= idx {
			return engine.Undefined(), nil, fmt.Errorf("%w: Reflect.%s requires an argument", engine.ErrTypeError, name)
		}
		t := args[idx]
		if t.IsNull() || t.IsUndefined() || !t.IsObject() {
			return t, nil, fmt.Errorf("%w: Reflect.%s called on non-object", engine.ErrTypeError, name)
		}
		vm := interp.currentVM
		if vm == nil {
			return t, nil, fmt.Errorf("aluka: internal error: Reflect.%s requires VM context", name)
		}
		return t, vm, nil
	}

	// Reflect.get(target, key[, receiver])
	_ = reflectObj.Set("get", interp.makeFunc("get", func(args []engine.Value) (engine.Value, error) {
		t, vm, err := requireObj(args, "get", 0)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: Reflect.get requires a key", engine.ErrTypeError)
		}
		key := propertyKeyOf(args[1])
		return vm.getProperty(t, key)
	}))

	// Reflect.set(target, key, value[, receiver])
	_ = reflectObj.Set("set", interp.makeFunc("set", func(args []engine.Value) (engine.Value, error) {
		t, vm, err := requireObj(args, "set", 0)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) < 3 {
			return engine.Undefined(), fmt.Errorf("%w: Reflect.set requires key and value", engine.ErrTypeError)
		}
		key := propertyKeyOf(args[1])
		if err := vm.setProperty(t, key, args[2]); err != nil {
			return engine.Boolean(false), err
		}
		return engine.Boolean(true), nil
	}))

	// Reflect.has(target, key)
	_ = reflectObj.Set("has", interp.makeFunc("has", func(args []engine.Value) (engine.Value, error) {
		t, vm, err := requireObj(args, "has", 0)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(vm.inOp(args[1], t)), nil
	}))

	// Reflect.deleteProperty(target, key)
	_ = reflectObj.Set("deleteProperty", interp.makeFunc("deleteProperty", func(args []engine.Value) (engine.Value, error) {
		t, _, err := requireObj(args, "deleteProperty", 0)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		key := propertyKeyOf(args[1])
		// Proxy interception.
		if p, ok := t.(*ProxyValue); ok {
			ok, err := p.proxyDelete(key)
			if err != nil {
				return engine.Boolean(false), err
			}
			return engine.Boolean(ok), nil
		}
		o, ok := t.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(o.Delete(key)), nil
	}))

	// Reflect.ownKeys(target)
	_ = reflectObj.Set("ownKeys", interp.makeFunc("ownKeys", func(args []engine.Value) (engine.Value, error) {
		t, _, err := requireObj(args, "ownKeys", 0)
		if err != nil {
			return engine.Undefined(), err
		}
		var keys []string
		if p, ok := t.(*ProxyValue); ok {
			keys, err = p.proxyOwnKeys()
			if err != nil {
				return engine.Undefined(), err
			}
		} else if o, ok := t.AsObject(); ok {
			keys = o.Keys()
		}
		vals := make([]engine.Value, 0, len(keys))
		for _, k := range keys {
			vals = append(vals, engine.Str(k))
		}
		return interp.newArray(vals), nil
	}))

	// Reflect.getPrototypeOf(target)
	_ = reflectObj.Set("getPrototypeOf", interp.makeFunc("getPrototypeOf", func(args []engine.Value) (engine.Value, error) {
		t, vm, err := requireObj(args, "getPrototypeOf", 0)
		if err != nil {
			return engine.Undefined(), err
		}
		proto := vm.getProto(t)
		if proto == nil {
			return engine.Null(), nil
		}
		return proto, nil
	}))

	// Reflect.setPrototypeOf(target, proto)
	_ = reflectObj.Set("setPrototypeOf", interp.makeFunc("setPrototypeOf", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		t := args[0]
		proto := args[1]
		o, ok := t.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		var protoObj engine.Object
		if !proto.IsNull() {
			po, ok := proto.AsObject()
			if !ok {
				return engine.Boolean(false), nil
			}
			protoObj = po
		}
		engine.SetProto(o, protoObj)
		return engine.Boolean(true), nil
	}))

	// Reflect.apply(target, thisArg, argsList)
	_ = reflectObj.Set("apply", interp.makeFunc("apply", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return engine.Undefined(), fmt.Errorf("%w: Reflect.apply requires target, thisArg, argsList", engine.ErrTypeError)
		}
		target := args[0]
		thisArg := args[1]
		vm := interp.currentVM
		if vm == nil {
			return engine.Undefined(), fmt.Errorf("aluka: internal error: Reflect.apply requires VM context")
		}
		fnArgs := vm.toArrayValues(args[2])
		return vm.invoke(target, thisArg, fnArgs, false)
	}))

	// Reflect.construct(target, argsList[, newTarget])
	_ = reflectObj.Set("construct", interp.makeFunc("construct", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: Reflect.construct requires target and argsList", engine.ErrTypeError)
		}
		target := args[0]
		vm := interp.currentVM
		if vm == nil {
			return engine.Undefined(), fmt.Errorf("aluka: internal error: Reflect.construct requires VM context")
		}
		fnArgs := vm.toArrayValues(args[1])
		return vm.invoke(target, engine.Undefined(), fnArgs, true)
	}))

	// Reflect.defineProperty(target, key, descriptor)
	// Simplified: honors descriptor.value, descriptor.get/set accessors.
	_ = reflectObj.Set("defineProperty", interp.makeFunc("defineProperty", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return engine.Boolean(false), nil
		}
		t := args[0]
		desc, ok := args[2].AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		o, ok := t.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		key := propertyKeyOf(args[1])
		// Accessor descriptor: get/set take precedence over value.
		if getVal, err := desc.Get("get"); err == nil && !getVal.IsUndefined() {
			setVal, _ := desc.Get("set")
			acc := &engine.AccessorValue{}
			if !getVal.IsUndefined() {
				acc.Getter = getVal
			}
			if !setVal.IsUndefined() {
				acc.Setter = setVal
			}
			_ = o.Set(key, acc)
			return engine.Boolean(true), nil
		}
		if val, err := desc.Get("value"); err == nil {
			_ = o.Set(key, val)
		}
		return engine.Boolean(true), nil
	}))

	// Reflect.getOwnPropertyDescriptor(target, key)
	_ = reflectObj.Set("getOwnPropertyDescriptor", interp.makeFunc("getOwnPropertyDescriptor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), nil
		}
		key := propertyKeyOf(args[1])
		val, err := o.Get(key)
		if err != nil || val.IsUndefined() {
			return engine.Undefined(), nil
		}
		desc := engine.NewObject()
		engine.SetProto(desc, interp.objectProto)
		if acc, ok := val.(*engine.AccessorValue); ok {
			if acc.Getter != nil && !acc.Getter.IsUndefined() {
				_ = desc.Set("get", acc.Getter)
			}
			if acc.Setter != nil && !acc.Setter.IsUndefined() {
				_ = desc.Set("set", acc.Setter)
			}
		} else {
			_ = desc.Set("value", val)
			_ = desc.Set("writable", engine.Boolean(true))
		}
		_ = desc.Set("enumerable", engine.Boolean(true))
		_ = desc.Set("configurable", engine.Boolean(true))
		return desc, nil
	}))

	// Reflect.isExtensible(target) — extension tracking is not enforced, so
	// report true for objects.
	_ = reflectObj.Set("isExtensible", interp.makeFunc("isExtensible", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsObject() {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(true), nil
	}))

	// Reflect.preventExtensions(target) — no-op, returns true.
	_ = reflectObj.Set("preventExtensions", interp.makeFunc("preventExtensions", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(true), nil
	}))

	_ = interp.globalObj.Set("Reflect", reflectObj)
}
