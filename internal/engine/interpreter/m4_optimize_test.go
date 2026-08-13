package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// M4 后续：setProperty 写入 IC 前置（SetCached 提到 FindAccessor 之前）。
// 验证 own 数据属性直写、accessor setter 仍被正确拦截调用的语义。

func TestVMSetPropertyICFront(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"plain prop write", "var o={x:0}; o.x=5; o.x", "5"},
		{"loop prop write", "var o={x:0}; for(var i=0;i<3;i++){o.x=i;} o.x", "2"},
		{"overwrite", "var o={x:1}; o.x=2; o.x=3; o.x", "3"},
		{"delete then rewrite", "var o={x:1}; delete o.x; o.x=9; o.x", "9"},
		{"new prop after literal", "var o={}; o.k='v'; o.k", "v"},
		{"getter setter", "var o={_x:0, get x(){return this._x;}, set x(v){this._x=v;}}; o.x=7; o.x", "7"},
		{"setter invokes", "var n=0; var o={set x(v){n=v;}}; o.x=42; n", "42"},
		{"prototype setter", "var p={set x(v){this._v=v*2;}}; var o=Object.create(p); o.x=5; o._v", "10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vmEvalStr(t, c.code); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// BenchmarkPropWrite 量化写入 IC 前置的字节码层收益（jit.Off 隔离）。
func BenchmarkPropWrite(b *testing.B) {
	vm, err := NewVM()
	if err != nil {
		b.Fatal(err)
	}
	vm.ConfigureJIT(jit.Config{Mode: jit.Off})
	code := `var o = {a:0,b:0,c:0,d:0,e:0}; for (var i = 0; i < 200000; i++) { o.a = i; o.b = i; o.c = i; o.d = i; o.e = i; } o.e`
	if _, err := vm.Eval(code, "bench.js"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Eval(code, "bench.js"); err != nil {
			b.Fatal(err)
		}
	}
}
