//go:build windows

package gui

import "syscall"

var procFreeConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole")

// ReleaseConsole 分离进程控制台（GUI 产物免黑框）。
// aluka build --gui 产物在展示窗口前调用；若无控制台则为空操作。
func ReleaseConsole() {
	procFreeConsole.Call()
}
