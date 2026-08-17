package gui

import (
	"sync"
	"sync/atomic"
)

var trayIDCounter uint64

// Tray 表示一个系统托盘图标实例。
type Tray struct {
	id       uint64
	opts     TrayOptions
	native   NativeTray
	mu       sync.RWMutex
	events   map[string][]func(interface{})
	isClosed bool
}

// NewTray 创建一个新的系统托盘图标。
func NewTray(opts TrayOptions) (*Tray, error) {
	id := atomic.AddUint64(&trayIDCounter, 1)

	app := GetApp()
	nativeTray, err := app.CreateTray(opts)
	if err != nil {
		return nil, err
	}

	t := &Tray{
		id:     id,
		opts:   opts,
		native: nativeTray,
		events: make(map[string][]func(interface{})),
	}

	// 建立 native → 包装层回链，用于托盘鼠标事件派发
	if sp, ok := nativeTray.(interface{ SetTrayParent(*Tray) }); ok {
		sp.SetTrayParent(t)
	}
	return t, nil
}

// ID 返回托盘 ID。
func (t *Tray) ID() uint64 {
	return t.id
}

// SetIcon 设置托盘图标。
func (t *Tray) SetIcon(iconPath string) {
	t.mu.Lock()
	t.opts.Icon = iconPath
	t.mu.Unlock()
	if t.native != nil {
		t.native.SetIcon(iconPath)
	}
}

// SetTooltip 设置托盘鼠标悬浮提示文本。
func (t *Tray) SetTooltip(tooltip string) {
	t.mu.Lock()
	t.opts.Tooltip = tooltip
	t.mu.Unlock()
	if t.native != nil {
		t.native.SetTooltip(tooltip)
	}
}

// SetMenu 设置托盘上下文菜单。
func (t *Tray) SetMenu(items []MenuItem) {
	t.mu.Lock()
	t.opts.Menu = items
	t.mu.Unlock()
	if t.native != nil {
		t.native.SetMenu(items)
	}
}

// Destroy 销毁托盘图标。
func (t *Tray) Destroy() {
	t.mu.Lock()
	t.isClosed = true
	t.mu.Unlock()

	t.Emit("destroy", nil)
	if t.native != nil {
		t.native.Destroy()
	}
}

// On 订阅托盘事件。
func (t *Tray) On(event string, handler func(interface{})) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events[event] = append(t.events[event], handler)
}

// Emit 派发托盘事件。
func (t *Tray) Emit(event string, data interface{}) {
	t.mu.RLock()
	handlers := append([]func(interface{}){}, t.events[event]...)
	t.mu.RUnlock()

	for _, h := range handlers {
		h(data)
	}
}
