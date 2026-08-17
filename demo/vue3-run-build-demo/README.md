# Vue 3：`aluka run` + `aluka build` 校验台

同一份 `src/App.vue`（官方 **vue@3.5.13**）验证两条通道；web 构建选项由项目根 `aluka.config.js` 提供（也可用 `vite.config.*` / `vue.config.js`，见下方）。

| 命令 | 做什么 |
|---|---|
| `aluka run verify.ts` | 运行时执行 `vue/compiler-sfc`，SSR 渲染 `.vue` |
| `aluka run server.ts` | `Aluka.serve` 提供 SSR HTML |
| `aluka build --target=web index.html` | 读 `aluka.config.js` → official SFC + minify + `dist/assets/*` |
| `aluka build --compile` | 单文件可执行：vue runtime SSR（不含 compiler-sfc） |

Vue 依赖通过 `tsconfig.json` `paths` 指向仓库已 vendored 的 `demo/web-bundle-vue-demo/node_modules/vue`，无需再装一份 fixture。入口用 `@/App.vue`（`aluka.config.js` 的 `alias`）。

## 运行时

在仓库根，或 `cd demo/vue3-run-build-demo` 后把 `aluka` 放进 `PATH`：

```bash
# SSR 断言（应打印 VERIFY_OK）
./bin/aluka run demo/vue3-run-build-demo/verify.ts

# HTTP：http://127.0.0.1:3040/
./bin/aluka run demo/vue3-run-build-demo/server.ts
```

`src/App.vue` 使用 `<script setup>` / `v-if` / `computed` / `<style scoped>`。scoped CSS 由纯 Go 选择器后缀生成（与 Vite 默认 `data-v-*` 属性选择器一致），不调用 `compileStyle`。`:deep` / `:slotted` / `:global` / `v-bind()` 仍会构建期报错。

## Web bundle

`aluka.config.js` 已声明 `vueCompiler: "official"`、`minify: true`、`outDir: "dist"`、`alias`、`define`，以及示例插件（注入 HTML meta、写出 `plugin-manifest.json`）。因此不必再手写一长串 flag：

```bash
# 仓库根：outDir 相对项目根解析 → demo/vue3-run-build-demo/dist
./bin/aluka build --target=web demo/vue3-run-build-demo/index.html

# 或进入项目目录（npm scripts 假设 PATH 里有 aluka）
cd demo/vue3-run-build-demo
aluka build --target=web index.html
```

浏览器打开 `demo/vue3-run-build-demo/dist/index.html`。

产物对齐 Vite `npm run build`：

- JS/CSS → `dist/assets/<name>-<hash>.js`
- `index.html`：`crossorigin` + 静态图 `modulepreload`
- 默认 ESM 原生 `import`/`export`（无 `__def`/`__req`；`--format=cjs|umd` 仍走 wrap）

CLI 仍可覆盖配置，例如 `--outdir /tmp/out` 优先于 `outDir`。

### 项目配置（动态发现）

`--target=web` / `aluka dev` 在 Aluka VM 里跑发现脚本（信任模型同 official `compiler-sfc`）。Go 只执行脚本并套用归一字段，不写死 `vite.config.ts` 文件名。

查找顺序：

1. `ALUKA_WEB_CONFIG` 自定义发现脚本（须导出 `loadWebConfigJSON(root)`）
2. 否则内置默认脚本：优先 `aluka.config.*`；否则扫描 `*.config.(js|ts|…)`，按对象形态识别
3. 无关配置（如 `jest.config.js`）跳过；钩子或已识别打包配置加载失败会报错

本示例使用 `aluka.config.js`（含 `plugins: [demoPlugin()]`）。等价 Vite 形态（删除钩子后由默认脚本识别）示例：

```js
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()], // → vueCompiler: official；自定义钩子见 aluka.config.js
  resolve: { alias: { '@': './src' } },
  build: { outDir: 'dist', assetsDir: 'assets', minify: true },
  define: { __APP_BUILD__: JSON.stringify('aluka-web') },
});
```

已支持的 Vite 同名钩子（无 HMR）：`config` / `configResolved` / `buildStart` / `resolveId` / `load` / `transform` / `transformIndexHtml` / `generateBundle` / `writeBundle` / `closeBundle`。

开发 watch：

```bash
./bin/aluka build --target=web --watch demo/vue3-run-build-demo/index.html
```

## `--compile` 可执行产物

```bash
./bin/aluka build --compile \
  --outfile demo/vue3-run-build-demo/dist/verify.exe \
  demo/vue3-run-build-demo/verify-compile.ts
./demo/vue3-run-build-demo/dist/verify.exe
```

这条路径只打包 `vue` + `vue/server-renderer`（`h()` 组件），避免把 compiler-sfc 打进可执行文件。`.vue` 编译属于 `aluka run` / `--target=web`。

## 目录

```
demo/vue3-run-build-demo/
  aluka.config.js      web 构建钩子（outDir / alias / define / vueCompiler）
  src/App.vue          唯一 SFC 源（script setup + v-if + scoped）
  src/styles.css       页面底色
  load-sfc.ts          运行时 compiler-sfc
  ssr.ts / verify.ts / server.ts
  verify-compile.ts    --compile 用的 runtime-only SSR
  main.ts / index.html web 入口（@/App.vue）
  tsconfig.json        paths → @/* + vue fixture
```
