// Vue 3 SFC 单文件组件解析与模板动态编译器
import { h } from './vdom.js';

export function parseSFC(source) {
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

export function compileTemplate(templateStr) {
  // 提取外层标签 tag 与 class / id
  const tagMatch = templateStr.match(/^<([a-zA-Z0-9_-]+)(?:\s+class="([^"]*)")?(?:\s+id="([^"]*)")?>/);
  const tag = tagMatch ? tagMatch[1] : 'div';
  const cls = tagMatch && tagMatch[2] ? tagMatch[2] : '';
  const id = tagMatch && tagMatch[3] ? tagMatch[3] : '';

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

  const childrenExpr = tokens.length > 0 ? tokens.join(' + ') : '""';
  const propsObj = {};
  if (cls) propsObj.class = cls;
  if (id) propsObj.id = id;

  const code =
    'return function render(_ctx) { return h(' +
    JSON.stringify(tag) +
    ', ' +
    JSON.stringify(propsObj) +
    ', ' +
    childrenExpr +
    '); };';

  return new Function('h', code)(h);
}
