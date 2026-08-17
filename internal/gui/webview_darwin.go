//go:build darwin

package gui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	nsWindowStyleTitled         = 1
	nsWindowStyleClosable       = 2
	nsWindowStyleMiniaturizable = 4
	nsWindowStyleResizable      = 8
	nsBackingStoreBuffered      = 2
	nsAnyEventMask              = ^uintptr(0)
	nsFloatingWindowLevel       = 3
)

type darwinApp struct {
	app     *App
	nsApp   uintptr
	tasks   chan func()
	running bool
	uiOn    bool
	mu      sync.Mutex
}

func createNativeApp(app *App) NativeApp {
	return &darwinApp{
		app:   app,
		tasks: make(chan func(), 128),
	}
}

func (a *darwinApp) ensureNSApp() error {
	if err := ensureObjC(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.nsApp != 0 {
		return nil
	}
	a.nsApp = objcCall0(objcClass("NSApplication"), "sharedApplication")
	if a.nsApp == 0 {
		return fmt.Errorf("gui: NSApplication.sharedApplication returned nil")
	}
	objcCall1(a.nsApp, "setActivationPolicy:", 0)
	return nil
}

func (a *darwinApp) Run() error {
	runtime.LockOSThread()
	if err := a.ensureNSApp(); err != nil {
		return err
	}
	a.mu.Lock()
	a.running = true
	a.uiOn = true
	a.mu.Unlock()

	defaultMode := nsString("kCFRunLoopDefaultMode")
	distantPast := objcCall0(objcClass("NSDate"), "distantPast")
	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()
		if !running {
			break
		}
		drain := true
		for drain {
			select {
			case fn := <-a.tasks:
				if fn != nil {
					withAutorelease(fn)
				}
			default:
				drain = false
			}
		}
		withAutorelease(func() {
			ev := objcCall(a.nsApp, sel("nextEventMatchingMask:untilDate:inMode:dequeue:"), nsAnyEventMask, distantPast, defaultMode, 1)
			if ev != 0 {
				objcCall1(a.nsApp, "sendEvent:", ev)
			}
			a.pollBridges()
		})
		if len(a.tasks) == 0 {
			time.Sleep(8 * time.Millisecond)
		}
	}
	a.mu.Lock()
	a.uiOn = false
	a.mu.Unlock()
	return nil
}

func (a *darwinApp) pollBridges() {
	if a.app == nil {
		return
	}
	for _, w := range a.app.Windows() {
		if dw, ok := w.native.(*darwinWindow); ok {
			dw.pollBridge()
		}
	}
}

func (a *darwinApp) Quit() {
	a.PostAction(func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	})
}

func (a *darwinApp) PostAction(fn func()) {
	if fn == nil {
		return
	}
	a.mu.Lock()
	running := a.running
	a.mu.Unlock()
	if !running {
		// 消息循环未跑：只入队，禁止 go fn() 在非主线程碰 AppKit。
		select {
		case a.tasks <- fn:
		default:
		}
		return
	}
	a.tasks <- fn
}

func (a *darwinApp) ShowDialog(opts DialogOptions) (int, []string, error) {
	return 0, nil, fmt.Errorf("gui: dialog is not implemented on darwin yet")
}

func (a *darwinApp) CreateTray(opts TrayOptions) (NativeTray, error) {
	return newDarwinTray(opts)
}

type darwinWindow struct {
	opts       WindowOptions
	parent     *Window
	nsWin      uintptr
	webView    uintptr
	styleMask  uintptr
	x, y       int
	w, h       int
	minW, minH int
	maxW, maxH int
	fullscreen bool
	maximized  bool
	bridgeLast string
}

func createNativeWindow(opts WindowOptions, parent *Window) (NativeWindow, error) {
	if err := ensureObjC(); err != nil {
		return nil, err
	}
	app, _ := GetApp().native.(*darwinApp)
	if app != nil {
		if err := app.ensureNSApp(); err != nil {
			return nil, err
		}
	}
	var win *darwinWindow
	var err error
	create := func() {
		win, err = newDarwinWindow(opts, parent)
	}
	if app != nil {
		app.mu.Lock()
		ui := app.uiOn
		app.mu.Unlock()
		if ui {
			done := make(chan struct{})
			app.PostAction(func() {
				create()
				close(done)
			})
			<-done
			return win, err
		}
	}
	create()
	return win, err
}

