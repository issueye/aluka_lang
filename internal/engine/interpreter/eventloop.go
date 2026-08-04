package interpreter

// 事件循环基础设施（node:http / 定时器）。
//
// 设计（对应需求分析文档 6.2 异步模型）：
//   - JS 代码（含 VM 执行）只在 RunLoop 所在 goroutine 上运行，保证单线程语义。
//   - 任意 Go goroutine（net/http 请求回调、定时器到期）通过 PostTask 投递
//     闭包到 taskCh，由 RunLoop 在 JS 线程依次执行。
//   - 每个任务执行后 drain 一次 microtask 队列（Promise 反应、async 续期）。
//
// 活跃度模型（退出检测）：active 计数"活跃句柄" = 已投递任务 + 活跃定时器
//   - PostTask：投递前 active++，任务执行后 active--。
//   - 定时器/服务器：创建时 AddRef（active++），触发/关闭时释放（active--）。
//   - active 归零时发送 idleCh 信号；RunLoop 收到后确认 active==0 且未关闭
//     则退出（设 loopDone 防止新任务进入）。
//
// 线程安全：所有 active 读写在 loopMu 锁内；Add 与退出判定互斥，避免
// WaitGroup 在计数归零后复用导致的 panic。

// AddRef 跟踪活跃句柄（implements engine.Context）。
func (interp *Interpreter) AddRef() func() {
	return interp.NewTaskHandle()
}

// NewTaskHandle 返回一个计数用的句柄（内部 active++ 一次）。
// 事件循环已结束时返回 no-op（不再计入活跃度）。
func (interp *Interpreter) NewTaskHandle() func() {
	if interp == nil || interp.taskCh == nil {
		// 兜底：无事件循环时返回 no-op。
		return func() {}
	}
	interp.loopMu.Lock()
	if interp.loopDone {
		// 事件循环已结束：不再计入活跃度。
		interp.loopMu.Unlock()
		return func() {}
	}
	interp.active++
	interp.loopMu.Unlock()
	return func() { interp.decActive() }
}

// PostTask 将任务投递到 JS 执行线程（非阻塞）。
// 可在任意 goroutine 调用。任务将在 RunLoop 的 JS goroutine 上执行。
func (interp *Interpreter) PostTask(fn func()) {
	if interp == nil || interp.taskCh == nil {
		// 无事件循环：直接执行（仅适用于当前就在 JS 线程的情况）。
		fn()
		return
	}
	interp.loopMu.Lock()
	if interp.loopDone {
		// 事件循环已结束：丢弃任务。
		interp.loopMu.Unlock()
		return
	}
	interp.active++
	interp.loopMu.Unlock()
	select {
	case interp.taskCh <- fn:
	case <-interp.stopCh:
		interp.decActive() // 循环已关闭，不再执行
	}
}

// decActive 递减活跃计数；归零时发送空闲信号（非阻塞，缓冲 1）。
func (interp *Interpreter) decActive() {
	interp.loopMu.Lock()
	interp.active--
	idle := interp.active == 0
	interp.loopMu.Unlock()
	if idle {
		select {
		case interp.idleCh <- struct{}{}:
		default:
		}
	}
}

// RunLoop 启动事件循环并阻塞执行任务，直到所有活跃任务完成。
// 必须在与 VM 相同的 goroutine 调用（此处即 JS 执行线程）。
func (interp *Interpreter) RunLoop() {
	interp.loopOnce.Do(func() {
		defer func() {
			// 幂等关闭 stopCh（Stop() 可能已关闭）。
			select {
			case <-interp.stopCh:
			default:
				close(interp.stopCh)
			}
			interp.loopMu.Lock()
			interp.loopDone = true
			interp.loopMu.Unlock()
		}()
		// 先处理挂起的 microtask：顶层 async 调用（fire-and-forget）可能
		// 在此创建定时器/投递任务，否则事件循环会误判为空闲立即退出。
		interp.drainMicrotasks()
		// 启动时若无活跃任务（纯同步程序），立即退出。
		interp.loopMu.Lock()
		if interp.active == 0 && !interp.loopDone {
			interp.loopDone = true
			interp.loopMu.Unlock()
			return
		}
		interp.loopMu.Unlock()

		for {
			select {
			case fn := <-interp.taskCh:
				fn()
				interp.decActive() // 任务完成
				// 任务执行后排空 microtask（Promise/async 续期）。
				interp.drainMicrotasks()
			case <-interp.idleCh:
				// 空闲信号可能已过期（期间又有新任务）。确认后才退出。
				interp.loopMu.Lock()
				if interp.active == 0 && !interp.loopDone {
					interp.loopDone = true
					interp.loopMu.Unlock()
					return
				}
				interp.loopMu.Unlock()
			case <-interp.stopCh:
				interp.loopMu.Lock()
				interp.loopDone = true
				interp.loopMu.Unlock()
				return
			}
		}
	})
}

// Wait 阻塞直到所有活跃任务完成（不关闭循环）。
func (interp *Interpreter) Wait() {
	for {
		interp.loopMu.Lock()
		idle := interp.active == 0
		interp.loopMu.Unlock()
		if idle {
			return
		}
		select {
		case <-interp.idleCh:
		case <-interp.stopCh:
			return
		}
	}
}

// Stop 关闭事件循环（已投递任务不再执行）。
func (interp *Interpreter) Stop() {
	select {
	case <-interp.stopCh:
		// 已关闭
	default:
		close(interp.stopCh)
	}
}
