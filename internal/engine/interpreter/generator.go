package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// yieldSignal is a sentinel error returned by run() when OpYield is executed.
// It signals that the generator suspended execution and the yielded value
// should be returned to the caller of .next().
type yieldSignal struct {
	value engine.Value
}

func (y *yieldSignal) Error() string { return "yield" }

// GeneratorValue is a generator object created by calling a generator function.
// It implements the iterator protocol (next/return/throw) and can be suspended
// and resumed by saving/restoring the VM frame state.
type GeneratorValue struct {
	obj      engine.Object
	vm       *VM
	tmpl     *bytecode.FuncTemplate
	upvalues []*upvalue
	thisVal  engine.Value
	args     []engine.Value

	// Saved state when suspended.
	savedStack []engine.Value // stack segment [base..top) at suspend time
	savedPC    int
	hasState   bool // true once the generator has been started

	// Generator state machine.
	state genState
}

type genState int

const (
	genSuspended genState = iota
	genExecuting
	genCompleted
)

// NewGeneratorValue creates a generator object from a generator function template.
func NewGeneratorValue(vm *VM, tmpl *bytecode.FuncTemplate, upvalues []*upvalue, thisVal engine.Value, args []engine.Value) *GeneratorValue {
	gen := &GeneratorValue{
		vm:       vm,
		tmpl:     tmpl,
		upvalues: upvalues,
		thisVal:  thisVal,
		args:     args,
		state:    genSuspended,
	}
	obj := engine.NewObject()
	engine.SetProto(obj, vm.interp.objectProto)
	gen.obj = obj

	// Install .next / .return / .throw methods.
	nextMethod := vm.interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var sendVal engine.Value
		if len(args) > 0 {
			sendVal = args[0]
		} else {
			sendVal = engine.Undefined()
		}
		return gen.next(sendVal)
	})
	_ = obj.Set("next", nextMethod)

	returnMethod := vm.interp.nativeMethod("return", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var retVal engine.Value
		if len(args) > 0 {
			retVal = args[0]
		} else {
			retVal = engine.Undefined()
		}
		return gen.returnVal(retVal)
	})
	_ = obj.Set("return", returnMethod)

	throwMethod := vm.interp.nativeMethod("throw", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var throwVal engine.Value
		if len(args) > 0 {
			throwVal = args[0]
		} else {
			throwVal = engine.Undefined()
		}
		return gen.throwVal(throwVal)
	})
	_ = obj.Set("throw", throwMethod)

	// Generators are iterable (they are their own iterator).
	_ = obj.Set(engine.SymbolIterator.SymbolKey(), vm.interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return gen, nil
	}))

	return gen
}

// next resumes the generator, optionally sending a value into the yield expression.
func (g *GeneratorValue) next(sendVal engine.Value) (engine.Value, error) {
	if g.state == genCompleted {
		return g.iterResult(engine.Undefined(), true), nil
	}
	if g.state == genExecuting {
		return engine.Undefined(), fmt.Errorf("%w: Generator is already running", engine.ErrTypeError)
	}

	result, err := g.resume(sendVal)
	if err != nil {
		if ys, ok := err.(*yieldSignal); ok {
			// Generator yielded — return {value, done: false}.
			return g.iterResult(ys.value, false), nil
		}
		// Propagate real errors.
		g.state = genCompleted
		return engine.Undefined(), err
	}
	// Generator returned normally — {value: result, done: true}.
	g.state = genCompleted
	return g.iterResult(result, true), nil
}

// returnVal forces the generator to complete and returns {value, done: true}.
func (g *GeneratorValue) returnVal(retVal engine.Value) (engine.Value, error) {
	g.state = genCompleted
	g.hasState = false
	return g.iterResult(retVal, true), nil
}

// throwVal throws an exception into the generator's current yield point.
func (g *GeneratorValue) throwVal(throwVal engine.Value) (engine.Value, error) {
	if g.state == genCompleted {
		return engine.Undefined(), &jsThrow{val: throwVal}
	}
	if g.state == genExecuting {
		return engine.Undefined(), fmt.Errorf("%w: Generator is already running", engine.ErrTypeError)
	}
	// For MVP: treat throw as an error that completes the generator.
	g.state = genCompleted
	g.hasState = false
	return engine.Undefined(), &jsThrow{val: throwVal}
}

