//go:build windows

package gproc

import "testing"

func TestRawStdinConsoleMode(t *testing.T) {
	const preservedMode = uint32(0x0080)
	mode := rawStdinConsoleMode(preservedMode | enableProcessedInput | enableLineInput | enableEchoInput)

	if mode&enableVirtualTerminalInput == 0 {
		t.Fatal("raw stdin mode does not enable virtual terminal input")
	}
	if mode&(enableProcessedInput|enableLineInput|enableEchoInput) != 0 {
		t.Fatal("raw stdin mode retains processed, line, or echo input")
	}
	if mode&preservedMode == 0 {
		t.Fatal("raw stdin mode cleared an unrelated console flag")
	}
}
