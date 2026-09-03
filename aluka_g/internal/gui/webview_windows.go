//go:build windows

package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	shlwapi  = syscall.NewLazyDLL("shlwapi.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	// shell32 由 tray_windows.go 声明（托盘与文件夹对话框共用）

	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procUpdateWindow               = user32.NewProc("UpdateWindow")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procSetWindowTextW             = user32.NewProc("SetWindowTextW")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
	procGetWindowRect              = user32.NewProc("GetWindowRect")
	procGetClientRect              = user32.NewProc("GetClientRect")
	procSetWindowPos               = user32.NewProc("SetWindowPos")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procPostThreadMessageW         = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId         = kernel32.NewProc("GetCurrentThreadId")
	procMessageBoxW                = user32.NewProc("MessageBoxW")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
	procGetOpenFileNameW           = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW           = comdlg32.NewProc("GetSaveFileNameW")
	procSHBrowseForFolderW         = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW       = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree              = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx             = ole32.NewProc("CoInitializeEx")
	procSHCreateMemStream          = shlwapi.NewProc("SHCreateMemStream")
	procDwmSetWindowAttribute      = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetWindowLongW             = user32.NewProc("GetWindowLongW")
	procSetWindowLongW             = user32.NewProc("SetWindowLongW")
	procSendMessageW               = user32.NewProc("SendMessageW")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procCreateMenu                 = user32.NewProc("CreateMenu")
	procSetMenu                    = user32.NewProc("SetMenu")
	procDrawMenuBar                = user32.NewProc("DrawMenuBar")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	wsPopUp            = 0x80000000
	swShow             = 5
	swHide             = 0
	swMinimize         = 6
	swMaximize         = 3
	swRestore          = 9
	smCxScreen         = 0
	smCyScreen         = 1
	wmDestroy          = 0x0002
	wmClose            = 0x0010
	wmSize             = 0x0005
	wmSetFocus         = 0x0007
	wmKillFocus        = 0x0008
	wmMove             = 0x0200
	wmGetMinMaxInfo    = 0x0024
	wmUser             = 0x0400
	wmCustomTask       = wmUser + 101

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpShowWindow = 0x0040
	hwndTopMost   = ^uintptr(0) // -1
	hwndNoTopMost = ^uintptr(1) // -2

	wsThickFrame = 0x00040000
	gwlStyle     = ^uintptr(15) // -16
	gwlExStyle   = ^uintptr(19) // -20
	wsExLayered  = 0x00080000
	lwaAlpha     = 0x00000002
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type rect struct {
	Left, Top, Right, Bottom int32
}

type msg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var (
	windowClassRegistered sync.Once
	windowClassAtom       uint16
	registeredWindowsMu   sync.RWMutex
	registeredWindows     = make(map[syscall.Handle]*windowsWindow)
	taskQueueMu           sync.Mutex
	taskQueue             []func()
)

// windowsApp 采用专用 UI 线程模型（与 Wails/Electron 一致）：
// 首次使用时启动一个锁定 OS 线程的消息循环，所有窗口创建 / WebView2 挂载
// 均投递到该线程执行——WebView2 要求父 HWND 与 Controller 同线程且线程持续泵消息。
type windowsApp struct {
	app *App

	uiOnce     sync.Once
	loopDone   chan struct{}
	quitDone   chan struct{}
	pendingMu  sync.Mutex
	uiThreadID uint32
	loopExited bool
	quitOnce   sync.Once
}

func createNativeApp(app *App) NativeApp {
	return &windowsApp{app: app, loopDone: make(chan struct{}), quitDone: make(chan struct{})}
}

// ensureUILoop 启动（仅需一次）OS UI 消息循环线程，并等待其就绪。
func (a *windowsApp) ensureUILoop() {
	a.uiOnce.Do(func() {
		started := make(chan struct{})
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			tid, _, _ := procGetCurrentThreadId.Call()
			a.pendingMu.Lock()
			a.uiThreadID = uint32(tid)
			a.pendingMu.Unlock()
			close(started)

			var m msg
			for {
				ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
				if int32(ret) <= 0 {
					break
				}

				if m.Message == wmCustomTask {
					taskQueueMu.Lock()
					tasks := taskQueue
					taskQueue = nil
					taskQueueMu.Unlock()
					for _, t := range tasks {
						t()
					}
					continue
				}

				procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
				procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
			}

			a.pendingMu.Lock()
			a.loopExited = true
			a.pendingMu.Unlock()
			close(a.loopDone)
		}()
		<-started
	})
}

// Run 阻塞直至应用退出（消息循环在专用 UI 线程上运行）。
func (a *windowsApp) Run() error {
	a.ensureUILoop()
	<-a.quitDone
	return nil
}

// Quit 通知应用退出：只关闭退出信号，不向 UI 线程投递 WM_QUIT。
// 消息循环保持运行，PostAction 在退出流程后仍可安全使用
// （如测试中"关窗即退出"的 App 级行为，之后仍能创建新窗口）。
func (a *windowsApp) Quit() {
	a.quitOnce.Do(func() {
		close(a.quitDone)
	})
}

