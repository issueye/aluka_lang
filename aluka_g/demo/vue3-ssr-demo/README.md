# Vue 3 SSR + Tailwind CSS 示例项目 (Aluka 运行时)

本项目展示了在 **Aluka（纯 Go 自研 JS/TS 运行时）** 上运行现代 Vue 3 + Tailwind CSS 的全链路能力，包含：

- **Vue 3 响应式系统（Reactivity）**：基于 `Proxy`、`Reflect`、`WeakMap` 的依赖收集（`track`）与派发更新（`trigger`），支持 `ref`、`reactive` 与惰性求值的 `computed`。
- **虚拟 DOM 与服务端渲染（SSR）**：`createSSRApp` + `renderToString`，支持异步组件渲染为 HTML 字符串，配合插槽（Slots）与 `provide` / `inject` 依赖注入。
- **Tailwind CSS JIT 即时编译器**：服务端渲染出 HTML 后，JIT 引擎自动扫描提取用到的原子类（如 `bg-slate-950`、`text-sky-400`、`rounded-2xl`、`shadow-xl`、`grid-cols-2` 等），即时生成最小化原子 CSS 样式表并注入页面 `<head>`。
- **单文件组件（SFC）动态编译**：读取 `.vue` 单文件组件，解析 `<template>` / `<script setup>` / `<style scoped>`，并通过 `new Function` 动态编译出可执行的 JS `render(_ctx)` 函数。

---

## 📁 目录结构

```
demo/vue3-ssr-demo/
├── src/
│   ├── reactivity.js        # Vue 3 响应式核心（reactive, ref, computed, effect）
│   ├── vdom.js              # 虚拟 DOM、依赖注入与 SSR 渲染器 (h, renderToString)
│   ├── compiler.js          # SFC 解析与模板动态编译 (parseSFC, compileTemplate)
│   ├── tailwind.js          # Tailwind CSS JIT 核心生成器 (generateTailwindCSS)
│   └── components/
│       ├── App.js           # 根组件（Tailwind CSS 现代化暗色系 UI 布局）
│       ├── Card.js          # 卡片子组件（Slots 插槽、Tailwind 工具类）
│       └── UserCard.vue     # .vue 单文件组件（Tailwind CSS 样式）
├── app.js                   # Web 服务入口（http.createServer 提供 SSR + Tailwind 页面）
├── test.js                  # 全链路自动化验证脚本
├── package.json
└── README.md
```

---

## 🚀 快速运行与体验

使用 Aluka 运行时直接执行：

### 1. 运行自动化测试
```bash
aluka demo/vue3-ssr-demo/test.js
```

### 2. 启动 HTTP 服务
```bash
aluka demo/vue3-ssr-demo/app.js
```

服务启动后，可在浏览器中访问：
- **主页**：[http://127.0.0.1:3001/](http://127.0.0.1:3001/) —— 查看 Vue 3 SSR + Tailwind CSS 渲染的完整页面；
- **SFC 动态编译页**：[http://127.0.0.1:3001/sfc](http://127.0.0.1:3001/sfc) —— 查看服务端实时编译渲染的 `UserCard.vue`；
- **生成的 Tailwind CSS**：[http://127.0.0.1:3001/tailwind-css](http://127.0.0.1:3001/tailwind-css) —— 查看服务端 JIT 即时生成的原子化 CSS 样式表；
- **状态 API**：[http://127.0.0.1:3001/api/state](http://127.0.0.1:3001/api/state) —— 返回响应式状态 JSON 快照。
