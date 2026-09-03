package interpreter

import (
	"testing"
)

// TestVue3Compiler 验证 Vue 3 SFC 解析与模板编译（Compiler）在 Aluka 引擎下的运行能力
func TestVue3Compiler(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "parse SFC blocks",
			code: `
				function parseSFC(source) {
					const sfc = {
						template: null,
						script: null,
						styles: []
					};

					const templateMatch = source.match(/<template>([\s\S]*?)<\/template>/i);
					if (templateMatch) {
						sfc.template = { content: templateMatch[1].trim() };
					}

					const scriptMatch = source.match(/<script(?:\s+([^>]*))?>([\s\S]*?)<\/script>/i);
					if (scriptMatch) {
						sfc.script = {
							setup: (scriptMatch[1] || '').includes('setup'),
							content: scriptMatch[2].trim()
						};
					}

					const styleMatches = source.matchAll(/<style(?:\s+([^>]*))?>([\s\S]*?)<\/style>/gi);
					for (const m of styleMatches) {
						sfc.styles.push({
							scoped: (m[1] || '').includes('scoped'),
							content: m[2].trim()
						});
					}

					return sfc;
				}

				const sfcSource = ` + "`" + `
<template>
  <div class="user-card">
    <h2>{{ user.name }}</h2>
    <p>Age: {{ user.age }}</p>
  </div>
</template>

<script setup>
import { ref } from 'vue';
const user = ref({ name: 'Aluka', age: 1 });
</script>

<style scoped>
.user-card { color: green; }
</style>
` + "`" + `;

				const descriptor = parseSFC(sfcSource);
				const hasTemplate = !!descriptor.template;
				const isScriptSetup = !!(descriptor.script && descriptor.script.setup);
				const styleScoped = descriptor.styles.length > 0 && descriptor.styles[0].scoped;

				return hasTemplate + "," + isScriptSetup + "," + styleScoped;
			`,
			want: "true,true,true",
		},
		{
			name: "compile template into render function and execute",
			code: `
				function h(type, props, children) {
					return {
						__v_isVNode: true,
						type,
						props: props || {},
						children: children || []
					};
				}

				function compileTemplate(templateStr) {
					// 提取外层标签 tag 与 class
					const tagMatch = templateStr.match(/^<([a-zA-Z0-9_-]+)(?:\s+class="([^"]*)")?>/);
					const tag = tagMatch ? tagMatch[1] : 'div';
					const cls = tagMatch && tagMatch[2] ? tagMatch[2] : '';
					const innerTemplate = templateStr.replace(/^<[^>]*>/, '').replace(/<\/[^>]*>$/, '');

					// 提取插值表达式 {{ ... }}
					const tokens = [];
					let lastIndex = 0;
					const regex = /\{\{\s*(.*?)\s*\}\}/g;
					let match;

					while ((match = regex.exec(innerTemplate)) !== null) {
						if (match.index > lastIndex) {
							tokens.push(JSON.stringify(innerTemplate.slice(lastIndex, match.index)));
						}
						tokens.push('_ctx.' + match[1]);
						lastIndex = regex.lastIndex;
					}
					if (lastIndex < innerTemplate.length) {
						tokens.push(JSON.stringify(innerTemplate.slice(lastIndex)));
					}

					// 构造纯 JS render 函数代码
					const childrenExpr = tokens.length > 0 ? tokens.join(' + ') : '""';
					const code = 'return function render(_ctx) { return h(' + JSON.stringify(tag) + ', { class: ' + JSON.stringify(cls) + ' }, ' + childrenExpr + '); };';
					return new Function('h', code)(h);
				}

				const template = '<div class="msg-box">Hello {{ greeting }}!</div>';
				const renderFn = compileTemplate(template);

				const state = { greeting: 'Vue3 World' };
				const vnode = renderFn(state);

				return vnode.type + "|" + vnode.props.class + "|" + vnode.children;
			`,
			want: "div|msg-box|Hello Vue3 World!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vmEvalStr(t, tt.code)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
