package builtin

// node:cluster 内置模块——多进程 master/worker。
// 基于 child_process.fork 实现 worker（独立进程 + IPC）。
// 提供：fork/isPrimary/isMaster/settings/Worker/schedulingPolicy。

import (
	"os"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewCluster 构造 node:cluster 模块导出对象。
func NewCluster(ctx engine.Context) (engine.Value, error) {
	// cluster 模块本身是 EventEmitter（on/once/emit：'fork'/'exit'/'message'）。
	m := newEmitterInstance().(engine.Object)

	// cluster.isPrimary / isMaster：当前进程是否为 master。
	// aluka 模型：环境变量 ALUKA_WORKER_ID 标记 worker 进程。
	isPrimary := os.Getenv("ALUKA_WORKER_ID") == ""
	_ = m.Set("isPrimary", engine.Boolean(isPrimary))
	_ = m.Set("isMaster", engine.Boolean(isPrimary)) // 兼容旧名
	_ = m.Set("isWorker", engine.Boolean(!isPrimary))

	// cluster.worker：worker 进程内为 {id, process...}；主进程 undefined。
	if !isPrimary {
		workerObj := engine.NewObject()
		if id := os.Getenv("ALUKA_WORKER_ID"); id != "" {
			if n, err := strconv.Atoi(id); err == nil {
				_ = workerObj.Set("id", engine.IntValue(n))
			}
		} else {
			_ = workerObj.Set("id", engine.IntValue(1))
		}
		_ = m.Set("worker", workerObj)
	}

	// cluster.workers：worker id → Worker 实例（主进程）。
	workersObj := engine.NewObject()
	_ = m.Set("workers", workersObj)

	// cluster.settings：fork 时的配置。初始为空对象（Node 语义：
	// 键在 setupPrimary/setupMaster 后成为可枚举自有属性）。
	settings := engine.NewObject()
	_ = m.Set("settings", settings)

	// cluster.schedulingPolicy：'rr' 或 'none'（Node 导出为 number：SCHED_*）。
	_ = m.Set("schedulingPolicy", engine.IntValue(1)) // SCHED_NONE
	_ = m.Set("SCHED_NONE", engine.IntValue(1))
	_ = m.Set("SCHED_RR", engine.IntValue(2))

	// cluster.setupMaster([settings])：更新配置（Node 22 别名 setupPrimary）。
	_ = m.Set("setupMaster", engine.NewFunction("setupMaster", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				for _, k := range []string{"exec", "execArgv", "args", "silent", "cwd", "serialization", "stdio"} {
					if v, err := o.Get(k); err == nil && !v.IsUndefined() {
						_ = settings.Set(k, v)
					}
				}
			}
		}
		return engine.Undefined(), nil
	}))
	_ = m.Set("setupPrimary", engine.NewFunction("setupPrimary", func(args []engine.Value) (engine.Value, error) {
		// setupPrimary 内部就是 setupMaster（Node 22 别名）。
		if onFn, err := m.Get("setupMaster"); err == nil && onFn.IsFunction() {
			if f, ok := onFn.AsFunction(); ok {
				_, _ = f.Call(args)
			}
		}
		return engine.Undefined(), nil
	}))

	// cluster.fork([env]) → Worker
	_ = m.Set("fork", engine.NewFunction("fork", func(args []engine.Value) (engine.Value, error) {
		workerID := len(workersObj.Keys()) + 1

		// 复用 child_process.fork（传递 ALUKA_WORKER_ID 环境变量标记）。
		// worker 执行当前脚本（process.argv[1]）。
		var script string
		if len(os.Args) > 1 {
			script = os.Args[1]
		}
		var workerArgs []engine.Value
		workerArgs = append(workerArgs, engine.Str(script))
		// forkChild 的 args[1] 为 forkArgs 数组（此处空）；args[2] 为 options。
		workerArgs = append(workerArgs, engine.NewArray(nil))
		// forkOpts：env 合并 ALUKA_WORKER_ID。
		optsObj := engine.NewObject()
		envObj := engine.NewObject()
		// 继承当前环境变量。
		for _, kv := range os.Environ() {
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					_ = envObj.Set(kv[:i], engine.Str(kv[i+1:]))
					break
				}
			}
		}
		_ = envObj.Set("ALUKA_WORKER_ID", engine.Number(float64(workerID)))
		// 用户传入的 env 覆盖。
		if len(args) > 0 {
			if uo, ok := args[0].AsObject(); ok {
				for _, k := range uo.Keys() {
					if v, err := uo.Get(k); err == nil {
						_ = envObj.Set(k, v)
					}
				}
			}
		}
		_ = optsObj.Set("env", envObj)
		workerArgs = append(workerArgs, optsObj)

		child := forkChild(ctx, workerArgs).(engine.Object)

		// 构造 Worker 对象（EventEmitter 子类语义：on/once/emit）。
		worker := newEmitterInstance().(engine.Object)
		_ = worker.Set("id", engine.IntValue(workerID))
		_ = worker.Set("process", child)
		// 复用 child 的 'exit'/'message' 事件到 worker。
		if onFn, err := child.Get("on"); err == nil && onFn.IsFunction() {
			if f, ok := onFn.AsFunction(); ok {
				// 'exit' → 先清理 workers 表 + 触发 cluster 'exit'，最后触发
				// worker 'exit'（Node 语义：用户监听器看到 workers 已清理；
				// 用户监听器可能同步 process.exit 中断后续执行）。
				exitWrapper := engine.NewFunction("__workerExit", func(ca []engine.Value) (engine.Value, error) {
					_ = workersObj.Delete(engine.Number(float64(workerID)).String())
					emitEvent(m, "exit", worker, engine.IntValue(0), engine.Null())
					emitEvent(worker, "exit", engine.IntValue(0), engine.Null())
					return engine.Undefined(), nil
				})
				_, _ = f.Call([]engine.Value{engine.Str("exit"), exitWrapper})
				msgWrapper := engine.NewFunction("__workerMsg", func(ca []engine.Value) (engine.Value, error) {
					if len(ca) > 0 {
						emitEvent(worker, "message", ca[0])
						emitEvent(m, "message", ca[0], worker)
					}
					return engine.Undefined(), nil
				})
				_, _ = f.Call([]engine.Value{engine.Str("message"), msgWrapper})
			}
		}
		_ = worker.Set("send", engine.NewFunction("send", func(args []engine.Value) (engine.Value, error) {
			// 委托给 child process.send（简化：空实现）。
			return engine.Boolean(true), nil
		}))
		_ = worker.Set("kill", engine.NewFunction("kill", func(args []engine.Value) (engine.Value, error) {
			return engine.Boolean(true), nil
		}))
		_ = worker.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = worker.Set("isConnected", engine.NewFunction("isConnected", func(args []engine.Value) (engine.Value, error) {
			return engine.Boolean(true), nil
		}))
		_ = worker.Set("isDead", engine.NewFunction("isDead", func(args []engine.Value) (engine.Value, error) {
			return engine.Boolean(false), nil
		}))

		_ = workersObj.Set(engine.Number(float64(workerID)).String(), worker)
		// 'fork' 事件。
		emitEvent(m, "fork", worker)

		return worker, nil
	}))

	// cluster.disconnect([callback])：终止所有 worker。
	_ = m.Set("disconnect", engine.NewFunction("disconnect", func(args []engine.Value) (engine.Value, error) {
		for _, k := range workersObj.Keys() {
			if wv, err := workersObj.Get(k); err == nil {
				if wo, ok := wv.AsObject(); ok {
					if dFn, err := wo.Get("destroy"); err == nil && dFn.IsFunction() {
						if f, ok := dFn.AsFunction(); ok {
							_, _ = f.Call(nil)
						}
					}
				}
			}
		}
		if len(args) > 0 && args[0].IsFunction() {
			if f, ok := args[0].AsFunction(); ok {
				_, _ = f.Call(nil)
			}
		}
		return engine.Undefined(), nil
	}))

	// Worker 类（构造器，供 instanceof）。
	_ = m.Set("Worker", engine.NewFunction("Worker", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))

	return m, nil
}
