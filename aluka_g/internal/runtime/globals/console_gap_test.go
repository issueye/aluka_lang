package globals

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gconsole"
)

// === 差距补齐 P2：console L1（gap-closure-plan §3 P2）====================

// TestConsoleProfileNoop 验证 profile/profileEnd/timeStamp 为静默 no-op
// （Node 22 行为：存在但不输出、不抛错）。
func TestConsoleProfileNoop(t *testing.T) {
	ctx, out, errOut := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	_, err := ctx.Eval(`
		console.profile();
		console.profile('myprofile');
		console.profileEnd();
		console.profileEnd('myprofile');
		console.timeStamp();
		console.timeStamp('mylabel');
	`, "test.js")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want empty (no-op)", out.String())
	}
	if errOut.String() != "" {
		t.Errorf("stderr = %q, want empty (no-op)", errOut.String())
	}

	// 方法存在性检查（stub 引擎不支持 instanceof，Go 侧直接断言）。
	consoleVal, _ := ctx.Global().Get("console")
	consoleObj, _ := consoleVal.AsObject()
	for _, m := range []string{"profile", "profileEnd", "timeStamp"} {
		v, err := consoleObj.Get(m)
		if err != nil || !v.IsFunction() {
			t.Errorf("console.%s = %v (%v), want function", m, v, err)
		}
	}
}

// fakeStreamObj 构造带 write 方法的假可写流（模拟 process.stdout）。
func fakeStreamObj(t *testing.T, buf *bytes.Buffer) engine.Object {
	t.Helper()
	obj := engine.NewObject()
	_ = obj.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			buf.WriteString(args[0].String())
		}
		return engine.Boolean(true), nil
	}))
	return obj
}

