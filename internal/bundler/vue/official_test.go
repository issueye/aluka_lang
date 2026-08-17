package vue

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

const benchmarkSFC = `<template><div class="counter">{{ count }} <button @click="inc">+</button></div></template>
<script setup>
import { ref } from 'vue'
const count = ref(0)
function inc() { count.value++ }
</script>`

func vueDemoEntry(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	entry := filepath.Join(root, "demo", "web-bundle-vue-demo", "main.ts")
	if _, err := os.Stat(filepath.Join(filepath.Dir(entry), "node_modules", "vue", "package.json")); err != nil {
		t.Skipf("vendored Vue fixture unavailable: %v", err)
	}
	return entry
}

func compileText(t testing.TB, c Compiler, src, name string) (*CompileResult, string) {
	t.Helper()
	result, err := compileNamed(c, src, name)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var out strings.Builder
	out.WriteString(result.Facade)
	for _, generated := range result.Modules {
		out.WriteByte('\n')
		out.WriteString(generated.Source)
	}
	return result, out.String()
}

// TestOfficialCompilerScriptSetup 验证官方后端完整处理 script setup，并产出
// 独立 facade/script/template 模块。
func TestOfficialCompilerScriptSetup(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	result, out := compileText(t, c, benchmarkSFC, "ScriptSetup.vue")
	for _, marker := range []string{"__isScriptSetup", "createElementBlock", "__sfc__.render = __sfc_render__"} {
		if !strings.Contains(out, marker) {
			t.Errorf("official output missing %q:\n%s", marker, out)
		}
	}
	if len(result.Modules) != 2 {
		t.Fatalf("generated modules = %d, want script + template", len(result.Modules))
	}
}

// TestOfficialCompilerIsolatesScopesAndPreservesTS 回归 script/template 同名绑定
// 冲突，并验证 lang=ts 通过生成模块扩展名进入 TS 前端，不再调用 rewriteDefault。
func TestOfficialCompilerIsolatesScopesAndPreservesTS(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	src := `<template><div>{{ render }}</div></template>
<script lang="ts">
import { ref as render } from "vue";
const diagnostic: string = "export default in string";
// export default in comment
export default { setup(): { render: typeof render } { return { render }; } };
</script>`
	result, out := compileText(t, c, src, "Scopes.vue")
	if len(result.Modules) != 2 {
		t.Fatalf("generated modules = %d, want 2", len(result.Modules))
	}
	if !strings.HasSuffix(result.Modules[0].Name, ".ts") {
		t.Fatalf("script module = %q, want .ts suffix", result.Modules[0].Name)
	}
	for _, marker := range []string{"const diagnostic: string", "function render", "export default __sfc__"} {
		if !strings.Contains(out, marker) {
			t.Errorf("official output missing %q:\n%s", marker, out)
		}
	}
	if strings.Contains(result.Facade, "const diagnostic") || strings.Contains(result.Modules[0].Source, "function render(") {
		t.Fatalf("script/template scopes were merged:\nfacade=%s\nscript=%s", result.Facade, result.Modules[0].Source)
	}
}

// TestOfficialCompilerPreservesScriptNamedExports 回归官方后端 facade 丢失
// 普通 script 命名导出，并覆盖 script-only/template-only 的默认导出与 render 挂载。
func TestOfficialCompilerPreservesScriptNamedExports(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	for _, tc := range []struct {
		name          string
		src           string
		moduleCount   int
		wantNamed     bool
		wantRender    bool
		wantNamedCode string
	}{
		{
			name: "script-and-template",
			src: `<script>
export const answer = 42;
export default { name: "Answer" };
</script>
<template><div>{{ answer }}</div></template>`,
			moduleCount:   2,
			wantNamed:     true,
			wantRender:    true,
			wantNamedCode: "export const answer = 42",
		},
		{
			name: "script-only",
			src: `<script>
export const answer = 42;
export default { name: "Answer" };
</script>`,
			moduleCount:   1,
			wantNamed:     true,
			wantNamedCode: "export const answer = 42",
		},
		{
			name:        "template-only",
			src:         `<template><div>answer</div></template>`,
			moduleCount: 2,
			wantRender:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, out := compileText(t, c, tc.src, "Named.vue")
			if len(result.Modules) != tc.moduleCount {
				t.Fatalf("generated modules = %d, want %d", len(result.Modules), tc.moduleCount)
			}
			if !strings.Contains(result.Facade, `export * from "./Named.vue.__aluka_script.js";`) {
				t.Fatalf("facade does not forward script exports:\n%s", result.Facade)
			}
			if strings.Count(result.Facade, "export default __sfc__;") != 1 || strings.Contains(result.Facade, "export { default") {
				t.Fatalf("facade has duplicate/default re-export:\n%s", result.Facade)
			}
			if got := strings.Contains(result.Facade, "__sfc__.render = __sfc_render__"); got != tc.wantRender {
				t.Fatalf("facade render attachment = %v, want %v:\n%s", got, tc.wantRender, result.Facade)
			}
			if strings.Count(result.Modules[0].Source, "export default __sfc__;") != 1 {
				t.Fatalf("generated script default export count = %d, want 1:\n%s", strings.Count(result.Modules[0].Source, "export default __sfc__;"), result.Modules[0].Source)
			}
			if tc.wantNamed && !strings.Contains(result.Modules[0].Source, tc.wantNamedCode) {
				t.Fatalf("generated script lost named export:\n%s", out)
			}
		})
	}
}

