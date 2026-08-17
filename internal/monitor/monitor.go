// Package monitor 提供 aluka 进程级性能/内存/运行时指标监控器（--monitor）。
//
// 指标分四组：
//   - 性能：解释器指令数、函数调用数、分配数、IC 命中率（由调用方注入）
//   - 内存：HeapAlloc/HeapInuse/HeapSys/峰值/对象存活（runtime + jsHeap）
//   - 运行时：goroutines、GC 次数/暂停总时长
//   - 时间：运行总时长、各阶段耗时（加载/执行）
package monitor

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// Format 是监控输出格式。
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Config 配置监控器。
type Config struct {
	Enabled  bool
	Interval time.Duration // 周期采样间隔；0 = 仅在结束时输出一次
	Format   Format
	Out      io.Writer
	// VMMetrics 返回解释器级指标（IC 统计等）；nil 则跳过。
	VMMetrics func() engine.ICStats
}

// Report 是一次监控快照的全部指标。
type Report struct {
	Timestamp time.Time
	Elapsed   time.Duration // 自 Start 起
	Period    time.Duration // 本次采样周期（终报为 0）

	// 性能
	Insns                 uint64 // 周期内指令数（周期=0 时终报累计）
	Calls                 uint64
	Allocs                uint64
	ICGetHit, ICGetMiss   uint64
	ICSetHit, ICSetMiss   uint64
	ICCallHit, ICCallMiss uint64

	// 内存
	HeapAlloc, HeapInuse, HeapSys, HeapIdle, HeapReleased uint64
	StackInuse, PeakAlloc, TotalAlloc                     uint64
	ObjectsAlloc, ObjectsLive                             int64

	// 运行时
	Goroutines int
	NumGC      uint32
	PauseTotal time.Duration
}

// Monitor 是进程级监控器实例。
type Monitor struct {
	cfg   Config
	start time.Time

	mu        sync.Mutex
	prev      engine.RuntimeSnapshot
	prevInsns uint64
	prevCalls uint64
	prevAlloc uint64
	lastTime  time.Time
	stopped   bool
}

// New 创建监控器。cfg.Enabled 时启动计数器。
func New(cfg Config) *Monitor {
	m := &Monitor{cfg: cfg, start: time.Now()}
	if cfg.Enabled {
		engine.EnableMetrics()
	}
	return m
}

// Snapshot 采集一次指标快照。
func (m *Monitor) Snapshot() *Report {
	now := time.Now()
	runtimeSnap := engine.ReadRuntimeSnapshot()
	insns := engine.MetricsCounters.Insns.Load()
	calls := engine.MetricsCounters.Calls.Load()
	allocs := engine.MetricsCounters.Allocs.Load()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastTime.IsZero() {
		m.lastTime = m.start
	}
	rep := &Report{
		Timestamp: now,
		Elapsed:   now.Sub(m.start),
		Period:    now.Sub(m.lastTime),
	}
	// 周期增量（终报：累计值）。
	if m.stopped || m.cfg.Interval == 0 {
		rep.Insns = insns
		rep.Calls = calls
		rep.Allocs = allocs
	} else {
		rep.Insns = insns - m.prevInsns
		rep.Calls = calls - m.prevCalls
		rep.Allocs = allocs - m.prevAlloc
	}
	rep.HeapAlloc = runtimeSnap.HeapAlloc
	rep.HeapInuse = runtimeSnap.HeapInuse
	rep.HeapSys = runtimeSnap.HeapSys
	rep.HeapIdle = runtimeSnap.HeapIdle
	rep.HeapReleased = runtimeSnap.HeapReleased
	rep.StackInuse = runtimeSnap.StackInuse
	rep.PeakAlloc = runtimeSnap.PeakAlloc
	rep.TotalAlloc = runtimeSnap.TotalAlloc
	rep.ObjectsAlloc = runtimeSnap.ObjectsAlloc
	rep.ObjectsLive = runtimeSnap.ObjectsLive
	rep.Goroutines = runtimeSnap.Goroutines
	rep.NumGC = runtimeSnap.NumGC
	rep.PauseTotal = runtimeSnap.PauseTotal
	if m.cfg.VMMetrics != nil {
		ic := m.cfg.VMMetrics()
		rep.ICGetHit, rep.ICGetMiss = ic.GetHit, ic.GetMiss
		rep.ICSetHit, rep.ICSetMiss = ic.SetHit, ic.SetMiss
		rep.ICCallHit, rep.ICCallMiss = ic.CallHit, ic.CallMiss
	}
	m.prev = runtimeSnap
	m.prevInsns = insns
	m.prevCalls = calls
	m.prevAlloc = allocs
	m.lastTime = now
	return rep
}

// Run 阻塞式周期采样：按 Interval 输出快照，直至 stop 通道关闭。
func (m *Monitor) Run(stop <-chan struct{}) {
	if m.cfg.Interval <= 0 {
		<-stop
		return
	}
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.Print(m.Snapshot())
		}
	}
}

