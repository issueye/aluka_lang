package emit

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// parseMod 解析模块源码为 Module。
func parseMod(t *testing.T, id, src string) Module {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	return Module{ID: id, Prog: prog}
}

// TestBundleDefinesReplaceExpressionsOnly 验证 define 只替换表达式，
// 不改写字符串字面量或成员属性名。
func TestBundleDefinesReplaceExpressionsOnly(t *testing.T) {
	bundle := Bundle{
		EntryID: "main",
		Modules: []Module{parseMod(t, "main", `
			export const prod = process.env.NODE_ENV === "production";
			export const options = __VUE_OPTIONS_API__;
			export const diagnostic = "__VUE_OPTIONS_API__";
			export const globalOption = globalThis.__VUE_OPTIONS_API__;
		`)},
		Defines: map[string]string{
			"process.env.NODE_ENV": `"production"`,
			"__VUE_OPTIONS_API__":  "true",
		},
	}

	out, err := bundle.Build()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "process.env.NODE_ENV") {
		t.Fatalf("member define was not replaced:\n%s", out)
	}
	for _, want := range []string{`const prod="production" === "production"`, "const options=true", "exports.options=options", `"__VUE_OPTIONS_API__"`, "globalThis.__VUE_OPTIONS_API__"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestBundleSmoke：多模块 bundle 产物在本引擎执行，import/export 语义正确。
func TestBundleSmoke(t *testing.T) {
	mainMod := parseMod(t, "main", `
				import { VERSION, add } from "./util";
				import Calculator from "./util";
				import * as data from "./data";
				export const result = add(1, 2);
				export const calc = new Calculator().double(result);
				export const version = VERSION;
				export const sum = data.total();
			`)
	mainMod.Resolved = map[string]string{"./util": "util", "./data": "data"}

	bundle := Bundle{
		EntryID: "main",
		Modules: []Module{
			parseMod(t, "util", `
				export const VERSION = "1.0";
				export function add(a, b) { return a + b }
				export default class Calculator { double(x) { return x * 2 } }
				const secret = "unused-internal";
			`),
			parseMod(t, "data", `
				export const items = [1, 2, 3];
				export function total() { return items.reduce((s, x) => s + x, 0) }
			`),
			mainMod,
		},
	}

	out, err := bundle.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 产物语法必须可回读
	if _, err := parser.Parse(out); err != nil {
		t.Fatalf("bundle 产物语法错误: %v\n产物: %s", err, out)
	}

	// 执行入口模块函数体（略去顶层 export——引擎以 CJS 风格消费产物校验语义）
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()

	shim := strings.ReplaceAll(out, "export var ", "globalThis.__exports_")
	shim = strings.ReplaceAll(shim, "export default", "globalThis.__exports_default =")
	// 去掉产物中 entry 的 ESM 导出行改造后的 globalThis 赋值（本引擎直接执行）
	if _, err := ctx.Eval(shim, "bundle.js"); err != nil {
		t.Fatalf("执行产物失败: %v\n产物: %s", err, out)
	}

	check := func(name, want string) {
		t.Helper()
		v, err := ctx.Global().Get("__exports_" + name)
		if err != nil {
			t.Fatalf("读取导出 %s: %v", name, err)
		}
		if got := v.String(); got != want {
			t.Errorf("导出 %s = %s, want %s", name, got, want)
		}
	}
	check("result", "3")
	check("calc", "6")
	check("version", "1.0")
	check("sum", "6")
}
