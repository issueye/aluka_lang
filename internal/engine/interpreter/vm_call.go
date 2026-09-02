// VM 调用协议：普通调用/方法调用/new、快路径闭包调用、this 绑定与原生构造。

package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// doCall pops numArgs + callee and invokes the function with this=undefined.
// Returns the result value. For bytecode closures, it sets up a new frame and
// runs it to completion (recursive call to run()).
func (v *VM) doCall(numArgs int, thisVal engine.Value) (engine.Value, error) {
	// Stack: ... callee arg0 arg1 ... argN-1
	argStart := len(v.stack) - numArgs
	callee := v.stack[argStart-1]
	// 快速路径：简单字节码闭包调用（无 generator/async/varargs/arguments、
	// 同模块、实参不超过形参）→ 参数原地布置为新帧参数槽，跳过中间 args
	// slice 的分配与二次拷贝。
	if cl, ok := callee.(*vmClosure); ok && v.canFastCall(cl, numArgs) {
		return v.fastCallClosure(cl, thisVal, numArgs, argStart)
	}
	// Keep args slice (without callee).
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	// Pop callee + args.
	v.stack = v.stack[:argStart-1]
	return v.invoke(callee, thisVal, args, false)
}

// canFastCall 判断字节码闭包是否可走快速调用路径（fastCallClosure）：
// 非 generator/async（这些在 callClosure 前部已分支返回 Promise/生成器）、
// 与当前模块一致（跳过 module 切换）、非 varargs、不创建 arguments 对象
// （O-5：未引用 arguments）、实参不超过形参（多余实参需要弹栈丢弃）。
func (v *VM) canFastCall(cl *vmClosure, numArgs int) bool {
	tmpl := cl.tmpl
	if tmpl.IsGenerator || tmpl.IsAsync || tmpl.IsVarArgs {
		return false
	}
	if tmpl.NFESlot > 0 {
		// NFE 自引用槽由 callClosure 帧建立时写入，快速路径不初始化。
		return false
	}
	if tmpl.ArgumentsSlot >= 0 && !tmpl.NoArgumentsObject {
		return false
	}
	if cl.module != v.module || numArgs > tmpl.NumParams {
		return false
	}
	return true
}

// fastCallClosure 是 doCall 的快速路径：参数已经在 v.stack 上（栈布局
// `… callee arg0…argN-1`），把 callee 槽改写为 this 后参数即成为新帧的
// 参数槽（零拷贝），只补齐剩余局部变量的 undefined。跳过了 doCall 的
// args slice 分配、callClosure 的 module 保存/恢复与参数二次拷贝。
// 调用方保证满足 canFastCall 的全部条件。
func (v *VM) fastCallClosure(cl *vmClosure, thisVal engine.Value, numArgs, argStart int) (engine.Value, error) {
	v.bumpCall()
	tmpl := cl.tmpl
	if numArgs < 0 || argStart < 1 || argStart+numArgs > len(v.stack) {
		return engine.Undefined(), fmt.Errorf("aluka: invalid fast-call stack layout: args=%d argStart=%d len=%d", numArgs, argStart, len(v.stack))
	}
	if v.jitConfig.Mode != jit.Off && (cl.jitState == nil || !cl.jitState.rejected) {
		if result, ok, err := v.tryQuickCall(cl, thisVal, v.stack[argStart:argStart+numArgs]); err != nil {
			return engine.Undefined(), err
		} else if ok {
			v.stack = v.stack[:argStart-1]
			return result, nil
		}
	}
	// 异步上下文恢复：仅当事件循环外（无 JS 帧）调用 JS 闭包时，恢复该闭包
	// 创建时捕获的异步上下文（与 callClosure 一致；同步 JS 调用帧存在，跳过）。
	var savedAsyncCtx interface{}
	if len(v.frames) == 0 && AsyncContextRestore != nil {
		savedAsyncCtx = AsyncContextCapture()
		if cl.asyncCtx != nil {
			AsyncContextRestore(cl.asyncCtx)
		}
		defer func() {
			if AsyncContextRestore != nil {
				AsyncContextRestore(savedAsyncCtx)
			}
		}()
	}

	// callee 槽改写为 this（slot 0）；参数 arg0..argN-1 已在
	// [frameBase+1, frameBase+1+numArgs)，零拷贝。
	frameBase := argStart - 1
	v.stack[frameBase] = thisVal
	// 补齐局部变量到完整帧长度。参数原地复用时，不能只依赖调用方的
	// `frameBase+1+numArgs` 假设；绝对终点保证 slot [0, NumLocals) 都可访问。
	need := frameBase + tmpl.NumLocals - len(v.stack)
	if need > 0 {
		v.reserveUndefined(need)
	}
	// 按 tmpl.MaxStack 预留该帧操作数栈，使帧内 push 永不扩容。
	v.ensureFrameStack(tmpl)

	frame := vmFrame{tmpl: tmpl, base: frameBase, upvalues: cl.upvalues}
	v.frames = append(v.frames, frame)
	result, err := v.run()
	if err != nil {
		// Uncaught exception: run() does NOT pop the frame on error.
		v.closeUpvalues(frameBase)
		v.stack = v.stack[:frameBase]
		v.frames = v.frames[:len(v.frames)-1]
		return result, err
	}
	// run() 在正常返回时经 doReturn 弹帧并截栈。
	return result, nil
}