func (a *windowsApp) PostAction(fn func()) {
	a.ensureUILoop()

	a.pendingMu.Lock()
	tid := a.uiThreadID
	exited := a.loopExited
	a.pendingMu.Unlock()

	// 循环已退出（应用关闭后）时退化为直接执行，避免调用方永久阻塞
	if tid == 0 || exited {
		go fn()
		return
	}

	taskQueueMu.Lock()
	taskQueue = append(taskQueue, fn)
	taskQueueMu.Unlock()

	procPostThreadMessageW.Call(uintptr(tid), wmCustomTask, 0, 0)
}

// onUIThread 判断当前是否运行在 UI 消息循环线程上。
func (a *windowsApp) onUIThread() bool {
	tid, _, _ := procGetCurrentThreadId.Call()
	a.pendingMu.Lock()
	uiTid := uint32(a.uiThreadID)
	a.pendingMu.Unlock()
	return uint32(tid) == uiTid
}

// uiThreadAlive 判断 UI 消息循环是否仍在运行。
func (a *windowsApp) uiThreadAlive() bool {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	return a.uiThreadID != 0 && !a.loopExited
}

func (a *windowsApp) ShowDialog(opts DialogOptions) (int, []string, error) {
	opts = NormalizeDialogOptions(opts)
	if opts.Type == "openFile" || opts.Type == "saveFile" {
		if opts.Directory && opts.Type == "openFile" {
			path, ok := browseForFolder(opts.Title, opts.DefaultPath)
			if !ok {
				return 0, nil, nil
			}
			return 1, []string{path}, nil
		}
		return showFileDialog(opts)
	}

	titlePtr, _ := syscall.UTF16PtrFromString(opts.Title)
	msgPtr, _ := syscall.UTF16PtrFromString(opts.Message)
	flags, mapRet := messageBoxStyle(opts)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(msgPtr)), uintptr(unsafe.Pointer(titlePtr)), uintptr(flags))
	return mapRet(int(ret)), nil, nil
}

func showFileDialog(opts DialogOptions) (int, []string, error) {
	const (
		ofnPathMustExist   = 0x00000800
		ofnFileMustExist   = 0x00001000
		ofnExplorer        = 0x00080000
		ofnAllowMulti      = 0x00000200
		ofnOverwritePrompt = 0x00000002
		ofnHideReadOnly    = 0x00000004
	)

	bufSize := 4096
	if opts.Multiple {
		bufSize = 65536
	}
	buf := make([]uint16, bufSize)

	type ofnStruct struct {
		lStructSize       uint32
		hwndOwner         syscall.Handle
		hInstance         syscall.Handle
		lpstrFilter       *uint16
		lpstrCustomFilter *uint16
		nMaxCustFilter    uint32
		nFilterIndex      uint32
		lpstrFile         *uint16
		nMaxFile          uint32
		lpstrFileTitle    *uint16
		nMaxFileTitle     uint32
		lpstrInitialDir   *uint16
		lpstrTitle        *uint16
		Flags             uint32
		nFileOffset       uint16
		nFileExtension    uint16
		lpstrDefExt       *uint16
		lCustData         uintptr
		lpfnHook          uintptr
		lpTemplateName    *uint16
	}

	filterStr := win32FilterString(opts.Filters)
	filterUtf16, _ := syscall.UTF16PtrFromString(filterStr)
	titleUtf16, _ := syscall.UTF16PtrFromString(opts.Title)

	var initialDir *uint16
	if opts.DefaultPath != "" {
		dir := opts.DefaultPath
		if isDir, exists := osStat(dir); exists && !isDir {
			name, _ := syscall.UTF16FromString(filepath.Base(dir))
			copy(buf, name)
			parent := filepath.Dir(dir)
			initialDir, _ = syscall.UTF16PtrFromString(parent)
		} else {
			initialDir, _ = syscall.UTF16PtrFromString(dir)
		}
	}

	flags := uint32(ofnExplorer | ofnHideReadOnly | ofnPathMustExist)
	if opts.Type == "openFile" {
		flags |= ofnFileMustExist
		if opts.Multiple {
			flags |= ofnAllowMulti
		}
	} else {
		flags |= ofnOverwritePrompt
	}

	ofn := ofnStruct{
		lStructSize:     uint32(unsafe.Sizeof(ofnStruct{})),
		lpstrFilter:     filterUtf16,
		lpstrFile:       &buf[0],
		nMaxFile:        uint32(len(buf)),
		lpstrTitle:      titleUtf16,
		lpstrInitialDir: initialDir,
		Flags:           flags,
	}

	var ok uintptr
	if opts.Type == "saveFile" {
		ok, _, _ = procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	} else {
		ok, _, _ = procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	}
	if ok == 0 {
		return 0, nil, nil
	}
	if opts.Multiple && opts.Type == "openFile" {
		files := parseNULSeparatedPaths(buf)
		return 1, files, nil
	}
	return 1, []string{syscall.UTF16ToString(buf)}, nil
}

