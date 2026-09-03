// Package gui —— 全局快捷键平台无关层：注册表与加速器（accelerator）解析。
// 平台相关的注册/注销由 shortcut_windows.go / shortcut_other.go 提供。
package gui

import (
	"fmt"
	"strings"
	"sync"
)

// Accelerator 解析后的快捷键描述。
type Accelerator struct {
	Raw      string
	Modifier Modifiers
	Key      string
}

// Modifiers 修饰键位掩码（各平台自行映射到本机常量）。
type Modifiers uint8

const (
	ModNone Modifiers = 0
	ModAlt  Modifiers = 1 << iota
	ModCtrl
	ModShift
	ModSuper
)

func (m Modifiers) Has(flag Modifiers) bool { return m&flag != 0 }

// ParseAccelerator 解析形如 "Ctrl+Shift+P"、"Alt+F4"、"CommandOrControl+K" 的加速器。
func ParseAccelerator(accel string) (Accelerator, error) {
	raw := strings.TrimSpace(accel)
	if raw == "" {
		return Accelerator{}, fmt.Errorf("gui: empty accelerator")
	}

	parts := strings.Split(raw, "+")
	var mods Modifiers
	var key string
	for i, p := range parts {
		p = strings.TrimSpace(p)
		last := i == len(parts)-1
		switch strings.ToLower(p) {
		case "ctrl", "control", "commandorcontrol", "cmdorctrl":
			mods |= ModCtrl
		case "shift":
			mods |= ModShift
		case "alt", "option":
			mods |= ModAlt
		case "super", "meta", "cmd", "command", "win", "windows":
			mods |= ModSuper
		default:
			if !last {
				return Accelerator{}, fmt.Errorf("gui: unknown accelerator modifier %q in %q", p, raw)
			}
			if !isValidVirtualKey(p) {
				return Accelerator{}, fmt.Errorf("gui: unknown accelerator key %q in %q", p, raw)
			}
			key = normalizeKey(p)
		}
	}
	if key == "" {
		return Accelerator{}, fmt.Errorf("gui: accelerator %q missing main key", raw)
	}
	return Accelerator{Raw: raw, Modifier: mods, Key: key}, nil
}

// normalizeKey 归一化主键：字母转大写、数字保持、功能键转大写。
func normalizeKey(k string) string {
	if len(k) == 1 {
		return strings.ToUpper(k)
	}
	return strings.ToUpper(k)
}

// isValidVirtualKey 校验主键是否属于受支持的虚拟键名。
func isValidVirtualKey(k string) bool {
	up := strings.ToUpper(k)
	if len(up) == 1 {
		c := up[0]
		return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	switch up {
	case "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
		"ENTER", "RETURN", "SPACE", "TAB", "ESCAPE", "ESC", "BACKSPACE", "DELETE",
		"DEL", "INSERT", "HOME", "END", "PAGEUP", "PAGEDOWN", "PGUP", "PGDN",
		"UP", "DOWN", "LEFT", "RIGHT",
		"PLUS", "COMMA", "PERIOD", "SLASH", "SEMICOLON", "QUOTE", "BRACKETLEFT",
		"BRACKETRIGHT", "BACKQUOTE", "MINUS", "EQUAL", "BACKSLASH":
		return true
	}
	return false
}

// globalShortcutState 平台无关的注册表（按加速器原文索引）。
var globalShortcutState struct {
	mu       sync.Mutex
	handlers map[string]func()
}

func init() {
	globalShortcutState.handlers = make(map[string]func())
}

// shortcutRegister 记录处理器（平台实现在原生注册成功后调用）。
func shortcutRemember(accel string, fn func()) {
	globalShortcutState.mu.Lock()
	globalShortcutState.handlers[accel] = fn
	globalShortcutState.mu.Unlock()
}

// shortcutLookupAndRun 按加速器原文查找并执行处理器。
func shortcutLookupAndRun(accel string) {
	globalShortcutState.mu.Lock()
	fn := globalShortcutState.handlers[accel]
	globalShortcutState.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

// shortcutForget 移除记录。
func shortcutForget(accel string) {
	globalShortcutState.mu.Lock()
	delete(globalShortcutState.handlers, accel)
	globalShortcutState.mu.Unlock()
}
