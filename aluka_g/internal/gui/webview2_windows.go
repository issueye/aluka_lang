//go:build windows

// 纯 Go WebView2 绑定（无 CGO、不依赖 WebView2Loader.dll）：
// 直接定位系统已安装的 WebView2 运行时，加载 EmbeddedBrowserWebView.dll 并通过
// 其内部导出 CreateWebViewEnvironmentWithOptionsInternal 创建环境，
// 再以 COM vtable syscall 驱动 ICoreWebView2Environment/Controller/WebView 全套接口。
// vtable 序号与微软 WebView2 IDL 保持一致（以 wailsapp/go-webview2 交叉核对）。
package gui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// ---------- COM 基础设施 ----------

// comCall 调用 COM 对象 vtable 上指定序号的方法（0 起，IUnknown 占 0/1/2）。
// COM 对象指针以 uintptr 传递并做 vtable 指针运算，属既定模式（nolint 平息 govet 误报）。
func comCall(obj uintptr, methodIdx int, args ...uintptr) (uintptr, error) {
	if obj == 0 {
		return 0, fmt.Errorf("gui: nil COM object (method %d)", methodIdx)
	}
	vtbl := *(*uintptr)(unsafe.Pointer(obj))                                               //nolint:govet // COM vtable 遍历
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(methodIdx)*unsafe.Sizeof(uintptr(0)))) //nolint:govet // COM vtable 遍历
	callArgs := append([]uintptr{obj}, args...)
	r1, _, _ := syscall.SyscallN(fn, callArgs...)
	if int32(r1) < 0 {
		return r1, fmt.Errorf("gui: COM method %d failed: 0x%08X", methodIdx, uint32(r1))
	}
	return r1, nil
}

// comHandler 是由 Go 实现的 COM 回调对象（vtable: QI/AddRef/Release/Invoke）。
// 所有接线到的 Invoke 签名实参均不超过 2 个（errorCode+对象 / sender+args）。
type comHandler struct {
	vtbl   *comHandlerVtbl
	refs   int32
	invoke func(a, b uintptr) uintptr
}

type comHandlerVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	invoke         uintptr
}

var comHandlerVtblInst = &comHandlerVtbl{
	queryInterface: syscall.NewCallback(comHandlerQI),
	addRef:         syscall.NewCallback(comHandlerAddRef),
	release:        syscall.NewCallback(comHandlerRelease),
	invoke:         syscall.NewCallback(comHandlerInvoke),
}

func comHandlerQI(self, iid, ppv uintptr) uintptr {
	// WebView2 会对回调对象 QI 其期望的 handler 接口；
	// 我们只实现 Invoke 一种行为，故对所有 IID 返回自身（并按 COM 规范 AddRef）
	if ppv != 0 {
		*(*uintptr)(unsafe.Pointer(ppv)) = self //nolint:govet // COM 出参写入
		_ = comHandlerAddRef(self)
	}
	return 0 // S_OK
}

func comHandlerAddRef(self uintptr) uintptr {
	h := (*comHandler)(unsafe.Pointer(self)) //nolint:govet // COM self 指针还原
	return uintptr(atomic.AddInt32(&h.refs, 1))
}

func comHandlerRelease(self uintptr) uintptr {
	h := (*comHandler)(unsafe.Pointer(self)) //nolint:govet // COM self 指针还原
	return uintptr(atomic.AddInt32(&h.refs, -1))
}

func comHandlerInvoke(self, a, b uintptr) uintptr {
	h := (*comHandler)(unsafe.Pointer(self)) //nolint:govet // COM self 指针还原
	if h.invoke == nil {
		return 0 // S_OK
	}
	return h.invoke(a, b)
}

// liveHandlers 持有所有已移交给原生代码的回调对象引用，防止被 GC 回收。
var (
	liveHandlersMu sync.Mutex
	liveHandlers   = make(map[uintptr]*comHandler)
)

func newComHandler(invoke func(a, b uintptr) uintptr) uintptr {
	h := &comHandler{vtbl: comHandlerVtblInst, refs: 1, invoke: invoke}
	p := uintptr(unsafe.Pointer(h))
	liveHandlersMu.Lock()
	liveHandlers[p] = h
	liveHandlersMu.Unlock()
	return p
}

var noopComHandler uintptr

