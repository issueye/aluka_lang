package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

type asyncGenRequestKind int

const (
	asyncGenNext asyncGenRequestKind = iota
	asyncGenReturn
	asyncGenThrow
)

type asyncGenRequest struct {
	kind    asyncGenRequestKind
	value   engine.Value
	promise *PromiseValue
}

// AsyncGeneratorValue combines generator suspension with async/await. Each
// iterator operation returns a Promise that settles at the next yield, return,
// or uncaught throw.
type AsyncGeneratorValue struct {
	obj      engine.Object
	vm       *VM
	tmpl     *bytecode.FuncTemplate
	module   *bytecode.Module
	upvalues []*upvalue
	thisVal  engine.Value
	args     []engine.Value
	self     *vmClosure // NFE 自引用（具名函数表达式；非 NFE 为 nil）

	savedStack    []engine.Value
	savedPC       int
	savedTryStack []*vmTryHandler
	savedBase     int
	hasState      bool
	closedUps     []upvalueClose

	state    genState
	current  *asyncGenRequest
	requests []*asyncGenRequest
}

func NewAsyncGeneratorValue(vm *VM, tmpl *bytecode.FuncTemplate, module *bytecode.Module, upvalues []*upvalue, thisVal engine.Value, args []engine.Value) *AsyncGeneratorValue {
	gen := &AsyncGeneratorValue{
		vm:       vm,
		tmpl:     tmpl,
		module:   module,
		upvalues: upvalues,
		thisVal:  thisVal,
		args:     args,
		state:    genSuspended,
	}
	obj := engine.NewObject()
	engine.SetProto(obj, vm.interp.objectProto)
	gen.obj = obj

	_ = obj.Set("next", vm.interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return gen.enqueue(asyncGenNext, firstOrUndefined(args)), nil
	}))
	_ = obj.Set("return", vm.interp.nativeMethod("return", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return gen.enqueue(asyncGenReturn, firstOrUndefined(args)), nil
	}))
	_ = obj.Set("throw", vm.interp.nativeMethod("throw", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return gen.enqueue(asyncGenThrow, firstOrUndefined(args)), nil
	}))
	_ = obj.Set(engine.SymbolAsyncIterator.SymbolKey(), vm.interp.nativeMethod("[Symbol.asyncIterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return gen, nil
	}))

	return gen
}

func firstOrUndefined(args []engine.Value) engine.Value {
	if len(args) > 0 {
		return args[0]
	}
	return engine.Undefined()
}

func (g *AsyncGeneratorValue) enqueue(kind asyncGenRequestKind, value engine.Value) *PromiseValue {
	p := NewPromiseValue(g.vm.interp)
	g.requests = append(g.requests, &asyncGenRequest{kind: kind, value: value, promise: p})
	g.pump()
	return p
}

func (g *AsyncGeneratorValue) pump() {
	if g.current != nil || len(g.requests) == 0 {
		return
	}

	req := g.requests[0]
	g.requests = g.requests[1:]
	if g.state == genCompleted {
		if req.kind == asyncGenThrow {
			req.promise.Reject(req.value)
		} else {
			value := engine.Undefined()
			if req.kind == asyncGenReturn {
				value = req.value
			}
			req.promise.Fulfill(g.iterResult(value, true))
		}
		g.pump()
		return
	}

	g.current = req
	switch req.kind {
	case asyncGenNext:
		g.state = genExecuting
		g.runStep(req.value, false)
	case asyncGenReturn:
		g.complete(req.value)
	case asyncGenThrow:
		if !g.hasState {
			g.fail(req.value)
			return
		}
		g.state = genExecuting
		g.runStep(req.value, true)
	}
}

func (g *AsyncGeneratorValue) runStep(resumeVal engine.Value, isThrow bool) {
	savedModule := g.vm.module
	if g.module != nil {
		g.vm.module = g.module
	}
	defer func() { g.vm.module = savedModule }()

	if !g.hasState {
		g.setupFrame()
		g.hasState = true
	} else {
		g.restoreFrame()
		if isThrow {
			result, err := g.vm.handleThrow(resumeVal)
			g.processResult(result, err)
			return
		}
		// resume 路径在 run 主循环之外，用 pushSafe。
		g.vm.pushSafe(resumeVal)
		// resume 重分配 base（挂起后栈被截断），须重新按 MaxStack 预留操作数栈。
		g.vm.ensureFrameStack(g.tmpl)
	}

	result, err := g.vm.run()
	g.processResult(result, err)
}

func (g *AsyncGeneratorValue) processResult(result engine.Value, err error) {
	if err != nil {
		switch signal := err.(type) {
		case *awaitSignal:
			g.suspendAtAwait(signal.value)
			return
		case *yieldSignal:
			g.suspendAtYield(signal.value)
			return
		default:
			g.cleanupFrame()
			g.fail(extractThrowValue(err, g.vm.interp))
			return
		}
	}
	g.complete(result)
}

