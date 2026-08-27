//go:build !windows && !darwin

package gui

import (
	"fmt"
	"runtime"
)

func getPrimaryDisplay() (DisplayInfo, error) {
	return DisplayInfo{}, fmt.Errorf("gui: screen info is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}

func getAllDisplays() ([]DisplayInfo, error) {
	return nil, fmt.Errorf("gui: screen info is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}
