# aluka Rust 重构计划（功能全景 + 仔细设计）

> 状态：规划稿（v0.1）｜日期：2026-09-03
> 目标读者：重构实施人 / 架构评审
> 依据：本仓库 Go 实现 88K 行引擎 + 74K 行运行时/工具链、全部 ADR 与性能报告

---

## 0. 执行摘要

aluka 是一个纯 Go 实现的、API 行为兼容 Bun/Node.js 的 JS/TS 运行时，核心全部
自研（引擎、模块系统、事件循环、TS 转译、RegExp、GC、包管理器、打包器、GUI、
IPC）。当前 Go 版约 47 万行（`internal` 192K + 测试/工具），25 个一致性套件。

重构为 Rust 的根本动机不是"Go 不行"，而是 **Go 的 GC + interface 内存模型对上
JS 引擎的分配密集负载存在结构性上限**——本仓库用四个实验（ADR：object-arena、
stage2-nanbox-slots、汇编直写、双栈）证明了该结论：Go 无法做 bump 分配的年轻代、
无法做 NaN-box 槽位、无法 unboxed 数值入 interface。Rust 的显式内存模型天然
放开这三条路（arena/盒位/数值直存都可做），目标是 **性能全面对齐 V8 到 2-5x**
而非当前 2-45x。

重构是**逐子系统替换**（strangler），不是重写。运行时先 Rust、工具链沿用 Go
过渡、以 conformance 套件作唯一行为仲裁。计划分 6 阶段约 12-18 个月。

---

## 1. 项目功能全景（Go 版现状，重构的功能基线）

### 1.1 运行时核心（internal/engine，88K 行）

**JS 语言引擎（双引擎）**
- AST 解释器（--ast）与字节码 VM（--vm，默认）：共享 lexer/parser/compiler
- 字面量/算术/比较/位运算/逻辑、严格与宽松相等、ToPrimitive 全表
- 隐藏类（Shape 树）+ 内联缓存（IC）：属性读写、方法调用 CallIC、直接映射
  字面量站点缓存
- 值系统：Object/Array/Function/String/Number（slab 装箱）/BigInt/Symbol/
  Boolean/Null/Undefined/错误对象/Date/RegExp/WeakMap/TypedArray/ArrayBuffer/
  DataView/Proxy
- 属性描述符完整语义：writable/enumerable/configurable、accessor、seal/freeze/
  preventExtensions、Array exotic（holes/length/species）、Proxy invariants
- ES 语法：ES5 全部、ES2015（class/箭头/解构/模板串/模块/生成器/迭代器
  /Symbol/Map/Set/WeakMap/WeakSet/Proxy/Reflect/Promise/async）、ES2017-24
  （async/await、TLA、for await、可选链、空值合并、BigInt、动态 import、
  import attributes、Promise.withResolvers、Array.fromAsync、Object.groupBy、
  装饰器 Stage 3/TS）

**执行架构**
- Tier 0：AST 解释器 + 字节码 VM（编译一次、9 个优化 pass：常量折叠/不可达删除
  /融合/跳转穿透）
- Tier 1：Quick JIT（类型化 IR，可执行 Go）
- Tier 2：Native JIT（amd64 机器码，W^X/崩溃隔离/safepoint/OSR）；property PIC、
  arrayIndex/arrayPush/arrayBatch/closureIncrement/upvalue 等专用快路径
- GC：Go GC 承载物理回收（weak.Pointer 注册表 + 周期清扫 + 显式标记）；
  数字 slab 分配
- 字节码磁盘缓存（.aluka-cache，FormatVersion 校验）
- V8 风格栈迹（错误对象 name/message/stack）

**RegExp**
- RE2 翻译快路径 + 自研回溯 fallback（lookaround/backref/类子集）
- legacy/u 模式 UTF-16 索引语义、lastIndex、replace/split 偏移、回溯预算

**TS/JSX**
- 源码级转译（类型注解剥离在 parser 层）、TSX/JSX 编译期 lowering

### 1.2 模块系统（internal/runtime/module，25K 行）
- ESM 全语法 + CJS（require/module.exports）+ Node 解析算法
- package.json 深度：exports 条件映射、imports、main、module、type、browser/
  node 实例级条件隔离
- 循环依赖、TLA、.ts 相对导入、import attributes、路径别名（paths/baseUrl）
- 字节码磁盘缓存

