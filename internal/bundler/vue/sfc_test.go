package vue

import (
	"strings"
	"testing"
)

// compile 对表驱动用例执行 TransformSFC 并返回产物。
func compile(t *testing.T, sfc string) string {
	t.Helper()
	js, err := TransformSFC(sfc, "Test.vue")
	if err != nil {
		t.Fatalf("TransformSFC: %v", err)
	}
	return js
}

func TestTransformSFCTemplateAndScript(t *testing.T) {
	js := compile(t, `
<template>
  <div class="counter">
    <button class="btn" @click="dec">-</button>
    <span class="count">{{ count }}</span>
    <button class="btn" @click="inc()">+</button>
    <small class="doubled">x2 = {{ doubled }}</small>
    <img src="/logo.png">
  </div>
</template>

<script>
import { ref } from 'vue';
export default {
  setup() {
    const count = ref(0);
    return { count, doubled: count, inc: () => {}, dec: () => {} };
  },
};
</script>
`)
	for _, want := range []string{
		`import { h as _h, toDisplayString as _toDisplayString, unref as _unref } from 'vue';`, // Vite 风格 helper import
		`const __sfc__ = {`,          // export default 改写
		`import { ref } from 'vue';`, // script 原样保留
		`__sfc__.render = render;`,   // render 挂接（Vite 同款模式）
		`export default __sfc__;`,
		`export function render(_ctx){return [`, // render 导出
		`_h("div",{"class":"counter"}`,          // 静态属性（_h 构造 vnode）
		`"onClick":_ctx.dec`,                    // @click 标识符引用
		`"onClick":($event)=>(_ctx.inc())`,      // @click 调用表达式
		`_toDisplayString(_unref(_ctx.count))`,  // 插值经展示 helper
		`"x2 = "`,                               // 插值旁内联空格保留
		`_h("img",{"src":"/logo.png"}`,          // void 元素
	} {
		if !strings.Contains(js, want) {
			t.Errorf("output missing %q:\n%s", want, js)
		}
	}
}

func TestTransformSFCNoScript(t *testing.T) {
	js := compile(t, `<template><p>{{ text }}</p></template>`)
	if !strings.Contains(js, `const __sfc__ = { setup: (props) => props };`) {
		t.Errorf("missing props passthrough setup:\n%s", js)
	}
	if !strings.Contains(js, `_toDisplayString(_unref(_ctx.text))`) {
		t.Errorf("missing interpolation:\n%s", js)
	}
}

func TestTransformSFCBindAttr(t *testing.T) {
	js := compile(t, `<template><a :href="url" :data-id="item.id">{{ label }}</a></template>`)
	for _, want := range []string{`"href":_unref(_ctx.url)`, `"data-id":_unref(_ctx.item.id)`, `_toDisplayString(_unref(_ctx.label))`} {
		if !strings.Contains(js, want) {
			t.Errorf("output missing %q:\n%s", want, js)
		}
	}
}

func TestTransformSFCNestedAndSelfClose(t *testing.T) {
	js := compile(t, `<template><ul><li>{{ a }}</li><li><br/></li></ul></template>`)
	if !strings.Contains(js, `_h("ul"`) || !strings.Contains(js, `_h("li"`) || !strings.Contains(js, `_h("br",{},[])`) {
		t.Errorf("nested/self-close output wrong:\n%s", js)
	}
}

func TestTransformSFCErrors(t *testing.T) {
	cases := []struct {
		name string
		sfc  string
		want string
	}{
		{"missing template", `<script>export default {};</script>`, "missing <template>"},
		{"style block", "<template><p/></template>\n<style>.a{}</style>", "<style> is not supported"},
		{"script setup", `<template><p/></template><script setup>const a=1;</script>`, "<script setup> is not supported"},
		{"no default export", "<template><p/></template>\n<script>const a = 1;</script>", "must `export default`"},
		{"mismatched close", `<template><div></span></div></template>`, "mismatched closing"},
		{"missing close", `<template><div><p></div></template>`, "mismatched closing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := TransformSFC(c.sfc, "Test.vue")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("TransformSFC error = %v, want containing %q", err, c.want)
			}
		})
	}
}

func TestRewriteIdents(t *testing.T) {
	cases := []struct{ in, want string }{
		{"count", "_ctx.count"},
		{"count + 1", "_ctx.count + 1"},
		{"item.id", "_ctx.item.id"},
		{"Math.max(a, b)", "Math.max(_ctx.a, _ctx.b)"},
		{"typeof x", "typeof _ctx.x"},
		{"flag ? 'a' : 'b'", "_ctx.flag ? 'a' : 'b'"},
	}
	for _, c := range cases {
		if got := rewriteIdents(c.in); got != c.want {
			t.Errorf("rewriteIdents(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
