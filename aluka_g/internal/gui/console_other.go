//go:build !windows

package gui

// ReleaseConsole 分离进程控制台（非 Windows 平台无此概念，空操作）。
func ReleaseConsole() {}
