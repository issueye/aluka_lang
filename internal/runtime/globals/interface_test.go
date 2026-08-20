package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// newInterfaceTestContext 创建裸 VM 上下文（仅引擎内建，不注册其余全局）。
func newInterfaceTestContext(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	return ctx
}

// ifaceEval 在上下文求值并返回字符串形式结果。
func ifaceEval(t *testing.T, ctx engine.Context, code string) string {
	t.Helper()
	v, err := ctx.Eval(code, "interface_test.js")
	if err != nil {
		t.Fatalf("Eval: %v\n  code: %s", err, code)
	}
	return v.String()
}

// TestRegisterInterfaceDescriptors 验证 helper 生成的描述符与原型链语义（对齐 Node 22 WebIDL）。
func TestRegisterInterfaceDescriptors(t *testing.T) {
	ctx := newInterfaceTestContext(t)

	_, proto, err := RegisterInterface(ctx, WebInterface{Name: "Widget", Tag: "Widget"})
	if err != nil {
		t.Fatalf("RegisterInterface: %v", err)
	}
	// 实例方法：默认 flags = wec 全 true（WebIDL 接口成员）。
	_ = proto.Set("doThing", engine.NewFunction("doThing", func(args []engine.Value) (engine.Value, error) {
		return engine.Str("did"), nil
	}))

	// 实例本体接入原型。
	instance := engine.NewObject()
	engine.SetProto(instance, proto)

	cases := []struct {
		name string
		code string
		want string
	}{
		{"prototype 链到 Object.prototype",
			`Object.getPrototypeOf(Widget.prototype) === Object.prototype`, "true"},
		{"constructor 不可枚举", `Object.keys(Widget.prototype).join(',')`, "doThing"},
		{"ctor.prototype 不可枚举", `Object.keys(Widget).includes('prototype')`, "false"},
		{"toStringTag 生效", `Object.prototype.toString.call(globalThis.__inst)`, "[object Widget]"},
		{"instanceof 成立", `globalThis.__inst instanceof Widget`, "true"},
		{"方法经原型可达", `globalThis.__inst.doThing()`, "did"},
		{"自有键为空", `Object.getOwnPropertyNames(globalThis.__inst).length`, "0"},
		{"delete 后方法仍可达", `delete globalThis.__inst.doThing && typeof globalThis.__inst.doThing`, "function"},
		{"hasOwnProperty 走 Object.prototype", `globalThis.__inst.hasOwnProperty('doThing')`, "false"},
		{"new 抛 TypeError", `try { new Widget() } catch (e) { e.constructor.name }`, "TypeError"},
	}
	_ = ctx.Global().Set("__inst", instance)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ifaceEval(t, ctx, tc.code); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestRegisterInterfaceNoTag 无 tag 接口（如 Navigator）：toString 保持 [object Object]。
func TestRegisterInterfaceNoTag(t *testing.T) {
	ctx := newInterfaceTestContext(t)
	_, proto, err := RegisterInterface(ctx, WebInterface{Name: "Gadget"})
	if err != nil {
		t.Fatalf("RegisterInterface: %v", err)
	}
	instance := engine.NewObject()
	engine.SetProto(instance, proto)
	_ = ctx.Global().Set("__inst", instance)
	if got := ifaceEval(t, ctx, `Object.prototype.toString.call(globalThis.__inst)`); got != "[object Object]" {
		t.Errorf("toString: got %s, want [object Object]", got)
	}
}

// TestRegisterInterfaceBase 显式父原型（多级接口，如 Performance → EventTarget）。
func TestRegisterInterfaceBase(t *testing.T) {
	ctx := newInterfaceTestContext(t)
	_, baseProto, err := RegisterInterface(ctx, WebInterface{Name: "Emitter", Tag: "EventTarget"})
	if err != nil {
		t.Fatalf("RegisterInterface base: %v", err)
	}
	_, proto, err := RegisterInterface(ctx, WebInterface{Name: "Clock", Tag: "Performance", Base: baseProto})
	if err != nil {
		t.Fatalf("RegisterInterface derived: %v", err)
	}
	instance := engine.NewObject()
	engine.SetProto(instance, proto)
	_ = ctx.Global().Set("__inst", instance)

	cases := []struct {
		code string
		want string
	}{
		{`Object.getPrototypeOf(Clock.prototype) === Emitter.prototype`, "true"},
		{`Object.getPrototypeOf(Emitter.prototype) === Object.prototype`, "true"},
		{`globalThis.__inst instanceof Emitter`, "true"},
		{`Object.prototype.toString.call(globalThis.__inst)`, "[object Performance]"},
	}
	for _, tc := range cases {
		if got := ifaceEval(t, ctx, tc.code); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.code, got, tc.want)
		}
	}
}