// osStat 返回 (isDir, exists)。
func osStat(path string) (isDir bool, exists bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, false
	}
	return fi.IsDir(), true
}

// browseForFolder 使用 SHBrowseForFolderW 选择目录。
func browseForFolder(title, defaultPath string) (string, bool) {
	type browseInfo struct {
		hwndOwner      syscall.Handle
		pidlRoot       uintptr
		pszDisplayName *uint16
		lpszTitle      *uint16
		ulFlags        uint32
		lpfn           uintptr
		lParam         uintptr
		iImage         int32
	}
	display := make([]uint16, 260)
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	const (
		bifReturnOnlyFSDirs = 0x00000001
		bifNewDialogStyle   = 0x00000040
	)
	bi := browseInfo{
		pszDisplayName: &display[0],
		lpszTitle:      titlePtr,
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle,
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", false
	}
	defer procCoTaskMemFree.Call(pidl)
	pathBuf := make([]uint16, 260)
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return "", false
	}
	path := syscall.UTF16ToString(pathBuf)
	if path == "" {
		return "", false
	}
	_ = defaultPath // 经典 BrowseInfo 无可靠初始目录；保留参数便于日后升级 IFileDialog
	return path, true
}

// windowsWindow 实现 NativeWindow 接口。
type windowsWindow struct {
	hwnd      syscall.Handle
	opts      WindowOptions
	parent    *Window
	width     int
	height    int
	x         int
	y         int
	maximized bool
	minWidth  int
	minHeight int
	maxWidth  int
	maxHeight int
	// lastDragClick 上次拖拽区按下时刻（双击最大化判定）
	lastDragClick time.Time

	// WebView2 渲染层状态（字段由 UI 线程读写，其他线程经 PostAction 投递）
	wvMu             sync.RWMutex
	wvController     uintptr
	wvWebview        uintptr
	wvReady          bool
	wvErr            error
	wvPendingURL     string
	wvPendingHTML    string
	wvPendingScripts []string

	// 原生窗口菜单栏
	menuMu   sync.Mutex
	hMenu    uintptr
	menuCmds map[uint32]*MenuItem
}

// minMaxInfo 对应 Win32 MINMAXINFO（5 个 POINT）。
type minMaxInfo struct {
	Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize pointStruct
}

func globalWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	registeredWindowsMu.RLock()
	w, ok := registeredWindows[hwnd]
	registeredWindowsMu.RUnlock()

	if msg == wmSize && ok {
		// 窗口尺寸变化时同步 WebView 渲染层边界（本回调即 UI 线程）
		w.syncScreenRect()
		w.wvUpdateBounds()
		width, height := w.parent.GetSize()
		w.emitWindowEvent("resize", map[string]interface{}{
			"width":  width,
			"height": height,
		})
	}

	if msg == wmMove && ok {
		w.syncScreenRect()
		x, y := w.parent.GetPosition()
		w.emitWindowEvent("move", map[string]interface{}{"x": x, "y": y})
	}

	if msg == wmSetFocus && ok {
		w.emitWindowEvent("focus", true)
	}

	if msg == wmKillFocus && ok {
		w.emitWindowEvent("blur", false)
	}

	if msg == wmNCHitTest && ok {
		// 无边框窗口边缘热区（父窗口可见像素时生效；WebView 覆盖区由前端热区兜底）
		if hit := w.framelessEdgeHitTest(lParam); hit != 0 {
			return hit
		}
	}

	switch msg {
	case wmCommand:
		if ok && w != nil {
			itemID := uint32(wParam & 0xFFFF)
			w.menuMu.Lock()
			var item *MenuItem
			if w.menuCmds != nil {
				item = w.menuCmds[itemID]
			}
			w.menuMu.Unlock()
			if item != nil && item.Click != nil {
				go item.Click()
			}
		}
		return 0
	case wmGetMinMaxInfo:
		if ok {
			mmi := (*minMaxInfo)(unsafe.Pointer(lParam)) //nolint:govet // Win32 回调指针
			if w.minWidth > 0 || w.minHeight > 0 {
				mmi.MinTrackSize.X = int32(max(w.minWidth, int(mmi.MinTrackSize.X)))
				mmi.MinTrackSize.Y = int32(max(w.minHeight, int(mmi.MinTrackSize.Y)))
			}
			if w.maxWidth > 0 || w.maxHeight > 0 {
				if w.maxWidth > 0 && mmi.MaxTrackSize.X > int32(w.maxWidth) {
					mmi.MaxTrackSize.X = int32(w.maxWidth)
				}
				if w.maxHeight > 0 && mmi.MaxTrackSize.Y > int32(w.maxHeight) {
					mmi.MaxTrackSize.Y = int32(w.maxHeight)
				}
			}
		}
	case wmClose:
		if ok && w.parent != nil {
			if w.parent.IsClosed() {
				w.destroyWindowMenu()
				w.wvCloseController()
				procDestroyWindow.Call(uintptr(hwnd))
				return 0
			}
			// TryClose：拦截失败则吞掉 WM_CLOSE，窗口保持
			w.parent.TryClose()
		}
		return 0
	case wmDestroy:
		registeredWindowsMu.Lock()
		delete(registeredWindows, hwnd)
		registeredWindowsMu.Unlock()
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func ensureWindowClass() error {
	var err error
	windowClassRegistered.Do(func() {
		className, _ := syscall.UTF16PtrFromString("AlukaWindowClass")
		hInstance, _, _ := procGetModuleHandleW.Call(0)

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			style:         0x0003, // CS_HREDRAW | CS_VREDRAW
			lpfnWndProc:   syscall.NewCallback(globalWndProc),
			hInstance:     syscall.Handle(hInstance),
			lpszClassName: className,
			hbrBackground: 6, // COLOR_WINDOW + 1
		}

		atom, _, lastErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			err = fmt.Errorf("register window class failed: %w", lastErr)
		} else {
			windowClassAtom = uint16(atom)
		}
	})
	return err
}

