package interpreter

// captureAsyncContext wraps a scheduled job with the AsyncLocalStorage state
// active when it was queued. When hooks are disabled the original function is
// returned without allocation.
func captureAsyncContext(fn func()) func() {
	if AsyncContextCapture != nil {
		captured := AsyncContextCapture()
		return func() {
			if AsyncContextRestore != nil && captured != nil {
				saved := AsyncContextCapture()
				AsyncContextRestore(captured)
				defer func() { AsyncContextRestore(saved) }()
			}
			fn()
		}
	}
	return fn
}

// enqueueNextTick adds a process.nextTick job. Node gives this queue priority
// over Promise reactions at every event-loop checkpoint.
func (interp *Interpreter) enqueueNextTick(fn func()) {
	interp.nextTickQueue = append(interp.nextTickQueue, captureAsyncContext(fn))
}

// enqueueMicrotask adds a Promise reaction or queueMicrotask callback.
func (interp *Interpreter) enqueueMicrotask(fn func()) {
	interp.microtaskQueue = append(interp.microtaskQueue, captureAsyncContext(fn))
}

func drainQueue(queue *[]func()) bool {
	if len(*queue) == 0 {
		return false
	}
	for len(*queue) > 0 {
		fn := (*queue)[0]
		(*queue)[0] = nil
		*queue = (*queue)[1:]
		fn()
	}
	// Do not retain callback closures through an empty slice's backing array.
	*queue = nil
	return true
}

// drainJobQueues runs a complete Node-style microtask checkpoint. nextTick
// jobs always run before Promise/queueMicrotask jobs. If a microtask schedules
// another nextTick, the outer loop services it before leaving the checkpoint.
func (interp *Interpreter) drainJobQueues() bool {
	ran := false
	for len(interp.nextTickQueue) > 0 || len(interp.microtaskQueue) > 0 {
		if drainQueue(&interp.nextTickQueue) {
			ran = true
		}
		if drainQueue(&interp.microtaskQueue) {
			ran = true
		}
	}
	return ran
}
