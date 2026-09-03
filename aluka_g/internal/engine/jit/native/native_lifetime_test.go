//go:build amd64 && (windows || linux)

package native

import (
	"runtime"
	"testing"
)

func TestExecutableMemoryAccountingReturnsToBaseline(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	codes := make([]*Code, 32)
	for i := range codes {
		code, err := Publish(AddF64Kernel())
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		codes[i] = code
	}
	regions, bytes := LiveExecutableMemory()
	if regions != baseRegions+uint64(len(codes)) || bytes <= baseBytes {
		t.Fatalf("published accounting=(%d,%d), baseline=(%d,%d)", regions, bytes, baseRegions, baseBytes)
	}
	for _, code := range codes {
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	regions, bytes = LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("closed accounting=(%d,%d), baseline=(%d,%d)", regions, bytes, baseRegions, baseBytes)
	}
}