// createNativeWindow 在专用 UI 线程上创建窗口（WebView2 要求父 HWND 与
// Controller 同线程且该线程持续泵消息），并同步等待创建结果。
func createNativeWindow(opts WindowOptions, parent *Window) (NativeWindow, error) {
	app := GetApp()
	if wa, ok := app.native.(*windowsApp); ok && wa.onUIThread() {
		// 已在 UI 线程上（如 UI 回调内的再创建），直接执行避免自锁
		return createWindowOnUIThread(opts, parent)
	}

	resCh := make(chan struct {
		win NativeWindow
		err error
	}, 1)
	app.PostAction(func() {
		win, err := createWindowOnUIThread(opts, parent)
		resCh <- struct {
			win NativeWindow
			err error
		}{win, err}
	})
	res := <-resCh
	return res.win, res.err
}

func createWindowOnUIThread(opts WindowOptions, parent *Window) (NativeWindow, error) {
	if err := ensureWindowClass(); err != nil {
		return nil, err
	}

	className, _ := syscall.UTF16PtrFromString("AlukaWindowClass")
	title, _ := syscall.UTF16PtrFromString(opts.Title)
	hInstance, _, _ := procGetModuleHandleW.Call(0)

	style := uint32(wsOverlappedWindow)
	if opts.Frame != nil && !*opts.Frame {
		style = wsPopUp | wsVisible
	}
	// resizable 选项（nil 默认 true）：无边框 + 不可调整大小时去掉 THICKFRAME
	if opts.Resizable != nil && !*opts.Resizable {
		style &^= wsThickFrame
	}

	// 居中计算
	scrW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	scrH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	posX := opts.X
	posY := opts.Y
	if opts.Center || (posX == 0 && posY == 0) {
		posX = (int(scrW) - opts.Width) / 2
		posY = (int(scrH) - opts.Height) / 2
	}

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(style),
		uintptr(posX),
		uintptr(posY),
		uintptr(opts.Width),
		uintptr(opts.Height),
		0,
		0,
		hInstance,
		0,
	)

	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx failed: %w", err)
	}

	h := syscall.Handle(hwnd)
	win := &windowsWindow{
		hwnd:      h,
		opts:      opts,
		parent:    parent,
		width:     opts.Width,
		height:    opts.Height,
		x:         posX,
		y:         posY,
		minWidth:  opts.MinWidth,
		minHeight: opts.MinHeight,
		maxWidth:  opts.MaxWidth,
		maxHeight: opts.MaxHeight,
	}
	// 初始导航目标（WebView2 就绪后应用）
	win.wvPendingURL = translateAlukaURL(opts.URL)
	win.wvPendingHTML = opts.HTML

	// 应用级图标（--icon 内嵌）→ 标题栏/任务栏
	applyAppIcon(h)

	// Windows 11 现代背景特效（Mica / Acrylic / MicaAlt）；旧系统上调用静默失败
	applyBackgroundEffect(h, opts.BackgroundEffect)

	if opts.AlwaysOnTop {
		win.SetAlwaysOnTop(true)
	}

	registeredWindowsMu.Lock()
	registeredWindows[h] = win
	registeredWindowsMu.Unlock()

	if !opts.Hidden {
		win.Show()
	}

	// 在 OS UI 线程上异步挂载 WebView2 渲染层（失败时窗口退化为普通 Win32 窗口）
	GetApp().PostAction(win.initWebView2)

	return win, nil
}

func (w *windowsWindow) Show() {
	procShowWindow.Call(uintptr(w.hwnd), swShow)
	procUpdateWindow.Call(uintptr(w.hwnd))
}

func (w *windowsWindow) Hide() {
	procShowWindow.Call(uintptr(w.hwnd), swHide)
}

