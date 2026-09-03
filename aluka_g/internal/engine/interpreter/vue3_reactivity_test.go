package interpreter

import (
	"testing"
)

// TestVue3Reactivity 验证 Vue 3 响应式核心（Reactivity）在 Aluka VM 下的底层支持
func TestVue3Reactivity(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "reactive and effect basic",
			code: `
				const targetMap = new WeakMap();
				let activeEffect = null;

				function track(target, key) {
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

				function trigger(target, key) {
					const depsMap = targetMap.get(target);
					if (!depsMap) return;
					const dep = depsMap.get(key);
					if (dep) {
						dep.forEach(fn => fn());
					}
				}

				function effect(fn) {
					const e = () => {
						activeEffect = e;
						try {
							return fn();
						} finally {
							activeEffect = null;
						}
					};
					e();
					return e;
				}

				function reactive(target) {
					return new Proxy(target, {
						get(target, key, receiver) {
							track(target, key);
							const res = Reflect.get(target, key, receiver);
							return (typeof res === 'object' && res !== null) ? reactive(res) : res;
						},
						set(target, key, value, receiver) {
							const oldVal = target[key];
							const res = Reflect.set(target, key, value, receiver);
							if (oldVal !== value) {
								trigger(target, key);
							}
							return res;
						}
					});
				}

				const state = reactive({ count: 1, info: { text: "hello" } });
				let log = "";
				effect(() => {
					log += state.count + ":" + state.info.text + ";";
				});

				state.count = 2;
				state.info.text = "world";
				return log;
			`,
			want: "1:hello;2:hello;2:world;",
		},
		{
			name: "ref and computed",
			code: `
				const targetMap = new WeakMap();
				let activeEffect = null;

				function track(target, key) {
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

				function trigger(target, key) {
					const depsMap = targetMap.get(target);
					if (!depsMap) return;
					const dep = depsMap.get(key);
					if (dep) {
						dep.forEach(fn => {
							if (fn.scheduler) {
								fn.scheduler();
							} else {
								fn();
							}
						});
					}
				}

				const effectStack = [];
				function effect(fn, options = {}) {
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

				function ref(raw) {
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

				function computed(getter) {
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

				const queue = new Set();
				function queueJob(job) {
					queue.add(job);
				}
				function flushJobs() {
					for (const job of Array.from(queue)) {
						job();
					}
					queue.clear();
				}

				const count = ref(2);
				const double = computed(() => count.value * 2);
				const quadruple = computed(() => double.value * 2);

				let history = "";
				let runner;
				runner = effect(() => {
					history += double.value + "," + quadruple.value + ";";
				}, {
					scheduler: () => queueJob(runner)
				});

				count.value = 5;
				flushJobs();
				return history;
			`,
			want: "4,8;10,20;",
		},
		{
			name: "reactive array operations",
			code: `
				const targetMap = new WeakMap();
				let activeEffect = null;

				function track(target, key) {
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

				function trigger(target, key) {
					const depsMap = targetMap.get(target);
					if (!depsMap) return;
					const dep = depsMap.get(key);
					if (dep) {
						dep.forEach(fn => fn());
					}
				}

				function effect(fn) {
					const e = () => {
						activeEffect = e;
						try {
							return fn();
						} finally {
							activeEffect = null;
						}
					};
					e();
					return e;
				}

				function reactive(target) {
					return new Proxy(target, {
						get(target, key, receiver) {
							track(target, key);
							return Reflect.get(target, key, receiver);
						},
						set(target, key, value, receiver) {
							const oldLen = Array.isArray(target) ? target.length : 0;
							const hadKey = Object.prototype.hasOwnProperty.call(target, key);
							const oldVal = target[key];
							const res = Reflect.set(target, key, value, receiver);
							if (!hadKey) {
								trigger(target, key);
								if (Array.isArray(target) && target.length !== oldLen) {
									trigger(target, 'length');
								}
							} else if (oldVal !== value) {
								trigger(target, key);
							}
							return res;
						}
					});
				}

				const list = reactive([1, 2]);
				let logs = [];
				effect(() => {
					logs.push("len=" + list.length + ":[" + list.join(",") + "]");
				});

				list.push(3);
				return logs.join("|");
			`,
			want: "len=2:[1,2]|len=3:[1,2,3]",
		},
		{
			name: "toRaw and isReactive with ReactiveFlags",
			code: `
				const ReactiveFlags = {
					IS_REACTIVE: '__v_isReactive',
					RAW: '__v_raw'
				};

				const reactiveMap = new WeakMap();

				function reactive(target) {
					if (typeof target !== 'object' || target === null) {
						return target;
					}
					if (target[ReactiveFlags.RAW]) {
						return target;
					}
					const existingProxy = reactiveMap.get(target);
					if (existingProxy) {
						return existingProxy;
					}
					const proxy = new Proxy(target, {
						get(target, key, receiver) {
							if (key === ReactiveFlags.IS_REACTIVE) {
								return true;
							}
							if (key === ReactiveFlags.RAW) {
								return target;
							}
							const res = Reflect.get(target, key, receiver);
							return (typeof res === 'object' && res !== null) ? reactive(res) : res;
						}
					});
					reactiveMap.set(target, proxy);
					return proxy;
				}

				function isReactive(value) {
					return !!(value && value[ReactiveFlags.IS_REACTIVE]);
				}

				function toRaw(observed) {
					const raw = observed && observed[ReactiveFlags.RAW];
					return raw ? toRaw(raw) : observed;
				}

				const rawObj = { a: 1 };
				const proxyObj = reactive(rawObj);
				const proxyObjAgain = reactive(rawObj);

				const isSameProxy = (proxyObj === proxyObjAgain);
				const isObsReactive = isReactive(proxyObj);
				const isRawReactive = isReactive(rawObj);
				const unwrapped = (toRaw(proxyObj) === rawObj);

				return isSameProxy + "," + isObsReactive + "," + isRawReactive + "," + unwrapped;
			`,
			want: "true,true,false,true",
		},
		{
			name: "getter receiver binding",
			code: `
				const raw = {
					_val: 'hello',
					get text() {
						return this._val;
					}
				};

				const proxy = new Proxy(raw, {
					get(target, key, receiver) {
						return Reflect.get(target, key, receiver);
					}
				});

				const child = Object.create(proxy);
				child._val = 'world';

				return proxy.text + "->" + child.text;
			`,
			want: "hello->world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vmEvalStr(t, tt.code)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
