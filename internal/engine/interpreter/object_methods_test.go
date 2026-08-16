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

// TestVMDefinePropertyDescriptorSemantics：属性描述符规范子集矩阵
// （engine.DefineOwnProperty——部分描述符合并/标志执行/校验）。
// 案例期望值与 Node 22 行为对齐（SameValue 窄实现除外）。
func TestVMDefinePropertyDescriptorSemantics(t *testing.T) {
	cases := []struct{ name, code, want string }{
		// 部分描述符保留现值：postcss _createClass 冻结类原型的真实形态
		//（vue-sfc 探针的原始阻塞点）。
		{"partial-writable-preserves-value", `
			function N(){}; N.prototype.w = 1;
			Object.defineProperty(N, 'prototype', {writable: false});
			typeof N.prototype`, "object"},
		// 新属性未指定字段缺省 false（不可枚举/不可写/不可配置）。
		{"new-partial-defaults-false", `
			var o = {}; Object.defineProperty(o, 'x', {value: 1});
			JSON.stringify([o.x, Object.keys(o).length, Object.getOwnPropertyNames(o).length])`, "[1,0,1]"},
		// writable:false 拦截赋值（sloppy 静默）。
		{"write-blocked", `
			var o = {}; Object.defineProperty(o, 'x', {value: 1, writable: false});
			o.x = 99; o.x`, "1"},
		// configurable:false 拦截 delete。
		{"delete-blocked", `
			var o = {}; Object.defineProperty(o, 'x', {value: 1, configurable: false});
			JSON.stringify([delete o.x, o.x])`, "[false,1]"},
		// accessor 与 data 字段互斥 → TypeError。
		{"accessor-value-mix-throws", `
			try { Object.defineProperty({}, 'x', {value: 1, get(){ return 2; }}); 'no-throw' }
			catch (e) { 'threw' }`, "threw"},
		// 非可配置：等值重定义允许，变值抛 TypeError。
		{"nonconfigurable-redefine", `
			var o = {}; Object.defineProperty(o, 'x', {value: 1, configurable: false});
			Object.defineProperty(o, 'x', {value: 1});
			try { Object.defineProperty(o, 'x', {value: 2}); 'no-throw' } catch (e) { 'threw' }`, "threw"},
		// gOPD 反映生效标志。
		{"gopd-reflects-flags", `
			var o = {a: 1};
			Object.defineProperty(o, 'b', {value: 2});
			var da = Object.getOwnPropertyDescriptor(o, 'a');
			var db = Object.getOwnPropertyDescriptor(o, 'b');
			JSON.stringify([da.writable, da.enumerable, db.writable, db.enumerable, db.configurable])`,
			"[true,true,false,false,false]"},
		// accessor → data 转换（configurable 时允许）。
		{"accessor-to-data", `
			var o = {};
			Object.defineProperty(o, 'x', {get(){ return 1; }, configurable: true});
			Object.defineProperty(o, 'x', {value: 5, writable: true, enumerable: true, configurable: true});
			o.x`, "5"},
		// writable true→false 收紧（configurable 属性允许）。
		{"writable-narrow", `
			var o = {x: 1};
			Object.defineProperty(o, 'x', {writable: false});
			o.x = 2; o.x`, "1"},
		// 函数对象（Closure 包装）上的访问器经 UnwrapObject 落到真实存储。
		{"function-object-accessor", `
			var f = function(){};
			Object.defineProperty(f, 'g', {get(){ return 42; }});
			f.g`, "42"},
		// defineProperties 批量（数据 + 访问器混布）。
		{"defineProperties-batch", `
			var o = Object.defineProperties({}, {
				a: {value: 1, enumerable: true},
				b: {get(){ return 2; }, enumerable: true}
			});
			JSON.stringify([o.a, o.b, Object.keys(o)])`, `[1,2,["a","b"]]`},
		// Object.create 第二参数走同一描述符语义。
		{"create-descriptors", `
			var o = Object.create(null, {x: {value: 10, enumerable: true}});
			JSON.stringify([o.x, Object.keys(o)])`, `[10,["x"]]`},
		// setter 经赋值触发。
		{"setter-invoked", `
			var o = {}, v = 0;
			Object.defineProperty(o, 'x', {set(n){ v = n; }});
			o.x = 7; v`, "7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := vmEvalStr(t, c.code)
			if got != c.want {
				t.Errorf("%s:\n  got  %q\n  want %q", c.name, got, c.want)
			}
		})
	}
}