// destroyWindowSafe 销毁窗口：DestroyWindow 仅允许由创建线程调用，
// 跨线程时投递 WM_CLOSE 交由 UI 线程窗口过程完成关闭流程。
func (w *windowsWindow) destroyWindowSafe() {
	app := GetApp()
	if wa, ok := app.native.(*windowsApp); ok && !wa.onUIThread() && wa.uiThreadAlive() {
		procPostMessageW.Call(uintptr(w.hwnd), wmClose, 0, 0)
		return
	}
	// 同线程：先优雅关闭 WebView2 渲染层再销毁宿主窗口
	w.wvCloseController()
	procDestroyWindow.Call(uintptr(w.hwnd))
}

// wvCloseController 调用 ICoreWebView2Controller::Close 释放渲染层（UI 线程）。
func (w *windowsWindow) wvCloseController() {
	w.wvMu.RLock()
	controller := w.wvController
	w.wvController, w.wvWebview = 0, 0
	w.wvMu.RUnlock()
	if controller != 0 {
		_, _ = comCall(controller, wv2CtlClose)
	}
}

func (w *windowsWindow) Close() {
	w.destroyWindowSafe()
}

func (w *windowsWindow) Destroy() {
	w.destroyWindowSafe()
}

func (w *windowsWindow) Center() {
	scrW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	scrH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	w.x = (int(scrW) - w.width) / 2
	w.y = (int(scrH) - w.height) / 2
	procSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(w.x), uintptr(w.y), uintptr(w.width), uintptr(w.height), swpNoZOrder)
}

func (w *windowsWindow) SetTitle(title string) {
	ptr, _ := syscall.UTF16PtrFromString(title)
	procSetWindowTextW.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(ptr)))
}

func (w *windowsWindow) SetSize(width, height int) {
	w.width = width
	w.height = height
	// SWP_NOMOVE：仅改尺寸。x/y 缓存在系统拖动后可能滞后，
	// 携带旧坐标会把窗口瞬移回创建时位置（表现为拖拽开始时闪跳）。
	procSetWindowPos.Call(uintptr(w.hwnd), 0, 0, 0, uintptr(w.width), uintptr(w.height), swpNoZOrder|swpNoMove)
}

func (w *windowsWindow) GetSize() (int, int) {
	var r rect
	procGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r)))
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

func (w *windowsWindow) SetPosition(x, y int) {
	w.x = x
	w.y = y
	// SWP_NOSIZE：仅改位置，避免用过期的 width/height 意外还原窗口尺寸。
	procSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(w.x), uintptr(w.y), 0, 0, swpNoZOrder|swpNoSize)
}

func (w *windowsWindow) GetPosition() (int, int) {
	var r rect
	procGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r)))
	return int(r.Left), int(r.Top)
}

// syncScreenRect 以实时 GetWindowRect 回写缓存的几何字段。
// 系统级拖动 / 边缘缩放（StartDragMove / StartResize）只产生 WM_MOVE / WM_SIZE，
// 不会经过 SetPosition / SetSize，缓存若不回写会一直停留在创建时的值。
// 本回调与窗口动作（PostAction）同在 UI 线程执行，无并发写风险。
func (w *windowsWindow) syncScreenRect() {
	var r rect
	procGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r)))
	w.x, w.y = int(r.Left), int(r.Top)
	w.width, w.height = int(r.Right-r.Left), int(r.Bottom-r.Top)
}

func (w *windowsWindow) SetMinSize(width, height int) {
	w.minWidth, w.minHeight = width, height
}

func (w *windowsWindow) SetMaxSize(width, height int) {
	w.maxWidth, w.maxHeight = width, height
}

func (w *windowsWindow) SetResizable(resizable bool) {
	style, _, _ := procGetWindowLongW.Call(uintptr(w.hwnd), gwlStyle)
	if resizable {
		style |= wsThickFrame
	} else {
		style &^= wsThickFrame
	}
	procSetWindowLongW.Call(uintptr(w.hwnd), gwlStyle, style)
	const swpFrameChanged = 0x0020
	procSetWindowPos.Call(uintptr(w.hwnd), 0, 0, 0, 0, 0,
		swpFrameChanged|swpNoZOrder|swpNoMove|swpNoSize)
}

func (w *windowsWindow) SetOpacity(opacity float64) {
	if opacity < 0 {
		opacity = 0
	}
	if opacity > 1 {
		opacity = 1
	}
	style, _, _ := procGetWindowLongW.Call(uintptr(w.hwnd), gwlExStyle)
	procSetWindowLongW.Call(uintptr(w.hwnd), gwlExStyle, style|wsExLayered)
	alpha := byte(opacity * 255)
	procSetLayeredWindowAttributes.Call(uintptr(w.hwnd), 0, uintptr(alpha), lwaAlpha)
}

