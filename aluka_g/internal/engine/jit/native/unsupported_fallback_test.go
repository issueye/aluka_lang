//go:build !amd64 || (!windows && !linux)

// R2-7: unsupported-platform fallback validation for the native backend.
//
// This file is compiled exactly where the exec-memory stub
// (execmem_unsupported.go, tag `!amd64 || (!windows && !linux)`) is active:
// darwin/arm64, linux/arm64, amd64 non-windows/linux, and every other
// combination without an amd64 Windows/Linux backend.
//
// It validates the R2-7 contract directly against the stub compilation unit:
//   - Publish is rejected with ErrUnsupported (never a panic, never a Code).
//   - Rejected publication never touches the RX accounting counters.
//   - The only Code values reachable on such platforms (zero values) are
//     inert: Entry/Size/DebugBytes are zero/nil and Call/CallAt short-circuit
//     on Entry()==0 without invoking callCode (which on amd64+unsupported OS
//     is still the real amd64 ABI assembly and must never be reached).
//   - ErrUnsupported is a stable sentinel whose text is distinguishable from
//     ordinary IR-level compile failures (the jit package test asserts the
//     flip side of that distinction).
//
// No t.Skip anywhere: every assertion executes on unsupported platforms.

package native

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformPublishRejectsNative(t *testing.T) {
	code, err := Publish([]byte{0xC3})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Publish error = %v, want ErrUnsupported", err)
	}
	if code != nil {
		t.Fatalf("Publish returned code %+v on an unsupported platform", code)
	}
}

func TestUnsupportedPlatformPublishNeverTouchesAccounting(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	kernels := [][]byte{{0xC3}, AddF64Kernel(), make([]byte, 4096)}
	for i, machineCode := range kernels {
		code, err := Publish(machineCode, true) // retainDebugBytes must not matter
		if !errors.Is(err, ErrUnsupported) || code != nil {
			t.Fatalf("kernel %d: Publish = (%v, %v), want (nil, ErrUnsupported)", i, code, err)
		}
		regions, bytes := LiveExecutableMemory()
		if regions != baseRegions || bytes != baseBytes {
			t.Fatalf("kernel %d: rejected Publish changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
				i, regions, bytes, baseRegions, baseBytes)
		}
	}
}

func TestUnsupportedPlatformInertCodeValues(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	var code Code // zero value: the only Code value reachable on unsupported platforms
	if entry := code.Entry(); entry != 0 {
		t.Fatalf("zero Code.Entry() = %#x, want 0", entry)
	}
	if size := code.Size(); size != 0 {
		t.Fatalf("zero Code.Size() = %d, want 0", size)
	}
	if debug := code.DebugBytes(); debug != nil {
		t.Fatalf("zero Code.DebugBytes() = %v, want nil", debug)
	}
	// Call/CallAt must short-circuit on Entry()==0 and return the guard
	// status without ever invoking callCode.
	if status := code.Call(nil); status != 1 {
		t.Fatalf("zero Code.Call(nil) = %d, want guard status 1", status)
	}
	if status := code.CallAt(0, &Frame{}); status != 1 {
		t.Fatalf("zero Code.CallAt = %d, want guard status 1", status)
	}
	if err := code.Close(); err != nil {
		t.Fatalf("zero Code.Close() = %v, want nil", err)
	}
	regions, bytes := LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("inert Code changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
}

func TestUnsupportedPlatformErrorIdentity(t *testing.T) {
	// The sentinel is stable and carries the platform gate in its text; it
	// must not be conflated with ordinary compile failures, which the jit
	// package test asserts keep their own messages.
	if msg := ErrUnsupported.Error(); msg != "native jit is not supported on this platform" {
		t.Fatalf("ErrUnsupported message = %q", msg)
	}
	if _, err := Publish(nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Publish(nil) = %v, want ErrUnsupported", err)
	}
}
