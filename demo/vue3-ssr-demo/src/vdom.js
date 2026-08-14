// Vue 3 虚拟 DOM 与 SSR 服务端渲染核心

let currentInstance = null;

export function provide(key, value) {
  if (currentInstance) {
    currentInstance.provides[key] = value;
  }
}

export function inject(key, defaultValue) {
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

export function h(type, props, children) {
  return {
    __v_isVNode: true,
    type,
    props: props || {},
    children: children || []
  };
}

export function escapeHtml(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

export async function renderComponentWithContext(comp, props = {}, parentInstance = null, slots = {}) {
  const instance = {
    provides: parentInstance ? Object.create(parentInstance.provides) : {},
    parent: parentInstance,
    slots: slots
  };
  const prev = currentInstance;
  currentInstance = instance;
  try {
    let render;
    if (typeof comp === 'function') {
      render = () => comp(props, { slots: instance.slots });
    } else if (comp.setup) {
      const setupResult = await comp.setup(props, { slots: instance.slots });
      render = typeof setupResult === 'function' ? setupResult : () => setupResult;
    } else if (comp.render) {
      render = () => comp.render(props, { slots: instance.slots });
    } else {
      render = () => '';
    }
    const vnode = render();
    return await renderVNode(vnode, instance);
  } finally {
    currentInstance = prev;
  }
}

export async function renderVNode(vnode, instance = null) {
  if (typeof vnode === 'string' || typeof vnode === 'number') {
    return escapeHtml(vnode);
  }
  if (Array.isArray(vnode)) {
    const parts = [];
    for (const child of vnode) {
      parts.push(await renderVNode(child, instance));
    }
    return parts.join('');
  }
  if (!vnode) return '';

  if (typeof vnode.type === 'object' || typeof vnode.type === 'function') {
    return renderComponentWithContext(vnode.type, vnode.props, instance, vnode.children);
  }

  const tag = vnode.type;
  let propsStr = '';
  if (vnode.props) {
    for (const key of Object.keys(vnode.props)) {
      const val = vnode.props[key];
      if (key === 'class') {
        propsStr += ' class="' + escapeHtml(val) + '"';
      } else if (key === 'style' && typeof val === 'object') {
        const styleStr = Object.keys(val).map((k) => k + ':' + val[k]).join(';');
        propsStr += ' style="' + escapeHtml(styleStr) + '"';
      } else if (typeof val === 'string' || typeof val === 'number') {
        propsStr += ' ' + key + '="' + escapeHtml(val) + '"';
      }
    }
  }

  let childrenStr = '';
  if (Array.isArray(vnode.children)) {
    for (const c of vnode.children) {
      childrenStr += await renderVNode(c, instance);
    }
  } else if (typeof vnode.children === 'object' && vnode.children.__v_isVNode) {
    childrenStr = await renderVNode(vnode.children, instance);
  } else if (typeof vnode.children === 'string' || typeof vnode.children === 'number') {
    childrenStr = escapeHtml(vnode.children);
  }

  return '<' + tag + propsStr + '>' + childrenStr + '</' + tag + '>';
}

export function createSSRApp(rootComponent) {
  return {
    _component: rootComponent,
    async renderToString() {
      return renderComponentWithContext(this._component);
    }
  };
}
