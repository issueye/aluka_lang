import { h, provide } from '../vdom.js';
import { ref, computed, reactive } from '../reactivity.js';
import { Card, ThemeKey } from './Card.js';

export const App = {
  async setup() {
    // 响应式状态
    const count = ref(24);
    const double = computed(() => count.value * 2);
    const serverInfo = reactive({
      runtime: 'Aluka Runtime (100% Pure Go)',
      cssEngine: 'Tailwind CSS JIT Compiler',
      features: [
        'Proxy-based Reactivity (Reflect + WeakMap)',
        'Virtual DOM & Async SSR (renderToString)',
        'SFC Dynamic Compilation (new Function)',
        'Tailwind CSS JIT Auto Extraction'
      ],
      timestamp: new Date().toISOString()
    });

    // 依赖注入
    provide(ThemeKey, 'dark');

    return () =>
      h('div', { class: 'min-h-screen bg-slate-950 text-slate-100 font-sans p-8' }, [
        h('div', { class: 'max-w-3xl mx-auto' }, [
          // Header 区域
          h('header', { class: 'border-b border-slate-800 pb-6 mb-8 flex items-center justify-between' }, [
            h('div', null, [
              h('h1', { class: 'text-3xl font-bold tracking-tight text-sky-400' }, '⚡ Vue 3 + Tailwind CSS'),
              h('p', { class: 'text-sm text-slate-400 mt-1' }, 'Full-stack Server-Side Rendering on Pure Go Aluka Runtime')
            ]),
            h('div', { class: 'px-3 py-1 bg-purple-900 border border-purple-500 text-purple-300 text-xs font-mono rounded-full' }, 'Tailwind v3 JIT')
          ]),

          // 响应式状态卡片
          h(
            Card,
            { title: '1. 响应式计算状态 (Reactivity & Computed)', badge: 'State Active' },
            {
              default: () => [
                h('div', { class: 'grid grid-cols-2 gap-4 my-2' }, [
                  h('div', { class: 'p-4 bg-slate-900 border border-slate-700 rounded-xl' }, [
                    h('div', { class: 'text-xs text-slate-400 font-medium' }, 'Base Count (ref)'),
                    h('div', { class: 'text-2xl font-bold text-sky-400 mt-1' }, String(count.value))
                  ]),
                  h('div', { class: 'p-4 bg-slate-900 border border-slate-700 rounded-xl' }, [
                    h('div', { class: 'text-xs text-slate-400 font-medium' }, 'Doubled (computed)'),
                    h('div', { class: 'text-2xl font-bold text-emerald-400 mt-1' }, String(double.value))
                  ])
                ]),
                h('p', { class: 'text-xs text-slate-500 mt-3' }, '💡 Dependent effects and computed values are automatically tracked and evaluated during SSR rendering.')
              ]
            }
          ),

          // 运行时特性与指标卡片
          h(
            Card,
            { title: '2. 引擎能力与架构特性 (Runtime Capabilities)', badge: 'Pure Go' },
            {
              default: () => [
                h('div', { class: 'flex flex-col gap-2 my-2' }, [
                  h('div', { class: 'flex items-center justify-between text-xs py-1 border-b border-slate-700' }, [
                    h('span', { class: 'text-slate-400' }, 'JS/TS Runtime:'),
                    h('span', { class: 'font-mono text-sky-400' }, serverInfo.runtime)
                  ]),
                  h('div', { class: 'flex items-center justify-between text-xs py-1 border-b border-slate-700' }, [
                    h('span', { class: 'text-slate-400' }, 'CSS Framework:'),
                    h('span', { class: 'font-mono text-purple-400' }, serverInfo.cssEngine)
                  ])
                ]),
                h('div', { class: 'mt-4' }, [
                  h('div', { class: 'text-xs font-semibold text-slate-400 mb-2' }, 'Supported Feature Matrix:'),
                  h('div', { class: 'grid grid-cols-2 gap-2' }, serverInfo.features.map(f =>
                    h('div', { class: 'text-xs p-2 bg-slate-900 border border-slate-800 rounded-lg text-slate-300 flex items-center gap-2' }, [
                      h('span', { class: 'text-emerald-400 font-bold' }, '✓'),
                      h('span', null, f)
                    ])
                  ))
                ])
              ]
            }
          ),

          // 底部导航栏与链接
          h('footer', { class: 'mt-8 pt-6 border-t border-slate-800 flex items-center justify-between text-xs text-slate-400' }, [
            h('div', null, [
              h('span', null, 'Rendered on Aluka at '),
              h('span', { class: 'font-mono text-slate-300' }, serverInfo.timestamp)
            ]),
            h('div', { class: 'flex items-center gap-4' }, [
              h('a', { href: '/sfc', class: 'text-sky-400 hover:text-sky-300 font-medium' }, '📦 SFC Demo'),
              h('a', { href: '/tailwind-css', class: 'text-purple-400 hover:text-purple-300 font-medium' }, '🎨 Raw Tailwind CSS'),
              h('a', { href: '/api/state', class: 'text-emerald-400 hover:text-emerald-300 font-medium' }, '⚡ State API')
            ])
          ])
        ])
      ]);
  }
};
