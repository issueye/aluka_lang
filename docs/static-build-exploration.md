# Aluka 静态构建探索文档 —— Web Bundle / SSG 能力空间分析

> 文档版本：v1.0 ｜ 日期：2026-08-15
> 性质：技术探索（方案空间扫描 + 可行性核实），为 [开发计划](./static-build-plan.md) 提供决策输入
> 依据：2026-08-15 代码核实（internal/bundler 全组件、cmd/aluka/build.go、GUI --web-dir 链路）+ 实测验证

---

## 1. 问题定义："页面的静态构建"在 Aluka 语境下的三种含义

| 含义 | 对标产品 | Aluka 现状 |
|------|---------|-----------|
| **A. Web Bundle**：把浏览器端源码（JS/TS/JSX、CSS、HTML 入口）打包成 `dist/` 静态产物（tree-shaking、minify、sourcemap、code splitting） | `bun build ./src/index.ts`、esbuild、Vite build | ❌ 未实现（`build` 仅支持 `--compile`） |
| **B. SSG 静态站点生成**：以数据 + 模板渲染输出静态 HTML 页面 | Astro、Eleventy、Next `output: export` | ✅ **可用**（运行时能力齐备，实测通过：TS 脚本 + node:fs + JSON 数据 → 多页 HTML；且可 `--compile` 打包成独立构建工具） |
| **C. GUI 前端资源打包**：桌面应用的前端 dist 嵌入 | Wails/Tauri 的 asset pipeline | ⚠️ 半支持：`build --gui --web-dir` 只**嵌入**已有 dist，不负责产出 dist（依赖外部 Vite 等） |

三者关系：**A 是 B/C 的地基**——A 落地后，C 可形成"一条命令从源码到桌面应用"的闭环
（`aluka build --gui` 直接消费自己的 bundle 输出），B 也可升级到组件化前端
（`.tsx` 页面 + 框架运行时预渲染）。

## 2. 现状资产盘点（已核实）

### 2.1 可复用（internal/bundler，服务于 --compile 的既有组件）

| 组件 | 能力 | Web bundle 复用度 |
|------|------|------------------|
| `graph` | ESM/CJS 依赖图、TS/TSX 源码解析（复用引擎 parser，JSX/TSX 已支持——见 commit ca88514）、动态 import 字面量、循环依赖、TLA | **高**：模块图与 target 无关 |
| `analyze` | 多阶段体积/热点分析（raw/shaken/minified/bytecodeOpt） | **高**：直接用于 bundle 报告 |
| `shake` | 模块级 tree-shaking（kept-set 对拍测试保障） | **高** |
| `minify` | **AST 级**死代码消除、常量折叠、控制流简化（不是文本压缩） | **高** |
| `compile` | AST → 字节码 → payload（--compile 专属） | **零**：web 输出不走此路 |
| loader（runtime/module） | Node 解析算法、`paths`/`baseUrl` 别名、node_modules | **高**：构建期解析已复用 |

### 2.2 缺失组件（关键差距）

| 缺失 | 说明 | 工作量评估 |
|------|------|-----------|
| **AST → JS 源码打印器** | 全仓库无 printer（`grep Print/Format/Emit` 无 AST 输出实现）。bundle 需要把（shake+minify 后的）AST 变回 JS 文本 | **核心工作量**。AST 节点完备（parser 全量维护），打印本身机械但量大；优先级输出：ES2020 子集 + 按需扩展新语法 |
| **CSS 处理** | 无 CSS 解析/合并/minify；`import "./x.css"` 目前在 graph 中是资产旁路 | 中。M1 可"原样拷贝+拼接"，minify 后置 |
| **HTML 入口处理** | `<script src>` / `<link>` 引用改写、注入构建产物 | 小（字符串/正则级处理够用） |
| **sourcemap** | 无任何支持（无 sourceMappingURL 产出） | 中。v3 标准，可接第三方纯 Go 库或简化为"文件级 map" |
| **code splitting / 动态 import 拆包** | graph 已解析动态 import，但产物只有一个 entry chunk | 中-大，可后置 |
| **浏览器垫片层** | `node:fs` 等 Node 内置在浏览器 bundle 中必须替换/报错；`process`/`Buffer` 需要 polyfill 注入 | 小-中（M1 先严格报错，M2 加 polyfill 可选项） |
| **target 语法降级** | 引擎自产自销无降级需求；web 输出需 es2020/es2018 选项 | 大，**建议 M1 不做**（输出与源码同代语法，现代浏览器全兼容） |

