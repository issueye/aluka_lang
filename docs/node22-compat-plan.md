# Aluka × Node 22 兼容开发计划

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 日期：2026-08-06
> 依据：2026-08-06 对 aluka 的 API 探测矩阵（45 项实测）+ 代码核查 + Node 22.12 LTS 特性清单
> 配套文档：[开发计划文档](./development-plan.md) / [Pi 兼容计划](./pi-compat-plan.md) / [打包器计划](./build-compile-plan.md)

---

## 1. 背景与目标

### 1.1 为什么对标 Node 22

Node 22（2024-04 LTS，22.12 起 `require(esm)` 默认开启）是当前主流运行时基准：
ES2024 语法（RegExp `v`/`Promise.withResolvers`/`Array.fromAsync`/`Object.groupBy`）、
全局 WebSocket 客户端、`node:sqlite`、`util.styleText`、`process.getBuiltinModule`、
`AbortSignal.any/timeout` 等。aluka 已覆盖其中大部分（Pi 兼容阶段验证），
剩余差距集中在**内置模块缺失**与**API 级补全**。

### 1.2 目标

1. **四维对齐**：内置模块 / 全局对象 / ES 语法特性 / 运行时语义
2. 消除 P0 运行时语义差距（影响真实代码运行）
3. P1 模块与 API 补全至 Node 22 常用面 ≥ 90%
4. 建立 **Node 22 差分测试基线**（同一脚本 aluka vs node22 输出对照）

### 1.3 对标基准

