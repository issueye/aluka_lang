//go:build darwin

package gui

import (
	"fmt"
	"runtime"
	"syscall"
)

//go:cgo_import_dynamic libc_dlopen dlopen "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_dlsym dlsym "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_dlerror dlerror "/usr/lib/libSystem.B.dylib"

const (
	rtldLazy   = 0x1
	rtldGlobal = 0x8
)

func dlopenABI(path *byte, mode int) uintptr
func dlsymABI(handle uintptr, symbol *byte) uintptr
func dlerrorABI() uintptr

func sysDlopen(path string, mode int) (uintptr, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0, err
	}
	h := dlopenABI(p, mode)
	runtime.KeepAlive(path)
	if h == 0 {
		return 0, fmt.Errorf("%s", dlerrorGo())
	}
	return h, nil
}

func sysDlsym(h uintptr, sym string) (uintptr, error) {
	p, err := syscall.BytePtrFromString(sym)
	if err != nil {
		return 0, err
	}
	addr := dlsymABI(h, p)
	runtime.KeepAlive(sym)
	if addr == 0 {
		return 0, fmt.Errorf("dlsym %s: %s", sym, dlerrorGo())
	}
	return addr, nil
}

func dlerrorGo() string {
	p := dlerrorABI()
	if p == 0 {
		return "unknown dlerror"
	}
	return goCString(p)
}
