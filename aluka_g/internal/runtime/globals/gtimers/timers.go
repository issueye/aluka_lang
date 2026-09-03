package gtimers

// 全局定时器（setTimeout/setInterval/setImmediate/clear*）。
// 基于事件循环的 PostTask 机制：到期后把 JS 回调投递到 JS 线程。
//
// 一次性定时器（setTimeout/setImmediate）经集中式到期队列派发：所有条目
// 按（到期时刻, 注册序号）堆序由单一派发 goroutine 依序 PostTask，消除
// 多个独立 AfterFunc 各自竞争投递导致的同刻到期乱序（Node 的 timer list
// 保证同批按注册顺序执行；compat-boundary-closure-plan 工作流 C2）。
// setInterval 仍走独立 Ticker（重复语义）。
//
// 注意：回调以 engine.Value（函数）形式存储，执行时经 engine.Function.Call
// 在 JS 线程调用（PostTask 保证在 JS 单线程执行）。

import (
	"container/heap"
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

// fireEntry 是一次性定时器的到期限排队条目。
type fireEntry struct {
	deadline time.Time
	seq      int64
	run      func() // PostTask 投递 JS 回调
	done     func()
	stopped  bool
}

// fireQueue 按 (deadline, seq) 的最小堆。
type fireQueue []*fireEntry

func (q fireQueue) Len() int { return len(q) }
func (q fireQueue) Less(i, j int) bool {
	if !q[i].deadline.Equal(q[j].deadline) {
		return q[i].deadline.Before(q[j].deadline)
	}
	return q[i].seq < q[j].seq
}
func (q fireQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *fireQueue) Push(x any)   { *q = append(*q, x.(*fireEntry)) }
func (q *fireQueue) Pop() any {
	old := *q
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return e
}

// NewTimers 注册全局定时器函数到 ctx。
func NewTimers(ctx engine.Context, cfg TimerConfig) error {
	// 每个 Context 一个定时器状态（闭包捕获）。
	state := &timerState{
		ctx:       ctx,
		nextID:    1,
		timers:    make(map[int]*activeTimer),
		maxTimers: cfg.MaxTimers,
		fireWake:  make(chan struct{}, 1),
	}
	state.startFireLoop()

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

// wakeFireLoop 唤醒集中派发 goroutine（非阻塞）。
func (s *timerState) wakeFireLoop() {
	select {
	case s.fireWake <- struct{}{}:
	default:
	}
}

// startFireLoop 启动一次性定时器的集中派发 goroutine：循环取出所有已到期
// 条目（堆序 = (deadline, seq)）依序 PostTask 到 JS 线程，随后休眠至最早
// 到期或被新调度唤醒。单一 goroutine 派发消除了多个 AfterFunc 各自竞争
// 投递 taskCh 的乱序窗口。
func (s *timerState) startFireLoop() {
	go func() {
		for {
			var due []*fireEntry
			var next time.Time
			s.mu.Lock()
			now := time.Now()
			for s.fireHeap.Len() > 0 {
				top := s.fireHeap[0]
				if top.deadline.After(now) {
					next = top.deadline
					break
				}
				e := heap.Pop(&s.fireHeap).(*fireEntry)
				if !e.stopped {
					due = append(due, e)
				}
			}
			s.mu.Unlock()
			for _, e := range due {
				e.run()
				e.done() // 单次触发后释放句柄
			}
			if len(due) > 0 {
				continue // 派发期间可能又有到期或新调度
			}
			// 休眠至最早到期时刻或新调度唤醒。
			var timerC <-chan time.Time
			if !next.IsZero() {
				t := time.NewTimer(time.Until(next))
				timerC = t.C
				select {
				case <-s.fireWake:
					t.Stop()
				case <-timerC:
				}
			} else {
				<-s.fireWake
			}
		}
	}()
}

// timerState 持有当前 Context 的定时器状态。
type timerState struct {
	ctx       engine.Context
	mu        sync.Mutex
	nextID    int
	timers    map[int]*activeTimer
	maxTimers int

	// 一次性定时器集中派发：堆序 = (deadline, seq)，同刻到期按注册序 FIFO。
	fireSeq  int64
	fireHeap fireQueue
	fireWake chan struct{}
}

// timerHandle 管理定时器的活跃引用，实现 Node 的 unref/ref/hasRef 语义：
// 定时器创建时持有 1 个活跃引用（ref'd，阻止进程退出）；unref() 释放该
// 引用；ref() 重新持有；单次触发或 clear 时若仍持有则最终释放。
type timerHandle struct {
	ctx     engine.Context
	mu      sync.Mutex
	refd    bool
	release func() // 当前持有的引用释放函数（refd 为 true 时有效）
}

func newTimerHandle(ctx engine.Context) *timerHandle {
	h := &timerHandle{ctx: ctx, refd: true}
	h.release = ctx.AddRef()
	return h
}

func (h *timerHandle) unref() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refd && h.release != nil {
		r := h.release
		h.release = nil
		h.refd = false
		r()
	}
}

func (h *timerHandle) ref() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.refd && h.release == nil {
		h.release = h.ctx.AddRef()
		h.refd = true
	}
}

