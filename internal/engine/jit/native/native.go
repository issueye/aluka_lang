package native

import (
	"errors"
	"runtime"
	"sync/atomic"
)

var ErrUnsupported = errors.New("native jit is not supported on this platform")

// Frame is the pointer-free ABI shared with generated code. Native code may
// read/write numeric fields but must not retain the frame address after Call.
type Frame struct {
	Args   [8]float64
	Result float64
	Status uint64
	Locals [32]float64
	Budget uint64
	Resume uint64
}

type Code struct {
	mem           executableMemory
	debugBytes    []byte
	accountedSize atomic.Uint64
}

var liveExecutableRegions atomic.Uint64
var liveExecutableBytes atomic.Uint64

// LiveExecutableMemory reports currently published native code regions. It is
// intended for diagnostics and lifecycle tests; the counters do not affect the
// execution ABI or cache policy.
func LiveExecutableMemory() (regions, bytes uint64) {
	return liveExecutableRegions.Load(), liveExecutableBytes.Load()
}

func Publish(machineCode []byte, retainDebugBytes ...bool) (*Code, error) {
	mem, err := publishExecutable(machineCode)
	if err != nil {
		return nil, err
	}
	code := &Code{mem: mem}
	if len(retainDebugBytes) != 0 && retainDebugBytes[0] {
		code.debugBytes = append([]byte(nil), machineCode...)
	}
	size := uint64(mem.size())
	code.accountedSize.Store(size)
	liveExecutableRegions.Add(1)
	liveExecutableBytes.Add(size)
	return code, nil
}

func (c *Code) Entry() uintptr {
	if c == nil {
		return 0
	}
	return c.mem.entry()
}

func (c *Code) Size() int {
	if c == nil {
		return 0
	}
	return c.mem.size()
}

func (c *Code) DebugBytes() []byte {
	if c == nil || len(c.debugBytes) == 0 {
		return nil
	}
	return append([]byte(nil), c.debugBytes...)
}

func (c *Code) Call(frame *Frame) uint64 {
	return c.CallAt(0, frame)
}

func (c *Code) CallAt(offset uint64, frame *Frame) uint64 {
	if c == nil || frame == nil || c.Entry() == 0 {
		return 1
	}
	if offset >= uint64(c.Size()) {
		return 1
	}
	status := callCode(c.Entry()+uintptr(offset), frame)
	runtime.KeepAlive(frame)
	runtime.KeepAlive(c)
	return status
}

func (c *Code) Close() error {
	if c == nil {
		return nil
	}
	err := c.mem.close()
	if err != nil {
		return err
	}
	c.mem = executableMemory{}
	c.debugBytes = nil
	if size := c.accountedSize.Swap(0); size != 0 {
		liveExecutableRegions.Add(^uint64(0))
		liveExecutableBytes.Add(^uint64(size - 1))
	}
	return err
}

// AddF64Kernel is the J2-S smoke kernel. It expects the frame pointer in R10,
// computes Args[0] + Args[1], stores Result, and returns status 0 in RAX.
func AddF64Kernel() []byte {
	return []byte{
		0xF2, 0x41, 0x0F, 0x10, 0x02,
		0xF2, 0x41, 0x0F, 0x58, 0x42, 0x08,
		0xF2, 0x41, 0x0F, 0x11, 0x42, 0x40,
		0x31, 0xC0,
		0xC3,
	}
}
