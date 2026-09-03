package interpreter

import (
	"testing"
)

// === 差距补齐 P1：全局成员（gap-closure-plan §3 P1）======================

func TestMathHypot(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"基本", `JSON.stringify(Math.hypot(3, 4))`, "5"},
		{"空参", `JSON.stringify(Math.hypot())`, "0"},
		{"全零", `JSON.stringify(Math.hypot(0, -0))`, "0"},
		{"字符串强转", `JSON.stringify(Math.hypot(3, "4"))`, "5"},
		{"NaN 优先于 Infinity", `JSON.stringify(Math.hypot(Infinity, NaN))`, "null"}, // JSON.stringify(NaN) = "null"
		{"多参数", `JSON.stringify(Math.hypot(3, 4, 12))`, "13"},
		{"负值取平方", `JSON.stringify(Math.hypot(-3, 4))`, "5"},
		{"undefined → NaN", `JSON.stringify(Math.hypot(1, undefined))`, "null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Errorf("%s = %s, want %s", tc.code, got, tc.want)
			}
		})
	}
}

func TestJSONRawJSON(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"数字", `JSON.stringify(JSON.rawJSON(3))`, "3"},
		{"数字科学计数", `JSON.stringify(JSON.rawJSON(1e21))`, "1e+21"},
		{"字符串需为合法 JSON 文本", `JSON.stringify(JSON.rawJSON('"ab"'))`, `"ab"`},
		{"null", `JSON.stringify(JSON.rawJSON(null))`, "null"},
		{"true", `JSON.stringify(JSON.rawJSON(true))`, "true"},
		{"嵌套对象", `JSON.stringify({a: JSON.rawJSON(1e2), b: [JSON.rawJSON('"x"')]})`, `{"a":100,"b":["x"]}`},
		{"isRawJSON 识别", `JSON.isRawJSON(JSON.rawJSON(5))`, "true"},
		{"isRawJSON 普通值", `JSON.isRawJSON(1) + ' ' + JSON.isRawJSON({}) + ' ' + JSON.isRawJSON(null)`, "false false false"},
		{"isRawJSON 拷贝丢失 marker", `JSON.isRawJSON(Object.assign({}, JSON.rawJSON(1)))`, "false"},
		{"ownKeys 含 rawJSON", `JSON.stringify(Object.keys(JSON.rawJSON(7)))`, `["rawJSON"]`},
		{"rawJSON 属性只读", `const r = JSON.rawJSON(5); r.rawJSON = '9'; JSON.stringify(r)`, "5"},
		{"描述符", `JSON.stringify(JSON.stringify(Object.getOwnPropertyDescriptor(JSON.rawJSON(1), 'rawJSON')))`, `"{\"value\":\"1\",\"writable\":false,\"enumerable\":true,\"configurable\":false}"`},
		{"BigInt → '1' 合法 JSON（Node 22 行为）", `JSON.stringify(JSON.rawJSON(1n))`, "1"},
		{"undefined → SyntaxError", `(() => { try { JSON.rawJSON(undefined); return 'no'; } catch (e) { return e.constructor.name; } })()`, "SyntaxError"},
		{"NaN → SyntaxError", `(() => { try { JSON.rawJSON(NaN); return 'no'; } catch (e) { return e.constructor.name; } })()`, "SyntaxError"},
		{"对象 → SyntaxError", `(() => { try { JSON.rawJSON({}); return 'no'; } catch (e) { return e.constructor.name; } })()`, "SyntaxError"},
		{"非 JSON 字符串 → SyntaxError", `(() => { try { JSON.rawJSON('ab'); return 'no'; } catch (e) { return e.constructor.name; } })()`, "SyntaxError"},
		{"symbol → TypeError", `(() => { try { JSON.rawJSON(Symbol()); return 'no'; } catch (e) { return e.constructor.name; } })()`, "TypeError"},
		{"空参数 → TypeError", `(() => { try { JSON.rawJSON(); return 'no'; } catch (e) { return e.constructor.name; } })()`, "TypeError"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Errorf("%s = %s, want %s", tc.code, got, tc.want)
			}
		})
	}
}

