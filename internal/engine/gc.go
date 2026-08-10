package engine

// 自研 GC（开发计划 1B.6）。
//
// 架构说明（纯 Go 引擎的务实边界）：
//   - 引擎创建的 JS 对象（objectValue/ArrayValue/functionValue/BufferValue）
//     经 register 注册到全局 jsHeap（用 Go 1.24+ 的 weak.Pointer 弱引用，
//     不阻止 Go GC 回收）。
//   - GC() 执行三色标记-清除：
//     标记阶段从根集（全局对象等）沿对象图 DFS，收集可达对象；
//     清除阶段移除 Go GC 已回收（weak.Value() == nil）的弱引用。
//   - 底层物理内存回收由 Go runtime 完成（Go 无法脱离 runtime 手动释放）；
//     自研 GC 提供对象图遍历验证、存活统计与显式触发（全局 gc()）。
//   - 分配计数与阈值可在内存压力场景自动触发。
//   - 周期性归还 OS 内存：Go runtime 回收对象后并不立即把堆页归还给 OS，
//     长跑/高分配程序 RSS 单调增长。register 的周期 sweep 之后按更高阈值
//     调用 runtime.GC() + debug.FreeOSMemory()，把已回收页归还 OS。

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"weak"
)

// jsHeap 是引擎级 JS 对象堆（弱引用注册表）。
type jsHeap struct {
	mu      sync.Mutex
	objects map[weak.Pointer[objectValue]]struct{}
	sweeps  int64 // 累计 sweep 次数（锁内访问）
}

// gcSweepEvery 控制注册表自动清扫频率：每分配这么多对象就清扫一次，
// 移除已被 Go GC 回收（weak.Value()==nil）的弱引用条目，防止注册表无限增长。
const gcSweepEvery = 4096

// freeOSEverySweep 控制周期性归还 OS 内存的频率：每完成这么多次 sweep
// （即每 gcSweepEvery*freeOSEverySweep 次对象分配）触发一次
// runtime.GC() + debug.FreeOSMemory()。默认 16 → 约每 65K 分配归还一次，
// 兼顾 RSS 稳定与 FreeOSMemory 的 syscall 成本。可由环境变量
// ALUKA_FREEOS_INTERVAL 覆盖（<=0 禁用周期归还）。
var freeOSEverySweep = int64(16)

// allocCount 是累计对象分配数（原子累加，无需持锁/无需 map）。
// 历史上挂在 jsHeap.alloc（锁内），但计数本身不需要弱引用 map；
// 拆出后让 register 在未启用监控时完全跳过 map 插入。
var allocCount atomic.Int64

func init() {
	if v := os.Getenv("ALUKA_FREEOS_INTERVAL"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			freeOSEverySweep = n
		}
	}
}

// jsHeapGlobal 是全局对象堆。
var jsHeapGlobal = &jsHeap{objects: make(map[weak.Pointer[objectValue]]struct{})}

// register 在 JS 对象创建时注册到堆（由 NewObject/NewArray/NewFunction 等调用）。
//
// 弱引用 map 注册仅在监控（--monitor）启用时进行：维护"全量对象→弱引用"
// 映射的唯一目的是供 GC()/monitor 统计存活对象数（liveCount），它对正确性
// 没有任何影响——标记-清除靠 markFromRoots 从根集遍历。对 200K 对象，弱引用
// map 本身约占 9-12MB 并产生等量的 register/weak 分配，是默认运行时的纯开销。
// 监控关闭时跳过 map 插入，只累加原子计数器并驱动周期 sweep/FreeOS。
func register(obj *objectValue) {
	alloc := allocCount.Add(1)
	// 周期 sweep 与 FreeOS 仅依赖分配计数，与是否注册弱引用无关。
	if alloc%gcSweepEvery == 0 {
		var needFree bool
		jsHeapGlobal.mu.Lock()
		// 仅在 map 非空（监控启用过）时才清扫。
		if len(jsHeapGlobal.objects) > 0 {
			jsHeapGlobal.sweepLocked()
		}
		jsHeapGlobal.sweeps++
		if freeOSEverySweep > 0 && jsHeapGlobal.sweeps%freeOSEverySweep == 0 {
			needFree = true
		}
		jsHeapGlobal.mu.Unlock()
		if needFree {
			runtime.GC()
			debug.FreeOSMemory()
		}
	}
	// 弱引用注册仅在监控启用时进行（liveCount 统计用）。
	if metricsEnabled.Load() {
		jsHeapGlobal.mu.Lock()
		jsHeapGlobal.objects[weak.Make(obj)] = struct{}{}
		jsHeapGlobal.mu.Unlock()
	}
	BumpAlloc() // 监控计数器（gated）
}

