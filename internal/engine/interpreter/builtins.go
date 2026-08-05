package interpreter

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupBuiltins initializes all built-in prototypes, constructors, and global functions.
func (interp *Interpreter) setupBuiltins() {
	// Create prototype objects
	interp.objectProto = engine.NewObject()
	interp.functionProto = engine.NewObject()
	interp.arrayProto = engine.NewObject()
	interp.stringProto = engine.NewObject()
	interp.numberProto = engine.NewObject()
	interp.booleanProto = engine.NewObject()
	interp.errorProto = engine.NewObject()

	// Object.prototype methods
	interp.setupObjectProto()
	// Array.prototype methods
	interp.setupArrayProto()
	// String.prototype methods
	interp.setupStringProto()
	// Number.prototype methods
	interp.setupNumberProto()
	// Function.prototype methods
	interp.setupFunctionProto()
	// Error.prototype methods
	interp.setupErrorProto()

	// Constructors
	interp.setupObjectCtor()
	interp.setupArrayCtor()
	interp.setupStringCtor()
	interp.setupNumberCtor()
	interp.setupBooleanCtor()
	interp.setupErrorCtors()
	// V8 stack-trace API（Error.captureStackTrace/prepareStackTrace/stackTraceLimit）。
	interp.setupErrorV8Stack()

	// Math
	interp.setupMath()
	// JSON
	interp.setupJSON()
	// Symbol
	interp.setupSymbol()
	// Promise + microtask queue
	interp.setupPromise()
	// Map / Set / WeakMap / WeakSet
	interp.setupMap()
	interp.setupSet()
	interp.setupWeakMap()
	interp.setupWeakSet()
	// RegExp（Go regexp 翻译层内核）
	interp.setupRegexp()
	// Date（P0-3）
	interp.setupDate()
	// URI 编码全局（P0-3）
	interp.setupURI()
	// structuredClone（P1-4）
	interp.setupStructuredClone()
	// Proxy / Reflect
	interp.setupProxy()
	interp.setupReflect()

	// Global functions
	interp.setupGlobalFuncs()
}

// makeFunc creates a Go-backed function and sets its prototype to Function.prototype.
func (interp *Interpreter) makeFunc(name string, fn engine.Func) engine.Object {
	f := engine.NewFunction(name, fn)
	engine.SetProto(f, interp.functionProto)
	return f.(engine.Object)
}

// makeCtor creates a constructor function with a .prototype object.
func (interp *Interpreter) makeCtor(name string, proto engine.Object, fn engine.Func) engine.Object {
	ctor := engine.NewObject()
	f := engine.NewFunction(name, fn)
	// Copy function properties
	_ = ctor.Set("name", engine.Str(name))
	_ = ctor.Set("length", engine.IntValue(1))
	_ = ctor.Set("prototype", proto)
	engine.SetProto(ctor, interp.functionProto)
	// Also make proto.constructor point back
	_ = proto.Set("constructor", ctor)
	// Store the underlying function for calling
	_ = ctor.Set("__call__", f)
	return ctor
}

// --- Object.prototype ---

