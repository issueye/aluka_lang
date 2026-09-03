// ReactDOMServer 服务端渲染器 (纯 JS 实现)
import { React } from './react.js';

const VOID_TAGS = new Set([
  'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input',
  'link', 'meta', 'param', 'source', 'track', 'wbr'
]);

function escapeHtml(str) {
  if (typeof str !== 'string') return '' + str;
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function styleObjToString(style) {
  if (typeof style === 'string') return style;
  if (!style || typeof style !== 'object') return '';
  return Object.entries(style)
    .map(([k, v]) => {
      const propName = k.replace(/([A-Z])/g, '-$1').toLowerCase();
      return `${propName}:${v}`;
    })
    .join(';');
}

export function renderToString(node) {
  if (node === null || node === undefined || node === false || node === true) {
    return '';
  }

  if (typeof node === 'string' || typeof node === 'number') {
    return escapeHtml('' + node);
  }

  if (Array.isArray(node)) {
    return node.map(renderToString).join('');
  }

  const { type, props = {} } = node;

  // 1. Fragment
  if (type === React.Fragment || type === Symbol.for('react.fragment')) {
    return renderToString(props.children);
  }

  // 2. 自定义函数组件
  if (typeof type === 'function') {
    const vnode = type(props);
    return renderToString(vnode);
  }

  // 3. 原生 HTML 标签
  if (typeof type === 'string') {
    const tagName = type.toLowerCase();
    const attrs = [];

    for (const [key, value] of Object.entries(props)) {
      if (key === 'children' || key === 'key' || key === 'ref') continue;

      if (key === 'className') {
        if (value) attrs.push(`class="${escapeHtml(value)}"`);
      } else if (key === 'style') {
        const styleStr = styleObjToString(value);
        if (styleStr) attrs.push(`style="${escapeHtml(styleStr)}"`);
      } else if (key === 'dangerouslySetInnerHTML' && value && value.__html) {
        // innerHTML 留待后续处理
      } else if (typeof value === 'boolean') {
        if (value) attrs.push(escapeHtml(key));
      } else if (value !== null && value !== undefined) {
        attrs.push(`${escapeHtml(key)}="${escapeHtml('' + value)}"`);
      }
    }

    const attrStr = attrs.length > 0 ? ' ' + attrs.join(' ') : '';

    if (VOID_TAGS.has(tagName)) {
      return `<${tagName}${attrStr} />`;
    }

    let childrenHtml = '';
    if (props.dangerouslySetInnerHTML && props.dangerouslySetInnerHTML.__html) {
      childrenHtml = props.dangerouslySetInnerHTML.__html;
    } else {
      childrenHtml = renderToString(props.children);
    }

    return `<${tagName}${attrStr}>${childrenHtml}</${tagName}>`;
  }

  return '';
}

export const ReactDOMServer = { renderToString };
export default ReactDOMServer;
