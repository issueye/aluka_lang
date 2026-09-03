// VM 异步驱动：微任务/nextTick 入队、微任务排空与 await 同步等待。

package interpreter

import (
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// EnqueueMicrotask 入队一个微任务（node:test 子测试调度等内置模块使用）。
// 微任务在下一次 drainMicrotasks/AwaitPromise 时执行。
func (v *VM) EnqueueMicrotask(fn func()) {
	v.interp.enqueueMicrotask(fn)
}

// EnqueueNextTick queues a Node process.nextTick callback separately from
// Promise microtasks so it can run first at the next checkpoint.
func (v *VM) EnqueueNextTick(fn func()) {
	v.interp.enqueueNextTick(fn)
}

// AsyncContextCapture / AsyncContextRestore 是 node:async_hooks 安装的异步
// 上下文钩子（AsyncLocalStorage 等）。两者必须成对设置（nil = 不启用）。
//
// 语义（对齐 Node async_hooks）：JS 闭包在**创建时**捕获当前异步上下文，
// 在**事件循环外首次调用时**（len(v.frames)==0，即定时器/微任务/IO 回调）
// 恢复该上下文，保证 AsyncLocalStorage 的 store 能跨异步资源传播。同步
// 调用（JS 帧存在时）不恢复，保持 Node 的 run()/enterWith 语义。
var (
	AsyncContextCapture func() interface{}
	AsyncContextRestore func(ctx interface{})
)

// DrainMicrotasks 排空 Node job queues（process.nextTick 优先于 Promise
// reactions、queueMicrotask、async 续体）。仅当无活跃 JS 帧（顶层模块加载场景）时安全调用，供 Loader 在
// 模块函数包装（P0-1）执行完毕后触发，模拟原 RunModule 顶层分支的排水行为。
func (v *VM) DrainMicrotasks() {
	if len(v.frames) == 0 {
		v.interp.drainJobQueues()
	}
}

// AwaitPromise 同步等待 promise settle（顶层 await / TLA 的模块加载语义）。
// 循环驱动 Node job queues 与投递的任务（IO 回调等），直至 promise 完成。
// 供 Loader 在 async 模块函数（含 TLA）执行后调用。
func (v *VM) AwaitPromise(p *PromiseValue) (engine.Value, error) {
	for {
		switch p.state {
		case promiseFulfilled:
			return p.result, nil
		case promiseRejected:
			return engine.Undefined(), &jsThrow{val: p.result}
		}
		v.interp.drainJobQueues()
		select {
		case fn := <-v.interp.taskCh:
			fn()
			// PostTask increments active for the queued task. AwaitPromise drives
			// that queue before RunLoop starts, so it must perform the matching
			// decrement that RunLoop normally does after executing a task.
			v.interp.decActive()
		case <-v.interp.idleCh:
			// 空闲信号：无任务在途，继续微任务驱动（TLA 依赖同步 promise）。
		default:
			// 微任务已排空且无投递任务：IO 在途（await fetch 等），短暂让出。
			time.Sleep(time.Millisecond)
		}
	}
}

// FlushMicrotasks 无条件排空 Node job queues（implements engine.Context）。
// 与 DrainMicrotasks 不同，不计较是否有活跃 JS 帧：HTTP handler 等在
// 同步返回后仍需驱动 Promise/async 续期，直到响应完成。
func (v *VM) FlushMicrotasks() bool {
	return v.interp.drainJobQueues()
}
