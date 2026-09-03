//go:build windows

package gproc

import (
	"os"
	"os/signal"
	"syscall"
)

// goSignalByName 将 JS 信号名映射为 Go 信号（Windows：仅系统支持的子集；
// SIGTSTP/SIGCONT 等不注册——与 Node Windows 行为一致）。
func goSignalByName(name string) (os.Signal, bool) {
	switch name {
	case "SIGINT":
		return os.Interrupt, true
	case "SIGTERM":
		return syscall.SIGTERM, true
	case "SIGHUP":
		return syscall.SIGHUP, true
	case "SIGQUIT":
		return syscall.SIGQUIT, true
	}
	return nil, false
}

// osSignalNotify 包装 signal.Notify。
func osSignalNotify(ch chan<- os.Signal, sig os.Signal) {
	signal.Notify(ch, sig)
}

// sigName 将 Go 信号转为 JS 风格名称。
func sigName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	}
	return sig.String()
}