// TestOfficialCompilerRejectsUnwiredBlocks 确保未接入 graph 的内容明确失败。
func TestOfficialCompilerRejectsUnwiredBlocks(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	for _, tc := range []struct {
		name, src, want string
	}{
		{"custom", `<template><div/></template><docs>hello</docs>`, "custom SFC blocks"},
		{"style-scss", `<template><div/></template><style lang="scss">.x{}</style>`, "lang="},
		{"style-module", `<template><div/></template><style module>.x{}</style>`, "<style module>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileNamed(c, tc.src, tc.name+".vue")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compile error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestOfficialCompilerStyleAndSrc(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "ext.css")
	if err := os.WriteFile(cssPath, []byte(".ext{color:green}"), 0o644); err != nil {
		t.Fatal(err)
	}
	vuePath := filepath.Join(dir, "Box.vue")
	src := `<template><div class="box">hi</div></template>
<style scoped>.box{color:red}</style>
<style src="./ext.css"></style>
<script>export default { name: "Box" }</script>`
	result, err := c.Compile(CompileRequest{Source: src, Name: "Box.vue", Filename: vuePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Styles) != 2 {
		t.Fatalf("styles = %d, want 2", len(result.Styles))
	}
	id := sfcScopeID("Box.vue")
	if !strings.Contains(result.Facade, `__sfc__.__scopeId = "data-v-`+id+`"`) {
		t.Fatalf("facade missing __scopeId:\n%s", result.Facade)
	}
	if strings.Contains(result.Styles[0].Source, "{ source:") {
		t.Fatalf("scoped style dumped postcss AST: %s", result.Styles[0].Source)
	}
	if !strings.Contains(result.Styles[0].Source, ".box[data-v-"+id+"]") {
		t.Fatalf("scoped css = %q, want .box[data-v-%s]", result.Styles[0].Source, id)
	}
	if !strings.Contains(result.Styles[1].Source, ".ext") {
		t.Fatalf("src style missing .ext: %s", result.Styles[1].Source)
	}
	if len(result.ExtraFiles) != 1 {
		t.Fatalf("ExtraFiles = %v", result.ExtraFiles)
	}
}

func TestOfficialCompilerStructuredDiagnostic(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	_, err = compileNamed(c, `<template>
  <div>{{ total + }}</div>
</template>`, "Broken.vue")
	var diagnostic *Diagnostic
	if !errors.As(err, &diagnostic) {
		t.Fatalf("Compile error = %T %v, want *Diagnostic", err, err)
	}
	if diagnostic.Filename != "Broken.vue" || diagnostic.Line != 2 || diagnostic.Column <= 0 {
		t.Fatalf("diagnostic = %+v, want Broken.vue line 2 with column", diagnostic)
	}

	_, err = compileNamed(c, `<template><div/>
</template>
<script setup lang="ts">
const value: = 1
</script>`, "BrokenScript.vue")
	if !errors.As(err, &diagnostic) || diagnostic.Filename != "BrokenScript.vue" || diagnostic.Line != 4 || diagnostic.Column <= 0 {
		t.Fatalf("script diagnostic = %T %+v, want BrokenScript.vue line 4 with column", err, diagnostic)
	}

	// 相邻的合法表达式必须成功，防止测试被过宽的错误匹配误判为通过。
	if _, err := compileNamed(c, `<template>
  <div>{{ total + 1 }}</div>
</template>`, "Valid.vue"); err != nil {
		t.Fatalf("valid neighboring expression rejected: %v", err)
	}
}

// BenchmarkSubsetTransform 测 subset 纯 Go 后端的单 SFC 热路径。
func BenchmarkSubsetTransform(b *testing.B) {
	c := SubsetCompiler{}
	sfc := `<template><div>{{ count }}</div></template><script>export default { setup(){ return {count:1} } };</script>`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := compileNamed(c, sfc, "Bench.vue"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOfficialTransformWarm 测同一 VM/Loader 已加载 compiler-sfc 后的
// 热 Compile（排除一次性依赖链加载成本）。
func BenchmarkOfficialTransformWarm(b *testing.B) {
	vm, err := interpreter.NewVM()
	if err != nil {
		b.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(b))
	if _, err := compileNamed(c, benchmarkSFC, "Warmup.vue"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := compileNamed(c, benchmarkSFC, "Bench.vue"); err != nil {
			b.Fatal(err)
		}
	}
}
