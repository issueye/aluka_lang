package builtin

// node:worker_threads 内置模块（开发计划 3.9）。
//
// 架构（3-R1 应对：独立堆 + 消息传递，无 SharedArrayBuffer）：
//   - Worker 在独立 Go goroutine 运行一个完整 VM（interpreter.NewVMEngine），
//     loader 加载模块文件。
//   - 消息经 JSON 序列化跨 VM 传递（valueToJSON/jsonToEngine，复用 v8.go）。
//   - worker 内注入 parentPort 全局（postMessage/on('message')）与 workerData。
//   - 主线程 Worker 对象是 EventEmitter：postMessage/on('message')/threadId/
//     terminate。

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// workerState 是 Worker 的内部状态（跨 VM 消息通道）。
type workerState struct {
	toWorker      chan string // 主线程 → worker
	toMain        chan string // worker → 主线程
	closed        chan struct{}
	mu            sync.Mutex
	stopFn        func() // 终止 worker 的事件循环
	releaseWorker func() // 释放 worker 端 parentPort 的活跃度
}

func (s *workerState) setStop(fn func()) {
	s.mu.Lock()
	s.stopFn = fn
	s.mu.Unlock()
}

func (s *workerState) stop() {
	s.mu.Lock()
	fn := s.stopFn
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *workerState) setWorkerRelease(fn func()) {
	s.mu.Lock()
	s.releaseWorker = fn
	s.mu.Unlock()
}

