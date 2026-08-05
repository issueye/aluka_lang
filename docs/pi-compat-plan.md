# Aluka × Pi 兼容性开发计划

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 日期：2026-08-05
> 依据：2026-08-05 对 `D:\codes\node_workspaces\pi`（Pi Agent Harness）全仓代码扫描 + aluka 能力实测
> 配套文档：[开发计划文档](./development-plan.md) / [缺陷修复计划](./defect-fixes-plan.md)

---

## 1. 背景与目标

### 1.1 为什么是 Pi

[Pi Agent Harness](https://pi.dev)（`pi-monorepo`）是开源 AI 编码代理，TypeScript monorepo（9 包、1069 个源码文件），具有三个决定性特征：

1. **Node ≥ 22.19 直接运行 .ts**：Node 原生 type stripping + `erasableSyntaxOnly: true`，1143 处相对导入带 `.ts` 扩展名
2. **官方双运行时支持**：发布前必须对 Node 与 **Bun** 两个运行时通过冒烟测试（AGENTS.md 明文要求）；二进制用 `bun build --compile` 打包
3. **重型现代依赖链**：undici、@anthropic-ai/sdk（→ws）、protobufjs、@aws-sdk、yaml、glob、cross-spawn、diff、typebox、vitest、node:sqlite

**目标**：aluka 作为"Bun 兼容运行时"，以 Pi 为真实世界验证目标，分三阶段达到：
- **阶段 A（加载）**：aluka 能完成 Pi 的模块加载与语法解析（含 TS、JSON、TLA、import attributes、.ts 扩展名导入）
- **阶段 B（运行）**：aluka 能运行 Pi 的核心路径（tui 渲染、agent 会话、ai 客户端）
- **阶段 C（测试）**：aluka 能跑通 Pi 的测试基础设施（node:test 最小集 + 轻量第三方包）

### 1.2 现状基线（2026-08-05 实测）

aluka 已具备：ES2023 语法（含 tagged template/String.raw）、TS 转译（enum 降级等）、ESM+CJS+循环依赖+动态 import、20+ Node 内置模块、fetch/WebSocket/Streams、正则回溯引擎（反向引用/前瞻/后行）、structuredClone、包管理器（workspace/.npmrc）。

**实测缺口**（探针逐项验证）：

| 优先级 | 缺口 | Pi 的用法 | 实测证据 |
|--------|------|-----------|----------|
| P0-1 | `.ts` 扩展名相对导入 | 全仓 1143 处 `import "./x.ts"` | loader 无 .ts 解析 |
| P0-2 | import attributes（`with {type:"json"}` 静态 + 动态第二参数） | `providers/*.models.ts` 全量 JSON 导入 | parser 报 `expected ")" but got ","` |
| P0-3 | 正则 `/v` unicodeSets（`\p{...}` + `--` 集合运算） | tui `zeroWidthRegex`（`\p{Default_Ignorable_Code_Point}`） | flag 自相矛盾：`'v'` 报 `'u' and 'v' cannot both be set` |
| P0-4 | `node:sqlite` DatabaseSync | session-backends 生产依赖 | `no such built-in module: node:sqlite` |
| P0-5 | top-level await | scripts/*.mjs、examples/sdk/*.ts | `.mjs` 顶层 `await` 语法错误 |
| P1-1 | fs 补全（promises mkdtemp/lstat/realpath、watch、createReadStream） | nodejs.ts 全量 API、git HEAD 监视 | 仅 11 个 promises 方法 |
| P1-2 | `AbortSignal.timeout` / `AbortSignal.any` | OAuth 超时链路 | 值为 undefined |
| P1-3 | `Intl.Segmenter`（grapheme/word） | tui 渲染/单词导航 | 无 Intl |
| P1-4 | worker_threads transferList（ArrayBuffer 转移） | image-resize worker | 不支持 transfer |
| P1-5 | 信号处理补全（kill、SIGTSTP/SIGCONT、SIGTERM→SIGKILL） | interactive-mode、rpc-client | 部分缺失 |
| P2-1 | `node:test` 运行器（describe/it + 目录模式） | tui 30 个测试文件 `node --test` | 无 node:test |
| P2-2 | `zstdCompressSync` / `process.getBuiltinModule` | openai-codex-responses（有 guard 回退） | 缺失（低风险） |
| P2-3 | 重型第三方包实跑（undici/yaml/minimatch/ws/…） | 生产依赖 | 未验证 |

### 1.3 验收总纲

每个 P0 任务完成必须满足：
1. 对应 Go 回归测试（`go test ./...` 全绿）
2. Node 22 对照行为一致（差分验证）
3. Pi 侧锚点文件可加载/运行（如 `scripts/repro-5893-wsl-bash.mjs` 过 TLA、`packages/tui/src/utils.ts` 过 /v 正则与 Segmenter）

---

## 2. 阶段 A：加载层（P0-1 ~ P0-5）

### 2.1 P0-1 `.ts` 扩展名相对导入

- **现状**：`internal/runtime/module/loader.go` 的 ESM/CJS 解析按 Node 算法（`.js`/`.json`/目录 index），无 `.ts` 扩展名
- **方案**：解析候选扩展名加入 `.ts`（及 `.tsx` 禁用——Pi 无 JSX）；`.ts` 文件经 TS 转译后按 ESM/CJS 语义执行；`import "./x.js"` 指向 `.ts` 文件的场景（Node type stripping 支持 js→ts 映射）一并处理
- **验收**：`aluka` 能加载含 `import "./a.ts"` 的模块；与 Node 22 对照

### 2.2 P0-2 import attributes

- **现状**：parser 不支持 `with { type: "json" }`（静态）与 `import(x, {with})`（动态）
- **方案**：
  1. lexer/parser：静态导入 `with {...}` 子句解析（AST 增 `ImportAttributes` 字段）
  2. compiler/interpreter：动态 `import()` 第二参数解析
  3. loader：`type: "json"` 走 JSON 模块（现有 loadJSON），`type: "module"` 常规加载
- **验收**：`import v from "./x.json" with {type:"json"}` 与动态形式均可用；非法 type 报错

### 2.3 P0-3 正则 `/v` unicodeSets

- **现状**：`ParseFlags` 接受 `v` 但置 `Unicode=true` 后自我冲突报错（regex.go:65-68）
- **方案**：
  1. 修 flag bug：`v` 不再置 `Unicode`，独立 `UnicodeSets` 标志
  2. 翻译层（translate.go）：`\p{...}` Unicode 属性类映射到 Go 兼容属性（`\p{L}`/`\p{N}` 等常见属性）；不支持属性时回退回溯引擎或报错
  3. 集合运算（`[a-z--x]`/`[a-z&&[^x]]`）先支持 `--` 差集（Pi 实际用例）
- **验收**：`new RegExp(String.raw`[\p{Default_Ignorable_Code_Point}]`, "v")` 可编译且行为与 Node 一致

### 2.4 P0-4 node:sqlite

- **现状**：无此 builtin；go.mod 已有 `modernc.org/sqlite`
- **方案**：`internal/builtin/sqlite.go` 注册 `node:sqlite`，实现 `DatabaseSync`：`prepare`（Statement: run/get/all/iterate）、`exec`、事务 `BEGIN IMMEDIATE` 语义（回调必须同步）、`close`；值类型映射（null/number/string/bigint/Buffer）
- **验收**：Pi 的 `packages/session-backends/sqlite-node` 适配层可加载；与 Node 对照 CRUD + 事务

### 2.5 P0-5 top-level await

- **现状**：模块加载器同步执行；parser 顶层 `await` 报错
- **方案**：
  1. parser：module 上下文允许顶层 AwaitExpression（与 import/export 同层）
  2. interpreter/compiler：顶层 await 语句编译为异步执行路径（模块求值返回 Promise）
  3. loader（esm.go）：模块执行改为异步链（动态 import/TLA 依赖等待）
- **验收**：`.mjs`/`.ts` 顶层 await 脚本可运行，依赖顺序与 Node 一致

---

## 3. 阶段 B：运行层（P1-1 ~ P1-5）

### 3.1 P1-1 fs 补全
- promises：`mkdtemp`/`lstat`/`realpath`/`opendir`/`readFile` encoding/`writeFile` 选项
- 流：`createReadStream`/`createWriteStream`（经现有 stream 模块）
- 监视：`fs.watch`（Go fsnotify，Pi 需要 watch 目录 + 文件变更事件 + rename）
- 验收：nodejs.ts 用到的 API 全部可用；watch 事件与 Node 对照

### 3.2 P1-2 AbortSignal.timeout / AbortSignal.any
- `AbortSignal.timeout(ms)`：定时 abort（AbortError DOMException）
- `AbortSignal.any([...])`：任一信号 abort 则 abort
- 验收：OAuth 场景（timeout + any 组合）行为与 Node 一致

### 3.3 P1-3 Intl.Segmenter
- 新增 `internal/runtime/globals/intl.go`：`Intl.Segmenter`（granularity: grapheme/word/sentence）
- 实现：自研 grapheme 簇（GB11 规则）或引入 `github.com/rivo/uniseg`
- 验收：tui 的 `grapheme`/`word` 分割与 Node 对照（含 emoji ZWJ 序列）

### 3.4 P1-4 worker_threads transferList
- `postMessage(value, transferList)`：ArrayBuffer 所有权转移（源置空、目标接收）
- 验收：Pi image-resize worker 模式（transfer 大 ArrayBuffer）可运行

### 3.5 P1-5 信号处理补全
- `process.kill(pid, signal)`、SIGTSTP/SIGCONT/SIGWINCH 支持、SIGTERM→SIGKILL 递进
- 验收：interactive-mode 的挂起/恢复（SIGTSTP→SIGCONT）行为正确

---

## 4. 阶段 C：测试层（P2-1 ~ P2-3）

### 4.1 P2-1 node:test 最小运行器
- `node:test` 模块：`describe`/`it`/`test`/`beforeEach`/`afterEach` + 断言（assert 已有）
- CLI：`aluka --test <dir|file>`（glob 收集 `*.test.ts/js/mjs`，Node 目录模式语义）
- 验收：Pi 的 `packages/tui/test/*.test.ts`（30 文件）可跑

### 4.2 P2-2 低风险补全
- `zlib.zstdCompressSync`/`zstdDecompressSync`（klauspost/compress）
- `process.getBuiltinModule`（builtin 注册表查询）

### 4.3 P2-3 第三方包实跑验证
- 优先：`yaml`、`minimatch`、`semver`（已过）、`diff`（纯 JS，API 面广）
- 其次：`undici`（依赖 fetch/streams 内部 API，Pi http-dispatcher 生产依赖）
- 最后：`ws`（WebSocket 客户端兼容）、`@anthropic-ai/sdk`（整链）

---

## 5. 优先级与依赖

```
P0-1 (.ts 导入) ──→ P0-2 (import attributes) ──→ P0-5 (TLA)    [模块加载链]
P0-3 (/v 正则) ──→ P1-3 (Intl.Segmenter)                       [tui 渲染链]
P0-4 (node:sqlite)                                             [session 链，独立]
P1-1 (fs) ──→ P1-4 (worker) ──→ P1-5 (信号)                     [agent 运行链]
P1-2 (AbortSignal)                                             [ai/oauth 链，独立]
P2-1 (node:test) ──→ P2-3 (第三方包)                            [测试链]
```

**实施顺序**（每项独立可验证，随时可提交）：
1. P0-1 `.ts` 扩展名导入（模块解析，最小改动）
2. P0-3 正则 /v（flag bug 修复 + 属性类映射）
3. P0-2 import attributes（parser → compiler → loader）
4. P0-4 node:sqlite（独立 builtin）
5. P0-5 top-level await（加载器异步化，风险最高）
6. P1-2 AbortSignal（快速）
7. P1-1 fs 补全（watch 用 fsnotify）
8. P1-3 Intl.Segmenter
9. P1-4 worker transferList
10. P1-5 信号补全
11. P2-1 node:test + P2-2 + P2-3

## 6. 风险与边界

| 风险 | 等级 | 缓解 |
|------|------|------|
| TLA 需模块加载器异步化，改动面大 | 高 | 先做"顶层 await 编译为 Promise 链"最小实现，保持同步模块缓存 |
| /v 集合运算语义复杂（`&&`/`--`/嵌套类） | 中 | 只做 Pi 实际用到的 `--` 差集 + 属性类；其余报"不支持" |
| node:sqlite 需对齐 Node 值映射与事务语义 | 中 | 以 Pi sqlite-node 适配层为验收锚点 |
| fs.watch 跨平台（win32）事件语义差异 | 中 | 以 Pi footer-data-provider（watch git HEAD 文件）为锚点 |
| undici/vitest 等重型包内部依赖多 | 高 | 阶段 C 最后攻坚，先保证 yaml/minimatch/diff |

## 7. 验收锚点（Pi 侧）

| 锚点 | 验证内容 | 对应任务 |
|------|----------|----------|
| `scripts/repro-5893-wsl-bash.mjs` | TLA + import.meta.url | P0-5 |
| `packages/ai/src/providers/openai.models.ts` | import attributes JSON | P0-2 |
| `packages/tui/src/utils.ts` | /v 正则 + Segmenter + structuredClone | P0-3, P1-3 |
| `packages/session-backends/sqlite-node/src/index.ts` | DatabaseSync 全 API | P0-4 |
| `packages/coding-agent/src/utils/image-resize.ts` | worker transferList | P1-4 |
| `packages/tui/test/*.test.ts`（30 文件） | node:test 运行器 | P2-1 |
