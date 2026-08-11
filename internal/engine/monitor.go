package engine

// 性能/内存/运行时监控与内存上限控制（--monitor / --max-memory）。
//
// 设计：
//   - 进程级原子计数器（指令数/调用数）由 VM 在 gated 分支下累加，
//     常态（未启用监控）零开销；对象分配数复用 jsHeap.alloc。
//   - 内存上限：Go debug.SetMemoryLimit 软上限（触发更激进 GC）+
//     看门狗 goroutine（周期采样 HeapAlloc，超限先 runtime.GC() 再判），
//     VM 安全点（指令分发/对象分配）检查 oomFlag → 抛 JS RangeError
//     （V8 同款 "JavaScript heap out of memory"）；宽限期后强制退出防挂死。

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// ErrOutOfMemory 是 JS 堆内存超限错误（映射为 RangeError，V8 同款语义）。
var ErrOutOfMemory = errors.New("aluka: out of memory")

// --- 进程级性能计数器（监控启用时累加，常态零开销） ---------------------

// metricsEnabled 是全局监控开关（StartupMetrics 设置；仅初始化时写入一次）。
var metricsEnabled atomic.Bool

// MetricsCounters 是进程级性能/内存计数器。
var MetricsCounters = struct {
	Insns  atomic.Uint64 // 解释器指令数
	Calls  atomic.Uint64 // 函数调用数（bytecode 闭包 + 原生）
	Allocs atomic.Uint64 // JS 对象分配数（register 挂钩）
}{}

// EnableMetrics 开启计数器累加（--monitor）。
func EnableMetrics() {
	metricsEnabled.Store(true)
}

// DisableMetricsForTest 关闭计数器（测试清理用）。
func DisableMetricsForTest() {
	metricsEnabled.Store(false)
}

// MetricsEnabled 返回监控是否启用。
func MetricsEnabled() bool { return metricsEnabled.Load() }

// BumpInsns 指令计数（VM 指令分发 gated 分支调用）。
func BumpInsns() {
	if metricsEnabled.Load() {
		MetricsCounters.Insns.Add(1)
	}
}

// BumpCalls 函数调用计数。
func BumpCalls() {
	if metricsEnabled.Load() {
		MetricsCounters.Calls.Add(1)
	}
}

// BumpAlloc 对象分配计数（gc.go register 内调用）。
func BumpAlloc() {
	if metricsEnabled.Load() {
		MetricsCounters.Allocs.Add(1)
	}
}

// --- 内存上限与 OOM 保护 -------------------------------------------------

var (
	memLimitBytes atomic.Int64  // 0 = 无限制
	memLimitSet   atomic.Bool   // 看门狗已启动（避免重复）
	memWatchStop  chan struct{} // 看门狗停止信号（测试用）
	oomFlag       atomic.Bool   // 已触发 OOM
	oomAt         atomic.Int64  // OOM 触发时刻（unix nano）

	// oomStrikeLimit 是连续超限强制退出的阈值（可被测试调大）。
	oomStrikeLimit atomic.Int32
)

func init() {
	oomStrikeLimit.Store(5)
}

// SetMemoryLimit 设置进程内存上限（bytes）。0/-1 关闭。
// 机制：Go debug.SetMemoryLimit 软上限 + 看门狗硬判定。
func SetMemoryLimit(bytes int64) {
	memLimitBytes.Store(bytes)
	if bytes <= 0 {
		debug.SetMemoryLimit(-1) // -1 = 关闭软上限
		oomFlag.Store(false)
		return
	}
	debug.SetMemoryLimit(bytes)
	if memLimitSet.CompareAndSwap(false, true) {
		memWatchStop = make(chan struct{})
		go memWatchdog(bytes, memWatchStop)
	}
}

// StopMemoryWatchdog 停止看门狗 goroutine（测试用）。
func StopMemoryWatchdog() {
	if memLimitSet.CompareAndSwap(true, false) {
		close(memWatchStop)
	}
}

// MemoryLimitBytes 返回当前内存上限（0 = 无限制）。
func MemoryLimitBytes() int64 { return memLimitBytes.Load() }

// memOverLimit 检测是否超限：先采样，超限则触发 Go GC 再判定（看门狗与测试共用）。
func memOverLimit(limit int64) bool {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if int64(ms.HeapAlloc) <= limit {
		return false
	}
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc) > limit
}

