package interpreter

import "testing"

// 本文件覆盖 Object 构造器静态方法的补全实现（含 ES2019 fromEntries、ES2022 hasOwn）。
// 风格对齐 vm_test.go：使用 vmEvalStr 辅助函数 + 表格驱动。

func TestVMObjectCreate(t *testing.T) {
	// 以指定原型创建
	got := vmEvalStr(t, `var p={greet(){return "hi"} }; var o=Object.create(p); o.greet()`)
	if got != "hi" {
		t.Errorf("create+proto = %q, want hi", got)
	}
	// 原型为 null
	got = vmEvalStr(t, `Object.create(null) instanceof Object`)
	if got != "false" {
		t.Errorf("create(null) proto = %q, want false", got)
	}
	// 带属性描述符
	got = vmEvalStr(t, `var o=Object.create(null, {x:{value:10}}); o.x`)
	if got != "10" {
		t.Errorf("create with props = %q, want 10", got)
	}
}

func TestVMObjectDefineProperty(t *testing.T) {
	got := vmEvalStr(t, `var o={}; Object.defineProperty(o, "x", {value:42}); o.x`)
	if got != "42" {
		t.Errorf("defineProperty value = %q, want 42", got)
	}
	// 返回原对象
	got = vmEvalStr(t, `var o={}; Object.defineProperty(o, "x", {value:1}) === o`)
	if got != "true" {
		t.Errorf("defineProperty returns obj = %q, want true", got)
	}
	// defineProperties 批量
	got = vmEvalStr(t, `var o={}; Object.defineProperties(o, {a:{value:1}, b:{value:2}}); o.a + ":" + o.b`)
	if got != "1:2" {
		t.Errorf("defineProperties = %q, want 1:2", got)
	}
}

func TestVMObjectGetOwnPropertyDescriptor(t *testing.T) {
	// 描述符结构
	got := vmEvalStr(t, `var d=Object.getOwnPropertyDescriptor({x:1}, "x"); d.value`)
	if got != "1" {
		t.Errorf("descriptor.value = %q, want 1", got)
	}
	got = vmEvalStr(t, `Object.getOwnPropertyDescriptor({x:1}, "x").writable`)
	if got != "true" {
		t.Errorf("descriptor.writable = %q, want true", got)
	}
	// 不存在的属性返回 undefined
	got = vmEvalStr(t, `Object.getOwnPropertyDescriptor({x:1}, "y")`)
	if got != "undefined" {
		t.Errorf("nonexistent descriptor = %q, want undefined", got)
	}
	// getOwnPropertyDescriptors（复数）
	got = vmEvalStr(t, `Object.getOwnPropertyDescriptor({a:1}, "missing")`)
	if got != "undefined" {
		t.Errorf("descriptors missing = %q, want undefined", got)
	}
	got = vmEvalStr(t, `var ds=Object.getOwnPropertyDescriptors({a:1, b:2}); ds.a.value + ":" + ds.b.value`)
	if got != "1:2" {
		t.Errorf("descriptors values = %q, want 1:2", got)
	}
}

func TestVMObjectGetOwnPropertyNames(t *testing.T) {
	got := vmEvalStr(t, `Object.getOwnPropertyNames({a:1, b:2}).sort().join(",")`)
	if got != "a,b" {
		t.Errorf("getOwnPropertyNames = %q, want a,b", got)
	}
	// getOwnPropertySymbols 返回空数组
	got = vmEvalStr(t, `Object.getOwnPropertySymbols({a:1}).length`)
	if got != "0" {
		t.Errorf("getOwnPropertySymbols length = %q, want 0", got)
	}
}

func TestVMObjectIs(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`Object.is(1, 1)`, "true"},
		{`Object.is("a", "a")`, "true"},
		{`Object.is(NaN, NaN)`, "true"}, // 与 === 不同
		{`Object.is(0, -0)`, "false"},   // 与 === 不同
		{`Object.is(+0, 0)`, "true"},
		{`Object.is(1, "1")`, "false"},
		{`Object.is(null, null)`, "true"},
		{`Object.is(undefined, undefined)`, "true"},
		// 对照 === 的差异
		{`NaN === NaN`, "false"},
		{`0 === -0`, "true"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMObjectFromEntries(t *testing.T) {
	// 数组输入
	got := vmEvalStr(t, `Object.fromEntries([["a",1],["b",2]]).a`)
	if got != "1" {
		t.Errorf("fromEntries array = %q, want 1", got)
	}
	got = vmEvalStr(t, `Object.fromEntries([["a",1],["b",2]]).b`)
	if got != "2" {
		t.Errorf("fromEntries array b = %q, want 2", got)
	}
	// Map 输入
	got = vmEvalStr(t, `Object.fromEntries(new Map([["x", 10], ["y", 20]])).x`)
	if got != "10" {
		t.Errorf("fromEntries Map = %q, want 10", got)
	}
	// 生成器输入（迭代器协议）
	got = vmEvalStr(t, `function* g(){ yield ["k","v"]; } Object.fromEntries(g()).k`)
	if got != "v" {
		t.Errorf("fromEntries generator = %q, want v", got)
	}
}

func TestVMObjectHasOwn(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`Object.hasOwn({a:1}, "a")`, "true"},
		{`Object.hasOwn({a:1}, "b")`, "false"},
		// 不走原型链
		{`Object.hasOwn(Object.create({inherited: 1}), "inherited")`, "false"},
		{`Object.hasOwn({}, "toString")`, "false"}, // 来自 Object.prototype
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestVMObjectSetPrototypeOf(t *testing.T) {
	got := vmEvalStr(t, `var o={}; Object.setPrototypeOf(o, {x:99}); o.x`)
	if got != "99" {
		t.Errorf("setPrototypeOf = %q, want 99", got)
	}
	// 确认 getPrototypeOf 能读回
	got = vmEvalStr(t, `var proto={tag:"P"}; var o=Object.create(proto); Object.getPrototypeOf(o) === proto`)
	if got != "true" {
		t.Errorf("proto identity = %q, want true", got)
	}
	// 置为 null
	got = vmEvalStr(t, `var o={}; Object.setPrototypeOf(o, null); Object.getPrototypeOf(o)`)
	if got != "null" {
		t.Errorf("null proto = %q, want null", got)
	}
}
