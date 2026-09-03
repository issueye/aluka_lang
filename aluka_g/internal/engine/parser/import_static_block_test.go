package parser

import "testing"

// TestImportBindingNameRejectsReservedWords 模块代码恒为严格模式，保留字
// 不可作 import 绑定名（与 Node 一致报 SyntaxError）。
func TestImportBindingNameRejectsReservedWords(t *testing.T) {
	cases := []struct{ name, src string }{
		{"as-if", `import { a as if } from 'm';`},
		{"as-let", `import { a as let } from 'm';`},
		{"as-await", `import { a as await } from 'm';`},
		{"as-default", `import { a as default } from 'm';`},
		{"as-class", `import { a as class } from 'm';`},
		{"default-import-class", `import class from 'm';`},
		{"namespace-as-if", `import * as if from 'm';`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseModule(c.src); err == nil {
				t.Fatalf("ParseModule(%q) = nil error, want syntax error", c.src)
			}
		})
	}
}

// TestImportBindingNameAllowsContextualKeywords 上下文关键字（of/async）
// 非保留字，可作绑定名；关键字/字符串作为导入名（imported）合法。
func TestImportBindingNameAllowsContextualKeywords(t *testing.T) {
	cases := []struct{ name, src string }{
		{"as-of", `import { a as of } from 'm';`},
		{"as-async", `import { a as async } from 'm';`},
		{"default-import-async", `import async from 'm';`},
		{"default-import-of", `import of from 'm';`},
		{"namespace-as-of", `import * as of from 'm';`},
		{"imported-keyword", `import { default as d } from 'm';`},
		{"imported-string", `import { "pkg-name" as pkg } from 'm';`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseModule(c.src); err != nil {
				t.Fatalf("ParseModule(%q) = %v, want nil", c.src, err)
			}
		})
	}
}

// TestStaticBlockArgumentsReject 规范：类静态初始化块内不允许出现
// arguments 引用（含箭头函数——沿用外层绑定）。
func TestStaticBlockArgumentsReject(t *testing.T) {
	cases := []struct{ name, src string }{
		{"direct", `class A { static { typeof arguments; } }`},
		{"indexed", `class A { static { console.log(arguments[0]); } }`},
		{"arrow", `class A { static { const f = () => arguments; } }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.src); err == nil {
				t.Fatalf("Parse(%q) = nil error, want syntax error", c.src)
			}
		})
	}
}

// TestStaticBlockArgumentsAllowed 非引用位置与嵌套普通函数（有独立
// arguments 绑定）不受限。
func TestStaticBlockArgumentsAllowed(t *testing.T) {
	cases := []struct{ name, src string }{
		{"nested-function", `class A { static { const f = function() { return arguments.length; }; f(); } }`},
		{"object-key", `class A { static { const o = { arguments: 1 }; } }`},
		{"member-key", `class A { static { const o = {}; o.arguments; } }`},
		{"method-name", `class A { static { class B { arguments() {} } } }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.src); err != nil {
				t.Fatalf("Parse(%q) = %v, want nil", c.src, err)
			}
		})
	}
}