// applyBackgroundEffect 应用 DWM 系统背景特效（Windows 11 22H2+，低版本静默忽略）。
func applyBackgroundEffect(hwnd syscall.Handle, effect string) {
	var backdrop uint32
	switch strings.ToLower(effect) {
	case "", "none":
		return
	case "mica":
		backdrop = 2 // DWMSBT_MAINWINDOW
	case "acrylic":
		backdrop = 3 // DWMSBT_TRANSIENTWINDOW
	case "micaalt", "mica-alt":
		backdrop = 4 // DWMSBT_TABBEDWINDOW
	default:
		return
	}
	const dwmwaSystemBackdropType = 38
	procDwmSetWindowAttribute.Call(
		uintptr(hwnd),
		uintptr(dwmwaSystemBackdropType),
		uintptr(unsafe.Pointer(&backdrop)),
		unsafe.Sizeof(backdrop),
	)
}

// ---------- 无边框拖拽 / 边缘缩放（UI 线程） ----------

// 非客户区命中测试码
const (
	htCaption       = 2
	htClient        = 1
	htLeft          = 10
	htRight         = 11
	htTop           = 12
	htTopLeft       = 13
	htTopRight      = 14
	htBottom        = 15
	htBottomLeft    = 16
	htBottomRight   = 17
	wmNCHitTest     = 0x0084
	wmNCLButtonDown = 0x00A1
)

var procReleaseCapture = user32.NewProc("ReleaseCapture")

// StartDragMove 进入系统原生窗口移动循环（前端拖拽区 mousedown 触发）。
// 400ms 内连续两次触发视为双击 → 切换最大化（拖动模态循环会吞掉 JS 侧
// dblclick 事件，双击判定必须在原生侧完成）。
func (w *windowsWindow) StartDragMove() {
	if w.IsMaximized() {
		return
	}
	now := time.Now()
	if now.Sub(w.lastDragClick) < 400*time.Millisecond {
		w.lastDragClick = time.Time{}
		if w.IsMaximized() {
			w.Unmaximize()
		} else {
			w.Maximize()
		}
		return
	}
	w.lastDragClick = now
	procReleaseCapture.Call()
	procSendMessageW.Call(uintptr(w.hwnd), wmNCLButtonDown, htCaption, 0)
}

// resizeHitCodes 边缘方向 → 非客户区命中码。
var resizeHitCodes = map[string]uintptr{
	"left": htLeft, "right": htRight, "top": htTop, "bottom": htBottom,
	"topLeft": htTopLeft, "topRight": htTopRight,
	"bottomLeft": htBottomLeft, "bottomRight": htBottomRight,
}

// StartResize 进入系统原生边缘缩放循环（前端边缘热区 mousedown 触发）。
func (w *windowsWindow) StartResize(dir string) {
	hit, ok := resizeHitCodes[dir]
	if !ok {
		return
	}
	procReleaseCapture.Call()
	procSendMessageW.Call(uintptr(w.hwnd), wmNCLButtonDown, hit, 0)
}

// framelessEdgeHitTest 无边框窗口的边缘命中测试（WM_NCHITTEST，屏幕坐标）。
func (w *windowsWindow) framelessEdgeHitTest(lParam uintptr) uintptr {
	if w.opts.Frame == nil || *w.opts.Frame {
		return 0 // 未命中（走默认处理）
	}
	x := int16(lParam & 0xFFFF)
	y := int16(lParam >> 16)
	var r rect
	procGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r)))
	const b = 8
	left := int(x) < int(r.Left)+b
	right := int(x) >= int(r.Right)-b
	top := int(y) < int(r.Top)+b
	bottom := int(y) >= int(r.Bottom)-b
	switch {
	case left && top:
		return htTopLeft
	case left && bottom:
		return htBottomLeft
	case right && top:
		return htTopRight
	case right && bottom:
		return htBottomRight
	case left:
		return htLeft
	case right:
		return htRight
	case top:
		return htTop
	case bottom:
		return htBottom
	}
	return 0
}

func (w *windowsWindow) SetAlwaysOnTop(alwaysOnTop bool) {
	target := hwndNoTopMost
	if alwaysOnTop {
		target = hwndTopMost
	}
	procSetWindowPos.Call(uintptr(w.hwnd), target, uintptr(w.x), uintptr(w.y), uintptr(w.width), uintptr(w.height), swpShowWindow)
}

func (w *windowsWindow) SetFullscreen(fullscreen bool) {
	if fullscreen {
		w.Maximize()
	} else {
		w.Unmaximize()
	}
}

func (w *windowsWindow) IsFullscreen() bool {
	return false
}

func (w *windowsWindow) Minimize() {
	procShowWindow.Call(uintptr(w.hwnd), swMinimize)
}

func (w *windowsWindow) Maximize() {
	procShowWindow.Call(uintptr(w.hwnd), swMaximize)
	w.maximized = true
}

func (w *windowsWindow) Unmaximize() {
	procShowWindow.Call(uintptr(w.hwnd), swRestore)
	w.maximized = false
}

func (w *windowsWindow) IsMaximized() bool {
	return w.maximized
}

