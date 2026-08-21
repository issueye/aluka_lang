package gui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
)

var windowIDCounter uint64

// Window 表示一个 Aluka GUI 窗口实例。
type Window struct {
	id             uint64
	opts           WindowOptions
	native         NativeWindow
	mu             sync.RWMutex
	events         map[string][]func(interface{})
	closeRequested []func() bool // 关闭前拦截回调（任一返回 false 则取消关闭）
	isClosed       bool
	closeOnce      sync.Once
}

// NewWindow 创建一个新的桌面窗口。
func NewWindow(opts WindowOptions) (*Window, error) {
	id := atomic.AddUint64(&windowIDCounter, 1)

	// 默认尺寸兜底
	if opts.Width <= 0 {
		opts.Width = 1024
	}
	if opts.Height <= 0 {
		opts.Height = 720
	}
	if opts.Title == "" {
		opts.Title = "Aluka App"
	}
	if opts.URL == "" && opts.HTML == "" {
		opts.URL = "aluka://app/index.html"
	}

	w := &Window{
		id:     id,
		opts:   opts,
		events: make(map[string][]func(interface{})),
	}

	// 创建平台原生窗口
	nativeWin, err := createNativeWindow(opts, w)
	if err != nil {
		return nil, fmt.Errorf("gui: create window failed: %w", err)
	}
	w.native = nativeWin

	// 注册到全局应用管理器
	GetApp().registerWindow(w)

	if opts.Opacity > 0 && opts.Opacity < 1 {
		w.SetOpacity(opts.Opacity)
	}
	if opts.Maximized {
		w.Maximize()
	}
	if opts.Minimized {
		w.Minimize()
	}

	return w, nil
}

// ID 返回窗口唯一标识符。
func (w *Window) ID() uint64 {
	return w.id
}

// Options 返回窗口配置。
func (w *Window) Options() WindowOptions {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.opts
}

// Show 显示窗口。
func (w *Window) Show() {
	if w.native != nil {
		w.native.Show()
		w.Emit("show", nil)
	}
}

// Hide 隐藏窗口。
func (w *Window) Hide() {
	if w.native != nil {
		w.native.Hide()
		w.Emit("hide", nil)
	}
}

// Close 关闭并销毁窗口（跳过 OnCloseRequested 拦截）。
// 系统关闭按钮、前端 close、JS win.close() 应走 TryClose；app.quit 走 Close。
func (w *Window) Close() {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.isClosed = true
		w.mu.Unlock()

		w.Emit("close", nil)
		if w.native != nil {
			w.native.Destroy()
		}
		GetApp().unregisterWindow(w.id)
	})
}

// IsClosed 报告窗口是否已进入销毁流程。
func (w *Window) IsClosed() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.isClosed
}

// TryClose 先走关闭拦截：全部回调放行后才 Close。任一回调返回 false 则取消。
func (w *Window) TryClose() bool {
	if w.IsClosed() {
		return true
	}
	if !w.RequestClose() {
		return false
	}
	w.Close()
	return true
}

// Center 将窗口居中。
func (w *Window) Center() {
	if w.native != nil {
		w.native.Center()
	}
}

// SetTitle 设置窗口标题。
func (w *Window) SetTitle(title string) {
	w.mu.Lock()
	w.opts.Title = title
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetTitle(title)
	}
}

// SetSize 设置窗口宽高。
func (w *Window) SetSize(width, height int) {
	w.mu.Lock()
	w.opts.Width = width
	w.opts.Height = height
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetSize(width, height)
	}
}

// GetSize 获取窗口当前宽高。
func (w *Window) GetSize() (int, int) {
	if w.native != nil {
		return w.native.GetSize()
	}
	return w.opts.Width, w.opts.Height
}

// SetPosition 设置窗口屏幕坐标。
func (w *Window) SetPosition(x, y int) {
	w.mu.Lock()
	w.opts.X = x
	w.opts.Y = y
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetPosition(x, y)
	}
}

