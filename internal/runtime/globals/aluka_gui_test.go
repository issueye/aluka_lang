package globals

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// TestAlukaGUIWindowAPI 测试 Aluka.gui 窗口创建与控制 API
func TestAlukaGUIWindowAPI(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	if err := NewAluka(ctx, AlukaConfig{}); err != nil {
		t.Fatalf("NewAluka: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	src := `
		globalThis.pass = false;

		var win = Aluka.gui.createWindow({
			title: "Aluka Desktop Studio",
			width: 1024,
			height: 768,
			center: true,
			hidden: true
		});

		if (win.id > 0) {
			win.setTitle("New Studio Title");
			win.setSize(1200, 800);
			var size = win.getSize();
			if (size[0] === 1200 && size[1] === 800) {
				globalThis.pass = true;
			}
		}

		win.close();
	`
	if _, err := ctx.Eval(src, "gui_test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	passV, err := ctx.Global().Get("pass")
	if err != nil || passV.String() != "true" {
		t.Fatalf("Aluka.gui createWindow test failed, pass=%v", passV)
	}
}

// TestAlukaGUIEvents 测试 Aluka.gui 窗口事件派发
func TestAlukaGUIEvents(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	if err := NewAluka(ctx, AlukaConfig{}); err != nil {
		t.Fatalf("NewAluka: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	src := `
		globalThis.eventReceived = false;

		var win = Aluka.gui.createWindow({
			title: "Event Test",
			hidden: true
		});

		win.on("renderer_ready", function(data) {
			if (data && data.version === "2.0") {
				globalThis.eventReceived = true;
			}
		});

		win.emit("renderer_ready", { version: "2.0" });
	`
	if _, err := ctx.Eval(src, "gui_events_test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	if rl, ok := ctx.(interface{ RunLoop() }); ok {
		rl.RunLoop()
	}

	passV, err := ctx.Global().Get("eventReceived")
	if err != nil || passV.String() != "true" {
		t.Fatalf("Aluka.gui event test failed, eventReceived=%v", passV)
	}
}

// TestAlukaGUITrayAndRPC 测试托盘创建与 RPC 注册
func TestAlukaGUITrayAndRPC(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	if err := NewAluka(ctx, AlukaConfig{}); err != nil {
		t.Fatalf("NewAluka: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	src := `
		globalThis.trayPass = false;

		// 1. 创建托盘
		var tray = Aluka.gui.createTray({
			tooltip: "Aluka Studio Running",
			icon: "assets/icon.ico"
		});

		if (tray.id > 0) {
			tray.setTooltip("Updated Tooltip");
			globalThis.trayPass = true;
		}

		// 2. 注册 RPC
		Aluka.gui.app.registerRPC("getEngineVersion", function(args) {
			return { engine: "Aluka", version: "0.1.0" };
		});
	`
	if _, err := ctx.Eval(src, "gui_tray_test.js"); err != nil {
		if strings.Contains(err.Error(), "Shell_NotifyIcon") {
			t.Skipf("无桌面托盘服务支持，跳过测试: %v", err)
		}
		t.Fatalf("Eval error: %v", err)
	}

	trayPassV, err := ctx.Global().Get("trayPass")
	if err != nil || trayPassV.String() != "true" {
		t.Fatalf("Aluka.gui createTray test failed, trayPass=%v", trayPassV)
	}
}

// TestAlukaGUIExtendedSurface 验证 Phase B 补充的 API 表面均已在 JS 侧绑定。
// 仅检查绑定存在性与基础调用，不依赖真实桌面托盘/常驻窗口（无桌面环境时 skips）。
func TestAlukaGUIExtendedSurface(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	if err := NewAluka(ctx, AlukaConfig{}); err != nil {
		t.Fatalf("NewAluka: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	src := `
		var missing = [];
		function has(fn) { return typeof fn === "function"; }

		// app 级新增 API
		if (!has(Aluka.gui.app.unregisterRPC)) missing.push("app.unregisterRPC");
		if (!has(Aluka.gui.app.getWindows)) missing.push("app.getWindows");
		if (!has(Aluka.gui.app.getWindowById)) missing.push("app.getWindowById");
		if (!has(Aluka.gui.shell.openExternal)) missing.push("shell.openExternal");

		// app.on 应返回 disposer 函数（Phase B 补）
		var appDisposer = Aluka.gui.app.on("ready", function() {});
		if (!has(appDisposer)) missing.push("app.on disposer");
		else { appDisposer(); }

		// 尝试创建窗口（无桌面环境会失败，此时仅验证绑定存在且跳过窗口级断言）
		var win = null;
		try { win = Aluka.gui.createWindow({ hidden: true }); } catch (e) {}
		if (win) {
			if (!has(win.setHTML)) missing.push("win.setHTML");
			if (!has(win.toggleMaximize)) missing.push("win.toggleMaximize");
			if (!has(win.setProgressBar)) missing.push("win.setProgressBar");
			if (!has(win.setOverlayIcon)) missing.push("win.setOverlayIcon");
			if (!has(win.setMenu)) missing.push("win.setMenu");
			if (!has(win.off)) missing.push("win.off");
			win.close();
		}

		globalThis.bindingPass = missing.length === 0;
		globalThis.bindingMissing = missing.join(",");
	`
	if _, err := ctx.Eval(src, "gui_surface_test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	passV, err := ctx.Global().Get("bindingPass")
	if err != nil || passV.String() != "true" {
		missV, _ := ctx.Global().Get("bindingMissing")
		t.Fatalf("Aluka.gui extended API surface incomplete, missing=%v", missV)
	}
}
