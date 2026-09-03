package interpreter

import "testing"

// TestStructuredClone 回归测试（P1-4）：全局 structuredClone 深拷贝语义。
func TestStructuredClone(t *testing.T) {
	code := `
var a = { x: 1, y: [1, 2, { z: 3 }], d: new Date(0), m: new Map([['k', 'v']]), s: new Set([1, 2]) };
var b = structuredClone(a);
var deep = b.y[2].z === 3 && b.d.getTime() === 0 && b.m.get('k') === 'v' && b.s.has(2);
var indep = b.y !== a.y;
b.y.push(99);
var after = a.y.length === 3 && b.y.length === 4;
deep + "|" + indep + "|" + after
`
	if got := vmEvalStr(t, code); got != "true|true|true" {
		t.Errorf("structuredClone = %q, want true|true|true", got)
	}
}

// TestStructuredCloneCycle 循环引用克隆。
func TestStructuredCloneCycle(t *testing.T) {
	code := `
var c = { self: null };
c.self = c;
var d = structuredClone(c);
d.self === d
`
	if got := vmEvalStr(t, code); got != "true" {
		t.Errorf("cycle clone = %q, want true", got)
	}
}

// TestStructuredClonePrimitives 原始值直接返回。
func TestStructuredClonePrimitives(t *testing.T) {
	code := `
var out = [];
out.push(structuredClone(42));
out.push(structuredClone('hi'));
out.push(structuredClone(null) === null);
out.join('|')
`
	if got := vmEvalStr(t, code); got != "42|hi|true" {
		t.Errorf("primitives = %q, want 42|hi|true", got)
	}
}
