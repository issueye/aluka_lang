package interpreter

import (
	"testing"
)

// TestVue3SSR 验证 Vue 3 组件系统与服务端渲染（SSR）在 Aluka 引擎下的运行能力
func TestVue3SSR(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "basic component renderToString",
			code: `
				(async () => {
					function h(type, props, children) {
						return {
							__v_isVNode: true,
							type,
							props: props || {},
							children: children || []
						};
					}

					function escapeHtml(str) {
						return String(str)
							.replace(/&/g, '&amp;')
							.replace(/</g, '&lt;')
							.replace(/>/g, '&gt;')
							.replace(/"/g, '&quot;');
					}

					async function renderComponent(comp, props = {}) {
						let vnode;
						if (typeof comp === 'function') {
							vnode = comp(props);
						} else if (comp.setup) {
							const render = await comp.setup(props);
							vnode = typeof render === 'function' ? render() : render;
						} else if (comp.render) {
							vnode = comp.render(props);
						}
						return renderVNode(vnode);
					}

					async function renderVNode(vnode) {
						if (typeof vnode === 'string' || typeof vnode === 'number') {
							return escapeHtml(vnode);
						}
						if (Array.isArray(vnode)) {
							const parts = [];
							for (const child of vnode) {
								parts.push(await renderVNode(child));
							}
							return parts.join('');
						}
						if (!vnode || !vnode.__v_isVNode) {
							return '';
						}
						if (typeof vnode.type === 'object' || typeof vnode.type === 'function') {
							return renderComponent(vnode.type, vnode.props);
						}

						const tag = vnode.type;
						let propsStr = '';
						for (const key of Object.keys(vnode.props)) {
							const val = vnode.props[key];
							if (key === 'class') {
								propsStr += ' class="' + escapeHtml(val) + '"';
							} else if (key === 'style' && typeof val === 'object') {
								const styleStr = Object.keys(val).map(k => k + ':' + val[k]).join(';');
								propsStr += ' style="' + escapeHtml(styleStr) + '"';
							} else if (typeof val === 'string' || typeof val === 'number') {
								propsStr += ' ' + key + '="' + escapeHtml(val) + '"';
							}
						}

						let childrenStr = '';
						if (Array.isArray(vnode.children)) {
							for (const c of vnode.children) {
								childrenStr += await renderVNode(c);
							}
						} else if (vnode.children) {
							childrenStr = await renderVNode(vnode.children);
						}

						return '<' + tag + propsStr + '>' + childrenStr + '</' + tag + '>';
					}

					function createSSRApp(rootComponent) {
						return {
							_component: rootComponent,
							async renderToString() {
								return renderComponent(this._component);
							}
						};
					}

					// Child Component
					const ChildComp = {
						setup(props) {
							return () => h('span', { class: 'child' }, 'Child content: ' + props.title);
						}
					};

					// Root Component with reactive state & async setup
					const App = {
						async setup() {
							const message = "Hello, Vue3 on Aluka!";
							return () => h('div', { id: 'app', class: 'container', style: { color: 'blue' } }, [
								h('h1', null, message),
								h(ChildComp, { title: 'Welcome' })
							]);
						}
					};

					const app = createSSRApp(App);
					globalThis.__r = await app.renderToString();
				})()
			`,
			want: `<div id="app" class="container" style="color:blue"><h1>Hello, Vue3 on Aluka!</h1><span class="child">Child content: Welcome</span></div>`,
		},
		{
			name: "component with slots and provide inject",
			code: `
				(async () => {
					let currentInstance = null;

					function provide(key, value) {
						if (currentInstance) {
							currentInstance.provides[key] = value;
						}
					}

					function inject(key, defaultValue) {
						if (currentInstance && currentInstance.parent) {
							let parent = currentInstance.parent;
							while (parent) {
								if (key in parent.provides) {
									return parent.provides[key];
								}
								parent = parent.parent;
							}
						}
						return defaultValue;
					}

					function h(type, props, children) {
						return {
							__v_isVNode: true,
							type,
							props: props || {},
							children: children || []
						};
					}

					async function renderComponentWithContext(comp, props = {}, parentInstance = null, slots = {}) {
						const instance = {
							provides: parentInstance ? Object.create(parentInstance.provides) : {},
							parent: parentInstance,
							slots: slots
						};
						const prev = currentInstance;
						currentInstance = instance;
						try {
							const render = await comp.setup(props, { slots: instance.slots });
							return await renderNode(render(), instance);
						} finally {
							currentInstance = prev;
						}
					}

					async function renderNode(vnode, instance) {
						if (typeof vnode === 'string' || typeof vnode === 'number') {
							return String(vnode);
						}
						if (!vnode) return '';
						if (typeof vnode.type === 'object') {
							return renderComponentWithContext(vnode.type, vnode.props, instance, vnode.children);
						}
						const tag = vnode.type;
						let propsStr = '';
						if (vnode.props) {
							for (const key of Object.keys(vnode.props)) {
								propsStr += ' ' + key + '="' + vnode.props[key] + '"';
							}
						}
						let childrenStr = '';
						if (Array.isArray(vnode.children)) {
							for (const c of vnode.children) {
								childrenStr += await renderNode(c, instance);
							}
						} else if (vnode.children) {
							childrenStr = await renderNode(vnode.children, instance);
						}
						return '<' + tag + propsStr + '>' + childrenStr + '</' + tag + '>';
					}

					const themeSymbol = Symbol('theme');

					const Card = {
						setup(props, { slots }) {
							const theme = inject(themeSymbol, 'light');
							const slotContent = (slots && slots.default) ? slots.default() : '';
							return () => h('div', { class: 'card theme-' + theme }, [
								h('header', null, 'Card Header (' + theme + ')'),
								h('section', null, slotContent)
							]);
						}
					};

					const ProviderApp = {
						setup() {
							provide(themeSymbol, 'dark');
							return () => h('main', null, [
								h(Card, null, {
									default: () => h('p', null, 'Slot passed content')
								})
							]);
						}
					};

					globalThis.__r = await renderComponentWithContext(ProviderApp);
				})()
			`,
			want: `<main><div class="card theme-dark"><header>Card Header (dark)</header><section><p>Slot passed content</p></section></div></main>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vmEvalPromise(t, tt.code)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