// sweepLocked 移除已由 Go GC 回收（weak.Value()==nil）的弱引用条目。
// 调用方须持有 mu。同时清理所有已注册 WeakMap（builtin 包关联存储）的
// 失效条目，避免 JS 对象死亡后 Go 资源条目残留。
func (h *jsHeap) sweepLocked() {
	for w := range h.objects {
		if w.Value() == nil {
			delete(h.objects, w)
		}
	}
	sweepAllWeakMaps()
}

// HeapStats 是 GC 统计结果。
type HeapStats struct {
	AllocCount  int64 // 累计分配对象数
	LiveCount   int64 // 当前存活对象数（Go 仍引用的弱引用）
	MarkedCount int64 // 标记阶段从根集可达的对象数
}

// FreeOSMemory 触发一次 Go GC 并把已回收堆页归还 OS（madvise）。
// 与 GC() 不同，它不做对象图标记-清除，仅用于在已知"大批临时分配已完成"
// 的时机（如启动期全局对象/内置模块注册完毕、payload 反序列化后）把
// Go runtime 持有但未归还的内存释放掉，降低启动 RSS。开销为一次 STW GC
// + FreeOSMemory syscall，仅在低频路径调用。
func FreeOSMemory() {
	runtime.GC()
	debug.FreeOSMemory()
	sweepAllWeakMaps()
}

// GC 触发标记-清除并返回统计。roots 为引擎根值（全局对象等）。
func GC(roots []Value) HeapStats {
	// 标记：从根集遍历对象图。
	marked := markFromRoots(roots)

	// 清除：移除 Go GC 已回收的弱引用，统计存活数。
	// 弱引用 map 仅在监控启用（--monitor）时填充；未启用时 map 为空，
	// liveCount 回退到可达对象数（marked），语义上更合理且零额外开销。
	live := int64(len(marked))
	jsHeapGlobal.mu.Lock()
	if len(jsHeapGlobal.objects) > 0 {
		live = 0
		for w := range jsHeapGlobal.objects {
			if w.Value() == nil {
				delete(jsHeapGlobal.objects, w)
			} else {
				live++
			}
		}
	}
	jsHeapGlobal.mu.Unlock()
	alloc := allocCount.Load()

	// 触发 Go 物理回收。
	runtime.GC()
	// Return released pages to the OS for long-running TUI processes. This is
	// intentionally part of explicit gc(), not the normal allocation path.
	debug.FreeOSMemory()
	// GC 后 weak.Pointer 失效，清理所有 WeakMap 的残留条目。
	sweepAllWeakMaps()

	return HeapStats{
		AllocCount:  alloc,
		LiveCount:   live,
		MarkedCount: int64(len(marked)),
	}
}

// markFromRoots 从根集沿对象图 DFS 标记可达对象。
// 遍历 own 属性（shape.slots）与数组元素。
func markFromRoots(roots []Value) map[*objectValue]bool {
	marked := make(map[*objectValue]bool)
	var visit func(v Value)
	visit = func(v Value) {
		if v == nil {
			return
		}
		var o *objectValue
		switch t := v.(type) {
		case *objectValue:
			o = t
		case *ArrayValue:
			o = t.objectValue
			for _, e := range t.elems {
				visit(e)
			}
		case *functionValue:
			o = t.objectValue
		case *BufferValue:
			o = t.objectValue
		case *DateValue:
			o = t.objectValue
		default:
			return
		}
		if o == nil || marked[o] {
			return
		}
		marked[o] = true
		// 遍历 own 属性引用。
		for i, name := range o.shape.names {
			if o.deleted != nil && o.deleted[name] {
				continue
			}
			if i < len(o.slots) {
				visit(o.slots[i])
			}
		}
	}
	for _, r := range roots {
		visit(r)
	}
	return marked
}