func newDarwinWindow(opts WindowOptions, parent *Window) (*darwinWindow, error) {
	w := &darwinWindow{
		opts:   opts,
		parent: parent,
		w:      opts.Width,
		h:      opts.Height,
		x:      opts.X,
		y:      opts.Y,
		minW:   opts.MinWidth,
		minH:   opts.MinHeight,
		maxW:   opts.MaxWidth,
		maxH:   opts.MaxHeight,
	}
	style := uintptr(nsWindowStyleTitled | nsWindowStyleClosable | nsWindowStyleMiniaturizable)
	if opts.Resizable == nil || *opts.Resizable {
		style |= nsWindowStyleResizable
	}
	if opts.Frame != nil && !*opts.Frame {
		style = 0 // borderless
	}
	w.styleMask = style
	nsWin := objcMsgSendRect(objcAlloc("NSWindow"), sel("initWithContentRect:styleMask:backing:defer:"),
		float64(opts.X), float64(opts.Y), float64(opts.Width), float64(opts.Height),
		style, nsBackingStoreBuffered, 0)
	if nsWin == 0 {
		return nil, fmt.Errorf("gui: NSWindow init failed")
	}
	objcCall1(nsWin, "setTitle:", nsString(opts.Title))
	objcCall1(nsWin, "setReleasedWhenClosed:", 0)

	config := objcCall0(objcAlloc("WKWebViewConfiguration"), "init")
	if config == 0 {
		return nil, fmt.Errorf("gui: WKWebViewConfiguration init failed")
	}
	prefs := objcCall0(config, "preferences")
	if prefs != 0 {
		objcCall(prefs, sel("setValue:forKey:"), nsNumberYES(), nsString("developerExtrasEnabled"), 0, 0)
	}
	uc := objcCall0(config, "userContentController")
	frameless := opts.Frame != nil && !*opts.Frame
	bridge := GenerateBridgeScript(0, frameless)
	if parent != nil {
		bridge = GenerateBridgeScript(parent.ID(), frameless)
	}
	src := bridgePolyfill() + bridge
	if opts.PreloadScript != "" {
		src += "\n" + opts.PreloadScript
	}
	userScript := objcCall(objcAlloc("WKUserScript"), sel("initWithSource:injectionTime:forMainFrameOnly:"),
		nsString(src), 0, 1, 0)
	if uc != 0 && userScript != 0 {
		objcCall1(uc, "addUserScript:", userScript)
	}

	wv := objcMsgSendRect(objcAlloc("WKWebView"), sel("initWithFrame:configuration:"),
		0, 0, float64(opts.Width), float64(opts.Height), config, 0, 0)
	if wv == 0 {
		return nil, fmt.Errorf("gui: WKWebView init failed")
	}
	objcCall1(nsWin, "setContentView:", wv)
	w.nsWin = nsWin
	w.webView = wv

	if opts.AlwaysOnTop {
		w.SetAlwaysOnTop(true)
	}
	if opts.MinWidth > 0 || opts.MinHeight > 0 {
		w.SetMinSize(opts.MinWidth, opts.MinHeight)
	}
	if opts.MaxWidth > 0 || opts.MaxHeight > 0 {
		w.SetMaxSize(opts.MaxWidth, opts.MaxHeight)
	}
	if opts.Center {
		w.Center()
	}
	if !opts.Hidden {
		w.Show()
	}
	if opts.HTML != "" {
		w.SetHTML(opts.HTML)
	} else if opts.URL != "" {
		w.Navigate(opts.URL)
	}
	return w, nil
}

