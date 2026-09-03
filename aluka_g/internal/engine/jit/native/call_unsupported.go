//go:build !amd64

package native

func callCode(uintptr, *Frame) uint64 { return 1 }
