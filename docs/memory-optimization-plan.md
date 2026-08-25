# 内存占用优化实施方案（pi-aluka.exe）

> 项目代号：`aluka`（Go 运行时）+ `pi/coding-agent`（TS payload）｜ 文档版本：v1.0 ｜ 日期：2026-08-10
> 适用产物：`packages/coding-agent/dist/pi-aluka.exe`（52MB，aluka `--compile` 产物）
> 前置依据：`docs/perf-memory-optimize-plan.md`（运行时内部内存优化，已实施 ME-1/2/3/7）、
> `docs/perf-optimization-plan.md`（性能，已实施 O-5/6）
> 问题现象：**启动即占用 ~300MB；开始对话后涨到 ~500MB**
> 本文目标：**启动 RSS ≤150MB；对话稳态 RSS ≤300MB**（长跑不单调增长）

---

## 1. 架构与内存构成

### 1.1 产物组成

`pi-aluka.exe` = `[aluka Go 运行时二进制 ~34MB]` + `[coding-agent TS payload 字节码]`

- aluka 二进制由 Makefile 构建（`CGO_ENABLED=0 go build ./cmd/aluka`），不含 `go:embed`
  资产；34MB 全部来自 Go 代码 + 链接依赖（`modernc.org/sqlite` 为最大贡献者，其次
  `pgx`/`go-redis`/`brotli`/`gorilla/websocket`）。
- coding-agent payload 经 `aluka build --compile` 追加到二进制尾部
  （footer 魔数 `ALUKAFTR` + 偏移 + SHA256；见 `internal/bundler/compile/payload.go`）。
- 运行时 aluka 反序列化 payload 字节码 → 构建模块加载器 → 注册 Node 兼容内置模块
  → 执行 `dist/cli.js` → `main.js`。

### 1.2 内存分层

| 层 | 占用（启动） | 说明 |
|----|-------------|------|
| Go runtime + 链接库基线 | 30–60MB | 空程序 12–16MB；sqlite/pgx/redis 等包级变量与 init 链叠加 |
| TS payload 模块图 | 80–150MB | **主因：highlight.js 全量 191 语言 eager 加载** + TUI 组件图 |
| 资源（context/skills/prompts/themes） | 5–30MB | `resource-loader` 扫描并全量读取 |
| 对话增长（运行时） | +100–200MB | session 历史全量 join + 字符串 rope 残留 + Go GC 不归还 OS |

---

## 2. 根因分析

### 2.1 启动内存根因（~300MB）

#### R1 【P0】highlight.js 全量语言 eager 加载（最大元凶）

`src/utils/syntax-highlight.ts:1`：
```ts
import hljs from "highlight.js/lib/index.js";
```

`highlight.js/lib/index.js` 加载时**同步 `require` 全部 191 种语言定义**
（`lib/languages/` 源码共 72MB，每份含正则/关键字表，实例化后常驻）。

eager import 链（与是否进入交互模式无关，入口即走完）：
```
dist/cli.js (入口)
  → main.ts:35      import { InteractiveMode, runPrintMode, runRpcMode } from "./modes/index.js"
  → modes/index.ts  export { InteractiveMode } from "./interactive/interactive-mode.js"
  → interactive-mode.ts  import { ... } from "@earendil-works/pi-tui"  (12MB)
  → theme/theme.ts:9     import { highlight, supportsLanguage } from "../../../utils/syntax-highlight.ts"
  → syntax-highlight.ts  import hljs from "highlight.js/lib/index.js"   ← 191 语言全部求值
```

**影响**：`--print`/`--rpc`/`--version`/`--help`/`--list-models` 等所有路径都被迫加载
全部语法表。估算 60–120MB。

#### R2 【P0】main.ts 顶层静态 import 整个 TUI 组件图

`src/main.ts` 顶部静态 import `./modes/index.js` → `interactive-mode.js`
（246KB 单文件，静态 import ~60 个组件，`dist/modes/interactive/` 共 28MB）。

无论 appMode 是 `print`/`rpc`/`interactive`，整个 TUI 组件图与 pi-tui（12MB）都在
启动时求值。`undici`（54MB 磁盘）、`diff`（22MB）、`glob`（9.4MB）作为依赖被拉入模块图。

