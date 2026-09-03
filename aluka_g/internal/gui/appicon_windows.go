//go:build windows

// 应用级图标（--icon 内嵌 .ico）：窗口标题栏/任务栏（WM_SETICON）
// 与默认托盘图标共用。产物模式启动时经 gui.SetAppIcon 注入。
package gui

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

var (
	appIconMu    sync.RWMutex
	appIconBig   syscall.Handle
	appIconSmall syscall.Handle
)

var procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")

// SetAppIcon 设置应用级图标（.ico 文件原始字节），
// 应用于此后创建的所有窗口（标题栏/任务栏）与未显式指定图标的托盘。
func SetAppIcon(ico []byte) error {
	if len(ico) == 0 {
		return nil
	}
	// .ico 文件 = ICONDIR(6B) + 目录项(16B/个) + 图像数据；CreateIconFromResource
	// 接受单张图标资源（GRPICONDIR 之后的数据）。取目录第一项定位图像数据。
	if len(ico) < 22 || ico[0] != 0 || ico[1] != 0 {
		return fmt.Errorf("gui: not a valid .ico file")
	}
	count := int(ico[4]) | int(ico[5])<<8
	if count == 0 {
		return fmt.Errorf("gui: .ico contains no images")
	}
	// 目录第一项：width(1) height(1) colors(1) reserved(1) planes(2) bitcount(2)
	// bytesInRes(4) imageOffset(4)
	offset := 6 + 16
	size := int(ico[14]) | int(ico[15])<<8 | int(ico[16])<<16 | int(ico[17])<<24
	imgOffset := int(ico[18]) | int(ico[19])<<8 | int(ico[20])<<16 | int(ico[21])<<24
	if imgOffset <= 0 || imgOffset+size > len(ico) || size <= 0 {
		return fmt.Errorf("gui: .ico directory entry out of range")
	}
	img := ico[imgOffset : imgOffset+size]
	_ = offset

	const lrDefaultSize = 0x00000040
	appIconMu.Lock()
	defer appIconMu.Unlock()
	// 大图标（标题栏/Alt-Tab）与小图标（任务栏）各自按默认尺寸解析
	big, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&img[0])), uintptr(len(img)),
		1, 0x00030000, 0, 0, lrDefaultSize)
	small, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&img[0])), uintptr(len(img)),
		1, 0x00030000,
		16, 16, 0x00000001 /* LR_DEFAULTCOLOR */)
	if big == 0 && small == 0 {
		return fmt.Errorf("gui: CreateIconFromResourceEx failed")
	}
	if big != 0 {
		appIconBig = syscall.Handle(big)
	}
	if small != 0 {
		appIconSmall = syscall.Handle(small)
	}
	return nil
}

// AppIcons 返回应用级大/小图标句柄（未设置时为 0）。
func AppIcons() (big, small syscall.Handle) {
	appIconMu.RLock()
	defer appIconMu.RUnlock()
	return appIconBig, appIconSmall
}

// applyAppIcon 为窗口应用应用级图标（标题栏/任务栏，WM_SETICON）。
func applyAppIcon(hwnd syscall.Handle) {
	big, small := AppIcons()
	if big != 0 {
		procSendMessageW.Call(uintptr(hwnd), 0x0080 /* WM_SETICON */, 1 /* ICON_BIG */, uintptr(big))
	}
	if small != 0 {
		procSendMessageW.Call(uintptr(hwnd), 0x0080, 0 /* ICON_SMALL */, uintptr(small))
	}
}
