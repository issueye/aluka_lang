package interpreter

import (
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// TestVMOOMLimit 端到端验证 --max-memory 的 VM 安全点：
// 超限时抛可被 JS catch 捕获的 RangeError（V8 同款语义）。
func TestVMOOMLimit(t *testing.T) {
	engine.ResetOOMState()
	oldStrikes := engine.OOMStrikeLimitForTest()
	engine.SetOOMStrikeLimitForTest(1000)
	defer func() {
		engine.StopMemoryWatchdog()
		engine.SetMemoryLimit(0)
		engine.ResetOOMState()
		engine.SetOOMStrikeLimitForTest(oldStrikes)
	}()

	// 极小上限（4MB），脚本在循环中无限拼接字符串（真实内存压力）。
	engine.SetMemoryLimit(4 << 20)

	eng := NewVMEngine()
	defer eng.Shutdown()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()

	script := `
let s = '';
let i = 0;
let caught = '';
try {
  while (true) {
    s += 'chunk-' + (i++) + '-';
  }
} catch (e) {
  caught = e.name + ':' + String(e.message).slice(0, 40);
}
globalThis.__oomResult = caught;
`
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ctx.Eval(script, "oom_test.js")
		if rl, ok := ctx.(interface{ RunLoop() }); ok {
			rl.RunLoop()
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("VM OOM test timed out (OOM not thrown or catch not reached)")
	}

	got, err := ctx.Global().Get("__oomResult")
	if err != nil || got.IsUndefined() || got.String() == "" {
		t.Fatalf("OOM not caught by JS; __oomResult = %v (err %v)", got, err)
	}
	if !strings.HasPrefix(got.String(), "RangeError:") {
		t.Errorf("caught error = %q, want RangeError:...", got.String())
	}
	if !strings.Contains(got.String(), "out of memory") {
		t.Errorf("caught error = %q, want 'out of memory'", got.String())
	}
}
