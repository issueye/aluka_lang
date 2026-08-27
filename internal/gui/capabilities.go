package gui

import "runtime"

// Capabilities 描述当前平台 GUI 运行时的能力支持状态。
type Capabilities struct {
	Platform       string `json:"platform"` // "windows" | "darwin" | "linux" | "unsupported"
	WebView        bool   `json:"webview"`
	Dialog         bool   `json:"dialog"`
	Evaluate       bool   `json:"evaluate"`
	CapturePreview bool   `json:"capturePreview"`
	Tray           bool   `json:"tray"`
	GlobalShortcut bool   `json:"globalShortcut"`
	Menu           bool   `json:"menu"`
	Clipboard      bool   `json:"clipboard"`
	Screen         bool   `json:"screen"`
}

// GetCapabilities 返回当前平台运行时支持的 GUI 特性。
func GetCapabilities() Capabilities {
	switch runtime.GOOS {
	case "windows":
		return Capabilities{
			Platform:       "windows",
			WebView:        true,
			Dialog:         true,
			Evaluate:       true,
			CapturePreview: true,
			Tray:           true,
			GlobalShortcut: true,
			Menu:           true,
			Clipboard:      true,
			Screen:         true,
		}
	case "darwin":
		return Capabilities{
			Platform:       "darwin",
			WebView:        true,
			Dialog:         false,
			Evaluate:       true, // 基于通用包装脚本与事件桥回传的 evaluate
			CapturePreview: false,
			Tray:           true,
			GlobalShortcut: false,
			Menu:           false,
			Clipboard:      true,
			Screen:         true,
		}
	default:
		return Capabilities{
			Platform:       runtime.GOOS,
			WebView:        false,
			Dialog:         false,
			Evaluate:       false,
			CapturePreview: false,
			Tray:           false,
			GlobalShortcut: false,
			Menu:           false,
			Clipboard:      false,
			Screen:         false,
		}
	}
}
