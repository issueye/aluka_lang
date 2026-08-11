//go:build !amd64 || (!windows && !linux)

// R2-7: VM-level validation on unsupported platforms.
//
// Compiled exactly where the native stubs are active. These tests drive the
// real interpreter VM (imported from package jit_test, so no production
// changes were needed and no seam is required: on these platforms
// Publish/CompileNative genuinely return native.ErrUnsupported) and prove
// the R2-7 contract end to end:
//   - Auto mode attempts native compilation, is rejected with the platform
//     gate ("native jit is not supported on this platform"), and keeps
//     executing the Quick / Tier-0 tiers: Executed/TracesExecuted grow,
//     NativeCompiled/NativeTracesCompiled stay 0.
//   - Results are identical to JIT Off.
//   - jitNativeBytes (stats.NativeCodeBytes) stays 0 and the package-global
//     RX counters stay at baseline across the run, a mid-lifecycle
//     reconfigure, and VM.Close: no executable memory is ever requested.
//   - The rejection record (tier "native" + reason) distinguishes the
//     platform gate from ordinary compile failures, which keep their own
//     messages (see fallback_unsupported_test.go).
//
// No t.Skip anywhere: every assertion executes on unsupported platforms.

package jit_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// unsupportedSumSource is the hot numeric kernel used across these tests.
// Its leaf IR is small (< 128 instrs) so Auto compiles natively through the
// synchronous installNative path (no background queue) and deterministically
// records one rejection per function.
const unsupportedSumSource = `
	function sum(n) {
		let total = 0;
		for (let i = 0; i < n; i++) total += i;
		return total;
	}
	globalThis.fbSum = sum(500);
	globalThis.fbSum2 = sum(123);
`

func unsupportedCheckResult(t *testing.T, vm *interpreter.VM, name, want string) {
	t.Helper()
	got, err := vm.Global().Get(name)
	if err != nil || got.String() != want {
		t.Fatalf("%s = %v (err=%v), want %s", name, got, err, want)
	}
}

func TestUnsupportedPlatformAutoFallsBackToQuick(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()

	auto := fallbackRunVM(t, jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true},
		unsupportedSumSource, "fb-unsup-auto.js")
	stats := auto.JITStats()
	if stats.NativeRejected == 0 {
		t.Fatalf("Auto must attempt native compilation on an unsupported platform: %+v", stats)
	}
	if stats.NativeCompiled != 0 || stats.NativeCodeBytes != 0 {
		t.Fatalf("native state appeared on an unsupported platform: compiled=%d bytes=%d: %+v",
			stats.NativeCompiled, stats.NativeCodeBytes, stats)
	}
	if stats.NativeTracesCompiled != 0 || stats.TracesCompiled != 0 {
		t.Fatalf("no traces expected for 623 backedges below the default threshold: %+v", stats)
	}
	if stats.Executed == 0 {
		t.Fatalf("Quick tier never executed after platform rejection: %+v", stats)
	}
	if !strings.Contains(stats.LastNativeError, "not supported on this platform") {
		t.Fatalf("LastNativeError = %q, want platform-gate message", stats.LastNativeError)
	}
	// The rejection record distinguishes the platform gate by tier + reason.
	foundGate := false
	for _, r := range stats.RejectionReasons {
		if r.Tier == "native" && strings.Contains(r.Reason, "not supported on this platform") {
			foundGate = true
		}
	}
	if !foundGate {
		t.Fatalf("platform-gate rejection record missing: %+v", stats.RejectionReasons)
	}
	unsupportedCheckResult(t, auto, "fbSum", "124750")
	unsupportedCheckResult(t, auto, "fbSum2", "7503")

	off := fallbackRunVM(t, jit.Config{Mode: jit.Off, Stats: true},
		unsupportedSumSource, "fb-unsup-off.js")
	unsupportedCheckResult(t, off, "fbSum", "124750")
	unsupportedCheckResult(t, off, "fbSum2", "7503")
	off.Close()

	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("Auto fallback on unsupported platform changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
	auto.Close()
	regions, bytes = jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("Close after Auto fallback changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
}

func TestUnsupportedPlatformTraceNativeRejected(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()

	vm := fallbackRunVM(t, jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, Stats: true},
		`function sum(n) {
			let total = 0;
			for (let i = 0; i < n; i++) total += i;
			return total;
		}
		globalThis.fbTraceSum = sum(2000);`, "fb-unsup-trace.js")
	stats := vm.JITStats()
	if stats.NativeTracesRejected == 0 {
		t.Fatalf("Auto trace compilation must be rejected on an unsupported platform: %+v", stats)
	}
	if stats.NativeTracesCompiled != 0 || stats.NativeCodeBytes != 0 {
		t.Fatalf("native trace state appeared on an unsupported platform: %+v", stats)
	}
	if stats.TracesCompiled == 0 || stats.TracesExecuted == 0 {
		t.Fatalf("Quick trace tier never executed on unsupported platform: %+v", stats)
	}
	if !strings.Contains(stats.LastNativeError, "not supported on this platform") {
		t.Fatalf("LastNativeError = %q, want platform-gate message", stats.LastNativeError)
	}
	unsupportedCheckResult(t, vm, "fbTraceSum", "1999000")

	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("trace fallback changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
	vm.Close()
	regions, bytes = jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("Close after trace fallback changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
}

func TestUnsupportedPlatformLifecycleKeepsRXZero(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()
	for i := 0; i < 4; i++ {
		vm := fallbackRunVM(t, jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true},
			fmt.Sprintf(`function hot%d(x) { return x * 2 + %d; } globalThis.life%d = hot%d(21);`, i, i, i, i),
			"fb-life-1.js")
		stats := vm.JITStats()
		if stats.NativeRejected == 0 || stats.NativeCompiled != 0 || stats.NativeCodeBytes != 0 {
			t.Fatalf("iteration %d: unexpected native state: %+v", i, stats)
		}
		unsupportedCheckResult(t, vm, fmt.Sprintf("life%d", i), fmt.Sprint(42+i))

		// Reconfigure mid-lifecycle: previous compiled state is dropped and
		// native accounting must remain zero.
		vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, BackedgeThreshold: 1, Stats: true})
		if _, err := vm.Eval(fmt.Sprintf(`globalThis.life%d = hot%d(0);`, i, i), "fb-life-2.js"); err != nil {
			t.Fatal(err)
		}
		unsupportedCheckResult(t, vm, fmt.Sprintf("life%d", i), fmt.Sprint(i))
		if stats := vm.JITStats(); stats.NativeCodeBytes != 0 {
			t.Fatalf("iteration %d: native bytes after reconfigure: %+v", i, stats)
		}
		regions, bytes := jitnative.LiveExecutableMemory()
		if regions != baseRegions || bytes != baseBytes {
			t.Fatalf("iteration %d: live RX changed mid-lifecycle: live=(%d,%d) baseline=(%d,%d)",
				i, regions, bytes, baseRegions, baseBytes)
		}
		if err := vm.Close(); err != nil {
			t.Fatal(err)
		}
		regions, bytes = jitnative.LiveExecutableMemory()
		if regions != baseRegions || bytes != baseBytes {
			t.Fatalf("iteration %d: Close changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
				i, regions, bytes, baseRegions, baseBytes)
		}
	}
}