#### R3 【P1】aluka 启动期分配不归还 OS

`internal/engine/gc.go:89`：`debug.FreeOSMemory()` **只在显式 `gc()` 时调用**。
启动期反序列化 payload、构建全局对象（`process`/`console`/`Buffer`/`fetch`/`Intl`…）、
注册全部内置模块（fs/http/crypto/sqlite/zlib…）产生大量中间分配；这些被 Go GC 回收后
**RSS 不下降**（`docs/perf-memory-optimize-plan.md:37` 已记录此结构性问题）。

#### R4 【P1】内置模块启动即全量注册

`internal/builtin/module.go:34` `builtinModulesSet` 在进程启动时构造，相关模块的包级
变量/init 链（sqlite 驱动注册、http 默认 transport 等）随之求值，即使用户程序从不
`require('sqlite')`。

#### R5 【P2】payload 未压缩

`--compile` payload 常量池字符串未共享/未压缩（~~aluka ME-8 已规划未实施~~ →
**已闭环**：PayloadVersion v3 zlib 压缩已落地，39MB→3.2MB（-91.8%），见
`docs/performance-and-functionality-evaluation.md` 与 README `--analyze/--max-payload`；
2026-08-25 gap-closure-plan D4 回填）。反序列化时峰值内存 ≈ 压缩后 payload 大小。

### 2.2 运行时内存增长根因（300MB → 500MB）

#### R6 【P0】session 历史全量读入内存并 join 成巨型字符串

`src/core/session-manager.ts:687-760` `buildSessionInfo`：
```ts
const allMessages: string[] = [];           // :693
// 逐行读 jsonl
allMessages.push(textContent);              // :732  累积每条 user/assistant 文本
...
allMessagesText: allMessages.join(" "),     // :760  再 join 成单巨型字符串常驻
```

`--resume`/`--continue`/列 session 时，**每个 session 文件的全部对话文本**被读入并
join 成单字符串。`buildSessionContext`（`:232`）还会重建整条消息链供 LLM 使用。
长会话轻松几十 MB。`buildSessionInfosWithConcurrency`（`:771`）并发加载多个 session 时
放大此开销。

#### R7 【P0】aluka Go GC 不定期归还 OS（RSS 单调增长根因）

`jsHeap.sweepLocked`（`gc.go:52`）每 4096 次分配清扫弱引用注册表，但**不触发
`runtime.GC()`/`debug.FreeOSMemory()`**。对话过程中大量字符串拼接（rope 节点）、
对象分配（Shape/slots）、工具输出缓冲被 Go GC 回收后，物理页不归还 → RSS 只涨不降。
这是"对话越久内存越高"的主因。

#### R8 【P1】无界全局注册表

| 位置 | 标识 | 问题 |
|------|------|------|
| `internal/builtin/crypto_x509.go:27` | `x509CertMap` | 每个 cert 对象一个条目，永不清理 |
| `internal/builtin/http_agent.go:18` | `httpAgentTransports` | 每个 Agent 一个连接池，永不清理 |

JS 侧 `allMessages`/`allMessagesText`（R6）也是无界累积。

#### R9 【P1】字符串 rope 残留 + 对象分配密度

ME-1 rope 优化后，50K 拼接 RSS 已从 200MB 降到 74MB（`perf-memory-optimize-plan.md:157`），
但对话中 LLM 流式 token、diff、markdown 渲染仍产生大量 rope 节点，展平前累积。
200K 短生命周期对象 RSS 132–146MB（`:160`），对话工具调用加剧此开销。

#### R10 【P2】对话上下文未定期压缩

`core/compaction/` 目录存在，但需确认长对话是否定期 summarize/截断历史。全量历史
保留在内存并送给 LLM，长会话内存线性增长。

---

## 3. 优化目标与里程碑

| 里程碑 | 启动 RSS | 对话稳态 RSS | 相对当前 |
|--------|---------|-------------|---------|
| 当前基线 | ~300MB | ~500MB | — |
| **M-M0**（P0 完成） | **≤180MB** | **≤350MB** | -40% / -30% |
| **M-M1**（P1 完成） | **≤150MB** | **≤300MB** | -50% / -40% |
| **M-M2**（P2 完成） | **≤130MB** | **≤280MB** | -57% / -44% |

