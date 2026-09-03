import { h, inject } from '../vdom.js';

export const ThemeKey = Symbol('ThemeKey');

export const Card = {
  setup(props, { slots }) {
    const theme = inject(ThemeKey, 'light');
    const slotContent = slots && slots.default ? slots.default() : '';

    const themeClass =
      theme === 'dark'
        ? 'bg-slate-800 border-slate-700 text-slate-100'
        : 'bg-white border-slate-200 text-slate-900';

    return () =>
      h('div', { class: `border rounded-2xl p-6 shadow-xl my-4 transition-all duration-200 ${themeClass}` }, [
        h(
          'div',
          { class: 'text-lg font-semibold tracking-tight text-sky-400 mb-3 flex items-center justify-between' },
          [
            h('span', null, props.title || 'Card Component'),
            props.badge ? h('span', { class: 'text-xs font-mono px-2 py-1 bg-sky-900 text-sky-300 rounded-full' }, props.badge) : ''
          ]
        ),
        h('div', { class: 'text-slate-300 text-sm leading-relaxed' }, slotContent)
      ]);
  }
};
