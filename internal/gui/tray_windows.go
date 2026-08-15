//go:build windows

// 系统托盘（Shell_NotifyIconW）与原生弹出菜单的 Windows 实装。
// 托盘图标与快捷键共享一个 UI 线程上的隐藏消息窗口，用于接收
// 托盘鼠标回调（自定义消息）、菜单命令（WM_COMMAND）与热键（WM_HOTKEY）。
package gui

import (
	"fmt"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	wmTrayIcon = 0x8000 + 0x1000 // WM_APP + 4096，避开常见 WM_APP 占用

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfGrayed    = 0x00000001
	mfChecked   = 0x00000008
	mfPopup     = 0x00000010

	tpmRightButton = 0x00000002

	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	wmCommand = 0x0111
)

// notifyIconDataW 对应 Win32 NOTIFYICONDATAW（x64 布局，含对齐填充）。
type notifyIconDataW struct {
	cbSize           uint32
	_                uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	_                uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uTimeout         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	_                uint32
	guidItem         [16]byte
	hBalloonIcon     syscall.Handle
}

type pointStruct struct{ X, Y int32 }

func copyUTF16Into(dst []uint16, s string) {
	copy(dst, utf16.Encode([]rune(s)))
}

// ---------- 共享消息窗口 ----------

var (
	msgWindowOnce sync.Once
	msgHwnd       syscall.Handle

	trayMu   sync.Mutex
	trayByID = make(map[uint32]*windowsTray)

	menuCmdMu sync.Mutex
	// menuCmds 弹出菜单命令 ID → 菜单项（弹出时重建；同一时刻仅一个菜单生效）
	menuCmds    = make(map[uint32]*MenuItem)
	menuCmdNext = uint32(0x1000)
)

// ensureMessageWindow 在 UI 线程创建共享隐藏消息窗口（幂等）。
func ensureMessageWindow() syscall.Handle {
	msgWindowOnce.Do(func() {
		className, _ := syscall.UTF16PtrFromString("AlukaMessageWindow")
		hInstance, _, _ := procGetModuleHandleW.Call(0)

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   syscall.NewCallback(messageWndProc),
			hInstance:     syscall.Handle(hInstance),
			lpszClassName: className,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		// HWND_MESSAGE 级隐藏窗口：仅收消息，不可见
		hwnd, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(className)),
			0,
			0, 0, 0, 0,
			uintptr(^uintptr(2)), /* HWND_MESSAGE */
			0, hInstance, 0,
		)
		msgHwnd = syscall.Handle(hwnd)
	})
	return msgHwnd
}

func messageWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmTrayIcon:
		trayID := uint32(wParam)
		mouseMsg := uint32(lParam)
		trayMu.Lock()
		tray := trayByID[trayID]
		trayMu.Unlock()
		if tray != nil {
			tray.handleNotify(mouseMsg)
		}
		return 0
	case wmCommand:
		itemID := uint32(wParam & 0xFFFF)
		menuCmdMu.Lock()
		item := menuCmds[itemID]
		menuCmdMu.Unlock()
		if item != nil && item.Click != nil {
			go item.Click()
		}
		return 0
	case wmHotkey:
		dispatchHotkey(int(wParam))
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

// ---------- 托盘实装 ----------

// windowsTray 真实系统托盘图标。
type windowsTray struct {
	parent   *Tray
	id       uint32
	iconPath string
	icon     syscall.Handle
	menuMu   sync.Mutex
	opts     TrayOptions
	menu     []MenuItem
}

func (a *windowsApp) CreateTray(opts TrayOptions) (NativeTray, error) {
	// Shell_NotifyIcon 要求在拥有回调窗口的线程（UI 线程）上调用
	if a.onUIThread() {
		return createTrayOnUIThread(opts)
	}
	resCh := make(chan struct {
		tray NativeTray
		err  error
	}, 1)
	a.PostAction(func() {
		tray, err := createTrayOnUIThread(opts)
		resCh <- struct {
			tray NativeTray
			err  error
		}{tray, err}
	})
	res := <-resCh
	return res.tray, res.err
}

// SetTrayParent 回链到 gui.Tray 包装层以派发事件（由 NewTray 调用）。
func (t *windowsTray) SetTrayParent(p *Tray) {
	t.parent = p
}

func createTrayOnUIThread(opts TrayOptions) (NativeTray, error) {
	ensureMessageWindow()

	t := &windowsTray{opts: opts, iconPath: opts.Icon, menu: opts.Menu}
	t.id = nextTrayID()
	if opts.Icon != "" {
		t.icon = loadTrayIcon(opts.Icon)
	}

	var data notifyIconDataW
	data.cbSize = uint32(unsafe.Sizeof(data))
	data.hWnd = msgHwnd
	data.uID = t.id
	data.uFlags = nifMessage | nifIcon | nifTip
	data.uCallbackMessage = wmTrayIcon
	data.hIcon = t.icon
	copyUTF16Into(data.szTip[:], opts.Tooltip)

	ret, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if ret == 0 {
		// 上一次异常退出可能残留同 ID 图标，清理后重试一次
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
		ret2, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
		if ret2 == 0 {
			return nil, fmt.Errorf("gui: Shell_NotifyIcon add failed")
		}
	}

	trayMu.Lock()
	trayByID[t.id] = t
	trayMu.Unlock()
	return t, nil
}

