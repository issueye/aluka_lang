# Aluka 运行时 — 开发计划文档

> 项目代号：`aluka` ｜ 文档版本：v0.1 ｜ 日期：2026-08-02
> 配套文档：[需求分析文档](./requirements-analysis.md)

---

## 目录

1. [文档概述](#1-文档概述)
2. [总体路线图](#2-总体路线图)
3. [Phase 0：工程基座](#phase-0工程基座)
4. [Phase 1：JS 引擎 + 模块系统 + TS 转译](#phase-1js-引擎--模块系统--ts-转译)
5. [Phase 2：Node.js 核心内置模块](#phase-2nodejs-核心内置模块)
6. [Phase 3：Web API + P1 Node 模块](#phase-3web-api--p1-node-模块)
7. [Phase 4：Bun 特有 API](#phase-4bun-特有-api)
8. [Phase 5：包管理器](#phase-5包管理器)
9. [Phase 6：测试器](#phase-6测试器)
10. [Phase 7：打包器](#phase-7打包器)
11. [Phase 8：优化与生态](#phase-8优化与生态)
12. [工程规范](#12-工程规范)
13. [测试策略](#13-测试策略)
14. [发布与版本管理](#14-发布与版本管理)
15. [风险管理](#15-风险管理)

---

## 1. 文档概述

### 1.1 文档目的

本文档基于 [需求分析文档](./requirements-analysis.md) 中确定的功能范围与约束，制定可执行的开发计划，包含：

- 每个 Phase 的目标、范围、任务分解（WBS）
- 关键模块设计与接口约定
- 验收标准与测试策略
- 工程规范（代码风格、CI/CD、版本管理）

### 1.2 制定原则

| 原则 | 说明 |
|------|------|
| **可执行性** | 每个 Phase 结束必须产出可发布的二进制 |
| **可验证性** | 每个任务必须有可量化的验收点 |
| **可中断性** | Phase 之间松耦合，允许暂停后继续 |
| **风险前置** | 高风险技术点在 Phase 早期验证（如字节码 VM 性能） |
| **范围控制** | 严格遵循需求分析的范围，避免 scope creep |

### 1.3 适用范围

- 项目维护者：作为任务分配与进度跟踪依据
- 贡献者：作为入门与认领任务的参考
- 用户：作为功能预期与发布时间表参考

---

## 2. 总体路线图

### 2.1 Phase 时间轴

```
Phase 0 ── Phase 1 ──────── Phase 2 ──── Phase 3 ──── Phase 4 ──── Phase 5 ── Phase 6 ──┐
 工程基座    JS 引擎+模块+TS    Node 核心模块   Web API      Bun API     包管理    测试器    │
                                                                                       │
                                                          Phase 7 ── 打包器 ───────────┤
                                                                                       │
                                                          Phase 8 ── 优化与生态 ───────┘
```

| Phase | 名称 | 关键里程碑 | 主要依赖 |
|-------|------|-----------|----------|
| 0 | 工程基座 | `aluka -e "console.log(1+1)"` 跑通 | — |
| 1 | JS 引擎 + 模块 + TS | test262 ES5+ES2015 ≥ 60% | Phase 0 |
| 2 | Node 核心模块 | HTTP server demo 通过 | Phase 1 |
| 3 | Web API + P1 Node | Top 100 npm ≥ 70% 加载 | Phase 2 |
| 4 | Bun 特有 API | Bun.serve + Bun.SQL demo | Phase 3 |
| 5 | 包管理器 | `aluka install express` 工作 | Phase 4 |
| 6 | 测试器 | 跑通 Jest 风格测试 | Phase 4 |
| 7 | 打包器 | 单文件可执行产物 | Phase 4 |
| 8 | 优化与生态 | benchmarks 接近 Bun 60% | Phase 5,6,7 |

### 2.2 依赖关系图

```
        ┌──────────┐
        │ Phase 0  │
        └────┬─────┘
             │
        ┌────▼─────────┐
        │   Phase 1   │ (JS 引擎)
        └────┬─────────┘
             │
        ┌────▼─────────┐
        │   Phase 2   │ (Node 核心)
        └────┬─────────┘
             │
        ┌────▼─────────┐
        │   Phase 3   │ (Web API)
        └────┬─────────┘
             │
        ┌────▼─────────┐
        │   Phase 4   │ (Bun API)
        └────┬─────────┘
             │
   ┌─────────┼─────────┐
   │         │         │
┌──▼──┐  ┌───▼───┐  ┌──▼───┐
│ P5  │  │  P6   │  │  P7  │
│包管 │  │测试器 │  │打包器│
└──┬──┘  └───┬───┘  └──┬───┘
   │         │         │
   └─────────┼─────────┘
             │
        ┌────▼─────────┐
        │   Phase 8   │ (优化)
        └──────────────┘
```

### 2.3 关键技术验证点

下列高风险技术点必须在所属 Phase 早期完成 PoC，避免后期返工：

| 验证点 | 所在 Phase | 验证方式 |
|--------|-----------|----------|
| 字节码 VM 基本性能 | Phase 1 中期 | 跑 fib(30) 对比 goja，目标 ≥ 30% |
| GC 与 Go runtime 协作 | Phase 1 后期 | 长时间运行无内存增长 |
| JS 正则引擎正确性 | Phase 2 中期 | test262 regex 子集 ≥ 90% |
| Go ↔ JS 异步桥接延迟 | Phase 2 后期 | I/O 回调延迟 < 1ms |
| TS 转译完整度 | Phase 1 末期 | TS 官方 conformance ≥ 50% |
| 模块解析算法 | Phase 1 中期 | Node.js 解析测试用例 ≥ 80% |

---

## Phase 0：工程基座

### 目标

搭建项目骨架，最小可运行二进制：能执行 `console.log(1+1)` 输出 `2`，验证端到端构建链路通畅。

### 范围

- 项目目录结构
- Go module 初始化
- CLI 框架（仅 `run` / `-e` / `--version` / `--help`）
- JS 引擎抽象层（接口定义 + 临时桩实现）
- 全局 `console` 对象（仅 `log/error/warn/info`）
- 全局 `process.argv` / `process.env`
- CI 基础（lint + test + build）

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 0.1 | 创建 Go module 与目录骨架 | `go.mod`、目录树 |
| 0.2 | 编写 `.golangci.yml`、`.editorconfig` | 配置文件 |
| 0.3 | 实现 CLI 入口 `cmd/aluka/main.go` | 二进制能解析 `-e`/`--version` |
| 0.4 | 设计引擎抽象层接口 `internal/engine/engine.go` | `Engine`/`Context`/`Value` 接口 |
| 0.5 | 实现临时桩引擎 `internal/engine/stub.go`（仅能 eval 简单表达式） | 跑通 `1+1` |
| 0.6 | 实现 `console.log/error/warn/info` | 输出到 stdout/stderr |
| 0.7 | 实现 `process.argv`/`process.env` 基础版 | 命令行参数读取 |
| 0.8 | 配置 GitHub Actions：lint + test + 多平台 build | `.github/workflows/ci.yml` |
| 0.9 | 编写 `Makefile`（build/test/install/release） | `make build` 可用 |
| 0.10 | 编写 README.md（最小：项目简介 + 构建 + 使用） | `README.md` |

### 模块设计

#### `internal/engine/engine.go`（接口定义）

```go
package engine

// Engine 是 JS 引擎的抽象接口，便于后续替换实现（Phase 1 起用自研 VM）。
type Engine interface {
    // NewContext 创建一个新的执行上下文（全局对象独立）。
    NewContext() (Context, error)
    // Shutdown 释放引擎资源。
    Shutdown() error
    // Version 返回引擎版本信息。
    Version() string
}

// Context 是一次 JS 执行上下文。
type Context interface {
    // Eval 执行一段 JS 代码，返回结果值。
    Eval(code string, filename string) (Value, error)
    // Global 获取全局对象。
    Global() Object
    // RegisterFunc 注册一个 Go 函数为全局可调用。
    RegisterFunc(name string, fn Func) error
    Close() error
}

// Value 是 JS 值的抽象（undefined/null/bool/number/string/object/function）。
type Value interface {
    Type() ValueType
    String() string
    Int() (int, bool)
    Float() (float64, bool)
    Bool() (bool, bool)
    IsUndefined() bool
    IsNull() bool
    IsObject() bool
    IsFunction() bool
}

type ValueType int
const (
    TypeUndefined ValueType = iota
    TypeNull
    TypeBoolean
    TypeNumber
    TypeString
    TypeObject
    TypeFunction
)

type Object interface {
    Value
    Get(key string) (Value, error)
    Set(key string, value Value) error
    Keys() []string
}

type Func func(args []Value) (Value, error)
```

#### `cmd/aluka/main.go`（CLI 框架）

```go
// 命令行入口。Phase 0 仅支持：
//   aluka -e "<code>"
//   aluka --version
//   aluka --help
// 后续 Phase 渐进添加 run/install/test/build 子命令。
package main

// 子命令分发设计（占位）：
//   run    Phase 0 简写为默认行为
//   eval   Phase 0
//   repl   Phase 1
//   install Phase 5
//   test   Phase 6
//   build  Phase 7
```

### 验收清单

- [ ] `go build ./cmd/aluka` 成功，二进制 < 5MB
- [ ] `aluka --version` 输出 `aluka 0.1.0-dev`
- [ ] `aluka --help` 显示帮助
- [ ] `aluka -e "console.log(1+1)"` 输出 `2`
- [ ] `aluka -e "console.error('err')"` 输出到 stderr
- [ ] CI 在 linux/darwin/windows 三端通过
- [ ] `go test ./...` 通过，覆盖率 ≥ 50%
- [ ] 冷启动延迟（`aluka -e "console.log(1)"`）< 50ms

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 0-R1 | 引擎抽象接口设计不当，后期返工 | 参考现有引擎（goja/v8go）API；接口尽量小 |
| 0-R2 | Windows 上 cgo 默认开启导致 build 失败 | 显式 `CGO_ENABLED=0`；CI 验证 |

---

## Phase 1：JS 引擎 + 模块系统 + TS 转译

### 目标

完成自研 JS 引擎核心，支持 ES5 + ES2015 全集 + 部分 ES2016-ES2020；实现 ESM/CJS 双模模块系统；实现 TS 类型剥离转译。能直接运行 `.ts` 文件。

### 范围

- Lexer / Parser / AST / Compiler / VM / GC
- ES5 + ES2015 完整实现
- ES2016-ES2020 关键特性（async/await、可选链、空值合并、BigInt、动态 import）
- 自研 JS 正则引擎
- ESM 加载器 + CJS 加载器
- Node.js 完整模块解析算法
- TS 转译器（类型剥离 + enum/namespace）
- `tsconfig.json` 读取（`paths`/`baseUrl`/`module`）
- 字节码缓存

### 子阶段划分

为控制复杂度，Phase 1 内部拆分为 4 个里程碑：

| 里程碑 | 名称 | 范围 |
|--------|------|------|
| **1A** | AST-walking PoC | Lexer + Parser + AST-walking 解释器，跑通 ES5 子集 |
| **1B** | 字节码 VM | 升级为字节码 VM，性能 ≥ goja 的 30% |
| **1C** | ES2015+ 模块 | 完整 ES2015 + ESM/CJS + Node 解析 |
| **1D** | TS 转译 + ES2017-2020 | TS strip-types + async/await + 可选链等 |

### 任务分解（WBS）

#### 1A：AST-walking PoC

| ID | 任务 | 输出 |
|----|------|------|
| 1A.1 | 实现 Lexer：完整 ES2023 token 集 | `internal/engine/lexer/lexer.go` |
| 1A.2 | 实现 Parser：递归下降 + Pratt 表达式 | `internal/engine/parser/parser.go` |
| 1A.3 | 定义 AST 节点类型 | `internal/engine/ast/` |
| 1A.4 | 实现 AST-walking 解释器 | `internal/engine/interpreter/` |
| 1A.5 | 实现基本内置：`Object`/`Array`/`String`/`Number`/`Boolean`/`Math`/`JSON` | `internal/engine/builtins/` |
| 1A.6 | 实现 `Error`/`TypeError`/`RangeError`/`SyntaxError` | `internal/engine/builtins/error.go` |
| 1A.7 | test262 ES5 子集回归 | 通过率 ≥ 50% |

#### 1B：字节码 VM

| ID | 任务 | 输出 |
|----|------|------|
| 1B.1 | 设计字节码指令集 | `internal/engine/bytecode/opcodes.go` |
| 1B.2 | 实现 Compiler（AST → Bytecode） | `internal/engine/compiler/` |
| 1B.3 | 实现 VM（栈式执行） | `internal/engine/vm/` |
| 1B.4 | 实现闭包环境（upvalue） | `internal/engine/vm/closure.go` |
| 1B.5 | 实现隐藏类（hidden class）+ 内联缓存 | `internal/engine/vm/inline_cache.go` |
| 1B.6 | 实现 GC：arena + 三色标记-清除 | `internal/engine/gc/` |
| 1B.7 | 性能基准：fib(30) ≥ goja 30% | `bench/fib_test.go` |
| 1B.8 | test262 ES5 通过率 ≥ 60% | CI 集成 |

#### 1C：ES2015 + 模块系统

| ID | 任务 | 输出 |
|----|------|------|
| 1C.1 | 实现 `let`/`const`/块级作用域 | parser + vm 扩展 |
| 1C.2 | 实现箭头函数 + `this` 绑定 | parser + vm 扩展 |
| 1C.3 | 实现 `class` 语法（含 extends/static/getter/setter） | `internal/engine/builtins/class.go` |
| 1C.4 | 实现 `Promise` + microtask 队列 | `internal/engine/builtins/promise.go` + `internal/runtime/eventloop/microtask.go` |
| 1C.5 | 实现 `Symbol`/`Map`/`Set`/`WeakMap`/`WeakSet` | `internal/engine/builtins/collections.go` |
| 1C.6 | 实现迭代器协议 + `for...of` + 生成器 | `internal/engine/builtins/iterator.go` |
| 1C.7 | 实现模板字符串 / 解构 / 默认参数 / rest/spread | parser + vm |
| 1C.8 | 实现 `Proxy`/`Reflect` | `internal/engine/builtins/proxy.go` |
| 1C.9 | 实现 ESM 加载器 | `internal/runtime/module/esm.go` |
| 1C.10 | 实现 CJS 加载器 | `internal/runtime/module/cjs.go` |
| 1C.11 | 实现 Node.js 模块解析算法 | `internal/runtime/module/resolver.go` |
| 1C.12 | 实现 `tsconfig.json` 读取 | `internal/runtime/module/tsconfig.go` |
| 1C.13 | 实现路径别名（`paths`/`baseUrl`） | resolver 扩展 |
| 1C.14 | 实现字节码缓存 | `internal/runtime/module/cache.go` |
| 1C.15 | test262 ES5+ES2015 ≥ 60% | CI |

#### 1D：TS 转译 + ES2017-2020

| ID | 任务 | 输出 |
|----|------|------|
| 1D.1 | 扩展 Parser 支持 TS 语法 | `internal/engine/parser/ts_extensions.go` |
| 1D.2 | 实现类型剥离（注解、interface、type 别名） | `internal/runtime/transpiler/strip.go` |
| 1D.3 | 实现 `enum` 转换 | `internal/runtime/transpiler/enum.go` |
| 1D.4 | 实现 `namespace` 转换 | `internal/runtime/transpiler/namespace.go` |
| 1D.5 | 实现参数属性（`constructor(public x)`） | transpiler 扩展 |
| 1D.6 | 实现装饰器（Stage 3 提案） | transpiler 扩展 |
| 1D.7 | 实现 `as`/`satisfies` 断言剥离 | transpiler 扩展 |
| 1D.8 | 实现 `import type`/`export type` 删除 | transpiler 扩展 |
| 1D.9 | 实现 ES2017：`async`/`await` | vm 扩展（基于 Promise + microtask） |
| 1D.10 | 实现 ES2018：`for await...of`/rest in object/Promise.finally | vm 扩展 |
| 1D.11 | 实现 ES2019：`Array.flat/flatMap`/`Object.fromEntries`/`trimStart` | builtins |
| 1D.12 | 实现 ES2020：可选链 `?.`/空值合并 `??`/`BigInt`/`Promise.allSettled` | parser + vm |
| 1D.13 | 实现动态 `import()` | 模块系统扩展 |
| 1D.14 | TS 官方 conformance 测试 ≥ 50% | CI |
| 1D.15 | 实现 REPL 基础 | `aluka repl` 子命令 |

### 模块设计

#### 引擎模块结构

```
internal/engine/
├── engine.go              # Engine/Context/Value 接口实现
├── lexer/
│   ├── lexer.go           # 主词法分析器
│   ├── token.go           # Token 类型定义
│   └── lexer_test.go
├── parser/
│   ├── parser.go          # 递归下降 parser
│   ├── pratt.go           # Pratt 表达式解析
│   ├── ts_extensions.go   # TS 语法扩展
│   └── parser_test.go
├── ast/
│   ├── node.go            # AST 节点接口
│   ├── stmt.go            # 语句节点
│   ├── expr.go            # 表达式节点
│   ├── decl.go           # 声明节点
│   └── visitor.go         # Visitor 模式
├── compiler/
│   ├── compiler.go        # AST → Bytecode
│   ├── scope.go           # 作用域分析
│   └── debug_info.go      # 行号表
├── bytecode/
│   ├── opcodes.go         # 指令集定义
│   ├── module.go          # 字节码模块结构
│   └── reader.go          # 字节码反序列化（用于缓存）
├── vm/
│   ├── vm.go              # 栈式 VM 主循环
│   ├── frame.go           # 调用帧
│   ├── closure.go         # 闭包环境
│   ├── upvalue.go         # upvalue 实现
│   ├── inline_cache.go    # 内联缓存
│   ├── hidden_class.go    # 隐藏类
│   └── exceptions.go      # 异常抛出/捕获
├── gc/
│   ├── arena.go           # 内存池
│   ├── marker.go          # 三色标记
│   ├── sweeper.go         # 清除
│   └── pinner.go          # 与 Go runtime 协作
├── regex/
│   ├── compiler.go        # 正则 → NFA
│   ├── matcher.go         # 回溯匹配器
│   └── unicode.go         # Unicode 属性支持
└── builtins/
    ├── object.go          # Object/Function
    ├── array.go           # Array
    ├── string.go          # String
    ├── number.go          # Number/Math
    ├── boolean.go         # Boolean
    ├── json.go            # JSON
    ├── error.go           # Error 体系
    ├── symbol.go          # Symbol
    ├── promise.go         # Promise
    ├── collections.go     # Map/Set/WeakMap/WeakSet
    ├── iterator.go        # Iterator/Generator
    ├── proxy.go           # Proxy/Reflect
    ├── typed_array.go     # ArrayBuffer/TypedArray/DataView
    └── bigint.go          # BigInt
```

#### 模块系统结构

```
internal/runtime/module/
├── resolver.go            # Node.js 解析算法
├── loader.go             # 文件加载器
├── esm.go                # ESM 加载器
├── cjs.go                # CJS 加载器
├── cache.go              # 字节码缓存
├── tsconfig.go           # tsconfig.json 解析
└── cycle.go              # 循环依赖处理
```

### 验收清单

#### 1A 验收

- [ ] `aluka -e "1+2*3"` 输出 `7`
- [ ] `aluka -e "[1,2,3].map(x=>x*2)"` 输出 `[2,4,6]`
- [ ] `aluka -e "({a:1,b:2}).a"` 输出 `1`
- [ ] `aluka -e "try{null.foo}catch(e){console.log(e.message)}"` 输出错误信息

#### 1B 验收

- [ ] `bench/fib_test.go` 性能 ≥ goja 30%
- [ ] GC 测试：循环创建 100 万对象无内存泄漏
- [ ] test262 ES5 通过率 ≥ 60%

#### 1C 验收

- [ ] `aluka run mod.mjs` 跑通 `import`/`export`
- [ ] `aluka run mod.cjs` 跑通 `require`/`module.exports`
- [ ] `aluka run nested.ts` 跑通 `tsconfig.paths` 别名
- [ ] `node_modules/lodash/lodash.js` 可被 require
- [ ] test262 ES5+ES2015 通过率 ≥ 60%

#### 1D 验收

- [ ] `aluka run hello.ts` 跑通带类型注解的 TS 文件
- [ ] `aluka run enum.ts` 跑通 enum 转换
- [ ] `aluka run async.ts` 跑通 `async/await`
- [ ] `aluka run bigint.js` 跑通 `123n * 456n`
- [ ] TS conformance 通过率 ≥ 50%
- [ ] `aluka repl` 启动交互式 REPL

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 1-R1 | 字节码 VM 设计复杂度超估 | 1A 先 AST-walking 跑通，1B 再升级 |
| 1-R2 | GC 与 Go runtime 冲突导致崩溃 | 用 `runtime.Pinner` + 早期压力测试 |
| 1-R3 | TS 转译 corner case 多 | 分阶段：先 strip-types，再 enum/namespace/decorator |
| 1-R4 | 模块解析算法复杂 | 严格按 Node.js 规范，用 Node 官方测试用例回归 |
| 1-R5 | 正则引擎正确性 | 实现 PCRE 子集 + test262 regex 测试 |

---

## Phase 2：Node.js 核心内置模块

### 目标

实现 P0 级 Node.js 内置模块（14 个核心 + 6 个网络），让 aluka 能跑通典型的 HTTP 服务器和文件操作代码。

### 范围

| 模块组 | 模块列表 |
|--------|----------|
| 核心 I/O | `fs`、`path`、`os`、`url`、`querystring`、`events`、`util`、`assert`、`stream`、`buffer`、`process`（完整）、`crypto`、`string_decoder` |
| 网络 | `http`、`https`、`net`、`tls`、`dns`、`zlib` |
| 全局增强 | 完整版 `console`、`process`、`Buffer`、`TextEncoder/Decoder`、`URL/URLSearchParams`、`AbortController`、`Event/EventTarget`、`setTimeout/setInterval/setImmediate`、`queueMicrotask` |

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 2.1 | 设计模块注册机制（`builtin.Register("fs", NewFS)`） | `internal/builtin/registry.go` |
| 2.2 | 实现 `console` 完整版（`table`/`group`/`time`/`dir`/`trace`/`assert`） | `internal/runtime/globals/console.go` |
| 2.3 | 实现 `process` 完整版（`stdout`/`stderr`/`stdin`/`on`/`kill`/`exit`/`hrtime`） | `internal/runtime/globals/process.go` |
| 2.4 | 实现 `Buffer`（基于 `[]byte`，Node API 完整） | `internal/runtime/globals/buffer.go` |
| 2.5 | 实现 `TextEncoder/Decoder`/`atob`/`btoa` | `internal/runtime/globals/encoding.go` |
| 2.6 | 实现 `URL`/`URLSearchParams`（WHATWG） | `internal/runtime/globals/url.go` |
| 2.7 | 实现 `AbortController`/`AbortSignal` | `internal/runtime/globals/abort.go` |
| 2.8 | 实现 `Event`/`EventTarget`/`CustomEvent` | `internal/runtime/globals/event.go` |
| 2.9 | 实现完整 Timers（`setTimeout`/`setInterval`/`setImmediate`/`queueMicrotask`） | `internal/runtime/globals/timers.go` |
| 2.10 | 实现 `node:fs`（sync/async/promises + `createReadStream/WriteStream`） | `internal/builtin/node/fs/` |
| 2.11 | 实现 `node:path`（`posix`+`win32` 双实现） | `internal/builtin/node/path/` |
| 2.12 | 实现 `node:os` | `internal/builtin/node/os/` |
| 2.13 | 实现 `node:url`/`node:querystring` | `internal/builtin/node/url/` |
| 2.14 | 实现 `node:events`（`EventEmitter` 完整 API） | `internal/builtin/node/events/` |
| 2.15 | 实现 `node:util`（`inspect`/`promisify`/`format`/`types`/`deprecate`） | `internal/builtin/node/util/` |
| 2.16 | 实现 `node:assert`（strict + loose） | `internal/builtin/node/assert/` |
| 2.17 | 实现 `node:stream`（`Readable`/`Writable`/`Duplex`/`Transform`/`pipeline`/`finished`） | `internal/builtin/node/stream/` |
| 2.18 | 实现 `node:buffer`（与全局 `Buffer` 同源） | 复用 2.4 |
| 2.19 | 实现 `node:crypto`（`createHash`/`createHmac`/`randomBytes`/`scrypt`/`pbkdf2`/`createCipheriv`） | `internal/builtin/node/crypto/` |
| 2.20 | 实现 `node:string_decoder` | `internal/builtin/node/string_decoder/` |
| 2.21 | 实现 `node:http`（基于 Go `net/http`，完整 API） | `internal/builtin/node/http/` |
| 2.22 | 实现 `node:https` | `internal/builtin/node/http/https.go` |
| 2.23 | 实现 `node:net`（TCP） | `internal/builtin/node/net/` |
| 2.24 | 实现 `node:tls` | `internal/builtin/node/tls/` |
| 2.25 | 实现 `node:dns`（`lookup`/`resolve`/`promises`） | `internal/builtin/node/dns/` |
| 2.26 | 实现 `node:zlib`（`gzip`/`deflate`/`brotli`） | `internal/builtin/node/zlib/` |
| 2.27 | Node.js 官方测试子集回归 | `tests/conformance/node/` |

### 模块设计

#### `internal/builtin/registry.go`

```go
package builtin

// Registry 注册内置模块的工厂函数。
type Registry struct {
    modules map[string]ModuleFactory
}

// ModuleFactory 接收运行时上下文，返回模块的导出对象。
type ModuleFactory func(ctx *Context) (Value, error)

func (r *Registry) Register(name string, factory ModuleFactory) { ... }
func (r *Registry) Resolve(name string) (ModuleFactory, bool) { ... }

// 在 init() 中注册所有内置模块。
func init() {
    Registry.Register("node:fs", fs.NewModule)
    Registry.Register("node:path", path.NewModule)
    // ...
}
```

#### `internal/builtin/node/http/server.go`（设计要点）

```go
// node:http Server 实现要点：
// 1. 用 Go net/http.Server 作为底层
// 2. 每个 request 在独立 goroutine 处理
// 3. 通过 PostTask 把 request 投递回 JS goroutine 调用 user handler
// 4. ServerResponse 用 chunked 流式写入，避免大响应占内存
// 5. keep-alive 复用连接
```

### 验收清单

- [ ] `aluka run fs_demo.js`：读写文件、`stat`、`mkdir`、`readdir` 全工作
- [ ] `aluka run http_server.js`：HTTP 服务可访问，wrk RPS ≥ 60k
- [ ] `aluka run stream_pipeline.js`：`pipeline` 串接 Readable→Transform→Writable
- [ ] `aluka run crypto_demo.js`：`sha256`/`aes-256-cbc`/`scrypt` 工作
- [ ] `aluka run net_echo.js`：TCP echo server 工作
- [ ] `aluka run zlib_demo.js`：gzip 压缩/解压
- [ ] Node.js 官方测试子集通过率 ≥ 70%

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 2-R1 | `stream` 实现复杂（背压/异步迭代） | 分阶段：先同步流，再加异步迭代 |
| 2-R2 | HTTP server 在 JS 单线程下吞吐受限 | I/O 在 Go goroutine 异步，仅回调进 JS |
| 2-R3 | `crypto` API 巨大 | 优先实现 hash/HMAC/random，对称加密延后 |
| 2-R4 | `Buffer` 与 JS 视图零拷贝复杂 | 用 `unsafe.Slice` 共享；先做拷贝版本 |

---

## Phase 3：Web API + P1 Node 模块

### 目标

实现 WHATWG Web API（fetch/WebSocket/Stream 等）和 P1 级 Node.js 模块（`child_process`/`worker_threads`/`timers/promises` 等），让 aluka 能加载运行大部分纯 JS 的 npm 包。

### 范围

| 类别 | 内容 |
|------|------|
| Web API | `fetch`/`Request`/`Response`/`Headers`/`FormData`、`WebSocket`、`ReadableStream`/`WritableStream`/`TransformStream`、`Blob`/`File`、`crypto.subtle`、`URLPattern`、`MessageChannel`/`MessagePort` |
| P1 Node 模块 | `child_process`、`worker_threads`、`perf_hooks`、`timers/promises`、`readline`、`repl`、`module`、`v8` |
| 兼容性测试 | Top 100 npm 包加载测试框架 |

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 3.1 | 实现 `fetch` + `Request`/`Response`/`Headers`/`FormData` | `internal/builtin/web/fetch/` |
| 3.2 | 实现 `WebSocket` + `WebSocketServer`（基于 `gorilla/websocket`） | `internal/builtin/web/websocket/` |
| 3.3 | 实现 `ReadableStream`/`WritableStream`/`TransformStream` | `internal/builtin/web/streams/` |
| 3.4 | 实现 `Blob`/`File` | `internal/builtin/web/blob/` |
| 3.5 | 实现 `crypto.subtle`（Web Crypto subset） | `internal/builtin/web/crypto/` |
| 3.6 | 实现 `URLPattern` | `internal/builtin/web/url_pattern/` |
| 3.7 | 实现 `MessageChannel`/`MessagePort` | `internal/builtin/web/messagechannel/` |
| 3.8 | 实现 `node:child_process`（`spawn`/`exec`/`execFile`/`fork`） | `internal/builtin/node/child_process/` |
| 3.9 | 实现 `node:worker_threads`（基于 Go goroutine + SharedArrayBuffer） | `internal/builtin/node/worker_threads/` |
| 3.10 | 实现 `node:perf_hooks` | `internal/builtin/node/perf_hooks/` |
| 3.11 | 实现 `node:timers/promises` | `internal/builtin/node/timers/promises.go` |
| 3.12 | 实现 `node:readline` | `internal/builtin/node/readline/` |
| 3.13 | 实现 `node:repl`（内部 REPL API） | `internal/builtin/node/repl/` |
| 3.14 | 实现 `node:module`（`createRequire`/`register`） | `internal/builtin/node/module/` |
| 3.15 | 实现 `node:v8`（subset：`serialize`/`deserialize`） | `internal/builtin/node/v8/` |
| 3.16 | 搭建 npm 包兼容性测试框架 | `tests/conformance/npm/` |
| 3.17 | 自动化拉取 Top 100 npm 包并加载测试 | CI 集成 |

### 验收清单

- [ ] `aluka run fetch_demo.js`：调用 `https://httpbin.org/get` 返回 JSON
- [ ] `aluka run ws_server.js`：WebSocket server 收发消息
- [ ] `aluka run stream_demo.js`：`ReadableStream.pipeTo(WritableStream)` 工作
- [ ] `aluka run child_process.js`：`spawn('ls')` 输出目录
- [ ] `aluka run worker.js`：worker_threads 收发消息
- [ ] Top 100 npm 包 ≥ 70% 可加载（`require('express')` 不报错）
- [ ] Top 50 npm 包 ≥ 60% 可执行基础 demo

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 3-R1 | `worker_threads` 与主线程共享内存复杂 | 先做独立堆 + 消息传递；SharedArrayBuffer 延后 |
| 3-R2 | `fetch` 流式响应处理复杂 | 先做完整缓冲，再加流式 |
| 3-R3 | npm 包兼容性长尾多 | 不追求 100%；标注已知不兼容清单 |

---

## Phase 4：Bun 特有 API

### 目标

实现 P0 + P1 级 Bun 特有 API，让 Bun 用户的代码可以基本无修改地在 aluka 上运行。

### 范围

- P0：`Bun.serve`、`Bun.file`/`write`、`Bun.env`、`Bun.sleep`/`sleepSync`、`Bun.nanoseconds`、`Bun.gc`、`Bun.main`/`cwd`/`origin`/`version`/`platform`、`Bun.stdin`/`stdout`/`stderr`
- P1：`Bun.$`、`Bun.password`、`Bun.hash`、`Bun.deflate`/`inflate`、`Bun.peek`、`Bun.deepEquals`/`deepAssign`、`Bun.tsv`/`csv`/`YAML`/`toml`、`Bun.spawn`/`spawnSync`、`Bun.which`、`Bun.unsafe`、`Bun.dns`
- P2 部分：`Bun.SQL`（Postgres + SQLite）、`Bun.Redis`、`Bun.S3`

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 4.1 | 设计 `Bun` 全局对象结构 | `internal/builtin/bun/bun.go` |
| 4.2 | 实现 `Bun.serve`（HTTP + WebSocket 一体，基于 Phase 2 http + Phase 3 ws） | `internal/builtin/bun/serve.go` |
| 4.3 | 实现 `Bun.file`/`Bun.write`/`Bun.openInEditor` | `internal/builtin/bun/file.go` |
| 4.4 | 实现 `Bun.env`（与 `process.env` 同源，支持嵌套） | `internal/builtin/bun/env.go` |
| 4.5 | 实现 `Bun.sleep`/`Bun.sleepSync`/`Bun.nanoseconds` | `internal/builtin/bun/time.go` |
| 4.6 | 实现 `Bun.gc`/`Bun.unsafe` | `internal/builtin/bun/gc.go` |
| 4.7 | 实现 `Bun.main`/`cwd`/`origin`/`version`/`platform` | `internal/builtin/bun/info.go` |
| 4.8 | 实现 `Bun.stdin`/`stdout`/`stderr`（BunFile 包装） | `internal/builtin/bun/stdio.go` |
| 4.9 | 实现 `Bun.$` 跨平台 shell（基于 `mvdan.cc/sh/v3`） | `internal/builtin/bun/shell.go` |
| 4.10 | 实现 `Bun.password`（bcrypt/argon2） | `internal/builtin/bun/password.go` |
| 4.11 | 实现 `Bun.hash`（wyhash/crc32/sha*） | `internal/builtin/bun/hash.go` |
| 4.12 | 实现 `Bun.deflate`/`inflate`/`gzip`/`gunzip` | `internal/builtin/bun/compress.go` |
| 4.13 | 实现 `Bun.peek`/`Bun.deepEquals`/`Bun.deepAssign` | `internal/builtin/bun/util.go` |
| 4.14 | 实现 `Bun.tsv`/`csv`/`YAML`/`toml` | `internal/builtin/bun/encoding.go` |
| 4.15 | 实现 `Bun.spawn`/`spawnSync`（与 `node:child_process` 不同的 API） | `internal/builtin/bun/spawn.go` |
| 4.16 | 实现 `Bun.which`/`Bun.dns`/`Bun.escapeHTML`/`Bun.fileType`/`Bun.isTerminal` | `internal/builtin/bun/util.go` |
| 4.17 | 实现 `Bun.SQL`（Postgres，基于 `jackc/pgx`） | `internal/builtin/bun/sql/postgres.go` |
| 4.18 | 实现 `Bun.SQL`（SQLite，基于 `modernc.org/sqlite`） | `internal/builtin/bun/sql/sqlite.go` |
| 4.19 | 实现 `Bun.Redis`（基于 `redis/go-redis/v9`） | `internal/builtin/bun/redis.go` |
| 4.20 | 实现 `Bun.S3`（基于 `aws-sdk-go-v2`） | `internal/builtin/bun/s3.go` |
| 4.21 | Bun 官方 example 集回归测试 | `tests/conformance/bun/` |

### 验收清单

- [ ] `aluka run bun_serve.ts`：HTTP + WebSocket 一体服务跑通
- [ ] `aluka run bun_file.ts`：`Bun.file().text()` 读文件，`Bun.write` 写文件
- [ ] `aluka run bun_shell.ts`：`Bun.$\`ls -la\`` 输出目录
- [ ] `aluka run bun_password.ts`：bcrypt hash + verify
- [ ] `aluka run bun_sql.ts`：Postgres + SQLite 增删改查
- [ ] `aluka run bun_redis.ts`：set/get 工作
- [ ] Bun 官方 example 集通过率 ≥ 70%

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 4-R1 | `Bun.$` shell 模板字符串语义复杂 | 严格按 Bun 文档实现；用 Bun 自身测试回归 |
| 4-R2 | `Bun.SQL` 连接池与 JS 异步桥接 | 连接池在 Go 侧管理；查询通过 PostTask 回 JS |
| 4-R3 | `Bun.ffi` 纯 Go 不可行 | 标注不实现；在文档中明确 |
| 4-R4 | `Bun.build`（bundler API）依赖 Phase 7 | 推迟到 Phase 7 |

---

## Phase 5：包管理器

### 目标

实现 npm 兼容的包管理器 `aluka install`，能解析 `package.json`、从 npm registry 下载、解析依赖树、写入 `node_modules`、生成 lockfile。

### 范围

- npm registry HTTP 客户端（含镜像、鉴权、scoped 包）
- semver 解析与依赖解析算法
- `node_modules` 布局（hoisting + nested）
- `aluka.lock` 文本 lockfile（兼容 `bun.lock` 格式）
- 生命周期脚本（`preinstall`/`postinstall`/`prepare`）
- `aluka install`/`add`/`remove`/`update`/`link`/`pm` 子命令
- workspace 支持

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 5.1 | 实现 npm registry HTTP 客户端 | `internal/pkgmanager/registry/client.go` |
| 5.2 | 实现 semver 解析与匹配 | `internal/pkgmanager/semver/` |
| 5.3 | 实现依赖解析算法（含 peer/optional/overrides） | `internal/pkgmanager/resolver/` |
| 5.4 | 实现 `node_modules` 布局策略 | `internal/pkgmanager/installer/layout.go` |
| 5.5 | 实现并发下载与解压（tar.gz） | `internal/pkgmanager/installer/download.go` |
| 5.6 | 实现 `aluka.lock` 读写（文本格式） | `internal/pkgmanager/lockfile/` |
| 5.7 | 实现生命周期脚本执行 | `internal/pkgmanager/installer/scripts.go` |
| 5.8 | 实现 `aluka install` 命令 | `cmd/aluka/install.go` |
| 5.9 | 实现 `aluka add`/`remove`/`update` | `cmd/aluka/pkgcmd.go` |
| 5.10 | 实现 `aluka link`/`pm` | 同上 |
| 5.11 | 实现 workspace 支持 | `internal/pkgmanager/workspace/` |
| 5.12 | 实现 `.npmrc` 读取 | `internal/pkgmanager/config/` |
| 5.13 | 兼容性测试：从 npm registry 安装真实包 | `tests/conformance/install/` |

### 验收清单

- [ ] `aluka install`：在空目录创建 `node_modules`
- [ ] `aluka install express`：成功安装 express 及其依赖
- [ ] `aluka add lodash`：更新 `package.json` + lockfile
- [ ] `aluka remove lodash`：移除并清理
- [ ] `aluka install` 速度：典型中型项目（50 依赖）< 10s
- [ ] `aluka run app.js`：依赖 express 的应用可运行
- [ ] workspace：monorepo 多包 install 工作

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 5-R1 | 依赖解析算法复杂（peer deps 冲突） | 参考npm/yarn/pnpm 实现；先做简单 hoisting |
| 5-R2 | 生命周期脚本可能调用 aluka 自身 | 检测 `aluka` 在 PATH；递归调用自身 |
| 5-R3 | npm registry 兼容性（私有 registry） | 实现 `.npmrc` 完整解析 |

---

## Phase 6：测试器

### 目标

实现 Jest 兼容的测试运行器 `aluka test`，支持 `describe`/`it`/`expect`/`mock`/`snapshot`/`coverage`。

### 范围

- `bun:test` 模块
- Jest 兼容 API（`describe`/`it`/`test`/`beforeEach`/`afterEach`/`beforeAll`/`afterAll`）
- `expect` 链式断言（完整 Jest matchers）
- `mock`/`jest.fn`/`jest.spyOn`/`jest.mock`
- 快照测试（`toMatchSnapshot`）
- 并行执行（每个测试文件独立 worker）
- Watch 模式
- Coverage 报告（基于字节码 PC 表）

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 6.1 | 实现 `bun:test` 模块 | `internal/testrunner/api/` |
| 6.2 | 实现 `expect` 链式断言（30+ matchers） | `internal/testrunner/expect/` |
| 6.3 | 实现 mock 系统（`jest.fn`/`spyOn`/`mock`） | `internal/testrunner/mock/` |
| 6.4 | 实现快照测试 | `internal/testrunner/snapshot/` |
| 6.5 | 实现并行执行（worker per test file） | `internal/testrunner/runner/` |
| 6.6 | 实现测试发现与过滤（pattern match） | `internal/testrunner/discovery/` |
| 6.7 | 实现终端报告器（彩色输出） | `internal/testrunner/reporter/` |
| 6.8 | 实现 Watch 模式 | `internal/testrunner/watch/` |
| 6.9 | 实现 Coverage 收集（字节码 PC 插桩） | `internal/testrunner/coverage/` |
| 6.10 | 实现 `aluka test` 命令 | `cmd/aluka/test.go` |
| 6.11 | 兼容性测试：跑通开源项目 Jest 套件 | `tests/conformance/jest/` |

### 验收清单

- [ ] `aluka test` 跑通 hello world 测试
- [ ] expect 全部 matchers 工作
- [ ] mock 系统工作（`jest.fn()` 返回值记录调用）
- [ ] 快照测试生成/更新
- [ ] 并行执行无干扰
- [ ] coverage 报告生成（LCOV + HTML）
- [ ] 跑通 lodash 测试套件子集 ≥ 50%

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 6-R1 | mock 系统复杂（hoisting、自动 mock） | 先做手动 mock，自动 mock 延后 |
| 6-R2 | coverage 字节码插桩影响性能 | 仅在 `--coverage` 时插桩 |

---

## Phase 7：打包器

### 目标

实现 JS/TS 打包器 `aluka build`，支持 tree-shaking、minify、多 target、`--compile` 单文件可执行。

### 范围

- 静态模块图构建
- Tree-shaking（基于 ESM export 用量分析）
- Minifier（基于 `tdewolff/minify/v2`）
- 多 target（browser/bun/node）
- `--compile` 单文件可执行（嵌入字节码 + 引擎 + 自执行 stub）
- Source map 生成
- 资源处理（CSS/JSON/图片，不包含 JSX）

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 7.1 | 实现模块图构建（静态分析 import/export） | `internal/bundler/graph/` |
| 7.2 | 实现 tree-shaking（dead code elimination） | `internal/bundler/shaker/` |
| 7.3 | 实现 minifier（变量名缩短、空白移除） | `internal/bundler/minify/` |
| 7.4 | 实现多 target 输出 | `internal/bundler/target/` |
| 7.5 | 实现 source map 生成 | `internal/bundler/sourcemap/` |
| 7.6 | 实现 `--compile` 单文件可执行（嵌入字节码） | `internal/bundler/compile/` |
| 7.7 | 实现资源处理（CSS/JSON/二进制） | `internal/bundler/assets/` |
| 7.8 | 实现 `aluka build` 命令 | `cmd/aluka/build.go` |
| 7.9 | 兼容性测试：打包真实项目 | `tests/conformance/build/` |

### 验收清单

- [ ] `aluka build ./src/index.ts --outdir ./dist` 生成打包文件
- [ ] tree-shaking 移除未使用 export
- [ ] minify 减小体积 ≥ 40%
- [ ] `--compile --outfile app` 生成单文件可执行
- [ ] `./app` 在无 aluka 安装的机器上运行
- [ ] source map 正确还原行号

### 风险

| ID | 风险 | 应对 |
|----|------|------|
| 7-R1 | tree-shaking 副作用分析复杂 | 保守策略：标记 `sideEffects: false` 才做 shake |
| 7-R2 | `--compile` 跨平台产物 | 用 Go `embed` + 各平台 builder |

---

## Phase 8：优化与生态

### 目标

性能优化、兼容性提升、生态建设，使 aluka 接近生产可用。

### 范围

- 性能基线建立与优化（benchmarks vs Bun/Node）
- test262 通过率提升至 ≥ 85%
- Node.js 官方测试通过率 ≥ 80%
- Top 500 npm 包兼容性 ≥ 80%
- 文档站（用法、API、迁移指南）
- VSCode 插件（语法、调试）
- REPL 增强（多行、历史、自动补全）
- `--inspect` 调试协议（Chrome DevTools）

### 任务分解（WBS）

| ID | 任务 | 输出 |
|----|------|------|
| 8.1 | 建立性能基准套件 | `bench/` |
| 8.2 | VM 热点优化（IC 命中率、隐藏类共享） | `internal/engine/vm/` |
| 8.3 | GC 优化（分代、增量） | `internal/engine/gc/` |
| 8.4 | test262 失败用例修复 | 持续 |
| 8.5 | Node.js 官方测试失败用例修复 | 持续 |
| 8.6 | npm 包兼容性修复 | 持续 |
| 8.7 | 文档站搭建（基于 Astro 或 VitePress） | `docs/site/` |
| 8.8 | 迁移指南（Node.js → aluka、Bun → aluka） | `docs/migration/` |
| 8.9 | VSCode 插件 | `editors/vscode/` |
| 8.10 | REPL 增强 | `cmd/aluka/repl.go` |
| 8.11 | Chrome DevTools 协议实现 | `internal/runtime/debug/` |
| 8.12 | Profile 工具（CPU/heap） | `internal/runtime/profile/` |

### 验收清单

- [ ] 性能 benchmark：HTTP RPS ≥ Bun 60%，启动延迟 ≥ Bun 80%
- [ ] test262 通过率 ≥ 85%
- [ ] Top 500 npm 包 ≥ 80% 可加载
- [ ] 文档站上线
- [ ] VSCode 插件发布
- [ ] `--inspect` 可在 Chrome DevTools 中调试

---

## 12. 工程规范

### 12.1 代码风格

| 项目 | 工具/规范 |
|------|----------|
| Go 代码格式化 | `gofmt` + `goimports` |
| Linter | `golangci-lint`（配置见 `.golangci.yml`） |
| 命名 | 包名小写单词；导出标识符 PascalCase；私有 camelCase |
| 文件长度 | 单文件 ≤ 500 行，超出拆分 |
| 函数复杂度 | cyclomatic complexity ≤ 15 |
| 错误处理 | 显式 `if err != nil`；禁用 panic（除非不可恢复） |
| 注释 | 公开 API 必须 doc comment；复杂逻辑必须说明 |

### 12.2 目录约定

```
aluka_lang/
├── cmd/                    # 仅 main 包
├── internal/               # 不对外的实现
├── pkg/                    # 对外稳定的 Go API
├── tests/                  # 集成/兼容性测试
├── bench/                  # 性能基准
├── docs/                   # 文档
├── scripts/                # 构建/发布脚本
├── .github/workflows/      # CI
├── .golangci.yml
├── .editorconfig
├── Makefile
├── go.mod
└── go.sum
```

### 12.3 提交规范（Conventional Commits）

```
<type>(<scope>): <subject>

<body>

<footer>
```

| type | 用途 |
|------|------|
| `feat` | 新功能 |
| `fix` | bug 修复 |
| `refactor` | 重构 |
| `perf` | 性能优化 |
| `test` | 测试 |
| `docs` | 文档 |
| `chore` | 构建/工具 |
| `ci` | CI 配置 |

示例：
```
feat(engine): 实现字节码 VM 主循环
fix(fs): 修复 readdir 顺序不稳定问题
perf(vm): 为属性访问添加内联缓存
```

### 12.4 分支策略

| 分支 | 用途 |
|------|------|
| `main` | 稳定主干，每次合并触发发布 |
| `develop` | 集成分支 |
| `feature/<name>` | 功能分支 |
| `fix/<name>` | 修复分支 |
| `release/v<x.y.z>` | 发布分支 |

### 12.5 PR 规范

- 每个 PR 单一职责，< 500 行 diff（除生成代码外）
- 必须含测试
- 必须通过 CI（lint + test + build）
- 必须有描述：背景、改动、测试方式
- 标签：`phase/<n>`、`type/<feat|fix|...>`、`needs-review`

---

## 13. 测试策略

### 13.1 测试金字塔

```
        ┌─────────────┐
        │   E2E 测试   │  端到端跑通真实项目
        ├─────────────┤
        │ 集成测试     │  模块组合行为
        ├─────────────┤
        │ 兼容性测试   │  test262 / Node / TS / Bun
        ├─────────────┤
        │ 单元测试     │  函数级
        └─────────────┘
```

### 13.2 各类测试要求

| 类别 | 范围 | 工具 | 频率 |
|------|------|------|------|
| 单元测试 | 每个 Go 包 | `go test` | 每次 commit |
| 集成测试 | 跨包组合 | `go test -tags=integration` | 每次 PR |
| test262 | ES 规范一致性 | 子集嵌入 CI | 每日 |
| Node.js conformance | Node 内置模块行为 | 子集嵌入 CI | 每日 |
| TS conformance | TS 转译完整度 | 子集嵌入 CI | 每日 |
| Bun example | Bun API 兼容 | 子集嵌入 CI | 每次 PR |
| 性能基准 | 关键指标 | `bench/` | 每周 |
| npm 兼容性 | Top N npm 包 | 自动化 | 每周 |

### 13.3 覆盖率要求

| 模块 | 覆盖率门槛 |
|------|-----------|
| `internal/engine/` | ≥ 80% |
| `internal/runtime/module/` | ≥ 80% |
| `internal/runtime/transpiler/` | ≥ 80% |
| `internal/builtin/node/` | ≥ 70% |
| `internal/builtin/web/` | ≥ 70% |
| `internal/builtin/bun/` | ≥ 70% |
| `cmd/aluka/` | ≥ 50% |
| 总体 | ≥ 70% |

### 13.4 性能基准

建立持续跟踪的性能基线：

| 基准 | 目标（Phase 8） | 测量 |
|------|----------------|------|
| `aluka -e "console.log(1)"` 冷启动 | < 30ms | hyperfine 50 次中位数 |
| fib(35) | ≥ goja 50% | benchstat |
| HTTP "Hello World" RPS | ≥ 60k | wrk -t4 -c100 |
| npm install（中型项目） | < 10s | 真实项目测试 |

---

## 14. 发布与版本管理

### 14.1 版本号

遵循 SemVer：`MAJOR.MINOR.PATCH`

| 阶段 | 版本范围 | 含义 |
|------|---------|------|
| Phase 0-1 | `0.1.x` | 预览，API 不稳定 |
| Phase 2-4 | `0.2.x - 0.4.x` | alpha，核心可用 |
| Phase 5-7 | `0.5.x - 0.7.x` | beta，工具链可用 |
| Phase 8 | `1.0.0` | 稳定版 |

### 14.2 发布渠道

| 渠道 | 用途 | 频率 |
|------|------|------|
| `latest` | 稳定版 | 每个 Phase 结束 |
| `canary` | 每日构建 | 每日 |
| `next` | 候选版 | 发布前 |

### 14.3 发布产物

每个 release 提供：

- 二进制：linux/darwin/windows × amd64/arm64（共 6 个）
- 校验和：`sha256sums.txt`
- 签名（Phase 8+）
- Release Notes（按 Conventional Commits 自动生成）
- Docker 镜像：`aluka:latest`、`aluka:<version>`

### 14.4 安装脚本

```bash
# curl 安装
curl -fsSL https://aluka.dev/install.sh | bash

# Windows PowerShell
irm https://aluka.dev/install.ps1 | iex

# Go install
go install github.com/aluka-lang/aluka/cmd/aluka@latest
```

---

## 15. 风险管理

### 15.1 顶级风险登记表

| ID | 风险 | 概率 | 影响 | 应对 | 触发预警 |
|----|------|------|------|------|----------|
| RM1 | JS 引擎实现工作量超估，Phase 1 严重延期 | 高 | 极高 | 严格分 1A-1D 子阶段；1A 仅做 AST-walking PoC 验证可行性 | 子阶段超期 50% |
| RM2 | 性能不达标（与 V8 差距 > 100x） | 高 | 高 | 优化优先级：IC > 隐藏类 > GC；接受性能折中 | bench 跑分低于 goja 30% |
| RM3 | 维护者疲劳，长期投入不足 | 高 | 极高 | 严格 scope；接受 P2 项长期不实现；社区化 | 连续 4 周无进展 |
| RM4 | TS 转译 corner case 多 | 中 | 中 | 严格按 TS conformance 测试；优先级递降 | conformance < 50% |
| RM5 | Go GC 与自管理堆冲突导致崩溃 | 中 | 高 | 用 `runtime.Pinner`；早期压力测试 | 长跑崩溃 |
| RM6 | npm 兼容性长尾多 | 中 | 中 | 不追求 100%；标注不兼容清单 | Top 100 通过率 < 60% |
| RM7 | 正则引擎性能/正确性 | 中 | 中 | 实现 PCRE 子集 + test262 regex 测试 | regex test262 < 90% |
| RM8 | Windows 平台行为差异 | 低 | 低 | CI 矩阵覆盖；抽象平台层 | Windows CI 失败 |
| RM9 | Bun.ffi 纯 Go 不可行 | 高 | 低 | 文档明确不实现；不阻塞发布 | — |
| RM10 | 法律风险（复制 Bun API） | 低 | 中 | API 行为不版权保护；代码原创 | 法律函告 |

### 15.2 应急预案

| 场景 | 预案 |
|------|------|
| Phase 1 子阶段严重延期（超期 50%+） | 暂停后续特性；评估是否降级为 AST-walking（不做字节码 VM） |
| 性能比 goja 还差 | 评估是否引入 `modernc.org/quickjs` 作为后备引擎（破坏 C2 约束但保项目） |
| 关键维护者离开 | 冻结功能开发；仅修严重 bug；推动社区接手 |
| 出现严重安全漏洞 | 紧急 hotfix；发布 `1.x.1`；公告 CVE |

---

## 附录 A：里程碑检查表

每个 Phase 完成时填写：

```markdown
## Phase X 完成检查

- [ ] 所有 WBS 任务完成
- [ ] 验收清单全部通过
- [ ] 测试覆盖率达标
- [ ] CI 在三端通过
- [ ] 性能基准达标
- [ ] 文档更新
- [ ] Release Notes 撰写
- [ ] 版本号升级
- [ ] 发布产物构建
- [ ] 已知问题列表更新
```

## 附录 B：变更日志

| 版本 | 日期 | 变更 |
|------|------|------|
| v0.1 | 2026-08-02 | 初稿，覆盖 Phase 0-8 全部规划 |
