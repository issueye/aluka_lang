//go:build !windows

// 全局快捷键：非 Windows 平台暂未实装（返回不支持错误）。
package gui

import "fmt"

func GlobalShortcutRegister(accel string, fn func()) error {
	return fmt.Errorf("gui: global shortcut not supported on this platform yet")
}

func GlobalShortcutUnregister(accel string) {}

func GlobalShortcutUnregisterAll() {}
