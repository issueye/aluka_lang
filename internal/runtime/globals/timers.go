package globals

// 全局定时器（setTimeout/setInterval/setImmediate/clear*）。
// 基于事件循环的 PostTask 机制：Go time.AfterFunc 到期后把 JS 回调投递到 JS 线程。
//
// 注意：回调以 engine.Value（函数）形式存储，执行时经 engine.Function.Call
// 在 JS 线程调用（PostTask 保证在 JS 单线程执行）。

import (
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// TimerConfig 是定时器注册配置。
type TimerConfig struct {
	// MaxTimers 限制活跃定时器数量（0 = 无限）。
	MaxTimers int
}

// NewTimers 注册全局定时器函数到 ctx。
func NewTimers(ctx engine.Context, cfg TimerConfig) error {
	// 每个 Context 一个定时器状态（闭包捕获）。
	state := &timerState{
		ctx:     ctx,
		nextID:  1,
		timers:  make(map[int]*activeTimer),
		maxTimers: cfg.MaxTimers,
	}

	g := ctx.Global()

	// setTimeout(fn, ms, ...args)
	_ = g.Set("setTimeout", engine.NewFunction("setTimeout", state.setTimeout))
	// clearTimeout(id)
	_ = g.Set("clearTimeout", engine.NewFunction("clearTimeout", state.clearTimeout))
	// setInterval(fn, ms, ...args)
	_ = g.Set("setInterval", engine.NewFunction("setInterval", state.setInterval))
	// clearInterval(id)
	_ = g.Set("clearInterval", engine.NewFunction("clearInterval", state.clearInterval))
	// setImmediate(fn, ...args)
	_ = g.Set("setImmediate", engine.NewFunction("setImmediate", state.setImmediate))
	// clearImmediate(id)
	_ = g.Set("clearImmediate", engine.NewFunction("clearImmediate", state.clearImmediate))

	return nil
}

// activeTimer 表示一个活跃定时器。
type activeTimer struct {
	id     int
	stopFn func()
}

// timerState 持有当前 Context 的定时器状态。
type timerState struct {
	ctx     engine.Context
	mu      sync.Mutex
	nextID  int
	timers  map[int]*activeTimer
	maxTimers int
}

// setTimeout(fn, ms, ...args)：延迟 ms 毫秒后调用 fn。
func (s *timerState) setTimeout(args []engine.Value) (engine.Value, error) {
	return s.schedule(args, false)
}

// setInterval(fn, ms, ...args)：每 ms 毫秒调用一次 fn。
func (s *timerState) setInterval(args []engine.Value) (engine.Value, error) {
	return s.schedule(args, true)
}

// setImmediate(fn, ...args)：立即（下一事件循环 tick）调用 fn。
func (s *timerState) setImmediate(args []engine.Value) (engine.Value, error) {
	return s.schedule(args, false, 0)
}

// schedule 创建定时器。interval 为 true 时重复调度；delay 默认 1ms（setImmediate 用 0）。
func (s *timerState) schedule(args []engine.Value, interval bool, forcedDelay ...int) (engine.Value, error) {
	if len(args) == 0 {
		return engine.IntValue(0), nil
	}
	cb := args[0] // 回调函数（engine.Value）
	delay := 1
	if !interval || len(args) > 1 {
		if d, ok := intArg2(args, 1, 1); ok {
			delay = d
		}
	}
	if len(forcedDelay) > 0 {
		delay = forcedDelay[0]
	}
	if delay < 0 {
		delay = 0
	}
	extraArgs := []engine.Value{}
	if len(args) > 2 {
		extraArgs = append(extraArgs, args[2:]...)
	}

	// 分配 id
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	if s.maxTimers > 0 && len(s.timers) >= s.maxTimers {
		s.mu.Unlock()
		return engine.IntValue(0), nil
	}
	s.mu.Unlock()

	ctx := s.ctx

	// 计时器句柄：计入事件循环活跃度（进程在定时器存活期间不退出）。
	// 单次定时器触发后释放；interval 在 clear 时释放。
	releaseHandle := ctx.AddRef()

	// 调度函数：到期后 PostTask 到 JS 线程执行回调。
	run := func() {
		ctx.PostTask(func() {
			if f, ok := cb.AsFunction(); ok {
				if _, err := f.Call(extraArgs); err != nil {
					// Node 语义：回调抛出且无上层捕获 → uncaughtException。
					interpreter.ReportUncaught(ctx, err)
				}
			}
		})
	}

	var stopFn func()
	if interval {
		// setInterval：重复调度。
		var timer *time.Ticker
		timer = time.NewTicker(time.Duration(delay) * time.Millisecond)
		// stopped 通道在 clear 时关闭，通知投递 goroutine 退出，避免其
		// 因 Ticker.Stop() 不关闭 C 而永久阻塞（goroutine 泄漏）。
		stopped := make(chan struct{})
		var stopOnce sync.Once
		stopFn = func() {
			stopOnce.Do(func() {
				timer.Stop()
				close(stopped)
				releaseHandle() // 释放 interval 句柄
			})
		}
		// 在独立 goroutine 持续投递（进程存活由 handle 保证）。
		go func() {
			for {
				select {
				case _, ok := <-timer.C:
					if !ok {
						return
					}
					run()
				case <-stopped:
					return
				}
			}
		}()
	} else {
		// setTimeout/setImmediate：单次。
		delayDur := time.Duration(delay) * time.Millisecond
		released := false
		t := time.AfterFunc(delayDur, func() {
			run()
			if !released {
				released = true
				releaseHandle() // 单次触发后释放句柄
			}
		})
		stopFn = func() {
			t.Stop()
			if !released {
				released = true
				releaseHandle()
			}
		}
	}

	s.mu.Lock()
	s.timers[id] = &activeTimer{id: id, stopFn: stopFn}
	s.mu.Unlock()

	return engine.IntValue(id), nil
}

// clearTimeout(id) / clearInterval(id) / clearImmediate(id)：取消定时器。
func (s *timerState) clearTimeout(args []engine.Value) (engine.Value, error) {
	s.clear(args)
	return engine.Undefined(), nil
}

func (s *timerState) clearInterval(args []engine.Value) (engine.Value, error) {
	s.clear(args)
	return engine.Undefined(), nil
}

func (s *timerState) clearImmediate(args []engine.Value) (engine.Value, error) {
	s.clear(args)
	return engine.Undefined(), nil
}

// clear 取消定时器。
func (s *timerState) clear(args []engine.Value) {
	if len(args) == 0 {
		return
	}
	id, ok := args[0].Int()
	if !ok {
		return
	}
	s.mu.Lock()
	t, exists := s.timers[id]
	if exists {
		delete(s.timers, id)
	}
	s.mu.Unlock()
	if exists && t != nil && t.stopFn != nil {
		t.stopFn()
	}
}

// intArg2 取第 i 个参数为 int；不存在返回 def 与 false。
func intArg2(args []engine.Value, i int, def int) (int, bool) {
	if i < len(args) {
		if n, ok := args[i].Int(); ok {
			return n, true
		}
	}
	return def, false
}
