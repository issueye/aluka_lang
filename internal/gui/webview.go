package gui

// NativeWindow 定义操作系统原生窗口的操作接口。
type NativeWindow interface {
	Show()
	Hide()
	Close()
	Destroy()
	Center()
	SetTitle(title string)
	SetSize(width, height int)
	GetSize() (int, int)
	SetPosition(x, y int)
	GetPosition() (int, int)
	SetMinSize(width, height int)
	SetMaxSize(width, height int)
	SetResizable(resizable bool)
	SetAlwaysOnTop(alwaysOnTop bool)
	SetFullscreen(fullscreen bool)
	IsFullscreen() bool
	Minimize()
	Maximize()
	Unmaximize()
	IsMaximized() bool
	Navigate(url string)
	SetHTML(html string)
	ExecuteScript(js string)
	// EvaluateScript 在渲染进程执行 JavaScript 并回调结果：
	// result 为脚本返回值的 JSON 序列化字符串（脚本返回 Promise 时等待其 settle）；
	// err 非 nil 表示脚本执行失败（语法错误 / 抛异常）。
	EvaluateScript(js string, cb func(result string, err error))
	// CapturePreviewPNG 捕获页面渲染为 PNG，回调返回图片字节（err 非 nil 表示失败）。
	CapturePreviewPNG(cb func(data []byte, err error))
	OpenDevTools()
}

// NativeApp 定义操作系统级应用生命周期抽象。
type NativeApp interface {
	Run() error
	Quit()
	PostAction(fn func())
	ShowDialog(opts DialogOptions) (int, []string, error)
	CreateTray(opts TrayOptions) (NativeTray, error)
}

// NativeTray 定义系统托盘操作接口。
type NativeTray interface {
	SetIcon(icon string)
	SetTooltip(tooltip string)
	SetMenu(items []MenuItem)
	Destroy()
}
