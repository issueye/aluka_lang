//go:build windows

package builtin

// Windows 子进程控制台窗口：对齐 Node windowsHide（CREATE_NO_WINDOW）。

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func applyWindowsHide(cmd *exec.Cmd, hide bool) {
	if !hide || cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
