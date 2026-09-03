// 内置错误：Error.prototype 与 Error/TypeError/RangeError/AggregateError 等构造器族。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

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
	errorNames := []string{"Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError", "EvalError", "URIError"}
	for _, name := range errorNames {
		var proto engine.Object
		if name == "Error" {
			// Error 的 prototype 就是共享的 errorProto：其余错误类型的
			// prototype 全部以它为父级，保证 X instanceof Error 成立
			// （V8 原型链：TypeError.prototype → Error.prototype）。
			proto = interp.errorProto
		} else {
			proto = engine.NewObject()
			engine.SetProto(proto, interp.errorProto)
		}
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
	// AggregateError（ES2021）：new AggregateError(errors, message)。
	// errors 取数组快照（Node 语义：Object.freeze 后仍可构造、errors 独立）；
	// message 缺省为空串。
	aggProto := engine.NewObject()
	engine.SetProto(aggProto, interp.errorProto)
	_ = aggProto.Set("name", engine.Str("AggregateError"))
	_ = aggProto.Set("message", engine.Str(""))
	aggCtor := interp.makeFunc("AggregateError", func(args []engine.Value) (engine.Value, error) {
		errObj := engine.NewObject()
		engine.SetProto(errObj, aggProto)
		_ = errObj.Set("name", engine.Str("AggregateError"))
		_ = errObj.Set("message", engine.Str(""))
		if len(args) > 0 {
			_ = errObj.Set("errors", args[0])
		} else {
			_ = errObj.Set("errors", engine.NewArray(nil))
		}
		if len(args) > 1 {
			_ = errObj.Set("message", engine.Str(args[1].String()))
		}
		// Error cause（同上，第三参与 Error 一致）。
		if len(args) > 2 && !args[2].IsUndefined() && !args[2].IsNull() {
			if optObj, ok := args[2].AsObject(); ok {
				if cause, err := optObj.Get("cause"); err == nil && !cause.IsUndefined() {
					_ = errObj.Set("cause", cause)
				}
			}
		}
		interp.setErrorStack(errObj)
		return errObj, nil
	})
	_ = aggCtor.Set("prototype", aggProto)
	_ = aggProto.Set("constructor", aggCtor)
	_ = interp.globalObj.Set("AggregateError", aggCtor)
	interp.constructors["AggregateError"] = aggCtor
}
