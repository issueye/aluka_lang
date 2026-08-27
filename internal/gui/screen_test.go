package gui

import (
	"runtime"
	"testing"
)

func TestScreenInfo(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("screen info not supported on this platform")
	}

	primary, err := GetPrimaryDisplay()
	if err != nil {
		t.Fatalf("GetPrimaryDisplay failed: %v", err)
	}

	if primary.Bounds.Width <= 0 || primary.Bounds.Height <= 0 {
		t.Fatalf("Invalid primary display bounds: %+v", primary.Bounds)
	}
	if primary.ScaleFactor <= 0 {
		t.Fatalf("Invalid primary display scaleFactor: %f", primary.ScaleFactor)
	}

	displays, err := GetAllDisplays()
	if err != nil {
		t.Fatalf("GetAllDisplays failed: %v", err)
	}
	if len(displays) == 0 {
		t.Fatalf("Expected at least one display")
	}
}
