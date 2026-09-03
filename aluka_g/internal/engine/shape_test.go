package engine

import (
	"fmt"
	"sync"
	"testing"
)

func TestShapeTransitionsAreConcurrentSafe(t *testing.T) {
	const (
		workers    = 32
		iterations = 200
	)

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				o := NewObject()
				for _, key := range []string{"name", "length", "prototype", fmt.Sprintf("worker%d", worker%4)} {
					if err := o.Set(key, IntValue(i)); err != nil {
						t.Errorf("Set(%q): %v", key, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// TestShapeTransitionDepthLimit 验证超深 transition 不挂全局树（防 O(N²) OOM）：
// 属性数超过 maxShapeProps 后 transition 仍返回携带完整 names/index 的 Shape，
// 属性查找正确。
func TestShapeTransitionDepthLimit(t *testing.T) {
	s := rootShape
	for i := 0; i < 500; i++ {
		s = s.transition("k" + string(rune('a'+i%26)) + string(rune('0'+i%10)))
	}
	if got := s.NumProps(); got != 500 {
		t.Errorf("NumProps = %d, want 500", got)
	}
	if idx, ok := s.lookup("k" + string(rune('a'+499%26)) + string(rune('0'+499%10))); !ok || idx != 499 {
		t.Errorf("last prop lookup = (%d,%v), want (499,true)", idx, ok)
	}
}