func bridgePolyfill() string {
	// 无 C→Go IMP：把 webkit.messageHandlers.aluka 接到 location.hash，
	// 由 NSApplication 循环同步读取后交给 HandleWebMessage。
	// title 仅作 hash 未反映到 WKURL 时的后备，写入前保存原标题。
	return `(function(){
  window.__alukaPending = window.__alukaPending || [];
  window.webkit = window.webkit || {};
  window.webkit.messageHandlers = window.webkit.messageHandlers || {};
  if (!window.webkit.messageHandlers.aluka) {
    window.webkit.messageHandlers.aluka = {
      postMessage: function(m) {
        var s = typeof m === 'string' ? m : JSON.stringify(m);
        window.__alukaPending.push(s);
        var packed = encodeURIComponent(JSON.stringify(window.__alukaPending));
        try { location.hash = 'aluka=' + packed; } catch (e) {}
        try {
          if (typeof window.__alukaTitle !== 'string') window.__alukaTitle = document.title;
          document.title = '\x01aluka\x01' + packed;
        } catch (e2) {}
      }
    };
  }
})();`
}

func (w *darwinWindow) pollBridge() {
	if w.webView == 0 || w.parent == nil {
		return
	}
	abs := ""
	if u := objcCall0(w.webView, "URL"); u != 0 {
		abs = nsToGo(objcCall0(u, "absoluteString"))
	}
	title := nsToGo(objcCall0(w.webView, "title"))
	raw := extractBridgePayload(abs, title)
	dispatch, next := consumeBridgePayload(raw, w.bridgeLast)
	w.bridgeLast = next
	if !dispatch {
		return
	}
	var msgs []string
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		w.parent.HandleWebMessage(raw)
	} else {
		for _, m := range msgs {
			w.parent.HandleWebMessage(m)
		}
	}
	objcCall(w.webView, sel("evaluateJavaScript:completionHandler:"),
		nsString(`window.__alukaPending=[];try{location.hash='';}catch(e){}
try{if(String(document.title).indexOf('\x01aluka\x01')===0){document.title=window.__alukaTitle||'';}}catch(e2){}`), 0, 0, 0)
}

// consumeBridgePayload 在 evaluateJavaScript 异步清 hash/title 之前去重，
// 避免同一批消息被 8ms 循环重复派发。
func consumeBridgePayload(raw, last string) (dispatch bool, next string) {
	if raw == "" {
		return false, ""
	}
	if raw == last {
		return false, last
	}
	return true, raw
}

func extractBridgePayload(absURL, title string) string {
	if i := strings.Index(absURL, "aluka="); i >= 0 {
		raw := absURL[i+len("aluka="):]
		if j := strings.IndexAny(raw, "&#"); j >= 0 {
			raw = raw[:j]
		}
		if s, err := url.QueryUnescape(raw); err == nil && s != "" {
			return s
		}
	}
	const p = "\x01aluka\x01"
	if strings.HasPrefix(title, p) {
		if s, err := url.QueryUnescape(title[len(p):]); err == nil && s != "" {
			return s
		}
	}
	return ""
}

func (w *darwinWindow) Show() {
	if w.nsWin != 0 {
		objcCall1(w.nsWin, "makeKeyAndOrderFront:", 0)
		if app, ok := GetApp().native.(*darwinApp); ok && app.nsApp != 0 {
			objcCall1(app.nsApp, "activateIgnoringOtherApps:", 1)
		}
	}
}

func (w *darwinWindow) Hide() {
	if w.nsWin != 0 {
		objcCall1(w.nsWin, "orderOut:", 0)
	}
}

func (w *darwinWindow) Close() {
	if w.nsWin != 0 {
		objcCall0(w.nsWin, "close")
	}
}

func (w *darwinWindow) Destroy() { w.Close() }

func (w *darwinWindow) Center() {
	if w.nsWin != 0 {
		objcCall0(w.nsWin, "center")
	}
}

func (w *darwinWindow) SetTitle(title string) {
	w.opts.Title = title
	if w.nsWin != 0 {
		objcCall1(w.nsWin, "setTitle:", nsString(title))
	}
}

