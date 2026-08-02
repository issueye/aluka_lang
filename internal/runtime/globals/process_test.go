package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// TestProcessArgv 验证 process.argv 显式注入。
func TestProcessArgv(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	err := NewProcess(ctx, ProcessConfig{
		Argv: []string{"aluka", "script.js", "arg1", "arg2"},
		Env:  map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("NewProcess: %v", err)
	}

	// process.argv.length === 4
	v, err := ctx.Eval(`process.argv.length`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	n, _ := v.Int()
	if n != 4 {
		t.Errorf("argv.length = %d, want 4", n)
	}

	// process.argv[1] === "script.js"
	v, _ = ctx.Eval(`process.argv[1]`, "test.js")
	if v.String() != "script.js" {
		t.Errorf("argv[1] = %q, want 'script.js'", v.String())
	}
}

// TestProcessEnv 验证 process.env 读取。
func TestProcessEnv(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	_ = NewProcess(ctx, ProcessConfig{
		Env: map[string]string{"NODE_ENV": "test"},
	})

	v, err := ctx.Eval(`process.env.NODE_ENV`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.String() != "test" {
		t.Errorf("env.NODE_ENV = %q, want 'test'", v.String())
	}
}

// TestProcessPlatform 验证 platform 字段。
func TestProcessPlatform(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	_ = NewProcess(ctx, ProcessConfig{})

	v, _ := ctx.Eval(`process.platform`, "test.js")
	s := v.String()
	if s != "linux" && s != "darwin" && s != "win32" && s != "freebsd" {
		t.Errorf("platform = %q", s)
	}
}

// TestProcessVersions 验证 versions 对象。
func TestProcessVersions(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	_ = NewProcess(ctx, ProcessConfig{})

	v, _ := ctx.Eval(`process.versions.aluka`, "test.js")
	if v.String() != "0.1.0" {
		t.Errorf("versions.aluka = %q, want '0.1.0'", v.String())
	}
}

// TestProcessHrtime 验证 hrtime 返回 2 元素数组。
func TestProcessHrtime(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	_ = NewProcess(ctx, ProcessConfig{})

	v, err := ctx.Eval(`process.hrtime()`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	arr, ok := v.(*engine.ArrayValue)
	if !ok {
		t.Fatalf("not array: %T", v)
	}
	if len(arr.Elems()) != 2 {
		t.Errorf("len = %d, want 2", len(arr.Elems()))
	}
}

// TestProcessMemoryUsage 验证 memoryUsage 返回对象。
func TestProcessMemoryUsage(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	_ = NewProcess(ctx, ProcessConfig{})

	v, err := ctx.Eval(`process.memoryUsage().rss`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if _, ok := v.Float(); !ok {
		t.Errorf("rss not number: %v", v)
	}
}
