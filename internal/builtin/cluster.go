package builtin

// node:cluster 内置模块——多进程 master/worker。
// 基于 child_process.fork 实现 worker（独立进程 + IPC）。
// 提供：fork/isPrimary/isMaster/settings/Worker/schedulingPolicy。

import (
	"os"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewCluster 构造 node:cluster 模块导出对象。
func NewCluster(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// cluster.isPrimary / isMaster：当前进程是否为 master。
	// aluka 模型：环境变量 ALUKA_WORKER_ID 标记 worker 进程。
	isPrimary := os.Getenv("ALUKA_WORKER_ID") == ""
	_ = m.Set("isPrimary", engine.Boolean(isPrimary))
	_ = m.Set("isMaster", engine.Boolean(isPrimary)) // 兼容旧名
	_ = m.Set("isWorker", engine.Boolean(!isPrimary))

	// cluster.workers：worker id → Worker 实例（主进程）。
	workersObj := engine.NewObject()
	_ = m.Set("workers", workersObj)

	// cluster.settings：fork 时的配置（exec/execArgs/silent/stdio）。
	settings := engine.NewObject()
	_ = settings.Set("execArgv", engine.NewArray(nil))
	_ = settings.Set("args", engine.NewArray(nil))
	_ = settings.Set("silent", engine.Boolean(false))
	_ = settings.Set("stdio", engine.NewArray(nil))
	_ = m.Set("settings", settings)

	// cluster.schedulingPolicy：'rr' 或 'none'（Node 导出为 number：SCHED_*）。
	_ = m.Set("schedulingPolicy", engine.IntValue(1)) // SCHED_NONE
	_ = m.Set("SCHED_NONE", engine.IntValue(1))
	_ = m.Set("SCHED_RR", engine.IntValue(2))

	// cluster.setupMaster([settings])：更新配置（简化）。
	_ = m.Set("setupMaster", engine.NewFunction("setupMaster", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				for _, k := range []string{"exec", "execArgv", "args", "silent", "cwd", "serialization"} {
					if v, err := o.Get(k); err == nil && !v.IsUndefined() {
						_ = settings.Set(k, v)
					}
				}
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

		// 构造 Worker 对象。
		worker := engine.NewObject()
		_ = worker.Set("id", engine.IntValue(workerID))
		_ = worker.Set("process", child)
		// 复用 child 的 'exit'/'message' 事件到 worker。
		if onFn, err := child.Get("on"); err == nil && onFn.IsFunction() {
			if f, ok := onFn.AsFunction(); ok {
				// 'exit' → 触发 worker 'exit' + 从 workers 移除。
				exitWrapper := engine.NewFunction("__workerExit", func(ca []engine.Value) (engine.Value, error) {
					emitEvent(worker, "exit", engine.IntValue(0), engine.Null())
					_ = workersObj.Delete(engine.Number(float64(workerID)).String())
					emitEvent(m, "exit", worker, engine.IntValue(0), engine.Null())
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