func (interp *Interpreter) setupObjectProto() {
	p := interp.objectProto
	_ = p.Set("hasOwnProperty", interp.nativeMethod("hasOwnProperty", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		key := args[0].String()
		o, ok := this.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		for _, k := range o.Keys() {
			if k == key {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		switch this.Type() {
		case engine.TypeUndefined:
			return engine.Str("[object Undefined]"), nil
		case engine.TypeNull:
			return engine.Str("[object Null]"), nil
		case engine.TypeBoolean:
			return engine.Str("[object Boolean]"), nil
		case engine.TypeNumber:
			return engine.Str("[object Number]"), nil
		case engine.TypeString:
			return engine.Str("[object String]"), nil
		case engine.TypeFunction:
			return engine.Str("[object Function]"), nil
		default:
			return engine.Str("[object Object]"), nil
		}
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return this, nil
	}))
	_ = p.Set("isPrototypeOf", interp.nativeMethod("isPrototypeOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		proto := engine.GetProto(args[0])
		thisObj, ok := this.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		for proto != nil {
			if proto == thisObj {
				return engine.Boolean(true), nil
			}
			proto = engine.GetProto(proto)
		}
		return engine.Boolean(false), nil
	}))
	_ = p.Set("propertyIsEnumerable", interp.nativeMethod("propertyIsEnumerable", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		o, ok := this.AsObject()
		if !ok {
			return engine.Boolean(false), nil
		}
		for _, k := range o.Keys() {
			if k == args[0].String() {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))
}

// --- Object constructor ---

func (interp *Interpreter) setupObjectCtor() {
	// Object is both a constructor (new Object() / Object()) and a namespace
	// for static methods (Object.keys, Object.values, ...). Use makeFunc so it
	// is callable; static methods are attached as properties on the function.
	obj := interp.makeFunc("Object", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || args[0].IsUndefined() || args[0].IsNull() {
			o := engine.NewObject()
			engine.SetProto(o, interp.objectProto)
			return o, nil
		}
		if ao, ok := args[0].AsObject(); ok {
			return ao, nil
		}
		o := engine.NewObject()
		engine.SetProto(o, interp.objectProto)
		return o, nil
	})
	_ = obj.Set("keys", interp.makeFunc("keys", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		// Proxy interception: use the ownKeys trap.
		if p, ok := args[0].(*ProxyValue); ok {
			keys, err := p.proxyOwnKeys()
			if err != nil {
				return interp.newArray(nil), nil
			}
			return interp.newArray(toValues(keys)), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		return interp.newArray(toValues(o.Keys())), nil
	}))
	_ = obj.Set("values", interp.makeFunc("values", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		var vals []engine.Value
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			vals = append(vals, v)
		}
		return interp.newArray(vals), nil
	}))
	_ = obj.Set("entries", interp.makeFunc("entries", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return interp.newArray(nil), nil
		}
		o, ok := args[0].AsObject()
		if !ok {
			return interp.newArray(nil), nil
		}
		var entries []engine.Value
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			entry := interp.newArray([]engine.Value{engine.Str(k), v})
			entries = append(entries, entry)
		}
		return interp.newArray(entries), nil
	}))
	_ = obj.Set("assign", interp.makeFunc("assign", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewObject(), nil
		}
		target, ok := args[0].AsObject()
		if !ok {
			return nil, fmt.Errorf("%w: Object.assign target must be object", engine.ErrTypeError)
		}
		for i := 1; i < len(args); i++ {
			src, ok := args[i].AsObject()
			if !ok {
				continue
			}
			for _, k := range src.Keys() {
				v, _ := src.Get(k)
				_ = target.Set(k, v)
			}
		}
		return target, nil
	}))
	_ = obj.Set("freeze", interp.makeFunc("freeze", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			return args[0], nil
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("getPrototypeOf", interp.makeFunc("getPrototypeOf", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Null(), nil
		}
		// Proxy interception: use the VM's getProto so the getPrototypeOf
		// trap fires for Proxy targets.
		if vm := interp.currentVM; vm != nil {
			proto := vm.getProto(args[0])
			if proto == nil {
				return engine.Null(), nil
			}
			return proto, nil
		}
		proto := engine.GetProto(args[0])
		if proto == nil {
			return engine.Null(), nil
		}
		return proto, nil
	}))
	// Object 静态方法扩展（见 object_methods.go）。
	interp.setupObjectCtorExt(obj)
	_ = obj.Set("prototype", interp.objectProto)
	_ = obj.Set("name", engine.Str("Object"))
	engine.SetProto(obj, interp.functionProto)
	_ = interp.objectProto.Set("constructor", obj)
	interp.globalObj.Set("Object", obj)
	interp.constructors["Object"] = obj
}

// nativeMethod creates a built-in method that receives `this`.
func (interp *Interpreter) nativeMethod(name string, fn func(this engine.Value, args []engine.Value) (engine.Value, error)) engine.Value {
	m := NewNativeMethod(name, fn)
	engine.SetProto(m, interp.functionProto)
	return m
}

// --- Array.prototype ---