// memWatchdog 周期采样堆内存：超限先 GC，仍超限则置 oomFlag（VM 安全点
// 抛 RangeError 并被消费）；连续多次超限（约 0.5s 未缓解）强制退出防挂死。
func memWatchdog(limit int64, stop <-chan struct{}) {
	strikes := 0
	for {
		select {
		case <-stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
		if memOverLimit(limit) {
			strikes++
			if !oomFlag.Load() {
				oomAt.Store(time.Now().UnixNano())
			}
			oomFlag.Store(true)
			// 宽限期后仍未缓解（VM 抛错被吞/挂死）：强制退出。
			if int32(strikes) >= oomStrikeLimit.Load() {
				fmt.Fprintf(os.Stderr, "aluka: fatal: memory limit %d bytes exceeded; process killed\n", limit)
				os.Exit(3)
			}
			continue
		}
		strikes = 0
	}
}

// OOMTriggered 返回是否已触发 OOM（VM 安全点检查）。
func OOMTriggered() bool { return oomFlag.Load() }

// ConsumeOOM 消费 OOM 标志（一次性）：VM 抛 RangeError 前清除，使 catch
// 块得以执行；若 catch 继续超限分配，看门狗将再次置位（strikes 累计）。
func ConsumeOOM() bool {
	return oomFlag.CompareAndSwap(true, false)
}

// OOMError 返回 OOM 错误（含超限大小；包装 ErrRangeError → JS RangeError）。
func OOMError() error {
	limit := memLimitBytes.Load()
	return fmt.Errorf("%w: JavaScript heap out of memory (limit %d bytes)", ErrRangeError, limit)
}

// OOMStrikeLimitForTest 返回当前强制退出阈值（测试用）。
func OOMStrikeLimitForTest() int {
	return int(oomStrikeLimit.Load())
}

// SetOOMStrikeLimitForTest 调整强制退出阈值（测试用）。
func SetOOMStrikeLimitForTest(n int) {
	oomStrikeLimit.Store(int32(n))
}

// ResetOOMState 清除 OOM 状态（测试/多文件场景复位）。
func ResetOOMState() {
	oomFlag.Store(false)
	oomAt.Store(0)
}

// TriggerOOMForTest sets the OOM flag without starting the watchdog.
func TriggerOOMForTest() {
	oomFlag.Store(true)
	oomAt.Store(time.Now().UnixNano())
}

// OOMAt 返回 OOM 触发时刻（unix nano；未触发为 0）。
func OOMAt() int64 { return oomAt.Load() }

// --- 运行时指标辅助 -------------------------------------------------------

// RuntimeSnapshot 是一次进程级运行时指标快照。
type RuntimeSnapshot struct {
	HeapAlloc    uint64
	HeapInuse    uint64
	HeapSys      uint64
	HeapIdle     uint64
	HeapReleased uint64
	StackInuse   uint64
	PeakAlloc    uint64
	TotalAlloc   uint64
	NumGC        uint32
	PauseTotal   time.Duration
	Goroutines   int
	ObjectsAlloc int64 // jsHeap 累计分配
	ObjectsLive  int64 // jsHeap 存活
}

// peakHeapAlloc 是进程级历史最高 HeapAlloc（monitor 报告用）。
var peakHeapAlloc atomic.Uint64

// ReadRuntimeSnapshot 收集当前运行时指标（--monitor 报告）。
func ReadRuntimeSnapshot() RuntimeSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	for {
		cur := peakHeapAlloc.Load()
		if ms.HeapAlloc <= cur || peakHeapAlloc.CompareAndSwap(cur, ms.HeapAlloc) {
			break
		}
	}
	snap := RuntimeSnapshot{
		HeapAlloc:    ms.HeapAlloc,
		HeapInuse:    ms.HeapInuse,
		HeapSys:      ms.HeapSys,
		HeapIdle:     ms.HeapIdle,
		HeapReleased: ms.HeapReleased,
		StackInuse:   ms.StackInuse,
		PeakAlloc:    peakHeapAlloc.Load(),
		TotalAlloc:   ms.TotalAlloc,
		NumGC:        ms.NumGC,
		PauseTotal:   time.Duration(ms.PauseTotalNs),
		Goroutines:   runtime.NumGoroutine(),
	}
	snap.ObjectsAlloc = allocCount.Load()
	live := int64(0)
	jsHeapGlobal.mu.Lock()
	for w := range jsHeapGlobal.objects {
		if w.Value() != nil {
			live++
		}
	}
	jsHeapGlobal.mu.Unlock()
	snap.ObjectsLive = live
	return snap
}