### 2.3 竞品基准（设计参照）

| 能力 | Bun build | esbuild | 本计划 M1 定位 |
|------|-----------|---------|---------------|
| 单 entry JS bundle + minify | ✅ | ✅ | ✅ |
| 多 entry | ✅ | ✅ | M2 |
| CSS 入口/拆分 | ✅ | ✅ | M2（M1 仅 JS import 的 CSS 拷贝拼接） |
| sourcemap | ✅ | ✅ | M2 |
| code splitting | ✅ | ✅ | M3 |
| JSX | ✅（内置） | ✅（内置） | ✅ **已有**（parser 源码级 JSX/TSX，ca88514）|
| 插件体系 | ✅ | ✅ | M3+ |

Aluka 的差异化：**JSX/TSX 源码级解析已在引擎内**（无需插件）、与 `--compile`/`--gui`
同一条管线（未来 `aluka build --gui` 可直接吃 bundle 产物）、TS 零配置。

### 2.4 SSG（含义 B）的运行时能力核实

实测（2026-08-15，demo 见本节末）：`aluka build.ts` 执行 TypeScript SSG 脚本
（`node:fs` 读写 + JSON 解析 + 模板字符串）输出多页互链 HTML；
`aluka build --compile --outfile ssg-tool build.ts` 将构建器本身打包为 26MB
独立可执行。**SSG 现在就可用**，无需新开发——缺的是开箱体验（脚手架、
Markdown 管线、热重建 watch），归入计划 M4。

## 3. 方案空间与关键决策

### D1：输出产物形态 —— AST 打印 vs 复用 minify 后直接拼接

| 方案 | 机制 | 优劣 |
|------|------|------|
| **AST printer（选定）** | shake+minify 后的 AST 经统一 printer 输出，模块包裹为单 IIFE（Bun/esbuild 同构） | 语义最稳（同一 parser 解析同一 printer 输出）、minify 收益直接兑现；需自研 printer |
| 逐模块源码透传拼接 | 保留原文本，只做 import 改写 | 实现快；但 tree-shaking/minify 全部失效，minify 的 AST 变更无法回写源码（**无 AST patcher**） |

结论：**自研 AST printer 是唯一能兑现既有 minify/shake 资产的路线**。
风险对冲：printer 输出必须经"printer → parser 回读 → AST 等价"对拍（仓库已有
zz_diff_test 传统）。

### D2：模块格式 —— ESM 输出 vs 双格式

M1 输出 **ESM 单文件**（`<script type="module">` 直接可用）；CJS/UMD 输出 M3
按需。浏览器内模块包裹沿用 esbuild 的作用域拼接（`__esm` wrapper 惰性初始化）。

### D3：与 GUI 的合流点

```
aluka build --gui --entry src/main.ts        # 现状：主进程 --compile
                    --web-dir dist           #   前端：要求用户先跑 vite
        ↓ M2 后
aluka build --gui --web-entry src/index.tsx  # 前端由本 bundle 产出 dist，
                                            # --web-dir 自动指向，一条命令到桌面应用
```

### D4：CLI 形态

```
aluka build --target=web [--entry …] [--outdir dist] [--minify] [--format=esm]
```
`--compile` 与 `--target=web` 互斥；默认（无 --compile）从"M1 报错"改为进入 web
bundle 路径——对齐 `bun build` 默认行为。

## 4. 风险与开放问题

1. **printer 覆盖度**：AST 节点多（JSX/装饰器/TS 全量），打印遗漏即产物语法错误——
   以"回读对拍 + 固定语料库"（含 tests/compat 真实包）控制；
2. **CJS 依赖在浏览器 bundle**：M1 对 `require("node:*")` 构建期报错并给出替换建议；
3. **npm 生态兼容**（浏览器库的 package.json `browser` 字段/exports 条件）：M2
   resolver 增加 `browser` condition；
4. **与磁盘字节码缓存的边界**：web 路径不写 `.aluka-cache`（不经 compile）。

## 5. 结论

- **B（SSG）今天可用**——补开箱体验即可（M4）；
- **A（web bundle）可行且路径清晰**：核心缺口只有 AST printer，其余组件
  （graph/shake/minify/analyze）均为现成资产，JSX/TSX 解析已超前具备；
- **C（GUI 闭环）** 是最有产品价值的落点：A 的 M2 完成后即形成
  "源码 → 浏览器 bundle → 嵌入 → 单文件桌面应用"的一条命令链路。

下一步见 [static-build-plan.md](./static-build-plan.md)。