// Navigate 导航到指定 URL（aluka://app/* 虚拟协议映射为内存资产域）。
func (w *windowsWindow) Navigate(url string) {
	target := translateAlukaURL(url)
	w.wvMu.Lock()
	w.wvPendingURL = target
	w.wvPendingHTML = ""
	ready := w.wvReady
	w.wvMu.Unlock()
	if ready {
		GetApp().PostAction(w.wvApplyTarget)
	}
}

// SetHTML 直接加载 HTML 字符串。
func (w *windowsWindow) SetHTML(html string) {
	w.wvMu.Lock()
	w.wvPendingHTML = html
	w.wvPendingURL = ""
	ready := w.wvReady
	w.wvMu.Unlock()
	if ready {
		GetApp().PostAction(w.wvApplyTarget)
	}
}

// emitWindowEvent 把窗口级事件投递到宿主 Window（Go 侧 handler + 前端广播）。
// globalWndProc 已在 UI 线程执行，Emit 内部对前端经 ExecuteScript（同样要求 UI 线程）。
func (w *windowsWindow) emitWindowEvent(name string, data interface{}) {
	if w.parent == nil || w.parent.IsClosed() {
		return
	}
	w.parent.Emit(name, data)
}

// ExecuteScript 在渲染进程中执行 JavaScript（就绪前投递的任务会排队）。
func (w *windowsWindow) ExecuteScript(js string) {
	w.wvMu.Lock()
	ready := w.wvReady
	if !ready {
		w.wvPendingScripts = append(w.wvPendingScripts, js)
	}
	w.wvMu.Unlock()
	if ready {
		GetApp().PostAction(func() {
			w.wvMu.RLock()
			webview := w.wvWebview
			w.wvMu.RUnlock()
			if webview == 0 {
				return
			}
			buf := newUTF16Buf(js)
			_, _ = comCall(webview, wv2ExecuteScript, buf.ptr(), noopComHandler)
			runtime.KeepAlive(buf)
		})
	}
}

// EvaluateScript 在渲染进程执行 JavaScript 并回调结果。
//
// result 为脚本返回值的 JSON 序列化字符串（LPWSTR，调用方无需释放——本函数已释放）；
// 脚本返回 Promise 时 WebView2 会等待其 settle 后再回调：
//   - resolve → resultObjectAsJson 为 resolve 值的 JSON 序列化
//   - reject  → errorCode 为失败码（脚本抛异常亦同）
//
// 注意：comHandler 一旦移交给 WebView2 即由 Go 侧 liveHandlers 常驻持有（防止被 GC），
// 每次调用会保留一个轻量回调对象；为控制泄漏，invoke 内会释放对 cb 的引用。
func (w *windowsWindow) EvaluateScript(js string, cb func(result string, err error)) {
	if cb == nil {
		w.ExecuteScript(js)
		return
	}
	w.wvMu.RLock()
	ready := w.wvReady
	w.wvMu.RUnlock()
	if !ready {
		cb("", fmt.Errorf("gui: webview not ready"))
		return
	}
	GetApp().PostAction(func() {
		w.wvMu.RLock()
		webview := w.wvWebview
		w.wvMu.RUnlock()
		if webview == 0 {
			cb("", fmt.Errorf("gui: webview not ready"))
			return
		}
		var cbRef func(result string, err error)
		cbRef = cb
		handler := newComHandler(func(errorCode, resultPtr uintptr) uintptr {
			// 先转存并清空闭包引用，避免长生命周期 handler 拖住调用方上下文
			done := cbRef
			cbRef = nil
			if done == nil {
				return 0
			}
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[aluka:gui] EvaluateScript invoke panic: %v\n", r)
				}
			}()
			result := ""
			if resultPtr != 0 {
				result = utf16PtrToString(resultPtr)
				procCoTaskMemFree.Call(resultPtr)
			}
			var err error
			if int32(errorCode) < 0 {
				err = fmt.Errorf("gui: script execution failed: 0x%08X", uint32(errorCode))
			}
			done(result, err)
			return 0
		})
		buf := newUTF16Buf(js)
		if _, err := comCall(webview, wv2ExecuteScript, buf.ptr(), handler); err != nil {
			// ExecuteScript 调用即失败：handler 不会被调用，主动回调避免挂起
			cbRef = nil
			cb("", err)
			return
		}
		runtime.KeepAlive(buf)
	})
}

// OpenDevTools 打开 WebView2 开发者工具窗口。
func (w *windowsWindow) OpenDevTools() {
	w.wvMu.RLock()
	webview := w.wvWebview
	w.wvMu.RUnlock()
	if webview == 0 {
		return
	}
	GetApp().PostAction(func() {
		_, _ = comCall(webview, wv2OpenDevTools)
	})
}

