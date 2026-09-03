// Tailwind CSS JIT 即时编译器 (纯 JS 自研轻量级实现)

const colorMap = {
  slate: {
    50: '#f8fafc', 100: '#f1f5f9', 200: '#e2e8f0', 300: '#cbd5e1',
    400: '#94a3b8', 500: '#64748b', 600: '#475569', 700: '#334155',
    800: '#1e293b', 900: '#0f172a', 950: '#020617'
  },
  sky: {
    300: '#7dd3fc', 400: '#38bdf8', 500: '#0ea5e9', 600: '#0284c7',
    900: '#0c4a6e', 950: '#082f49'
  },
  indigo: {
    300: '#a5b4fc', 400: '#818cf8', 500: '#6366f1', 600: '#4f46e5',
    900: '#312e81', 950: '#1e1b4b'
  },
  emerald: {
    300: '#6ee7b7', 400: '#34d399', 500: '#10b981', 900: '#064e3b'
  },
  purple: {
    300: '#d8b4fe', 400: '#c084fc', 500: '#a855f7', 900: '#581c87'
  },
  amber: {
    300: '#fcd34d', 400: '#fbbf24', 500: '#f59e0b', 900: '#78350f'
  },
  rose: {
    300: '#fda4af', 400: '#fb7185', 500: '#f43f5e', 900: '#881337'
  }
};

function resolveColor(name, shade) {
  if (name === 'white') return '#ffffff';
  if (name === 'black') return '#000000';
  if (name === 'transparent') return 'transparent';
  if (colorMap[name] && colorMap[name][shade]) {
    return colorMap[name][shade];
  }
  return null;
}

