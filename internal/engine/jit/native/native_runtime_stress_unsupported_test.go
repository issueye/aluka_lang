//go:build !amd64 || (!windows && !linux)

package native

import "testing"

// TestNativeJITStressUnsupportedPlatform proves unsupported platforms reject
// publication without changing executable-memory accounting.
func TestNativeJITStressUnsupportedPlatform(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish([]byte{0xC3})
	if err == nil {
		if code != nil {
			_ = code.Close()
		}
		t.Fatal("Publish succeeded on an unsupported platform")
	}
	regions, bytes := LiveExecutableMemory()
	if regions != baseRegions || bytes != baseBytes {
		t.Fatalf("unsupported Publish changed RX accounting: live=(%d,%d) baseline=(%d,%d)",
			regions, bytes, baseRegions, baseBytes)
	}
}
