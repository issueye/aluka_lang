// Package globals 注册 Node.js / Web 兼容的全局对象与 Web API
// （console/process/Buffer/URL/fetch/crypto/timers/streams 等；aluka*.go 为
// Aluka（Bun 兼容）特有 API）。
//
// interface.go 提供 WebIDL 风格「构造器 + prototype」注册 helper：泛化 M8-11
// 的 SetProto 模式（crypto_web.go 曾手写的构造器注入），统一描述符语义，供
// 工作流 B（全局实例原型链规范）迁移 crypto/performance/navigator 等实例。
package globals

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// WebInterface 声明一个 WebIDL 风格接口的注册参数。
type WebInterface struct {
	// Name 构造器名，同时以该名注册到全局（如 "Crypto"）。
	Name string
	// Tag 为 Symbol.toStringTag 值；空串表示该接口无 tag（如 Node 的 Navigator
	// 实例 Object.prototype.toString.call(navigator) 为 "[object Object]"）。
	Tag string
	// Base 为 prototype 的 [[Prototype]]；nil 表示 %Object.prototype%。
	// 多级接口用它在中间插入父接口原型（如 Performance.prototype → EventTarget.prototype）。
	Base engine.Object
	// Ctor 为自定义构造器实现；nil 表示 Illegal constructor（new 抛 TypeError）。
	Ctor engine.Func
}

// RegisterInterface 按 Node 22 WebIDL 语义注册构造器与 prototype，返回 (ctor, proto)：
//   - ctor 以 spec.Name 注册为全局；
//   - ctor.prototype → proto，描述符 {writable:false, enumerable:false, configurable:false}；
//   - proto.constructor → ctor，描述符 {writable:true, enumerable:false, configurable:true}；
//   - proto.[[Prototype]] → spec.Base（缺省 ctx 的 %Object.prototype%）；
//   - proto[Symbol.toStringTag] → spec.Tag（非可枚举值属性，空串跳过）。
//
// 实例方法与访问器由调用方挂到返回的 proto 上：proto.Set 走默认标志
// {writable:true, enumerable:true, configurable:true}，engine.SetAccessor 同，
// 均与 WebIDL 接口成员一致。实例本体经 engine.SetProto(instance, proto) 接入。
func RegisterInterface(ctx engine.Context, spec WebInterface) (engine.Function, engine.Object, error) {
	if spec.Name == "" {
		return nil, nil, fmt.Errorf("RegisterInterface: empty name")
	}
	fn := spec.Ctor
	if fn == nil {
		fn = func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), fmt.Errorf("%w: Illegal constructor", engine.ErrTypeError)
		}
	}
	ctor := engine.NewFunction(spec.Name, fn)
	proto := engine.NewObject()

	// proto.constructor：不可枚举（WebIDL constructor 标志）。
	_ = proto.Set("constructor", ctor)
	if err := engine.DefineOwnProperty(proto, "constructor", engine.Descriptor{
		HasEnumerable: true, Enumerable: false,
	}); err != nil {
		return nil, nil, err
	}

	// ctor.prototype → proto，不可写/不可枚举/不可配置。
	if co, ok := ctor.AsObject(); ok {
		_ = co.Set("prototype", proto)
		if err := engine.DefineOwnProperty(co, "prototype", engine.Descriptor{
			HasWritable: true, Writable: false,
			HasEnumerable: true, Enumerable: false,
			HasConfigurable: true, Configurable: false,
		}); err != nil {
			return nil, nil, err
		}
	}

	// 原型链接到 %Object.prototype%（或显式父接口原型）。
	base := spec.Base
	if base == nil {
		base = ctx.ObjectPrototype()
	}
	engine.SetProto(proto, base)

	// Symbol.toStringTag：非可枚举值属性。
	if spec.Tag != "" {
		key := engine.SymbolToStringTag.SymbolKey()
		_ = proto.Set(key, engine.Str(spec.Tag))
		if err := engine.DefineOwnProperty(proto, key, engine.Descriptor{
			HasEnumerable: true, Enumerable: false,
		}); err != nil {
			return nil, nil, err
		}
	}

	if err := ctx.Global().Set(spec.Name, ctor); err != nil {
		return nil, nil, err
	}
	return ctor, proto, nil
}

// nodeEnumerableGlobals 是 Node 22 中 globalThis 上可枚举的属性白名单
// （web 类全局）。其余全局（含 ES 内建与 console/process/Buffer 等）
// 一律不可枚举。
var nodeEnumerableGlobals = map[string]bool{
	"atob": true, "btoa": true,
	"clearImmediate": true, "clearInterval": true, "clearTimeout": true,
	"crypto": true, "fetch": true, "global": true,
	"navigator": true, "performance": true, "queueMicrotask": true,
	"setImmediate": true, "setInterval": true, "setTimeout": true,
	"structuredClone": true,
}

// SweepGlobalEnumerability 把 globalThis 上白名单之外的属性统一改为
// 不可枚举（对齐 Node 22）。在全部全局对象注册完成后调用一次；engine 侧
// 内建已在 setupBuiltins 末尾清扫，本调用覆盖 globals 包后注册的部分。
func SweepGlobalEnumerability(ctx engine.Context) {
	for _, k := range ctx.Global().Keys() {
		if !nodeEnumerableGlobals[k] {
			_ = engine.DefineOwnProperty(ctx.Global(), k, engine.Descriptor{HasEnumerable: true, Enumerable: false})
		}
	}
}
