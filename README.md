<p align="center">
  <img src="./assets/logo.svg" alt="Aluka" width="132">
</p>

<h1 align="center">Aluka</h1>

> 用纯 Go 实现的、兼容 Bun（JavaScript 运行时）的运行时引擎。

[![CI](https://github.com/aluka-lang/aluka/actions/workflows/ci.yml/badge.svg)](https://github.com/aluka-lang/aluka/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/aluka-lang/aluka.svg)](https://pkg.go.dev/github.com/aluka-lang/aluka)
[![Go Report Card](https://goreportcard.com/badge/github.com/aluka-lang/aluka)](https://goreportcard.com/report/github.com/aluka-lang/aluka)

## 项目目标

Aluka 旨在用纯 Go 实现一个 JavaScript/TypeScript 运行时，**API 行为兼容 [Bun](https://bun.sh/)**：

- 直接运行 JS/TS/JSX/TSX 文件（零配置、零外部编译器预构建）
- 兼容 Node.js 内置模块（fs / path / http / crypto / stream / v8 / inspector ...）
- 兼容 Web API（fetch / WebSocket / Streams / SubtleCrypto / CompressionStream / Intl ...）
- 兼容 Bun 特有 API（Aluka.serve / Aluka.file / Aluka.$ / Aluka.ipc ...）
- 原生 IPC 通讯协议（AIP）与 `aluka:plugin:*` 动态跨进程透明 RPC 代理
- 单二进制分发，零运行时依赖

## 项目状态

> 评估日期：2026-08-22 ｜ 测试总数：~1,570 个 Go 测试函数（全量通过，0 失败）

| Phase | 名称 | 状态 | 完成度 |
|-------|------|------|--------|
| 0 | 工程基座 | ✅ 完成 | 100% |
| 1A | AST-walking 解释器 | ✅ 完成 | ~95% |
| 1B | 字节码 VM | ✅ 完成（含隐藏类/IC、自研 GC） | ~98% |
| 1C | ES2015 + 模块系统 | ✅ 完成（含 package.json exports/imports、循环依赖） | ~98% |
| 1D | TS 转译 + JSX/TSX + 装饰器 | ✅ 完成（源码级 JSX/TSX Lowering、TC39 Stage 3 装饰器） | ~98% |
| 2 | Node.js 核心内置模块 | ✅ 完成（45+ 内置模块对齐 Node 22） | ~98% |
| 3 | Web API + 国际化 + 可观测性 | ✅ 完成（SubtleCrypto、Web 压缩流、Intl 全家桶、Chrome 堆快照、CDP） | ~98% |
| 4 | Aluka 特有 API（兼容 Bun） | ✅ P0+P1+P2 完成（SQL/Redis/S3/Shell/IPC） | ~100% |
| 5 | 包管理器 | ✅ P0 完成（含 workspace、.npmrc、lockfile） | ~95% |
| 8A | 原生 IPC 协议与动态插件系统 | ✅ 完成（AIP 二进制协议、aluka:plugin:* 透明代理、同步/异步/100+ 并发多路复用） | ~100% |
| Pi | 真实世界兼容（Pi Agent Harness 靶标） | ✅ 阶段 A/B/C 完成 | ~95% |
| 6 | 测试器 | ✅ node:test 兼容完成（describe/it/mock/coverage/快照/TAP 报告，差分 15/15） | ~85% |
| 7 | 打包器 | ✅ `--compile` 单文件可执行 + `--target=web` 浏览器 bundle（React/Vue SFC、CSS/HTML、chunk、watch/dev、ESM/CJS/UMD） | ~90% |
| 8B | JIT 优化与监控 | 🔨 JIT R1-R5 完成、**默认 auto**（Windows 实机 ~12x Node / mixed ~2.2x） | ~75% |
| N22 | Node 22 兼容 | ✅ M1-M5 差分全绿（运行时语义/ES2024/API 补全/dgram·cluster·http2·inspector/工具链） | ~95% |

### 核心能力一览

- **JS/TS/JSX 引擎（自研）**：AST 解释器 + 字节码 VM 双引擎、隐藏类 + 内联缓存、自研标记-清除 GC、磁盘字节码缓存、V8 风格错误堆栈、**源码级 JSX/TSX 即时转译**（原生运行 React 18 SSR / Vue 3）、**ECMAScript 属性描述符与 exotic object 语义**（Array holes/length、Proxy invariants、Reflect、seal/freeze）、**TC39 Stage 3 / TypeScript 装饰器（Decorators）**（原生支持 Nest.js / TypeORM / MobX）
- **ES 特性**：ES5 全部核心、ES2015（let/const/class/箭头函数/解构/Promise/Symbol/Map/Set/Proxy/Reflect/生成器/模块/tagged template）、ES2017-2024（async/await、**top-level await**、for await...of、可选链 `?.`、BigInt、动态 `import()`、**import attributes**、`Promise.withResolvers`、`Array.fromAsync`、`Object.groupBy` 等）
- **模块系统与 package.json 规范**：ESM（import/export 全语法）+ CJS（require/module.exports）+ Node.js 现代解析算法 + **`package.json` 深度兼容**（`"exports"` 条件导出映射、`"imports"` 包内私有路径、`"main"`、`"module"`、`"type": "module"`）+ browser/Node 实例级 resolver 条件隔离 + 循环依赖 + 字节码磁盘缓存
- **RegExp 引擎**：JS RegExp → Go RE2 翻译快路径 + 自研回溯 fallback（前后行断言、反向引用、字符类子集）；legacy/`u` 模式按 ECMAScript UTF-16 索引返回 capture、`lastIndex` 和 replace/split 偏移；回溯预算超限显式报错，并以 compiler-sfc 真实语料、双引擎对拍和 Node 22 oracle 回归。
- **Node.js 内置模块（45+）**：fs、path、os、url、querystring、events、util、assert、stream、buffer、crypto、string_decoder、http、https、net、tls、dns、zlib（含 **zstd**）、child_process、worker_threads、perf_hooks、timers/promises、readline、repl、module、**v8（含 writeHeapSnapshot/getHeapSnapshot Chrome 堆快照）**、**inspector（CDP 协议会话）**、tty、**sqlite（DatabaseSync）**、**test（node:test）**、dgram、cluster、http2 等
- **Web API & 国际化**：fetch/Request/Response/Headers/FormData、WebSocket、ReadableStream/WritableStream/TransformStream、**CompressionStream / DecompressionStream**（gzip/deflate/deflate-raw）、Blob/File、**Web Cryptography API (`crypto.subtle` / SubtleCrypto)**、URL/URLPattern、MessageChannel、AbortController、**ECMAScript `Intl` 国际化全家桶**（`DateTimeFormat`、`NumberFormat`、`RelativeTimeFormat`、`ListFormat`、`PluralRules`、`Collator` 自然排序、`Segmenter`）、**structuredClone**
- **Aluka 原生 IPC 协议（AIP）与动态插件系统**：
  - 16 字节大端序定长二进制帧头（Magic `0x414C4B01`），跨平台支持 Windows 命名管道与 Unix Domain Socket / TCP Loopback；
  - 脚本中直接 `import plugin from "aluka:plugin:xxx"` 或 `require("aluka:plugin:xxx")` 与 Rust / C++ / Python / Go 外部服务透明通信；
  - 支持 **异步非阻塞 Promise (`call`)**、**同步阻塞 (`callSync`)**、**单物理连接 100+ 并发多路复用** 与 **PubSub 事件流广播 (`emit`/`on`)**。
- **Aluka API（兼容 Bun）**：`Aluka.serve`、`Aluka.file`/`write`、`Aluka.$`、`Aluka.env`、`Aluka.sleep`、`Aluka.hash`/`password`、`Aluka.deflate`/`inflate`、`Aluka.spawn`、`Aluka.ipc`、`Bun.peek`/`deepEquals` 等（`Bun` 为兼容别名）
- **外部服务驱动**：`Aluka.SQL`（SQLite 零配置 + Postgres 经 `DATABASE_URL`，支持 tagged template 参数绑定）、`Aluka.Redis`（get/set/hget/hset...）、`Aluka.S3`（自研 AWS SigV4，get/put/delete/list/exists）
- **包管理器**：`aluka install/add/remove/update`、npm registry 客户端、自研 semver 解析、依赖树解析 + hoisting、并发下载解压、`aluka.lock` lockfile、workspace 多包管理、.npmrc（registry + 鉴权 token）
- **JIT 编译器（默认开启，--jit=off 回滚）**：Quick 类型化 IR（跨平台）+ amd64 Native 机器码两层（W^X/崩溃隔离/safepoint/OSR）；生成式差分（jitdiff 三 tier 零失配）+ 5 个 Go fuzz target
- **打包器**：`aluka build --compile` 生成静态单文件可执行产物；`aluka build --target=web` 生成浏览器 bundle，支持 JS/TS/JSX/TSX、CSS/HTML 入口、多 entry、sourcemap、动态 `import()` chunk、tree-shaking/minify、ESM/CJS/UMD、`--watch` 与 `aluka dev`。Vue SFC 提供默认纯 Go subset 后端和 `--vue-compiler=official` 官方 compiler-sfc 后端（`<style>` / `src` / scoped 已接入 graph CSS 管线）。
- **桌面 GUI**：Windows WebView2 已落地；macOS WKWebView 第一刀（syscall + libobjc，无 CGO、无 Vibrancy；`aluka://` 仅顶层 HTML inline）；Linux 及其它非 Darwin 平台明确报错而非静默 stub

## 约束

- **纯 Go 实现** — 禁用 CGO，所有代码 `//go:build !cgo`
- **核心组件自研** — JS 引擎、模块系统、事件循环、TS/JSX 转译器全部自研
- **单二进制分发** — 静态编译，零运行时依赖
- **不引入第三方 JS 引擎** — 拒绝 V8 / QuickJS / Goja，纯 Go 原生实现

详见 [需求分析文档](./docs/requirements-analysis.md)。

## 快速开始

### 构建

```bash
# 本机构建
make build

# 或直接用 go
CGO_ENABLED=0 go build -o bin/aluka ./cmd/aluka
```

### 使用

```bash
# 执行内联代码
aluka -e "console.log(1+1)"
# => 2

# 执行文件（JS 或 TS）
aluka run hello.js
aluka hello.ts    # 简写

# 交互式 REPL
aluka repl

# 包管理
aluka install          # 安装 package.json 依赖
aluka add is-number    # 添加依赖
aluka remove is-number # 移除依赖

# 单文件可执行产物（Phase 7，--compile）
aluka build --compile --outfile app ./src/index.ts
./app                  # 无 aluka/Go 环境直接运行（32MB，启动 ~50ms）

# 浏览器 bundle（JS/TS/JSX/TSX、CSS/HTML、动态 chunk）
aluka build --target=web --outdir dist ./src/index.ts

# Vue SFC：默认纯 Go subset；复杂 SFC 显式选择官方 compiler-sfc
aluka build --target=web --vue-compiler=official --outdir dist ./src/index.html

# 开发服务（全量 watch 重建、SPA fallback、health + reload SSE）
aluka dev --host 127.0.0.1 --port 3000 --outdir dist ./src/index.html
```

Vue SFC 默认使用纯 Go `subset` 后端。`--vue-compiler=official` 会在构建期执行
项目 `node_modules` 中的 compiler-sfc 及其传递依赖，权限与 `aluka run` 相同，
只应对可信依赖启用；失败时不会静默回退。已支持 `<script src>`、`<template src>`、
`<style>`（纯 CSS，含 scoped）。custom block、scss/less 等预处理器和 CSS modules
仍会明确报错。

```bash
# 优化并输出打包热点报告
aluka build --compile --optimize --analyze ./src/index.ts
aluka build --compile --analyze=json --analyze-out dist/analyze.json \
  --max-payload=2MB ./src/index.ts
```

### 示例

```bash
# 1. 基础语法与内联代码
$ aluka -e "console.log(process.platform, process.arch)"
win32 x64

$ aluka -e "console.log([1, 2, 3].map(x => x * 2))"

# -e 模式支持 require 与动态 import()（基于 cwd 解析，Node [eval] 语义）
$ aluka -e "console.log(require('os').platform())"
[ 2, 4, 6 ]

# 2. 源码级 JSX / TSX 即时执行（无需 Babel / Webpack / Vite）
$ aluka -e "const App = () => <div className='p-4 bg-blue-500'>Hello React JSX</div>; console.log(App().type);"
div

# 3. 动态跨进程插件 RPC 代理（通过 AIP 协议与外部 Rust/Go/Python 进程交互）
$ aluka -e "const mathService = require('aluka:plugin:math_engine'); console.log(mathService.callSync('add', [20, 22]));"
42

# 4. ECMAScript Intl 国际化全家桶
$ aluka -e "console.log(new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY' }).format(123456.78));"
¥123,456.78

# 5. Web Cryptography API (SubtleCrypto)
$ aluka -e "crypto.subtle.digest('SHA-256', Buffer.from('aluka')).then(b => console.log(Buffer.from(b).toString('hex')));"
6b6330058b...

# 6. Aluka.SQL (SQLite 零配置 + Postgres)
$ aluka -e "Aluka.SQL\`CREATE TABLE t (x INTEGER)\`.run().then(() => Aluka.SQL\`INSERT INTO t VALUES (42)\`.run()).then(() => Aluka.SQL\`SELECT * FROM t\`.all()).then(r => console.log(r[0].x));"
42
```

> 上面 SQL 示例使用零配置的 `:memory:` SQLite；设 `DATABASE_URL`（postgres:// 前缀）可切换 Postgres 后端。

## CLI 命令

| 命令 | 说明 |
|------|------|
| `aluka run <file>` / `aluka <file>` | 执行 JS/TS/JSX/TSX 文件（支持 .ts 导入、TLA、JSX 即时转译、import attributes） |
| `aluka -e <code>` | 执行内联代码（驱动事件循环，异步/Promise 可完整完成） |
| `aluka repl` | 交互式 REPL（状态保持、多行输入、`.help`/`.exit`） |
| `aluka test [dir]` | 测试运行器（node:test：describe/it + 目录递归发现） |
| `aluka install [pkg]` | 安装依赖（Phase 5，含 workspace/.npmrc） |
| `aluka add <pkg>` / `remove <pkg>` / `update` | 包管理 |
| `aluka build --compile [--outfile app] [--base <bin>] <entry>` | 单文件可执行产物（Phase 7）：嵌入预编译字节码，无 aluka/Go 环境可运行；`--base` 指定目标平台基座（跨平台） |
| `aluka build --compile --optimize <entry>` | 启用 tree-shaking、AST minify 和基础 VM 字节码优化 |
| `aluka build --compile --analyze[=text|json] <entry>` | 分析 payload、模块/资源热点、优化阶段收益和代码优化建议 |
| `aluka build --compile --analyze-only <entry>` | 只生成分析结果，不写原生可执行文件 |
| `aluka build --compile --max-payload=<size> <entry>` | 设置 payload 体积预算；超限退出码为 `2` |
| `aluka build --target=web [--outdir dist] <entry...>` | 浏览器 bundle：JS/TS/JSX/TSX、CSS/HTML、多入口、sourcemap、动态 chunk、tree-shaking/minify |
| `aluka build --target=web --format=esm\|cjs\|umd <entry>` | 选择 web 产物格式；UMD 可配 `--global-name` |
| `aluka build --target=web --vue-compiler=subset\|official <entry>` | Vue SFC 后端；默认 subset，official 在构建 VM 内执行项目 compiler-sfc 依赖 |
| `aluka build --target=web --watch <entry>` | 监听源文件并全量重建，失败后继续等待下一次变更 |
| `aluka dev [--host 127.0.0.1] [--port 3000] <entry>` | 构建并提供静态服务、SPA fallback、health 与 reload SSE |
| `aluka --vm` / `--ast` | 选择字节码 VM（默认）或 AST 解释器 |
| `aluka --no-cache` | 禁用字节码磁盘缓存 |
| `aluka --no-bytecode-opt` | 禁用编译管线默认的字节码优化（常量折叠/不可达删除等） |
| `aluka --jit=off\|quick\|auto` | JIT 分层开关（**默认 auto**；off = 完全关闭，回滚开关） |
| `aluka --jit-threshold=<n>` | 热叶函数编译阈值（默认 1000 次调用） |
| `aluka --jit-backedge-threshold=<n>` | 数值循环编译阈值（默认 10000 次回边） |
| `aluka --jit-trace-budget=<n>` | JIT 循环 yield 回边预算（默认 65536） |
| `aluka --jit-code-cache=<size>` | Native 代码缓存预算（默认 4MB） |
| `aluka --jit-stats` | 输出候选/编译/guard/deopt/淘汰聚合统计 |
| `aluka --jit-dump=ir\|asm` | dump 已验证 IR / amd64 汇编 |
| `aluka --monitor[=interval]` | 性能/内存/运行时指标监控（interval 如 `500ms`/`1s` 周期采样；默认仅终报；`--monitor-format=json`、`--monitor-out=<path>` 可选） |
| `aluka --max-memory=<n>` | 进程内存上限（`n` = 字节或 `KB`/`MB`/`GB` 后缀，如 `256MB`；环境变量 `ALUKA_MAX_MEMORY` 等效）。超限流程：先 GC → 仍超限则抛可捕获的 JS `RangeError: JavaScript heap out of memory`（V8 同款）→ 持续超限约 0.5s 后强制退出（码 3），防内存爆掉 |

## 运行时监控（--monitor）

`--monitor` 输出进程级性能/内存/运行时指标，用于观测长跑与高负载程序：

- **性能**：解释器指令数（含速率）、函数调用数、对象分配数、IC 命中率（get/set/call）
- **内存**：HeapAlloc/HeapInuse/HeapSys、历史峰值、Go 栈、JS 对象存活/累计、`--max-memory` 上限
- **运行时**：goroutines、GC 次数与暂停总时长、运行总耗时

```bash
aluka --monitor app.js                      # 结束输出 text 报告（stderr）
aluka --monitor=500ms app.js                # 每 500ms 周期采样 + 终报
aluka --monitor --monitor-format=json app.js  # JSON 单行（工具链友好）
aluka --monitor --monitor-out=metrics.json app.js
```

与 `--max-memory` 组合可同时观测与限制：`aluka --monitor --max-memory=256MB app.js`。


## 项目结构

```
aluka_lang/
├── assets/                   # 品牌资源（LOGO：assets/logo.svg）
├── cmd/
│   └── aluka/                 # CLI 入口（run/repl/test/install/build + 包管理子命令）
├── internal/
│   ├── cli/                   # 自研轻量 CLI 框架（注册式命令/flag 解析/帮助/错误/退出码）
│   ├── engine/                # JS 引擎（自研）
│   │   ├── lexer/             # 词法分析器
│   │   ├── parser/            # 递归下降 + Pratt 解析器（含 TS/JSX/装饰器解析）
│   │   ├── ast/               # AST 节点定义
│   │   ├── compiler/          # AST → 字节码
│   │   ├── bytecode/          # 指令集 / 序列化
│   │   ├── interpreter/       # AST 解释器 + 字节码 VM（属性描述符/Array/Proxy/Reflect、Date/URI/structuredClone/JIT 桥接）
│   │   ├── regex/             # RE2 翻译 + 回溯 fallback（UTF-16 索引、预算护栏、Node oracle）
│   │   ├── engine.go          # Engine/Context/Value 接口
│   │   ├── shape.go           # 隐藏类 + 内联缓存
│   │   └── gc.go              # 标记-清除 GC
│   ├── ipc/                   # Aluka 原生 IPC 协议（AIP：16B 帧头、全双工并发客户端/服务端、管道传输）
│   ├── runtime/
│   │   ├── globals/           # 全局对象与 Web API（按能力域分 12 个子包 + 装配入口，依赖为 4 层 DAG）
│   │   │   ├── gbase/         # 共享基座：WebIDL 注册 / Promise 驱动 / 参数归一 / JSON 互转
│   │   │   ├── gevent/        # Event/EventTarget、AbortController、DOMException、MessageChannel
│   │   │   ├── gbuffer/       # Buffer 全局与 node:buffer
│   │   │   ├── gencoding/     # TextEncoder/TextDecoder、atob/btoa
│   │   │   ├── gstream/       # WHATWG Streams、CompressionStream、Blob/File
│   │   │   ├── gfetch/        # fetch/Request/Response、WebSocket、URL、URLPattern
│   │   │   ├── gcrypto/       # crypto.subtle（SubtleCrypto）与 Aluka.hash/password
│   │   │   ├── gproc/         # process（argv/env/stdio/signals，平台分文件）
│   │   │   ├── gtimers/       # timers、performance、gc()
│   │   │   ├── gintl/         # ECMAScript Intl 国际化全家桶
│   │   │   ├── gconsole/      # console、navigator、BroadcastChannel
│   │   │   └── galuka/        # Aluka 特有 API 实现（gui/ipc/sql/redis/s3/shell/压缩）
│   │   └── module/            # ESM/CJS 模块系统 + package.json 规范 + aluka:plugin 动态透明 RPC 代理
│   ├── builtin/               # Node.js 内置模块（按领域分 18 个子包 + 注册表，依赖为 4 层 DAG）
│   │   ├── nodebase/          # 共享基座：参数取值 / 值比较 / 错误码 / JSON / Promise
│   │   ├── nodefs/  nodeos/   # fs、fs/promises ／ os、path、tty、constants
│   │   ├── nodenet/ nodehttp/ # net、tls、dns、dgram ／ http、https、http2
│   │   ├── nodecrypto/        # crypto 与 Web Cryptography
│   │   ├── nodetest/          # node:test 运行器
│   │   ├── nodediag/          # async_hooks、perf_hooks、inspector、v8、domain
│   │   └── ...                # nodestream/nodeproc/nodeutil/nodevm/nodetimers/noderepl/nodesqlite/nodeassert/nodeevents/nodeglob
│   ├── bundler/               # 可执行/web 打包器（graph/shake/minify/emit/Vue SFC）
│   ├── gui/                   # 跨平台桌面 GUI 框架（Windows WebView2 / macOS WKWebView，无 CGO）
│   ├── project/               # web 构建工作台（项目配置 / 插件会话 / HTML 入口 / 写盘）
│   ├── monitor/               # --monitor 性能/内存/运行时指标（独立 module）
│   └── pkgmanager/            # npm 兼容包管理器（semver/registry/resolver/...）
├── pkg/aluka/                 # 嵌入式 Go API（NewRuntime/Eval/RunFile——Go 宿主嵌入 JS 运行时）
│       ├── config/            # .npmrc 解析（registry + 鉴权）
│       └── workspace/         # workspace 发现（glob 展开 + 本地包链接）
├── tests/conformance/         # 一致性测试（node/test262/npm/install/express/build/webbuild/vue-sfc/node22）
├── demo/
│   ├── express-demo/          # 真实 express 运行验证 demo
│   ├── react-ssr-demo/        # React 18 源码级 JSX SSR + Tailwind CSS JIT 现代化 demo
│   ├── vue3-ssr-demo/         # Vue 3 响应式/SFC SSR 现代化 demo
│   ├── vue3-run-build-demo/   # 官方 vue@3.5.13：aluka run SSR + build web/--compile
│   └── web-bundle-vue-demo/   # Vue 3.5.13 web bundle + official compiler-sfc 离线 fixture
├── bench/                     # 性能基准
├── docs/                      # 需求分析 / 开发计划 / AIP 协议规范 / JIT 优化报告
├── .github/workflows/ci.yml   # CI（三端 lint + test + build）
├── Makefile
├── go.work                    # 单仓多 module workspace（与 replace 配套）
└── go.mod                     # 根模块：cmd/aluka + tests/demo/bench 胶水
```

## 开发

```bash
# 运行测试
make test

# 覆盖率报告
make cover

# Lint（需要安装 golangci-lint）
make lint

# 跨平台构建
make release

# 安装到 GOBIN
make install
```

### 一致性测试

```bash
# Node.js 官方测试子集（11/11 通过）
bash tests/conformance/node/run.sh

# test262 子集（8/8 通过）
cd tests/conformance/test262 && ALUKA=../../../bin/aluka go run .

# 真实 npm 包加载测试（semver/ms/debug/is-odd/chalk@4，5/5 通过）
ALUKA=./bin/aluka bash tests/conformance/npm/run.sh

# 包管理器 conformance（离线 monorepo workspace install，全通过）
ALUKA=./bin/aluka bash tests/conformance/install/run.sh

# express-demo 真实环境验证（HTTP 全链路：中间件/路由/body 解析/500 并发，6/6）
ALUKA=./bin/aluka bash tests/conformance/express/run.sh

# build --compile conformance（可执行产物 + shake/minify/analyze，24/24）
ALUKA=./bin/aluka bash tests/conformance/build/run.sh

# 浏览器 bundle conformance（React/TSX/chunk/ESM/CJS/UMD/cache，11/11）
ALUKA=./bin/aluka bash tests/conformance/webbuild/run.sh

# Aluka 与 Node 双跑 @vue/compiler-sfc 探针（1/1）
ALUKA=./bin/aluka bash tests/conformance/vue-sfc/run.sh

# Node 22 差分 conformance（同一用例 aluka vs node22 双跑对比，18 个场景全绿）
ALUKA=./bin/aluka bash tests/conformance/node22/run.sh
```

### 作为 Go 库嵌入

`pkg/aluka` 提供面向 Go 宿主的公共 API（CLI 之外的第二种使用形态）：

```go
import "github.com/aluka-lang/aluka/pkg/aluka"

rt, err := aluka.NewRuntime()
if err != nil { panic(err) }
defer rt.Close()

v, err := rt.Eval("({ answer: 6 * 7 })", "embed.js")
// v 可经 engine.Value 接口读取（AsObject/Float/...）

if err := rt.RunFile("script.ts"); err != nil { // TS 源级转译执行
    panic(err)
}
```

## 设计原则

1. **纯 Go 实现** — 禁用 CGO，所有代码 `//go:build !cgo`
2. **核心自研** — JS 引擎、模块系统、事件循环、TS 转译器全部自研
3. **测试驱动** — 每个 ES 特性配 test262 子集回归
4. **渐进兼容** — 按 P0/P1/P2 优先级分阶段交付
5. **单二进制** — 静态编译，无运行时依赖

## License

MIT
