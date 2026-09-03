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

// trackUnhandled 登记一个已 reject 且当时无处理者的 promise，供微任务检查点
// 末尾统一判定（Node 语义：unhandledRejection 在检查点末尾、按 rejection
// FIFO 派发；同检查点内稍后挂 catch 的不派发）。
func (interp *Interpreter) trackUnhandled(p *PromiseValue) {
	interp.unhandledQueue = append(interp.unhandledQueue, p)
}

// flushPendingUnhandled 在检查点末尾判定待定 unhandledRejection：已挂处理者
// 的跳过并移除；仍无处理者的按 FIFO 派发（一次性）。返回是否派发了事件。
func (interp *Interpreter) flushPendingUnhandled() bool {
	if len(interp.unhandledQueue) == 0 {
		return false
	}
	pending := interp.unhandledQueue
	interp.unhandledQueue = nil
	dispatched := false
	for _, p := range pending {
		if p.hadHandler || p.unhandledReported {
			continue
		}
		p.unhandledReported = true
		p.dispatchUnhandledRejection()
		dispatched = true
	}
	return dispatched
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
// After the queues are empty, pending unhandledRejection promises are judged
// (Node fires the event at checkpoint end); listeners may enqueue more jobs,
// so the whole sequence repeats until stable.
func (interp *Interpreter) drainJobQueues() bool {
	ran := false
	for {
		drained := false
		for len(interp.nextTickQueue) > 0 || len(interp.microtaskQueue) > 0 {
			if drainQueue(&interp.nextTickQueue) {
				drained = true
			}
			if drainQueue(&interp.microtaskQueue) {
				drained = true
			}
		}
		if !interp.flushPendingUnhandled() {
			ran = ran || drained
			return ran
		}
		ran = true
	}
}