func (interp *Interpreter) setupArrayProto() {
	p := interp.arrayProto
	_ = p.Set("push", interp.nativeMethod("push", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.IntValue(0), nil
		}
		for _, a := range args {
			_ = arr.Set(strconv.Itoa(len(arr.Elems())), a)
		}
		return engine.IntValue(len(arr.Elems())), nil
	}))
	_ = p.Set("pop", interp.nativeMethod("pop", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		elems := arr.Elems()
		if len(elems) == 0 {
			return engine.Undefined(), nil
		}
		last := elems[len(elems)-1]
		_ = arr.Set("length", engine.IntValue(len(elems)-1))
		return last, nil
	}))
	_ = p.Set("shift", interp.nativeMethod("shift", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Undefined(), nil
		}
		elems := arr.Elems()
		if len(elems) == 0 {
			return engine.Undefined(), nil
		}
		first := elems[0]
		// Shift elements
		rest := elems[1:]
		for i, e := range rest {
			_ = arr.Set(strconv.Itoa(i), e)
		}
		_ = arr.Set("length", engine.IntValue(len(rest)))
		return first, nil
	}))
	_ = p.Set("unshift", interp.nativeMethod("unshift", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.IntValue(0), nil
		}
		old := arr.Elems()
		newElems := append(append([]engine.Value{}, args...), old...)
		for i, e := range newElems {
			_ = arr.Set(strconv.Itoa(i), e)
		}
		return engine.IntValue(len(newElems)), nil
	}))
	_ = p.Set("join", interp.nativeMethod("join", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Str(""), nil
		}
		sep := ","
		if len(args) > 0 && !args[0].IsUndefined() {
			sep = args[0].String()
		}
		elems := arr.Elems()
		parts := make([]string, len(elems))
		for i, e := range elems {
			if e.IsUndefined() || e.IsNull() {
				parts[i] = ""
			} else {
				parts[i] = e.String()
			}
		}
		return engine.Str(strings.Join(parts, sep)), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.Str(""), nil
		}
		elems := arr.Elems()
		parts := make([]string, len(elems))
		for i, e := range elems {
			if e.IsUndefined() || e.IsNull() {
				parts[i] = ""
			} else {
				parts[i] = e.String()
			}
		}
		return engine.Str(strings.Join(parts, ",")), nil
	}))
	_ = p.Set("slice", interp.nativeMethod("slice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.NewArray(nil), nil
		}
		elems := arr.Elems()
		start := 0
		end := len(elems)
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				start = n
				if start < 0 {
					start += len(elems)
					if start < 0 {
						start = 0
					}
				}
				if start > len(elems) {
					start = len(elems)
				}
			}
		}
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok {
				end = n
				if end < 0 {
					end += len(elems)
				}
				if end > len(elems) {
					end = len(elems)
				}
			}
		}
		result := engine.NewArray(append([]engine.Value{}, elems[start:end]...))
		engine.SetProto(result, interp.arrayProto)
		return result, nil
	}))
	_ = p.Set("concat", interp.nativeMethod("concat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return engine.NewArray(nil), nil
		}
		var result []engine.Value
		result = append(result, arr.Elems()...)
		for _, a := range args {
			if aArr, ok := a.(*engine.ArrayValue); ok {
				result = append(result, aArr.Elems()...)
			} else {
				result = append(result, a)
			}
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("indexOf", interp.nativeMethod("indexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		elems := arr.Elems()
		target := args[0]
		for i, e := range elems {
			if strictEqual(e, target) {
				return engine.IntValue(i), nil
			}
		}
		return engine.IntValue(-1), nil
	}))
	_ = p.Set("includes", interp.nativeMethod("includes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Boolean(false), nil
		}
		elems := arr.Elems()
		target := args[0]
		for _, e := range elems {
			if strictEqual(e, target) {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))
	_ = p.Set("reverse", interp.nativeMethod("reverse", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok {
			return this, nil
		}
		elems := arr.Elems()
		for i, j := 0, len(elems)-1; i < j; i, j = i+1, j-1 {
			_ = arr.Set(strconv.Itoa(i), elems[j])
			_ = arr.Set(strconv.Itoa(j), elems[i])
		}
		return arr, nil
	}))
	_ = p.Set("forEach", interp.nativeMethod("forEach", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Undefined(), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		elems := arr.Elems()
		for i, e := range elems {
			_, _ = fn.callWith(engine.Undefined(), []engine.Value{e, engine.IntValue(i), arr})
		}
		return engine.Undefined(), nil
	}))
	_ = p.Set("map", interp.nativeMethod("map", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		elems := arr.Elems()
		result := make([]engine.Value, len(elems))
		for i, e := range elems {
			v, err := fn.callWith(engine.Undefined(), []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			result[i] = v
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("filter", interp.nativeMethod("filter", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		elems := arr.Elems()
		var result []engine.Value
		for i, e := range elems {
			v, err := fn.callWith(engine.Undefined(), []engine.Value{e, engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			b, _ := v.Bool()
			if b {
				result = append(result, e)
			}
		}
		out := engine.NewArray(result)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("reduce", interp.nativeMethod("reduce", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		arr, ok := this.(*engine.ArrayValue)
		if !ok || len(args) == 0 {
			return engine.Undefined(), nil
		}
		fn, err := asCallable(args[0])
		if err != nil {
			return nil, err
		}
		elems := arr.Elems()
		var acc engine.Value
		startIdx := 0
		if len(args) > 1 {
			acc = args[1]
		} else {
			if len(elems) == 0 {
				return nil, fmt.Errorf("%w: Reduce of empty array with no initial value", engine.ErrTypeError)
			}
			acc = elems[0]
			startIdx = 1
		}
		for i := startIdx; i < len(elems); i++ {
			v, err := fn.callWith(engine.Undefined(), []engine.Value{acc, elems[i], engine.IntValue(i), arr})
			if err != nil {
				return nil, err
			}
			acc = v
		}
		return acc, nil
	}))

	// ES5+ 基础方法与 ES2019/ES2022/ES2023 扩展（见 array_methods.go）。
	interp.setupArrayProtoExt()
}

// --- Array constructor ---

func (interp *Interpreter) setupArrayCtor() {
	ctor := interp.makeFunc("Array", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 1 && args[0].Type() == engine.TypeNumber {
			n, _ := args[0].Int()
			elems := make([]engine.Value, n)
			for i := range elems {
				elems[i] = engine.Undefined()
			}
			arr := engine.NewArray(elems)
			engine.SetProto(arr, interp.arrayProto)
			return arr, nil
		}
		arr := engine.NewArray(args)
		engine.SetProto(arr, interp.arrayProto)
		return arr, nil
	})
	_ = ctor.Set("isArray", interp.makeFunc("isArray", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		_, ok := args[0].(*engine.ArrayValue)
		return engine.Boolean(ok), nil
	}))
	interp.setupArrayCtorExt(ctor)
	_ = ctor.Set("prototype", interp.arrayProto)
	_ = interp.arrayProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("Array", ctor)
	interp.constructors["Array"] = ctor
}

// --- String.prototype ---

func (interp *Interpreter) setupStringProto() {
	p := interp.stringProto
	_ = p.Set("charAt", interp.nativeMethod("charAt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		idx := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				idx = n
			}
		}
		if idx < 0 || idx >= len(s) {
			return engine.Str(""), nil
		}
		return engine.Str(string(s[idx])), nil
	}))
	_ = p.Set("charCodeAt", interp.nativeMethod("charCodeAt", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		idx := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				idx = n
			}
		}
		if idx < 0 || idx >= len(s) {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(float64(s[idx])), nil
	}))
	_ = p.Set("toUpperCase", interp.nativeMethod("toUpperCase", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.ToUpper(this.String())), nil
	}))
	_ = p.Set("toLowerCase", interp.nativeMethod("toLowerCase", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.ToLower(this.String())), nil
	}))
	_ = p.Set("slice", interp.nativeMethod("slice", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		start, end := normalizeSliceArgs(len(s), args)
		if start >= end {
			return engine.Str(""), nil
		}
		return engine.Str(s[start:end]), nil
	}))
	_ = p.Set("substring", interp.nativeMethod("substring", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		n := len(s)
		start, end := normalizeSubstringArgs(n, args)
		return engine.Str(s[start:end]), nil
	}))
	_ = p.Set("indexOf", interp.nativeMethod("indexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		s := this.String()
		needle := args[0].String()
		from := 0
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok && n > 0 {
				from = n
			}
		}
		if from >= len(s) {
			return engine.IntValue(-1), nil
		}
		idx := strings.Index(s[from:], needle)
		if idx < 0 {
			return engine.IntValue(-1), nil
		}
		return engine.IntValue(idx + from), nil
	}))
	_ = p.Set("lastIndexOf", interp.nativeMethod("lastIndexOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(-1), nil
		}
		s := this.String()
		needle := args[0].String()
		return engine.IntValue(strings.LastIndex(s, needle)), nil
	}))
	_ = p.Set("includes", interp.nativeMethod("includes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(strings.Contains(this.String(), args[0].String())), nil
	}))
	_ = p.Set("startsWith", interp.nativeMethod("startsWith", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(strings.HasPrefix(this.String(), args[0].String())), nil
	}))
	_ = p.Set("endsWith", interp.nativeMethod("endsWith", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(strings.HasSuffix(this.String(), args[0].String())), nil
	}))
	_ = p.Set("split", interp.nativeMethod("split", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.NewArray([]engine.Value{engine.Str(s)}), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			limit := -1
			if len(args) > 1 {
				if n, ok := args[1].Float(); ok {
					limit = int(n)
				}
			}
			return regexpSplit(interp, r, s, limit)
		}
		sep := args[0].String()
		if sep == "" {
			elems := make([]engine.Value, 0, len(s))
			for _, ch := range s {
				elems = append(elems, engine.Str(string(ch)))
			}
			return engine.NewArray(elems), nil
		}
		parts := strings.Split(s, sep)
		elems := make([]engine.Value, len(parts))
		for i, part := range parts {
			elems[i] = engine.Str(part)
		}
		return engine.NewArray(elems), nil
	}))
	_ = p.Set("replace", interp.nativeMethod("replace", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Str(this.String()), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			return regexpReplace(interp, r, this.String(), args[1], r.compiled.Flags.Global)
		}
		return engine.Str(strings.Replace(this.String(), args[0].String(), args[1].String(), 1)), nil
	}))
	_ = p.Set("replaceAll", interp.nativeMethod("replaceAll", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Str(this.String()), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			if !r.compiled.Flags.Global {
				return engine.Undefined(), fmt.Errorf("%w: String.prototype.replaceAll called with a non-global RegExp", engine.ErrTypeError)
			}
			return regexpReplace(interp, r, this.String(), args[1], true)
		}
		return engine.Str(strings.ReplaceAll(this.String(), args[0].String(), args[1].String())), nil
	}))
	_ = p.Set("match", interp.nativeMethod("match", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.Null(), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			if !r.compiled.Flags.Global {
				return r.execString(s)
			}
			matches := r.compiled.MatchAllIndex(s)
			if len(matches) == 0 {
				return engine.Null(), nil
			}
			elems := make([]engine.Value, 0, len(matches))
			for _, m := range matches {
				elems = append(elems, engine.Str(s[m[0]:m[1]]))
			}
			out := engine.NewArray(elems)
			engine.SetProto(out, interp.arrayProto)
			return out, nil
		}
		// 非正则：按规范，若存在 Symbol.match 则调用之；此处简化为字符串查找。
		return engine.Null(), nil
	}))
	_ = p.Set("search", interp.nativeMethod("search", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.IntValue(-1), nil
		}
		if r, ok := args[0].(*RegexpValue); ok {
			m := r.compiled.MatchIndex(s)
			if m == nil {
				return engine.IntValue(-1), nil
			}
			return engine.IntValue(m[0]), nil
		}
		idx := strings.Index(s, args[0].String())
		return engine.IntValue(idx), nil
	}))
	_ = p.Set("matchAll", interp.nativeMethod("matchAll", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.Undefined(), fmt.Errorf("%w: String.prototype.matchAll requires a global RegExp", engine.ErrTypeError)
		}
		r, ok := args[0].(*RegexpValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: String.prototype.matchAll requires a RegExp", engine.ErrTypeError)
		}
		if !r.compiled.Flags.Global {
			return engine.Undefined(), fmt.Errorf("%w: String.prototype.matchAll called with a non-global RegExp", engine.ErrTypeError)
		}
		matches := r.compiled.MatchAllIndex(s)
		elems := make([]engine.Value, 0, len(matches))
		for _, m := range matches {
			v, err := r.execStringAt(s, m)
			if err != nil {
				return nil, err
			}
			elems = append(elems, v)
		}
		out := engine.NewArray(elems)
		engine.SetProto(out, interp.arrayProto)
		return out, nil
	}))
	_ = p.Set("trim", interp.nativeMethod("trim", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.TrimSpace(this.String())), nil
	}))
	_ = p.Set("trimStart", interp.nativeMethod("trimStart", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.TrimLeftFunc(this.String(), unicode.IsSpace)), nil
	}))
	_ = p.Set("trimEnd", interp.nativeMethod("trimEnd", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(strings.TrimRightFunc(this.String(), unicode.IsSpace)), nil
	}))
	_ = p.Set("repeat", interp.nativeMethod("repeat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		n, ok := args[0].Int()
		if !ok || n < 0 {
			return engine.Str(""), nil
		}
		return engine.Str(strings.Repeat(this.String(), n)), nil
	}))
	_ = p.Set("concat", interp.nativeMethod("concat", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var b strings.Builder
		b.WriteString(this.String())
		for _, a := range args {
			b.WriteString(a.String())
		}
		return engine.Str(b.String()), nil
	}))
	_ = p.Set("padStart", interp.nativeMethod("padStart", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 {
			return engine.Str(s), nil
		}
		targetLen, _ := args[0].Int()
		if targetLen <= len(s) {
			return engine.Str(s), nil
		}
		pad := " "
		if len(args) > 1 {
			pad = args[1].String()
		}
		if pad == "" {
			return engine.Str(s), nil
		}
		need := targetLen - len(s)
		rep := (need + len(pad) - 1) / len(pad)
		return engine.Str(strings.Repeat(pad, rep)[:need] + s), nil
	}))
	_ = p.Set("padEnd", interp.nativeMethod("padEnd", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 {
			return engine.Str(s), nil
		}
		targetLen, _ := args[0].Int()
		if targetLen <= len(s) {
			return engine.Str(s), nil
		}
		pad := " "
		if len(args) > 1 {
			pad = args[1].String()
		}
		if pad == "" {
			return engine.Str(s), nil
		}
		need := targetLen - len(s)
		rep := (need + len(pad) - 1) / len(pad)
		return engine.Str(s + strings.Repeat(pad, rep)[:need]), nil
	}))
	_ = p.Set("at", interp.nativeMethod("at", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		s := this.String()
		if len(args) == 0 {
			return engine.Str(string(s[0])), nil
		}
		idx, _ := args[0].Int()
		if idx < 0 {
			idx += len(s)
		}
		if idx < 0 || idx >= len(s) {
			return engine.Undefined(), nil
		}
		return engine.Str(string(s[idx])), nil
	}))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))
	// [Symbol.iterator]() 字符串默认迭代器（逐码点产出）。缺失时
	// `''[Symbol.iterator]()` 报 "undefined is not a function"。
	_ = p.Set(engine.SymbolIterator.SymbolKey(), interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return interp.currentVM.newStringIterator(this.String()), nil
	}))
}

