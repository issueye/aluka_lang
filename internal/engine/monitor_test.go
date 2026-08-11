package engine

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMetricsCounters 验证监控计数器的 gated 累加。
func TestMetricsCounters(t *testing.T) {
	// 关闭状态下不累加。
	metricsEnabled.Store(false)
	before := MetricsCounters.Insns.Load()
	BumpInsns()
	BumpCalls()
	BumpAlloc()
	if got := MetricsCounters.Insns.Load(); got != before {
		t.Fatalf("Insns should not count when disabled: %d -> %d", before, got)
	}

	// 开启后累加。
	EnableMetrics()
	defer metricsEnabled.Store(false)
	before = MetricsCounters.Insns.Load()
	BumpInsns()
	BumpInsns()
	BumpCalls()
	if got := MetricsCounters.Insns.Load(); got != before+2 {
		t.Fatalf("Insns = %d, want %d", got, before+2)
	}
	if got := MetricsCounters.Calls.Load(); got < before {
		t.Fatalf("Calls did not increment")
	}
	if !MetricsEnabled() {
		t.Fatal("MetricsEnabled() = false after EnableMetrics")
	}
}

// TestReadRuntimeSnapshot 验证运行时指标快照字段完整性。
func TestReadRuntimeSnapshot(t *testing.T) {
	// 分配一些堆以产生非零统计。
	_ = make([]byte, 1<<20)
	snap := ReadRuntimeSnapshot()
	if snap.Goroutines < 1 {
		t.Errorf("Goroutines = %d, want >= 1", snap.Goroutines)
	}
	if snap.TotalAlloc == 0 {
		t.Error("TotalAlloc = 0, want > 0")
	}
	if snap.ObjectsAlloc < 0 || snap.ObjectsLive < 0 {
		t.Errorf("Objects stats negative: alloc=%d live=%d", snap.ObjectsAlloc, snap.ObjectsLive)
	}
	if snap.HeapAlloc == 0 && snap.HeapInuse == 0 {
		t.Error("Heap stats all zero")
	}
	// 峰值单调。
	snap2 := ReadRuntimeSnapshot()
	if snap2.PeakAlloc < snap.PeakAlloc {
		t.Errorf("PeakAlloc regressed: %d -> %d", snap.PeakAlloc, snap2.PeakAlloc)
	}
}

// TestMemoryLimitDetection 验证超限检测 + OOM 标志 + 一次性消费。
func TestMemoryLimitDetection(t *testing.T) {
	ResetOOMState()
	defer ResetOOMState()

	// 直接调用 memOverLimit：用极小上限必然超限。
	if !memOverLimit(1) {
		t.Fatal("memOverLimit(1) = false, want true (any heap exceeds 1 byte)")
	}
	if memOverLimit(1 << 62) {
		t.Fatal("memOverLimit(huge) = true, want false")
	}

	// 看门狗集成：设置极小上限，分配后等待 OOM 标志。
	SetOOMStrikeLimitForTest(1000) // 测试期间不强制退出
	defer SetOOMStrikeLimitForTest(5)
	SetMemoryLimit(1 << 20) // 1MB
	defer StopMemoryWatchdog()
	defer SetMemoryLimit(0)

	// 分配 ~8MB 保证超限（KeepAlive 钉住，防止 liveness 分析提前释放）。
	keep := make([][]byte, 0, 16)
	for i := 0; i < 16; i++ {
		keep = append(keep, make([]byte, 512*1024))
	}
	defer runtime.KeepAlive(keep)

	deadline := time.Now().Add(3 * time.Second)
	for !OOMTriggered() {
		if time.Now().After(deadline) {
			t.Fatal("OOM flag not set within 3s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !OOMTriggered() {
		t.Fatal("OOMTriggered = false after allocation")
	}
	// 一次性消费。
	if !ConsumeOOM() {
		t.Fatal("ConsumeOOM = false while flag set")
	}
	if OOMTriggered() {
		t.Fatal("OOMTriggered still true after ConsumeOOM")
	}
	// OOMError 是 RangeError 包装（可被 JS 捕获为 RangeError）。
	if !strings.Contains(OOMError().Error(), "JavaScript heap out of memory") {
		t.Errorf("OOMError message wrong: %v", OOMError())
	}
	if MemoryLimitBytes() != 1<<20 {
		t.Errorf("MemoryLimitBytes = %d, want %d", MemoryLimitBytes(), 1<<20)
	}
	_ = runtime.GC
}
