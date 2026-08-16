package vue

import (
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

// TestOfficialCompilerScriptSetup 验证官方后端完整处理 script setup，
// 并产出官方 helper/绑定元数据语义，而非 subset 的明确拒绝路径。
func TestOfficialCompilerScriptSetup(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	out, err := c.Transform(benchmarkSFC, "ScriptSetup.vue")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"__isScriptSetup", "createElementBlock", "__sfc__.render = render"} {
		if !strings.Contains(out, marker) {
			t.Errorf("official output missing %q:\n%s", marker, out)
		}
	}
}

// TestOfficialCompilerRewriteDefaultUsesOfficialHelper：script 的注释/字符串里含
// "export default" 时仍正确改写真正的默认导出（防文本 lastIndexOf 回归）。
func TestOfficialCompilerRewriteDefaultUsesOfficialHelper(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	src := `<template><div>{{ text }}</div></template>
<script>
const diagnostic = "export default in string";
// export default in comment
export default { setup(){ return { text: diagnostic } } };
</script>`
	out, err := c.Transform(src, "Rewrite.vue")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`const diagnostic = "export default in string"`, "const __sfc__ = {", "__sfc__.render = render", "export default __sfc__"} {
		if !strings.Contains(out, marker) {
			t.Errorf("official output missing %q:\n%s", marker, out)
		}
	}
}

// TestOfficialCompilerRejectsUnwiredBlocks 确保尚未接入 graph 资产管线的
// style/custom block 明确失败，不能静默丢失用户内容。
func TestOfficialCompilerRejectsUnwiredBlocks(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(t))
	for _, tc := range []struct {
		name, src, want string
	}{
		{"style", `<template><div/></template><style>.x{color:red}</style>`, "<style>"},
		{"custom", `<template><div/></template><docs>hello</docs>`, "custom SFC blocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Transform(tc.src, tc.name+".vue")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Transform error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// BenchmarkSubsetTransform 测 subset 纯 Go 后端的单 SFC 热路径。
func BenchmarkSubsetTransform(b *testing.B) {
	c := SubsetCompiler{}
	sfc := `<template><div>{{ count }}</div></template><script>export default { setup(){ return {count:1} } };</script>`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := c.Transform(sfc, "Bench.vue"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOfficialTransformWarm 测同一 VM/Loader 已加载 compiler-sfc 后的
// 热 Transform（排除一次性依赖链加载成本）。
func BenchmarkOfficialTransformWarm(b *testing.B) {
	vm, err := interpreter.NewVM()
	if err != nil {
		b.Fatal(err)
	}
	c := NewOfficialCompiler(vm, vueDemoEntry(b))
	if _, err := c.Transform(benchmarkSFC, "Warmup.vue"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Transform(benchmarkSFC, "Bench.vue"); err != nil {
			b.Fatal(err)
		}
	}
}
