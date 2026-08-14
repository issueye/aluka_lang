// Tailwind CSS JIT 核心生成器 (Pure JS for Aluka Engine)

// 基础原子工具类字典映射
const colorPalette = {
  slate: {
    50: '#f8fafc', 100: '#f1f5f9', 200: '#e2e8f0', 300: '#cbd5e1', 400: '#94a3b8',
    500: '#64748b', 600: '#475569', 700: '#334155', 800: '#1e293b', 900: '#0f172a', 950: '#020617'
  },
  sky: {
    50: '#f0f9ff', 100: '#e0f2fe', 200: '#bae6fd', 300: '#7dd3fc', 400: '#38bdf8',
    500: '#0ea5e9', 600: '#0284c7', 700: '#0369a1', 800: '#075985', 900: '#0c4a6e'
  },
  emerald: {
    50: '#ecfdf5', 100: '#d1fae5', 400: '#34d399', 500: '#10b981', 600: '#059669', 900: '#064e3b'
  },
  purple: {
    50: '#faf5ff', 100: '#f3e8ff', 400: '#c084fc', 500: '#a855f7', 600: '#9333ea', 900: '#581c87'
  },
  amber: {
    50: '#fffbeb', 100: '#fef3c7', 400: '#fbbf24', 500: '#f59e0b', 600: '#d97706', 900: '#78350f'
  },
  rose: {
    50: '#fff1f2', 100: '#ffe4e6', 400: '#fb7185', 500: '#f43f5e', 600: '#e11d48', 900: '#881337'
  },
  white: '#ffffff',
  black: '#000000',
  transparent: 'transparent'
};

const spacing = {
  0: '0px', 1: '0.25rem', 2: '0.5rem', 3: '0.75rem', 4: '1rem', 5: '1.25rem', 6: '1.5rem',
  8: '2rem', 10: '2.5rem', 12: '3rem', 16: '4rem', 20: '5rem', 24: '6rem', auto: 'auto'
};

// 解析颜色类名（如 bg-slate-900, text-sky-400, border-purple-500）
function resolveColor(prefix, token) {
  if (!token.startsWith(prefix)) return null;
  const raw = token.slice(prefix.length);
  if (raw === 'white') return colorPalette.white;
  if (raw === 'black') return colorPalette.black;
  if (raw === 'transparent') return colorPalette.transparent;

  const parts = raw.split('-');
  if (parts.length === 2) {
    const [name, shade] = parts;
    if (colorPalette[name] && colorPalette[name][shade]) {
      return colorPalette[name][shade];
    }
  }
  return null;
}