// doCallMethod handles obj.method(args) where the method name is a const index.
// Stack: ... receiver arg0 ... argN-1
func (v *VM) doCallMethod(numArgs, nameIdx, pc int) (engine.Value, error) {
	argStart := len(v.stack) - numArgs
	receiver := v.stack[argStart-1]

	name := v.cur().tmpl.Constants[nameIdx].String()
	// 方法调用内联缓存快速路径（O1-C4）：per-PC 槽命中（隐藏类 own
	// 方法、槽值未变）→ 直接 invoke，跳过属性解析链。
	var fn engine.Value
	if cached, hit := v.ic.CallCached(pc, receiver, name); hit {
		fn = cached
	} else {
		var err error
		fn, err = v.getProperty(receiver, name)
		if err != nil {
			return engine.Undefined(), err
		}
		v.ic.CallPut(pc, receiver, name, fn)
	}
	// 快速路径：字节码闭包方法 → 参数原地布置（复用 fastCallClosure）。
	if cl, ok := fn.(*vmClosure); ok && v.canFastCall(cl, numArgs) {
		return v.fastCallClosure(cl, receiver, numArgs, argStart)
	}
	// Keep args slice (without callee), pop receiver + args.
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	v.stack = v.stack[:argStart-1]
	return v.invoke(fn, receiver, args, false)
}

// toArrayValues converts a value (expected to be an array) into a slice of
// engine.Value, used to expand spread arguments at runtime.
func (v *VM) toArrayValues(val engine.Value) []engine.Value {
	if arr, ok := val.(*engine.ArrayValue); ok {
		return arr.Elems()
	}
	if obj, ok := val.AsObject(); ok {
		var out []engine.Value
		for _, k := range obj.Keys() {
			if pv, err := obj.Get(k); err == nil {
				out = append(out, pv)
			}
		}
		return out
	}
	return nil
}

// doNew pops numArgs + constructor and invokes it as a constructor.
func (v *VM) doNew(numArgs int) (engine.Value, error) {
	argStart := len(v.stack) - numArgs
	callee := v.stack[argStart-1]
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	// Pop callee + args.
	v.stack = v.stack[:argStart-1]
	return v.invoke(callee, engine.Undefined(), args, true)
}

// doCallThis pops numArgs + callee and calls/constructs it with this = the
// current frame's slot 0 (the `this` binding). Used for super.method() and
// super() inside class methods/constructors.
func (v *VM) doCallThis(numArgs int, asNew bool) (engine.Value, error) {
	argStart := len(v.stack) - numArgs
	callee := v.stack[argStart-1]
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	v.stack = v.stack[:argStart-1]
	thisVal := *v.local(0)
	if asNew {
		return v.constructThis(callee, thisVal, args)
	}
	return v.invoke(callee, thisVal, args, false)
}

// doCallThisArgs is the spread-args variant of doCallThis.
func (v *VM) doCallThisArgs(asNew bool) (engine.Value, error) {
	argsArr := v.pop()
	callee := v.pop()
	args := v.toArrayValues(argsArr)
	thisVal := *v.local(0)
	if asNew {
		return v.constructThis(callee, thisVal, args)
	}
	return v.invoke(callee, thisVal, args, false)
}

