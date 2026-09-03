// Vue 3 核心响应式系统 (Reactivity)
const targetMap = new WeakMap();
let activeEffect = null;
const effectStack = [];

const ReactiveFlags = {
  IS_REACTIVE: '__v_isReactive',
  RAW: '__v_raw'
};

const reactiveMap = new WeakMap();

export function track(target, key) {
  if (!activeEffect) return;
  let depsMap = targetMap.get(target);
  if (!depsMap) {
    depsMap = new Map();
    targetMap.set(target, depsMap);
  }
  let dep = depsMap.get(key);
  if (!dep) {
    dep = new Set();
    depsMap.set(key, dep);
  }
  dep.add(activeEffect);
}

export function trigger(target, key) {
  const depsMap = targetMap.get(target);
  if (!depsMap) return;
  const dep = depsMap.get(key);
  if (dep) {
    dep.forEach((fn) => {
      if (fn.scheduler) {
        fn.scheduler();
      } else {
        fn();
      }
    });
  }
}

export function effect(fn, options = {}) {
  const e = () => {
    if (!effectStack.includes(e)) {
      try {
        effectStack.push(e);
        activeEffect = e;
        return fn();
      } finally {
        effectStack.pop();
        activeEffect = effectStack.length > 0 ? effectStack[effectStack.length - 1] : null;
      }
    }
  };
  e.scheduler = options.scheduler;
  if (!options.lazy) {
    e();
  }
  return e;
}

export function reactive(target) {
  if (typeof target !== 'object' || target === null) {
    return target;
  }
  if (target[ReactiveFlags.RAW]) {
    return target;
  }
  const existing = reactiveMap.get(target);
  if (existing) {
    return existing;
  }
  const proxy = new Proxy(target, {
    get(target, key, receiver) {
      if (key === ReactiveFlags.IS_REACTIVE) return true;
      if (key === ReactiveFlags.RAW) return target;
      track(target, key);
      const res = Reflect.get(target, key, receiver);
      return typeof res === 'object' && res !== null ? reactive(res) : res;
    },
    set(target, key, value, receiver) {
      const hadKey = Object.prototype.hasOwnProperty.call(target, key);
      const oldVal = target[key];
      const res = Reflect.set(target, key, value, receiver);
      if (!hadKey) {
        trigger(target, key);
      } else if (oldVal !== value) {
        trigger(target, key);
      }
      return res;
    }
  });
  reactiveMap.set(target, proxy);
  return proxy;
}

export function ref(raw) {
  const wrapper = {
    _isRef: true,
    _val: raw,
    get value() {
      track(wrapper, 'value');
      return this._val;
    },
    set value(newVal) {
      if (this._val !== newVal) {
        this._val = newVal;
        trigger(wrapper, 'value');
      }
    }
  };
  return wrapper;
}

export function computed(getter) {
  let dirty = true;
  let value;
  let runner;
  const computedObj = {
    _isRef: true,
    get value() {
      if (dirty) {
        value = runner();
        dirty = false;
      }
      track(computedObj, 'value');
      return value;
    }
  };
  runner = effect(getter, {
    lazy: true,
    scheduler: () => {
      if (!dirty) {
        dirty = true;
        trigger(computedObj, 'value');
      }
    }
  });
  return computedObj;
}

export function isReactive(v) {
  return !!(v && v[ReactiveFlags.IS_REACTIVE]);
}

export function toRaw(observed) {
  const raw = observed && observed[ReactiveFlags.RAW];
  return raw ? toRaw(raw) : observed;
}
