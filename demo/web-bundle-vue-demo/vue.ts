// 迷你 Vue 3 shim：组合式 API 核心（ref/computed/watchEffect）+ h() vdom。
// 仅为演示 aluka web bundler，非完整框架：
//   - 无 Proxy/reactive、无模板编译、无 DOM diff；
//   - 重渲染为粗粒度整树重建（watchEffect 捕获渲染期间读取的全部 ref）；
//   - 组件模型为无状态函数组件，响应式状态集中在根组件 setup。

let activeEffect = null;

// watchEffect：立即执行 fn，执行期间读取的 ref 变化时重新执行。
export function watchEffect(fn) {
  const run = () => {
    const prev = activeEffect;
    activeEffect = run;
    try {
      fn();
    } finally {
      activeEffect = prev;
    }
  };
  run();
  return run;
}

// ref：单值响应式容器（getter/setter 收集与触发依赖）。
export function ref(initial) {
  const deps = new Set();
  let value = initial;
  return {
    _isRef: true,
    get value() {
      if (activeEffect) deps.add(activeEffect);
      return value;
    },
    set value(next) {
      if (next === value) return;
      value = next;
      for (const fn of Array.from(deps)) fn();
    },
  };
}

// computed：脏标记惰性求值；上游 ref 变化置脏并通知读取方。
export function computed(getter) {
  const subs = new Set();
  let value;
  let dirty = true;
  const invalidate = () => {
    if (dirty) return;
    dirty = true;
    for (const fn of Array.from(subs)) fn();
  };
  const evaluate = () => {
    const prev = activeEffect;
    activeEffect = invalidate;
    try {
      value = getter();
      dirty = false;
    } finally {
      activeEffect = prev;
    }
  };
  return {
    _isRef: true,
    get value() {
      if (activeEffect) subs.add(activeEffect);
      if (dirty) evaluate();
      return value;
    },
  };
}

// h：vnode 构造（children 统一为数组）。
export function h(type, props, children) {
  const kids = children == null ? [] : Array.isArray(children) ? children : [children];
  return { type, props: props || {}, children: kids };
}

// createApp：浏览器挂载。render 包进 watchEffect —— render 期间读取的
// ref 变化即整树重建（事件监听随节点重建重新绑定）。
export function createApp(component, sharedCtx) {
  return {
    mount(selector) {
      const container = typeof selector === 'string' ? document.querySelector(selector) : selector;
      const ctx = sharedCtx || (component.setup ? component.setup() : {});
      watchEffect(() => {
        container.innerHTML = '';
        container.appendChild(buildDOM(component.render(ctx)));
      });
      return ctx;
    },
  };
}

// 组件解析：无状态函数组件直接调用；选项组件（.vue SFC 编译产物，
// { setup, render } 对象）缓存实例后执行 render。
// v1 限制：同一组件的多实例共享同一份状态（demo 规模足够）。
const instanceCache = new Map();

function resolveVNode(type, props) {
  if (typeof type === 'function') return type(props);
  if (type && typeof type.render === 'function') {
    if (!instanceCache.has(type)) {
      const ctx = typeof type.setup === 'function' ? type.setup(props) : props;
      instanceCache.set(type, ctx);
    }
    return type.render(instanceCache.get(type));
  }
  return null;
}

// unwrapRef：模板/绑定展示语境的 ref 自动解包（对齐 Vue 模板语义）。
function unwrapRef(v) {
  return v && v._isRef ? v.value : v;
}
// buildDOM：vnode → 真实 DOM（组件展开；on* 绑定事件；ref 解包）。
function buildDOM(vnode) {
  if (vnode == null || vnode === false || vnode === true) return document.createTextNode('');
  if (typeof vnode === 'string' || typeof vnode === 'number') return document.createTextNode(String(vnode));
  if (Array.isArray(vnode)) {
    const frag = document.createDocumentFragment();
    for (const child of vnode) frag.appendChild(buildDOM(child));
    return frag;
  }
  if (vnode._isRef) return buildDOM(unwrapRef(vnode));
  if (typeof vnode.type === 'function' || (vnode.type && typeof vnode.type.render === "function")) {
    return buildDOM(resolveVNode(vnode.type, vnode.props));
  }
  const el = document.createElement(vnode.type);
  for (const key of Object.keys(vnode.props || {})) {
    const val = vnode.props[key];
    if (key === 'children') continue;
    if (key.startsWith('on') && typeof val === 'function') {
      el.addEventListener(key.slice(2).toLowerCase(), val);
    } else {
      el.setAttribute(key === 'className' ? 'class' : key, unwrapRef(val));
    }
  }
  for (const child of vnode.children) el.appendChild(buildDOM(child));
  return el;
}

// renderToString：vnode → HTML 字符串（跳过事件句柄；ref 解包；Node 验证用）。
export function renderToString(node) {
  if (node == null || node === false || node === true) return '';
  if (typeof node === 'string' || typeof node === 'number') return escapeHtml(String(node));
  if (Array.isArray(node)) return node.map(renderToString).join('');
  if (node._isRef) return renderToString(unwrapRef(node));
  if (typeof node.type === 'function' || (node.type && typeof node.type.render === "function")) {
    return renderToString(resolveVNode(node.type, node.props));
  }
  let attrs = '';
  for (const key of Object.keys(node.props || {})) {
    const val = node.props[key];
    if (key === 'children' || (key.startsWith('on') && typeof val === 'function')) continue;
    attrs += ' ' + (key === 'className' ? 'class' : key) + '="' + escapeHtml(String(unwrapRef(val))) + '"';
  }
  return '<' + node.type + attrs + '>' + renderToString(node.children) + '</' + node.type + '>';
}

function escapeHtml(text) {
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