// resume restores the saved frame (or creates a new one on first call) and
// runs the VM until the next yield or return.
func (g *GeneratorValue) resume(sendVal engine.Value) (engine.Value, error) {
	g.state = genExecuting

	if !g.hasState {
		// First call: set up a fresh frame.
		frame := vmFrame{
			tmpl:     g.tmpl,
			base:     len(g.vm.stack),
			upvalues: g.upvalues,
		}
		g.vm.reserveUndefined(g.tmpl.NumLocals)
		g.vm.stack[frame.base] = g.thisVal
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
		g.hasState = true
		// On the first call there is no pending yield expression to receive a
		// sent value, so discard sendVal (per spec, the first .next(v)'s v is
		// ignored). The body starts from PC 0.
	} else {
		// Subsequent call: restore saved frame.
		frame := vmFrame{
			tmpl:     g.tmpl,
			base:     len(g.vm.stack),
			upvalues: g.upvalues,
			pc:       g.savedPC,
		}
		// Restore saved stack segment (locals + operands).
		g.vm.appendValues(g.savedStack)
		g.savedStack = nil
		g.vm.frames = append(g.vm.frames, frame)
		// Push the send value — it becomes the result of the suspended
		// `yield` expression. The instruction after OpYield consumes it.
		g.vm.push(sendVal)
	}

	// Run until yield or return.
	result, err := g.vm.run()
	if err != nil {
		if ys, ok := err.(*yieldSignal); ok {
			// Yield: save the current frame state so we can resume later.
			// The top frame is the generator's frame (yield only appears in
			// generator bytecode). Its pc already points past OpYield.
			frame := g.vm.cur()
			g.savedStack = make([]engine.Value, len(g.vm.stack)-frame.base)
			copy(g.savedStack, g.vm.stack[frame.base:])
			g.savedPC = frame.pc
			// Close upvalues pointing into this frame, then pop it.
			g.vm.closeUpvalues(frame.base)
			g.vm.stack = g.vm.stack[:frame.base]
			g.vm.frames = g.vm.frames[:len(g.vm.frames)-1]
			g.state = genSuspended
			return ys.value, ys
		}
		// Real error: generator is done.
		g.state = genCompleted
		g.hasState = false
		return engine.Undefined(), err
	}
	// Normal return: generator is done.
	g.state = genCompleted
	g.hasState = false
	return result, nil
}

// iterResult creates an iterator result object {value, done}.
func (g *GeneratorValue) iterResult(value engine.Value, done bool) engine.Value {
	result := engine.NewObject()
	engine.SetProto(result, g.vm.interp.objectProto)
	_ = result.Set("value", value)
	_ = result.Set("done", engine.Boolean(done))
	return result
}

// doYield is called from the VM main loop when OpYield is executed. It simply
// returns a yieldSignal to break out of run(); the generator's resume() method
// is responsible for saving the frame state (stack + pc) before popping it.
func (v *VM) doYield(yieldVal engine.Value) (engine.Value, error) {
	return yieldVal, &yieldSignal{value: yieldVal}
}

// === engine.Value interface ================================================

func (g *GeneratorValue) Type() engine.ValueType { return engine.TypeObject }

func (g *GeneratorValue) String() string { return "[object Generator]" }

func (g *GeneratorValue) Int() (int, bool)                    { return 0, false }
func (g *GeneratorValue) Float() (float64, bool)              { return 0, false }
func (g *GeneratorValue) Bool() (bool, bool)                  { return true, true }
func (g *GeneratorValue) IsUndefined() bool                   { return false }
func (g *GeneratorValue) IsNull() bool                        { return false }
func (g *GeneratorValue) IsObject() bool                      { return true }
func (g *GeneratorValue) IsFunction() bool                    { return false }
func (g *GeneratorValue) AsObject() (engine.Object, bool)     { return g, true }
func (g *GeneratorValue) AsFunction() (engine.Function, bool) { return nil, false }

func (g *GeneratorValue) Get(key string) (engine.Value, error) { return g.obj.Get(key) }
func (g *GeneratorValue) Set(key string, val engine.Value) error {
	return g.obj.Set(key, val)
}
func (g *GeneratorValue) Keys() []string       { return g.obj.Keys() }
func (g *GeneratorValue) Delete(key string) bool { return g.obj.Delete(key) }
