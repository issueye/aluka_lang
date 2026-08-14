package module

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/ipc"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// TestPluginModuleDynamicImport 验证通过 require("aluka:plugin:xxx") 动态连接外部微服务
func TestPluginModuleDynamicImport(t *testing.T) {
	// 启动外部模拟守护进程（模拟 Rust/C++ 核心）
	srv, err := ipc.NewServer("tcp:127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	srv.RegisterMethod("processData", func(params interface{}) (interface{}, error) {
		arr := params.([]interface{})
		x := arr[0].(float64)
		return x * 10, nil
	})

	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Close()

	if err := globals.NewAluka(ctx, globals.AlukaConfig{}); err != nil {
		t.Fatalf("NewAluka: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	loader := NewLoader(ctx)
	reqFn := loader.MakeRequireFunc(".")
	_ = ctx.Global().Set("require", reqFn)

	// 注册指向后台服务的插件
	pluginSpecifier := "aluka:plugin:tcp:" + srv.Addr().String()

	src := `
		globalThis.pass = false;
		var myEngine = require("` + pluginSpecifier + `");
		var result = myEngine.call("processData", [4.2]);
		if (result === 42) {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "main.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	passV, err := ctx.Global().Get("pass")
	if err != nil || passV.String() != "true" {
		t.Fatalf("Plugin require failed, pass=%v", passV)
	}
}
