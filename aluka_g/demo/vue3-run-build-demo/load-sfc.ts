// 运行时用官方 vue/compiler-sfc 编译同目录 .vue，供 aluka run SSR。
import fs from 'node:fs';
import path from 'node:path';
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc';

const SFC_ID = 'c0ffee11';

export type LoadedSFC = {
  default: unknown;
  css: string;
};

export async function loadSFC(filename: string): Promise<LoadedSFC> {
  const src = fs.readFileSync(filename, 'utf8');
  const { descriptor, errors } = parse(src, { filename });
  if (errors && errors.length) {
    throw errors[0];
  }

  const script = compileScript(descriptor, {
    id: SFC_ID,
    inlineTemplate: false,
    genDefaultAs: '__sfc__',
  });

  const hasScoped = (descriptor.styles || []).some((style) => style.scoped);
  if (!descriptor.template) {
    throw new Error('loadSFC: missing <template>');
  }
  const template = compileTemplate({
    source: descriptor.template.content,
    filename,
    id: SFC_ID,
    scoped: hasScoped,
    compilerOptions: { bindingMetadata: script.bindings },
  });
  if (template.errors && template.errors.length) {
    throw template.errors[0];
  }

  const cssChunks: string[] = [];
  for (const style of descriptor.styles || []) {
    cssChunks.push(style.content || '');
  }

  const outDir = path.join(path.dirname(filename), '..', '.generated');
  fs.mkdirSync(outDir, { recursive: true });
  const stem = path.basename(filename, '.vue');
  const renderFile = path.join(outDir, stem + '.render.mjs');
  const facadeFile = path.join(outDir, stem + '.mjs');
  fs.writeFileSync(renderFile, template.code);
  fs.writeFileSync(
    facadeFile,
    [
      `import { render } from ${JSON.stringify('./' + stem + '.render.mjs')};`,
      script.content,
      '__sfc__.render = render;',
      hasScoped ? `__sfc__.__scopeId = ${JSON.stringify('data-v-' + SFC_ID)};` : '',
      'export default __sfc__;',
    ]
      .filter(Boolean)
      .join('\n'),
  );

  const mod = await import('./.generated/' + stem + '.mjs');
  return { default: mod.default, css: cssChunks.join('\n') };
}