// normalizeSliceArgs computes start/end indices for slice() (supports negatives).
func normalizeSliceArgs(n int, args []engine.Value) (int, int) {
	start := 0
	end := n
	if len(args) > 0 {
		if v, ok := args[0].Int(); ok {
			start = v
			if start < 0 {
				start += n
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
		}
	}
	if len(args) > 1 && !args[1].IsUndefined() {
		if v, ok := args[1].Int(); ok {
			end = v
			if end < 0 {
				end += n
			}
			if end < 0 {
				end = 0
			}
			if end > n {
				end = n
			}
		}
	}
	return start, end
}

// normalizeSubstringArgs computes start/end for substring() (swaps if start>end, no negatives).
func normalizeSubstringArgs(n int, args []engine.Value) (int, int) {
	start := 0
	end := n
	if len(args) > 0 {
		if v, ok := args[0].Int(); ok {
			start = v
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
		}
	}
	if len(args) > 1 && !args[1].IsUndefined() {
		if v, ok := args[1].Int(); ok {
			end = v
			if end < 0 {
				end = 0
			}
			if end > n {
				end = n
			}
		}
	}
	if start > end {
		start, end = end, start
	}
	return start, end
}

// --- String constructor ---

func (interp *Interpreter) setupStringCtor() {
	ctor := interp.makeFunc("String", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(args[0].String()), nil
	})
	_ = ctor.Set("fromCharCode", interp.makeFunc("fromCharCode", func(args []engine.Value) (engine.Value, error) {
		var b strings.Builder
		for _, a := range args {
			n, _ := a.Int()
			b.WriteRune(rune(n))
		}
		return engine.Str(b.String()), nil
	}))
	// String.raw`...`：按模板对象 .raw 数组拼接（raw 保留转义原文）。
	_ = ctor.Set("raw", interp.makeFunc("raw", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		tpl, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: String.raw: template is not an object", engine.ErrTypeError)
		}
		rawVal, err := tpl.Get("raw")
		if err != nil {
			return engine.Undefined(), err
		}
		rawObj, ok := rawVal.AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: String.raw: template.raw is not an object", engine.ErrTypeError)
		}
		lv, err := rawObj.Get("length")
		if err != nil {
			return engine.Undefined(), err
		}
		n, ok := lv.Int()
		if !ok || n < 0 {
			return engine.Undefined(), fmt.Errorf("%w: String.raw: invalid raw length", engine.ErrTypeError)
		}
		var b strings.Builder
		for i := 0; i < n; i++ {
			qv, err := rawObj.Get(strconv.Itoa(i))
			if err != nil {
				return engine.Undefined(), err
			}
			b.WriteString(qv.String())
			if i+1 < n {
				sub := engine.Str("")
				if i < len(args)-1 {
					sub = args[i+1]
				}
				b.WriteString(sub.String())
			}
		}
		return engine.Str(b.String()), nil
	}))
	_ = ctor.Set("prototype", interp.stringProto)
	_ = interp.stringProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("String", ctor)
	interp.constructors["String"] = ctor
}