### 1.3 Node.js 内置库（internal/builtin，31K 行，59 个 registerBuiltin 模块：fs、path…见 §1.3 全表）
fs（sync/promises/cp）、path（posix/win32）、os、url、util（types）、events、
assert（strict）、constants、crypto（含 WebCrypto）、stream（web/promises/
consumers）、string_decoder、http、https、net、tls、dns（promises）、zlib（zstd）、
perf_hooks、timers（promises）、v8（writeHeapSnapshot）、vm、inspector
（CDP 会话）、dgram、http2、cluster、trace_events、readline（promises）、repl、
child_process、worker_threads、module、buffer、tty、sqlite（DatabaseSync）、
domain、punycode、wasi、process、console、test（node:test + reporters）、
async_hooks、diagnostics_channel

### 1.4 Web API & 全局（internal/runtime/globals，13 子包）
- console、navigator、BroadcastChannel、MessageChannel、AbortController
- fetch 全家（Request/Response/Headers/FormData）、WebSocket、URL/URLPattern
- Streams（可读/可写/转换）、CompressionStream（gzip/deflate/deflate-raw）、
  Blob/File、structuredClone
- WebCrypto `crypto.subtle`、TextEncoder/Decoder、atob/btoa
- timers/performance、Intl 全家（DateTimeFormat/NumberFormat/RelativeTimeFormat/
  ListFormat/PluralRules/Collator/Segmenter）
- Aluka（Bun 兼容）：serve/file/write/$/env/sleep/hash/password/deflate/
  inflate/spawn/ipc、SQL（sqlite+postgres）、Redis、S3（自研 SigV4）、shell、
  Bun.peek/deepEquals

### 1.5 工具链（non-runtime，30K+ 行）
- 包管理器（internal/pkgmanager，1.6K 行 src）：registry 客户端、semver、依赖树
  + hoisting、lockfile、workspace、.npmrc
- 打包器（internal/bundler，13K 行）：build --compile（单文件产物+payload 字节码
  +manifest+footer）、build --target=web（graph/shake/minify/emit/chunk/sourcemap/
  ESM-CJS-UMD/watch/dev）、Vue SFC（subset + official compiler-sfc 双后端）
- GUI（internal/gui，6.9K 行）：Windows WebView2、macOS WKWebView（无 CGO）
- IPC（internal/ipc，0.7K 行 src）：AIP 协议 16B 帧头、管道/UDS/TCP、多路复用、
  PubSub
- project/web 工作台（1.2K 行）、monitor 指标（0.3K 行）、pkg/aluka 嵌入 API

### 1.6 CLI（cmd/aluka，5K 行）
run/编译产物检测、repl、test、build/install/add/remove/update、--watch/--dev、
--profile（pprof CPU+heap）、--monitor、--ic-stats/--jit*/--max-memory、
--target=web/--vue-compiler

### 1.7 质量基座
- conformance：node 11、node22 17、build 24、webbuild 13、vue-sfc 1、npm、
  install、express、test262