func (h *timerHandle) hasRef() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refd
}

// done 在定时器触发（单次）或 clear 时调用：确保不再持有活跃引用。
func (h *timerHandle) done() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.release != nil {
		r := h.release
		h.release = nil
		h.refd = false
		r()
	}
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
// 返回 Node 风格 Timeout 对象：unref()/ref()/hasRef() 控制句柄是否计入事件
// 循环活跃度；Symbol.toPrimitive 返回定时器 id，供 clearTimeout 数字兼容。
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

	// 计时器句柄：默认计入事件循环活跃度（进程在定时器存活期间不退出）；
	// unref() 可释放。单次定时器触发后释放；interval 在 clear 时释放。
	handle := newTimerHandle(ctx)

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
				handle.done() // 释放 interval 句柄
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
		// setTimeout/setImmediate：单次，经集中到期队列按 (deadline, seq)
		// 依序派发（同刻到期的定时器按注册顺序执行，对齐 Node）。
		entry := &fireEntry{
			deadline: time.Now().Add(time.Duration(delay) * time.Millisecond),
			run:      run,
			done:     handle.done,
		}
		s.mu.Lock()
		s.fireSeq++
		entry.seq = s.fireSeq
		heap.Push(&s.fireHeap, entry)
		s.mu.Unlock()
		s.wakeFireLoop()
		stopFn = func() {
			s.mu.Lock()
			entry.stopped = true
			s.mu.Unlock()
			handle.done()
		}
	}

	s.mu.Lock()
	s.timers[id] = &activeTimer{id: id, stopFn: stopFn}
	s.mu.Unlock()

	// Node 风格 Timeout 对象。unref()/ref() 返回对象本身（可链式调用）。
	timeout := engine.NewObject()
	_ = timeout.Set("unref", engine.NewFunction("unref", func(args []engine.Value) (engine.Value, error) {
		handle.unref()
		return timeout, nil
	}))
	_ = timeout.Set("ref", engine.NewFunction("ref", func(args []engine.Value) (engine.Value, error) {
		handle.ref()
		return timeout, nil
	}))
	_ = timeout.Set("hasRef", engine.NewFunction("hasRef", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(handle.hasRef()), nil
	}))
	// 数字转换：clearTimeout(setTimeout(...)) 与 `+timeout` 均可用。
	_ = timeout.Set(engine.SymbolToPrimitive.SymbolKey(), engine.NewFunction("[Symbol.toPrimitive]", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(id), nil
	}))

	return timeout, nil
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

// clear 取消定时器。参数支持 number 或 Timeout 对象（Node 语义）。
func (s *timerState) clear(args []engine.Value) {
	if len(args) == 0 {
		return
	}
	id, ok := timerIDOf(args[0])
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

// timerIDOf 从 clear 参数提取定时器 id：数字直接返回；Timeout 对象经
// Symbol.toPrimitive 转换（与 Node 一致）。
func timerIDOf(v engine.Value) (int, bool) {
	if n, ok := v.Int(); ok {
		return n, true
	}
	obj, ok := v.AsObject()
	if !ok {
		return 0, false
	}
	fv, err := obj.Get(engine.SymbolToPrimitive.SymbolKey())
	if err != nil || !fv.IsFunction() {
		return 0, false
	}
	f, ok := fv.AsFunction()
	if !ok {
		return 0, false
	}
	res, err := f.Call([]engine.Value{engine.Str("number")})
	if err != nil {
		return 0, false
	}
	return res.Int()
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
