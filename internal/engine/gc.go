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

import (
	"runtime"
	"sync"
	"weak"
)

// jsHeap 是引擎级 JS 对象堆（弱引用注册表）。
type jsHeap struct {
	mu      sync.Mutex
	objects map[weak.Pointer[objectValue]]struct{}
	alloc   int64 // 累计分配数（锁内访问）
}

// gcSweepEvery 控制注册表自动清扫频率：每分配这么多对象就清扫一次，
// 移除已被 Go GC 回收（weak.Value()==nil）的弱引用条目，防止注册表无限增长。
const gcSweepEvery = 4096

// jsHeapGlobal 是全局对象堆。
var jsHeapGlobal = &jsHeap{objects: make(map[weak.Pointer[objectValue]]struct{})}

// register 在 JS 对象创建时注册到堆（由 NewObject/NewArray/NewFunction 等调用）。
// 按阈值周期清扫注册表，避免长跑/高分配程序下 `objects` 随分配无限膨胀。
func register(obj *objectValue) {
	jsHeapGlobal.mu.Lock()
	jsHeapGlobal.objects[weak.Make(obj)] = struct{}{}
	jsHeapGlobal.alloc++
	if jsHeapGlobal.alloc%gcSweepEvery == 0 {
		jsHeapGlobal.sweepLocked()
	}
	jsHeapGlobal.mu.Unlock()
}

// sweepLocked 移除已由 Go GC 回收（weak.Value()==nil）的弱引用条目。
// 调用方须持有 mu。
func (h *jsHeap) sweepLocked() {
	for w := range h.objects {
		if w.Value() == nil {
			delete(h.objects, w)
		}
	}
}

// HeapStats 是 GC 统计结果。
type HeapStats struct {
	AllocCount  int64 // 累计分配对象数
	LiveCount   int64 // 当前存活对象数（Go 仍引用的弱引用）
	MarkedCount int64 // 标记阶段从根集可达的对象数
}

// GC 触发标记-清除并返回统计。roots 为引擎根值（全局对象等）。
func GC(roots []Value) HeapStats {
	// 标记：从根集遍历对象图。
	marked := markFromRoots(roots)

	// 清除：移除 Go GC 已回收的弱引用，统计存活数。
	jsHeapGlobal.mu.Lock()
	live := int64(0)
	for w := range jsHeapGlobal.objects {
		if w.Value() == nil {
			delete(jsHeapGlobal.objects, w)
		} else {
			live++
		}
	}
	alloc := jsHeapGlobal.alloc
	jsHeapGlobal.mu.Unlock()

	// 触发 Go 物理回收。
	runtime.GC()

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