// 规则处理器列表
const rules = [
  // Display & Layout
  { match: /^flex$/, css: () => 'display: flex;' },
  { match: /^inline-flex$/, css: () => 'display: inline-flex;' },
  { match: /^grid$/, css: () => 'display: grid;' },
  { match: /^block$/, css: () => 'display: block;' },
  { match: /^inline-block$/, css: () => 'display: inline-block;' },
  { match: /^hidden$/, css: () => 'display: none;' },

  // Flexbox & Grid Props
  { match: /^flex-col$/, css: () => 'flex-direction: column;' },
  { match: /^flex-row$/, css: () => 'flex-direction: row;' },
  { match: /^flex-wrap$/, css: () => 'flex-wrap: wrap;' },
  { match: /^items-center$/, css: () => 'align-items: center;' },
  { match: /^items-start$/, css: () => 'align-items: flex-start;' },
  { match: /^items-end$/, css: () => 'align-items: flex-end;' },
  { match: /^justify-between$/, css: () => 'justify-content: space-between;' },
  { match: /^justify-center$/, css: () => 'justify-content: center;' },
  { match: /^justify-start$/, css: () => 'justify-content: flex-start;' },
  { match: /^justify-end$/, css: () => 'justify-content: flex-end;' },
  { match: /^grid-cols-(\d+)$/, css: (m) => `grid-template-columns: repeat(${m[1]}, minmax(0, 1fr));` },
  { match: /^gap-(\d+|auto)$/, css: (m) => `gap: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^gap-x-(\d+|auto)$/, css: (m) => `column-gap: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^gap-y-(\d+|auto)$/, css: (m) => `row-gap: ${spacing[m[1]] || m[1] + 'px'};` },

  // Spacing (Padding & Margin)
  { match: /^p-(\d+|auto)$/, css: (m) => `padding: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^px-(\d+|auto)$/, css: (m) => `padding-left: ${spacing[m[1]] || m[1] + 'px'}; padding-right: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^py-(\d+|auto)$/, css: (m) => `padding-top: ${spacing[m[1]] || m[1] + 'px'}; padding-bottom: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^pt-(\d+|auto)$/, css: (m) => `padding-top: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^pb-(\d+|auto)$/, css: (m) => `padding-bottom: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^pl-(\d+|auto)$/, css: (m) => `padding-left: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^pr-(\d+|auto)$/, css: (m) => `padding-right: ${spacing[m[1]] || m[1] + 'px'};` },

  { match: /^m-(\d+|auto)$/, css: (m) => `margin: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^mx-(\d+|auto)$/, css: (m) => `margin-left: ${spacing[m[1]] || m[1] + 'px'}; margin-right: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^my-(\d+|auto)$/, css: (m) => `margin-top: ${spacing[m[1]] || m[1] + 'px'}; margin-bottom: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^mt-(\d+|auto)$/, css: (m) => `margin-top: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^mb-(\d+|auto)$/, css: (m) => `margin-bottom: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^ml-(\d+|auto)$/, css: (m) => `margin-left: ${spacing[m[1]] || m[1] + 'px'};` },
  { match: /^mr-(\d+|auto)$/, css: (m) => `margin-right: ${spacing[m[1]] || m[1] + 'px'};` },

  // Sizing
  { match: /^w-full$/, css: () => 'width: 100%;' },
  { match: /^w-screen$/, css: () => 'width: 100vw;' },
  { match: /^h-full$/, css: () => 'height: 100%;' },
  { match: /^h-screen$/, css: () => 'height: 100vh;' },
  { match: /^min-h-screen$/, css: () => 'min-height: 100vh;' },
  { match: /^max-w-([a-z0-9]+)$/, css: (m) => {
    const map = { sm: '24rem', md: '28rem', lg: '32rem', xl: '36rem', '2xl': '42rem', '3xl': '48rem', '4xl': '56rem', '5xl': '64rem', full: '100%' };
    return `max-width: ${map[m[1]] || m[1]};`;
  }},

  // Colors (Background, Text, Border)
  { match: /^bg-/, css: (_, t) => {
    const c = resolveColor('bg-', t);
    return c ? `background-color: ${c};` : null;
  }},
  { match: /^text-/, css: (_, t) => {
    const c = resolveColor('text-', t);
    if (c) return `color: ${c};`;
    const fontSizes = { xs: '0.75rem; line-height: 1rem', sm: '0.875rem; line-height: 1.25rem', base: '1rem; line-height: 1.5rem', lg: '1.125rem; line-height: 1.75rem', xl: '1.25rem; line-height: 1.75rem', '2xl': '1.5rem; line-height: 2rem', '3xl': '1.875rem; line-height: 2.25rem', '4xl': '2.25rem; line-height: 2.5rem' };
    const raw = t.slice(5);
    if (fontSizes[raw]) return `font-size: ${fontSizes[raw]};`;
    const aligns = { left: 'left', center: 'center', right: 'right' };
    if (aligns[raw]) return `text-align: ${aligns[raw]};`;
    return null;
  }},
  { match: /^border-/, css: (_, t) => {
    const c = resolveColor('border-', t);
    if (c) return `border-color: ${c};`;
    const widths = { 0: '0px', 2: '2px', 4: '4px', 8: '8px' };
    const raw = t.slice(7);
    if (widths[raw]) return `border-width: ${widths[raw]};`;
    return null;
  }},
  { match: /^border$/, css: () => 'border-width: 1px; border-style: solid;' },
  { match: /^border-b$/, css: () => 'border-bottom-width: 1px; border-style: solid;' },
  { match: /^border-t$/, css: () => 'border-top-width: 1px; border-style: solid;' },

  // Typography
  { match: /^font-sans$/, css: () => `font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;` },
  { match: /^font-mono$/, css: () => `font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;` },
  { match: /^font-bold$/, css: () => 'font-weight: 700;' },
  { match: /^font-semibold$/, css: () => 'font-weight: 600;' },
  { match: /^font-medium$/, css: () => 'font-weight: 500;' },
  { match: /^font-normal$/, css: () => 'font-weight: 400;' },
  { match: /^tracking-tight$/, css: () => 'letter-spacing: -0.025em;' },
  { match: /^tracking-wide$/, css: () => 'letter-spacing: 0.025em;' },

  // Borders & Radius
  { match: /^rounded$/, css: () => 'border-radius: 0.25rem;' },
  { match: /^rounded-md$/, css: () => 'border-radius: 0.375rem;' },
  { match: /^rounded-lg$/, css: () => 'border-radius: 0.5rem;' },
  { match: /^rounded-xl$/, css: () => 'border-radius: 0.75rem;' },
  { match: /^rounded-2xl$/, css: () => 'border-radius: 1rem;' },
  { match: /^rounded-3xl$/, css: () => 'border-radius: 1.5rem;' },
  { match: /^rounded-full$/, css: () => 'border-radius: 9999px;' },

  // Shadows
  { match: /^shadow-sm$/, css: () => 'box-shadow: 0 1px 2px 0 rgb(0 0 0 / 0.05);' },
  { match: /^shadow$/, css: () => 'box-shadow: 0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1);' },
  { match: /^shadow-md$/, css: () => 'box-shadow: 0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1);' },
  { match: /^shadow-lg$/, css: () => 'box-shadow: 0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1);' },
  { match: /^shadow-xl$/, css: () => 'box-shadow: 0 20px 25px -5px rgb(0 0 0 / 0.1), 0 8px 10px -6px rgb(0 0 0 / 0.1);' },
  { match: /^shadow-2xl$/, css: () => 'box-shadow: 0 25px 50px -12px rgb(0 0 0 / 0.25);' },

  // Effects & Transitions
  { match: /^transition-all$/, css: () => 'transition-property: all; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;' },
  { match: /^duration-200$/, css: () => 'transition-duration: 200ms;' },
  { match: /^duration-300$/, css: () => 'transition-duration: 300ms;' },
  { match: /^cursor-pointer$/, css: () => 'cursor: pointer;' }
];

// 转义 CSS 选择器中的特殊字符（如冒号、斜杠等）
function escapeCssSelector(className) {
  return className.replace(/([:/.])/g, '\\$1');
}

// 扫描 HTML 中所有 class 并即时生成 Tailwind CSS 样式表
export function generateTailwindCSS(htmlContent) {
  const classRegex = /class=["']([^"']+)["']/g;
  const classSet = new Set();
  let match;

  while ((match = classRegex.exec(htmlContent)) !== null) {
    const tokens = match[1].trim().split(/\s+/);
    for (const t of tokens) {
      if (t) classSet.add(t);
    }
  }

  const generatedRules = [];
  const processedClasses = new Set();

  for (const rawToken of classSet) {
    if (processedClasses.has(rawToken)) continue;
    processedClasses.add(rawToken);

    let modifier = '';
    let baseToken = rawToken;

    if (rawToken.startsWith('hover:')) {
      modifier = ':hover';
      baseToken = rawToken.slice(6);
    } else if (rawToken.startsWith('focus:')) {
      modifier = ':focus';
      baseToken = rawToken.slice(6);
    }

    for (const rule of rules) {
      const m = baseToken.match(rule.match);
      if (m) {
        const cssBody = rule.css(m, baseToken);
        if (cssBody) {
          const selector = `.${escapeCssSelector(rawToken)}${modifier}`;
          generatedRules.push(`${selector} { ${cssBody} }`);
          break;
        }
      }
    }
  }

  // 基础 Reset 与规范化全局样式
  const preflight = `
*, ::before, ::after { box-sizing: border-box; border-width: 0; border-style: solid; border-color: #e2e8f0; }
html { line-height: 1.5; -webkit-text-size-adjust: 100%; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
body { margin: 0; line-height: inherit; }
h1, h2, h3, h4, h5, h6, p, ul { margin: 0; padding: 0; }
ul { list-style: none; }
a { color: inherit; text-decoration: inherit; }
`;

  return `${preflight}\n/* --- Tailwind CSS JIT Generated Utilities (${generatedRules.length} rules) --- */\n${generatedRules.join('\n')}\n`;
}