func (s *workerState) releaseWorkerRef() {
	s.mu.Lock()
	fn := s.releaseWorker
	s.releaseWorker = nil
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

var workerThreadCounter int64

// NewWorkerThreads 构造 node:worker_threads 模块导出对象。
// parentPort/workerData/isMainThread 从全局读取（worker 内注入对应值）。
func NewWorkerThreads(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	_ = m.Set("Worker", engine.NewFunction("Worker", func(args []engine.Value) (engine.Value, error) {
		return newWorkerInstance(ctx, args), nil
	}))
	// 主线程默认值。
	isMain := true
	if v, err := ctx.Global().Get("isMainThread"); err == nil {
		if b, ok := v.Bool(); ok {
			isMain = b
		}
	}
	_ = m.Set("isMainThread", engine.Boolean(isMain))
	_ = m.Set("threadId", engine.IntValue(0))

	var ppVal engine.Value = engine.Null()
	if v, err := ctx.Global().Get("parentPort"); err == nil && !v.IsUndefined() && !v.IsNull() {
		ppVal = v
	}
	_ = m.Set("parentPort", ppVal)

	var wdVal engine.Value = engine.Undefined()
	if v, err := ctx.Global().Get("workerData"); err == nil && !v.IsUndefined() {
		wdVal = v
	}
	_ = m.Set("workerData", wdVal)
	return m, nil
}

// newWorkerInstance 创建 Worker 对象并启动 worker goroutine。
func newWorkerInstance(mainCtx engine.Context, args []engine.Value) engine.Value {
	worker := newEmitterInstance().(engine.Object)
	filename := ""
	if len(args) > 0 {
		filename = args[0].String()
	}
	// workerData（JSON 序列化传给 worker）。
	var workerDataJSON []byte
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("workerData"); err == nil && !v.IsUndefined() {
				if dj, err := valueToJSON(v, make(map[engine.Object]bool)); err == nil {
					workerDataJSON, _ = json.Marshal(dj)
				}
			}
		}
	}

	state := &workerState{
		toWorker: make(chan string, 16),
		toMain:   make(chan string, 16),
		closed:   make(chan struct{}),
	}
	threadID := int(atomic.AddInt64(&workerThreadCounter, 1))
	_ = worker.Set("threadId", engine.IntValue(threadID))

	// worker 计入事件循环活跃度（运行期间保持主进程存活）。
	release := mainCtx.AddRef()

	// worker.postMessage(data) → worker 端 parentPort 'message'。
	_ = worker.Set("postMessage", engine.NewFunction("postMessage", func(pa []engine.Value) (engine.Value, error) {
		var msg engine.Value
		if len(pa) > 0 {
			msg = pa[0]
		}
		data, _ := json.Marshal(mustValueToJSON(msg))
		select {
		case state.toWorker <- string(data):
		case <-state.closed:
		}
		return engine.Undefined(), nil
	}))

	// worker.terminate()：关闭通道并停止 worker 事件循环。
	_ = worker.Set("terminate", engine.NewFunction("terminate", func(pa []engine.Value) (engine.Value, error) {
		select {
		case <-state.closed:
		default:
			close(state.closed)
		}
		state.stop()
		state.releaseWorkerRef()
		return engine.Undefined(), nil
	}))

	// 主线程接收 worker 消息 → worker 'message' 事件。
	go func() {
		for {
			select {
			case msg := <-state.toMain:
				mv := jsonToEngine(jsonDecode(msg))
				mainCtx.PostTask(func() {
					emitEvent(worker, "message", mv)
				})
			case <-state.closed:
				return
			}
		}
	}()

	// worker goroutine：独立 VM + loader。
	go func() {
		defer release()
		eng := interpreter.NewVMEngine()
		wctx, err := eng.NewContext()
		if err != nil {
			mainCtx.PostTask(func() {
				emitEvent(worker, "error", engine.Str(err.Error()))
				emitEvent(worker, "exit", engine.IntValue(1))
			})
			return
		}
		defer wctx.Close()
		_ = globals.NewConsole(wctx, globals.ConsoleConfig{})
		_ = globals.NewProcess(wctx, globals.ProcessConfig{})
		_ = globals.NewBuffer(wctx, globals.BufferConfig{})
		_ = globals.NewTimers(wctx, globals.TimerConfig{})
		_ = wctx.Global().Set("globalThis", wctx.Global())

		// parentPort：worker 端消息端口。
		pp := newEmitterInstance().(engine.Object)
		_ = pp.Set("postMessage", engine.NewFunction("postMessage", func(pa []engine.Value) (engine.Value, error) {
			var msg engine.Value
			if len(pa) > 0 {
				msg = pa[0]
			}
			data, _ := json.Marshal(mustValueToJSON(msg))
			select {
			case state.toMain <- string(data):
			case <-state.closed:
			}
			return engine.Undefined(), nil
		}))
		// parentPort 保持 worker 事件循环存活；close 时释放。
		workerRelease := wctx.AddRef()
		state.setWorkerRelease(workerRelease)
		_ = pp.Set("close", engine.NewFunction("close", func(pa []engine.Value) (engine.Value, error) {
			state.releaseWorkerRef()
			select {
			case <-state.closed:
			default:
				close(state.closed)
			}
			return engine.Undefined(), nil
		}))
		_ = wctx.Global().Set("parentPort", pp)
		_ = wctx.Global().Set("isMainThread", engine.Boolean(false))
		if workerDataJSON != nil {
			_ = wctx.Global().Set("workerData", jsonToEngine(jsonDecode(string(workerDataJSON))))
		}

		// 主线程消息 → worker 端 parentPort 'message'。
		go func() {
			for {
				select {
				case msg := <-state.toWorker:
					mv := jsonToEngine(jsonDecode(msg))
					wctx.PostTask(func() {
						emitEvent(pp, "message", mv)
					})
				case <-state.closed:
					return
				}
			}
		}()

		// 加载并运行 worker 模块。
		loader := modmodule.NewLoader(wctx)
		RegisterAll(loader)
		runErr := loader.Run(filename)
		if runErr != nil {
			mainCtx.PostTask(func() {
				emitEvent(worker, "error", engine.Str(fmt.Sprintf("worker: %v", runErr)))
				emitEvent(worker, "exit", engine.IntValue(1))
			})
			state.releaseWorkerRef()
			return
		}
		// 主线程 terminate 时停止 worker 事件循环。
		state.setStop(func() {
			if vm, ok := wctx.(interface{ Stop() }); ok {
				vm.Stop()
			}
		})
		if vm, ok := wctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		state.releaseWorkerRef()
		// worker 事件循环结束 → 主线程 'exit'。
		mainCtx.PostTask(func() {
			emitEvent(worker, "exit", engine.IntValue(0))
		})
	}()

	return worker
}

// jsonDecode 把 JSON 字符串解码为 interface{}。
func jsonDecode(s string) interface{} {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return v
}