验收硬指标：
- `pi-aluka.exe --version` RSS ≤ 80MB（最小路径）
- `pi-aluka.exe --help` / `--list-models` RSS ≤ 120MB（无 TUI 路径）
- 交互模式启动 RSS ≤ 180MB
- 10 轮对话后 RSS 增长 ≤ 50MB（长跑稳定，无单调增长）

---

## 4. 任务分解

### P0 —— 启动期高收益（M-M0）

| ID | 任务 | 方案 | 预期收益 | 工作量 | 位置 |
|----|------|------|---------|--------|------|
| **M-1** | **highlight.js 按需加载** | `syntax-highlight.ts` 改 `import hljs from "highlight.js/lib/core.js"`（仅核心，~73KB）；首次 `highlight()` 时按需 `await import("highlight.js/lib/languages/<lang>.js")` 注册。维护一个常用语言白名单（ts/js/python/go/rust/bash/json/yaml/md/java/c/c++/html/css/sql/shell/docker…约 20 种）预热，其余首次命中再加载。`highlight()` 改为 async 或提供同步回退（core.js 已注册的语言同步走，未注册的语言先 await 再同步）。 | **-60~120MB** | ~80 行 | `src/utils/syntax-highlight.ts` |
| **M-2** | **main.ts 拆动态 import** | `./modes/index.js` 等重型 import 改为在 `resolveAppMode()` 确定 appMode 后动态加载：interactive 分支 `await import("./modes/interactive/interactive-mode.js")`；print 分支仅加载 `print-mode`；rpc 分支仅加载 `rpc-mode`。`--version`/`--help`/`--list-models`/`config`/`package`/`credential-print` 等纯命令路径完全跳过 modes 加载。 | **-40~80MB**（非交互模式） | ~60 行 | `src/main.ts:35` 及各 appMode 分支 |
| **M-3** | **session 历史不全量 join** | `buildSessionInfo` 移除 `allMessages[]` 累积与 `allMessagesText: allMessages.join(" ")`；只保留 `firstMessage` + `messageCount` + `lastActivityTime` + `name`。全文检索（若 session-picker 需要 fuzzy 匹配）改为流式扫描 jsonl 或单独的 search 命令，命中即停。`SessionInfo` 类型移除或懒化 `allMessagesText` 字段。 | 长会话 **-几十MB** | ~40 行 | `src/core/session-manager.ts:687-760,185-187` |
| **M-4** | **aluka 定期归还 OS 内存** | `jsHeap.sweepLocked` 后按更高阈值（如每 16 次 sweep，即 ~65K 分配）调用一次 `runtime.GC()` + `debug.FreeOSMemory()`，而非只在显式 `gc()`。阈值参数化为 `freeOSEverySweep`，可用 `ALUKA_FREEOS_INTERVAL` 环境变量调。避免每次 sweep 都 `FreeOSMemory`（有 syscall 成本）。 | 长跑 **-30~80MB** | ~30 行 | `internal/engine/gc.go:52` |

### P1 —— 运行时与启动辅助（M-M1）

| ID | 任务 | 方案 | 预期收益 | 工作量 | 位置 |
|----|------|------|---------|--------|------|
| **M-5** | **aluka 启动后主动 gc** | `cmd/aluka/main.go` 在 payload 加载、全局对象初始化完成后、执行用户入口前，调用一次全局 `gc()`（已含 `debug.FreeOSMemory`），归还启动期临时分配。可在 `--compile` 运行模式的入口注入。 | 启动 **-20~40MB** | ~10 行 | `cmd/aluka/main.go` 启动序列 |
| **M-6** | **内置模块懒注册** | fs/http/crypto/sqlite/zlib 等 Node 内置模块改为首次 `require`/`import` 时才初始化模块对象，而非启动时全量建表。`builtinModulesSet` 仅登记名字，`require` 命中时按需构造。 | 启动 **-10~20MB** | ~150 行 | `internal/builtin/module.go:34` 及各模块构造点 |
| **M-7** | **无界注册表接入弱引用清理** | `x509CertMap`/`httpAgentTransports` 改用 `weak.Pointer` 键或挂到对象 finalize 路径，在对象 GC 时移除条目（复用 `gc.go` 的弱引用机制）。 | 视使用量 | ~60 行 | `internal/builtin/crypto_x509.go:27`、`http_agent.go:18` |
| **M-8** | **函数帧复用（ME-4）** | 实施已规划的 ME-4（`perf-memory-optimize-plan.md:82`）：非挂起帧（同步调用、非 async/generator）入池复用，降低 fib 类递归与对话中短函数调用的帧分配。 | 对话 **-10~20MB** | ~300 行 | `internal/engine/interpreter/vm.go` |