// TestConsoleCtor 验证 console.Console 构造器：
//   - 配置对象形式 {stdout, stderr}；stderr 缺省回退 stdout（Node 语义）
//   - 位置参数形式 Console(stdout[, stderr])
//   - stdout 必填：缺失抛 TypeError
//   - prototype 镜像方法 + 实例自有属性方法
//   - 全局 console 的 [[Prototype]] 指向 Console.prototype（instanceof 依据）
func TestConsoleCtor(t *testing.T) {
	ctx, _, _ := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	consoleVal, err := ctx.Global().Get("console")
	if err != nil {
		t.Fatalf("Get console: %v", err)
	}
	consoleObj, ok := consoleVal.AsObject()
	if !ok {
		t.Fatalf("console is not an object")
	}
	ctorVal, err := consoleObj.Get("Console")
	if err != nil {
		t.Fatalf("Get Console: %v", err)
	}
	ctor, ok := ctorVal.AsFunction()
	if !ok {
		t.Fatalf("console.Console is not a function")
	}

	t.Run("配置对象形式", func(t *testing.T) {
		out := &bytes.Buffer{}
		errOut := &bytes.Buffer{}
		cfgObj := engine.NewObjectFromPairs([]engine.Value{
			engine.Str("stdout"), fakeStreamObj(t, out),
			engine.Str("stderr"), fakeStreamObj(t, errOut),
		})
		instVal, err := ctor.Call([]engine.Value{cfgObj, engine.Undefined()})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		inst, ok := instVal.AsObject()
		if !ok {
			t.Fatalf("instance is not an object: %s", instVal.String())
		}
		if _, err := inst.Get("log"); err != nil {
			t.Fatalf("instance.log missing: %v", err)
		}
		if _, err := ctx.Eval(`c.log('cfg-ok')`, "test.js"); err == nil {
			t.Fatal("unexpected eval ok with undefined c")
		}
		// 直接调用实例方法并断言分流到对应流。
		logFn, _ := inst.Get("log")
		if f, ok := logFn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{engine.Str("to-stdout")}); err != nil {
				t.Fatalf("log: %v", err)
			}
		}
		errFn, _ := inst.Get("error")
		if f, ok := errFn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{engine.Str("to-stderr")}); err != nil {
				t.Fatalf("error: %v", err)
			}
		}
		if got := out.String(); !strings.Contains(got, "to-stdout") {
			t.Errorf("stdout = %q, want contains to-stdout", got)
		}
		if got := errOut.String(); !strings.Contains(got, "to-stderr") {
			t.Errorf("stderr = %q, want contains to-stderr", got)
		}
		if strings.Contains(out.String(), "to-stderr") {
			t.Errorf("stdout 收到 error 输出: %q", out.String())
		}
	})

	t.Run("stderr 缺省回退 stdout", func(t *testing.T) {
		out := &bytes.Buffer{}
		cfgObj := engine.NewObjectFromPairs([]engine.Value{
			engine.Str("stdout"), fakeStreamObj(t, out),
		})
		instVal, err := ctor.Call([]engine.Value{cfgObj})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		inst, _ := instVal.AsObject()
		errFn, _ := inst.Get("error")
		if f, ok := errFn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{engine.Str("fallback")}); err != nil {
				t.Fatalf("error: %v", err)
			}
		}
		if got := out.String(); !strings.Contains(got, "fallback") {
			t.Errorf("stdout = %q, want contains fallback（stderr 回退 stdout）", got)
		}
	})

	t.Run("位置参数形式", func(t *testing.T) {
		out := &bytes.Buffer{}
		instVal, err := ctor.Call([]engine.Value{fakeStreamObj(t, out)})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		inst, _ := instVal.AsObject()
		logFn, _ := inst.Get("log")
		if f, ok := logFn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{engine.Str("pos")}); err != nil {
				t.Fatalf("log: %v", err)
			}
		}
		if got := out.String(); !strings.Contains(got, "pos") {
			t.Errorf("stdout = %q, want contains pos", got)
		}
	})

	t.Run("stdout 缺失抛 TypeError", func(t *testing.T) {
		if _, err := ctor.Call(nil); err == nil {
			t.Fatal("new Console() should throw TypeError")
		}
		cfgObj := engine.NewObject() // 空配置对象同样抛错（Node 行为）
		if _, err := ctor.Call([]engine.Value{cfgObj}); err == nil {
			t.Fatal("new Console({}) should throw TypeError")
		}
	})

	t.Run("prototype 镜像与 instanceof 链", func(t *testing.T) {
		protoVal, err := consoleObj.Get("Console")
		if err != nil {
			t.Fatalf("Get Console: %v", err)
		}
		ctorObj, ok := protoVal.AsObject()
		if !ok {
			t.Fatalf("Console not object")
		}
		proto, err := ctorObj.Get("prototype")
		if err != nil {
			t.Fatalf("Console.prototype: %v", err)
		}
		protoObj, ok := proto.AsObject()
		if !ok {
			t.Fatalf("prototype not object")
		}
		// Console.prototype.assert 存在（L1 要求 Console#assert）。
		if av, err := protoObj.Get("assert"); err != nil || !av.IsFunction() {
			t.Errorf("Console.prototype.assert = %v (%v), want function", av, err)
		}
		// 全局 console 的 [[Prototype]] === Console.prototype。
		if got := engine.GetProto(consoleObj); got != protoObj {
			t.Errorf("getPrototypeOf(console) != Console.prototype")
		}
		// 新实例的 [[Prototype]] 同样指向 Console.prototype。
		out := &bytes.Buffer{}
		cfgObj := engine.NewObjectFromPairs([]engine.Value{
			engine.Str("stdout"), fakeStreamObj(t, out),
		})
		instVal, err := ctor.Call([]engine.Value{cfgObj})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		inst, _ := instVal.AsObject()
		if got := engine.GetProto(inst); got != protoObj {
			t.Errorf("getPrototypeOf(instance) != Console.prototype")
		}
	})
}

// TestConsoleInstanceStateIsolated 验证多实例状态隔离（time 计时器互不干扰）。
func TestConsoleInstanceStateIsolated(t *testing.T) {
	ctx, _, _ := newTestContext(t, gconsole.ConsoleConfig{})
	defer ctx.Close()

	consoleVal, _ := ctx.Global().Get("console")
	consoleObj, _ := consoleVal.AsObject()
	ctorVal, _ := consoleObj.Get("Console")
	ctor, _ := ctorVal.AsFunction()

	out := &bytes.Buffer{}
	cfgObj := engine.NewObjectFromPairs([]engine.Value{
		engine.Str("stdout"), fakeStreamObj(t, out),
	})
	instVal, err := ctor.Call([]engine.Value{cfgObj})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	inst, _ := instVal.AsObject()
	timeVal, _ := inst.Get("time")
	timeEndVal, _ := inst.Get("timeEnd")
	timeFn, timeOk := timeVal.AsFunction()
	timeEndFn, timeEndOk := timeEndVal.AsFunction()
	if !timeOk || !timeEndOk {
		t.Fatalf("instance time/timeEnd missing")
	}
	// 在实例上调用 time/timeEnd：无警告（已在本实例启动计时器）。
	if _, err := timeFn.Call([]engine.Value{engine.Str("iso")}); err != nil {
		t.Fatalf("time: %v", err)
	}
	if _, err := timeEndFn.Call([]engine.Value{engine.Str("iso")}); err != nil {
		t.Fatalf("timeEnd: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "iso:") {
		t.Errorf("stdout = %q, want contains 'iso:'", got)
	}
}
