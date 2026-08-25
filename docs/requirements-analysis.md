# Aluka 运行时 — 需求分析文档

> 项目代号：`aluka` ｜ 目标：用纯 Go 实现一个兼容 Bun（JavaScript 运行时）API 行为与开发体验的运行时引擎
> 文档版本：v0.1 ｜ 日期：2026-08-02

---

## 目录

1. [项目概述](#1-项目概述)
2. [约束条件](#2-约束条件)
3. [功能性需求](#3-功能性需求)
4. [非功能性需求](#4-非功能性需求)
5. [技术挑战与应对](#5-技术挑战与应对)
6. [技术选型](#6-技术选型)
7. [系统架构](#7-系统架构)
8. [兼容性矩阵](#8-兼容性矩阵)
9. [风险分析](#9-风险分析)
10. [验收标准](#10-验收标准)
11. [开发路线图](#11-开发路线图)
12. [术语表](#12-术语表)

---

## 1. 项目概述

### 1.1 项目背景

Bun 是 2023 年发布的现代 JavaScript/TypeScript 运行时，基于 JavaScriptCore (JSC) + Zig 实现，定位为 Node.js 的替代方案。它将「运行时 + 包管理器 + 测试器 + 打包器」集成在单一可执行文件中，凭借 JSC 的快速启动和 Zig 的原生 I/O 性能，在启动速度、安装速度、HTTP 吞吐等关键指标上显著优于 Node.js。

由于 Bun 依赖 JSC（Apple WebKit 的 JS 引擎）和 Zig，在 Go 生态中无法直接复用。本项目目标是用 **纯 Go** 重新实现一个语义兼容 Bun 的运行时，使其能够：

- 直接执行 Bun 风格的 JS/TS 代码
- 复用 Bun 的 API（`Bun.serve`、`Bun.file`、`Bun.$` 等）
- 兼容绝大部分 Node.js 内置模块与 npm 包
- 单二进制分发，零运行时依赖

### 1.2 项目目标

| 编号 | 目标 | 衡量标准 |
|------|------|----------|
| G1 | 自研 JS 引擎（纯 Go） | 通过 test262 核心子集 ≥ 80% |
| G2 | TS 一等公民（无 JSX） | 内置 TS 转译，支持常用 TS 特性 |
| G3 | Node.js API 兼容 | Top 500 npm 包 ≥ 80% 可加载运行 |
| G4 | Bun API 兼容 | P0 级 Bun API 100% 行为对齐 |
| G5 | 单二进制跨平台 | linux/darwin/windows 单文件 < 25MB |
| G6 | 启动性能 | `aluka -e "console.log(1)"` < 30ms |

### 1.3 非目标（明确排除）

- **不**追求与 JSC 引擎字节级兼容
- **不**实现 JSX/TSX 转换（本期范围外）
- **不**实现 N-API / Node C++ 原生扩展接口
- **不**追求 100% npm 包兼容（依赖 V8 内部 API 的包不兼容）
- **不**实现 JIT 编译（纯 Go 下不可行，靠字节码 VM 优化）
- **不**实现 Bun 的 macOS Keychain 集成、Windows TTS 等平台特定能力

### 1.4 命名与对外接口

- 二进制名：`aluka`
- 默认全局对象：`globalThis.Bun`（兼容 Bun 用户代码）
- 别名全局对象：`globalThis.Aluka`（可选，作为本项目的标识）
- 模块协议：`import { serve } from "bun"` 解析到内置 `bun` 模块
- 配置文件：`aluka.jsonc`（兼容读取 `bunfig.toml`、`package.json` 的 `"bun"` 字段）

---

## 2. 约束条件

### 2.1 硬约束

| ID | 约束 | 说明 |
|----|------|------|
| C1 | **纯 Go，禁用 CGO** | 所有代码 `//go:build !cgo`，构建命令带 `CGO_ENABLED=0` |
| C2 | **核心组件自研** | JS 引擎 / 模块系统 / 事件循环 / TS 转译器 / Promise 队列 / JS 正则引擎 全部自研 |
| C3 | **JSX/TSX 源码级支持**（2026-08 更新） | Phase 1D 起由自研 parser/compiler 源码级 lowering 支持 JSX/TSX（此前的「暂不支持」约束已失效，见 docs/gap-closure-plan.md H5）；Vue SFC 见 AGENTS 约束 3 |
| C4 | **不引入第三方 JS 引擎** | 不使用 goja / v8go / quickjs-go / modernc.org/quickjs 等 |
| C5 | **跨平台** | linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 |
| C6 | **单二进制** | 静态编译，无外部 .so/.dll/.dylib 依赖 |

### 2.2 软约束（设计原则）

| ID | 原则 | 说明 |
|----|------|------|
| P1 | 自研优先 | 当 Go 标准库能解决时优先用标准库；核心逻辑（与 JS 语义紧耦合）必须自研 |
| P2 | 第三方库边界 | 仅允许引入「纯 Go、无 cgo、协议层」第三方库（如 Postgres 协议、WebSocket 协议），不得引入 JS 引擎或运行时核心 |
| P3 | 测试驱动 | 每个 ES 特性配 test262 子集回归；每个内置模块配 Node.js 官方测试子集 |
| P4 | 渐进兼容 | 不追求一次性 100% 兼容，按 P0/P1/P2 优先级分阶段交付 |
| P5 | 可观测 | 内置 `--inspect`、CPU profile、heap snapshot 协议钩子（即使初期为 stub） |

### 2.3 允许的依赖分类

| 类别 | 是否允许 | 示例 |
|------|---------|------|
| Go 标准库 | ✅ 允许 | `net/http`, `crypto/*`, `encoding/*`, `compress/*`, `io/fs`, `os`, `sync`, `regexp/syntax` |
| 纯 Go 协议库 | ✅ 允许（非核心） | `gorilla/websocket`, `jackc/pgx/v5`, `redis/go-redis/v9`, `mvdan.cc/sh/v3` |
| 纯 Go 数据库驱动 | ✅ 允许 | `modernc.org/sqlite`（纯 Go SQLite） |
| JS 引擎 | ❌ 禁止 | goja, v8go, quickjs-go, modernc.org/quickjs |
| 含 cgo 的库 | ❌ 禁止 | 任何 `import "C"` 的库 |
| golang.org/x/* | ✅ 允许 | `golang.org/x/sys`, `golang.org/x/crypto`, `golang.org/x/net` |

---

## 3. 功能性需求

### 3.1 JavaScript 引擎（自研）

自研一个栈式字节码虚拟机，支持 ES2023 核心特性。

#### 3.1.1 组件分解

```
Source → Lexer → Token → Parser → AST → Compiler → Bytecode → VM → Result
                                       ↑
                            Transpiler (TS strip)
                                       ↑
                            Module Loader
```

| 组件 | 职责 | 实现要点 |
|------|------|----------|
| Lexer | 源码 → Token 流 | 支持 ES2023 全部 token，包括模板字符串、正则字面量、数字分隔符 |
| Parser | Token → AST | 递归下降 + Pratt 表达式解析；覆盖 ECMA-262 语法 |
| Compiler | AST → 字节码 | 单 pass 编译，生成栈式字节码 + 调试元数据（行号表） |
| VM | 执行字节码 | 栈式虚拟机，操作数栈 + 调用栈 + 闭包环境 |
| GC | 自动内存管理 | 三色标记-清除，分代可选；与 Go runtime GC 协作避免阻塞 |
| Regex Engine | JS 正则 | 自研（Go `regexp` 是 RE2，不支持反向引用与回溯） |

#### 3.1.2 ES 特性优先级

| 优先级 | ES 版本 | 关键特性 |
|--------|---------|----------|
| P0 | ES5 | 严格模式、`Object.create`、`Array` 方法、`JSON`、getter/setter |
| P0 | ES2015 | `let`/`const`、箭头函数、`class`、`Promise`、`Symbol`、`Map`/`Set`/`WeakMap`/`WeakSet`、`for...of`、迭代器/生成器、模板字符串、解构、默认参数、rest/spread、模块（`import`/`export`）、`Proxy`/`Reflect`、二进制/八进制字面量 |
| P0 | ES2016 | `**` 指数运算符、`Array.prototype.includes` |
| P0 | ES2017 | `async`/`await`、`Object.entries`/`values`/`fromEntries`、`padStart`/`padEnd` |
| P0 | ES2018 | rest/spread in object、`for await...of`、`Promise.finally`、正则增强（命名捕获、dotAll、后行断言） |
| P0 | ES2019 | `Array.flat`/`flatMap`、`Object.fromEntries`、`trimStart`/`trimEnd`、可选 catch 绑定 |
| P0 | ES2020 | 可选链 `?.`、空值合并 `??`、`BigInt`、`Promise.allSettled`/`any`、`globalThis`、动态 `import()` |
| P1 | ES2021 | `WeakRef`/`FinalizationRegistry`、逻辑赋值 `||=` `&&=` `??=`、`String.replaceAll`、`Promise.any`、数字分隔符 |
| P1 | ES2022 | 顶层 `await`、类字段、私有方法/字段 `#x`、`Object.hasOwn`、`Array.prototype.at`、Error cause |
| P1 | ES2023 | `Array.findLast`/`findLastIndex`、`Hashbang` 语法、`WeakSet.prototype` 收纳 symbol |
| P2 | ES2024+ | `Promise.withResolvers`、`Object.groupBy`、`ArrayBuffer.prototype.resize`、`Temporal`（暂不实现）、`Iterator.prototype` helpers |

#### 3.1.3 字节码设计要点

- 操作数栈深度 + 局部变量槽位在编译期确定
- 指令集分类：常量加载、栈操作、算术、比较、跳转、调用、属性访问、闭包创建、模块边界
- 函数对象 = 字节码 + 闭包环境 + `this` 绑定
- 调试信息：源码行号 → 字节码 PC 映射（用于 stack trace 与断点）

### 3.2 TypeScript 转译器（自研）

**范围**：在 JS Parser 基础上扩展支持 TS 语法，编译期剥离类型信息，输出合法 JS。**不实现 JSX/TSX**。

#### 3.2.1 支持的 TS 特性

| 特性 | 处理方式 |
|------|----------|
| 类型注解 `let x: number = 1` | 剥离 `: number` |
| 接口 `interface Foo {}` | 整段删除 |
| 类型别名 `type Foo = ...` | 整段删除 |
| 函数参数/返回值类型 | 剥离 |
| 泛型 `<T>(x: T): T` | 剥离 `<T>` 与类型 |
| `as` / `satisfies` 断言 | 剥离，保留表达式 |
| `enum` | 转换为对象字面量（数字/字符串枚举 + 反向映射） |
| `namespace` | 转换为立即执行函数 + 闭包赋值 |
| 参数属性 `constructor(public x: number)` | 转换为 `this.x = x` 赋值 |
| 装饰器（类/方法/字段/参数） | 按 Stage 3 提案转换 |
| `declare` 声明 | 删除 |
| `import type` / `export type` | 整行删除 |
| `const enum` | 内联常量值 |
| `typeof` 类型 | 在类型位置剥离，在表达式位置保留 |
| 条件类型 / 映射类型 | 仅在类型位置出现，剥离 |
| `keyof` / `infer` / `extends` 约束 | 剥离 |
| `abstract` / `implements` | 剥离修饰符与 implements 子句 |

#### 3.2.2 不支持的 TS 特性

- ❌ JSX / TSX（本期范围外）
- ❌ `namespace` 跨文件合并（仅单文件 namespace）
- ❌ 复杂类型运算的运行时影响（类型本就不应有运行时影响）
- ❌ 装饰器旧提案（Stage 2）

#### 3.2.3 tsconfig.json 读取

仅读取以下字段：

| 字段 | 用途 |
|------|------|
| `compilerOptions.target` | 决定是否降级（本期固定 ES2020+，不降级） |
| `compilerOptions.module` | 模块解析策略 (`node16`/`nodenext`/`bundler`) |
| `compilerOptions.paths` | 路径别名 |
| `compilerOptions.baseUrl` | 路径解析基目录 |
| `compilerOptions.moduleResolution` | 解析模式 |
| `compilerOptions.esModuleInterop` | CJS/ESM 互操作默认导出处理 |
| `compilerOptions.allowSyntheticDefaultImports` | 同上 |
| `compilerOptions.experimentalDecorators` | 装饰器语义开关 |

### 3.3 模块系统（自研）

#### 3.3.1 加载模式

| 模式 | 文件 | 检测 | 加载器 |
|------|------|------|--------|
| ESM | `.mjs`、`.ts`、`.mts`、`package.json.type=module` | `import`/`export` 关键字 | ESM Loader |
| CJS | `.cjs`、`.cts`、`package.json.type=commonjs` | `require()`/`module.exports` | CJS Loader |
| 智能 | `.js`（无 type 字段） | 解析文件检测 `import/export` | 自动选择 |

#### 3.3.2 解析算法（Node.js 兼容）

实现完整的 Node.js 模块解析算法（[ESM](https://nodejs.org/api/esm.html#resolution-algorithm) + [CJS](https://nodejs.org/api/modules.html#all-together)）：

1. **URL 规范化**：file/data/node 协议
2. **相对/绝对路径**：`./`、`../`、`/`
3. **扩展名补全**：按顺序尝试 `.ts` → `.mts` → `.cts` → `.tsx`(忽略) → `.js` → `.mjs` → `.cjs` → `.json` → `.node`
4. **目录解析**：`./dir` → `./dir/index.{ext}`
5. **package.json 字段**：
   - `main`（CJS 主入口）
   - `module`（ESM 主入口，bundler 模式）
   - `exports`（条件导出：`import`/`require`/`node`/`default`/`types`/`bun`）
   - `imports`（`#` 前缀内部别名）
   - `type`（`module`/`commonjs`）
6. **node_modules 向上查找**：当前目录 → 父目录 `node_modules` → ... 直到根
7. **bare specifier 子路径**：`lodash/fp`、`@scope/pkg/sub`
8. **路径别名**：`tsconfig.json` 的 `paths`
9. **导出缓存**：每个模块实例化一次，循环依赖按 ESM 规范处理（返回未完成的 exports）

#### 3.3.3 字节码缓存

- 缓存键：文件路径 + mtime + size + 编译器版本 hash
- 缓存值：字节码 + 调试元数据
- 存储位置：`node_modules/.aluka/cache/` 或 `~/.aluka/cache/`
- 失效策略：源文件变化即失效；`--no-cache` 强制重编译

### 3.4 全局对象与 Timer

| 全局对象 | 主要 API | 来源 |
|----------|----------|------|
| `console` | `log`/`error`/`warn`/`info`/`debug`/`table`/`group`/`groupEnd`/`time`/`timeEnd`/`dir`/`trace`/`assert` | 自研 |
| `process` | `argv`/`env`/`cwd`/`chdir`/`exit`/`nextTick`/`stdin`/`stdout`/`stderr`/`platform`/`arch`/`versions`/`pid`/`kill`/`on`/`once`/`emit` | 自研（基于 Go os） |
| `Buffer` | Node.js Buffer 完整 API | 自研（基于 `[]byte` + JS 视图） |
| `TextEncoder`/`TextDecoder` | UTF-8/UTF-16 编解码 | 自研 |
| `atob`/`btoa` | Base64 | 自研 |
| `URL`/`URLSearchParams` | WHATWG URL | 自研 |
| `AbortController`/`AbortSignal` | 中断信号 | 自研 |
| `Event`/`EventTarget`/`CustomEvent` | 事件 | 自研 |
| `setTimeout`/`setInterval`/`setImmediate`/`clearTimeout`/`clearInterval`/`clearImmediate` | 定时器 | 自研 |
| `queueMicrotask` | 微任务入队 | 自研 |
| `structuredClone` | 深克隆 | 自研 |
| `Promise`/`Symbol`/`Proxy`/`Reflect`/`Map`/`Set`/`WeakMap`/`WeakSet`/`WeakRef`/`FinalizationRegistry` | 内置对象 | 引擎自研 |
| `BigInt`/`BigInt64Array`/`BigUint64Array` | 大整数 | 引擎自研 |
| `Intl` | 国际化（仅 `Collator`/`DateTimeFormat`/`NumberFormat` 基础） | P1 |

### 3.5 Node.js 兼容内置模块

按优先级分组（自研封装，基于 Go 标准库）。

#### 3.5.1 P0 — 核心 I/O

| 模块 | 主要能力 |
|------|----------|
| `node:fs` | sync/async/promises 三套 API；`readFile`/`writeFile`/`readdir`/`stat`/`mkdir`/`rm`/`rename`/`copyFile`/`watch`/`createReadStream`/`createWriteStream` |
| `node:path` | `posix`/`win32` 双实现；`join`/`resolve`/`normalize`/`relative`/`dirname`/`basename`/`extname`/`parse`/`format` |
| `node:os` | `platform`/`arch`/`cpus`/`totalmem`/`freemem`/`homedir`/`tmpdir`/`networkInterfaces` |
| `node:url` | `parse`/`format`/`resolve`/`fileURLToPath`/`pathToFileURL` |
| `node:querystring` | `parse`/`stringify`/`escape`/`unescape` |
| `node:events` | `EventEmitter`/`once`/`on`/`EventEmitterAsyncResource` |
| `node:util` | `inspect`/`promisify`/`callbackify`/`format`/`deprecate`/`types`/`isDeepStrictEqual` |
| `node:assert` | `ok`/`equal`/`deepEqual`/`throws`/`rejects`/`strict` 模式 |
| `node:stream` | `Readable`/`Writable`/`Duplex`/`Transform`/`pipeline`/`finished`/`Readable.from`/异步迭代 |
| `node:buffer` | `Buffer`（与全局同源）/`File`/`Blob`/`constants` |
| `node:process` | 与全局 `process` 同源 |
| `node:crypto` | `createHash`/`createHmac`/`randomBytes`/`randomInt`/`scrypt`/`pbkdf2`/`createCipheriv`/`createDecipheriv`/`webcrypto` |
| `node:string_decoder` | `StringDecoder` |

#### 3.5.2 P0 — 网络

| 模块 | 主要能力 |
|------|----------|
| `node:http` | `Server`/`IncomingMessage`/`ServerResponse`/`request`/`get`/`Agent`/`globalAgent` |
| `node:https` | 同上 + TLS |
| `node:net` | TCP `Server`/`Socket`/`createConnection` |
| `node:tls` | TLS 包装 |
| `node:dns` | `lookup`/`resolve`/`reverse`/`promises` |
| `node:zlib` | `gzip`/`gunzip`/`deflate`/`inflate`/`brotliCompress`/`brotliDecompress` |

#### 3.5.3 P1 — 进程与并发

| 模块 | 主要能力 |
|------|----------|
| `node:child_process` | `spawn`/`exec`/`execFile`/`fork`/`execSync` |
| `node:worker_threads` | `Worker`/`MessagePort`/`SharedArrayBuffer` |
| `node:perf_hooks` | `performance.now`/`mark`/`measure`/`observe` |
| `node:timers` | 与全局定时器同源 + `promises` 子模块 |
| `node:readline` | `createInterface`/`question`/`clearLine` |
| `node:repl` | `start`/`REPLServer` |
| `node:module` | `createRequire`/`register`/`Module` |
| `node:v8` | `serialize`/`deserialize`/`getHeapStatistics`（subset） |

#### 3.5.4 P2 — 较少使用

`async_hooks`、`cluster`、`dgram`、`trace_events`、`inspector`、`node:test`（仅作为兼容入口）。

### 3.6 Web API

| API | 主要能力 | 实现基础 |
|-----|----------|----------|
| `fetch`/`Request`/`Response`/`Headers`/`FormData` | WHATWG Fetch | Go `net/http` |
| `WebSocket` / `WebSocketServer` | 浏览器 + ws 包 API | `gorilla/websocket`（纯 Go） |
| `ReadableStream`/`WritableStream`/`TransformStream` | WHATWG Streams | 自研（基于 `node:stream`） |
| `Blob`/`File` | 二进制容器 | 自研 |
| `AbortController` | 中断信号 | 与全局同源 |
| `crypto.subtle` | Web Crypto（encrypt/decrypt/digest/sign/verify） | Go `crypto/*` |
| `URLPattern` | URL 模式匹配 | 自研 |
| `caches` | Cache Storage（subset） | P2 |
| `MessageChannel`/`MessagePort` | 跨上下文消息 | 自研 |

### 3.7 Bun 特有 API

#### 3.7.1 P0 — 核心高频

| API | 行为描述 |
|-----|----------|
| `Bun.serve(options)` | HTTP + WebSocket 一体化服务器；返回 `{port, hostname, stop, ref, unref}`；`fetch(req, server)` 处理请求；`websocket` 处理 WS 消息 |
| `Bun.file(path)` | 同步创建 `BunFile` 引用（懒加载） |
| `Bun.write(dest, input)` | 写入文件/标准输出/Blob，返回写入字节数 |
| `Bun.env` | 进程环境变量（与 `process.env` 同源，但支持嵌套访问） |
| `Bun.sleep(ms)` | Promise 化的睡眠（不阻塞事件循环） |
| `Bun.sleepSync(ms)` | 同步睡眠（阻塞） |
| `Bun.nanoseconds()` | 高精度时间戳（纳秒） |
| `Bun.gc(mode?)` | 主动触发 GC（major/minor） |
| `Bun.main` | 入口文件绝对路径 |
| `Bun.cwd` | 当前工作目录 |
| `Bun.origin` | 当前进程 origin |
| `Bun.version` | aluka 版本号（形如 `1.0.0-aluka`） |
| `Bun.platform` | `{platform, arch}` |
| `Bun.stderr`/`Bun.stdin`/`Bun.stdout` | BunFile 包装的 stdio |

#### 3.7.2 P1 — 常用工具

| API | 行为描述 |
|-----|----------|
| `Bun.$` | 跨平台 shell 模板字符串 `$\`ls -la\``，支持管道、变量插值 |
| `Bun.password` | `hash`（bcrypt/argon2）/`verify`/`needsRehash` |
| `Bun.hash` | `wyhash`/`crc32`/`adler32`/`sha*` 快速哈希 |
| `Bun.deflate`/`Bun.gzip`/`Bun.inflate`/`Bun.gunzip` | 同步压缩解压 |
| `Bun.peek` | 查看流/数组/iterator 第一个元素而不消费 |
| `Bun.deepEquals` | 深比较 |
| `Bun.deepAssign` | 深合并 |
| `Bun.tsv`/`Bun.csv`/`Bun.YAML`/`Bun.toml` | 解析/序列化 |
| `Bun.escapeHTML`/`Bun.fileType`/`Bun.isTerminal` | 工具函数 |
| `Bun.color` | 颜色转换 |
| `Bun.dns` | DNS 查询 |
| `Bun.unsafe` | `gc`/`noUnsafeEvalWarning`/`cast`/`asString` |
| `Bun.which` | 查找可执行文件路径 |
| `Bun.spawn`/`Bun.spawnSync` | 子进程（与 `node:child_process` 类似但 API 不同） |
| `Bun.readableStreamToArray`/`toArrayBuffer`/`toText`/`toBlob` | 流消费工具 |
| `Bun.concatArrayBuffers` | 拼接 ArrayBuffer |

#### 3.7.3 P2 — 较少使用或实现复杂

| API | 实现可行性 |
|-----|------------|
| `Bun.ffi` | **困难**：纯 Go 无 cgo 难以加载动态库；可考虑自研 ELF/Mach-O 解析 + syscall 调用约定（不保证 Windows），或仅 stub |
| `Bun.SQL` | 用纯 Go `jackc/pgx` 实现 Postgres；`modernc.org/sqlite` 实现 SQLite；MySQL 用 `go-sql-driver/mysql` |
| `Bun.Redis` | 用纯 Go `redis/go-redis/v9` |
| `Bun.S3` | 用纯 Go `aws-sdk-go-v2` |
| `Bun.build` | 自研 bundler（Phase 7） |
| `Bun.JS`/`Bun.Transpiler` | JS Transpiler API（暴露给用户代码） |
| `Bun.semver` | semver 解析 |
| `Bun.md`/`Bun.md5` | Markdown 解析（可用 `gomarkdown/markdown`） |
| `Bun.HCI`/`Bun.keychain`/`Bun.tts` | 平台特性，本期不实现 |

### 3.8 工具链（CLI）

```bash
aluka run <file>          # 执行 JS/TS 文件
aluka <file>              # 简写
aluka -e "<code>"         # 内联执行
aluka --eval "<code>"     # 同上
aluka --print "<code>"    # 执行并打印结果
aluka repl                # 交互式 REPL
aluka init                # 创建 package.json
aluka install             # 安装依赖（Phase 5）
aluka add <pkg>           # 添加包（Phase 5）
aluka remove <pkg>        # 移除包（Phase 5）
aluka update [pkg]        # 更新依赖（Phase 5）
aluka test [pattern]      # 运行测试（Phase 6）
aluka build <entry>       # 打包（Phase 7）
aluka build --compile     # 单文件可执行（Phase 7）
aluka upgrade             # 自更新
aluka completions         # 生成 shell 补全
aluka --version
aluka --help
```

支持的子命令在 Phase 0 仅含 `run` / `eval` / `repl` / `--version` / `--help`，其余按 Phase 渐进交付。

---

## 4. 非功能性需求

| 维度 | 指标 | 测量方法 |
|------|------|----------|
| 启动延迟 | `aluka -e "console.log(1)"` 冷启动 < 30ms | `hyperfine` 测 50 次中位数 |
| 启动延迟（含 TS） | `aluka run hello.ts` < 50ms | 同上 |
| HTTP 吞吐 | "Hello World" RPS ≥ 60k（单核） | `wrk -t 4 -c 100` |
| 内存占用 | hello world 常驻 RSS < 30MB | `ps -o rss` |
| 二进制体积 | 单文件 < 25MB（压缩后 < 10MB） | `go build` + `upx` 可选 |
| test262 通过率 | 核心 ES 特性 ≥ 80% | `test262.fyi` 子集 |
| npm 兼容性 | Top 500 npm 包 ≥ 80% 可 `require`/`import` 加载 | 自动化测试套件 |
| TS 兼容性 | TS 官方 `conformance` 测试 ≥ 70% | 自跑测试 |
| 单测覆盖率 | ≥ 70% | `go test -cover` |
| 跨平台一致性 | 三端行为一致（路径/换行差异除外） | CI 矩阵 |

---

## 5. 技术挑战与应对

| ID | 挑战 | 难度 | 应对 |
|----|------|------|------|
| CH1 | **纯 Go 自研 JS 引擎** — ES2023 规模巨大，test262 ~40k 测试 | 极高 | 分阶段：先 ES5+ES2015，再渐进；先 AST-walking 跑通，再升级字节码 VM |
| CH2 | **无 cgo 下性能瓶颈** — 无法 JIT；Go GC 与 JS GC 协作复杂 | 高 | 字节码 VM + 内联缓存（inline cache）+ 隐藏类（hidden class）；热点循环检测 |
| CH3 | **JS 正则引擎** — Go `regexp` 是 RE2，不支持反向引用、回溯、命名捕获 | 高 | 自研基于 NFA + 回溯的正则引擎（参考 V8 `irregexp`） |
| CH4 | **异步事件循环** — JS 单线程 + microtask/macrotask 与 Go goroutine 桥接 | 高 | 每个 Runtime 绑定一个 goroutine；I/O 在 Go 异步完成后通过 channel 投递回 JS goroutine |
| CH5 | **GC 协作** — JS 对象生命周期与 Go GC 不同 | 中 | 自管理 JS 堆（不依赖 Go GC），周期性 mark-sweep；大对象用 `runtime.Pinner` 防止过早回收 |
| CH6 | **Buffer 零拷贝** — JS ArrayBuffer 与 Go `[]byte` 共享内存 | 中 | 用 `unsafe.Slice` 共享底层 `[]byte`；JS 视图对象持 `*[]byte` 引用 |
| CH7 | **TS 转译完整性** — 类型系统复杂，decorator/enum/namespace 边界多 | 中 | 严格按 TS 报告测试用例；先做 strip-types，再补充 enum/namespace/decorator |
| CH8 | **Node.js 解析算法** — `exports` 字段条件解析复杂 | 中 | 严格按 Node.js 规范实现；用 Node 官方测试用例回归 |
| CH9 | **npm 兼容性** — 海量历史 API、生命周期脚本、postinstall | 中 | 不实现原生 addon；只支持纯 JS 包；postinstall 用 aluka 自己执行 |
| CH10 | **Windows 路径差异** — UNC 路径、驱动器号、大小写 | 低 | 抽象 `path` 模块；按 `os.PathSeparator` 分发 |
| CH11 | **模块循环依赖** — ESM 与 CJS 处理语义不同 | 中 | 严格按规范实现 cycle detection；用 `node_modules` 测试集回归 |
| CH12 | **WSL/Unix signal 处理** — `SIGINT`/`SIGTERM` 行为差异 | 低 | 用 `os/signal`；测试各平台行为 |

---

## 6. 技术选型

### 6.1 JS 引擎：栈式字节码 VM

| 选项 | 评价 | 选择 |
|------|------|------|
| AST-walking 解释器 | 实现最简单，性能差 5-10x | ❌ 仅作为 Phase 0 临时方案 |
| **栈式字节码 VM** | 性能/复杂度平衡好；可后续升级 | ✅ |
| 寄存器式字节码 VM | 性能略好，但编译器复杂 | ❌ |
| JIT（LLVM 风格） | 纯 Go 下不可行（无法 mmap 可执行内存） | ❌ |
| AOT 编译为 Go 源 | 体积爆炸，无法动态 eval | ❌ |

**字节码 VM 设计要点**：
- 单操作数栈，深度编译期确定
- 函数帧：返回 PC、调用者栈基、参数槽、局部变量槽
- 闭包：捕获变量通过 `upvalue` 引用（类似 Lua）
- 内联缓存：属性访问缓存 (class_id, slot_index) 命中
- 隐藏类：相同形状对象共享 layout 描述符

### 6.2 异步模型

```
┌─────────────────────────────────────────────────────────────┐
│                  JS Runtime Goroutine (1:1)                 │
│  ┌─────────────────────┐    ┌────────────────────────────┐  │
│  │   VM (single-thread)│    │  Microtask Queue           │  │
│  │   ←──── ticks ───────┼────┤  (Promise.then, queueMicro) │  │
│  └─────────────────────┘    └────────────────────────────┘  │
│           ↑                                       ↑          │
│           │ sync call                             │ enqueue   │
│  ┌────────┴───────────────────────────────────────┴───────┐  │
│  │              Macrotask Queue (timers, I/O completion)   │  │
│  └───────────────────────────────┬───────────────────────┘  │
└──────────────────────────────────┼───────────────────────────┘
                                   │ channel (PostTask)
              ┌────────────────────┴────────────────────┐
              │         I/O Workers (Go goroutines)     │
              │  net/http | os | crypto | child_process │
              └─────────────────────────────────────────┘
```

- **JS Runtime** 绑定单个 goroutine，所有 JS 字节码在该 goroutine 执行（保证单线程语义）
- **microtask** 在每次 JS 同步代码块退出前清空（与 V8 行为一致）
- **macrotask** 通过 `runtime.PostTask(fn)` 投递到 JS goroutine 的 channel
- **I/O** 在独立 goroutine 执行（用 Go `net/http` 等），完成后通过 `PostTask` 回调 JS

### 6.3 内存管理

- **JS 堆自管理**：分配 `[]byte` 大块（arena），内部 bump-allocator + free-list
- **GC**：三色标记-清除，增量式；与 Go runtime 协作（避免 STW 时长过长）
- **大对象**：直接 `make([]byte, n)`，由 Go GC 管理
- **字符串**：内部用 Go `string`（不可变），减少拷贝

### 6.4 TS 转译器架构

- 复用 JS Parser，扩展 TS 语法产生式
- AST 节点携带 `TypeAnnotation` 字段（编译期消费，不进入字节码）
- 单文件输出转换后的 JS 源（或直接 AST→字节码，跳过 JS 文本）

### 6.5 错误处理

- JS `Error` 对象携带完整 stack trace（行号、列号、源文件）
- 未捕获异常打印到 stderr，进程退出码 1
- `unhandledRejection` 事件触发警告
- `--stack-trace-limit` 控制深度

---

## 7. 系统架构

### 7.1 模块分层

```
┌──────────────────────────────────────────────────────────────┐
│                        CLI Layer                             │
│  cmd/aluka: run | eval | repl | install | test | build       │
└──────────────────────────────────────────────────────────────┘
                              │
┌──────────────────────────────────────────────────────────────┐
│                     Runtime Layer                            │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │            Engine (JS VM Core)                         │ │
│  │  Lexer │ Parser │ Compiler │ VM │ GC │ Regex │ Builtins│ │
│  └─────────────────────────────────────────────────────────┘ │
│  ┌────────────────┐  ┌──────────────┐  ┌─────────────────┐  │
│  │ Event Loop     │  │ Module System│  │  Transpiler     │  │
│  │ (micro/macro)  │  │ (CJS/ESM)    │  │  (TS strip)     │  │
│  └────────────────┘  └──────────────┘  └─────────────────┘  │
│  ┌─────────────────────────────────────────────────────────┐│
│  │              Built-in Modules                          ││
│  │  Node.js Compat  │  Web APIs  │  Bun.* APIs             ││
│  └─────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
                              │
┌──────────────────────────────────────────────────────────────┐
│                    Foundation Layer                          │
│  Go stdlib: net/http, crypto/*, os, io/fs, compress/*, ...    │
│  Pure-Go libs: gorilla/websocket, jackc/pgx, mvdan.cc/sh ...  │
└──────────────────────────────────────────────────────────────┘
```

### 7.2 包结构

```
aluka_lang/
├── cmd/
│   └── aluka/                  # main 入口
├── internal/
│   ├── engine/                 # JS 引擎（自研）
│   │   ├── lexer/
│   │   ├── parser/
│   │   ├── ast/
│   │   ├── compiler/
│   │   ├── bytecode/
│   │   ├── vm/
│   │   ├── gc/
│   │   ├── regex/
│   │   └── builtins/            # JS 内置对象 (Promise/Map/Symbol/...)
│   ├── runtime/                 # 运行时核心
│   │   ├── eventloop/
│   │   ├── module/              # 解析/加载/缓存
│   │   ├── transpiler/          # TS → JS
│   │   ├── globals/             # console/process/Buffer/...
│   │   └── bridge/              # Go<->JS 函数/Promise 桥接
│   ├── builtin/                 # 内置模块实现
│   │   ├── node/                # fs/path/http/crypto/...
│   │   ├── web/                 # fetch/websocket/stream/...
│   │   └── bun/                 # Bun.serve/Bun.file/...
│   ├── pkgmanager/              # aluka install (Phase 5)
│   ├── testrunner/              # aluka test (Phase 6)
│   ├── bundler/                  # aluka build (Phase 7)
│   └── lockfile/                 # aluka.lock
├── pkg/                         # 对外暴露的 Go API（供库使用）
│   └── aluka/                   # 嵌入式 API: aluka.NewRuntime()
├── tests/                       # 集成测试
│   ├── test262/                 # test262 子集
│   ├── conformance/             # Node.js / TS 兼容性
│   └── fixtures/
├── docs/
├── scripts/
├── go.mod
└── go.sum
```

### 7.3 数据流示例：`aluka run server.ts`

```
1. CLI 解析: cmd/aluka → RunCommand("server.ts")
2. 文件加载: os.ReadFile("server.ts")
3. TS 转译: transpiler.Strip("server.ts") → JS AST
4. 编译:    compiler.Compile(AST) → bytecode.Module
5. 模块系统: module.ResolveImports(bytecode) → 递归编译依赖
6. 事件循环: eventloop.Run()
   - 执行入口 module
   - 注册 setTimeout / Bun.serve 等回调
   - I/O 在 Go goroutine 完成，PostTask 回灌
   - microtask 在每个 sync 块后清空
   - 直到没有 pending macrotask → 退出
7. 退出: 进程退出码 = main 模块同步返回值或 process.exit(code)
```

---

## 8. 兼容性矩阵

### 8.1 ES 特性（按 Phase 交付）

| Phase | ES 版本范围 | test262 通过率目标 |
|-------|-------------|---------------------|
| Phase 1 | ES5 + ES2015 | ≥ 60% |
| Phase 2 | ES2016 - ES2020 | ≥ 75% |
| Phase 3 | ES2021 - ES2023 | ≥ 80% |
| Phase 4+ | ES2024+ 渐进 | ≥ 85% |

### 8.2 Node.js 内置模块覆盖

| Phase | 模块数 | 覆盖率 |
|-------|--------|--------|
| Phase 2 | 14 (P0 核心+网络) | ~50% API |
| Phase 3 | +6 (P1 进程) | ~70% API |
| Phase 4+ | +剩余 P2 | ~85% API |

### 8.3 Bun API 覆盖

| Phase | API 数 | 覆盖率 |
|-------|--------|--------|
| Phase 4 | 14 (P0) | 100% P0 |
| Phase 5 | +20 (P1) | 100% P0+P1 |
| Phase 7+ | +10 (P2) | 90% P0+P1+P2（FFI 等不可行项除外） |

### 8.4 npm 包兼容性目标

| Phase | 目标 | 测试方法 |
|-------|------|----------|
| Phase 3 | Top 100 npm 包 ≥ 70% | 自动化加载测试 |
| Phase 4 | Top 500 npm 包 ≥ 80% | 同上 |
| Phase 5+ | Top 1000 npm 包 ≥ 90% | 同上 |

不兼容范围（明确）：
- 依赖 N-API / Node C++ addon 的包（如 `sharp`、`bcrypt`、`node-sass`）
- 依赖 V8 内部 API 的包（如 `v8-profiler-next`）
- 依赖 `node:vm` 完整 V8 语义的包

---

## 9. 风险分析

| ID | 风险 | 概率 | 影响 | 应对 |
|----|------|------|------|------|
| R1 | **JS 引擎实现工作量超估** — 单人难以在合理时间内完成 ES2023 全集 | 高 | 极高 | 严格分 Phase 交付；Phase 1 仅做 ES5+ES2015；可引入社区贡献 |
| R2 | **性能不达标** — 纯 Go 字节码 VM 可能比 V8 慢 50-100x | 高 | 高 | 优化优先级：热路径内联缓存 > 减少 GC 停顿 > 字节码优化；接受性能折中 |
| R3 | **Go GC 与自管理堆冲突** — JS 对象被 Go GC 过早回收 | 中 | 高 | 用 `runtime.Pinner` / `runtime.KeepAlive`；JS 堆完全自管理 arena |
| R4 | **TS 转译 corner case 多** — decorator、enum、namespace 边界 | 中 | 中 | 跑 TS 官方 conformance 测试；优先级递降 |
| R5 | **npm 兼容性长尾** — 历史 API 行为差异多 | 中 | 中 | 不追求 100%；标注已知不兼容清单 |
| R6 | **正则引擎性能/正确性** — 自研可能引入 bug | 中 | 中 | 实现 PCRE 子集 + test262 regex 测试 |
| R7 | **Windows 平台行为差异** — 路径、signal、文件锁 | 低 | 低 | CI 矩阵覆盖；抽象平台层 |
| R8 | **维护者疲劳** — 单人项目长期投入 | 高 | 极高 | 严格 scope 控制；接受 P2 项长期不实现；社区化运营 |
| R9 | **Go 标准库版本变化** — Go 1.25+ API 可能变更 | 低 | 低 | 锁定最低 Go 版本；CI 多版本测试 |
| R10 | **法律/许可证** — 复制 Bun API 是否侵权 | 低 | 中 | API 行为不版权保护；实现代码原创；引用规范条款 |

---

## 10. 验收标准

每个 Phase 的可量化验收点。

### Phase 0 — 工程基座

```bash
aluka -e "console.log(1+1)"        # 输出 2，冷启动 < 30ms
aluka run hello.js                  # 执行 hello.js
aluka --version                     # 输出 1.0.0-aluka
aluka --help                        # 显示帮助
```

### Phase 1 — JS 引擎 + 模块系统 + TS

```bash
aluka run hello.ts                  # TS 类型剥离后执行
aluka run mod_test.js               # ESM import/export 工作
aluka run cjs_test.cjs              # CJS require/exports 工作
# test262 通过率：ES5+ES2015 ≥ 60%
```

### Phase 2 — Node.js 核心模块

```bash
aluka run fs_demo.js                # fs 读写文件
aluka run http_server.js            # http 起服务，wrk 测 RPS ≥ 60k
aluka run stream_demo.js            # stream pipeline 工作
```

### Phase 3 — Web API + P1 Node 模块

```bash
aluka run fetch_demo.js             # fetch 调用外网 API
aluka run ws_demo.js                # WebSocket 服务端
# Top 100 npm 包 ≥ 70% 可加载
```

### Phase 4 — Bun 特有 API

```bash
aluka run bun_serve.ts              # Bun.serve + WebSocket 一体
aluka run bun_file.ts               # Bun.file/write 工作
aluka run bun_shell.ts              # Bun.$`ls -la` 输出目录
```

### Phase 5 — 包管理器

```bash
aluka install                       # 解析 package.json，写入 node_modules
aluka add express                   # 安装 express
aluka run app.js                     # 运行依赖 express 的代码
```

### Phase 6 — 测试器

```bash
aluka test                          # 跑通 Jest 风格测试
aluka test --coverage               # 输出覆盖率
```

### Phase 7 — 打包器

```bash
aluka build ./src/index.ts --outdir ./dist
aluka build --compile --outfile app ./src/index.ts   # 生成单文件可执行
./app                                # 直接运行
```

---

## 11. 开发路线图

> 阶段编号与验收标准对应。每个 Phase 完成后产出可发布二进制。

| Phase | 名称 | 主要交付 | 验收 | 依赖 |
|-------|------|----------|------|------|
| 0 | 工程基座 | 项目骨架、CLI、引擎抽象层、console/process 基础 | `console.log(1+1)` 跑通 | - |
| 1 | JS 引擎 + 模块 + TS | Lexer/Parser/VM、ES5+ES2015、ESM/CJS、TS strip-types | test262 ES5+ES2015 ≥ 60% | 0 |
| 2 | Node 核心模块 | fs/path/events/stream/buffer/crypto/http/https/net/tls/dns/zlib | http_server demo | 1 |
| 3 | Web API + P1 Node | fetch/ws/Headers/Stream + child_process/worker_threads/timers.promises | Top 100 npm ≥ 70% | 2 |
| 4 | Bun 特有 API | Bun.serve/file/$/password/hash/SQL/Redis/S3 | bun_serve demo | 3 |
| 5 | 包管理器 | aluka install/add/remove/update、npm registry、lockfile | `aluka install express` | 4 |
| 6 | 测试器 | bun:test、Jest expect、mock、snapshot、coverage | 跑通 Jest 风格测试 | 4 |
| 7 | 打包器 | tree-shake、minify、--compile 单文件 | 单文件产物可执行 | 4 |
| 8 | 优化与生态 | 性能调优、test262 ≥ 85%、文档站、VSCode 插件 | benchmarks 接近 Bun 60% | 5,6,7 |

---

## 12. 术语表

| 术语 | 含义 |
|------|------|
| **Aluka** | 本项目名（Bun 兼容运行时） |
| **Bun** | oven-sh 开发的 JS/TS 运行时（本项目兼容目标） |
| **JSC** | JavaScriptCore，Apple/WebKit 的 JS 引擎 |
| **V8** | Google 的 JS 引擎，Node.js/Deno 使用 |
| **QuickJS** | Fabrice Bellard 的小型 JS 引擎，ES2023 |
| **test262** | ECMAScript 官方一致性测试套件 |
| **Esm/CJS** | ECMAScript Modules / CommonJS |
| **AST** | 抽象语法树 |
| **VM** | 虚拟机（本文特指字节码虚拟机） |
| **JIT** | 即时编译 |
| **AOT** | 提前编译 |
| **RE2** | Google 的正则引擎，不支持反向引用 |
| **N-API** | Node.js 的原生扩展接口（ABI 稳定层） |
| **macrotask/microtask** | JS 异步任务的两类队列 |
| **Hidden Class** | V8 优化对象属性访问的机制 |
| **Inline Cache** | 内联缓存，加速属性访问/方法调用 |

---

## 附录 A：参考资料

- [Bun 官方文档](https://bun.com/docs)
- [ECMAScript 2023 规范](https://tc39.es/ecma262/2023/)
- [test262 测试套件](https://github.com/tc39/test262)
- [Node.js 模块解析算法](https://nodejs.org/api/esm.html#resolution-algorithm)
- [TypeScript 语言规范](https://github.com/microsoft/TypeScript/blob/main/doc/spec-ARCHIVED.md)
- [WHATWG Fetch 标准](https://fetch.spec.whatwg.org/)
- [WHATWG Streams 标准](https://streams.spec.whatwg.org/)

## 附录 B：变更日志

| 版本 | 日期 | 变更 |
|------|------|------|
| v0.1 | 2026-08-02 | 初稿，确定纯 Go + 自研 + 无 JSX 的约束方向 |
