//go:build amd64 && (windows || linux)

package interpreter

// R2-4 soak gate: VM lifecycle, background compilation, and LRU cache
// eviction under repeated create / eval / evict / close cycles.
//
// 入口（entry points）:
//   PR（默认，~2-4 秒）:
//     go test ./internal/engine/interpreter/ -run TestAutoJITSoakLifecycleGCAndLRU -count=1 -v
//   Nightly / 长时（轮数 x20，环境变量 ALUKA_JIT_SOAK=1 启用）:
//     ALUKA_JIT_SOAK=1 go test ./internal/engine/interpreter/ -run TestAutoJITSoakLifecycleGCAndLRU -count=1
//
// 注意：本测试禁止 t.Parallel。jitnative.LiveExecutableMemory() 与 RX 代码
// 区域池是包级共享状态，本测试逐轮断言内存回到基线；任何同包并行测试都会
// 发布/释放 RX 区域，干扰基线断言。包内其余测试同样不调用 t.Parallel，
// 顺序执行时互不干扰。

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

const (
	soakPRRounds       = 16              // 默认 PR 模式轮数（8-16 轮区间）。
	soakSoakMultiplier = 20              // ALUKA_JIT_SOAK 模式下轮数 = PRRounds x 20 = 320。
	soakPhaseCount     = 4               // 每轮循环覆盖 4 种生命周期阶段。
	soakCompilePoll    = 5 * time.Second // 后台编译完成轮询的单次截止时间。
)

// soakAggregates 汇总整次 soak 的统计证据，用于批次校验与最终报告。
type soakAggregates struct {
	rounds              int
	gcs                 int
	duration            time.Duration
	nativeCompiled      uint64
	nativeExecuted      uint64
	tracesCompiled      uint64
	tracesExecuted      uint64
	evictions           uint64
	backgroundQueued    uint64
	backgroundCompleted uint64
}

func (a *soakAggregates) String() string {
	return fmt.Sprintf(
		"rounds=%d gc=%d duration=%v nativeCompiled=%d nativeExecuted=%d tracesCompiled=%d tracesExecuted=%d evictions=%d backgroundQueued=%d backgroundCompleted=%d",
		a.rounds, a.gcs, a.duration, a.nativeCompiled, a.nativeExecuted,
		a.tracesCompiled, a.tracesExecuted, a.evictions,
		a.backgroundQueued, a.backgroundCompleted)
}

// TestAutoJITSoakLifecycleGCAndLRU 是 R2-4 的统一 soak 循环（单个测试函数）。
// 每轮按阶段循环覆盖:
//
//	phase 0: 大 IR 函数 -> 后台编译排队 -> 截止时间轮询完成 -> Native 执行 ->
//	         结果校验 -> 收敛后 Close。
//	phase 1: 大 IR 函数 -> 后台编译已发布 RX 但结果仍 pending -> 立即 Close
//	         （锻炼 closeJIT 的 pending-result 排空路径）。
//	phase 2: 多个热点小函数 + 1KB CodeCacheBytes -> 同步 Native 安装 ->
//	         LRU 淘汰（断言 NativeEvictions 增长且 NativeCodeBytes <= 预算）。
//	phase 3: 热点循环 -> Trace 编译 -> Native Trace 执行 -> 小缓存 LRU 淘汰。
//
// 每 4 轮（一个完整阶段循环）执行一次 runtime.GC()；所有轮结束后再 GC 一次。
// 每轮 Close 后都断言 LiveExecutableMemory() 回到测试开始基线。
// 整体由 watchdog 超时保护（PR 90s / soak 10min）。
func TestAutoJITSoakLifecycleGCAndLRU(t *testing.T) {
	soakMode := os.Getenv("ALUKA_JIT_SOAK") != ""
	rounds := soakPRRounds
	if soakMode {
		rounds = soakPRRounds * soakSoakMultiplier
	}
	timeout := 90 * time.Second
	if soakMode {
		timeout = 10 * time.Minute
	}
	t.Logf("JIT soak start: mode=%s rounds=%d hardTimeout=%v", soakModeName(soakMode), rounds, timeout)

	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	agg := &soakAggregates{}
	start := time.Now()

	// soak 主体在独立 goroutine 中运行：保证 watchdog 超时后主测试 goroutine
	// 可以立即 FailNow（FailNow 只能由测试 goroutine 调用）。goroutine 内只
	// 使用 t.Logf 并返回 error；超时路径上泄漏的 VM 属于失败场景，可接受。
	done := make(chan error, 1)
	go func() {
		done <- soakLifecycleLoop(agg, rounds, baseRegions, baseBytes)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("JIT soak failed: %v", err)
		}
	case <-time.After(timeout):
		t.Fatalf("JIT soak did not finish within %v (rounds=%d); a background compile or VM Close is likely hung", timeout, rounds)
	}
	agg.duration = time.Since(start)

	// 批次校验（Requirement 5）：每轮/每周期都必须有 Native 命中与后台编译
	// 证据。phase 0/2/3 每周期至少各 1 次 NativeCompiled/NativeTracesCompiled，
	// phase 0/1 每周期各 1 次 BackgroundQueued，phase 0 每周期 1 次
	// BackgroundCompleted。LRU 淘汰必须至少出现一次。
	cycles := rounds / soakPhaseCount
	if agg.nativeCompiled+agg.tracesCompiled < uint64(cycles)*3 {
		t.Fatalf("JIT soak native hits too low: %s", agg)
	}
	if agg.backgroundQueued < uint64(cycles)*2 || agg.backgroundCompleted < uint64(cycles) {
		t.Fatalf("JIT soak background compile evidence too low: %s", agg)
	}
	if agg.evictions == 0 {
		t.Fatalf("JIT soak observed no LRU evictions: %s", agg)
	}
	if agg.nativeExecuted == 0 && agg.tracesExecuted == 0 {
		t.Fatalf("JIT soak never executed native code: %s", agg)
	}

	// 最终收敛检查（Requirement 6）：全部 VM 已 Close、后台任务已收敛、
	// 额外 GC 后，RX 内存必须回到基线。
	runtime.GC()
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("JIT soak leaked executable memory after final GC: live=(%d,%d) baseline=(%d,%d) %s",
			regions, bytes, baseRegions, baseBytes, agg)
	}
	t.Logf("JIT soak done: %s", agg)
}

