package builtin

// node:http 端到端测试：启动服务器，用客户端请求自身，验证完整闭环。
// 依赖事件循环（RunLoop）驱动异步回调。
//
// 测试策略：把 JS 写入临时 .cjs 文件，经 loader.Run 执行——loader 会注入
// require/__filename 等模块全局（与 CLI 行为一致），require('node:http')
// 经 RegisterAll 注册的工厂构造导出对象。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	modmodule "github.com/aluka-lang/aluka/internal/runtime/module"
)

// httpTestEnv 封装测试环境：VM 上下文 + 已注册内置模块的 loader。
type httpTestEnv struct {
	ctx    engine.Context
	loader *modmodule.Loader
}

// newHTTPEnv 创建带事件循环与内置模块（http/timers/buffer）的测试环境。
func newHTTPEnv(t *testing.T) *httpTestEnv {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := globals.NewTimers(ctx, globals.TimerConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := globals.NewBuffer(ctx, globals.BufferConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := globals.NewWebCrypto(ctx, globals.WebCryptoConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	loader := modmodule.NewLoader(ctx)
	RegisterAll(loader)
	return &httpTestEnv{ctx: ctx, loader: loader}
}

// runWithLoop 将 JS 写入临时 .cjs 文件并经 loader 执行，然后驱动事件循环
// 直到无活跃任务（服务器已 close、定时器已 clear）或超时。
func (e *httpTestEnv) runWithLoop(t *testing.T, code string) error {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cjs")
	if err := os.WriteFile(path, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	if err := e.loader.Run(path); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		if vm, ok := e.ctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return errTimeout
	}
}

// globalGet 从全局对象读值（转字符串便于断言）。
func (e *httpTestEnv) globalGet(key string) string {
	v, _ := e.ctx.Global().Get(key)
	return v.String()
}

var errTimeout = &timeoutError{}

type timeoutError struct{}

func (e *timeoutError) Error() string { return "event loop timeout" }

// TestHTTPServerClientRoundTrip: 服务器 + 客户端完整闭环。
// 服务器动态端口，客户端 http.get 请求自身，验证状态码/响应体/请求体事件。
func TestHTTPServerClientRoundTrip(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var http = require('node:http');
var server = http.createServer(function(req, res) {
  res.writeHead(200, {'Content-Type': 'text/plain'});
  res.end('Hello from aluka!');
});
server.listen(0, function() {
  var port = server.address().port;
  globalThis.__port = port;
  http.get('http://127.0.0.1:' + port + '/test', function(res) {
    globalThis.__status = res.statusCode;
    var body = '';
    res.on('data', function(c) { body += c; });
    res.on('end', function() {
      globalThis.__body = body;
      server.close(function() { globalThis.__closed = true; });
    });
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 验证结果。
	port := env.globalGet("__port")
	if port == "undefined" || port == "0" {
		t.Errorf("port = %q, want real port", port)
	}
	if got := env.globalGet("__status"); got != "200" {
		t.Errorf("status = %q, want 200", got)
	}
	if got := env.globalGet("__body"); got != "Hello from aluka!" {
		t.Errorf("body = %q, want Hello from aluka!", got)
	}
	if got := env.globalGet("__closed"); got != "true" {
		t.Errorf("closed = %q, want true", got)
	}
}

// TestServerEchoBody: 服务器读取请求体并原样返回（验证 'data'/'end' 时序）。
func TestServerEchoBody(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var http = require('node:http');
var server = http.createServer(function(req, res) {
  var body = '';
  req.on('data', function(c) { body += c; });
  req.on('end', function() { res.end(body); });
});
server.listen(0, function() {
  var port = server.address().port;
  http.request({ host: '127.0.0.1', port: port, path: '/echo', method: 'POST' }, function(res) {
    var out = '';
    res.on('data', function(c) { out += c; });
    res.on('end', function() {
      globalThis.__echo = out;
      server.close();
    });
  }).end('ping-pong');
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__echo"); got != "ping-pong" {
		t.Errorf("echo = %q, want ping-pong", got)
	}
}

// TestSetTimeoutBasics: setTimeout 基础行为。
func TestSetTimeoutBasics(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
setTimeout(function() {
  globalThis.__t = 'fired';
}, 10);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__t"); got != "fired" {
		t.Errorf("setTimeout = %q, want fired", got)
	}
}

// TestSetIntervalAndClear: setInterval 计数后 clearInterval。
func TestSetIntervalAndClear(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var count = 0;
var iv = setInterval(function() {
  count++;
  if (count >= 3) {
    clearInterval(iv);
    globalThis.__count = count;
  }
}, 5);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__count"); got != "3" {
		t.Errorf("interval count = %q, want 3", got)
	}
}

// TestSetImmediate: setImmediate 在下一 tick 触发。
func TestSetImmediate(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var order = [];
order.push('sync');
setImmediate(function() { order.push('immediate'); globalThis.__order = order.join(','); });
order.push('sync2');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__order"); got != "sync,sync2,immediate" {
		t.Errorf("immediate order = %q, want sync,sync2,immediate", got)
	}
}
