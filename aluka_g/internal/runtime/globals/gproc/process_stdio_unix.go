//go:build !windows

package gproc

import (
	"os"

	"golang.org/x/sys/unix"
)

func streamIsTTY(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalSize(file *os.File) (columns, rows int, ok bool) {
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, false
	}
	columns, rows = int(size.Col), int(size.Row)
	return columns, rows, columns > 0 && rows > 0
}

// Raw-mode control is currently implemented for the Windows target used by
// the compiled Pi artifact. POSIX terminals retain their existing mode.
func setStdinRawMode(enabled bool) error {
	return nil
}