func soakModeName(soak bool) string {
	if soak {
		return "nightly"
	}
	return "pr"
}

func soakAutoConfig() jit.Config {
	return jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, Stats: true}
}

// soakLifecycleLoop 依次执行 rounds 轮，按 i%4 轮换 4 种生命周期阶段，
// 每完成一个完整阶段循环（4 轮）后执行一次 runtime.GC()。
func soakLifecycleLoop(agg *soakAggregates, rounds int, baseRegions, baseBytes uint64) error {
	for i := 0; i < rounds; i++ {
		var err error
		switch i % soakPhaseCount {
		case 0:
			err = soakRoundBackground(agg, i, baseRegions, baseBytes)
		case 1:
			err = soakRoundPendingClose(agg, i, baseRegions, baseBytes)
		case 2:
			err = soakRoundLRUEvict(agg, i, baseRegions, baseBytes)
		default:
			err = soakRoundTrace(agg, i, baseRegions, baseBytes)
		}
		if err != nil {
			return fmt.Errorf("iteration %d (phase %d): %w", i, i%soakPhaseCount, err)
		}
		agg.rounds++
		if i%soakPhaseCount == soakPhaseCount-1 {
			runtime.GC()
			agg.gcs++
		}
	}
	return nil
}

// soakBigIR 生成大 IR 表达式（x 加 80 个常量），使 len(program.Code) >= 128，
// 从而走 Auto 模式的 queueNativeCompile 后台编译路径（与既有测试同款）。
func soakBigIR() string {
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	return expression.String()
}

// soakWaitBackgroundCompile 以截止时间轮询等待后台编译完成（Requirement 4：
// 不用固定长 sleep），通过 runtime.Gosched + pollNativeCompiles 驱动。
func soakWaitBackgroundCompile(vm *VM, deadline time.Time) error {
	for vm.jitPending > 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("background compile did not complete within deadline: pending=%d stats=%+v",
				vm.jitPending, vm.JITStats())
		}
		runtime.Gosched()
		vm.pollNativeCompiles()
	}
	stats := vm.JITStats()
	if stats.BackgroundCompleted != 1 || stats.BackgroundDiscarded != 0 {
		return fmt.Errorf("background compile completed uncleanly: %+v", stats)
	}
	return nil
}

func soakCheckBaseline(baseRegions, baseBytes uint64, phase string) error {
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		return fmt.Errorf("%s leaked executable memory: live=(%d,%d) baseline=(%d,%d)",
			phase, regions, bytes, baseRegions, baseBytes)
	}
	return nil
}

