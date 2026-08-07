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
func TestProcessStdinStreamShape(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	_ = NewProcess(ctx, ProcessConfig{})

	processValue, err := ctx.Global().Get("process")
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	processObject, ok := processValue.AsObject()
	if !ok {
		t.Fatalf("process is not an object")
	}
	stdinValue, err := processObject.Get("stdin")
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdinObject, ok := stdinValue.AsObject()
	if !ok {
		t.Fatalf("stdin is not an object")
	}
	for _, name := range []string{"on", "once", "off", "removeListener", "setEncoding", "setRawMode", "pause", "resume"} {
		value, getErr := stdinObject.Get(name)
		if getErr != nil || !value.IsFunction() {
			t.Errorf("stdin.%s is not a function", name)
		}
	}
	isTTY, err := stdinObject.Get("isTTY")
	if err != nil || isTTY.Type() != engine.TypeBoolean {
		t.Errorf("stdin.isTTY is not a boolean")
	}
	stdoutValue, err := processObject.Get("stdout")
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	stdoutObject, ok := stdoutValue.AsObject()
	if !ok {
		t.Fatalf("stdout is not an object")
	}
	for _, name := range []string{"write", "on", "once", "off", "removeListener"} {
		value, getErr := stdoutObject.Get(name)
		if getErr != nil || !value.IsFunction() {
			t.Errorf("stdout.%s is not a function", name)
		}
	}
}

// TestProcessListenerAliases 验证 Pi 交互模式使用的 prependListener/off。
func TestProcessListenerAliases(t *testing.T) {
	eng := engine.NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	if err := NewProcess(ctx, ProcessConfig{}); err != nil {
		t.Fatalf("NewProcess: %v", err)
	}
	procValue, err := ctx.Global().Get("process")
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	proc, ok := procValue.AsObject()
	if !ok {
		t.Fatal("process is not an object")
	}
	getFn := func(name string) engine.Function {
		v, getErr := proc.Get(name)
		if getErr != nil || !v.IsFunction() {
			t.Fatalf("process.%s is not a function", name)
		}
		f, _ := v.AsFunction()
		return f
	}
	var calls []string
	first := engine.NewFunction("first", func([]engine.Value) (engine.Value, error) {
		calls = append(calls, "first")
		return engine.Undefined(), nil
	})
	second := engine.NewFunction("second", func([]engine.Value) (engine.Value, error) {
		calls = append(calls, "second")
		return engine.Undefined(), nil
	})
	on, prepend, emit, off := getFn("on"), getFn("prependListener"), getFn("emit"), getFn("off")
	_, _ = on.Call([]engine.Value{engine.Str("test"), second})
	_, _ = prepend.Call([]engine.Value{engine.Str("test"), first})
	_, _ = emit.Call([]engine.Value{engine.Str("test")})
	_, _ = off.Call([]engine.Value{engine.Str("test"), first})
	_, _ = emit.Call([]engine.Value{engine.Str("test")})
	got := ""
	for i, call := range calls {
		if i > 0 {
			got += ","
		}
		got += call
	}
	if got != "first,second,second" {
		t.Fatalf("listener order/removal = %q, want first,second,second", got)
	}
}

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
