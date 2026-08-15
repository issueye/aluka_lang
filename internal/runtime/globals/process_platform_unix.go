//go:build !windows

package globals

// process.umask / process.cpuUsage 的 Unix 实现（POSIX 语义）。

import "syscall"

// getUmask 读取当前 umask（临时置 0 读取后恢复）。
func getUmask() int {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return m
}

// setUmask 设置 umask 并返回旧值。
func setUmask(mask int) int {
	return syscall.Umask(mask)
}

// getProcessUsage 返回用户/系统 CPU 时间（微秒）。
func getProcessUsage() (user, system int64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil {
		user = ru.Utime.Sec*1e6 + int64(ru.Utime.Usec)
		system = ru.Stime.Sec*1e6 + int64(ru.Stime.Usec)
	}
	return
}