// soakRoundBackground（phase 0）：大 IR 函数触发后台 Native 编译，轮询等待
// 安装后再次调用命中 Native，校验结果，最后在完全收敛后 Close。
func soakRoundBackground(agg *soakAggregates, i int, baseRegions, baseBytes uint64) error {
	vm, err := NewVM()
	if err != nil {
		return err
	}
	vm.ConfigureJIT(soakAutoConfig())
	source := fmt.Sprintf(`
		globalThis.soakBig%d = function(x) { return %s; };
		globalThis.soakBigFirst%d = globalThis.soakBig%d(1);
	`, i, soakBigIR(), i, i)
	if _, err := vm.Eval(source, "jit-soak-bg.js"); err != nil {
		vm.Close()
		return err
	}
	first, err := vm.Global().Get(fmt.Sprintf("soakBigFirst%d", i))
	if err != nil || first.String() != "81" {
		vm.Close()
		return fmt.Errorf("first result=%v err=%v", first, err)
	}
	stats := vm.JITStats()
	if stats.BackgroundQueued != 1 || vm.jitPending != 1 || stats.NativeCompiled != 0 {
		vm.Close()
		return fmt.Errorf("background compile was not queued: pending=%d stats=%+v", vm.jitPending, stats)
	}
	if err := soakWaitBackgroundCompile(vm, time.Now().Add(soakCompilePoll)); err != nil {
		vm.Close()
		return err
	}
	stats = vm.JITStats()
	if stats.BackgroundCompleted != 1 || stats.NativeCompiled != 1 ||
		stats.NativeExecuted != 0 || stats.NativeCodeBytes == 0 {
		vm.Close()
		return fmt.Errorf("background native was not installed: %+v", stats)
	}
	if _, err := vm.Eval(fmt.Sprintf(`globalThis.soakBigSecond%d = globalThis.soakBig%d(2);`, i, i),
		"jit-soak-bg2.js"); err != nil {
		vm.Close()
		return err
	}
	second, err := vm.Global().Get(fmt.Sprintf("soakBigSecond%d", i))
	if err != nil || second.String() != "82" {
		vm.Close()
		return fmt.Errorf("second result=%v err=%v", second, err)
	}
	stats = vm.JITStats()
	if stats.NativeExecuted != 1 {
		vm.Close()
		return fmt.Errorf("installed background native code was not executed: %+v", stats)
	}
	if err := vm.Close(); err != nil {
		return err
	}
	agg.nativeCompiled += stats.NativeCompiled
	agg.nativeExecuted += stats.NativeExecuted
	agg.backgroundQueued += stats.BackgroundQueued
	agg.backgroundCompleted += stats.BackgroundCompleted
	return soakCheckBaseline(baseRegions, baseBytes, "background round")
}

