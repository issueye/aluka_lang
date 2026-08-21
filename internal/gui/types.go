// Package gui 提供 Aluka 跨平台轻量级 GUI 桌面框架核心实现（参考 Wails v3 架构）。
package gui

// WindowOptions 定义创建原生窗口的配置项。
type WindowOptions struct {
	Title            string `json:"title"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	MinWidth         int    `json:"minWidth"`
	MinHeight        int    `json:"minHeight"`
	MaxWidth         int    `json:"maxWidth"`
	MaxHeight        int    `json:"maxHeight"`
	X                int    `json:"x"`
	Y                int    `json:"y"`
	Center           bool   `json:"center"`
	Frame            *bool  `json:"frame"`            // 是否有原生窗口边框（nil 默认为 true）
	Transparent      bool   `json:"transparent"`      // 是否启用背景透明
	BackgroundEffect string `json:"backgroundEffect"` // 现代特效: "mica" / "acrylic" / "vibrancy" / "none"
	AlwaysOnTop      bool   `json:"alwaysOnTop"`      // 是否置顶
	Resizable        *bool  `json:"resizable"`        // 是否允许调整大小（nil 默认为 true）
	Maximized        bool   `json:"maximized"`        // 创建时是否直接最大化
	Minimized        bool   `json:"minimized"`        // 创建时是否直接最小化
	Opacity          float64 `json:"opacity"`         // 初始不透明度 0.0~1.0（1 为不透明）
	URL              string `json:"url"`              // 初始加载 URL（支持 http/https 与 aluka://app/*）
	HTML             string `json:"html"`             // 直接加载的 HTML 内容
	PreloadScript    string `json:"preloadScript"`    // 前端预加载注入脚本
	DevTools         bool   `json:"devTools"`         // 是否开启开发者工具
	Hidden           bool   `json:"hidden"`           // 创建时是否初始隐藏
}

// Menu 表示窗口菜单栏（托盘仍直接使用 []MenuItem）。
type Menu struct {
	Items []MenuItem
}

// DialogOptions 原生文件/消息对话框配置。
type DialogOptions struct {
	Title       string       `json:"title"`
	Message     string       `json:"message"`
	Type        string       `json:"type"` // "info" / "warning" / "error" / "question"
	Buttons     []string     `json:"buttons"`
	DefaultID   int          `json:"defaultId"`
	CancelID    int          `json:"cancelId"`
	Filters     []FileFilter `json:"filters"`
	DefaultPath string       `json:"defaultPath"`
	Directory   bool         `json:"directory"`
	Multiple    bool         `json:"multiple"`
	// Properties 兼容 Electron 风格：openDirectory / multiSelections / openFile
	Properties []string `json:"properties"`
}

// FileFilter 文件类型过滤器。
type FileFilter struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
}

// MenuItem 原生菜单项配置。
type MenuItem struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Type     string     `json:"type"` // "normal" / "separator" / "checkbox" / "radio"
	Checked  bool       `json:"checked"`
	Disabled bool       `json:"disabled"`
	Shortcut string     `json:"shortcut"`
	Submenu  []MenuItem `json:"submenu"`
	// Click 菜单项点击回调（Go 侧注入，不参与 JSON 序列化）
	Click func() `json:"-"`
}

// TrayOptions 系统托盘配置。
type TrayOptions struct {
	Icon    string     `json:"icon"`    // 图标路径或 base64
	Tooltip string     `json:"tooltip"` // 鼠标悬停提示文本
	Menu    []MenuItem `json:"menu"`    // 托盘右键菜单
}
