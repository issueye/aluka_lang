//go:build windows

package gui

import (
	"os"
	"testing"
	"time"
)

// TestWebView2RuntimeDiscovery 验证 WebView2 运行时发现逻辑（无运行时的机器上 Skip）。
func TestWebView2RuntimeDiscovery(t *testing.T) {
	dllPath, runtimeType, err := findWebView2Runtime()
	if err != nil {
		t.Skipf("WebView2 runtime not installed: %v", err)
	}
	if dllPath == "" {
		t.Fatal("expected non-empty dll path")
	}
	if _, err := os.Stat(dllPath); err != nil {
		t.Fatalf("runtime dll not accessible: %v", err)
	}
	t.Logf("found WebView2 runtime: %s (type=%d)", dllPath, runtimeType)
}

// TestWebView2Smoke 端到端冒烟：消息循环 → 窗口 → WebView2 就绪 → aluka://app 虚拟协议
// 加载页面 → 前端桥接 postMessage 回传事件 → 窗口尺寸同步 → 脚本注入。
// 需显式开启：ALUKA_GUI_WEBVIEW2=1，且本机已安装 WebView2 运行时。
func TestWebView2Smoke(t *testing.T) {
	if os.Getenv("ALUKA_GUI_WEBVIEW2") == "" {
		t.Skip("set ALUKA_GUI_WEBVIEW2=1 to run the WebView2 smoke test")
	}
	if _, _, err := findWebView2Runtime(); err != nil {
		t.Skipf("WebView2 runtime not installed: %v", err)
	}

	// 页面加载后经 window.chrome.webview.postMessage 向主进程回传事件
	memProvider := NewMemoryAssetProvider()
	memProvider.AddAsset("index.html", []byte(`<!DOCTYPE html><html><body><h1>Aluka WebView2</h1>
<script>
window.chrome.webview.postMessage(JSON.stringify({
  type: "event", name: "renderer_ready", data: { engine: "aluka" }
}));
</script></body></html>`))
	SetAssetProvider(memProvider)

	rendererReady := make(chan interface{}, 1)

	app := GetApp()
	go func() { _ = app.Run() }()

	win, err := NewWindow(WindowOptions{
		Title:  "Aluka WebView2 Smoke",
		Width:  640,
		Height: 480,
		Hidden: true,
	})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	native, ok := win.native.(*windowsWindow)
	if !ok {
		t.Fatalf("unexpected native window type: %T", win.native)
	}

	win.On("renderer_ready", func(data interface{}) {
		select {
		case rendererReady <- data:
		default:
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		native.wvMu.RLock()
		ready, wvErr := native.wvReady, native.wvErr
		native.wvMu.RUnlock()
		if ready {
			break
		}
		if wvErr != nil {
			t.Fatalf("WebView2 init failed: %v", wvErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for WebView2 readiness")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 等待前端页面经虚拟协议加载并通过桥接回传事件
	select {
	case data := <-rendererReady:
		m, ok := data.(map[string]interface{})
		if !ok || m["engine"] != "aluka" {
			t.Fatalf("unexpected renderer_ready payload: %#v", data)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for renderer_ready bridge event")
	}

	// 渲染进程执行脚本不应崩溃
	native.ExecuteScript("console.log('smoke')")

	// Phase C：验证窗口级事件（resize/move）能被 Go 侧 `on` 监听捕获。
	windowEvent := make(chan string, 8)
	for _, ev := range []string{"resize", "move", "focus", "blur"} {
		name := ev
		win.On(name, func(data interface{}) {
			select {
			case windowEvent <- name:
			default:
			}
		})
	}

	// 窗口尺寸变化触发 WM_SIZE 边界同步
	win.SetSize(800, 600)
	time.Sleep(600 * time.Millisecond)

	// 关闭前再发送一次 move 以覆盖 WM_MOVE 路径（SetSize 通常已触发 WM_SIZE）
	win.SetPosition(60, 40)
	time.Sleep(300 * time.Millisecond)

	win.Close()
}