func (w *darwinWindow) SetSize(width, height int) {
	w.w, w.h = width, height
	if w.nsWin != 0 {
		objcMsgSendRect(w.nsWin, sel("setContentSize:"), float64(width), float64(height), 0, 0, 0, 0, 0)
	}
}

func (w *darwinWindow) GetSize() (int, int) { return w.w, w.h }

func (w *darwinWindow) SetPosition(x, y int) {
	w.x, w.y = x, y
	if w.nsWin != 0 {
		objcMsgSendRect(w.nsWin, sel("setFrameTopLeftPoint:"), float64(x), float64(y), 0, 0, 0, 0, 0)
	}
}

func (w *darwinWindow) GetPosition() (int, int) { return w.x, w.y }

func (w *darwinWindow) SetMinSize(width, height int) {
	w.minW, w.minH = width, height
	if w.nsWin != 0 {
		objcMsgSendRect(w.nsWin, sel("setContentMinSize:"), float64(width), float64(height), 0, 0, 0, 0, 0)
	}
}

func (w *darwinWindow) SetMaxSize(width, height int) {
	w.maxW, w.maxH = width, height
	if w.nsWin != 0 {
		objcMsgSendRect(w.nsWin, sel("setContentMaxSize:"), float64(width), float64(height), 0, 0, 0, 0, 0)
	}
}

func (w *darwinWindow) SetResizable(resizable bool) {
	r := resizable
	w.opts.Resizable = &r
	if w.nsWin == 0 {
		return
	}
	if resizable {
		w.styleMask |= nsWindowStyleResizable
	} else {
		w.styleMask &^= nsWindowStyleResizable
	}
	objcCall1(w.nsWin, "setStyleMask:", w.styleMask)
}

func (w *darwinWindow) SetAlwaysOnTop(alwaysOnTop bool) {
	w.opts.AlwaysOnTop = alwaysOnTop
	if w.nsWin == 0 {
		return
	}
	level := uintptr(0) // NSNormalWindowLevel
	if alwaysOnTop {
		level = nsFloatingWindowLevel
	}
	objcCall1(w.nsWin, "setLevel:", level)
}

func (w *darwinWindow) SetFullscreen(fullscreen bool) {
	if w.nsWin != 0 && w.fullscreen != fullscreen {
		objcCall1(w.nsWin, "toggleFullScreen:", 0)
	}
	w.fullscreen = fullscreen
}

func (w *darwinWindow) IsFullscreen() bool { return w.fullscreen }

func (w *darwinWindow) Minimize() {
	if w.nsWin != 0 {
		objcCall1(w.nsWin, "miniaturize:", 0)
	}
}

func (w *darwinWindow) Maximize() {
	w.maximized = true
	if w.nsWin != 0 {
		objcCall1(w.nsWin, "zoom:", 0)
	}
}

func (w *darwinWindow) Unmaximize() {
	w.maximized = false
	if w.nsWin != 0 {
		objcCall1(w.nsWin, "zoom:", 0)
	}
}

func (w *darwinWindow) IsMaximized() bool { return w.maximized }

func (w *darwinWindow) Navigate(url string) {
	if strings.HasPrefix(url, "aluka://") {
		w.loadAlukaURL(url)
		return
	}
	if w.webView == 0 {
		return
	}
	req := objcCall1(objcClass("NSURLRequest"), "requestWithURL:", nsURL(url))
	if req != 0 {
		objcCall1(w.webView, "loadRequest:", req)
	}
}

func (w *darwinWindow) loadAlukaURL(raw string) {
	// 无 CGO 无法注册 WKURLSchemeHandler：顶层 HTML 经 ResolveAssetURL 读取后
	// inline 本地 script/link/img（及 CSS url()）。fetch / 动态 import / 二次
	// 导航仍无法走 aluka://，与 Windows WebResourceRequested 不对等。
	rc, mimeType, status, err := ResolveAssetURL(raw)
	if err != nil || status != 200 || rc == nil {
		w.SetHTML("<html><body>failed to load " + html.EscapeString(raw) + "</body></html>")
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return
	}
	if strings.Contains(mimeType, "html") || strings.HasSuffix(strings.ToLower(raw), ".html") || strings.HasSuffix(raw, "/") {
		w.SetHTML(inlineAlukaAssets(raw, string(data)))
		return
	}
	escaped := html.EscapeString(string(data))
	w.SetHTML("<html><body><pre>" + escaped + "</pre></body></html>")
}

