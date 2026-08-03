package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// awaitSignal is a sentinel error returned by run() when OpAwait is executed.
// It signals that the async function suspended at an await expression and the
// awaited value should be wrapped in a Promise to schedule resumption.
type awaitSignal struct {
	value engine.Value
}

func (a *awaitSignal) Error() string { return "await" }

// asyncRunner manages the execution of an async function. It is created when
// an async function is called, creates a Promise that is returned to the
// caller, and drives the function body with suspension at each OpAwait and
// resumption when the awaited Promise settles.
type asyncRunner struct {
	vm       *VM
	tmpl     *bytecode.FuncTemplate
	upvalues []*upvalue
	thisVal  engine.Value
	args     []engine.Value
	promise  *PromiseValue

	// Saved frame state when suspended at an await.
	savedStack    []engine.Value
	savedPC       int
	savedTryStack []*vmTryHandler
	hasState      bool
}

// newAsyncRunner creates an async function runner. Call start() to begin
// execution and obtain the result Promise.
func newAsyncRunner(vm *VM, tmpl *bytecode.FuncTemplate, upvalues []*upvalue, thisVal engine.Value, args []engine.Value) *asyncRunner {
	return &asyncRunner{
		vm:       vm,
		tmpl:     tmpl,
		upvalues: upvalues,
		thisVal:  thisVal,
		args:     args,
	}
}

// start begins async function execution and returns the Promise that will be
// settled with the function's return value (fulfilled) or thrown error
// (rejected).
func (ar *asyncRunner) start() *PromiseValue {
	ar.promise = NewPromiseValue(ar.vm.interp)
	ar.runStep(engine.Undefined(), false)
	return ar.promise
}

// runStep sets up or restores the async function's frame and runs it until the
// next await, return, or throw. When resumeVal is provided (isThrow == false),
// it is pushed as the result of the awaited expression. When isThrow == true,
// the resumeVal is thrown into the frame at the await point (for rejected
// awaited promises).
func (ar *asyncRunner) runStep(resumeVal engine.Value, isThrow bool) {
	if !ar.hasState {
		// First run: set up a fresh frame.
		ar.setupFrame()
		ar.hasState = true
	} else {
		// Subsequent run: restore saved frame.
		ar.restoreFrame()
		if isThrow {
			// Throw the rejection reason into the restored frame.
			// Pass the raw engine.Value so normalizeException preserves it
			// (wrapping in *jsThrow would cause it to be re-wrapped as Error).
			result, err := ar.vm.handleThrow(resumeVal)
			ar.processResult(result, err)
			return
		}
		// Push the resolved value as the result of the await expression.
		ar.vm.push(resumeVal)
	}

	result, err := ar.vm.run()
	ar.processResult(result, err)
}

// processResult handles the outcome of run(): either the function returned,
// hit an await, or threw an exception.
func (ar *asyncRunner) processResult(result engine.Value, err error) {
	if err != nil {
		if as, ok := err.(*awaitSignal); ok {
			// Await: save frame state and schedule continuation.
			ar.suspendAtAwait(as.value)
			return
		}
		// Real error (throw not caught inside the async body): clean up the
		// frame (run() does NOT pop it on error) and reject the promise.
		ar.cleanupFrame()
		ar.promise.reject(extractThrowValue(err, ar.vm.interp))
		return
	}
	// Normal return: resolve the promise with the return value.
	// The frame was already cleaned up by doReturn inside run().
	// If the return value is itself a Promise, resolve() will adopt it.
	ar.promise.resolve(result)
}

// cleanupFrame removes the async function's frame from the VM stack. Called
// when the body throws an uncaught exception (handleThrow does not pop the
// frame when no try/catch handler is found).
func (ar *asyncRunner) cleanupFrame() {
	frame := ar.vm.cur()
	ar.vm.closeUpvalues(frame.base)
	ar.vm.stack = ar.vm.stack[:frame.base]
	ar.vm.frames = ar.vm.frames[:len(ar.vm.frames)-1]
}

// setupFrame creates a fresh VM frame for the async function body (first call).
func (ar *asyncRunner) setupFrame() {
	frame := vmFrame{
		tmpl:     ar.tmpl,
		base:     len(ar.vm.stack),
		upvalues: ar.upvalues,
	}
	for i := 0; i < ar.tmpl.NumLocals; i++ {
		ar.vm.stack = append(ar.vm.stack, engine.Undefined())
	}
	ar.vm.stack[frame.base] = ar.thisVal
	for i := 0; i < ar.tmpl.NumParams && i < len(ar.args); i++ {
		ar.vm.stack[frame.base+1+i] = ar.args[i]
	}
	if ar.tmpl.IsVarArgs {
		restSlot := frame.base + 1 + ar.tmpl.NumParams
		var restElems []engine.Value
		if len(ar.args) > ar.tmpl.NumParams {
			restElems = append(restElems, ar.args[ar.tmpl.NumParams:]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, ar.vm.interp.arrayProto)
		ar.vm.stack[restSlot] = restArr
	}
	ar.vm.frames = append(ar.vm.frames, frame)
}

// restoreFrame restores the saved frame state so run() can resume from the
// point where OpAwait suspended execution.
func (ar *asyncRunner) restoreFrame() {
	frame := vmFrame{
		tmpl:     ar.tmpl,
		base:     len(ar.vm.stack),
		upvalues: ar.upvalues,
		pc:       ar.savedPC,
		tryStack: ar.savedTryStack,
	}
	ar.vm.stack = append(ar.vm.stack, ar.savedStack...)
	ar.savedStack = nil
	ar.savedTryStack = nil
	ar.vm.frames = append(ar.vm.frames, frame)
}

// suspendAtAwait saves the current frame state, wraps the awaited value in a
// Promise, and attaches continuation handlers that will resume the async
// function when the Promise settles.
func (ar *asyncRunner) suspendAtAwait(awaitedVal engine.Value) {
	// Save frame state (stack segment + PC + try handlers).
	frame := ar.vm.cur()
	ar.savedStack = make([]engine.Value, len(ar.vm.stack)-frame.base)
	copy(ar.savedStack, ar.vm.stack[frame.base:])
	ar.savedPC = frame.pc
	ar.savedTryStack = frame.tryStack
	// Close upvalues and pop the frame (same as generator suspension).
	ar.vm.closeUpvalues(frame.base)
	ar.vm.stack = ar.vm.stack[:frame.base]
	ar.vm.frames = ar.vm.frames[:len(ar.vm.frames)-1]

	// Wrap the value in a Promise and schedule continuation.
	awaitedPromise := promiseResolve(ar.vm.interp, awaitedVal)

	onFulfilled := ar.vm.interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var val engine.Value
		if len(args) > 0 {
			val = args[0]
		} else {
			val = engine.Undefined()
		}
		ar.runStep(val, false)
		return engine.Undefined(), nil
	})

	onRejected := ar.vm.interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		var val engine.Value
		if len(args) > 0 {
			val = args[0]
		} else {
			val = engine.Undefined()
		}
		ar.runStep(val, true)
		return engine.Undefined(), nil
	})

	awaitedPromise.then(onFulfilled, onRejected)
}

// doAwait is called by the VM's OpAwait handler. It pops the awaited value
// and returns an awaitSignal to break out of run(), mirroring doYield.
func (v *VM) doAwait(awaitedVal engine.Value) (engine.Value, error) {
	return awaitedVal, &awaitSignal{value: awaitedVal}
}
