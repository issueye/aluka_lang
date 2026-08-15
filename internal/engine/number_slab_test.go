package engine

import (
	"sync"
	"testing"
)

// TestNumberSlabConcurrent 并发压力：每个数字必须精确持有自己的值
// （检测 slab 索引竞争导致的覆写/串值）。跨越多个 slab 换块边界。
func TestNumberSlabConcurrent(t *testing.T) {
	const goroutines = 8
	const per = numSlabBoxes / 4 // 每协程分配量确保多次换块
	vals := make([][]Value, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			local := make([]Value, per)
			for i := 0; i < per; i++ {
				local[i] = Number(float64(g<<32 | i))
			}
			vals[g] = local
		}(g)
	}
	wg.Wait()
	for g := 0; g < goroutines; g++ {
		for i := 0; i < per; i++ {
			want := float64(g<<32 | i)
			got, ok := vals[g][i].Float()
			if !ok || got != want {
				t.Fatalf("值被覆写: goroutine %d i=%d want=%v got=%v", g, i, want, got)
			}
		}
	}
}
