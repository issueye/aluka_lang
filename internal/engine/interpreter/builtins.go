package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// 内置对象的装配入口。各命名空间的具体实现按类型分文件：
// builtins_object.go / builtins_array.go / builtins_string.go /
// builtins_number.go（含 Boolean/BigInt/Math）/ builtins_json.go /
// builtins_error.go / builtins_function.go（含 Symbol）/ builtins_global.go。

// setupBuiltins initializes all built-in prototypes, constructors, and global functions.
func (interp *Interpreter) setupBuiltins() {
	// Create prototype objects
	interp.objectProto = engine.NewObject()
	interp.functionProto = engine.NewObject()
	interp.arrayProto = engine.NewObject()
	interp.stringProto = engine.NewObject()
	interp.numberProto = engine.NewObject()
	interp.booleanProto = engine.NewObject()
	interp.bigintProto = engine.NewObject()
	interp.errorProto = engine.NewObject()

	// Object.prototype methods
	interp.setupObjectProto()
	// Array.prototype methods
	interp.setupArrayProto()
	// String.prototype methods
	interp.setupStringProto()
	// Number.prototype methods
	interp.setupNumberProto()
	interp.setupBigIntProto()
	// Function.prototype methods
	interp.setupFunctionProto()
	// Error.prototype methods
	interp.setupErrorProto()
	// 内置 prototype 统一接 %Object.prototype%（ECMAScript 语义：Array/
	// String/Number/Boolean/BigInt/Error 的实例都应能访问 Object.prototype
	// 方法如 hasOwnProperty；此前仅 functionProto 接了链）。
	for _, p := range []engine.Object{
		interp.arrayProto, interp.stringProto, interp.numberProto,
		interp.booleanProto, interp.bigintProto, interp.errorProto,
	} {
		engine.SetProto(p, interp.objectProto)
	}

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
	// TypedArray / ArrayBuffer / DataView（Pi 兼容：typebox 等依赖）
	interp.setupTypedArrays()
	// BigInt 全局构造器（BigInt(x) 转换；字面量 123n 已在 lexer/compiler 支持）
	interp.setupBigIntGlobal()

	// Global functions
	interp.setupGlobalFuncs()
	interp.setupFinalizationRegistry()

	// 内建枚举性清扫：所有内建构造器静态方法与其 prototype 成员统一为
	// 不可枚举（ES 惯例）。for-in 走原型链后若不清扫，内建方法会泄漏进
	// 所有数组/字符串等的 for-in 结果（Node 中 for (k in []) 仅得索引）。
	interp.sweepBuiltinEnumerability()
}

// sweepBuiltinEnumerability 把 constructors 表中全部内建构造器与其原型对象
// 的（可枚举）成员重定义为不可枚举。WebIDL 接口（globals 包经
// RegisterInterface 注册）不在此表，方法保持 WebIDL 的可枚举语义。
// 同时收口 globalThis：内建全局键不可枚举（Node 仅 web 类全局可枚举）、
// 原型链接 %Object.prototype%、补 Symbol.toStringTag 'global'。
func (interp *Interpreter) sweepBuiltinEnumerability() {
	swept := make(map[engine.Object]bool)
	sweep := func(o engine.Object) {
		if o == nil || swept[o] {
			return
		}
		swept[o] = true
		for _, k := range o.Keys() {
			_ = engine.DefineOwnProperty(o, k, engine.Descriptor{HasEnumerable: true, Enumerable: false})
		}
	}
	// 内建命名空间（非构造器）：Math/JSON/Reflect 成员同样不可枚举，且
	// 原型链接 %Object.prototype%（此前为 null 原型）并补 Symbol.toStringTag。
	for _, name := range []string{"Math", "JSON", "Reflect"} {
		if v, err := interp.globalObj.Get(name); err == nil {
			if o, ok := v.AsObject(); ok {
				engine.SetProto(o, interp.objectProto)
				sweep(o)
				tagKey := engine.SymbolToStringTag.SymbolKey()
				_ = o.Set(tagKey, engine.Str(name))
				_ = engine.DefineOwnProperty(o, tagKey, engine.Descriptor{HasEnumerable: true, Enumerable: false})
			}
		}
	}
	for _, ctor := range interp.constructors {
		sweep(ctor)
		if pv, err := ctor.Get("prototype"); err == nil {
			if po, ok := pv.AsObject(); ok {
				sweep(po)
			}
		}
	}
	// globalThis 自有键清扫：engine 侧注册的内建一律不可枚举；例外为 Node
	// 22 同样可枚举的 engine 注册项。globals 包（crypto/fetch/timers 等）
	// 在 setupBuiltins 之后注册，不受影响，保持可枚举（对齐 Node）。
	for _, k := range interp.globalObj.Keys() {
		if !globalEnumerableAllowlist[k] {
			_ = engine.DefineOwnProperty(interp.globalObj, k, engine.Descriptor{HasEnumerable: true, Enumerable: false})
		}
	}
	// globalThis [[Prototype]] = %Object.prototype%（hasOwnProperty 等经原型
	// 链解析，Node 语义）+ Symbol.toStringTag 'global'。
	engine.SetProto(interp.globalObj, interp.objectProto)
	tagKey := engine.SymbolToStringTag.SymbolKey()
	_ = interp.globalObj.Set(tagKey, engine.Str("global"))
	_ = engine.DefineOwnProperty(interp.globalObj, tagKey, engine.Descriptor{HasEnumerable: true, Enumerable: false})
}

// globalEnumerableAllowlist 是 Node 22 中可枚举、且由 engine（而非 globals
// 包）注册的 globalThis 属性。其余 engine 注册项一律不可枚举。
var globalEnumerableAllowlist = map[string]bool{
	"structuredClone": true,
	"queueMicrotask":  true,
	"global":          true,
}

// --- BigInt.prototype ---

// makeFunc creates a Go-backed function and sets its prototype to Function.prototype.
func (interp *Interpreter) makeFunc(name string, fn engine.Func) engine.Object {
	f := engine.NewFunction(name, fn)
	engine.SetProto(f, interp.functionProto)
	return f.(engine.Object)
}

// nativeMethod creates a built-in method that receives `this`.
func (interp *Interpreter) nativeMethod(name string, fn func(this engine.Value, args []engine.Value) (engine.Value, error)) engine.Value {
	m := NewNativeMethod(name, fn)
	engine.SetProto(m, interp.functionProto)
	return m
}