// --- Number.prototype ---

func (interp *Interpreter) setupNumberProto() {
	p := interp.numberProto
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			radix, ok := args[0].Int()
			if ok && radix != 10 && radix >= 2 && radix <= 36 {
				n, _ := this.Float()
				return engine.Str(strconv.FormatFloat(n, 'f', -1, 64)), nil
			}
		}
		return engine.Str(this.String()), nil
	}))
	_ = p.Set("toFixed", interp.nativeMethod("toFixed", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		n, _ := this.Float()
		digits := 0
		if len(args) > 0 {
			if d, ok := args[0].Int(); ok {
				digits = d
			}
		}
		if digits < 0 {
			digits = 0
		}
		if digits > 20 {
			digits = 20
		}
		return engine.Str(strconv.FormatFloat(n, 'f', digits, 64)), nil
	}))
	_ = p.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		f, _ := this.Float()
		return engine.Number(f), nil
	}))
	_ = p.Set("toPrecision", interp.nativeMethod("toPrecision", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		n, _ := this.Float()
		if len(args) == 0 || args[0].IsUndefined() {
			return engine.Str(strconv.FormatFloat(n, 'g', -1, 64)), nil
		}
		prec, _ := args[0].Int()
		return engine.Str(strconv.FormatFloat(n, 'g', prec, 64)), nil
	}))
	_ = p.Set("toExponential", interp.nativeMethod("toExponential", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		n, _ := this.Float()
		digits := -1
		if len(args) > 0 && !args[0].IsUndefined() {
			digits, _ = args[0].Int()
		}
		return engine.Str(strconv.FormatFloat(n, 'e', digits, 64)), nil
	}))
}