func (w *darwinWindow) SetHTML(html string) {
	if w.webView == 0 {
		return
	}
	objcCall(w.webView, sel("loadHTMLString:baseURL:"), nsString(html), 0, 0, 0)
}

func (w *darwinWindow) ExecuteScript(js string) {
	if w.webView == 0 {
		return
	}
	objcCall(w.webView, sel("evaluateJavaScript:completionHandler:"), nsString(js), 0, 0, 0)
}

func (w *darwinWindow) OpenDevTools() {
	if w.webView == 0 {
		return
	}
	inspector := sel("_showWebInspector:")
	if objcCall1(w.webView, "respondsToSelector:", inspector) == 0 {
		return
	}
	objcCall(w.webView, inspector, 0, 0, 0, 0)
}

func (w *darwinWindow) StartDragMove() {
	if w.nsWin == 0 {
		return
	}
	app, _ := GetApp().native.(*darwinApp)
	ev := uintptr(0)
	if app != nil && app.nsApp != 0 {
		ev = objcCall0(app.nsApp, "currentEvent")
	}
	if ev != 0 {
		objcCall1(w.nsWin, "performWindowDragWithEvent:", ev)
	}
}

var (
	alukaScriptSrcRe = regexp.MustCompile(`(?i)<script\s+([^>]*?)src=["']([^"']+)["']([^>]*)>(?:\s*</script>)?`)
	alukaLinkHrefRe  = regexp.MustCompile(`(?i)<link\s+([^>]*?)href=["']([^"']+)["']([^>]*)/?>`)
	alukaImgSrcRe    = regexp.MustCompile(`(?i)<img\s+([^>]*?)src=["']([^"']+)["']([^>]*)/?>`)
	alukaCSSURLRe    = regexp.MustCompile(`url\(\s*(['"]?)([^'")]+)\1\s*\)`)
)

func inlineAlukaAssets(pageURL, htmlStr string) string {
	rewrite := func(tag, ref string, kind string) string {
		abs := resolveAlukaRef(pageURL, ref)
		if abs == "" {
			return tag
		}
		rc, mimeType, status, err := ResolveAssetURL(abs)
		if err != nil || status != 200 || rc == nil {
			return tag
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return tag
		}
		switch kind {
		case "script":
			return "<script>" + string(data) + "</script>"
		case "style":
			return "<style>" + inlineCSSURLs(abs, string(data)) + "</style>"
		case "img":
			if mimeType == "" {
				mimeType = mime.TypeByExtension(path.Ext(ref))
			}
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			return strings.Replace(tag, ref, "data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString(data), 1)
		}
		return tag
	}
	out := alukaScriptSrcRe.ReplaceAllStringFunc(htmlStr, func(tag string) string {
		m := alukaScriptSrcRe.FindStringSubmatch(tag)
		if len(m) < 3 {
			return tag
		}
		return rewrite(tag, m[2], "script")
	})
	out = alukaLinkHrefRe.ReplaceAllStringFunc(out, func(tag string) string {
		m := alukaLinkHrefRe.FindStringSubmatch(tag)
		if len(m) < 4 {
			return tag
		}
		attrs := strings.ToLower(m[1] + " " + m[3])
		if !strings.Contains(attrs, "stylesheet") {
			return tag
		}
		return rewrite(tag, m[2], "style")
	})
	out = alukaImgSrcRe.ReplaceAllStringFunc(out, func(tag string) string {
		m := alukaImgSrcRe.FindStringSubmatch(tag)
		if len(m) < 3 {
			return tag
		}
		return rewrite(tag, m[2], "img")
	})
	return out
}

func inlineCSSURLs(cssURL, css string) string {
	return alukaCSSURLRe.ReplaceAllStringFunc(css, func(m string) string {
		sub := alukaCSSURLRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		abs := resolveAlukaRef(cssURL, strings.TrimSpace(sub[2]))
		if abs == "" {
			return m
		}
		rc, mimeType, status, err := ResolveAssetURL(abs)
		if err != nil || status != 200 || rc == nil {
			return m
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return m
		}
		if mimeType == "" {
			mimeType = mime.TypeByExtension(path.Ext(abs))
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		return "url(data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data) + ")"
	})
}

func resolveAlukaRef(pageURL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "//") {
		return ""
	}
	if strings.HasPrefix(ref, "aluka://") {
		return ref
	}
	base := pageURL
	if !strings.HasPrefix(base, "aluka://") {
		base = "aluka://app/"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "aluka://app/" + strings.TrimPrefix(ref, "./")
	}
	rel, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return u.ResolveReference(rel).String()
}

