//go:build windows

package globals

import (
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

const (
	enableProcessedInput       = 0x0001
	enableLineInput            = 0x0002
	enableEchoInput            = 0x0004
	enableVirtualTerminalInput = 0x0200
)

var stdinConsoleMode struct {
	sync.Mutex
	saved uint32
	valid bool
}

func streamIsTTY(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

func terminalSize(file *os.File) (columns, rows int, ok bool) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(file.Fd()), &info); err != nil {
		return 0, 0, false
	}
	columns = int(info.Window.Right-info.Window.Left) + 1
	rows = int(info.Window.Bottom-info.Window.Top) + 1
	return columns, rows, columns > 0 && rows > 0
}

func rawStdinConsoleMode(mode uint32) uint32 {
	mode &^= enableProcessedInput | enableLineInput | enableEchoInput
	return mode | enableVirtualTerminalInput
}

func setStdinRawMode(enabled bool) error {
	handle := windows.Handle(os.Stdin.Fd())
	stdinConsoleMode.Lock()
	defer stdinConsoleMode.Unlock()

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		// Redirected stdin is not a console. Node's setRawMode is unavailable in
		// that case; treating the call as a no-op keeps non-interactive runs safe.
		return nil
	}
	if enabled {
		if !stdinConsoleMode.valid {
			stdinConsoleMode.saved = mode
			stdinConsoleMode.valid = true
		}
		return windows.SetConsoleMode(handle, rawStdinConsoleMode(mode))
	}
	if stdinConsoleMode.valid {
		err := windows.SetConsoleMode(handle, stdinConsoleMode.saved)
		stdinConsoleMode.valid = false
		return err
	}
	return nil
}