// --- Number constructor ---

func (interp *Interpreter) setupNumberCtor() {
	ctor := interp.makeFunc("Number", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(0), nil
		}
		f, ok := args[0].Float()
		if !ok {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(f), nil
	})
	_ = ctor.Set("MAX_SAFE_INTEGER", engine.Number(9007199254740991))
	_ = ctor.Set("MIN_SAFE_INTEGER", engine.Number(-9007199254740991))
	_ = ctor.Set("MAX_VALUE", engine.Number(math.MaxFloat64))
	_ = ctor.Set("MIN_VALUE", engine.Number(5e-324))
	_ = ctor.Set("POSITIVE_INFINITY", engine.Number(math.Inf(1)))
	_ = ctor.Set("NEGATIVE_INFINITY", engine.Number(math.Inf(-1)))
	_ = ctor.Set("NaN", engine.Number(math.NaN()))
	_ = ctor.Set("EPSILON", engine.Number(2.220446049250313e-16))
	_ = ctor.Set("isFinite", interp.makeFunc("isFinite", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0)), nil
	}))
	_ = ctor.Set("isNaN", interp.makeFunc("isNaN", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(true), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(!ok || math.IsNaN(f)), nil
	}))
	_ = ctor.Set("isInteger", interp.makeFunc("isInteger", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == float64(int64(f))), nil
	}))
	_ = ctor.Set("prototype", interp.numberProto)
	_ = interp.numberProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("Number", ctor)
	interp.constructors["Number"] = ctor
}

// --- Boolean constructor ---

func (interp *Interpreter) setupBooleanCtor() {
	ctor := interp.makeFunc("Boolean", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		b, _ := args[0].Bool()
		return engine.Boolean(b), nil
	})
	_ = ctor.Set("prototype", interp.booleanProto)
	_ = interp.booleanProto.Set("constructor", ctor)
	_ = interp.globalObj.Set("Boolean", ctor)
	interp.constructors["Boolean"] = ctor
}

// --- Function.prototype ---