// constructThis calls callee as a constructor but with a pre-existing `this`
// (the current frame's slot 0). This implements super() semantics: the parent
// constructor runs on the same `this` object instead of creating a new one.
func (v *VM) constructThis(callee engine.Value, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	if cl, ok := callee.(*vmClosure); ok {
		return v.callClosureThis(cl, thisVal, args)
	}
	// 原生/非闭包父类（Error 等内建构造）：其构造返回全新实例对象。
	// ES 语义：super() 的构造结果即派生实例——替换当前帧 this 槽，使
	// 构造器后续的字段初始化/方法调用在该对象上进行（此前返回结果被
	// 丢弃，导致 `class X extends Error` 的实例 message/stack 等从未
	// 初始化——@babel/core 等广泛使用此模式）。
	var result engine.Value
	var err error
	if ac, ok := callee.(*Closure); ok {
		result, err = ac.construct(args)
	} else {
		result, err = v.invoke(callee, engine.Undefined(), args, true)
	}
	if err != nil {
		return engine.Undefined(), err
	}
	if result.IsObject() {
		// ES 语义：super() 的构造结果（原生父类新实例）的 [[Prototype]]
		// 必须是 newTarget.prototype（派生类），而非父类构造器默认的
		// callee.prototype——否则派生类的实例方法（类体中的方法）丢失。
		frame := v.cur()
		if frame.tmpl.NewTargetSlot >= 0 {
			nt := v.stack[frame.base+frame.tmpl.NewTargetSlot]
			if !nt.IsUndefined() {
				if proto, err := v.getProperty(nt, "prototype"); err == nil && !proto.IsUndefined() {
					if protoObj, ok := proto.(engine.Object); ok {
						if resObj, ok := result.(engine.Object); ok {
							engine.SetProto(resObj, protoObj)
						}
					}
				}
			}
		}
		*v.local(0) = result
	}
	return result, nil
}

