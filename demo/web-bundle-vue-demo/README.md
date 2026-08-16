# Aluka Web Bundle Demo — Vue SFC

自包含示例：用 **`.vue` 单文件组件** 演示 `aluka build --target=web`，
无任何 npm 依赖。

## 演示内容

- **`.vue` 单文件组件**：`components/Counter.vue` / `StatCard.vue` 使用
  `<template>` + `<script>` 写法，构建期由 `internal/bundler/vue` 编译为
  JS 模块（template → `render(ctx)`，无需运行时编译器）
- **迷你 Vue shim**（`vue.ts`）：`ref` / `computed` / `watchEffect` 响应式
  核心 + `h()` vdom + 浏览器挂载 / 字符串渲染；模板语境 ref 自动解包
- **组件状态**：Counter 自持 `ref`/`computed` 状态（setup + render 选项组件）；
  StatCard 为纯展示组件（无 setup 时模板直接消费 props）
- **动态 import 拆包**：`lib/heavy-data.ts` 拆为独立 chunk，点击按钮按需加载
- **CSS / HTML 入口**：`styles.css` 随构建拷贝，`<script src="./main.ts">`
  自动改写为产物路径

## 模板语法子集（v1）

| 支持 | 形式 |
|---|---|
| 元素 | 嵌套 / 自闭合 `/>` / void 元素（br、img…） |
| 静态属性 | `class="btn"` |
| 绑定 | `:href="url"`（表达式，裸标识符重写为 `ctx.<id>`） |
| 事件 | `@click="dec"`（方法引用）或 `@click="inc()"`（调用表达式） |
| 插值 | `{{ count }}`（ref 自动解包） |

暂不支持（构建期明确报错）：`<style>` 块（样式放入口 CSS）、
`<script setup>`、指令（`v-if`/`v-for`/`v-model`）。
空白处理为 Vue condense 近似（换行分隔删除、内联空白折叠）。

> 另见 `demo/vue3-ssr-demo`：完整版迷你 Vue（reactive/Proxy/模板编译/SSR），
> 演示 aluka 运行时；本 demo 演示 aluka **web bundler** 的 SFC 支持。

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
  const m = await import(pathToFileURL('dist/main.js'));
  console.log(m.renderApp());          // 首帧 HTML（Counter 计数 0）
  const ctx = m.Counter.setup();       // SFC 组件选项对象
  ctx.inc(); ctx.inc();
  console.log(m.renderToString(m.Counter.render(ctx))); // 计数 2、x2 = 4
  await m.loadStatsOnce();             // 触发动态 chunk
  console.log(m.renderApp());          // 出现 StatCard
})"
```

> 入口含 `typeof document` 守卫，Node 下导入不会触碰 DOM。

## 多格式输出

```bash
go run ./cmd/aluka build --target=web --format=cjs --outfile dist/main.cjs demo/web-bundle-vue-demo/main.ts
go run ./cmd/aluka build --target=web --format=umd --global-name=VueBundleDemo --outfile dist/main.umd.js demo/web-bundle-vue-demo/main.ts
```
