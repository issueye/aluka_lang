//go:build !amd64 || (!windows && !linux)

package native

type executableMemory struct{}

func publishExecutable([]byte) (executableMemory, error) { return executableMemory{}, ErrUnsupported }
func (executableMemory) entry() uintptr                  { return 0 }
func (executableMemory) size() int                       { return 0 }
func (executableMemory) close() error                    { return nil }
