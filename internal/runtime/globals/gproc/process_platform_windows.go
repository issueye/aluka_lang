//go:build windows

package gproc

// process.umask / process.cpuUsage 的 Windows 实现。
// Node 在 Windows 上 umask 恒为 0 且设置无效（实测）。

import (
	"syscall"
	"unsafe"
)

// getUmask Windows 恒 0。
func getUmask() int { return 0 }

// setUmask Windows 不生效，返回 0。
func setUmask(mask int) int { return 0 }

// getProcessUsage 用 GetProcessTimes 取用户/系统 CPU 时间（微秒）。
func getProcessUsage() (user, system int64) {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	proc := k32.NewProc("GetProcessTimes")
	var creation, exit, kernel, userTime syscall.Filetime
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return
	}
	r1, _, _ := proc.Call(uintptr(handle),
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&userTime)))
	if r1 == 0 {
		return
	}
	// Filetime：100ns 单位 → 微秒。
	user = (int64(userTime.HighDateTime)<<32 + int64(userTime.LowDateTime)) / 10
	system = (int64(kernel.HighDateTime)<<32 + int64(kernel.LowDateTime)) / 10
	return
}