func (interp *Interpreter) setupFunctionProto() {
	p := interp.functionProto
	// Function.prototype 的原型是 Object.prototype，使函数对象可访问
	// hasOwnProperty/toString 等对象方法（ECMAScript 语义）。
	engine.SetProto(p, interp.objectProto)
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

	// 注册全局 Function 构造器。npm 包常访问 Function.prototype.toString /
	// bind 等（如 object-inspect 的 `Function.prototype.toString`）。
	ctor := interp.makeFunc("Function", func(args []engine.Value) (engine.Value, error) {
		// 简化实现：返回一个可调用占位函数（动态代码构造暂不支持）。
		return interp.makeFunc("anonymous", func(_ []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}), nil
	})
	_ = ctor.Set("prototype", p)
	_ = p.Set("constructor", ctor)
	_ = interp.globalObj.Set("Function", ctor)
	interp.constructors["Function"] = ctor
}

// --- Error.prototype ---

func (interp *Interpreter) setupErrorProto() {
	p := interp.errorProto
	_ = p.Set("name", engine.Str("Error"))
	_ = p.Set("message", engine.Str(""))
	_ = p.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		o, ok := this.AsObject()
		if !ok {
			return engine.Str("Error"), nil
		}
		nameVal, _ := o.Get("name")
		msgVal, _ := o.Get("message")
		name := "Error"
		if !nameVal.IsUndefined() {
			name = nameVal.String()
		}
		msg := ""
		if !msgVal.IsUndefined() {
			msg = msgVal.String()
		}
		if msg == "" {
			return engine.Str(name), nil
		}
		return engine.Str(name + ": " + msg), nil
	}))
}

func (interp *Interpreter) setupErrorCtors() {
	errorNames := []string{"Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError"}
	for _, name := range errorNames {
		proto := engine.NewObject()
		engine.SetProto(proto, interp.errorProto)
		_ = proto.Set("name", engine.Str(name))
		_ = proto.Set("message", engine.Str(""))
		ctorName := name
		ctor := interp.makeFunc(ctorName, func(args []engine.Value) (engine.Value, error) {
			errObj := engine.NewObject()
			engine.SetProto(errObj, proto)
			if len(args) > 0 {
				_ = errObj.Set("message", engine.Str(args[0].String()))
			}
			// Error cause（ES2022）：第二参数 options 的 cause 属性。
			// new Error("msg", { cause: originalError }) → err.cause
			if len(args) > 1 && !args[1].IsUndefined() && !args[1].IsNull() {
				if optObj, ok := args[1].AsObject(); ok {
					if cause, err := optObj.Get("cause"); err == nil && !cause.IsUndefined() {
						_ = errObj.Set("cause", cause)
					}
				}
			}
			_ = errObj.Set("name", engine.Str(ctorName))
			// V8 stack：为新建的 Error 捕获调用栈（Error.<stack> 属性）。
			interp.setErrorStack(errObj)
			return errObj, nil
		})
		_ = ctor.Set("prototype", proto)
		_ = proto.Set("constructor", ctor)
		_ = interp.globalObj.Set(name, ctor)
		interp.constructors[name] = ctor
	}
}

// --- Math ---

func (interp *Interpreter) setupMath() {
	m := engine.NewObject()
	mathFunc := func(name string, fn func(float64) float64) {
		_ = m.Set(name, interp.makeFunc(name, func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Number(math.NaN()), nil
			}
			f, _ := args[0].Float()
			return engine.Number(fn(f)), nil
		}))
	}
	mathFunc("abs", math.Abs)
	mathFunc("floor", math.Floor)
	mathFunc("ceil", math.Ceil)
	mathFunc("round", func(f float64) float64 { return math.RoundToEven(f) })
	mathFunc("sqrt", math.Sqrt)
	mathFunc("sin", math.Sin)
	mathFunc("cos", math.Cos)
	mathFunc("tan", math.Tan)
	mathFunc("log", math.Log)
	mathFunc("log2", math.Log2)
	mathFunc("log10", math.Log10)
	mathFunc("exp", math.Exp)
	_ = m.Set("PI", engine.Number(math.Pi))
	_ = m.Set("E", engine.Number(math.E))
	_ = m.Set("LN2", engine.Number(math.Ln2))
	_ = m.Set("LN10", engine.Number(math.Log(10)))
	_ = m.Set("SQRT2", engine.Number(math.Sqrt2))
	_ = m.Set("max", interp.makeFunc("max", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.Inf(-1)), nil
		}
		result := math.Inf(-1)
		for _, a := range args {
			f, _ := a.Float()
			if math.IsNaN(f) {
				return engine.Number(math.NaN()), nil
			}
			if f > result {
				result = f
			}
		}
		return engine.Number(result), nil
	}))
	_ = m.Set("min", interp.makeFunc("min", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.Inf(1)), nil
		}
		result := math.Inf(1)
		for _, a := range args {
			f, _ := a.Float()
			if math.IsNaN(f) {
				return engine.Number(math.NaN()), nil
			}
			if f < result {
				result = f
			}
		}
		return engine.Number(result), nil
	}))
	_ = m.Set("pow", interp.makeFunc("pow", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Number(math.NaN()), nil
		}
		x, _ := args[0].Float()
		y, _ := args[1].Float()
		return engine.Number(math.Pow(x, y)), nil
	}))
	_ = m.Set("random", interp.makeFunc("random", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(0.5), nil // simplified; no math/rand for determinism
	}))
	// Math 扩展方法与常量（见 math_methods.go）。
	interp.setupMathExt(m)
	_ = interp.globalObj.Set("Math", m)
}

