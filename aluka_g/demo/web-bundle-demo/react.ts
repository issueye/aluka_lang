// 极简 React shim：满足 JSX lower 产物 React.createElement 的调用约定。
// 仅实现 vnode 构造与 HTML 序列化，无 DOM diff——演示 bundler，非 UI 框架。

export function Fragment() {}

export function createElement(type, props, ...children) {
  const flat = [];
  for (const c of children) {
    if (Array.isArray(c)) {
      for (const inner of c) flat.push(inner);
    } else {
      flat.push(c);
    }
  }
  return { type, props: props || {}, children: flat };
}

export function renderToString(node) {
  if (node == null || node === false || node === true) return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(renderToString).join('');
  // 函数组件：先执行组件函数得到下层 vnode 再序列化。
  if (typeof node.type === 'function') {
    const props = node.props || {};
    props.children = node.children;
    return renderToString(node.type(props));
  }
  let attrs = '';
  for (const key of Object.keys(node.props || {})) {
    if (key === 'children') continue;
    const name = key === 'className' ? 'class' : key;
    attrs += ' ' + name + '="' + node.props[key] + '"';
  }
  const tag = node.type;
  return '<' + tag + attrs + '>' + renderToString(node.children) + '</' + tag + '>';
}
