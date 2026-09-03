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
	"time"
	"weak"
)

// jsHeap 是引擎级 JS 对象堆（弱引用注册表）。
type jsHeap struct {
	mu      sync.Mutex
	objects map[weak.Pointer[objectValue]]struct{}
	// sweepAt 是下次清扫的条目数阈值（每次清扫后按存活规模重设，见 sweepLocked）。
	sweepAt int
}

// gcSweepEvery 是注册表清扫的起始阈值：条目数涨到它就清扫一次，移除已被
// Go GC 回收（weak.Value()==nil）的弱引用条目，防止注册表无限增长。清扫后
// 阈值按存活规模上调（sweepLocked），使全表扫描的成本对分配次数摊还。
const gcSweepEvery = 4096

// freeOSAllocThreshold 控制高分配压力下通知后台 freeOS 的频率：每分配这么
// 多对象 try-send 一次信号。默认 32*gcSweepEvery ≈ 131K，让重负载（如对话
// 上下文构建）期间后台 goroutine 及时归还 OS，而轻负载只靠定时器周期触发。
const freeOSAllocThreshold = 32 * gcSweepEvery

// allocCount 是累计对象分配数（原子累加，无需持锁/无需 map）。
var allocCount atomic.Int64

// jsHeapGlobal 是全局对象堆。
var jsHeapGlobal = &jsHeap{objects: make(map[weak.Pointer[objectValue]]struct{}), sweepAt: gcSweepEvery}

// register 在 JS 对象创建时注册到堆（由 NewObject/NewArray/NewFunction 等调用）。
//
// 弱引用 map 注册仅在监控（--monitor）启用时进行：维护"全量对象→弱引用"
// 映射的唯一目的是供 GC()/monitor 统计存活对象数（liveCount），它对正确性
// 没有任何影响——标记-清除靠 markFromRoots 从根集遍历。对 200K 对象，弱引用
// map 本身约占 9-12MB 并产生等量的 register/weak 分配，是默认运行时的纯开销。
// 监控关闭时跳过 map 插入，只累加原子计数器。
//
// 归还 OS 内存（FreeOSMemory）不在热路径同步执行——那会因 runtime.GC() 的
// STW + FreeOSMemory syscall 阻塞分配线程，在对话/交互场景造成可感知卡顿。
// 改由后台 freeOSLoop goroutine 按空闲周期触发（见 startFreeOSLoop）。
func register(obj *objectValue) {
	allocCount.Add(1)
	// 弱引用注册与清扫仅在监控启用时进行（liveCount 统计用）。
	if metricsEnabled.Load() {
		BumpAlloc()
		jsHeapGlobal.mu.Lock()
		jsHeapGlobal.objects[weak.Make(obj)] = struct{}{}
		if len(jsHeapGlobal.objects) >= jsHeapGlobal.sweepAt {
			jsHeapGlobal.sweepLocked()
		}
		jsHeapGlobal.mu.Unlock()
	}
}

// freeOSInterval 控制后台归还 OS 内存的周期。默认 2s：在对话/交互的间隙
// 触发 GC+FreeOSMemory，把已回收页归还 OS，而不阻塞分配热路径。可由环境
// 变量 ALUKA_FREEOS_INTERVAL 覆盖：
//
//	>0 ：以秒为单位的周期（如 3 = 每 3 秒）
//	<=0：禁用后台归还（RSS 将单调增长，但零延迟开销）
var freeOSInterval = 2 * time.Second

func init() {
	if v := os.Getenv("ALUKA_FREEOS_INTERVAL"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			switch {
			case n <= 0:
				freeOSInterval = 0 // 禁用
			default:
				freeOSInterval = time.Duration(n) * time.Second
			}
		}
	}
	// 启动后台归还 OS 内存 goroutine（sync.Once 内部守卫，禁用时直接返回）。
	startFreeOSLoop()
}

// startFreeOSLoop 启动后台归还 OS 内存 goroutine。进程生命周期内只启一次
// （sync.Once）。goroutine 在定时器到期时执行 runtime.GC()+debug.FreeOSMemory()
// ——STW 发生在后台空闲 goroutine 调度点，不阻塞 register 热路径。
func startFreeOSLoop() {
	freeOSLoopOnce.Do(func() {
		if freeOSInterval <= 0 {
			return // 禁用
		}
		go func() {
			ticker := time.NewTicker(freeOSInterval)
			defer ticker.Stop()
			for range ticker.C {
				runtime.GC()
				debug.FreeOSMemory()
			}
		}()
	})
}

// freeOSLoopOnce 保证后台 goroutine 只启动一次。
var freeOSLoopOnce sync.Once

// sweepLocked 移除已由 Go GC 回收（weak.Value()==nil）的弱引用条目。
// 调用方须持有 mu。同时清理所有已注册 WeakMap（builtin 包关联存储）的
// 失效条目，避免 JS 对象死亡后 Go 资源条目残留。
func (h *jsHeap) sweepLocked() {
	for w := range h.objects {
		if w.Value() == nil {
			delete(h.objects, w)
		}
	}
	// 下次清扫阈值设为存活数的两倍：清扫是 O(表长) 全扫，阈值必须随存活
	// 规模增长才能把成本摊还到分配次数上。固定步长在存活数远超步长时退化
	// 为"每几次分配就全表扫一遍"（监控模式下 60 万存活对象实测慢 20 倍）。
	if next := 2 * len(h.objects); next > gcSweepEvery {
		h.sweepAt = next
	} else {
		h.sweepAt = gcSweepEvery
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

// markFromRoots 从根集沿对象图遍历标记可达对象（迭代式 worklist 遍历，防栈溢出）。
// 遍历 own 属性（shape.slots）、原型链（proto）、数组元素与 Accessor。
func markFromRoots(roots []Value) map[*objectValue]bool {
	marked := make(map[*objectValue]bool)
	worklist := make([]Value, 0, len(roots)*4)
	worklist = append(worklist, roots...)

	for len(worklist) > 0 {
		idx := len(worklist) - 1
		v := worklist[idx]
		worklist = worklist[:idx]
		if v == nil {
			continue
		}

		var o *objectValue
		switch t := v.(type) {
		case *objectValue:
			o = t
		case *ArrayValue:
			o = &t.objectValue
			for _, e := range t.elems {
				if e != nil {
					worklist = append(worklist, e)
				}
			}
		case *functionValue:
			o = &t.objectValue
		case *BufferValue:
			o = t.objectValue
		case *DateValue:
			o = t.objectValue
		case *AccessorValue:
			if t.Getter != nil {
				worklist = append(worklist, t.Getter)
			}
			if t.Setter != nil {
				worklist = append(worklist, t.Setter)
			}
			continue
		default:
			continue
		}

		if o == nil || marked[o] {
			continue
		}
		marked[o] = true

		if o.proto != nil {
			worklist = append(worklist, o.proto)
		}

		// 遍历 own 属性引用。
		for i, name := range o.shape.names {
			if o.isDeleted(name) {
				continue
			}
			if i < len(o.slots) && o.slots[i] != nil {
				worklist = append(worklist, o.slots[i])
			}
		}
	}
	return marked
}
