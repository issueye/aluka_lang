package globals

import (
	"sync/atomic"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/ipc"
	"github.com/aluka-lang/aluka/internal/runtime/globals/galuka"
)

// TestAlukaIPCSync 测试同步阻塞 RPC 调用 (callSync)
func TestAlukaIPCSync(t *testing.T) {
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

		var server = Aluka.ipc.listen("tcp:127.0.0.1:0", {
			methods: {
				multiply: (params) => params[0] * params[1],
				greet: (params) => "Hello " + params[0]
			}
		});

		var client = Aluka.ipc.connect("tcp:" + server.address);

		// 同步阻塞调用 callSync
		var mul = client.callSync("multiply", [6, 7]);
		var greeting = client.callSync("greet", ["Aluka"]);

		if (mul === 42 && greeting === "Hello Aluka") {
			globalThis.pass = true;
		}

		client.close();
		server.close();
	`
	if _, err := ctx.Eval(src, "sync_test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	passV, err := ctx.Global().Get("pass")
	if err != nil || passV.String() != "true" {
		t.Fatalf("Aluka.ipc callSync test failed, pass=%v", passV)
	}
}

// TestAlukaIPCAsyncAndConcurrent 测试高并发异步 Promise.all 多路复用 RPC
func TestAlukaIPCAsyncAndConcurrent(t *testing.T) {
	var handledCount int32

	srv, err := ipc.NewServer("tcp:127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	// 模拟并发服务端计算
	srv.RegisterMethod("computeSquare", func(params interface{}) (interface{}, error) {
		atomic.AddInt32(&handledCount, 1)
		arr := params.([]interface{})
		x := arr[0].(float64)
		return x * x, nil
	})

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

	pluginProxy, err := galuka.CreatePluginProxyModule(ctx, "tcp:"+srv.Addr().String())
	if err != nil {
		t.Fatalf("CreatePluginProxyModule: %v", err)
	}
	_ = ctx.Global().Set("mathPlugin", pluginProxy)

	src := `
		globalThis.pass = false;
		globalThis.completed = 0;

		async function runConcurrent() {
			var promises = [];
			for (var i = 1; i <= 100; i++) {
				promises.push(mathPlugin.call("computeSquare", [i]));
			}

			var results = await Promise.all(promises);
			var sum = 0;
			for (var j = 0; j < results.length; j++) {
				sum += results[j];
			}

			// 1^2 + 2^2 + ... + 100^2 = 338350
			if (sum === 338350 && results.length === 100) {
				globalThis.pass = true;
			}
		}

		runConcurrent();
	`
	if _, err := ctx.Eval(src, "concurrent_test.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	if rl, ok := ctx.(interface{ RunLoop() }); ok {
		rl.RunLoop()
	}

	passV, err := ctx.Global().Get("pass")
	if err != nil || passV.String() != "true" {
		t.Fatalf("Concurrent IPC test failed: pass=%v, handled=%d", passV, atomic.LoadInt32(&handledCount))
	}
}
