//go:build amd64 && (windows || linux)

package interpreter

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

func TestAutoJITShortLivedVMsReleaseExecutableMemory(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	for i := 0; i < 32; i++ {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
		source := fmt.Sprintf(`
			function shortLived%d(x) { return x * 2 + %d; }
			globalThis.shortLivedResult = shortLived%d(21);
		`, i, i, i)
		if _, err := vm.Eval(source, "jit-short-lived.js"); err != nil {
			t.Fatal(err)
		}
		result, err := vm.Global().Get("shortLivedResult")
		if err != nil || result.String() != fmt.Sprint(42+i) {
			t.Fatalf("iteration=%d result=%v err=%v", i, result, err)
		}
		stats := vm.JITStats()
		if stats.NativeCompiled != 1 || stats.NativeExecuted != 1 || stats.NativeCodeBytes == 0 {
			t.Fatalf("iteration=%d native code was not installed: %+v", i, stats)
		}
		regions, bytes := jitnative.LiveExecutableMemory()
		if regions <= baseRegions || bytes <= baseBytes {
			t.Fatalf("iteration=%d live memory=(%d,%d), baseline=(%d,%d)", i, regions, bytes, baseRegions, baseBytes)
		}
		if err := vm.Close(); err != nil {
			t.Fatal(err)
		}
		if i%4 == 0 {
			runtime.GC()
		}
		regions, bytes = jitnative.LiveExecutableMemory()
		if regions != baseRegions || bytes != baseBytes {
			t.Fatalf("iteration=%d close leaked memory=(%d,%d), baseline=(%d,%d)", i, regions, bytes, baseRegions, baseBytes)
		}
	}
}

func TestAutoJITCloseReleasesPendingBackgroundCompile(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	var expression strings.Builder
	expression.WriteString("x")
	for i := 0; i < 80; i++ {
		expression.WriteString(" + 1")
	}
	for i := 0; i < 8; i++ {
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
		source := fmt.Sprintf(`
			globalThis.pendingJIT = function(x) { return %s; };
			globalThis.pendingResult = globalThis.pendingJIT(%d);
		`, expression.String(), i)
		if _, err := vm.Eval(source, "jit-pending-close.js"); err != nil {
			t.Fatal(err)
		}
		if stats := vm.JITStats(); stats.BackgroundQueued != 1 || vm.jitPending != 1 {
			t.Fatalf("iteration=%d background compile was not queued: pending=%d stats=%+v", i, vm.jitPending, stats)
		}

		// Let compilation publish RX code, but leave the result queued so Close
		// exercises the pending-result drain rather than the normal poll path.
		vm.jitCompileWG.Wait()
		regions, bytes := jitnative.LiveExecutableMemory()
		if regions <= baseRegions || bytes <= baseBytes {
			t.Fatalf("iteration=%d pending compile did not publish RX memory: live=(%d,%d) baseline=(%d,%d)",
				i, regions, bytes, baseRegions, baseBytes)
		}
		if err := vm.Close(); err != nil {
			t.Fatal(err)
		}
		regions, bytes = jitnative.LiveExecutableMemory()
		if vm.jitPending != 0 || regions != baseRegions || bytes != baseBytes {
			t.Fatalf("iteration=%d pending close leaked: pending=%d live=(%d,%d) baseline=(%d,%d)",
				i, vm.jitPending, regions, bytes, baseRegions, baseBytes)
		}
	}
	runtime.GC()
}