// Stop 停止监控并输出终报（输出到 cfg.Out）。
func (m *Monitor) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.mu.Unlock()
	if m.cfg.Out != nil && m.cfg.Enabled {
		m.Print(m.Snapshot())
	}
}

// Print 输出一次快照（text/json）。
func (m *Monitor) Print(rep *Report) {
	if m.cfg.Out == nil {
		return
	}
	switch m.cfg.Format {
	case FormatJSON:
		m.printJSON(rep)
	default:
		m.printText(rep)
	}
}

// formatBytes 人类可读字节。
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (m *Monitor) printText(rep *Report) {
	w := m.cfg.Out
	period := rep.Period
	if rep.Period == 0 {
		period = rep.Elapsed
	}
	insnsPerSec := float64(0)
	if period > 0 {
		insnsPerSec = float64(rep.Insns) / period.Seconds()
	}
	fmt.Fprintf(w, "── aluka monitor ──────────────────────────────────────────\n")
	fmt.Fprintf(w, "  elapsed     %s\n", rep.Elapsed.Round(time.Millisecond))
	fmt.Fprintf(w, "  性能:\n")
	fmt.Fprintf(w, "    指令      %d (%.1f M/s)\n", rep.Insns, insnsPerSec/1e6)
	fmt.Fprintf(w, "    调用      %d\n", rep.Calls)
	fmt.Fprintf(w, "    分配      %d 对象\n", rep.Allocs)
	fmt.Fprintf(w, "    IC 命中   get %d/%d  set %d/%d  call %d/%d\n",
		rep.ICGetHit, rep.ICGetHit+rep.ICGetMiss,
		rep.ICSetHit, rep.ICSetHit+rep.ICSetMiss,
		rep.ICCallHit, rep.ICCallHit+rep.ICCallMiss)
	fmt.Fprintf(w, "  内存:\n")
	fmt.Fprintf(w, "    heap      %s 使用 / %s 占用 / 峰值 %s\n",
		formatBytes(rep.HeapAlloc), formatBytes(rep.HeapInuse), formatBytes(rep.PeakAlloc))
	fmt.Fprintf(w, "    heapSys   %s (idle %s, released %s)\n",
		formatBytes(rep.HeapSys), formatBytes(rep.HeapIdle), formatBytes(rep.HeapReleased))
	fmt.Fprintf(w, "    stack     %s\n", formatBytes(rep.StackInuse))
	fmt.Fprintf(w, "    对象      %d 存活 / %d 累计\n", rep.ObjectsLive, rep.ObjectsAlloc)
	if limit := engine.MemoryLimitBytes(); limit > 0 {
		fmt.Fprintf(w, "    limit     %s (--max-memory)\n", formatBytes(uint64(limit)))
	}
	fmt.Fprintf(w, "  运行时:\n")
	fmt.Fprintf(w, "    goroutines %d\n", rep.Goroutines)
	fmt.Fprintf(w, "    GC        %d 次, 暂停累计 %s\n", rep.NumGC, rep.PauseTotal.Round(time.Microsecond))
	fmt.Fprintf(w, "───────────────────────────────────────────────────────────\n")
}

func (m *Monitor) printJSON(rep *Report) {
	w := m.cfg.Out
	lines := []string{
		`"elapsed_ms":` + fmt.Sprintf("%.1f", float64(rep.Elapsed.Nanoseconds())/1e6),
		fmt.Sprintf(`"insns":%d`, rep.Insns),
		fmt.Sprintf(`"calls":%d`, rep.Calls),
		fmt.Sprintf(`"allocs":%d`, rep.Allocs),
		fmt.Sprintf(`"ic_get_hit":%d,"ic_get_miss":%d`, rep.ICGetHit, rep.ICGetMiss),
		fmt.Sprintf(`"ic_set_hit":%d,"ic_set_miss":%d`, rep.ICSetHit, rep.ICSetMiss),
		fmt.Sprintf(`"ic_call_hit":%d,"ic_call_miss":%d`, rep.ICCallHit, rep.ICCallMiss),
		fmt.Sprintf(`"heap_alloc":%d,"heap_inuse":%d,"heap_sys":%d`, rep.HeapAlloc, rep.HeapInuse, rep.HeapSys),
		fmt.Sprintf(`"heap_idle":%d,"heap_released":%d,"stack_inuse":%d`, rep.HeapIdle, rep.HeapReleased, rep.StackInuse),
		fmt.Sprintf(`"peak_alloc":%d,"total_alloc":%d`, rep.PeakAlloc, rep.TotalAlloc),
		fmt.Sprintf(`"objects_live":%d,"objects_alloc":%d`, rep.ObjectsLive, rep.ObjectsAlloc),
		fmt.Sprintf(`"goroutines":%d,"gc_count":%d,"gc_pause_ms":%.1f`, rep.Goroutines, rep.NumGC, float64(rep.PauseTotal.Nanoseconds())/1e6),
	}
	sort.Strings(lines)
	fmt.Fprintf(w, "{\"ts\":%q,%s}\n", rep.Timestamp.Format(time.RFC3339Nano), strings.Join(lines, ","))
}