// GetPosition 获取窗口屏幕坐标。
func (w *Window) GetPosition() (int, int) {
	if w.native != nil {
		return w.native.GetPosition()
	}
	return w.opts.X, w.opts.Y
}

// Minimize 最小化窗口。
func (w *Window) Minimize() {
	if w.native != nil {
		w.native.Minimize()
		w.Emit("minimize", nil)
	}
}

// Maximize 最大化窗口。
func (w *Window) Maximize() {
	if w.native != nil {
		w.native.Maximize()
		w.Emit("maximize", nil)
	}
}

// Unmaximize 还原最大化。
func (w *Window) Unmaximize() {
	if w.native != nil {
		w.native.Unmaximize()
		w.Emit("unmaximize", nil)
	}
}

// IsMaximized 查询是否已最大化。
func (w *Window) IsMaximized() bool {
	if w.native != nil {
		return w.native.IsMaximized()
	}
	return false
}

// SetFullscreen 设置全屏。
func (w *Window) SetFullscreen(fullscreen bool) {
	if w.native != nil {
		w.native.SetFullscreen(fullscreen)
		w.Emit("fullscreen", fullscreen)
	}
}

// IsFullscreen 查询是否全屏。
func (w *Window) IsFullscreen() bool {
	if w.native != nil {
		return w.native.IsFullscreen()
	}
	return false
}

// SetAlwaysOnTop 设置窗口置顶。
func (w *Window) SetAlwaysOnTop(alwaysOnTop bool) {
	w.mu.Lock()
	w.opts.AlwaysOnTop = alwaysOnTop
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetAlwaysOnTop(alwaysOnTop)
	}
}

// SetResizable 设置窗口是否允许调整大小。
func (w *Window) SetResizable(resizable bool) {
	w.mu.Lock()
	w.opts.Resizable = &resizable
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetResizable(resizable)
	}
}

// GetTitle 返回窗口标题。
func (w *Window) GetTitle() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.opts.Title
}

// SetOpacity 设置窗口整体不透明度（0.0 ~ 1.0，平台支持时生效）。
func (w *Window) SetOpacity(opacity float64) {
	if t, ok := w.native.(interface{ SetOpacity(opacity float64) }); ok {
		t.SetOpacity(opacity)
	}
}

// SetProgressBar 设置任务栏进度（0.0 ~ 1.0；progress<0 表示清除进度条）。
func (w *Window) SetProgressBar(progress float64) {
	if t, ok := w.native.(interface{ SetProgressBar(progress float64) }); ok {
		t.SetProgressBar(progress)
	}
}

// SetOverlayIcon 设置任务栏叠加图标（badge），icon 为 .ico 文件路径；空字符串清除。
func (w *Window) SetOverlayIcon(iconPath string) {
	if t, ok := w.native.(interface{ SetOverlayIcon(iconPath string) }); ok {
		t.SetOverlayIcon(iconPath)
	}
}

// SetMenu 将原生菜单栏挂载到窗口（平台支持时生效；菜单项点击回调运行在 Go 侧）。
func (w *Window) SetMenu(menu *Menu) {
	if t, ok := w.native.(interface{ SetMenu(menu *Menu) }); ok {
		t.SetMenu(menu)
	}
}

// SetMinSize 设置窗口最小尺寸约束。
func (w *Window) SetMinSize(width, height int) {
	w.mu.Lock()
	w.opts.MinWidth, w.opts.MinHeight = width, height
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetMinSize(width, height)
	}
}

// SetMaxSize 设置窗口最大尺寸约束。
func (w *Window) SetMaxSize(width, height int) {
	w.mu.Lock()
	w.opts.MaxWidth, w.opts.MaxHeight = width, height
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetMaxSize(width, height)
	}
}

// Navigate 导航到指定 URL。
func (w *Window) Navigate(url string) {
	w.mu.Lock()
	w.opts.URL = url
	w.mu.Unlock()
	if w.native != nil {
		w.native.Navigate(url)
	}
}