// callClosureThis sets up a new VM frame for a bytecode closure, reusing the
// caller's `this` value (slot 0) instead of creating a new object. Used by
// super() to chain constructor calls on the same instance.
func (v *VM) callClosureThis(cl *vmClosure, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	savedModule := v.module
	if cl.module != nil {
		v.module = cl.module
	}
	tmpl := cl.tmpl
	frame := vmFrame{
		tmpl:     tmpl,
		base:     len(v.stack),
		upvalues: cl.upvalues,
	}
	v.reserveUndefined(tmpl.NumLocals)
	v.stack[frame.base] = thisVal // reuse the caller's this
	for i := 0; i < tmpl.NumParams && i < len(args); i++ {
		v.stack[frame.base+1+i] = args[i]
	}
	v.ensureFrameStack(tmpl)
	if tmpl.IsVarArgs {
		restSlot := frame.base + 1 + tmpl.NumParams
		var restElems []engine.Value
		if len(args) > tmpl.NumParams {
			restElems = append(restElems, args[tmpl.NumParams:]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, v.interp.arrayProto)
		v.stack[restSlot] = restArr
	}
	v.frames = append(v.frames, frame)
	result, err := v.run()
	v.module = savedModule
	// super() returns this if the parent ctor didn't return an object.
	if err == nil && !result.IsObject() {
		result = thisVal
	}
	return result, err
}

// invoke calls callee with the given this and args, handling both bytecode
// closures and native functions. When `asNew` is true, it constructs a new
// object for constructors. The callee and args must already be popped from
// the stack before calling invoke; the result is returned (not pushed).
func (v *VM) invoke(callee engine.Value, thisVal engine.Value, args []engine.Value, asNew bool) (engine.Value, error) {
	v.bumpCall() // 监控：函数调用计数（gated）
	// Bytecode closure: set up a new frame.
	if cl, ok := callee.(*vmClosure); ok {
		return v.callClosure(cl, thisVal, args, asNew)
	}
	// NativeMethod: receives this.
	if nm, ok := callee.(*NativeMethod); ok {
		if asNew {
			return nm.construct(args)
		}
		return nm.callWith(thisVal, args)
	}
	// AST Closure (from interpreter mode): use callWith/construct.
	if ac, ok := callee.(*Closure); ok {
		if asNew {
			return ac.construct(args)
		}
		return ac.callWith(thisVal, args)
	}
	// Generic engine.Function (e.g. makeFunc results like Object, Array).
	if f, ok := callee.AsFunction(); ok {
		if asNew {
			return v.constructNative(f, callee, args)
		}
		return f.Call(args)
	}
	if asNew {
		return engine.Undefined(), fmt.Errorf("%w: %s is not a constructor", engine.ErrTypeError, callee.Type())
	}
	return engine.Undefined(), fmt.Errorf("%w: %s is not a function", engine.ErrTypeError, callee.String())
}

// constructNative handles `new NativeFunc(args)` for Go-backed functions.
func (v *VM) constructNative(f engine.Function, callee engine.Value, args []engine.Value) (engine.Value, error) {
	// Special-case our makeCtor-style constructors that store the actual
	// callable under "__call__". These build their own `this` object.
	if fo, ok := callee.AsObject(); ok {
		if callProp, err := fo.Get("__call__"); err == nil && !callProp.IsUndefined() {
			if inner, ok := callProp.AsFunction(); ok {
				return inner.Call(args)
			}
		}
	}
	// Generic path: create a new object with proto from f.prototype, call f.
	newObj := engine.NewObject()
	engine.SetProto(newObj, v.interp.objectProto)
	if fo, ok := callee.AsObject(); ok {
		if proto, err := fo.Get("prototype"); err == nil && !proto.IsUndefined() {
			if protoObj, ok := proto.(engine.Object); ok {
				engine.SetProto(newObj, protoObj)
			}
		}
	}
	result, err := f.Call(args)
	if err != nil {
		return engine.Undefined(), err
	}
	if result.IsObject() {
		return result, nil
	}
	return newObj, nil
}

// callClosure sets up a new VM frame for a bytecode closure and runs it.
// The callee and args must already be popped from the stack.
func (v *VM) callClosure(cl *vmClosure, thisVal engine.Value, args []engine.Value, asNew bool) (engine.Value, error) {
	tmpl := cl.tmpl
	if v.jitConfig.Mode != jit.Off && !asNew && !tmpl.IsAsync && !tmpl.IsGenerator &&
		(cl.jitState == nil || !cl.jitState.rejected) {
		if result, ok, err := v.tryQuickCall(cl, thisVal, args); err != nil {
			return engine.Undefined(), err
		} else if ok {
			return result, nil
		}
	}
	// 异步上下文恢复：仅当事件循环外（无 JS 帧）调用 JS 闭包时，恢复该闭包
	// 创建时捕获的异步上下文（定时器/微任务/IO 回调等）。同步 JS 调用
	// （帧存在）不恢复——保持当前执行上下文，对应 Node 的 run()/enterWith
	// 语义。恢复逻辑需在 generator/async 提前返回之前执行（async 函数体在
	// start() 内立即运行到首个 await）。
	var savedAsyncCtx interface{}
	if len(v.frames) == 0 && AsyncContextRestore != nil {
		savedAsyncCtx = AsyncContextCapture()
		if cl.asyncCtx != nil {
			AsyncContextRestore(cl.asyncCtx)
		}
		defer func() {
			if AsyncContextRestore != nil {
				AsyncContextRestore(savedAsyncCtx)
			}
		}()
	}
	// Generator function: calling it returns a generator object rather than
	// executing the body. The body runs lazily on each .next() call.
	if tmpl.IsGenerator {
		if tmpl.IsAsync {
			ag := NewAsyncGeneratorValue(v, tmpl, cl.module, cl.upvalues, thisVal, args)
			ag.self = cl
			return ag, nil
		}
		gen := NewGeneratorValue(v, tmpl, cl.module, cl.upvalues, thisVal, args)
		gen.self = cl
		return gen, nil
	}
	// Async function: calling it returns a Promise. The body runs with
	// suspension at each OpAwait; the asyncRunner resolves/rejects the
	// Promise when the body completes or throws.
	if tmpl.IsAsync {
		ar := newAsyncRunner(v, tmpl, cl.module, cl.upvalues, thisVal, args)
		ar.self = cl
		return ar.start(), nil
	}
	// 切换到闭包定义时的 module，使函数体内 OpMakeClosure/OpMakeClass 的
	// fnIdx/classIdx 解析到正确的 Functions/Classes（跨模块闭包，如 CJS
	// getter 在另一模块上下文调用）。
	savedModule := v.module
	if cl.module != nil {
		v.module = cl.module
	}
	defer func() { v.module = savedModule }()
	if asNew {
		// Create a new object with proto from cl.prototype.
		newObj := engine.NewObject()
		engine.SetProto(newObj, v.interp.objectProto)
		if proto, err := cl.obj.Get("prototype"); err == nil && !proto.IsUndefined() {
			if protoObj, ok := proto.(engine.Object); ok {
				engine.SetProto(newObj, protoObj)
			}
		}
		thisVal = newObj
	}

	// Set up the new frame.
	frame := vmFrame{
		tmpl:     tmpl,
		base:     len(v.stack),
		upvalues: cl.upvalues,
		isCtor:   asNew,
	}
	// Reserve local slots: slot 0 = this, 1..N = params, rest = undefined.
	v.reserveUndefined(tmpl.NumLocals)
	v.ensureFrameStack(tmpl)
	v.stack[frame.base] = thisVal
	// 具名函数表达式（NFE）自引用槽：写入闭包自身（`const f = function
	// named() {...}` 的函数体内 `named` 引用）。
	if tmpl.NFESlot > 0 {
		v.stack[frame.base+tmpl.NFESlot] = cl
	}
	// new.target 槽位：new 调用时填入构造器函数（vmClosure 值本身，
	// 与全局绑定身份一致），普通调用保持 undefined。
	if tmpl.NewTargetSlot >= 0 && asNew {
		v.stack[frame.base+tmpl.NewTargetSlot] = cl
	}
	for i := 0; i < tmpl.NumParams && i < len(args); i++ {
		v.stack[frame.base+1+i] = args[i]
	}
	// Rest parameter: collect remaining args into an array at slot 1+NumParams.
	if tmpl.IsVarArgs {
		restSlot := frame.base + 1 + tmpl.NumParams
		var restElems []engine.Value
		if len(args) > tmpl.NumParams {
			restElems = append(restElems, args[tmpl.NumParams:]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, v.interp.arrayProto)
		v.stack[restSlot] = restArr
	}

	// Populate the `arguments` object for non-arrow functions (slot from
	// ArgumentsSlot). The arguments object is array-like: it has numeric
	// indices and a `length`. We bind it as a local so `arguments.length` /
	// `arguments[i]` work（get-intrinsic 等 npm 包依赖它）。
	if tmpl.ArgumentsSlot >= 0 && !tmpl.NoArgumentsObject {
		argsObj := engine.NewArray(append([]engine.Value{}, args...))
		engine.SetProto(argsObj, v.interp.arrayProto)
		_ = argsObj.Set("callee", cl.obj)
		v.stack[frame.base+tmpl.ArgumentsSlot] = argsObj
	}

	v.frames = append(v.frames, frame)
	result, err := v.run()
	if err != nil {
		// Uncaught exception: run() does NOT pop the frame on error.
		// Clean up so the caller's handleThrow sees its own frame on top.
		v.closeUpvalues(frame.base)
		v.stack = v.stack[:frame.base]
		v.frames = v.frames[:len(v.frames)-1]
		return result, err
	}
	// run() pops the frame on normal return.
	// Constructor semantics: if the function returns a non-object, the
	// newly-created `this` object is the result of `new`.
	if asNew && !result.IsObject() {
		result = thisVal
	}
	return result, nil
}

// doReturn is called from the run loop when a function returns. It pops the
// current frame, closes any open upvalues pointing into it, and truncates
// the stack. The return value is returned to the caller (NOT pushed — the
// caller's OpCall handler pushes it).
func (v *VM) doReturn(retVal engine.Value) engine.Value {
	frame := v.cur()
	// 构造器语义：返回非对象时以 this（slot 0）为 `new` 结果。必须从栈槽
	// 读取当前值——super() 调用原生父类构造（Error 等）时 constructThis
	// 会把父类构造的新实例写回 slot 0，此处快照 thisVal 已过期。
	if frame.isCtor && !retVal.IsObject() {
		retVal = v.stack[frame.base]
	}
	// 无开 upvalue 的普通函数（绝大多数）跳过 closeUpvalues 调用。
	if len(frame.openUpvalues) > 0 {
		// Close ALL open upvalues pointing into this frame's slots (locals +
		// operands). Using frame.base as the threshold ensures closures that
		// captured this frame's locals get a stable copy before the stack is
		// truncated; otherwise the upvalue would hold a dangling pointer.
		v.closeUpvalues(frame.base)
	}
	// Truncate stack to the frame's base (removes locals + operands).
	v.stack = v.stack[:frame.base]
	// Pop the frame.
	v.frames = v.frames[:len(v.frames)-1]
	return retVal
}

// InvokeFn 以指定 this 和参数调用函数值（供外部包调用 JS 函数，如 loadCJS 触发 getter）。
func (v *VM) InvokeFn(fn, this engine.Value, args []engine.Value) (engine.Value, error) {
	return v.invoke(fn, this, args, false)
}