- jitdiff 生成式差分（三 tier 零失配）+ 5 个 fuzz target
- bench 跨引擎对比（node/aluka 交替，方法学见 tests/benchmark/）
- 39 份 docs/*.md 计划+报告、ADR

---

## 2. 为什么用 Rust 重构（Go 版实测结论 → Rust 解决）

| Go 版约束（已实验证实） | 权威证据 | Rust 版解决 |
|---|---|---|
| 对象无法 arena/bump 年轻代分配（存活 pin 整块，级联保活） | ADR object-arena（RSS 放大 22-71x） | 自研分配器：bump 年轻代 + 标记-清除或 RC，`Box`/`Vec` 或手动 arena，对象地址可搬移（copying GC 可选） |
| 槽位无法 NaN-box（[]uint64 内指针 GC 不可见 → 悬垂） | ADR stage2-nanbox-slots（weak 实证回收） | Value 设为 `u64`（NaN-boxing 或 tagged union），引用分代由引擎自管；无 Go GC 干涉 |
| 数值无法 unboxed 入 interface（convT64 8B/次；unsafe 直写消费端 fault） | ADR 汇编/unsafe 节 + 双栈实验 | `enum Value { Num(f64), ... }` 栈内/槽内直接内联数字；Rust 无"接口 data 字"概念 |
| JIT 只到 amd64 单层、Quick 跨平台但慢 | perf-report-v7 等 | Cranelift 或手写后端可做多目标（x86-64/ARM64）+ 更成熟的 regalloc（参考 V8/SpiderMonkey） |
| 无逃生分析 -> 21x gcPressure | perf-report | Rust 可做标量替换/逃逸分析（对象内联进栈），对齐 V8 |

**注意反向风险**：Rust 无 GC 默认 → 循环引用/自引用（JS 对象图任意引用）需要
设计选择（见 §6.3）；unsafe 面必须严格收口。

---

## 3. 目标架构（Rust crate 布局，strangler 替换）

```
crates/
  aluka-core/          值系统 + GC + 堆分配（Value=u64、对象盒、shape 树）
  aluka-parser/        lexer/parser/AST（可移植自 Go 语义）
  aluka-bytecode/      指令集 + 序列化 + 优化 pass
  aluka-compiler/      AST→字节码 + TS/JSX lowering
  aluka-vm/            Tier0 解释器（字节码 VM）
  aluka-jit/           Quick IR → Cranelift/后端（阶段 4）
  aluka-gc/            GC（阶段 3-4 选型）
  aluka-regex/         RE2 风格 + 回溯引擎
  aluka-module/        ESM/CJS 模块系统 + resolver
  aluka-builtins/      node:* 内置（60+ 模块，可先绑定 Go 版经 FFI 过渡）
  aluka-webapi/        Web API 全家
  aluka-aluka/         Bun 兼容 Aluka API + IPI
  aluka-pkg/           包管理器
  aluka-bundler/       打包器（graph/shake/minify/emit）
  aluka-cli/           CLI（重构早期可继续用 Go CLI 调 Rust 库）
  ffi/                 cgo/cabi 绑定：Go 宿主 pkg/aluka、GUI WebView2 等
```

对应关系：engine→aluka-core/parser/bytecode/compiler/vm/jit/gc/regex；
runtime/module→aluka-module；builtin→aluka-builtins；runtime/globals→
aluka-webapi+aluka-aluka；pkgmanager/bundler→后置阶段沿用/重写。

---

## 4. 阶段路线（12-18 个月，每阶段有验收门）

### 阶段 0：价值锚定（2-3 周）
- 冻结 conformance 套件与 bench 基线
- 选定 Rust GC 策略并做微原型（§6.3）
- 决定"先引擎后工具链"还是"先 CLI/打包器"（建议先引擎）

### 阶段 1：核心值 + 解释器（2-3 个月）
- Value（NaN-box u64）+ 对象盒 + Shape/IC
- 字节码 VM 全指令、模块系统最小集（CJS only）
- 里程碑：`aluka run hello.js`、fib30 达 Go 版 2x、Tier0 对拍 100%
- 验收：node conformance 11/11

### 阶段 2：全语言特性 + 内置库（3-4 个月）
- ES2024 全量、TS/JSX、RegExp、BigInt、Proxy/Reflect、异步（async/await/
  generator/TLA）
- node:* 60+ 模块（先 FFI 到 Go builtin，逐步 Rust 化）
- 验收：node22 17/17 + webapi 套件绿

### 阶段 3：GC 真章（2-3 个月）
- 选择：A) 分代标记-清除（bump 年轻代 + 卡表）B) 引用计数 + 周期回收 C) RC+分代混合
- 数字内联、数组 packed elements（f64 数组直存）
- 里程碑：gcPressure 对齐 node ≤3x（当前 21x）
- 验收：test262 子集 + jitdiff 等价物

### 阶段 4：JIT（3-4 个月）
- Quick IR → Cranelift 后端（或自研 amd64 先）
- PIC/shape guard/去优化恢复（对应现有 jitdiff 全用例）
- 里程碑：propAccess/closureCall ≤ node 5x
- 验收：新 fuzz + 三 tier 零失配

### 阶段 5：工具链迁移（2-3 个月并行）
- pkgmanager/bundler/Vue SFC/CLI 逐个 Rust 化；GUI 经 ffi 桥接

### 阶段 6：打磨（1-2 个月）
- AIP/IPC、监控、堆快照、inspector CDP、测试运行器、bench 终检

---

## 5. 逐子系统迁移核对表（来源：§1 功能全景）

每个子系统三态：`Go 沿用` / `FFI 过渡` / `Rust 重写`，附迁移验收标准。

| 子系统 | 状态 | 验收 |
|---|---|---|
| lexer/parser | Rust 重写 | test262 语法子集 |
| 字节码 + 优化 | Rust 重写 | optimize-equivalence 对拍 |
| VM Tier0 | Rust 重写 | node22 17/17 |
| JIT | Rust 重写 | jitdiff 零失配 |
| RegExp | Rust 重写 | regex corpus + Node oracle |
| ESM/CJS 模块 | Rust 重写 | npm/import conformance |
| node:* 60+ | FFI 过渡→Rust | builtin conformance |
| Web API | Rust 重写 | webbuild/vue-sfc |
| Aluka API | Rust 重写 | express/es 套件 |
| pkgmanager | Go 沿用→重写 | install/npm |
| bundler | Go 沿用→重写 | build/webbuild |
| GUI/IPC | FFI 桥接 | gui smoke |
| CLI | 过渡期 Go 壳 | cli 冒烟 |

---

## 6. 关键设计决策（需阶段 0 前敲定）

### 6.1 Value 表示
- 首选：`u64` NaN-box（lower 51 位 NaN 编码 pointer/f64/小整数/特殊值）
- 备选：`enum Value{t}` 也可，但比对/复制开销更大；跟随 Go ADR 结论定
- 关联：数字直存（f64 内联）、字符串 rope、BigInt 堆盒

### 6.2 对象布局
- Shape 树（迁移 Go 现有设计）+ 内联属性（specified inline slots）
- Packed double elements（`Vec<f64>` 数组）、hole 位图
- 隐藏类不需要 Go 版"小槽位/PIC 上限"等变通（Rust 无 GC 可见性约束）

### 6.3 GC 策略（最高风险决策）
- 建议：**分代 + 卡表标记-清除**（年轻代 bump、老年代 mark-sweep），根集由
  VM/栈显式提供
- 必须解决：跨代引用（写屏障）、栈扫描（调用栈是 Rust 物理栈——用 shadow stack
  或栈顶 root 列表）、`extern` 边界（FFI/内建持有对象）
- 工程兜底：RC + cycle collector 更易正确但性能上限低；最终取舍看阶段 3 原型

### 6.4 unsafe 收口策略
- unsafe 仅限：GC 遍历/分配器/FFI/JIT 机器码生成
- 编译器 `#![forbid(unsafe_code)]` 于业务 crate + 特批白名单
- 每 unsafe 点要求注释（SAFETY）+ 审查清单

### 6.5 兼容性仲裁
- conformance 套件（现 25 个）作为唯一行为标准；diff 方法学沿用
  （交替执行、时钟分辨率、循环消除修正）

---

## 7. 风险登记与应对（Top 10）

1. GC 正确性（对象任意引用图）→ 阶段 0 微原型 + 大量 fuzz/差分
2. 栈根集/写屏障性能 → shadow stack 微基准提前做
3. JIT 后端工期 → 先 Cranelift（—— 风险低）再自研
4. unsafe 内存事故 → 白名单 + Miri 定期跑
5. 内置库 60+ 模块迁移量 → 先 FFI 过渡保证行为一致，再逐模块 Rust 化
6. FFI 边界内存泄漏/双 GC 交互 → 边界对象用专有句柄表（handle table）
7. 并发模型（worker_threads/async）→ 复用 Go 事件循环语义，Rust async 或
   线程池决策需提前
8. 工具链（pkgmanager/bundler）与引擎解耦 → 阶段 5 独立推进，不阻塞引擎
9. 团队 Rust 熟练度 → 先做 parser/bytecode（低风险练手）
10. 规格漂移（ECMA-262 新特性）→ conformance + test262 持续集成

---

## 8. 验收与里程碑汇总

| 里程碑 | 时间 | 验收 |
|---|---|---|
| M0 原型（Value+GC 选型） | +3 周 | 微基准报告 |
| M1 解释器 run hello | +3 月 | node 11/11 |
| M2 全语言 | +7 月 | node22 17/17 |
| M3 GC/数组 | +10 月 | gcPressure ≤3x node |
| M4 JIT | +13 月 | jitdiff 零失配、call ≤5x |
| M5 工具链 | +15 月 | build/webbuild |
| M6 打磨 | +17 月 | 全 conformance + bench 终检 |

---

## 9. 附录

- Go 版功能清单出处：README.md 核心能力一览（§1）、docs 39 份、tests/conformance
- 性能基线：docs/performance-report-v7.md
- 内存/GC 探索结论：docs/adr/object-arena-rejected.md、stage2-nanbox-slots-rejected.md、
  typed-value-stack-plan.md §9/§10
- JIT 现状：docs/jit-performance-optimization-plan.md、jit-coverage-matrix.md