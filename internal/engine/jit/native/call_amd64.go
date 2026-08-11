//go:build amd64

package native

// callCode is implemented by a fixed Go assembly trampoline. Generated code
// receives frame in R10 and returns a status code in RAX.
//
//go:noescape
func callCode(entry uintptr, frame *Frame) uint64
