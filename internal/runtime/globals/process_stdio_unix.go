//go:build !windows

package globals

import "os"

func streamIsTTY(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Raw-mode control is currently implemented for the Windows target used by
// the compiled Pi artifact. POSIX terminals retain their existing mode.
func setStdinRawMode(enabled bool) error {
	return nil
}
