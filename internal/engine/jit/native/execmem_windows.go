//go:build windows && amd64

package native

import (
	"fmt"

	"golang.org/x/sys/windows"
)

type executableMemory struct {
	addr uintptr
	n    int
}

func publishExecutable(code []byte) (executableMemory, error) {
	if len(code) == 0 {
		return executableMemory{}, fmt.Errorf("native jit: empty code")
	}
	addr, err := windows.VirtualAlloc(0, uintptr(len(code)), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return executableMemory{}, fmt.Errorf("native jit: VirtualAlloc: %w", err)
	}
	mem := executableMemory{addr: addr, n: len(code)}
	var written uintptr
	if err := windows.WriteProcessMemory(windows.CurrentProcess(), addr, &code[0], uintptr(len(code)), &written); err != nil || written != uintptr(len(code)) {
		_ = windows.VirtualFree(addr, 0, windows.MEM_RELEASE)
		if err != nil {
			return executableMemory{}, fmt.Errorf("native jit: WriteProcessMemory: %w", err)
		}
		return executableMemory{}, fmt.Errorf("native jit: short machine-code write: %d/%d", written, len(code))
	}
	var oldProtect uint32
	if err := windows.VirtualProtect(addr, uintptr(len(code)), windows.PAGE_EXECUTE_READ, &oldProtect); err != nil {
		_ = windows.VirtualFree(addr, 0, windows.MEM_RELEASE)
		return executableMemory{}, fmt.Errorf("native jit: VirtualProtect: %w", err)
	}
	return mem, nil
}

func (m executableMemory) entry() uintptr { return m.addr }
func (m executableMemory) size() int      { return m.n }

func (m executableMemory) close() error {
	if m.addr == 0 {
		return nil
	}
	if err := windows.VirtualFree(m.addr, 0, windows.MEM_RELEASE); err != nil {
		return fmt.Errorf("native jit: VirtualFree: %w", err)
	}
	return nil
}
