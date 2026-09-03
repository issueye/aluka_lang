#!/usr/bin/env bash
# 对象 arena 原型实验（ADR object-arena-rejected 的复现脚本）。
# 生成 main.go 到临时目录并运行；不触碰仓库内容。
set -e
DIR=$(mktemp -d)
cd "$DIR"
cat > main.go <<'GOEOF'
package main

import (
	"fmt"
	"runtime"
	"time"
)

type objB struct{ v uintptr }

type objA struct {
	shape uintptr
	slots [3]*objB
}

func mallocMode(iters, keepRate int) (time.Duration, uint64) {
	var m1 runtime.MemStats
	runtime.GC()
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

func arenaMode(iters, keepRate, block int) (time.Duration, uint64) {
	var m1 runtime.MemStats
	runtime.GC()
	t0 := time.Now()
	allocator := make([]objA, block)
	used := 0
	var keep []*objA
	for i := 0; i < iters; i++ {
		if used == block {
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
	const iters = 3000000
	for _, rate := range []int{100, 1000} {
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
			fmt.Printf("keep=1/%d: malloc %v (heap %v) | arena(%d) %v (heap %v) 吞吐比 %.1fx RSS比 %.1fx\n",
				rate, bestA, heapA, block, bestB, heapB, float64(bestA)/float64(bestB), float64(heapB)/float64(heapA))
		}
	}
}
GOEOF
cat > go.mod <<'GOEOF'
module arenaexp

go 1.25
GOEOF
CGO_ENABLED=0 GOWORK=off go run .
rm -rf "$DIR"
