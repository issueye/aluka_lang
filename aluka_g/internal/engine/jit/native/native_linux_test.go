//go:build linux && amd64

package native

import (
	"bufio"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestPublishUsesWXRAndExecutes(t *testing.T) {
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	perms, err := linuxMappingPermissions(code.Entry())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(perms, "x") || strings.Contains(perms, "w") {
		t.Fatalf("page permissions = %q, want executable and not writable", perms)
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

func linuxMappingPermissions(addr uintptr) (string, error) {
	f, err := os.Open("/proc/self/maps")
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		rangeParts := strings.SplitN(fields[0], "-", 2)
		if len(rangeParts) != 2 {
			continue
		}
		start, startErr := strconv.ParseUint(rangeParts[0], 16, 64)
		end, endErr := strconv.ParseUint(rangeParts[1], 16, 64)
		if startErr == nil && endErr == nil && uint64(addr) >= start && uint64(addr) < end {
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", os.ErrNotExist
}