// soakRoundPendingClose（phase 1）：大 IR 函数排队后台编译后，等编译 goroutine
// 已发布 RX 但结果仍留在 jitCompileDone 队列中时立即 Close —— 锻炼 closeJIT
// 的 pending-result 排空路径（而非正常 poll 路径），并断言内存回到基线。
func soakRoundPendingClose(agg *soakAggregates, i int, baseRegions, baseBytes uint64) error {
	vm, err := NewVM()
	if err != nil {
		return err
	}
	vm.ConfigureJIT(soakAutoConfig())
	source := fmt.Sprintf(`
		globalThis.soakPending%d = function(x) { return %s; };
		globalThis.soakPendingFirst%d = globalThis.soakPending%d(1);
	`, i, soakBigIR(), i, i)
	if _, err := vm.Eval(source, "jit-soak-pending.js"); err != nil {
		vm.Close()
		return err
	}
	first, err := vm.Global().Get(fmt.Sprintf("soakPendingFirst%d", i))
	if err != nil || first.String() != "81" {
		vm.Close()
		return fmt.Errorf("result=%v err=%v", first, err)
	}
	stats := vm.JITStats()
	if stats.BackgroundQueued != 1 || vm.jitPending != 1 {
		vm.Close()
		return fmt.Errorf("pending compile was not queued: pending=%d stats=%+v", vm.jitPending, stats)
	}
	// 等后台编译发布 RX 代码（regions/bytes 应高于基线），但故意不 poll，
	// 让结果停留在 channel 中，Close 走 pending 排空分支。
	vm.jitCompileWG.Wait()
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions <= baseRegions || bytes <= baseBytes {
		vm.Close()
		return fmt.Errorf("pending compile did not publish RX memory: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
	if err := vm.Close(); err != nil {
		return err
	}
	if vm.jitPending != 0 {
		return fmt.Errorf("pending drain left work behind: pending=%d", vm.jitPending)
	}
	agg.backgroundQueued += stats.BackgroundQueued
	return soakCheckBaseline(baseRegions, baseBytes, "pending-close round")
}

// soakRoundLRUEvict（phase 2）：1KB CodeCacheBytes 下逐个安装热点小函数，
// 同步 Native 编译会不断触发 reserveNativeCode 的 LRU 淘汰。循环安装直到
// 观察到 NativeEvictions 增长（对单函数 Native 尺寸不敏感，天然稳定），
// 并断言 NativeCodeBytes 始终 <= 缓存预算。
func soakRoundLRUEvict(agg *soakAggregates, i int, baseRegions, baseBytes uint64) error {
	vm, err := NewVM()
	if err != nil {
		return err
	}
	config := soakAutoConfig()
	config.CodeCacheBytes = 1024
	vm.ConfigureJIT(config)
	installed := 0
	for k := 0; k < 64; k++ {
		source := fmt.Sprintf(`
			function lruSoak%d_%d(x) { return x + %d; }
			globalThis.lruSoak%d_%d = lruSoak%d_%d(1);
		`, i, k, k, i, k, i, k)
		if _, err := vm.Eval(source, "jit-soak-lru.js"); err != nil {
			vm.Close()
			return err
		}
		got, err := vm.Global().Get(fmt.Sprintf("lruSoak%d_%d", i, k))
		if err != nil || got.String() != strconv.Itoa(k+1) {
			vm.Close()
			return fmt.Errorf("function=%d result=%v err=%v", k, got, err)
		}
		installed++
		if vm.JITStats().NativeEvictions > 0 {
			break
		}
	}
	stats := vm.JITStats()
	if stats.NativeEvictions == 0 {
		vm.Close()
		return fmt.Errorf("no LRU eviction after %d installs into %d-byte cache: %+v",
			installed, config.CodeCacheBytes, stats)
	}
	if stats.NativeCompiled == 0 {
		vm.Close()
		return fmt.Errorf("LRU round produced no native code: %+v", stats)
	}
	if stats.NativeCodeBytes > stats.CodeCacheLimit {
		vm.Close()
		return fmt.Errorf("native cache exceeded budget: %+v", stats)
	}
	if err := vm.Close(); err != nil {
		return err
	}
	agg.nativeCompiled += stats.NativeCompiled
	agg.nativeExecuted += stats.NativeExecuted
	agg.evictions += stats.NativeEvictions
	return soakCheckBaseline(baseRegions, baseBytes, "LRU round")
}

// soakRoundTrace（phase 3）：热点循环函数触发 Trace 编译与 Native Trace 执行。
// Threshold 必须钉死在 ^uint32(0)：若 Threshold=1，函数首次入口的
// noteJITBackedge 会立刻编译 leaf 并抢占执行路径，Trace 将永不触发；
// BackedgeThreshold 用 2（与既有 Trace 测试一致）让回边计数先累积到阈值。
// 512B 缓存 + 8 个循环函数保证 Trace 区域发生 LRU 淘汰。
func soakRoundTrace(agg *soakAggregates, i int, baseRegions, baseBytes uint64) error {
	vm, err := NewVM()
	if err != nil {
		return err
	}
	vm.ConfigureJIT(jit.Config{
		Mode:              jit.Auto,
		Threshold:         ^uint32(0),
		BackedgeThreshold: 2,
		CodeCacheBytes:    512,
		Stats:             true,
	})
	var source strings.Builder
	for k := 0; k < 8; k++ {
		fmt.Fprintf(&source, `
			function traceSoak%d_%d(n) {
				const marker = {};
				let total = 0;
				for (let i = 0; i < n; i++) total += i;
				return total;
			}
			globalThis.traceSoak%d_%d = traceSoak%d_%d(20);
		`, i, k, i, k, i, k)
	}
	if _, err := vm.Eval(source.String(), "jit-soak-trace.js"); err != nil {
		vm.Close()
		return err
	}
	for k := 0; k < 8; k++ {
		got, err := vm.Global().Get(fmt.Sprintf("traceSoak%d_%d", i, k))
		if err != nil || got.String() != "190" {
			vm.Close()
			return fmt.Errorf("trace function=%d result=%v err=%v", k, got, err)
		}
	}
	stats := vm.JITStats()
	if stats.NativeTracesCompiled == 0 || stats.NativeTracesExecuted == 0 {
		vm.Close()
		return fmt.Errorf("trace round produced no native trace: %+v", stats)
	}
	if stats.NativeEvictions == 0 {
		vm.Close()
		return fmt.Errorf("trace cache did not evict: %+v", stats)
	}
	if stats.NativeCodeBytes > stats.CodeCacheLimit {
		vm.Close()
		return fmt.Errorf("trace cache exceeded budget: %+v", stats)
	}
	if err := vm.Close(); err != nil {
		return err
	}
	agg.tracesCompiled += stats.NativeTracesCompiled
	agg.tracesExecuted += stats.NativeTracesExecuted
	agg.evictions += stats.NativeEvictions
	return soakCheckBaseline(baseRegions, baseBytes, "trace round")
}
