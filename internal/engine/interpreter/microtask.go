package interpreter

// enqueueMicrotask adds a job to the microtask queue. Microtasks run after the
// current synchronous execution completes (i.e., after the VM's top-level
// runModule returns).
//
// 异步上下文传播（AsyncLocalStorage）：入队时捕获当前异步上下文，执行时恢复
// ——使 Promise reaction / async 续体 / queueMicrotask 能继承入队时刻的 store。
// 钩子未安装时零开销（AsyncContextCapture/Restore 为 nil）。
func (interp *Interpreter) enqueueMicrotask(fn func()) {
	if AsyncContextCapture != nil {
		captured := AsyncContextCapture()
		interp.microtaskQueue = append(interp.microtaskQueue, func() {
			if AsyncContextRestore != nil && captured != nil {
				saved := AsyncContextCapture()
				AsyncContextRestore(captured)
				defer func() { AsyncContextRestore(saved) }()
			}
			fn()
		})
		return
	}
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

// drainMicrotasksReport 排空微任务队列，返回本次是否执行了微任务。
// 若无待执行微任务返回 false（供调用方判断是否继续排空）。
func (interp *Interpreter) drainMicrotasksReport() bool {
	if len(interp.microtaskQueue) == 0 {
		return false
	}
	interp.drainMicrotasks()
	return true
}
