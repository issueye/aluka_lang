// 内置函数对象：Function.prototype（call/apply/bind/toString）与 Symbol 命名空间、FinalizationRegistry。

package interpreter

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupFinalizationRegistry installs the API shape used by undici. The VM's
// object GC does not expose weak reachability callbacks, so registrations are
// retained as inert records and are never invoked spuriously.
func (interp *Interpreter) setupFinalizationRegistry() {
	ctor := interp.makeFunc("FinalizationRegistry", func(args []engine.Value) (engine.Value, error) {
		registry := engine.NewObject()
		var callback engine.Value
		if len(args) > 0 && args[0].IsFunction() {
			callback = args[0]
		}
		_ = callback
		_ = registry.Set("register", interp.makeFunc("register", func(_ []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = registry.Set("unregister", interp.makeFunc("unregister", func(_ []engine.Value) (engine.Value, error) {
			return engine.Boolean(false), nil
		}))
		_ = registry.Set("cleanupSome", interp.makeFunc("cleanupSome", func(_ []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		return registry, nil
	})
	_ = interp.globalObj.Set("FinalizationRegistry", ctor)
}

func (interp *Interpreter) setupFunctionProto() {
	p := interp.functionProto
	// Function.prototype 的原型是 Object.prototype，使函数对象可访问
	// hasOwnProperty/toString 等对象方法（ECMAScript 语义）。
	engine.SetProto(p, interp.objectProto)
	// Function.prototype[Symbol.hasInstance]：`fn[Symbol.hasInstance](V)` 等价于
	// `V instanceof fn`。undici 等库直接调用该内置方法（Function.call.bind(
	// Function.prototype[Symbol.hasInstance])），缺失会导致 "undefined is not a
	// function"。注意不能内部调用 VM.instanceof——它又会查 Symbol.hasInstance
	// 导致递归栈溢出；此处实现普通原型链判断（忽略 @@hasInstance 覆写）。
	_ = p.Set(engine.SymbolHasInstance.SymbolKey(), interp.nativeMethod("[Symbol.hasInstance]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var v engine.Value = engine.Undefined()
		if len(args) > 0 {
			v = args[0]
		}
		return engine.Boolean(interp.ordinaryHasInstance(v, this)), nil
	}))
	_ = p.Set("call", interp.nativeMethod("call", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		callable, err := asCallable(this)
		if err != nil {
			return nil, err
		}
		var thisVal engine.Value = engine.Undefined()
		var callArgs []engine.Value
		if len(args) > 0 {
			thisVal = args[0]
			callArgs = args[1:]
		}
		return callable.callWith(thisVal, callArgs)
	}))
	_ = p.Set("apply", interp.nativeMethod("apply", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		callable, err := asCallable(this)
		if err != nil {
			return nil, err
		}
		var thisVal engine.Value = engine.Undefined()
		var callArgs []engine.Value
		if len(args) > 0 {
			thisVal = args[0]
		}
		if len(args) > 1 && !args[1].IsUndefined() {
			if arr, ok := args[1].(*engine.ArrayValue); ok {
				callArgs = arr.Elems()
			} else {
				return nil, fmt.Errorf("%w: apply argArray must be an array", engine.ErrTypeError)
			}
		}
		return callable.callWith(thisVal, callArgs)
	}))
	_ = p.Set("bind", interp.nativeMethod("bind", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		callable, err := asCallable(this)
		if err != nil {
			return nil, err
		}
		boundThis := engine.Undefined()
		var boundArgs []engine.Value
		if len(args) > 0 {
			boundThis = args[0]
			boundArgs = append(boundArgs, args[1:]...)
		}
		// Capture the bound function name if available
		boundName := "bound"
		if n, ok := this.AsObject(); ok && n != nil {
			if nv, gerr := n.Get("name"); gerr == nil {
				boundName = "bound " + nv.String()
			}
		}
		return NewNativeMethod(boundName, func(_ engine.Value, callArgs []engine.Value) (engine.Value, error) {
			allArgs := append(append([]engine.Value{}, boundArgs...), callArgs...)
			return callable.callWith(boundThis, allArgs)
		}), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))

	// 注册全局 Function 构造器，支持动态编译（供 Vue 3 模板运行时编译 compileToFunction / new Function 使用）。
	ctor := interp.makeFunc("Function", func(args []engine.Value) (engine.Value, error) {
		paramList := ""
		body := ""
		if len(args) == 1 {
			body = args[0].String()
		} else if len(args) > 1 {
			var params []string
			for i := 0; i < len(args)-1; i++ {
				params = append(params, args[i].String())
			}
			paramList = strings.Join(params, ",")
			body = args[len(args)-1].String()
		}
		source := "(function anonymous(" + paramList + ") {\n" + body + "\n})"
		vm := interp.currentVM
		if vm != nil {
			mod, err := vm.Compile(source, "anonymous.js")
			if err != nil {
				return engine.Undefined(), fmt.Errorf("%w: %v", engine.ErrSyntaxError, err)
			}
			fnVal, err := vm.RunModule(mod)
			if err != nil {
				return engine.Undefined(), err
			}
			return fnVal, nil
		}
		return interp.Eval(source, "anonymous.js")
	})
	_ = ctor.Set("prototype", p)
	_ = p.Set("constructor", ctor)
	_ = interp.globalObj.Set("Function", ctor)
	interp.constructors["Function"] = ctor
}

// setupSymbol creates the Symbol global function and well-known symbols.
// Symbol is not a constructor (new Symbol() throws), but Symbol() creates
// a unique symbol primitive.
func (interp *Interpreter) setupSymbol() {
	symbolFn := interp.makeFunc("Symbol", func(args []engine.Value) (engine.Value, error) {
		desc := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			desc = args[0].String()
		}
		return engine.NewSymbol(desc), nil
	})
	// Well-known symbols accessible as Symbol.iterator, Symbol.asyncIterator, etc.
	_ = symbolFn.Set("iterator", engine.SymbolIterator)
	_ = symbolFn.Set("asyncIterator", engine.SymbolAsyncIterator)
	_ = symbolFn.Set("hasInstance", engine.SymbolHasInstance)
	_ = symbolFn.Set("toPrimitive", engine.SymbolToPrimitive)
	_ = symbolFn.Set("toStringTag", engine.SymbolToStringTag)
	_ = symbolFn.Set("match", engine.SymbolMatch)
	_ = symbolFn.Set("replace", engine.SymbolReplace)
	_ = symbolFn.Set("search", engine.SymbolSearch)
	_ = symbolFn.Set("split", engine.SymbolSplit)
	_ = symbolFn.Set("species", engine.SymbolSpecies)
	// Symbol.for(key) / Symbol.keyFor(symbol): global symbol registry.
	_ = symbolFn.Set("for", interp.makeFunc("for", func(args []engine.Value) (engine.Value, error) {
		key := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			key = args[0].String()
		}
		return engine.SymbolFor(key), nil
	}))
	_ = symbolFn.Set("keyFor", interp.makeFunc("keyFor", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: Symbol.keyFor requires a symbol argument", engine.ErrTypeError)
		}
		sym, ok := args[0].(*engine.SymbolValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: %s is not a symbol", engine.ErrTypeError, args[0].Type())
		}
		if k, ok := sym.KeyFor(); ok {
			return engine.Str(k), nil
		}
		return engine.Undefined(), nil
	}))
	_ = interp.globalObj.Set("Symbol", symbolFn)
}