### P2 —— 长期与压缩（M-M2）

| ID | 任务 | 方案 | 预期收益 | 工作量 | 位置 |
|----|------|------|---------|--------|------|
| **M-9** | **payload 压缩（ME-8）** | `--compile` payload 常量池字符串共享 + LZ 风格压缩；运行时按需解压单模块。降低二进制大小与反序列化峰值内存。 | 启动 **-5~15MB** | ~400 行 | `internal/bundler/compile/payload.go` |
| **M-10** | **对话上下文定期压缩** | 确认 `core/compaction/` 在长对话（如 >50 轮）自动 summarize/截断历史，仅保留摘要 + 近期 N 轮原文；压缩后释放旧消息内存。 | 长会话显著 | 视现状 | `src/core/compaction/`、`agent-session.ts` |
| **M-11** | **TUI 组件懒加载** | interactive-mode 的 ~60 个组件（selector/editor/dialog）按需动态 import，首屏只加载必需组件。 | 启动 **-10~20MB** | ~200 行 | `src/modes/interactive/interactive-mode.ts` |

---

## 5. 执行顺序与依赖

```
P0（独立、低风险、高收益，先做）：
  M-1（highlight 按需）  ← 纯 TS 改动，独立验证
  M-2（main 动态 import）← 纯 TS 改动，独立验证
  M-3（session 不全量）   ← 纯 TS 改动，独立验证
  M-4（aluka 定期归还）   ← Go 改动，需重编 aluka + 重打 pi-aluka.exe

P1（依赖 P0 基线测量）：
  M-5（启动 gc）          ← 依赖 M-4 的 FreeOSMemory 基础
  M-6（内置模块懒注册）    ← 独立，影响面大需回归
  M-7（弱引用清理）        ← 独立
  M-8（帧复用 ME-4）       ← 独立，aluka 侧

P2（长期）：
  M-9（payload 压缩）      ← 依赖 ME-8 规划
  M-10（上下文压缩）       ← 需先调研 compaction 现状
  M-11（TUI 懒加载）       ← 依赖 M-2 的动态 import 基础设施
```

**建议首批交付 M-1/M-2/M-3（纯 TS，当天可验证）+ M-4（Go，需重编）**，
预期即达到 M-M0（启动 ≤180MB，对话 ≤350MB）。

---

## 6. 验证与回归

### 6.1 内存基线测量脚本

新建 `packages/coding-agent/scripts/mem-baseline.mjs`（或 ts），跨平台读 RSS：

```js
// 伪代码：分别启动各路径，采样 RSS 峰值
const cases = [
  ["--version", "version"],
  ["--help", "help"],
  ["--list-models", "list-models"],
  ['"hi"', "print-1round"],          // print 模式 1 轮对话
  ["-i", "interactive-startup"],     // 交互启动（自动退出）
];
// 每个用例：spawn pi-aluka.exe args，轮询 process.memoryUsage().rss 或 OS 级 RSS
// 输出表格：路径 / 峰值RSS / 基线差 / 是否达标
```

> 注意：aluka 自身有 `--monitor`（ME-7）可报告峰值/分配数；优先用 `--monitor` 取
> 引擎内部数据，OS 级 RSS 用平台工具（Windows: `tasklist /fi`，或 PowerShell
> `Get-Process pi-aluka`；Linux: `ps -o rss`）。

### 6.2 验收命令

