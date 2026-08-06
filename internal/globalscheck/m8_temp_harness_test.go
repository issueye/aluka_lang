package globals

// 临时 M8 验证装置（仅用于本代理验证，提交前删除）：
// 注册全部运行时全局后运行 m8 diff 脚本，把捕获的 console.log 输出
// 打印到测试日志，供与 node 输出对比。

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// m8AllGlobals 注册与 cmd/aluka registerRuntimeGlobals 一致的全局。
func m8AllGlobals(ctx engine.Context) error {
	calls := []func() error{
		func() error { return NewConsole(ctx, ConsoleConfig{}) },
		func() error { return NewProcess(ctx, ProcessConfig{}) },
		func() error { return NewPerformance(ctx, PerformanceConfig{}) },
		func() error { return NewTimers(ctx, TimerConfig{}) },
		func() error { return NewBuffer(ctx, BufferConfig{}) },
		func() error { return NewEncoding(ctx, EncodingConfig{}) },
		func() error { return NewURL(ctx, URLConfig{}) },
		func() error { return NewAbort(ctx, AbortConfig{}) },
		func() error { return NewEvent(ctx, EventConfig{}) },
		func() error { return NewDOMException(ctx, DOMExceptionConfig{}) },
		func() error { return NewFetch(ctx, FetchConfig{}) },
		func() error { return NewBlob(ctx, BlobConfig{}) },
		func() error { return NewStream(ctx, StreamConfig{}) },
		func() error { return NewWebCrypto(ctx, WebCryptoConfig{}) },
		func() error { return NewURLPattern(ctx, URLPatternConfig{}) },
		func() error { return NewMessageChannel(ctx, MessageConfig{}) },
		func() error { return NewNavigator(ctx, NavigatorConfig{}) },
		func() error { return NewBroadcastChannel(ctx, MessageConfig{}) },
		func() error { return NewWebSocket(ctx, WebSocketConfig{}) },
	}
	for _, c := range calls {
		if err := c(); err != nil {
			return err
		}
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	_ = ctx.Global().Set("global", ctx.Global())
	return nil
}

// m8RunScript 运行脚本并返回捕获的 console.log 输出。
func m8RunScript(t *testing.T, script string) (string, error) {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		return "", err
	}
	defer ctx.Close()
	if err := m8AllGlobals(ctx); err != nil {
		return "", err
	}
	// 捕获 console.log → __log。
	prelude := `globalThis.__log = '';
const __origLog = console.log;
console.log = function() {
  var parts = [];
  for (var i = 0; i < arguments.length; i++) parts.push(String(arguments[i]));
  globalThis.__log += parts.join(' ') + '\n';
};
`
	if _, err := ctx.Eval(prelude+script, "m8_script.js"); err != nil {
		return "", err
	}
	done := make(chan struct{})
	go func() {
		if vm, ok := ctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("event loop timeout")
	}
	logV, _ := ctx.Global().Get("__log")
	return logV.String(), nil
}

// m8DiffFile 定位 diff 用例文件（兼容不同包目录深度）。
func m8DiffFile(name string) ([]byte, error) {
	var lastErr error
	for _, p := range []string{
		"../../../tests/compat/node22/diff/" + name, // internal/runtime/globals
		"../../tests/compat/node22/diff/" + name,    // internal/globalscheck
	} {
		if data, err := os.ReadFile(p); err == nil {
			return data, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}

// TestM8TempEncoding 运行 m8-encoding.cjs（临时验证）。
func TestM8TempEncoding(t *testing.T) {
	data, err := m8DiffFile("m8-encoding.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-ENCODING OUTPUT:\n%s", strings.TrimSpace(out))
}

// TestM8TempStructuredClone 运行 m8-structured-clone.cjs（临时验证）。
func TestM8TempStructuredClone(t *testing.T) {
	data, err := m8DiffFile("m8-structured-clone.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-STRUCTURED-CLONE OUTPUT:\n%s", strings.TrimSpace(out))
}

// TestM8TempEvents 运行 m8-events.cjs（临时验证）。
func TestM8TempEvents(t *testing.T) {
	data, err := m8DiffFile("m8-events.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-EVENTS OUTPUT:\n%s", strings.TrimSpace(out))
}

// TestM8TempBlob 运行 m8-blob.cjs（临时验证）。
func TestM8TempBlob(t *testing.T) {
	data, err := m8DiffFile("m8-blob.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-BLOB OUTPUT:\n%s", strings.TrimSpace(out))
}


// TestM8TempStreams 运行 m8-streams.cjs（临时验证）。
func TestM8TempStreams(t *testing.T) {
	data, err := m8DiffFile("m8-streams.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-STREAMS OUTPUT:\n%s", strings.TrimSpace(out))
}

// TestM8TempFetch 运行 m8-fetch.cjs（临时验证）。
func TestM8TempFetch(t *testing.T) {
	data, err := m8DiffFile("m8-fetch.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-FETCH OUTPUT:\n%s", strings.TrimSpace(out))
}

// TestM8TempAbort 运行 m8-abort.cjs（临时验证）。
func TestM8TempAbort(t *testing.T) {
	data, err := m8DiffFile("m8-abort.cjs")
	if err != nil {
		t.Skip("diff file not found")
	}
	out, err := m8RunScript(t, string(data))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("M8-ABORT OUTPUT:\n%s", strings.TrimSpace(out))
}
