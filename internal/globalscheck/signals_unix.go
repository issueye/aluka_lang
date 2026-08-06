//go:build !windows

package globals

import (
	"os"
	"os/signal"
	"syscall"
)

// goSignalByName 将 JS 信号名映射为 Go 信号（Unix：全量）。
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
	case "SIGTSTP":
		return syscall.SIGTSTP, true
	case "SIGCONT":
		return syscall.SIGCONT, true
	case "SIGWINCH":
		return syscall.SIGWINCH, true
	case "SIGUSR1":
		return syscall.SIGUSR1, true
	case "SIGUSR2":
		return syscall.SIGUSR2, true
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
	case syscall.SIGTSTP:
		return "SIGTSTP"
	case syscall.SIGCONT:
		return "SIGCONT"
	case syscall.SIGWINCH:
		return "SIGWINCH"
	}
	return sig.String()
}
