//go:build windows && amd64

package native

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPublishUsesWXRAndExecutes(t *testing.T) {
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(code.Entry(), &info, unsafe.Sizeof(info)); err != nil {
		t.Fatal(err)
	}
	if info.Protect != windows.PAGE_EXECUTE_READ {
		t.Fatalf("page protection = %#x, want PAGE_EXECUTE_READ", info.Protect)
	}
	frame := &Frame{}
	frame.Args[0], frame.Args[1] = 1.25, 2.5
	if status := code.Call(frame); status != 0 {
		t.Fatalf("status=%d", status)
	}
	if math.Abs(frame.Result-3.75) > 1e-12 {
		t.Fatalf("result=%v", frame.Result)
	}
}

func TestGeneratedCodeSurvivesConcurrentGC(t *testing.T) {
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	frame := &Frame{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			runtime.GC()
		}
	}()
	for i := 0; i < 100000; i++ {
		frame.Args[0], frame.Args[1] = float64(i), 0.5
		if status := code.Call(frame); status != 0 || frame.Result != float64(i)+0.5 {
			t.Fatalf("iteration=%d status=%d result=%v", i, status, frame.Result)
		}
	}
	wg.Wait()
}

func BenchmarkNativeAdd(b *testing.B) {
	code, err := Publish(AddF64Kernel())
	if err != nil {
		b.Fatal(err)
	}
	defer code.Close()
	frame := &Frame{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Args[0], frame.Args[1] = float64(i), 1
		_ = code.Call(frame)
	}
}

func TestPublishedCodeCanBeReleased(t *testing.T) {
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	if err := code.Close(); err != nil {
		t.Fatal(err)
	}
	if code.Entry() != 0 {
		t.Fatal("entry remains live after close")
	}
}
