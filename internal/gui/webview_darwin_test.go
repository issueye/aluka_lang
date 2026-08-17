//go:build darwin

package gui

import (
	"os"
	"strings"
	"testing"
)

func TestExtractBridgePayload(t *testing.T) {
	got := extractBridgePayload("aluka://app/index.html#aluka=%5B%22hi%22%5D", "")
	if got != `["hi"]` {
		t.Fatalf("url payload = %q", got)
	}
	got = extractBridgePayload("about:blank", "\x01aluka\x01%5B%22x%22%5D")
	if got != `["x"]` {
		t.Fatalf("title payload = %q", got)
	}
	if extractBridgePayload("https://example.com", "Home") != "" {
		t.Fatal("expected empty payload")
	}
}

func TestResolveAlukaRef(t *testing.T) {
	cases := []struct{ page, ref, want string }{
		{"aluka://app/index.html", "./assets/app.js", "aluka://app/assets/app.js"},
		{"aluka://app/index.html", "aluka://app/foo.css", "aluka://app/foo.css"},
		{"aluka://app/index.html", "https://cdn.example/x.js", ""},
		{"aluka://app/index.html", "data:text/plain,x", ""},
	}
	for _, c := range cases {
		if got := resolveAlukaRef(c.page, c.ref); got != c.want {
			t.Errorf("resolveAlukaRef(%q,%q)=%q want %q", c.page, c.ref, got, c.want)
		}
	}
}

func TestDarwinDialogUnsupported(t *testing.T) {
	app := createNativeApp(&App{})
	if _, _, err := app.ShowDialog(DialogOptions{Title: "x"}); err == nil {
		t.Fatal("expected dialog error")
	}
}

func TestDarwinHiddenWindow(t *testing.T) {
	if os.Getenv("ALUKA_GUI_TEST") == "" && os.Getenv("CI") != "" {
		t.Skip("headless CI: set ALUKA_GUI_TEST=1 to force native window")
	}
	win, err := NewWindow(WindowOptions{
		Title:  "Aluka Darwin Test",
		Width:  640,
		Height: 480,
		Hidden: true,
	})
	if err != nil {
		t.Skipf("native window unavailable: %v", err)
	}
	defer win.Close()
	if win.Options().Title != "Aluka Darwin Test" {
		t.Errorf("title = %q", win.Options().Title)
	}
	w, h := win.GetSize()
	if w != 640 || h != 480 {
		t.Errorf("size = %dx%d", w, h)
	}
}

func TestConsumeBridgePayload(t *testing.T) {
	dispatch, next := consumeBridgePayload(`["a"]`, "")
	if !dispatch || next != `["a"]` {
		t.Fatalf("first = %v %q", dispatch, next)
	}
	dispatch, next = consumeBridgePayload(`["a"]`, `["a"]`)
	if dispatch || next != `["a"]` {
		t.Fatalf("dup = %v %q", dispatch, next)
	}
	dispatch, next = consumeBridgePayload("", `["a"]`)
	if dispatch || next != "" {
		t.Fatalf("clear = %v %q", dispatch, next)
	}
	dispatch, next = consumeBridgePayload(`["a"]`, "")
	if !dispatch {
		t.Fatal("same payload after clear should dispatch")
	}
}

func TestMenuHasClick(t *testing.T) {
	if menuHasClick([]MenuItem{{Label: "x"}}) {
		t.Fatal("label-only menu")
	}
	if !menuHasClick([]MenuItem{{Label: "x", Click: func() {}}}) {
		t.Fatal("direct Click")
	}
	if !menuHasClick([]MenuItem{{Label: "p", Submenu: []MenuItem{{Label: "c", Click: func() {}}}}}) {
		t.Fatal("nested Click")
	}
}

func TestDarwinTrayClickUnsupported(t *testing.T) {
	_, err := newDarwinTray(TrayOptions{Menu: []MenuItem{{Label: "x", Click: func() {}}}})
	if err == nil || !strings.Contains(err.Error(), "Click") {
		t.Fatalf("CreateTray with Click: %v", err)
	}
}

func TestInlineAlukaAssetsUsesProvider(t *testing.T) {
	mem := NewMemoryAssetProvider()
	mem.AddAsset("index.html", []byte(`<html><head><link rel="stylesheet" href="./app.css"></head><body><script src="./app.js"></script></body></html>`))
	mem.AddAsset("app.css", []byte("body{color:red}"))
	mem.AddAsset("app.js", []byte("window.ok=1"))
	SetAssetProvider(mem)
	html := `<html><head><link rel="stylesheet" href="./app.css"></head><body><script src="./app.js"></script></body></html>`
	out := inlineAlukaAssets("aluka://app/index.html", html)
	if !strings.Contains(out, "<style>body{color:red}</style>") {
		t.Fatalf("css not inlined:\n%s", out)
	}
	if !strings.Contains(out, "<script>window.ok=1</script>") {
		t.Fatalf("js not inlined:\n%s", out)
	}
}

func TestInlineCSSURLs(t *testing.T) {
	mem := NewMemoryAssetProvider()
	mem.AddAsset("app.css", []byte(`body{background:url("./bg.png")}`))
	mem.AddAsset("bg.png", []byte("PNG"))
	SetAssetProvider(mem)
	out := inlineCSSURLs("aluka://app/app.css", `body{background:url("./bg.png")}`)
	if !strings.Contains(out, "data:") || strings.Contains(out, "./bg.png") {
		t.Fatalf("css url not inlined: %s", out)
	}
}
