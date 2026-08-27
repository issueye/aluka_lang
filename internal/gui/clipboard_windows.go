//go:build windows

package gui

import (
	"fmt"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

// openClipboardWithRetry 尝试打开系统剪贴板（带重试机制，防止并发占用冲突）。
func openClipboardWithRetry() error {
	for i := 0; i < 5; i++ {
		ret, _, _ := procOpenClipboard.Call(0)
		if ret != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("gui: failed to open clipboard")
}

func clipboardReadText() (string, error) {
	if err := openClipboardWithRetry(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	avail, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText)
	if avail == 0 {
		return "", nil
	}

	hMem, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if hMem == 0 {
		return "", nil
	}

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return "", fmt.Errorf("gui: GlobalLock failed on clipboard handle")
	}
	defer procGlobalUnlock.Call(hMem)

	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		return "", nil
	}

	// 构造 uint16 slice 解析 UTF-16
	maxLen := int(size / 2)
	u16Slice := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), maxLen)
	return syscall.UTF16ToString(u16Slice), nil
}

func clipboardWriteText(text string) error {
	if err := openClipboardWithRetry(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	if text == "" {
		return nil
	}

	u16 := utf16.Encode([]rune(text + "\x00"))
	bytesLen := uintptr(len(u16) * 2)

	hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, bytesLen)
	if hMem == 0 {
		return fmt.Errorf("gui: GlobalAlloc failed for clipboard data")
	}

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("gui: GlobalLock failed for clipboard data")
	}

	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16))
	copy(dst, u16)

	procGlobalUnlock.Call(hMem)

	ret, _, _ := procSetClipboardData.Call(cfUnicodeText, hMem)
	if ret == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("gui: SetClipboardData failed")
	}

	return nil
}