func (g *AsyncGeneratorValue) setupFrame() {
	frame := vmFrame{tmpl: g.tmpl, base: len(g.vm.stack), upvalues: g.upvalues}
	g.vm.reserveUndefined(g.tmpl.NumLocals)
	g.vm.ensureFrameStack(g.tmpl)
	g.vm.stack[frame.base] = g.thisVal
	if g.tmpl.NFESlot > 0 && g.self != nil {
		g.vm.stack[frame.base+g.tmpl.NFESlot] = g.self
	}
	for i := 0; i < g.tmpl.NumParams && i < len(g.args); i++ {
		g.vm.stack[frame.base+1+i] = g.args[i]
	}
	if g.tmpl.IsVarArgs {
		restSlot := frame.base + 1 + g.tmpl.NumParams
		var restElems []engine.Value
		if len(g.args) > g.tmpl.NumParams {
			restElems = append(restElems, g.args[g.tmpl.NumParams:]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, g.vm.interp.arrayProto)
		g.vm.stack[restSlot] = restArr
	}
	g.vm.frames = append(g.vm.frames, frame)
}

func (g *AsyncGeneratorValue) saveFrame() {
	frame := g.vm.cur()
	g.savedStack = append(g.savedStack[:0], g.vm.stack[frame.base:]...)
	g.savedBase = frame.base
	g.savedPC = frame.pc
	g.savedTryStack = frame.tryStack
	g.closedUps = g.vm.closeUpvalues(frame.base)
	g.vm.stack = g.vm.stack[:frame.base]
	g.vm.frames = g.vm.frames[:len(g.vm.frames)-1]
}

func (g *AsyncGeneratorValue) restoreFrame() {
	frame := vmFrame{
		tmpl:     g.tmpl,
		base:     g.savedBase,
		upvalues: g.upvalues,
		pc:       g.savedPC,
		tryStack: g.savedTryStack,
	}
	if len(g.vm.stack) > g.savedBase {
		g.vm.stack = g.vm.stack[:g.savedBase]
	}
	for len(g.vm.stack) < g.savedBase {
		g.vm.stack = append(g.vm.stack, engine.Undefined())
	}
	g.vm.appendValues(g.savedStack)
	g.savedStack = nil
	g.savedTryStack = nil
	g.vm.reopenUpvalues(&frame, g.closedUps, g.savedBase)
	g.closedUps = nil
	g.vm.frames = append(g.vm.frames, frame)
}

func (g *AsyncGeneratorValue) cleanupFrame() {
	if len(g.vm.frames) == 0 {
		return
	}
	frame := g.vm.cur()
	if frame.tmpl != g.tmpl {
		return
	}
	g.vm.closeUpvalues(frame.base)
	g.vm.stack = g.vm.stack[:frame.base]
	g.vm.frames = g.vm.frames[:len(g.vm.frames)-1]
}

func (g *AsyncGeneratorValue) suspendAtAwait(value engine.Value) {
	g.saveFrame()
	awaited := promiseResolve(g.vm.interp, value)
	onFulfilled := g.vm.interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		g.runStep(firstOrUndefined(args), false)
		return engine.Undefined(), nil
	})
	onRejected := g.vm.interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		g.runStep(firstOrUndefined(args), true)
		return engine.Undefined(), nil
	})
	awaited.Then(onFulfilled, onRejected)
}

func (g *AsyncGeneratorValue) suspendAtYield(value engine.Value) {
	g.saveFrame()
	g.state = genSuspended
	req := g.current
	g.current = nil
	req.promise.Fulfill(g.iterResult(value, false))
	g.pump()
}

func (g *AsyncGeneratorValue) complete(value engine.Value) {
	g.state = genCompleted
	g.hasState = false
	g.savedStack = nil
	g.savedTryStack = nil
	g.closedUps = nil
	req := g.current
	g.current = nil
	if req != nil {
		req.promise.Fulfill(g.iterResult(value, true))
	}
	g.pump()
}

func (g *AsyncGeneratorValue) fail(reason engine.Value) {
	g.state = genCompleted
	g.hasState = false
	g.savedStack = nil
	g.savedTryStack = nil
	g.closedUps = nil
	req := g.current
	g.current = nil
	if req != nil {
		req.promise.Reject(reason)
	}
	g.pump()
}

func (g *AsyncGeneratorValue) iterResult(value engine.Value, done bool) engine.Value {
	result := engine.NewObject()
	engine.SetProto(result, g.vm.interp.objectProto)
	_ = result.Set("value", value)
	_ = result.Set("done", engine.Boolean(done))
	return result
}

func (g *AsyncGeneratorValue) Type() engine.ValueType { return engine.TypeObject }
func (g *AsyncGeneratorValue) String() string         { return "[object AsyncGenerator]" }
func (g *AsyncGeneratorValue) Int() (int, bool)       { return 0, false }
func (g *AsyncGeneratorValue) Float() (float64, bool) { return 0, false }
func (g *AsyncGeneratorValue) Bool() (bool, bool)     { return true, true }
func (g *AsyncGeneratorValue) IsUndefined() bool      { return false }
func (g *AsyncGeneratorValue) IsNull() bool           { return false }
func (g *AsyncGeneratorValue) IsObject() bool         { return true }
func (g *AsyncGeneratorValue) IsFunction() bool       { return false }
func (g *AsyncGeneratorValue) AsObject() (engine.Object, bool) {
	return g, true
}
func (g *AsyncGeneratorValue) AsFunction() (engine.Function, bool) { return nil, false }
func (g *AsyncGeneratorValue) Get(key string) (engine.Value, error) {
	return g.obj.Get(key)
}
func (g *AsyncGeneratorValue) Set(key string, val engine.Value) error {
	return g.obj.Set(key, val)
}
func (g *AsyncGeneratorValue) Keys() []string         { return g.obj.Keys() }
func (g *AsyncGeneratorValue) Delete(key string) bool { return g.obj.Delete(key) }

var _ engine.Object = (*AsyncGeneratorValue)(nil)
