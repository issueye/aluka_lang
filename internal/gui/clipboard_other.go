//go:build !windows && !darwin

package gui

import (
	"fmt"
	"runtime"
)

func clipboardReadText() (string, error) {
	return "", fmt.Errorf("gui: clipboard is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}

func clipboardWriteText(text string) error {
	return fmt.Errorf("gui: clipboard is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}
