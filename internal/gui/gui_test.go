package gui

import (
	"io"
	"strings"
	"testing"
)

func TestGUIProtocolAndAssets(t *testing.T) {
	memProvider := NewMemoryAssetProvider()
	memProvider.AddAsset("index.html", []byte("<!DOCTYPE html><html><body>Hello Aluka GUI</body></html>"))
	memProvider.AddAsset("assets/app.js", []byte("console.log('App loaded');"))

	SetAssetProvider(memProvider)

	rc, mimeType, status, err := ResolveAssetURL("aluka://app/index.html")
	if err != nil || status != 200 {
		t.Fatalf("Resolve index.html failed: status=%d, err=%v", status, err)
	}
	defer rc.Close()

	if !strings.HasPrefix(mimeType, "text/html") {
		t.Errorf("Unexpected mimeType: got %q", mimeType)
	}
	content, _ := io.ReadAll(rc)
	if string(content) != "<!DOCTYPE html><html><body>Hello Aluka GUI</body></html>" {
		t.Errorf("Unexpected content: %s", string(content))
	}

	// 测试 JS 资源
	rc2, mime2, _, err2 := ResolveAssetURL("aluka://app/assets/app.js")
	if err2 != nil {
		t.Fatalf("Resolve app.js failed: %v", err2)
	}
	defer rc2.Close()
	if !strings.HasPrefix(mime2, "application/javascript") && !strings.HasPrefix(mime2, "text/javascript") {
		t.Errorf("Unexpected JS mimeType: %s", mime2)
	}
}

func TestGUIWindowManagement(t *testing.T) {
	app := GetApp()

	win, err := NewWindow(WindowOptions{
		Title:  "Test Main Window",
		Width:  800,
		Height: 600,
		Center: true,
		Hidden: true,
	})
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}

	if win.ID() == 0 {
		t.Errorf("Expected non-zero window ID")
	}

	if win.Options().Title != "Test Main Window" {
		t.Errorf("Unexpected title: %s", win.Options().Title)
	}

	// 事件订阅与派发测试
	var eventFired bool
	win.On("custom-event", func(data interface{}) {
		eventFired = true
	})
	win.Emit("custom-event", "test-payload")

	if !eventFired {
		t.Errorf("Expected custom-event to fire")
	}

	win.SetTitle("Updated Title")
	win.SetSize(1024, 768)

	w, h := win.GetSize()
	if w <= 0 || h <= 0 {
		t.Errorf("Invalid window size: %dx%d", w, h)
	}

	// 窗口销毁
	win.Close()

	if app.GetWindowByID(win.ID()) != nil {
		t.Errorf("Expected window to be removed from app after Close()")
	}
}
