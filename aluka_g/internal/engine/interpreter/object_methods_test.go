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

func TestVMPostImplementationDescriptorReviewRegressions(t *testing.T) {
	cases := []struct{ name, code, want string }{
		{"array-holes-delete-and-prototype", `
			var a = [undefined, 2]; Reflect.deleteProperty(a,"1"); Array.prototype[1] = 9;
			var out = [0 in a, 1 in a, a[1], Object.keys(a).join(","), Object.hasOwn(a, "0"), Object.hasOwn(a, "1")];
			delete Array.prototype[1]; JSON.stringify(out)`, `[true,true,9,"0",true,false]`},
		{"array-index-boundary", `
			var a = []; a[4294967295] = 7;
			JSON.stringify([a.length, a[4294967295], Object.hasOwn(a,"4294967295")])`, `[0,7,true]`},
		{"fractional-length-rejected", `
			var a=[1,2], s=[];
			try { a.length=1.5; } catch(e) { s.push("set"); }
			try { Object.defineProperty(a,"length",{value:1.5}); } catch(e) { s.push("define"); }
			JSON.stringify([a.length,s])`, `[2,["set","define"]]`},
		{"shrink-nonconfigurable-atomic", `
			var a=[1,2,3]; Object.defineProperty(a,"2",{configurable:false});
			var reflected=Reflect.defineProperty(a,"length",{value:1});
			JSON.stringify([reflected,a.length,a[2]])`, `[false,3,3]`},
		{"array-index-accessor-atomic", `
			var a=[], calls=0; Object.defineProperty(a,"2",{get(){calls++; return 7}, configurable:true});
			JSON.stringify([a.length,a[2],calls,Object.hasOwn(a,"0"),Object.hasOwn(a,"2")])`, `[3,7,1,false,true]`},
		{"descriptor-getter-and-has-errors", `
			var calls=0, d={get value(){calls++; return 8}}; var o={}; Object.defineProperty(o,"x",d);
			var p=new Proxy({}, {has(){throw "has"}}), threw=false;
			try { Object.defineProperty({},"y",p); } catch(e) { threw=e==="has"; }
			JSON.stringify([o.x,calls,threw])`, `[8,1,true]`},
		{"accessor-data-resets-writable", `
			var o={}; Object.defineProperty(o,"x",{get(){return 1},configurable:true});
			Object.defineProperty(o,"x",{value:2}); o.x=3;
			JSON.stringify([o.x,Object.getOwnPropertyDescriptor(o,"x").writable])`, `[2,false]`},
		{"reflect-ordinary-rejection", `
			var o={}; Object.defineProperty(o,"x",{value:1,configurable:false});
			Reflect.defineProperty(o,"x",{value:2})`, `false`},
		{"nonextensible-prototype", `
			var o={}, p={}; Object.preventExtensions(o);
			JSON.stringify([Reflect.setPrototypeOf(o,p),Object.getPrototypeOf(o)===Object.prototype])`, `[false,true]`},
		{"proxy-invariants-symbols", `
			var s=Symbol("s"), target={}; Object.defineProperty(target,s,{value:1,configurable:false});
			var p=new Proxy(target,{ownKeys(){return []}}), threw=false;
			try { Reflect.ownKeys(p); } catch(e) { threw=true; }
			var q=new Proxy(target,{ownKeys(){return [s]}});
			JSON.stringify([threw,Reflect.ownKeys(q)[0]===s])`, `[true,true]`},
		{"proxy-getown-set-extensibility", `
			var t={}; Object.defineProperty(t,"x",{value:1,writable:false,configurable:false}); Object.preventExtensions(t);
			var g=new Proxy(t,{getOwnPropertyDescriptor(){return undefined}}), s=new Proxy(t,{set(){return true}}), e=new Proxy(t,{isExtensible(){return true}});
			var out=[]; for (var p of [g,s,e]) { try { p===s ? Reflect.set(p,"x",2) : p===e ? Reflect.isExtensible(p) : Object.hasOwn(p,"x"); out.push(false); } catch(err) { out.push(true); } }
			JSON.stringify(out)`, `[true,true,true]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVMPropertyDescriptorReviewedRegressions(t *testing.T) {
	cases := []struct{ name, code, want string }{
		{"array-index-and-length", `
			var a = [1, 2];
			Object.defineProperty(a, "0", {value: 7, writable: false, enumerable: false, configurable: false});
			Object.defineProperty(a, "length", {writable: false});
			a[0] = 9; a[2] = 3;
			var d0 = Object.getOwnPropertyDescriptor(a, "0");
			var dl = Object.getOwnPropertyDescriptor(a, "length");
			JSON.stringify([a[0], a.length, Object.keys(a), d0.writable, d0.enumerable, dl.enumerable, dl.configurable])`,
			`[7,2,["1"],false,false,false,false]`},
		{"function-setter", `
			var f = function(){}, seen = 0;
			Object.defineProperty(f, "x", {set(v){ seen = v; }});
			f.x = 8; seen`, "8"},
		{"map-regexp-backing", `
			var m = new Map(), r = /x/;
			Object.defineProperty(m, "hidden", {value: 1});
			Object.defineProperty(r, "hidden", {value: 2});
			JSON.stringify([m.hidden, r.hidden, Object.hasOwn(m, "hidden"), Object.hasOwn(r, "hidden"), Object.keys(m).length])`,
			`[1,2,true,true,0]`},
		{"proxy-descriptor-traps", `
			var log = [], target = {};
			var p = new Proxy(target, {
				defineProperty(t, k, d) { log.push("d:" + k + ":" + d.value); return Reflect.defineProperty(t, k, d); },
				getOwnPropertyDescriptor(t, k) { log.push("g:" + k); return Reflect.getOwnPropertyDescriptor(t, k); }
			});
			var ok = Reflect.defineProperty(p, "x", {value: 4, enumerable: true, configurable: true});
			var d = Object.getOwnPropertyDescriptor(p, "x");
			JSON.stringify([ok, d.value, log])`,
			`[true,4,["d:x:4","g:x"]]`},
		{"proxy-define-false", `
			var p = new Proxy({}, {defineProperty(){ return false; }});
			var reflect = Reflect.defineProperty(p, "x", {value: 1});
			var object; try { Object.defineProperty(p, "x", {value: 1}); object = "no"; } catch (e) { object = "throw"; }
			reflect + ":" + object`, "false:throw"},
		{"reflect-reuses-semantics", `
			var o = {};
			Reflect.defineProperty(o, "x", {value: 3});
			var d = Reflect.getOwnPropertyDescriptor(o, "x");
			JSON.stringify([d.value, d.writable, d.enumerable, d.configurable, Object.hasOwn(o, "x")])`,
			`[3,false,false,false,true]`},
		{"to-property-descriptor-inherited-hidden", `
			var proto = {};
			Object.defineProperty(proto, "enumerable", {value: true});
			var d = Object.create(proto); d.value = 5;
			var o = {}; Object.defineProperty(o, "x", d);
			JSON.stringify([o.x, Object.keys(o)])`, `[5,["x"]]`},
		{"nonconfigurable-writable-narrow", `
			var o = {};
			Object.defineProperty(o, "x", {value: 1, writable: true, configurable: false});
			Object.defineProperty(o, "x", {writable: false});
			o.x = 2; Object.getOwnPropertyDescriptor(o, "x").writable + ":" + o.x`, "false:1"},
		{"same-value-signed-zero", `
			var o = {}; Object.defineProperty(o, "x", {value: 0, writable: false, configurable: false});
			try { Object.defineProperty(o, "x", {value: -0}); "no" } catch (e) { "throw" }`, "throw"},
		{"symbol-name-split", `
			var s = Symbol("k"), o = {plain: 1}; Object.defineProperty(o, s, {value: 2});
			var names = Object.getOwnPropertyNames(o), syms = Object.getOwnPropertySymbols(o), own = Reflect.ownKeys(o);
			JSON.stringify([names, syms.length, syms[0] === s, own.length, own[1] === s])`,
			`[["plain"],1,true,2,true]`},
		{"integrity-levels", `
			var o = {x: 1}; Object.freeze(o); o.x = 2; o.y = 3;
			var s = {x: 1}; Object.seal(s); delete s.x; s.x = 4;
			JSON.stringify([o.x, Object.hasOwn(o,"y"), Object.isFrozen(o), Reflect.isExtensible(o), s.x, Object.isSealed(s)])`,
			`[1,false,true,false,4,true]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
