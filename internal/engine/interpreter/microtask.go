package interpreter

// enqueueMicrotask adds a job to the microtask queue. Microtasks run after the
// current synchronous execution completes (i.e., after the VM's top-level
// runModule returns).
func (interp *Interpreter) enqueueMicrotask(fn func()) {
	interp.microtaskQueue = append(interp.microtaskQueue, fn)
}

// drainMicrotasks runs all pending microtasks until the queue is empty.
// New microtasks may be enqueued during execution (e.g., chained Promise
// reactions), and they will be processed in the same drain cycle.
func (interp *Interpreter) drainMicrotasks() {
	for len(interp.microtaskQueue) > 0 {
		fn := interp.microtaskQueue[0]
		interp.microtaskQueue = interp.microtaskQueue[1:]
		fn()
	}
}
