# Aluka 静态构建开发计划 —— `aluka build --target=web` 与 SSG 开箱

> 文档版本：v1.0 ｜ 日期：2026-08-15
> 前置输入：[静态构建探索文档](./static-build-exploration.md)（能力空间与方案决策）
> 参照惯例：[build-compile-plan.md](./build-compile-plan.md)（Phase 7 / M1 已交付 `--compile`）
> 验收基线：每个里程碑 `CGO_ENABLED=0 go test ./...` 全绿 + 新增对拍/一致性测试

---

## 1. 总目标

1. **Web Bundle**：`aluka build --target=web --entry src/index.ts [--outdir dist]`
   产出浏览器可用的 ESM bundle（JS/TS/JSX 零配置，tree-shaking + minify 生效）
2. **SSG 开箱**：`aluka` 上一条命令完成数据驱动静态站点构建与热重建
3. **GUI 闭环**：`aluka build --gui --web-entry src/index.tsx` 一条命令
   从源码到单文件桌面应用（前端由本 bundle 产出，替代外部 Vite 依赖）

## 2. 里程碑

### M1：JS bundle 最小闭环（核心：AST printer）

> 目标：单 entry、ESM 单文件输出、shake+minify 生效、JSX/TSX 零配置

| 项 | 内容 |
|----|------|
| M1-1 AST printer | `internal/bundler/emit/`：AST → JS 源码。ES2020 子集起步 + TS/JSX 已剥离后的节点；表达式/语句全量覆盖，格式不追求美化（minify 形态优先：无缩进、必要的括号） |
| M1-2 printer 对拍 | "printer → parser 回读 → AST 等价"差分测试；语料：tests/ 全部 *.ts/*.js + 固定语法矩阵（对标 zz_diff_test 传统） |
| M1-3 模块拼接 | esbuild 式作用域拼接：每模块 `__esm(init, fn)` 惰性 wrapper，import/export 改写为本地绑定；CJS 模块包 `__commonJS` wrapper |
| M1-4 CLI | `--target=web`（与 `--compile` 互斥）；默认无 `--compile` 时进入 web 路径（替换现有报错）；`--outdir dist`；`--minify/--no-minify` |
| M1-5 浏览器边界 | 构建期解析到 `node:*` 内置（fs/http 等）→ 明确报错 + 提示 polyfill；`process`/`Buffer` 可选注入最小 polyfill（`--polyfill`） |
| M1-6 验收 | 真实前端库冒烟：React（UMD 产物 re-export）+ 一个无构建依赖的 TS 组件库能正确打包并在浏览器执行（conformance 新增 `tests/conformance/webbuild/`）。**✅ 已完成**：`tests/conformance/webbuild/run.sh` 固定 React 18.3.1，执行真实 React bundle 与 TSX bundle smoke；Node ESM loader 作为无浏览器依赖的执行 oracle。 |

预估：printer 5-7 天（对拍驱动），其余 2-3 天。

### M2：CSS / HTML / 多入口 / sourcemap / GUI 合流

> 目标：CSS 拼接与 Minify、HTML 入口解析与改写、Sourcemap v3 外链、多 Entry 独立产出、GUI `--web-entry` 闭环

| 项 | 内容 |
|----|------|
| M2-1 CSS | `import "./x.css"`：自动抽取并伴随输出 `.css`，按依赖去重 + 纯 Go Minify（去注释/空白）；CSS entry（`--entry style.css`）输出单文件。**✅ 已完成**（`internal/bundler/emit/css.go`） |
| M2-2 HTML 入口 | `--entry index.html`：解析 `<script src>/<link href>` 引用并改写为产物路径；支持 JS/TSX 与 CSS 联动打包。**✅ 已完成**（`internal/bundler/emit/html.go`） |
| M2-3 多 entry + 公共块 | 多 entry 分别独立产出（`--outdir dist a.ts b.ts`），共享模块在 graph 中统一去重。**✅ 已完成** |
| M2-4 sourcemap | 纯 Go 实现 v3 规范 Base64-VLQ 编解码（文件/行级映射），`--sourcemap` 产出 `.map` 与 `sourceMappingURL` 注释。**✅ 已完成**（`internal/bundler/emit/sourcemap.go`） |
| M2-5 resolver browser 条件 | package.json `browser` 字符串/映射与 `exports["."].browser` 条件解析。**✅ 已完成**（`internal/runtime/module/resolver.go`） |
| M2-6 **GUI 合流** | `aluka build --gui --web-entry src/index.tsx`：前端直接 bundle 并注入桌面 exe 的虚拟资产，端到端 demo 落地于 `demo/web-gui/`。**✅ 已完成**（`cmd/aluka/build.go`） |

### M3：Code splitting 与开发体验

| 项 | 内容 |
|----|------|
| M3-1 动态 import 拆包 | `import()` 生成独立 chunk + 运行时加载器（graph 的动态依赖分析已有）。**✅ 已完成**：字面量动态 import 生成稳定 `*-chunk.js`，主 bundle 使用浏览器原生动态加载；非字面量动态 import 在 web 构建期拒绝。当前不做公共 chunk 提取。 |
| M3-2 watch 模式 | `--watch`：依赖图增量重建（graph 已有 per-unit 文件信息；mtime 失效）。**✅ 已完成（全量重建版）**：300ms 源文件快照轮询（排除输出目录防自触发），变更后全量重建并清理陈旧 chunk；构建失败保留进程继续等待。增量编译缓存后置。 |
| M3-3 dev server | `aluka dev`：静态服务 dist + 变更刷新（复用 builtin http server 能力）；GUI 场景联动 `--gui` 热重载（后置评估）。**✅ 已完成（基础版）**：`--host/--port/--outdir/--minify`；SPA fallback；`/__aluka/health` 返回最近构建错误；`/__aluka/reload` SSE 在重建成功后广播 reload。GUI 热重载后置。 |
| M3-4 输出格式 | `--format=cjs/umd` 按需；`--target=es2018` 语法降级（评估真实需求后决定是否做）。**◐ 部分完成**：`--format=esm/cjs/umd` + `--global-name`（标识符校验）已实现并经 Node 三分支（CommonJS/AMD-global/vm-global）验证，动态 chunk 在 CJS 下可用；`--target=es2018` 为明确拒绝（降级 pass 未实现，构建期报错）。 |

### M4：SSG 开箱

> 目标：纯 Go 内置 Markdown 渲染管线、开箱即用 SSG 站点示例与独立构建器打包

| 项 | 内容 |
|----|------|
| M4-1 脚手架 | 最小开箱 SSG 工程（content/posts + 模板 + build.ts）。**✅ 已完成**（`demo/ssg-site/`） |
| M4-2 Markdown 管线 | 内置纯 Go Markdown → HTML 渲染器与 Frontmatter 解析，作为 `aluka:markdown` 模块导出。**✅ 已完成**（`internal/builtin/markdown.go`） |
| M4-3 增量与独立工具 | 支持一条命令生成多页 HTML，亦可 `aluka build --compile` 将 SSG 构建器打包为免环境独立单文件工具。**✅ 已完成** |
| M4-4 文档 | SSG 指南与完整示例。**✅ 已完成**（`demo/ssg-site/README.md`） |

### 扩展：Vue SFC（计划外增量，随 M3 后落地）

| 项 | 内容 |
|----|------|
| .vue 单文件组件 | `internal/bundler/vue`：构建期编译，架构对齐 Vite / @vitejs/plugin-vue——**编译器只产出 import 运行时 helper 的代码**（`import { h as _h, toDisplayString as _toDisplayString, unref as _unref } from 'vue'`，`render(_ctx)` 调用 `_h(...)` 构造 vnode），`vue` 是用户项目的 node_modules 依赖经 graph 正常解析，**编译器不内嵌运行时、不与任何实现锁版本**；script 的 `export default` 经 `__sfc__.render = render` 挂接（Vite 同款）；`:绑定`/`@事件`/`{{插值}}`/嵌套/自闭合/void；`<style>`/`<script setup>` 构建期明确拒绝。**✅ 已完成** |
| demo | `demo/web-bundle-vue-demo/`：官方 `vue@3.5.13` 及完整传递依赖作为离线 fixture 随仓库分发，`package-lock.json` 固定依赖闭包；Node 端到端验证真实 `vue/server-renderer` 首屏 SSR、响应式状态与动态 chunk。**✅ 已完成** |

## 3. 架构与落点

```
cmd/aluka/build.go            # CLI 分派：--compile（现） / --target=web（新）
internal/bundler/
  graph/    （复用）依赖图：新增 browser condition（M2-5）
  shake/    （复用）kept-set
  minify/   （复用）AST 级优化
  emit/     （新）  printer.go / wrap.go（__esm/__commonJS）/ css.go / html.go / sourcemap.go
  webbuild/ （新）  编排：graph → shake → minify → emit → 产物清单/报告（复用 analyze）
tests/conformance/webbuild/   （新）真实前端库冒烟 + 产物浏览器语义断言
demo/web-gui/                 （新，M2-6）源码 → 桌面应用一条命令示例
```

关键约束：
- web 路径**不写** `.aluka-cache`、不经 bytecode compile；
- printer 输出必须满足回读对拍（parser 是唯一 oracle——与 jitdiff 同哲学）；
- 与 `--compile` 共享 graph/shake/minify 的任何改动，须跑 `--compile` 既有
  conformance（tests/conformance/build）防回归。

## 4. 测试策略

| 层 | 手段 |
|----|------|
| printer 正确性 | 回读对拍（全仓库源码语料 + 语法矩阵），等价断言 AST 结构 |
| bundle 语义 | 产物在浏览器运行断言（conformance/webbuild：node --experimental-vm 或 headless 校验导出值） |
| 体积/优化 | analyze 报告对拍（shake 前后模块数、minify 前后字节数） |
| GUI 闭环 | `--gui --web-entry` 端到端：构建 → 运行 exe → aluka://app 加载断言 |
| 回归 | 全量 go test + build conformance 双绿 |

## 5. 里程碑验收标准（Definition of Done）

- **M1**：`aluka build --target=web --minify src/index.ts` 产物 ≤ esbuild 同参产物
  1.5 倍体积；React+TSX 冒烟通过；printer 对拍语料 100% 等价
- **M2**：`demo/web-gui` 一条命令产出可运行单文件桌面应用；sourcemap 在浏览器
  DevTools 正确映射
- **M3**：动态 import 站点拆包正确；watch 全量重建 < 200ms（中小工程）
- **M4**：`aluka new site && aluka run build.ts` 开箱出站；文档齐全

## 6. 风险与对策

| 风险 | 对策 |
|------|------|
| printer 节点遗漏 → 产物语法错误 | 回读对拍全覆盖 + 语料库持续扩充（每次新增语法先补对拍用例） |
| CJS/npm 生态边角（browser 字段、exports 条件） | M1 严格报错收集真实需求，M2 按需补齐 |
| 与 --compile 管线共享组件回归 | 共享组件改动双跑 build conformance |
| sourcemap 精度坑 | 分级交付：先文件/行级，列级与表达式级后置 |

## 7. 与现有计划的关系

- 承接 [build-compile-plan.md](./build-compile-plan.md) §8 预留的"纯 JS bundle 扩展"；
- GUI 侧兑现 [aluka-gui-architecture-plan.md](./aluka-gui-architecture-plan.md)
  GUI-4 的"前端静态资源打包"完整愿景（当前 --web-dir 仅嵌入）；
- 插件体系（`Bun.build` JS API 等价物）超出本计划，M3 后评估。
