//go:build windows

package gui

import (
	"os"
	"testing"
	"time"
)

// TestTraySmoke 系统托盘冒烟：创建 → 更新 tooltip/菜单 → 销毁。
// 需显式开启：ALUKA_GUI_WEBVIEW2=1（复用 GUI 门控，避免 CI 无桌面环境失败）。
func TestTraySmoke(t *testing.T) {
	if os.Getenv("ALUKA_GUI_WEBVIEW2") == "" {
		t.Skip("set ALUKA_GUI_WEBVIEW2=1 to run the tray smoke test")
	}

	app := GetApp()
	go func() { _ = app.Run() }()

	tray, err := NewTray(TrayOptions{
		Tooltip: "Aluka Tray Smoke",
		Menu: []MenuItem{
			{Label: "显示主窗口"},
			{Type: "separator"},
			{Label: "退出", Disabled: false},
		},
	})
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}

	tray.SetTooltip("Updated Tooltip")
	tray.SetMenu([]MenuItem{
		{Label: "新菜单项", Checked: true},
	})

	// 等待 UI 线程异步任务落地
	time.Sleep(300 * time.Millisecond)

	var clicked bool
	tray.On("click", func(data interface{}) { clicked = true })
	tray.Emit("click", nil)
	if !clicked {
		t.Fatal("tray click event not dispatched")
	}

	tray.Destroy()
}

// TestGlobalShortcutSmoke 全局快捷键冒烟：注册 → 注销。
func TestGlobalShortcutSmoke(t *testing.T) {
	if os.Getenv("ALUKA_GUI_WEBVIEW2") == "" {
		t.Skip("set ALUKA_GUI_WEBVIEW2=1 to run the shortcut smoke test")
	}

	app := GetApp()
	go func() { _ = app.Run() }()

	if err := GlobalShortcutRegister("Ctrl+Alt+F9", func() {}); err != nil {
		t.Fatalf("GlobalShortcutRegister: %v", err)
	}
	if err := GlobalShortcutRegister("Ctrl+Alt+F9", func() {}); err != nil {
		// 同一热键重复注册预期失败（被自身占用）
		t.Logf("duplicate register rejected as expected: %v", err)
	}

	GlobalShortcutUnregister("Ctrl+Alt+F9")

	// 注销后应可重新注册
	if err := GlobalShortcutRegister("Ctrl+Alt+F9", func() {}); err != nil {
		t.Fatalf("re-register after unregister: %v", err)
	}
	GlobalShortcutUnregisterAll()

	// 非法加速器应报错
	if err := GlobalShortcutRegister("Ctrl+NotAKey", func() {}); err == nil {
		t.Fatal("expected error for invalid accelerator")
	}
}
