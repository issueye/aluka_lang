//go:build windows

// 全局快捷键 Windows 实装：RegisterHotKey 注册到共享消息窗口，
// WM_HOTKEY 消息驱动回调。
package gui

import (
	"fmt"
	"strings"
	"sync"
)

const (
	wmHotkey = 0x0312

	modAlt   = 0x0001
	modCtrl  = 0x0002
	modShift = 0x0004
	modWin   = 0x0008

	hkNoRepeat = 0x4000
)

// virtualKeyCode 主键名 → Windows 虚拟键码。
func virtualKeyCode(key string) (uintptr, bool) {
	up := strings.ToUpper(key)
	if len(up) == 1 {
		c := up[0]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return uintptr(c), true
		}
		return 0, false
	}
	switch up {
	case "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		var n int
		_, _ = fmt.Sscanf(strings.TrimPrefix(up, "F"), "%d", &n)
		if n >= 1 && n <= 12 {
			return uintptr(0x6F + n), true // F1=0x70 ... F12=0x7B
		}
		return 0, false
	case "ENTER", "RETURN":
		return 0x0D, true
	case "SPACE":
		return 0x20, true
	case "TAB":
		return 0x09, true
	case "ESCAPE", "ESC":
		return 0x1B, true
	case "BACKSPACE":
		return 0x08, true
	case "DELETE", "DEL":
		return 0x2E, true
	case "INSERT":
		return 0x2D, true
	case "HOME":
		return 0x24, true
	case "END":
		return 0x23, true
	case "PAGEUP", "PGUP":
		return 0x21, true
	case "PAGEDOWN", "PGDN":
		return 0x22, true
	case "UP":
		return 0x26, true
	case "DOWN":
		return 0x28, true
	case "LEFT":
		return 0x25, true
	case "RIGHT":
		return 0x27, true
	case "PLUS":
		return 0xBB, true // VK_OEM_PLUS
	case "COMMA":
		return 0xBC, true
	case "PERIOD":
		return 0xBE, true
	case "SLASH":
		return 0xBF, true
	case "SEMICOLON":
		return 0xBA, true
	case "QUOTE":
		return 0xDE, true
	case "BRACKETLEFT":
		return 0xDB, true
	case "BRACKETRIGHT":
		return 0xDD, true
	case "BACKQUOTE":
		return 0xC0, true
	case "MINUS":
		return 0xBD, true
	case "EQUAL":
		return 0xBB, true
	case "BACKSLASH":
		return 0xDC, true
	}
	return 0, false
}

var (
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")

	hotkeyMu     sync.Mutex
	hotkeyByID   = make(map[int]string) // 平台热键 ID → 加速器原文
	hotkeyNextID = 1
)

// dispatchHotkey 由消息窗口过程调用（UI 线程）。
func dispatchHotkey(id int) {
	hotkeyMu.Lock()
	accel := hotkeyByID[id]
	hotkeyMu.Unlock()
	if accel != "" {
		shortcutLookupAndRun(accel)
	}
}

// GlobalShortcutRegister 注册全局快捷键（需在 UI 线程外调用时自动投递）。
func GlobalShortcutRegister(accel string, fn func()) error {
	parsed, err := ParseAccelerator(accel)
	if err != nil {
		return err
	}
	vk, ok := virtualKeyCode(parsed.Key)
	if !ok {
		return fmt.Errorf("gui: unsupported hotkey %q", parsed.Key)
	}

	var mods uintptr
	if parsed.Modifier.Has(ModAlt) {
		mods |= modAlt
	}
	if parsed.Modifier.Has(ModCtrl) {
		mods |= modCtrl
	}
	if parsed.Modifier.Has(ModShift) {
		mods |= modShift
	}
	if parsed.Modifier.Has(ModSuper) {
		mods |= modWin
	}

	app := GetApp()
	wa, _ := app.native.(*windowsApp)
	if wa != nil && wa.onUIThread() {
		return registerHotKeyOnUIThread(parsed.Raw, mods, vk, fn)
	}

	resCh := make(chan error, 1)
	app.PostAction(func() {
		resCh <- registerHotKeyOnUIThread(parsed.Raw, mods, vk, fn)
	})
	return <-resCh
}

func registerHotKeyOnUIThread(raw string, mods, vk uintptr, fn func()) error {
	ensureMessageWindow()

	hotkeyMu.Lock()
	id := hotkeyNextID
	hotkeyNextID++
	hotkeyMu.Unlock()

	ret, _, _ := procRegisterHotKey.Call(uintptr(msgHwnd), uintptr(id), mods|hkNoRepeat, vk)
	if ret == 0 {
		hotkeyMu.Lock()
		delete(hotkeyByID, id)
		hotkeyMu.Unlock()
		return fmt.Errorf("gui: RegisterHotKey failed for %q (可能已被其他应用占用)", raw)
	}

	hotkeyMu.Lock()
	hotkeyByID[id] = raw
	hotkeyMu.Unlock()
	shortcutRemember(raw, fn)
	return nil
}

// GlobalShortcutUnregister 注销全局快捷键。
func GlobalShortcutUnregister(accel string) {
	hotkeyMu.Lock()
	id, found := -1, false
	for k, v := range hotkeyByID {
		if v == accel {
			id, found = k, true
			delete(hotkeyByID, k)
			break
		}
	}
	hotkeyMu.Unlock()
	if !found {
		return
	}
	shortcutForget(accel)

	app := GetApp()
	run := func() {
		procUnregisterHotKey.Call(uintptr(msgHwnd), uintptr(id))
	}
	if wa, ok := app.native.(*windowsApp); ok && wa.onUIThread() {
		run()
	} else {
		app.PostAction(run)
	}
}

// GlobalShortcutUnregisterAll 注销全部全局快捷键。
func GlobalShortcutUnregisterAll() {
	hotkeyMu.Lock()
	ids := make([]int, 0, len(hotkeyByID))
	for id := range hotkeyByID {
		ids = append(ids, id)
	}
	hotkeyByID = make(map[int]string)
	hotkeyMu.Unlock()

	globalShortcutState.mu.Lock()
	globalShortcutState.handlers = make(map[string]func())
	globalShortcutState.mu.Unlock()

	app := GetApp()
	run := func() {
		for _, id := range ids {
			procUnregisterHotKey.Call(uintptr(msgHwnd), uintptr(id))
		}
	}
	if wa, ok := app.native.(*windowsApp); ok && wa.onUIThread() {
		run()
	} else {
		app.PostAction(run)
	}
}
