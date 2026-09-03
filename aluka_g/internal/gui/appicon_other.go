//go:build !windows

package gui

// SetAppIcon 设置应用级图标（非 Windows 平台暂为空操作，
// 由各平台原生层决定图标承载方式）。
func SetAppIcon(ico []byte) error { return nil }