func init() {
	noopComHandler = newComHandler(func(a, b uintptr) uintptr { return 0 })
}

// utf16PtrToString 将原生 LPWSTR 转为 Go 字符串（不负责释放内存）。
func utf16PtrToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Pointer(p + uintptr(n)*2)) != 0 { //nolint:govet // LPWSTR 遍历
		n++
		if n > 1<<22 {
			break
		}
	}
	return string(utf16.Decode(unsafe.Slice((*uint16)(unsafe.Pointer(p)), n))) //nolint:govet // LPWSTR 转 Go 字符串
}

// utf16Buf 持有 Go 侧 UTF16 编码缓冲，确保指针在 COM 调用期间不被 GC 回收。
type utf16Buf []uint16

func newUTF16Buf(s string) utf16Buf {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return utf16Buf{0}
	}
	return utf16Buf(u)
}

func (b utf16Buf) ptr() uintptr {
	if len(b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&b[0]))
}

// ---------- WebView2 运行时发现 ----------

var wv2ChannelGUIDs = []string{
	"{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}", // stable
	"{2CD8A007-E189-409D-A2C8-9AF4EF3C72AA}", // beta
	"{0D50BFEC-CD6A-4F9A-964C-C7416E3ACB10}", // dev
	"{65C35B14-6C1D-4122-AC46-7148CC9D6497}", // canary
}

func findEmbeddedBrowserDll(root string) (string, error) {
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "x86"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("gui: unsupported arch %s", runtime.GOARCH)
	}
	p := filepath.Join(root, "EBWebView", arch, "EmbeddedBrowserWebView.dll")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// findWebView2Runtime 按优先级定位 WebView2 运行时：