export function compileTailwind(htmlSources) {
  const classNames = new Set();
  const classRegex = /class(?:Name)?=["']([^"']+)["']/g;

  for (const src of htmlSources) {
    let match;
    while ((match = classRegex.exec(src)) !== null) {
      const tokens = match[1].split(/\s+/);
      for (const t of tokens) {
        if (t.trim()) classNames.add(t.trim());
      }
    }
  }

  const rules = [];

  for (const cls of classNames) {
    const escaped = cls.replace(/([:/.\[\]])/g, '\\$1');

    // 1. Flex & Grid
    if (cls === 'flex') rules.push(`.${escaped} { display: flex; }`);
    else if (cls === 'inline-flex') rules.push(`.${escaped} { display: inline-flex; }`);
    else if (cls === 'grid') rules.push(`.${escaped} { display: grid; }`);
    else if (cls === 'hidden') rules.push(`.${escaped} { display: none; }`);
    else if (cls === 'items-center') rules.push(`.${escaped} { align-items: center; }`);
    else if (cls === 'items-start') rules.push(`.${escaped} { align-items: flex-start; }`);
    else if (cls === 'justify-between') rules.push(`.${escaped} { justify-content: space-between; }`);
    else if (cls === 'justify-center') rules.push(`.${escaped} { justify-content: center; }`);
    else if (cls === 'flex-col') rules.push(`.${escaped} { flex-direction: column; }`);
    else if (cls === 'flex-1') rules.push(`.${escaped} { flex: 1 1 0%; }`);
    else if (cls === 'flex-wrap') rules.push(`.${escaped} { flex-wrap: wrap; }`);
    else if (cls === 'grid-cols-1') rules.push(`.${escaped} { grid-template-columns: repeat(1, minmax(0, 1fr)); }`);
    else if (cls === 'grid-cols-2') rules.push(`.${escaped} { grid-template-columns: repeat(2, minmax(0, 1fr)); }`);
    else if (cls === 'grid-cols-3') rules.push(`.${escaped} { grid-template-columns: repeat(3, minmax(0, 1fr)); }`);
    else if (cls === 'grid-cols-4') rules.push(`.${escaped} { grid-template-columns: repeat(4, minmax(0, 1fr)); }`);

    // 2. Spacing & Gap
    else if (/^gap-(\d+)$/.test(cls)) {
      const n = cls.match(/^gap-(\d+)$/)[1];
      rules.push(`.${escaped} { gap: ${n * 0.25}rem; }`);
    } else if (/^space-y-(\d+)$/.test(cls)) {
      const n = cls.match(/^space-y-(\d+)$/)[1];
      rules.push(`.${escaped} > :not([hidden]) ~ :not([hidden]) { margin-top: ${n * 0.25}rem; }`);
    } else if (/^space-x-(\d+)$/.test(cls)) {
      const n = cls.match(/^space-x-(\d+)$/)[1];
      rules.push(`.${escaped} > :not([hidden]) ~ :not([hidden]) { margin-left: ${n * 0.25}rem; }`);
    }

    // 3. Padding & Margin
    else if (/^p-(\d+)$/.test(cls)) {
      const n = cls.match(/^p-(\d+)$/)[1];
      rules.push(`.${escaped} { padding: ${n * 0.25}rem; }`);
    } else if (/^px-(\d+)$/.test(cls)) {
      const n = cls.match(/^px-(\d+)$/)[1];
      rules.push(`.${escaped} { padding-left: ${n * 0.25}rem; padding-right: ${n * 0.25}rem; }`);
    } else if (/^py-(\d+)$/.test(cls)) {
      const n = cls.match(/^py-(\d+)$/)[1];
      rules.push(`.${escaped} { padding-top: ${n * 0.25}rem; padding-bottom: ${n * 0.25}rem; }`);
    } else if (/^pt-(\d+)$/.test(cls)) {
      const n = cls.match(/^pt-(\d+)$/)[1];
      rules.push(`.${escaped} { padding-top: ${n * 0.25}rem; }`);
    } else if (/^pb-(\d+)$/.test(cls)) {
      const n = cls.match(/^pb-(\d+)$/)[1];
      rules.push(`.${escaped} { padding-bottom: ${n * 0.25}rem; }`);
    } else if (/^m-(\d+)$/.test(cls)) {
      const n = cls.match(/^m-(\d+)$/)[1];
      rules.push(`.${escaped} { margin: ${n * 0.25}rem; }`);
    } else if (/^mb-(\d+)$/.test(cls)) {
      const n = cls.match(/^mb-(\d+)$/)[1];
      rules.push(`.${escaped} { margin-bottom: ${n * 0.25}rem; }`);
    } else if (/^mt-(\d+)$/.test(cls)) {
      const n = cls.match(/^mt-(\d+)$/)[1];
      rules.push(`.${escaped} { margin-top: ${n * 0.25}rem; }`);
    } else if (cls === 'mx-auto') {
      rules.push(`.${escaped} { margin-left: auto; margin-right: auto; }`);
    }

    // 4. Width & Height & Sizing
    else if (cls === 'w-full') rules.push(`.${escaped} { width: 100%; }`);
    else if (cls === 'h-full') rules.push(`.${escaped} { height: 100%; }`);
    else if (cls === 'min-h-screen') rules.push(`.${escaped} { min-height: 100vh; }`);
    else if (cls === 'max-w-4xl') rules.push(`.${escaped} { max-width: 56rem; }`);
    else if (cls === 'max-w-5xl') rules.push(`.${escaped} { max-width: 64rem; }`);
    else if (cls === 'max-w-6xl') rules.push(`.${escaped} { max-width: 72rem; }`);
    else if (/^w-(\d+)$/.test(cls)) {
      const n = cls.match(/^w-(\d+)$/)[1];
      rules.push(`.${escaped} { width: ${n * 0.25}rem; }`);
    } else if (/^h-(\d+)$/.test(cls)) {
      const n = cls.match(/^h-(\d+)$/)[1];
      rules.push(`.${escaped} { height: ${n * 0.25}rem; }`);
    }

    // 5. Colors (bg, text, border)
    else if (/^bg-([a-z]+)-(\d+)$/.test(cls)) {
      const [, col, shade] = cls.match(/^bg-([a-z]+)-(\d+)$/);
      const c = resolveColor(col, shade);
      if (c) rules.push(`.${escaped} { background-color: ${c}; }`);
    } else if (/^text-([a-z]+)-(\d+)$/.test(cls)) {
      const [, col, shade] = cls.match(/^text-([a-z]+)-(\d+)$/);
      const c = resolveColor(col, shade);
      if (c) rules.push(`.${escaped} { color: ${c}; }`);
    } else if (/^border-([a-z]+)-(\d+)$/.test(cls)) {
      const [, col, shade] = cls.match(/^border-([a-z]+)-(\d+)$/);
      const c = resolveColor(col, shade);
      if (c) rules.push(`.${escaped} { border-color: ${c}; }`);
    }

    // 6. Typography
    else if (cls === 'font-bold') rules.push(`.${escaped} { font-weight: 700; }`);
    else if (cls === 'font-semibold') rules.push(`.${escaped} { font-weight: 600; }`);
    else if (cls === 'font-mono') rules.push(`.${escaped} { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }`);
    else if (cls === 'font-sans') rules.push(`.${escaped} { font-family: ui-sans-serif, system-ui, sans-serif; }`);
    else if (cls === 'text-xs') rules.push(`.${escaped} { font-size: 0.75rem; line-height: 1rem; }`);
    else if (cls === 'text-sm') rules.push(`.${escaped} { font-size: 0.875rem; line-height: 1.25rem; }`);
    else if (cls === 'text-base') rules.push(`.${escaped} { font-size: 1rem; line-height: 1.5rem; }`);
    else if (cls === 'text-lg') rules.push(`.${escaped} { font-size: 1.125rem; line-height: 1.75rem; }`);
    else if (cls === 'text-xl') rules.push(`.${escaped} { font-size: 1.25rem; line-height: 1.75rem; }`);
    else if (cls === 'text-2xl') rules.push(`.${escaped} { font-size: 1.5rem; line-height: 2rem; }`);
    else if (cls === 'text-3xl') rules.push(`.${escaped} { font-size: 1.875rem; line-height: 2.25rem; }`);

    // 7. Borders & Shadows
    else if (cls === 'border') rules.push(`.${escaped} { border-width: 1px; }`);
    else if (cls === 'border-t') rules.push(`.${escaped} { border-top-width: 1px; }`);
    else if (cls === 'border-b') rules.push(`.${escaped} { border-bottom-width: 1px; }`);
    else if (cls === 'rounded-lg') rules.push(`.${escaped} { border-radius: 0.5rem; }`);
    else if (cls === 'rounded-xl') rules.push(`.${escaped} { border-radius: 0.75rem; }`);
    else if (cls === 'rounded-2xl') rules.push(`.${escaped} { border-radius: 1rem; }`);
    else if (cls === 'rounded-full') rules.push(`.${escaped} { border-radius: 9999px; }`);
    else if (cls === 'shadow-lg') rules.push(`.${escaped} { box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.3); }`);
    else if (cls === 'shadow-2xl') rules.push(`.${escaped} { box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5); }`);
  }

  const baseCss = `
*, ::before, ::after { box-sizing: border-box; border-width: 0; border-style: solid; border-color: #334155; }
html { line-height: 1.5; -webkit-text-size-adjust: 100%; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
body { margin: 0; line-height: inherit; }
  `;

  return baseCss + '\n' + rules.join('\n');
}
