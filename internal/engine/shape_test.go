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