func nextTrayID() uint32 {
	trayMu.Lock()
	defer trayMu.Unlock()
	for {
		trayIDCounter++
		id := uint32(trayIDCounter)
		if _, exists := trayByID[id]; !exists {
			return id
		}
	}
}

// loadTrayIcon 从 .ico 文件加载小图标，失败时回退可执行文件自带图标。
func loadTrayIcon(path string) syscall.Handle {
	p, err := syscall.UTF16PtrFromString(path)
	if err == nil {
		h, _, _ := procLoadImageW.Call(
			0,
			uintptr(unsafe.Pointer(p)),
			1, /* IMAGE_ICON */
			0, 0,
			lrLoadFromFile|lrDefaultSize,
		)
		if h != 0 {
			return syscall.Handle(h)
		}
	}
	// 回退：应用可执行文件自带图标（资源 ID 1）
	hInst, _, _ := procGetModuleHandleW.Call(0)
	hIcon, _, _ := procLoadImageW.Call(
		hInst,
		1,
		1, 0, 0, lrDefaultSize,
	)
	return syscall.Handle(hIcon)
}

func (t *windowsTray) notify(modify func(*notifyIconDataW)) {
	var data notifyIconDataW
	data.cbSize = uint32(unsafe.Sizeof(data))
	data.hWnd = msgHwnd
	data.uID = t.id
	data.uFlags = nifMessage | nifIcon | nifTip
	data.uCallbackMessage = wmTrayIcon
	t.menuMu.Lock()
	data.hIcon = t.icon
	copyUTF16Into(data.szTip[:], t.opts.Tooltip)
	t.menuMu.Unlock()
	if modify != nil {
		modify(&data)
	}
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
}

func (t *windowsTray) SetIcon(iconPath string) {
	icon := loadTrayIcon(iconPath)
	t.menuMu.Lock()
	t.iconPath = iconPath
	t.icon = icon
	t.menuMu.Unlock()
	t.notify(func(d *notifyIconDataW) { d.hIcon = icon })
}

func (t *windowsTray) SetTooltip(tooltip string) {
	t.menuMu.Lock()
	t.opts.Tooltip = tooltip
	t.menuMu.Unlock()
	t.notify(nil)
}

func (t *windowsTray) SetMenu(items []MenuItem) {
	t.menuMu.Lock()
	t.menu = items
	t.menuMu.Unlock()
}

// handleNotify 处理托盘区鼠标消息（UI 线程）。
func (t *windowsTray) handleNotify(mouseMsg uint32) {
	switch mouseMsg {
	case 0x0202: // WM_LBUTTONUP
		if t.parent != nil {
			go t.parent.Emit("click", nil)
		}
	case 0x0203: // WM_LBUTTONDBLCLK
		if t.parent != nil {
			go t.parent.Emit("double-click", nil)
		}
	case 0x0205, 0x007B: // WM_RBUTTONUP / WM_CONTEXTMENU
		t.showMenu()
	}
}

// showMenu 在光标处弹出原生上下文菜单（UI 线程）。
func (t *windowsTray) showMenu() {
	t.menuMu.Lock()
	items := t.menu
	t.menuMu.Unlock()
	if len(items) == 0 {
		return
	}

	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	menuCmdMu.Lock()
	menuCmds = make(map[uint32]*MenuItem)
	menuCmdNext = 0x1000
	menuCmdMu.Unlock()
	buildPopupMenu(hMenu, items)

	// TrackPopupMenu 需前台切换，否则菜单可能不消失（经典 Win32 问题）
	procSetForegroundWindow.Call(uintptr(msgHwnd))
	var pt pointStruct
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procTrackPopupMenu.Call(hMenu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(msgHwnd), 0)
	procPostMessageW.Call(uintptr(msgHwnd), 0x0000 /* WM_NULL */, 0, 0)
}

// buildPopupMenu 递归构建原生弹出菜单并登记命令 ID。
func buildPopupMenu(hMenu uintptr, items []MenuItem) {
	for i := range items {
		item := items[i]
		var flags uintptr

		switch item.Type {
		case "separator":
			flags = mfSeparator
			procAppendMenuW.Call(hMenu, flags, 0, 0)
			continue
		default:
			flags = mfString
			if item.Disabled {
				flags |= mfGrayed
			}
			if item.Checked {
				flags |= mfChecked
			}
			if len(item.Submenu) > 0 {
				sub, _, _ := procCreatePopupMenu.Call()
				buildPopupMenu(sub, item.Submenu)
				p, _ := syscall.UTF16PtrFromString(item.Label)
				procAppendMenuW.Call(hMenu, flags|mfPopup, sub, uintptr(unsafe.Pointer(p)))
				continue
			}
		}

		var cmdID uintptr
		menuCmdMu.Lock()
		cmdID = uintptr(menuCmdNext)
		menuCmds[menuCmdNext] = &items[i]
		menuCmdNext++
		menuCmdMu.Unlock()
		p, _ := syscall.UTF16PtrFromString(item.Label)
		procAppendMenuW.Call(hMenu, flags, cmdID, uintptr(unsafe.Pointer(p)))
	}
}

func (t *windowsTray) Destroy() {
	trayMu.Lock()
	delete(trayByID, t.id)
	trayMu.Unlock()

	var data notifyIconDataW
	data.cbSize = uint32(unsafe.Sizeof(data))
	data.hWnd = msgHwnd
	data.uID = t.id
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}
