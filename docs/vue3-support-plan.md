# Vue 3 框架支持技术方案与实施规划

> 文档状态：规划与实施中 ｜ 评估版本：Vue 3.5.x ｜ 引擎基座：Aluka (纯 Go)

本文档规划 Aluka 运行时对 Vue 3 框架生态的全面支持路径，涵盖响应式系统、服务端渲染（SSR）、SFC 模板编译以及自动化一致性测试。

---

## 1. 目标与范围

支持 Vue 3 在 Aluka 上的三层运行场景：

1. **第一层：响应式核心与状态机（Reactivity & Runtime Core）**
   - `@vue/reactivity`（`reactive`, `ref`, `computed`, `effect`, `watch`, `watchEffect`）
   - 依赖收集与触发、`WeakMap` 弱引用映射与内存安全、`Proxy` 与 `Reflect` 深度联动。

2. **第二层：组件系统与服务端渲染（SSR）**
   - `createSSRApp` + `@vue/server-renderer`（`renderToString`, `renderToNodeStream`, `renderToWebStream`）
   - 组件生命周期、`setup` 函数、`provide`/`inject`、插槽（Slots）在 SSR 环境下的正确求值。
   - `AsyncLocalStorage`（`node:async_hooks`）请求隔离。

3. **第三层：SFC 模板编译与开发生态（SFC & Compiler）**
   - `@vue/compiler-sfc` / `@vue/compiler-dom` 对 `<template>`, `<script setup>`, `<style>` 的解析与渲染函数生成。
   - npm 包管理安装 `vue` 依赖树与条件导出（Subpath Exports）解析。

---

## 2. 核心架构与底层依赖矩阵

| 模块 | 核心依赖特性 | 对应 Aluka 底层组件 | 状态 |
| :--- | :--- | :--- | :---: |
| **@vue/reactivity** | `Proxy` (get/set/has/deleteProperty/ownKeys), `Reflect`, `WeakMap`, `Set`, `Symbol` | `internal/engine/interpreter/proxy.go`, `reflect.go`, `shape.go`, `gc.go` | ✅ 基础完备 |
| **@vue/runtime-core** | 微任务异步调度器 (`queueMicrotask` / `Promise`), `Object.is`, 原型链 | `internal/engine/interpreter/promise.go`, `vm.go` | ✅ 完备 |
| **@vue/server-renderer** | Web Streams / Node Streams, `AsyncLocalStorage`, Buffer, HTML 编码 | `internal/runtime/globals/streams.go`, `internal/builtin/async_hooks.go` | ✅ 完备 |
| **@vue/compiler-sfc** | 正则回溯、命名捕获、Unicode 匹配、AST 生成 | `internal/engine/regex/`, `parser/` | ✅ 完备 |
| **包管理分发** | `package.json` 的 `exports` 条件导出与 Monorepo 依赖解析 | `internal/runtime/module/`, `internal/pkgmanager/` | ✅ 基础完备 |

---

## 3. 实施与验证步骤

### Phase 1: 响应式核心能力与边界验证
- 验证 `Proxy` 对嵌套对象代理、Getter `receiver` 绑定、`Array` 索引变异（`push`, `splice`, `length`）的拦截正确性。
- 验证 `WeakMap` 与垃圾回收（GC）在长生命周期下的行为。
- 构建包含 `reactive`, `ref`, `computed`, `effect` 的综合单元测试。

### Phase 2: Vue 3 SSR 端到端验证
- 引入 Vue 3 SSR 渲染器，执行组件渲染为 HTML 字符串：
  ```javascript
  import { createSSRApp, h, ref } from 'vue';
  import { renderToString } from 'vue/server-renderer';
  ```
- 验证带有响应式状态、计算属性和子组件的 SSR 输出一致性。

### Phase 3: SFC 模板编译验证
- 验证 `@vue/compiler-sfc` 编译 `<template><div>{{ msg }}</div></template>` 产出正确的 JS render 函数。

### Phase 4: 自动化一致性测试与 Demo
- 创建 `tests/conformance/vue3/` 或 `demo/vue3-ssr-demo/` 作为长期回归套件。