// --- JSON ---

func (interp *Interpreter) setupJSON() {
	j := engine.NewObject()
	_ = j.Set("stringify", interp.makeFunc("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		s, err := jsonValueToJSON(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(s), nil
	}))
	_ = j.Set("parse", interp.makeFunc("parse", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("%w: JSON.parse requires an argument", engine.ErrSyntaxError)
		}
		var data interface{}
		if err := json.Unmarshal([]byte(args[0].String()), &data); err != nil {
			return nil, fmt.Errorf("%w: %v", engine.ErrSyntaxError, err)
		}
		return jsonToValue(data), nil
	}))
	_ = interp.globalObj.Set("JSON", j)
}

func jsonValueToJSON(v engine.Value) (string, error) {
	data, err := valueToJSON(v, make(map[engine.Object]bool))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(data)
	return string(b), err
}

// valueToJSON 将 JS 值转为可 JSON 序列化的 Go 结构。
// seen 记录当前递归路径上的对象，用于检测循环引用（命中返回 TypeError，
// 避免无限递归导致 Go 栈溢出崩溃）。对象在完成自身序列化后从 seen 移除，
// 因此共享但非循环的引用不会被误判。
func valueToJSON(v engine.Value, seen map[engine.Object]bool) (interface{}, error) {
	if v == nil || v.IsUndefined() {
		return nil, nil
	}
	switch v.Type() {
	case engine.TypeNull:
		return nil, nil
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return b, nil
	case engine.TypeNumber:
		f, _ := v.Float()
		return f, nil
	case engine.TypeString:
		return v.String(), nil
	case engine.TypeObject, engine.TypeFunction:
		if arr, ok := v.(*engine.ArrayValue); ok {
			o, _ := arr.AsObject()
			if seen[o] {
				return nil, fmt.Errorf("%w: Converting circular structure to JSON", engine.ErrTypeError)
			}
			seen[o] = true
			elems := arr.Elems()
			result := make([]interface{}, len(elems))
			for i, e := range elems {
				if e.IsUndefined() {
					result[i] = nil
					continue
				}
				r, err := valueToJSON(e, seen)
				if err != nil {
					return nil, err
				}
				result[i] = r
			}
			delete(seen, o)
			return result, nil
		}
		if o, ok := v.AsObject(); ok {
			if seen[o] {
				return nil, fmt.Errorf("%w: Converting circular structure to JSON", engine.ErrTypeError)
			}
			seen[o] = true
			m := make(map[string]interface{})
			for _, k := range o.Keys() {
				val, _ := o.Get(k)
				if val.IsFunction() || val.IsUndefined() {
					continue
				}
				r, err := valueToJSON(val, seen)
				if err != nil {
					return nil, err
				}
				m[k] = r
			}
			delete(seen, o)
			return m, nil
		}
	}
	return nil, nil
}

func jsonToValue(data interface{}) engine.Value {
	switch v := data.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(v)
	case float64:
		return engine.Number(v)
	case string:
		return engine.Str(v)
	case []interface{}:
		elems := make([]engine.Value, len(v))
		for i, e := range v {
			elems[i] = jsonToValue(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, val := range v {
			_ = obj.Set(k, jsonToValue(val))
		}
		return obj
	}
	return engine.Undefined()
}

// --- Global functions ---

func (interp *Interpreter) setupGlobalFuncs() {
	_ = interp.globalObj.Set("parseInt", interp.makeFunc("parseInt", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.NaN()), nil
		}
		s := strings.TrimSpace(args[0].String())
		radix := 10
		if len(args) > 1 {
			r, ok := args[1].Int()
			if ok && r != 0 {
				radix = r
			}
		}
		n, err := strconv.ParseInt(s, radix, 64)
		if err != nil {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(float64(n)), nil
	}))
	_ = interp.globalObj.Set("parseFloat", interp.makeFunc("parseFloat", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.NaN()), nil
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(args[0].String()), 64)
		if err != nil {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(f), nil
	}))
	_ = interp.globalObj.Set("isNaN", interp.makeFunc("isNaN", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(true), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(!ok || math.IsNaN(f)), nil
	}))
	_ = interp.globalObj.Set("isFinite", interp.makeFunc("isFinite", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		f, ok := args[0].Float()
		return engine.Boolean(ok && !math.IsNaN(f) && !math.IsInf(f, 0)), nil
	}))
	_ = interp.globalObj.Set("String", interp.constructors["String"])
}

// --- Symbol ---

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

// toValues converts []string to []Value.
func toValues(keys []string) []engine.Value {
	vals := make([]engine.Value, len(keys))
	for i, k := range keys {
		vals[i] = engine.Str(k)
	}
	return vals
}
