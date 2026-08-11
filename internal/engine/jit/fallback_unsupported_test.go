// R2-7: VM-level fallback validation, platform-independent.
//
// These tests run on every platform (package jit_test imports the
// interpreter VM). They force a deterministic native-compile rejection that
// is independent of the platform backend: a local that is not proven numeric
// fails native lowering ("jit: native local N is not a proven number") in
// compileNative, before any machine code exists. On unsupported platforms
// the same exercise rejects with the platform gate instead; the
// build-tag-gated fallback_unsupported_platform_test.go covers that case.
//
// Assertions here are real on every platform (no t.Skip):
//   - Auto with a rejected native compile keeps executing the Quick tier
//     (Executed grows, NativeCompiled stays 0).
//   - Results are identical to JIT Off.
//   - jitNativeBytes (stats.NativeCodeBytes) stays 0 and the package-global
//     RX counters (jitnative.LiveExecutableMemory) stay at baseline across
//     the run, a reconfigure, and VM.Close.
//   - The rejection reason is the ordinary lowering error, NOT the platform
//     gate ("not supported on this platform"), so the two are
//     distinguishable.

package jit_test

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// fallbackGuardedSource: `y` is only assigned on one branch, so the leaf's
// `y + 1` cannot be proven numeric by native lowering on ANY platform.
// guarded(1) -> 6, guarded(0) -> 1 (this engine coerces undefined to 0 in
// arithmetic, identically in every tier — verified against Off/Quick/Auto).
const fallbackGuardedSource = `
	function guarded(x) { let y; if (x > 0) y = 5; return y + 1; }
	globalThis.fbPos = guarded(1);
	globalThis.fbNeg = guarded(0);
`

func fallbackRunVM(t *testing.T, config jit.Config, source, filename string) *interpreter.VM {
	t.Helper()
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	vm.ConfigureJIT(config)
	if _, err := vm.Eval(source, filename); err != nil {
		t.Fatal(err)
	}
	return vm
}

func fallbackCheckResults(t *testing.T, vm *interpreter.VM, wantPos, wantNeg string) {
	t.Helper()
	for name, want := range map[string]string{"fbPos": wantPos, "fbNeg": wantNeg} {
		got, err := vm.Global().Get(name)
		if err != nil || got.String() != want {
			t.Fatalf("%s = %v (err=%v), want %s", name, got, err, want)
		}
	}
}

func TestFallbackAutoUsesQuickWhenNativeRejected(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()

	auto := fallbackRunVM(t, jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true},
		fallbackGuardedSource, "fb-auto.js")
	stats := auto.JITStats()
	if stats.NativeRejected != 1 {
		t.Fatalf("NativeRejected = %d, want exactly 1 leaf rejection: %+v", stats.NativeRejected, stats)
	}
	if stats.NativeCompiled != 0 || stats.NativeCodeBytes != 0 {
		t.Fatalf("native state appeared despite rejection: compiled=%d bytes=%d: %+v",
			stats.NativeCompiled, stats.NativeCodeBytes, stats)
	}
	if stats.Executed == 0 {
		t.Fatalf("Quick tier never executed after native rejection: %+v", stats)
	}
	if !strings.Contains(stats.LastNativeError, "not a proven number") {
		t.Fatalf("LastNativeError = %q, want ordinary lowering error", stats.LastNativeError)
	}
	foundPlatformGate := false
	for _, r := range stats.RejectionReasons {
		if strings.Contains(r.Reason, "not supported on this platform") {
			foundPlatformGate = true
		}
	}
	if foundPlatformGate {
		t.Fatalf("ordinary lowering rejection must not be reported as the platform gate: %+v", stats.RejectionReasons)
	}
	fallbackCheckResults(t, auto, "6", "1")

	off := fallbackRunVM(t, jit.Config{Mode: jit.Off, Stats: true},
		fallbackGuardedSource, "fb-off.js")
	fallbackCheckResults(t, off, "6", "1")
	if st := off.JITStats(); st.Executed != 0 {
		t.Fatalf("Off mode must not execute JIT tiers: %+v", st)
	}
	off.Close()

	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("Auto fallback changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
	auto.Close()
	regions, bytes = jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("Close after Auto fallback changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
}

func TestFallbackReconfigureAndCloseKeepAccounting(t *testing.T) {
	baseRegions, baseBytes := jitnative.LiveExecutableMemory()

	vm := fallbackRunVM(t, jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true},
		fallbackGuardedSource, "fb-reconfig-1.js")
	if stats := vm.JITStats(); stats.NativeRejected == 0 || stats.NativeCodeBytes != 0 {
		t.Fatalf("expected rejected native and zero native bytes: %+v", stats)
	}

	// Reconfigure to Quick: clears compiled state; native must stay absent.
	vm.ConfigureJIT(jit.Config{Mode: jit.Quick, Threshold: 1, Stats: true})
	if _, err := vm.Eval(fallbackGuardedSource, "fb-reconfig-2.js"); err != nil {
		t.Fatal(err)
	}
	fallbackCheckResults(t, vm, "6", "1")
	if stats := vm.JITStats(); stats.NativeCodeBytes != 0 || stats.NativeCompiled != 0 {
		t.Fatalf("reconfigured Quick VM must not hold native state: %+v", stats)
	}
	regions, bytes := jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("reconfigure left RX accounting changed: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}

	// Reconfigure back to Auto and close: still zero RX.
	vm.ConfigureJIT(jit.Config{Mode: jit.Auto, Threshold: 1, Stats: true})
	if err := vm.Close(); err != nil {
		t.Fatal(err)
	}
	regions, bytes = jitnative.LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("Close after reconfigure left RX accounting changed: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
}
