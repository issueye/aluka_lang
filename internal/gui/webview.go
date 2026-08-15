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