// CapturePreviewPNG 捕获当前页面渲染为 PNG，回调返回图片字节。
// 经 ICoreWebView2::CapturePreview(format=PNG, IStream, handler) 写入内存流，
// 完成回调中读取流数据（IStream::Seek/Read vtable 调用）。
func (w *windowsWindow) CapturePreviewPNG(cb func(data []byte, err error)) {
	if cb == nil {
		return
	}
	w.wvMu.RLock()
	ready := w.wvReady
	w.wvMu.RUnlock()
	if !ready {
		cb(nil, fmt.Errorf("gui: webview not ready"))
		return
	}
	GetApp().PostAction(func() {
		w.wvMu.RLock()
		webview := w.wvWebview
		w.wvMu.RUnlock()
		if webview == 0 {
			cb(nil, fmt.Errorf("gui: webview not ready"))
			return
		}
		stream, _, _ := procSHCreateMemStream.Call(0, 0)
		if stream == 0 {
			cb(nil, fmt.Errorf("gui: SHCreateMemStream failed"))
			return
		}
		var cbRef func(data []byte, err error)
		cbRef = cb
		handler := newComHandler(func(errorCode, _ uintptr) uintptr {
			done := cbRef
			cbRef = nil
			if done == nil {
				return 0
			}
			data := readMemStream(stream)
			// 释放我们持有的 IStream 引用（SHCreateMemStream 初始引用计数 1）
			_, _ = comCall(stream, 2 /* IUnknown::Release */)
			var err error
			if int32(errorCode) < 0 {
				err = fmt.Errorf("gui: capture preview failed: 0x%08X", uint32(errorCode))
			}
			done(data, err)
			return 0
		})
		// ICoreWebView2::CapturePreview(COREWEBVIEW2_CAPTURE_PREVIEW_IMAGE_FORMAT_PNG=0, stream, handler)
		if _, err := comCall(webview, wv2CapturePreview, 0, stream, handler); err != nil {
			cbRef = nil
			_, _ = comCall(stream, 2)
			cb(nil, err)
			return
		}
		runtime.KeepAlive(stream)
	})
}

// readMemStream 从 SHCreateMemStream 创建的内存流读取全部数据。
// IStream vtable：Read=3（IUnknown 0/1/2 之后），Seek=5；Seek 到开头后循环 Read。
func readMemStream(stream uintptr) []byte {
	if stream == 0 {
		return nil
	}
	// IStream::Seek(0, STREAM_SEEK_SET=0, NULL)
	if _, err := comCall(stream, 5, 0, 0, 0); err != nil {
		return nil
	}
	var out []byte
	buf := make([]byte, 64*1024)
	for {
		var read uint32
		if _, err := comCall(stream, 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&read))); err != nil {
			break
		}
		if read == 0 {
			break
		}
		out = append(out, buf[:read]...)
	}
	return out
}

func (w *windowsWindow) destroyWindowMenu() {
	w.menuMu.Lock()
	defer w.menuMu.Unlock()
	if w.hMenu != 0 {
		procSetMenu.Call(uintptr(w.hwnd), 0)
		procDestroyMenu.Call(w.hMenu)
		w.hMenu = 0
		w.menuCmds = nil
	}
}

// SetMenu 挂载或更新窗口原生菜单栏。
func (w *windowsWindow) SetMenu(menu *Menu) {
	GetApp().PostAction(func() {
		w.menuMu.Lock()
		defer w.menuMu.Unlock()

		if w.hMenu != 0 {
			procSetMenu.Call(uintptr(w.hwnd), 0)
			procDestroyMenu.Call(w.hMenu)
			w.hMenu = 0
			w.menuCmds = nil
		}

		if menu == nil || len(menu.Items) == 0 {
			procDrawMenuBar.Call(uintptr(w.hwnd))
			return
		}

		hMenu, _, _ := procCreateMenu.Call()
		if hMenu == 0 {
			return
		}

		w.menuCmds = make(map[uint32]*MenuItem)
		var nextCmdID uint32 = 0x2000

		buildWindowMenuBar(hMenu, menu.Items, w.menuCmds, &nextCmdID)

		procSetMenu.Call(uintptr(w.hwnd), hMenu)
		procDrawMenuBar.Call(uintptr(w.hwnd))
		w.hMenu = hMenu
	})
}

// buildWindowMenuBar 递归构建顶级菜单栏与下拉子菜单。
func buildWindowMenuBar(hMenu uintptr, items []MenuItem, cmdMap map[uint32]*MenuItem, nextID *uint32) {
	for i := range items {
		item := &items[i]
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
				buildWindowMenuBar(sub, item.Submenu, cmdMap, nextID)
				p, _ := syscall.UTF16PtrFromString(item.Label)
				procAppendMenuW.Call(hMenu, flags|mfPopup, sub, uintptr(unsafe.Pointer(p)))
				continue
			}
		}

		cmdID := uintptr(*nextID)
		cmdMap[*nextID] = item
		*nextID++

		label := item.Label
		if item.Shortcut != "" {
			label += "\t" + item.Shortcut
		}
		p, _ := syscall.UTF16PtrFromString(label)
		procAppendMenuW.Call(hMenu, flags, cmdID, uintptr(unsafe.Pointer(p)))
	}
}

