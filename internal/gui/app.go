package gui

import (
	"sync"
)

// App 表示全局 GUI 应用管理器。
type App struct {
	mu       sync.RWMutex
	windows  map[uint64]*Window
	native   NativeApp
	events   map[string][]func(interface{})
	isReady  bool
	isQuit   bool
	quitOnce sync.Once
}

var (
	globalAppOnce sync.Once
	globalApp     *App
)

// GetApp 获取全局单例 GUI App。
func GetApp() *App {
	globalAppOnce.Do(func() {
		globalApp = &App{
			windows: make(map[uint64]*Window),
			events:  make(map[string][]func(interface{})),
		}
		globalApp.native = createNativeApp(globalApp)
	})
	return globalApp
}

func (a *App) registerWindow(w *Window) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.windows[w.ID()] = w
}

func (a *App) unregisterWindow(id uint64) {
	a.mu.Lock()
	delete(a.windows, id)
	remaining := len(a.windows)
	quitting := a.isQuit
	a.mu.Unlock()

	// 当最后一个窗口关闭且应用未在退出流程中时，默认退出应用。
	// 注意：Quit() 内部会销毁全部窗口并走到这里，sync.Once 不可重入，
	// 必须以 isQuit 标志拦截递归调用，否则同 goroutine 重入即死锁。
	if remaining == 0 && !quitting {
		a.Quit()
	}
}

// Windows 返回当前所有活跃窗口列表。
func (a *App) Windows() []*Window {
	a.mu.RLock()
	defer a.mu.RUnlock()
	list := make([]*Window, 0, len(a.windows))
	for _, w := range a.windows {
		list = append(list, w)
	}
	return list
}

// GetWindowByID 根据 ID 获取窗口。
func (a *App) GetWindowByID(id uint64) *Window {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.windows[id]
}

// Run 启动原生 OS 消息循环并阻塞。
func (a *App) Run() error {
	a.mu.Lock()
	a.isReady = true
	a.mu.Unlock()

	a.Emit("ready", nil)
	if a.native != nil {
		return a.native.Run()
	}
	return nil
}

// Quit 退出桌面应用。
func (a *App) Quit() {
	a.quitOnce.Do(func() {
		a.mu.Lock()
		a.isQuit = true
		a.mu.Unlock()

		a.Emit("before-quit", nil)

		// 销毁所有窗口
		for _, w := range a.Windows() {
			w.Close()
		}

		a.Emit("quit", nil)
		if a.native != nil {
			a.native.Quit()
		}
	})
}

// PostAction 投递任务到 OS UI 主线程执行。
func (a *App) PostAction(fn func()) {
	if a.native != nil {
		a.native.PostAction(fn)
	} else {
		go fn()
	}
}

// ShowDialog 弹出原生消息框或文件选择框。
func (a *App) ShowDialog(opts DialogOptions) (int, []string, error) {
	if a.native != nil {
		return a.native.ShowDialog(opts)
	}
	return -1, nil, nil
}

// CreateTray 创建系统托盘图标。
func (a *App) CreateTray(opts TrayOptions) (NativeTray, error) {
	if a.native != nil {
		return a.native.CreateTray(opts)
	}
	return nil, nil
}

// On 订阅全局应用生命周期事件。
func (a *App) On(event string, handler func(interface{})) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events[event] = append(a.events[event], handler)

	// 如果应用已 ready 且当前订阅的是 ready 事件，立即触发
	if event == "ready" && a.isReady {
		go handler(nil)
	}
}

// Emit 派发全局应用事件。
func (a *App) Emit(event string, data interface{}) {
	a.mu.RLock()
	handlers := append([]func(interface{}){}, a.events[event]...)
	a.mu.RUnlock()

	for _, h := range handlers {
		h(data)
	}
}
