package interpreter

// 事件循环基础设施（node:http / 定时器）。
//
// 设计（对应需求分析文档 6.2 异步模型）：
//   - JS 代码（含 VM 执行）只在 RunLoop 所在 goroutine 上运行，保证单线程语义。
//   - 任意 Go goroutine（net/http 请求回调、定时器到期）通过 PostTask 投递
//     闭包到 taskCh，由 RunLoop 在 JS 线程依次执行。
//   - 每个任务执行后 drain 一次 microtask 队列（Promise 反应、async 续期）。
//   - WaitGroup 跟踪活跃任务/定时器；所有任务完成（计数归零）时 RunLoop 退出。
//
// 活跃度模型：taskWG 计数"活跃句柄"= 已投递任务 + 活跃定时器。
//   - PostTask：投递时 Add(1)，执行后 Done。
//   - 定时器：创建时 Add(1)，单次触发后 Done / interval 在 clear 时 Done。
//   - 当 taskWG 归零时关闭 allDone，RunLoop 据此退出。

// AddRef 跟踪活跃句柄（implements engine.Context）。
func (interp *Interpreter) AddRef() func() {
	return interp.NewTaskHandle()
}

// NewTaskHandle 返回一个计数用的句柄（内部 Add 一次到 taskWG）。
func (interp *Interpreter) NewTaskHandle() func() {
	if interp == nil || interp.taskCh == nil {
		// 兜底：无事件循环时返回 no-op。
		return func() {}
	}
	interp.taskWG.Add(1)
	done := make(chan struct{})
	go func() {
		<-done
		interp.taskWG.Done()
	}()
	return func() { close(done) }
}

// PostTask 将任务投递到 JS 执行线程（非阻塞）。
// 可在任意 goroutine 调用。任务将在 RunLoop 的 JS goroutine 上执行。
func (interp *Interpreter) PostTask(fn func()) {
	if interp == nil || interp.taskCh == nil {
		// 无事件循环：直接执行（仅适用于当前就在 JS 线程的情况）。
		fn()
		return
	}
	interp.taskWG.Add(1)
	select {
	case interp.taskCh <- fn:
	case <-interp.stopCh:
		interp.taskWG.Done() // 循环已关闭，不再执行
	}
}

// RunLoop 启动事件循环并阻塞执行任务，直到所有活跃任务完成。
// 必须在与 VM 相同的 goroutine 调用（此处即 JS 执行线程）。
func (interp *Interpreter) RunLoop() {
	interp.loopOnce.Do(func() {
		defer close(interp.stopCh)
		// 监听 taskWG 归零，关闭 allDone 通知退出。
		allDone := make(chan struct{})
		go func() {
			interp.taskWG.Wait()
			close(allDone)
		}()
		for {
			select {
			case fn := <-interp.taskCh:
				fn()
				interp.taskWG.Done()
				// 任务执行后排空 microtask（Promise/async 续期）。
				interp.drainMicrotasks()
			case <-allDone:
				return
			case <-interp.stopCh:
				return
			}
		}
	})
}

// Wait 阻塞直到所有活跃任务完成（不关闭循环）。
func (interp *Interpreter) Wait() {
	interp.taskWG.Wait()
}

// Stop 关闭事件循环（所有已投递任务执行完成后退出）。
func (interp *Interpreter) Stop() {
	select {
	case <-interp.stopCh:
		// 已关闭
	default:
		close(interp.stopCh)
	}
}
