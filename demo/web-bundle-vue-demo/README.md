# Aluka Web Bundle Demo — Vue SFC

自包含示例：用 **`.vue` 单文件组件** 和官方 **Vue 3.5.13** 演示
`aluka build --target=web`。Vue runtime 与 SSR 依赖作为离线 fixture 随仓库分发，
构建和验证不需要访问 npm registry。

## 架构（Vite 式）

```
.vue 源码 ──aluka SFC 编译器──▶ import { h, toDisplayString, unref } from 'vue'
                                   + render(_ctx) 调用 _h(...)
                                          │
                              'vue' 经 node_modules 正常解析
                                          │
                   node_modules/vue/ + @vue/*（官方 3.5.13 离线 fixture）
```

- **编译器不内嵌运行时**：`internal/bundler/vue` 只产出「import 运行时
  helper 的代码」；`vue` 与 `vue/server-renderer` 由普通 package resolver
  从 demo 的 `node_modules` 解析，依赖版本固定在 `package.json`
- **vnode 形状由运行时 `h()` 唯一定义**：编译产物调用 `_h(...)` 构造，
  不手写数据结构；插值经 `_toDisplayString(_unref(...))` 展示转换
- **组件挂接用 Vite 同款模式**：`const __sfc__ = ...` +
  `__sfc__.render = render` + `export default __sfc__`
- SFC 编译器后续演进只改变所调用的 helper，不与任何内嵌实现锁版本

## 演示内容

- **`.vue` 组件**：`components/Counter.vue`（自持 `ref`/`computed` 状态）、
  `components/StatCard.vue`（纯展示，无 setup 时模板直接消费 props）
- **真实 Vue 3.5.13**：`ref` / `computed` 响应式、`h` vnode、
  `createSSRApp` 浏览器挂载及 `vue/server-renderer` 的 `renderToString`
- **动态 import 拆包**：`lib/heavy-data.ts` 拆为独立 chunk，点击按钮按需加载
- **CSS / HTML 入口**：`styles.css` 随构建拷贝，`<script src="./main.ts">`
  自动改写为产物路径

## SFC 编译后端

Aluka 提供两个后端，均输出从 `vue` 导入运行时 helper 的 ESM 模块：

| 后端 | 启用方式 | 支持范围 | 特点 |
|---|---|---|---|
| `subset` | 默认，或 `--vue-compiler=subset` | 本文“模板语法子集（v1）” | 纯 Go，约微秒级/SFC；不执行依赖代码，超出子集明确报错 |
| `official` | `--vue-compiler=official` | 官方 script/template 编译（含 `<script setup>`、TypeScript、named exports、指令、官方模板优化）；`<script src>`/`<template src>`/`<style>`（纯 CSS，含 scoped）已接入 graph CSS 管线；custom block / 预处理器 / CSS modules 明确拒绝 | 在 Aluka 自研 VM 内执行 vendored compiler-sfc；无需 Node/外部工具链 |

```bash
# 官方 compiler-sfc 后端
go run ./cmd/aluka build --target=web --vue-compiler=official \
  --outdir demo/web-bundle-vue-demo/dist demo/web-bundle-vue-demo/index.html
```

> `official` 构建期会执行项目 `node_modules` 中的 compiler-sfc 及其依赖代码，
> 权限与 `aluka run` 相同；只对可信依赖启用。失败直接报错，禁止静默回退
> `subset`（两者产物语义不同）。

本机基线（Windows amd64，i5-13420H，2026-08-16）：subset CLI 构建中位
约 173ms；official CLI 冷构建中位约 1.73s（约 10x，主要是首次加载/
解析 compiler 依赖链）；同一 VM 内 official 热 Transform 约 10–15ms/SFC，
subset 约 7–9µs/SFC（`go test ./internal/bundler/vue -run '^$' -bench
'Benchmark(SubsetTransform|OfficialTransformWarm)'`）。主 bundle
328,454B → 329,193B，仅 +739B（+0.23%）。

## 模板语法子集（v1）

| 支持 | 形式 |
|---|---|
| 元素 | 嵌套 / 自闭合 `/>` / void 元素（br、img…） |
| 静态属性 | `class="btn"` |
| 绑定 | `:href="url"`（表达式，裸标识符重写为 `_ctx.<id>`，经 `_unref` 解包） |
| 事件 | `@click="dec"`（方法引用）或 `@click="inc()"`（调用表达式） |
| 插值 | `{{ count }}`（经 `_toDisplayString(_unref(...))` 展示） |

暂不支持（构建期明确报错）：`<script setup>`（subset）、指令（`v-if`/`v-for`/`v-model`，subset）、
custom block、`<style lang>` 预处理器、`<style module>`。`<style>` 纯 CSS（含 scoped）已支持。
空白处理为 Vue condense 近似（换行分隔删除、内联空白折叠）。

> 另见 `demo/vue3-ssr-demo`：完整版迷你 Vue（reactive/Proxy/模板编译/SSR），
> 演示 aluka 运行时；本 demo 演示 aluka **web bundler** 的 SFC 支持
> （编译产物 import `vue` 包 helper，Vite 同构方案）。

## 快速开始

仓库根目录执行：

```bash
# 构建（产物到 demo/web-bundle-vue-demo/dist/）
go run ./cmd/aluka build --target=web --outdir demo/web-bundle-vue-demo/dist demo/web-bundle-vue-demo/index.html

# 浏览器打开
demo/web-bundle-vue-demo/dist/index.html
```

产物结构：

```
dist/
  index.html        # script 引用已改写为 main.js
  main.js           # 单文件 ESM bundle（含编译后的 .vue 组件）
  chunk-xxxxxxxx.js # 动态 import 拆出的 heavy-data chunk
  styles.css
```

页面交互：

- `+` / `-` 按钮修改 Counter 内部 `ref` 计数，`computed` 倍数与整树自动重渲染
- 「加载动态 chunk」按钮触发 `import('./lib/heavy-data.ts')`，按需请求 chunk

## 开发模式

```bash
# watch：源文件（含 .vue）变更后全量重建
go run ./cmd/aluka build --target=web --watch --outdir demo/web-bundle-vue-demo/dist demo/web-bundle-vue-demo/index.html

# dev server：静态服务 + SPA fallback + SSE 热重载端点
go run ./cmd/aluka dev --port 3000 --outdir demo/web-bundle-vue-demo/dist demo/web-bundle-vue-demo/index.html
```

## Node 中验证产物

```bash
node --input-type=module -e "import('url').then(async ({pathToFileURL}) => {
  const m = await import(pathToFileURL('demo/web-bundle-vue-demo/dist/main.js'));
  console.log(await m.renderApp());     // 首帧 HTML（Counter 计数 0）
  console.log(await m.loadStatsOnce()); // 触发动态 chunk
  console.log(await m.renderApp());     // 出现 StatCard
})"
```

> 入口含 `typeof document` 守卫，Node 下导入不会触碰 DOM。

## 多格式输出

```bash
go run ./cmd/aluka build --target=web --format=cjs --outfile dist/main.cjs demo/web-bundle-vue-demo/main.ts
go run ./cmd/aluka build --target=web --format=umd --global-name=VueBundleDemo --outfile dist/main.umd.js demo/web-bundle-vue-demo/main.ts
```