Node 22.12.0（LTS）。以 [Node.js 官方文档](https://nodejs.org/docs/latest-v22.x/api/) 的能力清单为参照。

---

## 2. 现状基线（2026-08-06 实测）

### 2.1 内置模块（42 个已注册 vs Node 22）

| 状态 | 模块 |
|------|------|
| ✅ 已有（42） | assert、assert/strict、async_hooks、buffer、child_process、console、constants、crypto、diagnostics_channel、dns、events、fs、fs/promises、http、https、module、net、os、path、perf_hooks、process、querystring、readline、repl、sqlite、stream、stream/promises、stream/web、string_decoder、test、timers、timers/promises、tls、tty、url、util、util/types、v8、vm、worker_threads、zlib |
| ❌ 缺失（核心 6） | **dgram**（UDP）、**cluster**、**http2**、**inspector**、**trace_events** |
| ⚠️ 范围外 | wasi（纯 Go 无 WASM 运行时）、punycode/domain（Node 已废弃）、sea（Node 22 实验）、test/reporters |

### 2.2 全局对象

| 状态 | 全局 |
|------|------|
| ✅ 已有 | fetch/Request/Response/Headers/FormData、**WebSocket（客户端）**、ReadableStream/WritableStream/TransformStream、Blob/File、URL/URLPattern、AbortController（timeout/any）、crypto/subtle、structuredClone、Event/EventTarget、MessageChannel、Performance、Intl（Segmenter）、DOMException、globalThis.crypto |
| ❌ 缺失 | **navigator**（Node 21+）、**BroadcastChannel**、scheduler（实验） |

### 2.3 ES 语法与内置对象（ES2023-2024）

| 状态 | 特性 |
|------|------|
| ✅ 已有 | RegExp `v` unicodeSets、Error cause、import attributes、top-level await、数字分隔符、逻辑赋值、可选链、`.ts` 导入 |
| ❌ 缺失 | **Promise.withResolvers**、**Array.fromAsync**、**Object.groupBy**、**Map.groupBy**、**String.isWellFormed/toWellFormed**、**Array.toSorted/toReversed/toSpliced/with/findLast/findLastIndex**、**Object.hasOwn**（待核对） |

### 2.4 API 级差距（探测 45 项，MISS 明细）

| 模块 | 缺失 API | Node 22 语义 |
|------|----------|--------------|
| assert | `match`/`doesNotMatch`、`partialDeepStrictEqual` | 正则/部分深比较断言 |
| util | `parseArgs` | CLI 参数解析（Node 18.3+，22 稳定） |
| fs | `cp`/`cpSync`、`glob`/`globSync` | 目录复制、glob 匹配（22.9 稳定） |
| path | `matchesGlob` | 路径 glob 匹配（22.3） |
| crypto | `X509Certificate` | 证书解析 |
| process | `umask`、`cpuUsage` | 权限掩码、CPU 用量 |
| child_process | `spawnSync`/`execFileSync`/`execSync` | 同步子进程 |
| http/https | `Agent`（keepAlive 语义） | 连接复用 |
| buffer | `isUtf8`/`isAscii` | 编码检测 |
| timers/promises | `scheduler`（实验） | 任务调度 |

---

## 3. 差距分级（按影响面）

| 级别 | 内容 | 理由 |
|------|------|------|
| **P0 运行时语义** | `for await...of` 流迭代（Readable Symbol.asyncIterator）、`Array.prototype` 方法 `thisArg`（非箭头函数）、`require(esm)` 真实验证、事件循环细节（process.nextTick 顺序/微任务边界） | 影响任意真实代码的"隐形错误" |
| **P1 模块与 API** | dgram、cluster、http2、inspector + 2.4 表全部 API | Node 生态常用面 |
| **P2 ES2024 全局** | Promise.withResolvers、Array.fromAsync、Object.groupBy、Map.groupBy、String/Array 新方法、navigator、BroadcastChannel | 语法级对齐 |
| **P3 测试与工具** | node:test 完整（mock/coverage/snapshot/spawn）、REPL 补全、trace_events（可选） | 开发体验 |

---

## 4. 分阶段任务（WBS）

### 阶段 A：P0 运行时语义（最高优先）

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| N22-A1 | Readable 实现 `Symbol.asyncIterator`（for await...of 流迭代；复用 fromWeb 桥接的 Promise 链模式） | `internal/builtin/stream.go` | [ ] |
| N22-A2 | `Array.prototype` 方法的 `thisArg` 对非箭头函数生效（find/map/filter/forEach/reduce 等） | `internal/engine/interpreter/builtins.go` | [ ] |
| N22-A3 | `require(esm)`：CJS require 同步加载 ESM 模块（含 `module.exports` 与命名导出互操作） | `internal/runtime/module/` | [ ] |
| N22-A4 | 事件循环语义差分：`process.nextTick` 优先级、微任务/宏任务边界（对照 Node 22 行为） | 差分测试 + 修正 | [ ] |
| N22-A5 | 回归：全量测试 + Node 22 差分脚本（同一代码双跑对比） | `tests/conformance/node22/` | [ ] |

**验收**：Pi 全量测试 + express + 差分脚本三绿；`for await (const c of stream)` 可用。

### 阶段 B：P1 模块与 API

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| N22-B1 | `node:dgram`（UDP socket：createSocket/send/bind/message 事件） | `internal/builtin/dgram.go` | [ ] |
| N22-B2 | `node:cluster`（master/worker、fork、IPC 基础） | `internal/builtin/cluster.go` | [ ] |
| N22-B3 | `node:http2`（session/stream 基础：connect/request/respond） | `internal/builtin/http2.go` | [ ] |
| N22-B4 | `node:inspector`（最小面：Session/url——协议级留白） | `internal/builtin/inspector.go` | [ ] |
| N22-B5 | assert 补全：match/doesNotMatch/partialDeepStrictEqual | `internal/builtin/assert.go` | [ ] |
| N22-B6 | util.parseArgs（Node 22 语义：options/positionals/严格模式） | `internal/builtin/util.go` | [ ] |
| N22-B7 | fs.cp/cpSync、fs.glob/globSync | `internal/builtin/fs.go` | [ ] |
| N22-B8 | path.matchesGlob、crypto.X509Certificate、Buffer.isUtf8/isAscii | 对应模块 | [ ] |
| N22-B9 | process.umask/cpuUsage、child_process 同步三件套（spawnSync/execFileSync/execSync） | `process.go`/`child_process.go` | [ ] |
| N22-B10 | http.Agent（keepAlive 语义，连接复用） | `internal/builtin/http.go` | [ ] |

**验收**：每个任务附 Node 22 差分用例；`tests/conformance/node22/` 模块级脚本全绿。

### 阶段 C：P2 ES2024 全局

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| N22-C1 | Promise.withResolvers、Array.fromAsync | globals | [ ] |
| N22-C2 | Object.groupBy、Map.groupBy | globals | [ ] |
| N22-C3 | String.isWellFormed/toWellFormed、Array.toSorted/toReversed/toSpliced/with/findLast/findLastIndex、Object.hasOwn（ES2023 补全核对） | engine builtins | [ ] |
| N22-C4 | navigator（userAgent/platform 等）、BroadcastChannel | globals | [ ] |

**验收**：ES2024 特性差分脚本与 Node 22 输出一致。

### 阶段 D：P3 测试与工具链

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| N22-D1 | node:test 补全：mock（mock.fn/mock.method）、coverage、snapshot、spawn（子进程测试） | `internal/builtin/test.go` | [ ] |
| N22-D2 | REPL 补全（多行编辑、.editor、历史文件） | `cmd/aluka/repl.go` | [ ] |
| N22-D3 | trace_events（可选：Tracing 对象最小面） | 可选 | [ ] |

---

## 5. 里程碑规划

阶段 A-D 的 WBS 按"影响面 × 依赖 × 工作量"打包为 5 个里程碑。
**原则**：M1（运行时语义）先行——它修正的是影响一切真实代码的隐形错误；
M2（ES2024）独立低风险可穿插；M4（新模块）复杂度最高放最后。

### 5.1 里程碑总览

| 里程碑 | 内容 | 对应阶段 | 工作量估 | 依赖 | 验收锚点 |
|--------|------|----------|----------|------|----------|
| **M1 运行时语义** | for-await 流迭代、thisArg、require(esm)、事件循环差分 + 差分框架 | A | ~600-1000 行 | 无 | 差分框架跑通，4 项语义与 node22 一致 |
| **M2 ES2024 全局** | withResolvers/fromAsync/groupBy/新数组方法/navigator/BroadcastChannel | C | ~400-600 行 | 无（可穿插） | ES2024 差分脚本 100% |
| **M3 常用 API 补全** | assert.match 系、parseArgs、fs.cp/glob、matchesGlob、X509Certificate、isUtf8、umask/cpuUsage、spawnSync 系、http.Agent | B（B5-B10） | ~600-900 行 | M1（差分基线） | API 差分全绿（2.4 表 MISS 清零） |
| **M4 缺失模块** | dgram、cluster、http2、inspector | B（B1-B4） | ~800-1200 行 | M1 | 4 模块基础场景差分通过 |
| **M5 测试与工具链** | node:test mock/coverage/snapshot/spawn、REPL 补全、trace_events（可选） | D | ~500-800 行 | M1-M3 | `aluka test --coverage` + mock 差分 |

### 5.2 M1：运行时语义（P0，先行）—— ✅ 完成（2026-08-06）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| N22-A1 | Readable `Symbol.asyncIterator` | ✅ | for await...of 流迭代；复用 fromWeb 桥接的 Promise 链模式（已证明可行） | ✅ |
| N22-A2 | `Array.prototype` thisArg（非箭头函数） | ✅ | find/map/filter/forEach/reduce 等第二参数；README 已知限制 | ✅ |
| N22-A3 | `require(esm)` | ✅ | CJS require 同步加载 ESM（复用 ESM→CJS 管线 + 缓存；无 TLA 可同步） | ✅ |
| N22-A4 | 事件循环语义差分 | ✅ | process.nextTick 优先级、微任务/宏任务边界 | ✅ |
| N22-A5 | 差分框架 + 回归 | ✅ | `tests/conformance/node22/run.sh`（同一脚本双跑对比） | ✅ |

**M1 验收**：
- `for await (const c of stream)` 与 node22 输出一致
- `[1].map(function(x){return x*this.m}, {m:2})` 返回 2
- `require('./esm.mjs')` 同步返回命名导出
- 差分框架对 4 项语义逐项对比通过；`go test ./...` 全绿

### 5.3 M2：ES2024 全局（P2，可穿插）—— ✅ 完成（2026-08-06）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| N22-C1 | Promise.withResolvers、Array.fromAsync | ✅ ES2024 核心全局 | ✅ |
| N22-C2 | Object.groupBy、Map.groupBy | ✅ ES2024 | ✅ |
| N22-C3 | String.isWellFormed/toWellFormed、Array.toSorted/toReversed/toSpliced/with、Object.hasOwn | ✅ findLast 已存在；surrogate 语义为架构差异 | ✅ |
| N22-C4 | navigator、BroadcastChannel | ✅ 全局对象 | ✅ |

**M2 验收**：ES2024 差分脚本（`tests/conformance/node22/es2024/`）与 node22 输出 100% 一致。

### 5.4 M3：常用 API 补全（P1-API）—— ✅ 完成（2026-08-06）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| N22-B5 | assert.match/doesNotMatch/partialDeepStrictEqual | ✅ 正则断言 + partialDeepStrictEqual（递归部分比较） | ✅ |
| N22-B6 | util.parseArgs | ✅ 按 node v22.x 源码移植（strict/short/brace/--no-/错误码） | ✅ |
| N22-B7 | fs.cp/cpSync、fs.glob/globSync | ✅ minimatch 子集移植 + 40+ 差分用例全绿 | ✅ |
| N22-B8 | path.matchesGlob、crypto.X509Certificate、Buffer.isUtf8/isAscii | ✅ X509 全属性 + checkHost/verify/toLegacyObject | ✅ |
| N22-B9 | process.umask/cpuUsage、spawnSync/execFileSync/execSync | ✅ 超时/编码/cwd/env/input 全支持 | ✅ |
| N22-B10 | http.Agent（keepAlive 连接复用） | ✅ API 完整；精确连接计数复用留后续 | ✅ |

**M3 验收**：2.4 表 API MISS 全部清零；`tests/conformance/node22/modules/` 差分全绿。

### 5.5 M4：缺失模块（P1-模块，复杂度最高）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| N22-B1 | node:dgram | UDP：createSocket/send/bind/message；标准库 net 直实现，最易 | [ ] |
| N22-B2 | node:cluster | master/worker/fork/IPC 基础（收敛基础场景） | [ ] |
| N22-B3 | node:http2 | session/stream 基础：connect/request/respond（收敛单 session） | [ ] |
| N22-B4 | node:inspector | 仅 API 面 Session/url（不做 CDP 协议，纯 Go 无 V8） | [ ] |

**M4 验收**：echo（UDP）、fork+IPC（cluster）、流式请求（http2）差分通过；inspector API 面存在性验证。

### 5.6 M5：测试与工具链（P3）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| N22-D1 | node:test 补全 | mock（mock.fn/mock.method）、coverage（`aluka test --coverage`）、snapshot、spawn | [ ] |
| N22-D2 | REPL 补全 | 多行编辑、.editor、历史文件 | [ ] |
| N22-D3 | trace_events | 可选：Tracing 对象最小面 | [ ] |

**M5 验收**：node:test 差分（mock/coverage 场景）与 node22 一致；REPL 交互基线。

### 5.7 执行顺序与并行

```
M1 ──→ M3 ──→ M5          （主线：运行时语义 → API → 工具链）
 └─→ M2（可并行，独立）     （ES2024 随时穿插）
      └─→ M4（依赖 M1 后即可启动，可与 M3 并行）
```

- **M1 是唯一硬前置**（差分基线 + 语义修复）
- M2 独立可随时插入；M4 与 M3 无依赖可并行（不同文件域）
- 每个里程碑独立提交（`feat(node22): M<n> ...`），文档状态表同步勾选

---

## 6. 测试策略

```
tests/conformance/node22/
├── run.sh                    # 差分框架：同一脚本 aluka vs node22 双跑对比
├── runtime/                  # P0：for-await 流/thisArg/require(esm)/事件循环
├── modules/                  # P1：dgram/cluster/http2/assert/fs/util 差分
├── es2024/                   # P2：Promise.withResolvers/groupBy/toSorted 等
└── test-runner/              # P3：mock/coverage/snapshot
```

- **差分框架**：每个用例输出 stdout 规范化为 `result: <值>`，aluka 与 node22 输出逐行对比
- 门禁：`go test ./...` + 全部 conformance（node/test262/npm/install/express/build/node22）
- CI：ubuntu 上 node22 差分 job（Node 22 预装于 GitHub Actions runner）

## 7. 验收标准（总纲）

1. `for await...of` 流迭代、`thisArg`、`require(esm)` 与 Node 22 行为一致
2. dgram/cluster/http2 基础场景（echo/多进程/流式请求）差分通过
3. ES2024 全部缺失项补齐，差分脚本 100% 一致
4. node:test 支持 mock + coverage（`aluka test --coverage`）
5. 每阶段完成：Go 回归 + 差分用例 + 文档状态表更新

## 8. 风险与边界

| 风险 | 应对 |
|------|------|
| `for await` 流迭代需 asyncIterator 协议（Symbol.asyncIterator + next() Promise 链） | 复用 fromWeb 桥接模式（A1 已验证可行） |
| `require(esm)` 需要 ESM 同步求值（Node 用 CJS-ESM interop 层） | 复用现有 ESM→CJS 转换管线 + 缓存；无 TLA 的 ESM 可同步 |
| cluster/http2 复杂度高 | 收敛到"基础场景"：fork+IPC / 单 session 流式请求；协议细节留白 |
| inspector 是协议级（CDP） | 仅 API 面（Session/url），不实现调试协议（纯 Go 无 V8） |
| dgram 依赖系统 UDP | 标准库 net 包可直接实现 |
| ES2023 数组方法（toSorted 等）缺失面需先核对 | C1 任务先做全量核对再补 |
| 差分测试依赖 node22 环境 | CI 提供；本地无 node 时 SKIP |
| require 含 TLA 的 ESM（Node 22 报 ERR_REQUIRE_ASYNC_MODULE） | aluka 同步等待成功——**超集**，记录为已知差异（不强制对齐） |
| 非严格模式回调 `this=undefined` → globalThis（Node 语义） | aluka 回调 this 保持 undefined——已知差异（严格模式代码不受影响） |
| `\uXXXX` surrogate 转义（如 `'\uD800'`）：Node 保留码元（isWellFormed=false） | aluka 字节字符串模型在 lexer 层将 surrogate 替换为 U+FFFD（isWellFormed 恒 true）——架构级差异，记录不修 |

## 9. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-08-06 | 初稿：四维基线矩阵（45 项实测探测）、P0-P3 分级、阶段 A-D WBS、差分测试策略 |
| v1.1 | 2026-08-06 | **里程碑规划**：阶段 A-D 打包为 M1-M5（运行时语义 → ES2024 → API → 模块 → 工具链），含工作量估、依赖关系、执行顺序与并行策略 |
| v1.2 | 2026-08-06 | **M1 完成**：for-await 流迭代（Symbol.asyncIterator）、Array thisArg、require(esm)（+__esModule）、nextTick 差分一致、node22 差分框架 4/4；已知差异 2 项记录（TLA require 超集、非严格模式 this） |
| v1.3 | 2026-08-06 | **M2 完成**：ES2024 全局全量（Promise.withResolvers、Array.fromAsync、Object.groupBy、Map.groupBy、String.isWellFormed/toWellFormed、Array.toSorted/toReversed/toSpliced/with、Object.hasOwn）+ navigator/BroadcastChannel 全局对象；差分框架 6/6；已知差异 +1（surrogate 转义模型，架构级） |
| v1.4 | 2026-08-06 | **M3 完成**：常用 API 补全（assert.match 系、util.parseArgs 按 node 源码移植、fs.cp/glob 40+ 差分、path.matchesGlob、crypto.X509Certificate 全属性、Buffer.isUtf8/isAscii、process.umask/cpuUsage、spawnSync 三件套、http.Agent）；顺带修复 JSON.stringify 键序/HTML 转义、Infinity/NaN String() 格式；差分框架 8/8 |