type darwinTray struct {
	item uintptr
}

func menuHasClick(items []MenuItem) bool {
	for _, it := range items {
		if it.Click != nil {
			return true
		}
		if menuHasClick(it.Submenu) {
			return true
		}
	}
	return false
}

func newDarwinTray(opts TrayOptions) (*darwinTray, error) {
	if menuHasClick(opts.Menu) {
		return nil, fmt.Errorf("gui: tray menu Click callbacks are not supported on darwin yet")
	}
	if err := ensureObjC(); err != nil {
		return nil, err
	}
	bar := objcCall0(objcClass("NSStatusBar"), "systemStatusBar")
	if bar == 0 {
		return nil, fmt.Errorf("gui: NSStatusBar unavailable")
	}
	item := objcMsgSendF1(bar, sel("statusItemWithLength:"), -1)
	if item == 0 {
		return nil, fmt.Errorf("gui: statusItemWithLength failed")
	}
	t := &darwinTray{item: item}
	if opts.Icon != "" {
		t.SetIcon(opts.Icon)
	}
	if opts.Tooltip != "" {
		t.SetTooltip(opts.Tooltip)
	}
	if len(opts.Menu) > 0 {
		t.SetMenu(opts.Menu)
	}
	return t, nil
}

func (t *darwinTray) SetIcon(icon string) {
	if t.item == 0 || icon == "" {
		return
	}
	btn := objcCall0(t.item, "button")
	if btn == 0 {
		return
	}
	img := objcCall1(objcAlloc("NSImage"), "initWithContentsOfFile:", nsString(icon))
	if img != 0 {
		objcCall1(btn, "setImage:", img)
	}
}

func (t *darwinTray) SetTooltip(tooltip string) {
	if t.item == 0 {
		return
	}
	btn := objcCall0(t.item, "button")
	if btn != 0 {
		objcCall1(btn, "setToolTip:", nsString(tooltip))
		if objcCall0(btn, "image") == 0 {
			objcCall1(btn, "setTitle:", nsString(tooltip))
		}
	}
}

func (t *darwinTray) SetMenu(items []MenuItem) {
	if t.item == 0 {
		return
	}
	if menuHasClick(items) {
		// NativeTray.SetMenu 无 error：有 Click 时保持原菜单，避免假装已接线。
		return
	}
	menu := objcCall0(objcAlloc("NSMenu"), "init")
	if menu == 0 {
		return
	}
	for _, it := range items {
		if it.Type == "separator" {
			sep := objcCall0(objcClass("NSMenuItem"), "separatorItem")
			objcCall1(menu, "addItem:", sep)
			continue
		}
		objcCall(menu, sel("addItemWithTitle:action:keyEquivalent:"), nsString(it.Label), 0, nsString(""), 0)
	}
	objcCall1(t.item, "setMenu:", menu)
}

func (t *darwinTray) Destroy() {
	if t.item == 0 {
		return
	}
	bar := objcCall0(objcClass("NSStatusBar"), "systemStatusBar")
	if bar != 0 {
		objcCall1(bar, "removeStatusItem:", t.item)
	}
	t.item = 0
}