// ALUKA_WEBVIEW2_DIR 环境变量（固定版本）→ 注册表渠道（evergreen 安装版）→ 可执行文件旁目录。
// 第二个返回值为 runtimeType：0 = installed（evergreen），1 = redistributable（固定版本）。
func findWebView2Runtime() (string, uintptr, error) {
	if dir := os.Getenv("ALUKA_WEBVIEW2_DIR"); dir != "" {
		p, err := findEmbeddedBrowserDll(dir)
		if err != nil {
			return "", 0, fmt.Errorf("gui: ALUKA_WEBVIEW2_DIR invalid: %w", err)
		}
		return p, 1, nil
	}

	for _, guid := range wv2ChannelGUIDs {
		key := `Software\Microsoft\EdgeUpdate\ClientState\` + guid
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			k, err := registry.OpenKey(root, key, registry.READ|registry.WOW64_32KEY)
			if err != nil {
				continue
			}
			folder, _, err := k.GetStringValue("EBWebView")
			k.Close()
			if err != nil || folder == "" {
				continue
			}
			if p, err2 := findEmbeddedBrowserDll(folder); err2 == nil {
				return p, 0, nil
			}
		}
	}

	if exe, err := os.Executable(); err == nil {
		if p, err2 := findEmbeddedBrowserDll(filepath.Dir(exe)); err2 == nil {
			return p, 1, nil
		}
	}
	return "", 0, fmt.Errorf("gui: WebView2 runtime not found (可设置 ALUKA_WEBVIEW2_DIR 指向固定版本运行时目录)")
}

// ---------- WebView2 环境管理（进程内共享单例） ----------

var wv2EnvState struct {
	mu          sync.Mutex
	initialized bool
	creating    bool
	env         uintptr
	err         error
	pending     []func(env uintptr, err error)
}

// acquireWebView2Environment 获取（或排队等待）共享 WebView2 环境。
// 环境创建必须发生在 OS UI 线程，因此通过 App.PostAction 投递。
func acquireWebView2Environment(cb func(env uintptr, err error)) {
	wv2EnvState.mu.Lock()
	if wv2EnvState.initialized {
		env, err := wv2EnvState.env, wv2EnvState.err
		wv2EnvState.mu.Unlock()
		cb(env, err)
		return
	}
	wv2EnvState.pending = append(wv2EnvState.pending, cb)
	first := !wv2EnvState.creating
	wv2EnvState.creating = true
	wv2EnvState.mu.Unlock()

	if first {
		GetApp().PostAction(initWebView2Environment)
	}
}

func initWebView2Environment() {
	// WebView2 要求调用线程已完成 COM 初始化（STA）
	hr, _, _ := procCoInitializeEx.Call(0, 2 /* COINIT_APARTMENTTHREADED */)
	_ = hr // S_OK / S_FALSE / RPC_E_CHANGED_MODE 均可继续

	dllPath, runtimeType, err := findWebView2Runtime()
	if err != nil {
		finishWebView2Environment(0, err)
		return
	}

	dll, err := syscall.LoadDLL(dllPath)
	if err != nil {
		finishWebView2Environment(0, fmt.Errorf("gui: load WebView2 dll failed: %w", err))
		return
	}
	proc, err := dll.FindProc("CreateWebViewEnvironmentWithOptionsInternal")
	if err != nil {
		finishWebView2Environment(0, fmt.Errorf("gui: WebView2 dll entrypoint missing: %w", err))
		return
	}

	userData := filepath.Join(os.TempDir(), fmt.Sprintf("aluka-webview2-%d", os.Getpid()))
	_ = os.MkdirAll(userData, 0o755)
	userPtr, _ := syscall.UTF16PtrFromString(userData)

	handler := newComHandler(func(errorCode, envPtr uintptr) uintptr {
		if int32(errorCode) < 0 || envPtr == 0 {
			finishWebView2Environment(0, fmt.Errorf("gui: WebView2 environment creation failed: 0x%08X", uint32(errorCode)))
			return 0
		}
		// 持有环境对象引用：完成后浏览器会释放自己一侧的引用
		_, _ = comCall(envPtr, 1 /* AddRef */)
		finishWebView2Environment(envPtr, nil)
		return 0
	})

	// 与官方 Loader 行为对齐：清空可能覆盖运行时选择的环境变量
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "")
	os.Setenv("WEBVIEW2_RELEASE_CHANNEL_PREFERENCE", "0")

	callHR, _, _ := proc.Call(1, runtimeType, uintptr(unsafe.Pointer(userPtr)), 0, handler)
	if int32(callHR) < 0 {
		finishWebView2Environment(0, fmt.Errorf("gui: CreateWebViewEnvironmentWithOptionsInternal failed: 0x%08X", uint32(callHR)))
	}
}

func finishWebView2Environment(env uintptr, err error) {
	wv2EnvState.mu.Lock()
	wv2EnvState.initialized = true
	wv2EnvState.creating = false
	wv2EnvState.env, wv2EnvState.err = env, err
	cbs := wv2EnvState.pending
	wv2EnvState.pending = nil
	wv2EnvState.mu.Unlock()

	// finish 由 UI 线程上的完成回调触发，回调可直接创建 Controller
	for _, cb := range cbs {
		cb(env, err)
	}
}

// ---------- 虚拟协议映射 ----------

// alukaAppHTTPHost aluka://app 虚拟协议在 WebView2 内部映射到的 HTTP 主机。
// 早期 SDK 对自定义 scheme 的 WebResourceRequested 支持不完整，映射到 http 域可全版本兼容。
const alukaAppHTTPHost = "http://aluka.app"

func translateAlukaURL(url string) string {
	if strings.HasPrefix(url, "aluka://app") {
		return alukaAppHTTPHost + strings.TrimPrefix(url, "aluka://app")
	}
	return url
}

// ---------- WebView2 窗口集成（UI 线程方法） ----------

// WebView2 vtable 序号（IUnknown 占 0/1/2，下述为自有方法序号）
const (
	wv2EnvCreateController      = 3
	wv2EnvCreateResourceResp    = 4
	wv2CtlPutIsVisible          = 4
	wv2CtlPutBounds             = 6
	wv2CtlClose                 = 24
	wv2CtlGetCoreWebView2       = 25
	wv2Ctl2PutDefaultBg         = 27 // ICoreWebView2Controller2::put_DefaultBackgroundColor
	wv2GetSettings              = 3
	wv2Navigate                 = 5
	wv2NavigateToString         = 6
	wv2SettingsPutScriptEnabled = 4
	wv2SettingsPutWebMessage    = 6
	wv2SettingsPutDevTools      = 12
	wv2AddScriptToExecute       = 27
	wv2ExecuteScript            = 29
	wv2CapturePreview           = 30 // 官方 IDL：ExecuteScript(23)→CapturePreview(24)→Reload(25)；本栈 ExecuteScript=29、Reload=31
	wv2Reload                   = 31
	wv2AddWebMessageReceived    = 34
	wv2AddProcessFailed         = 25
	wv2OpenDevTools             = 51
	wv2AddWebResourceRequested  = 55
	wv2AddWebResourceFilter     = 57
	wv2ArgsGetRequest           = 3
	wv2ArgsPutResponse          = 5
	wv2ReqGetUri                = 3
	wv2RespPutContent           = 4
	wv2RespPutStatusCode        = 7
	wv2MsgArgsTryGetAsString    = 5
	wv2PFArgsGetKind            = 3
)

// initWebView2 在 UI 线程为窗口挂载 WebView2（异步）。
func (w *windowsWindow) initWebView2() {
	acquireWebView2Environment(func(env uintptr, err error) {
		if err != nil {
			w.setWebViewError(err)
			return
		}
		// 环境创建为异步：期间窗口可能已被关闭（如测试快速 Close），
		// 此时再对已销毁 HWND 创建 Controller 会让 WebView2 挂起 UI 线程。
		if w.parent != nil && w.parent.IsClosed() {
			return
		}
		handler := newComHandler(func(errorCode, controllerPtr uintptr) uintptr {
			if int32(errorCode) < 0 || controllerPtr == 0 {
				w.setWebViewError(fmt.Errorf("gui: WebView2 controller creation failed: 0x%08X", uint32(errorCode)))
				return 0
			}
			w.onWebView2Controller(controllerPtr)
			return 0
		})
		// ICoreWebView2Environment::CreateCoreWebView2Controller(hwnd, handler)
		if _, err := comCall(env, wv2EnvCreateController, uintptr(w.hwnd), handler); err != nil {
			w.setWebViewError(err)
		}
	})
}

// onWebView2Controller 在 Controller 创建完成（UI 线程）后完成全部接线。
func (w *windowsWindow) onWebView2Controller(controller uintptr) {
	// 创建与回调之间窗口可能已被关闭：立即释放刚创建的渲染层，避免对
	// 已销毁 HWND 做后续 COM 调用（会让 WebView2 阻塞消息循环）。
	if w.parent != nil && w.parent.IsClosed() {
		_, _ = comCall(controller, wv2CtlClose)
		return
	}
	// 持有 controller/webview 引用：Invoke 返回后浏览器会释放其侧的引用，
	// 不 AddRef 的话对象会被 PartitionAlloc 回收，后续调用即悬垂指针崩溃
	_, _ = comCall(controller, 1 /* AddRef */)

	var webview uintptr
	if _, err := comCall(controller, wv2CtlGetCoreWebView2, uintptr(unsafe.Pointer(&webview))); err != nil {
		w.setWebViewError(err)
		return
	}
	_, _ = comCall(webview, 1 /* AddRef */)

	// Settings：启用脚本 / WebMessage / （可选）DevTools
	var settings uintptr
	if _, err := comCall(webview, wv2GetSettings, uintptr(unsafe.Pointer(&settings))); err == nil && settings != 0 {
		_, _ = comCall(settings, wv2SettingsPutScriptEnabled, 1)
		_, _ = comCall(settings, wv2SettingsPutWebMessage, 1)
		devTools := uintptr(0)
		if w.opts.DevTools {
			devTools = 1
		}
		_, _ = comCall(settings, wv2SettingsPutDevTools, devTools)
	}

	// 透明窗口：WebView 背景置为全透明，让 Mica/Acrylic/窗口透明效果透出
	if w.opts.Transparent || w.opts.BackgroundEffect != "" {
		_, _ = comCall(controller, wv2Ctl2PutDefaultBg, 0 /* COREWEBVIEW2_COLOR{0,0,0,0} */)
	}

	env := wv2EnvState.env

	// 拦截 aluka://app/* → http://aluka.app/* 的资源请求（内存虚拟协议，零 TCP 端口）
	// 注意：PCWSTR 实参必须以 utf16Buf 持引用，防止 GC 在调用前回收缓冲（悬垂指针）
	filterURI := newUTF16Buf(alukaAppHTTPHost + "/*")
	if filterURI.ptr() != 0 {
		_, _ = comCall(webview, wv2AddWebResourceFilter, filterURI.ptr(), 0 /* COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL */)
		runtime.KeepAlive(filterURI)
	}
	resHandler := newComHandler(func(sender, eventArgs uintptr) uintptr {
		w.handleWebResourceRequested(env, eventArgs)
		return 0
	})
	var token uintptr
	_, _ = comCall(webview, wv2AddWebResourceRequested, resHandler, uintptr(unsafe.Pointer(&token)))

	// 渲染进程故障观测：白屏的最常见原因是 renderer 崩溃/失去响应，
	// 记录日志并在 renderer 类故障时自动 Reload 自愈
	pfHandler := newComHandler(func(sender, eventArgs uintptr) uintptr {
		kind := uintptr(0xFFFFFFFF)
		if _, err := comCall(eventArgs, wv2PFArgsGetKind, uintptr(unsafe.Pointer(&kind))); err != nil {
			kind = 0xFFFFFFFF
		}
		fmt.Fprintf(os.Stderr, "[aluka:gui] %s WebView2 process failed: kind=%d\n", time.Now().Format("15:04:05.000"), int32(kind))
		// 仅 renderer 故障自愈：Reload 重载当前页面
		if kind == 1 || kind == 2 {
			GetApp().PostAction(func() {
				w.wvMu.RLock()
				wv := w.wvWebview
				w.wvMu.RUnlock()
				if wv != 0 {
					fmt.Fprintln(os.Stderr, "[aluka:gui] renderer 故障，自动 Reload 自愈")
					_, _ = comCall(wv, wv2Reload)
				}
			})
		}
		return 0
	})
	var pfToken uintptr
	_, _ = comCall(webview, wv2AddProcessFailed, pfHandler, uintptr(unsafe.Pointer(&pfToken)))

	// 前端 → 主进程 WebMessage 通道（window.chrome.webview.postMessage）
	msgHandler := newComHandler(func(sender, eventArgs uintptr) uintptr {
		var msgPtr uintptr
		if _, err := comCall(eventArgs, wv2MsgArgsTryGetAsString, uintptr(unsafe.Pointer(&msgPtr))); err != nil || msgPtr == 0 {
			return 0
		}
		msg := utf16PtrToString(msgPtr)
		procCoTaskMemFree.Call(msgPtr)
		if w.parent != nil {
			w.parent.HandleWebMessage(msg)
		}
		return 0
	})
	var token2 uintptr
	_, _ = comCall(webview, wv2AddWebMessageReceived, msgHandler, uintptr(unsafe.Pointer(&token2)))

	// 注入 window.aluka 前端桥接客户端（无边框时启用拖拽/边缘缩放热区）
	if w.parent != nil {
		frameless := w.opts.Frame != nil && !*w.opts.Frame
		bridge := newUTF16Buf(GenerateBridgeScript(w.parent.ID(), frameless))
		if bridge.ptr() != 0 {
			_, _ = comCall(webview, wv2AddScriptToExecute, bridge.ptr(), noopComHandler)
			runtime.KeepAlive(bridge)
		}
		if w.opts.PreloadScript != "" {
			preload := newUTF16Buf(w.opts.PreloadScript)
			if preload.ptr() != 0 {
				_, _ = comCall(webview, wv2AddScriptToExecute, preload.ptr(), noopComHandler)
				runtime.KeepAlive(preload)
			}
		}
	}

	w.wvMu.Lock()
	w.wvController = controller
	w.wvWebview = webview
	w.wvReady = true
	w.wvMu.Unlock()

	// 初始尺寸同步 + 显示渲染层
	w.wvUpdateBounds()
	_, _ = comCall(controller, wv2CtlPutIsVisible, 1)

	// 应用排队中的导航目标与脚本
	w.wvMu.Lock()
	url, html := w.wvPendingURL, w.wvPendingHTML
	scripts := w.wvPendingScripts
	w.wvPendingURL, w.wvPendingHTML, w.wvPendingScripts = "", "", nil
	w.wvMu.Unlock()

	if html != "" {
		buf := newUTF16Buf(html)
		_, _ = comCall(webview, wv2NavigateToString, buf.ptr())
		runtime.KeepAlive(buf)
	} else if url != "" {
		buf := newUTF16Buf(url)
		_, _ = comCall(webview, wv2Navigate, buf.ptr())
		runtime.KeepAlive(buf)
	}
	for _, js := range scripts {
		buf := newUTF16Buf(js)
		_, _ = comCall(webview, wv2ExecuteScript, buf.ptr(), noopComHandler)
		runtime.KeepAlive(buf)
	}

	if w.parent != nil {
		w.parent.Emit("webview-ready", nil)
	}
}

// handleWebResourceRequested 以 aluka://app 虚拟资产协议响应 WebView 的资源请求。
func (w *windowsWindow) handleWebResourceRequested(env, eventArgs uintptr) {
	var req uintptr
	if _, err := comCall(eventArgs, wv2ArgsGetRequest, uintptr(unsafe.Pointer(&req))); err != nil || req == 0 {
		return
	}
	var uriPtr uintptr
	if _, err := comCall(req, wv2ReqGetUri, uintptr(unsafe.Pointer(&uriPtr))); err != nil || uriPtr == 0 {
		return
	}
	uri := utf16PtrToString(uriPtr)
	procCoTaskMemFree.Call(uriPtr)

	if !strings.HasPrefix(uri, alukaAppHTTPHost+"/") {
		return
	}
	assetPath := strings.TrimPrefix(uri, alukaAppHTTPHost+"/")
	if i := strings.IndexAny(assetPath, "?#"); i >= 0 {
		assetPath = assetPath[:i]
	}

	statusCode := 200
	reason := "OK"
	var content []byte
	rc, mimeType, status, err := ResolveAssetURL("aluka://app/" + assetPath)
	if err != nil {
		statusCode, reason = 404, "Not Found"
		mimeType = "text/plain"
		content = []byte("404 Not Found: " + assetPath)
	} else {
		statusCode = status
		content, _ = io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
	}

	headers := "Content-Type: " + mimeType + "\r\n"

	var stream uintptr
	if len(content) > 0 {
		stream, _, _ = procSHCreateMemStream.Call(uintptr(unsafe.Pointer(&content[0])), uintptr(len(content)))
		runtime.KeepAlive(content)
	}

	// reasonPhrase/headers 的 PCWSTR 必须在 COM 调用期间持引用，
	// 否则 GC 可能回收缓冲使浏览器读到悬垂字符串（间歇性渲染崩溃的隐患）
	reasonBuf := newUTF16Buf(reason)
	headersBuf := newUTF16Buf(headers)
	var resp uintptr
	if _, err := comCall(env, wv2EnvCreateResourceResp, stream, uintptr(int32(statusCode)), reasonBuf.ptr(), headersBuf.ptr(), uintptr(unsafe.Pointer(&resp))); err != nil {
		runtime.KeepAlive(reasonBuf)
		runtime.KeepAlive(headersBuf)
		return
	}
	runtime.KeepAlive(reasonBuf)
	runtime.KeepAlive(headersBuf)
	_, _ = comCall(eventArgs, wv2ArgsPutResponse, resp)
}

// wvUpdateBounds 将宿主窗口客户区尺寸同步到 WebView 渲染层（UI 线程 / WM_SIZE）。
func (w *windowsWindow) wvUpdateBounds() {
	w.wvMu.RLock()
	controller := w.wvController
	w.wvMu.RUnlock()
	if controller == 0 {
		return
	}
	var r rect
	procGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r)))
	bounds := rect{0, 0, r.Right, r.Bottom}
	_, _ = comCall(controller, wv2CtlPutBounds, uintptr(unsafe.Pointer(&bounds)))
}

func (w *windowsWindow) setWebViewError(err error) {
	w.wvMu.Lock()
	w.wvErr = err
	w.wvMu.Unlock()
}

// wvApplyTarget 应用最近一次导航目标（UI 线程）。
func (w *windowsWindow) wvApplyTarget() {
	w.wvMu.RLock()
	webview, url, html := w.wvWebview, w.wvPendingURL, w.wvPendingHTML
	w.wvMu.RUnlock()
	if webview == 0 {
		return
	}
	if html != "" {
		buf := newUTF16Buf(html)
		_, _ = comCall(webview, wv2NavigateToString, buf.ptr())
		runtime.KeepAlive(buf)
	} else if url != "" {
		buf := newUTF16Buf(url)
		_, _ = comCall(webview, wv2Navigate, buf.ptr())
		runtime.KeepAlive(buf)
	}
}
