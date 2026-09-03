package main

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

// 模拟 gcPressure 的对象形态：外对象(3属性 120B) + 内对象(1属性 120B) + 数组(176B)
// A: 普通 mallocgc 分配（现状）；B: bump arena（块内连续分配，模拟帧级 arena）

type objB struct{ v uintptr } // 被引用目标（模拟数字/内对象）

type objA struct {
	shape uintptr
	slots [3]*objB // 真实指针槽：死亡元素的指针也会保活目标，模拟级联
}

func mallocMode(iters int, keepRate int) (time.Duration, uint64) {
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	t0 := time.Now()
	var keep []*objA
	for i := 0; i < iters; i++ {
		o := &objA{shape: uintptr(i)}
		o.slots[0] = &objB{v: uintptr(i)}
		if i%keepRate == 0 {
			keep = append(keep, o)
		}
	}
	el := time.Since(t0)
	runtime.GC()
	runtime.ReadMemStats(&m1)
	runtime.KeepAlive(keep)
	return el, m1.HeapAlloc
}

type objArena struct{ buf []objA }

// arenaMode 真实 bump 语义：块创建一次，块内连续分配，块满才建下一块。
func arenaMode(iters, keepRate, block int) (time.Duration, uint64) {
	var m1 runtime.MemStats
	allocator := make([]objA, block)
	used := 0
	t0 := time.Now()
	var keep []*objA
	for i := 0; i < iters; i++ {
		if used == block {
			m1 = runtime.MemStats{}
			allocator = make([]objA, block)
			used = 0
		}
		o := &allocator[used]
		used++
		o.shape = uintptr(i)
		o.slots[0] = &objB{v: uintptr(i)}
		if i%keepRate == 0 {
			keep = append(keep, o)
		}
	}
	el := time.Since(t0)
	runtime.GC()
	runtime.ReadMemStats(&m1)
	runtime.KeepAlive(keep)
	runtime.KeepAlive(allocator)
	return el, m1.HeapAlloc
}

func main() {
	fmt.Println("objA size:", unsafe.Sizeof(objA{}))
	const iters = 3000000
	for _, rate := range []int{100, 1000} {
		// 冷却两轮取最小值
		mallocMode(iters/10, rate)
		bestA := time.Duration(1 << 62)
		var heapA uint64
		for k := 0; k < 3; k++ {
			el, h := mallocMode(iters, rate)
			if el < bestA {
				bestA, heapA = el, h
			}
		}
		for _, block := range []int{32, 128} {
			arenaMode(iters/10, rate, block)
			bestB := time.Duration(1 << 62)
			var heapB uint64
			for k := 0; k < 3; k++ {
				el, h := arenaMode(iters, rate, block)
				if el < bestB {
					bestB, heapB = el, h
				}
			}
			fmt.Printf("keep=1/%d  iters=%d\n  malloc: %v  heapAfterGC=%v\n  arena(%d/块): %v  heapAfterGC=%v  吞吐比=%.1fx  RSS比=%.1fx\n",
				rate, iters, bestA, heapA, block, bestB, heapB, float64(bestA)/float64(bestB), float64(heapB)/float64(heapA))
		}
	}
}