```bash
# TS 侧改动后（在 pi monorepo）
cd packages/coding-agent
npm run build                                        # tsgo -p tsconfig.build.json
# 用 aluka 跑构建产物（验证 aluka 兼容性）
aluka dist/cli.js --version
aluka dist/cli.js --help
aluka --monitor dist/cli.js -i                       # 看 --monitor 内存报告

# 重打 pi-aluka.exe（M-4/M-5/M-6/M-8 后需要）
# 在 aluka_lang 仓库
make bin/aluka                                       # 或 go build ./cmd/aluka
# 在 pi 仓库
bun build --compile ./dist/bun/cli.js ./src/utils/image-resize-worker.ts \
  --outfile dist/pi-aluka                            # 参照 package.json build:binary

# aluka Go 回归
cd /e/codes/go_projects/aluka_lang/aluka_lang
go test ./...
go test ./bench -bench . -benchmem -count=3

# conformance 回归（确认 Node 兼容性不倒退）
cd tests/conformance/node22 && ALUKA=aluka bash run.sh
```

### 6.3 达标判定

每个任务需附实测前后对比（同机、≥3 次取样取中位数），填入下表模板：

| 用例 | 基线 RSS | 优化后 RSS | 变化 | 达标 |
|------|---------|-----------|------|------|
| `--version` | — MB | — MB | —% | ✅/❌ |
| `--help` | — MB | — MB | —% | ✅/❌ |
| 交互启动 | — MB | — MB | —% | ✅/❌ |
| 10 轮对话后 | — MB | — MB | —% | ✅/❌ |

---

## 7. 风险与回退

| 风险 | 应对 |
|------|------|
| **M-1 按需加载导致首块高亮延迟**（首次 await import 语言文件） | 预热常用 20 语言（同步 require）；未命中语言显示纯文本降级，不阻塞渲染 |
| **M-1 `highlight()` 改 async 破坏调用方** | 保留同步 `highlight()`（core 已注册语言同步走）；新增 `highlightAsync()` 处理需动态加载的语言；调用方渐进迁移 |
| **M-2 动态 import 在 aluka 下的兼容性** | aluka 支持 ESM 动态 `import()`（`internal/runtime/module/esm.go`）；需验证 `--compile` 模式下动态 import 能命中 payload 字节码缓存（`bc_cache.go`）而非回退源码解析 |
| **M-3 移除 `allMessagesText` 破坏 session fuzzy 搜索** | 先 grep 全仓 `allMessagesText` 调用点（`session-manager.ts` 内部 + session-selector UI）；改为流式扫描或保留前 N 条文本 |
| **M-4 `FreeOSMemory` 频率过高影响性能** | 阈值参数化（默认 ~65K 分配一次），可 env 调；用 `--monitor` 观察 GC 停顿；过高则回调 |
| **M-6 懒注册破坏"内置模块同步可用"语义** | 仅延迟模块对象构造，不延迟 `require` 解析；首次 require 透明触发；回归 `node:fs` 等用例 |
| **M-8 帧复用与 async/generator 冲突** | 仅池化同步非挂起帧（已在 ME-4 风险表记录）；async/生成器帧不参与 |

回退策略：每个任务独立 commit，内存基线脚本不达标即 `git revert`，不影响其他任务。

---

## 8. 调研待办（实施前需确认）

- [ ] **M-2**：确认 aluka `--compile` 模式下动态 `import()` 是否命中 payload 字节码缓存。
      若回退到源码路径，需先扩展 `bc_cache.go` 支持动态 import 的预编译。
- [ ] **M-3**：grep `allMessagesText` 全部消费点，确认哪些 UI/搜索依赖全文。
- [ ] **M-10**：调研 `src/core/compaction/` 当前是否启用自动压缩、阈值多少、压缩后
      是否释放旧消息内存（`agent-session.ts` 中 compaction 调用点）。
- [ ] **M-1**：统计 coding-agent 实际会高亮的语言集合（从 markdown 代码块语言标记），
      确定预热白名单。

---

## 9. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-08-10 | 初稿：基于 pi-aluka.exe 启动 ~300MB / 对话 ~500MB 现象，定位 10 项根因（R1-R10），分 P0/P1/P2 共 11 项任务（M-1~M-11），含里程碑、验收、风险与调研待办 |
