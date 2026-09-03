//go:build linux && amd64

package native

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

type executableMemory struct {
	mem []byte
}

func publishExecutable(code []byte) (executableMemory, error) {
	if len(code) == 0 {
		return executableMemory{}, fmt.Errorf("native jit: empty code")
	}
	mem, err := unix.Mmap(-1, 0, len(code), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return executableMemory{}, fmt.Errorf("native jit: mmap: %w", err)
	}
	copy(mem, code)
	if err := unix.Mprotect(mem, unix.PROT_READ|unix.PROT_EXEC); err != nil {
		_ = unix.Munmap(mem)
		return executableMemory{}, fmt.Errorf("native jit: mprotect: %w", err)
	}
	return executableMemory{mem: mem}, nil
}

func (m executableMemory) entry() uintptr {
	if len(m.mem) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&m.mem[0]))
}

func (m executableMemory) size() int { return len(m.mem) }

func (m executableMemory) close() error {
	if len(m.mem) == 0 {
		return nil
	}
	if err := unix.Munmap(m.mem); err != nil {
		return fmt.Errorf("native jit: munmap: %w", err)
	}
	return nil
}