// SetHTML 设置 HTML 内容。
func (w *Window) SetHTML(html string) {
	w.mu.Lock()
	w.opts.HTML = html
	w.mu.Unlock()
	if w.native != nil {
		w.native.SetHTML(html)
	}
}

// ExecuteScript 在渲染进程 WebView 中执行 JavaScript 代码。
func (w *Window) ExecuteScript(js string) {
	if w.native != nil {
		w.native.ExecuteScript(js)
	}
}

// OpenDevTools 打开开发者工具。
func (w *Window) OpenDevTools() {
	if w.native != nil {
		w.native.OpenDevTools()
	}
}

// On 订阅窗口事件。返回取消订阅函数。
func (w *Window) On(event string, handler func(interface{})) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events[event] = append(w.events[event], handler)
	removed := false
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if removed {
			return
		}
		removed = true
		handlers := w.events[event]
		for i, h := range handlers {
			if sameHandler(h, handler) {
				w.events[event] = append(handlers[:i], handlers[i+1:]...)
				return
			}
		}
	}
}

// sameHandler 比较两个事件处理函数是否同一引用。
func sameHandler(a, b func(interface{})) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// Go 1.20 起函数指针可通过 reflect.ValueOf(a).Pointer() 比较
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// Off 取消指定事件处理函数的订阅（无匹配时静默）。
func (w *Window) Off(event string, handler func(interface{})) {
	w.mu.Lock()
	defer w.mu.Unlock()
	handlers := w.events[event]
	for i, h := range handlers {
		if sameHandler(h, handler) {
			w.events[event] = append(handlers[:i], handlers[i+1:]...)
			return
		}
	}
}

// OnCloseRequested 注册关闭前拦截回调：任一回调返回 false 将取消窗口关闭。
// 配合 TryClose 使用（JS win.close()、前端 window.aluka.window.close()、
// 原生 WM_CLOSE）。JS 侧回调经事件循环异步执行：Go 包装器立即返回 false，
// 由 JS 在回调里 return true 或 close(true) 完成真正关闭。
func (w *Window) OnCloseRequested(handler func() bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeRequested = append(w.closeRequested, handler)
}

// RequestClose 触发关闭前检查：全部回调放行（或未注册回调）才返回 true。
func (w *Window) RequestClose() bool {
	w.mu.RLock()
	handlers := append([]func() bool{}, w.closeRequested...)
	w.mu.RUnlock()
	for _, h := range handlers {
		if h != nil && !h() {
			return false
		}
	}
	return true
}

// fireLocalEvent 仅触发 Go 侧事件处理函数（不回投前端）。
// 用于"前端 → 后端"方向的事件，避免广播回前端造成回声循环。
func (w *Window) fireLocalEvent(event string, data interface{}) {
	w.mu.RLock()
	handlers := append([]func(interface{}){}, w.events[event]...)
	wildcards := append([]func(interface{}){}, w.events["*"]...)
	w.mu.RUnlock()

	for _, h := range handlers {
		h(data)
	}
	for _, h := range wildcards {
		h(map[string]interface{}{"event": event, "data": data})
	}
}

// Emit 派发窗口事件。
func (w *Window) Emit(event string, data interface{}) {
	w.mu.RLock()
	handlers := append([]func(interface{}){}, w.events[event]...)
	wildcards := append([]func(interface{}){}, w.events["*"]...)
	w.mu.RUnlock()

	for _, h := range handlers {
		h(data)
	}
	for _, h := range wildcards {
		h(map[string]interface{}{"event": event, "data": data})
	}

	// 同时将事件广播给前端 WebView 的 window.aluka.events
	if w.native != nil && !w.isClosed {
		payloadJSON, _ := json.Marshal(data)
		js := fmt.Sprintf(`if(window.aluka_dispatch){window.aluka_dispatch('event', {name:%q, data:%s});}`, event, string(payloadJSON))
		w.native.ExecuteScript(js)
	}
}
