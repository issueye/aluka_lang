package builtin

import (
	"os"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/builtin/nodediag"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// TestV8HeapSnapshot 验证 v8.getHeapSnapshot 与 v8.writeHeapSnapshot
func TestV8HeapSnapshot(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	v8Mod, err := nodediag.NewV8(ctx)
	if err != nil {
		t.Fatalf("NewV8: %v", err)
	}
	_ = ctx.Global().Set("v8", v8Mod)

	testFile := "test_heap_dump.heapsnapshot"
	defer os.Remove(testFile)

	src := `
		globalThis.pass = false;
		var path = v8.writeHeapSnapshot("test_heap_dump.heapsnapshot");
		if (path === "test_heap_dump.heapsnapshot") {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "test_snapshot.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	passV, _ := ctx.Global().Get("pass")
	if passV.String() != "true" {
		t.Fatalf("v8.writeHeapSnapshot failed")
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read snapshot file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"node_fields"`) || !strings.Contains(content, `"AlukaRuntime"`) {
		t.Fatalf("Snapshot content invalid: %s", content[:200])
	}
}

// TestInspectorSession 验证 inspector.Session 的连接与 post 回调
func TestInspectorSession(t *testing.T) {
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	inspMod, err := nodediag.NewInspector(ctx)
	if err != nil {
		t.Fatalf("NewInspector: %v", err)
	}
	_ = ctx.Global().Set("inspector", inspMod)

	src := `
		globalThis.pass = false;
		var session = new inspector.Session();
		
		// 1. 未连接时 post 抛错
		var notConnectedError = false;
		try {
			session.post("Runtime.enable");
		} catch (e) {
			notConnectedError = true;
		}

		// 2. 连接后发送 CDP 命令并接收回调
		var callbackSuccess = false;
		session.connect();
		session.post("HeapProfiler.takeHeapSnapshot", (err, res) => {
			if (!err && res && res.method === "HeapProfiler.takeHeapSnapshot") {
				callbackSuccess = true;
			}
		});

		session.disconnect();

		if (notConnectedError && callbackSuccess) {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "test_session.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	passV, _ := ctx.Global().Get("pass")
	if passV.String() != "true" {
		t.Fatalf("inspector.Session test failed")
	}
}