func TestErrorCtorGap(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"EvalError 基础", `(() => { const e = new EvalError('msg'); return e.name + ' ' + e.message + ' ' + (e instanceof Error); })()`, "EvalError msg true"},
		{"URIError 基础", `(() => { const e = new URIError('m'); return e.name + ' ' + (e instanceof Error); })()`, "URIError true"},
		{"AggregateError errors+message", `(() => { const e = new AggregateError([new Error('a'), 'b'], 'msg'); return e.name + ' ' + e.message + ' ' + e.errors.length + ' ' + (e.errors[0] instanceof Error) + ' ' + e.errors[1]; })()`, "AggregateError msg 2 true b"},
		{"AggregateError 默认 message", `(() => { const e = new AggregateError([1]); return JSON.stringify(e.message) + ' ' + JSON.stringify(e.errors); })()`, `"" [1]`},
		{"AggregateError instanceof Error", `new AggregateError([1]) instanceof Error`, "true"},
		{"原型链", `Object.getPrototypeOf(TypeError.prototype) === Error.prototype`, "true"},
		{"全部 instanceof Error", `[TypeError, RangeError, SyntaxError, ReferenceError, EvalError, URIError, AggregateError].every(C => new C('x') instanceof Error)`, "true"},
		{"Error.prototype.constructor", `Error.prototype.constructor === Error`, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Errorf("%s = %s, want %s", tc.code, got, tc.want)
			}
		})
	}
}

func TestEscapeUnescape(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"escape 安全字符", `escape('a b@*_+-./')`, "a%20b@*_+-./"},
		{"escape 中文 → %uXXXX", `escape('\u4e2d\u6587')`, `%u4E2D%u6587`},
		{"escape ≤0xFF → %XX", `escape('\u00e9')`, "%E9"},
		{"escape 代理对", `escape('\u{1D11E}')`, `%uD834%uDD1E`},
		{"unescape %XX", `unescape('%20')`, " "},
		{"unescape %uXXXX", `unescape('%u4e2d')`, "中"},
		{"unescape 字面保持", `unescape('no%zz')`, "no%zz"},
		{"unescape 大小写", `unescape('%u4E2D%2f')`, "中/"},
		{"roundtrip", `unescape(escape('a b~\u4e2d'))`, "a b~中"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Errorf("%s = %s, want %s", tc.code, got, tc.want)
			}
		})
	}
}

func TestEvalGlobal(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"基本求值", `eval('1+2')`, "3"},
		{"非字符串原样返回", `JSON.stringify(eval(5)) + ' ' + JSON.stringify(eval({a:1}))`, `5 {"a":1}`},
		{"对象字面量求值", `eval('({a: 1, b: [1,2]})').b.length`, "2"},
		{"JSON.stringify 可用", `eval('JSON.stringify({x: 1})')`, `{"x":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Errorf("%s = %s, want %s", tc.code, got, tc.want)
			}
		})
	}
}

func TestBuiltinProtoChainToObjectProto(t *testing.T) {
	// 内置 prototype 链到 %Object.prototype%（ECMAScript 语义）：实例可访问
	// Object.prototype 方法（hasOwnProperty 等），此前仅 functionProto 接链。
	cases := []struct {
		name string
		code string
		want string
	}{
		{"数组", `typeof [].hasOwnProperty`, "function"},
		{"字符串", `typeof 'x'.hasOwnProperty`, "function"},
		{"数字", `typeof (5).hasOwnProperty`, "function"},
		{"布尔", `typeof (true).hasOwnProperty`, "function"},
		{"BigInt", `typeof (1n).hasOwnProperty`, "function"},
		{"Error", `typeof new Error().hasOwnProperty`, "function"},
		{"Date", `typeof new Date().hasOwnProperty`, "function"},
		{"TypedArray", `typeof new Uint8Array(1).hasOwnProperty`, "function"},
		{"ArrayBuffer", `typeof new ArrayBuffer(1).hasOwnProperty`, "function"},
		{"DataView", `typeof new DataView(new ArrayBuffer(1)).hasOwnProperty`, "function"},
		{"Array.prototype 父级", `Object.getPrototypeOf(Array.prototype) === Object.prototype`, "true"},
		{"String.prototype 父级", `Object.getPrototypeOf(String.prototype) === Object.prototype`, "true"},
		{"Error.prototype 父级", `Object.getPrototypeOf(Error.prototype) === Object.prototype`, "true"},
		{"Date.prototype 父级", `Object.getPrototypeOf(Date.prototype) === Object.prototype`, "true"},
		{"实例 instanceof Object", `[new Error() instanceof Object, new Date() instanceof Object, [1] instanceof Object].join(' ')`, "true true true"},
		{"Object.prototype 链终点", `Object.getPrototypeOf(Object.prototype) === null`, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Errorf("%s = %s, want %s", tc.code, got, tc.want)
			}
		})
	}
}
