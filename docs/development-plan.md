# Aluka 运行时 — 开发计划文档

> 项目代号：`aluka` ｜ 文档版本：v1.9 ｜ 日期：2026-08-04
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

### 2.0 当前完成状态评估

> 评估日期：2026-08-04 ｜ 测试总数：438 个（全部通过）

#### 总体进度概览

| Phase | 名称 | 状态 | 完成度 |
|-------|------|------|--------|
| 0 | 工程基座 | ✅ 完成 | 100% |
| 1A | AST-walking PoC | ✅ 完成 | ~90% |
| 1B | 字节码 VM | ⚠️ 大部分完成 | ~65% |
| 1C | ES2015 + 模块系统 | 🔨 进行中 | ~80% |
| 1D | TS 转译 + ES2017-2020 | 🔨 进行中 | ~95% |
| 2 | Node.js 核心内置模块 | 🔨 进行中 | ~50% |
| 3-8 | 后续阶段 | ❌ 未开始 | 0% |

#### Phase 0：工程基座 — ✅ 完成

| ID | 任务 | 状态 | 说明 |
|----|------|------|------|
| 0.1 | Go module 与目录骨架 | ✅ | `go.mod` + 完整目录树 |
| 0.2 | `.golangci.yml` / `.editorconfig` | ✅ | 配置文件就绪 |
| 0.3 | CLI 入口 `cmd/aluka/main.go` | ✅ | 支持 `-e`/`--version`/`--help`/`run`/`--ast`/`--vm` |
| 0.4 | 引擎抽象层接口 | ✅ | `Engine`/`Context`/`Value`/`Object` 接口（含 `Delete` 方法） |
| 0.5 | 桩引擎 → 已升级为真实引擎 | ✅ | AST 解释器 + 字节码 VM 双引擎 |
| 0.6 | `console.log/error/warn/info` | ✅ | `internal/runtime/globals/console.go` |
| 0.7 | `process.argv`/`process.env` | ✅ | `internal/runtime/globals/process.go` |
| 0.8 | GitHub Actions CI | ✅ | lint + test + build |
| 0.9 | Makefile | ✅ | `make build`/`test` 可用 |
| 0.10 | README.md | ✅ | 项目简介 + 构建 + 使用 |

#### Phase 1A：AST-walking PoC — ✅ 完成（~90%）

| ID | 任务 | 状态 | 说明 |
|----|------|------|------|
| 1A.1 | Lexer | ✅ | 支持 ES5 + ES2015 token 集（含模板字符串、箭头函数、spread/rest）；未覆盖完整 ES2023 |
| 1A.2 | Parser（递归下降 + Pratt） | ✅ | ES5 + ES2015 核心语法 |
| 1A.3 | AST 节点类型 | ✅ | 完整节点定义，含解构模式（`ArrayPattern`/`ObjectPattern`） |
| 1A.4 | AST-walking 解释器 | ✅ | `internal/engine/interpreter/interpreter.go` |
| 1A.5 | 基本内置对象 | ✅ | Object/Array/String/Number/Boolean/Math/JSON（方法覆盖良好） |
| 1A.6 | Error 体系 | ✅ | Error/TypeError/RangeError/SyntaxError/ReferenceError |
| 1A.7 | test262 ES5 子集 ≥ 50% | ❌ | 未集成 test262 |

#### Phase 1B：字节码 VM — ⚠️ 大部分完成（~60%）

| ID | 任务 | 状态 | 说明 |
|----|------|------|------|
| 1B.1 | 字节码指令集设计 | ✅ | `internal/engine/bytecode/opcodes.go`，含 `OpDelProp`/`OpMakeClass`/`OpCallThis` 等 65+ 指令 |
| 1B.2 | Compiler（AST → Bytecode） | ✅ | `internal/engine/compiler/compiler.go` |
| 1B.3 | VM（栈式执行） | ✅ | `internal/engine/interpreter/vm.go` |
| 1B.4 | 闭包环境（upvalue） | ✅ | 完整 upvalue 捕获链（local → upvalue → global 解析） |
| 1B.5 | 隐藏类 + 内联缓存 | ❌ | 未实现 |
| 1B.6 | GC（arena + 三色标记-清除） | ❌ | 依赖 Go runtime GC，未自研 GC |
| 1B.7 | 性能基准 fib(30) ≥ goja 30% | ⚠️ | `bench/fib_test.go` 存在，未与 goja 对比验证 |
| 1B.8 | test262 ES5 ≥ 60% | ❌ | 未集成 test262 |

#### Phase 1C：ES2015 + 模块系统 — 🔨 进行中（~25%）

| ID | 任务 | 状态 | 说明 |
|----|------|------|------|
| 1C.1 | `let`/`const`/块级作用域 | ✅ | parser + compiler + VM 完整支持 |
| 1C.2 | 箭头函数 + `this` 绑定 | ✅ | 词法 this 捕获 |
| 1C.3 | `class` 语法 | ✅ | **完成**：class 声明/表达式、继承（extends）、super 调用/方法、静态方法、getter/setter、默认构造函数、instanceof |
| 1C.4 | `Promise` + microtask 队列 | ✅ | **完成**：`Promise` 构造器（executor + resolve/reject）、`then`/`catch`/`finally`（链式调用、值穿透、错误冒泡）、`Promise.resolve`/`Promise.reject`、`Promise.all`/`race`/`allSettled`（支持迭代器输入）、微任务队列（`runModule` 后排水）、`queueMicrotask`、`instanceof` 支持自定义值类型原型链 |
| 1C.5 | `Symbol`/`Map`/`Set`/`WeakMap`/`WeakSet` | ✅ | **完成**：`Symbol()`（+ `Symbol.for`/`keyFor` 全局注册表 + `hasInstance`/`toPrimitive`/`toStringTag` well-known symbols）、`Map`（get/set/has/delete/clear/forEach/keys/values/entries/size getter/`[Symbol.iterator]`/构造器支持 iterable/对象键/SameValueZero）、`Set`（add/has/delete/clear/forEach/keys/values/entries/size/`[Symbol.iterator]`/构造器支持 iterable/去重）、`WeakMap`（get/set/has/delete，键必须为对象）、`WeakSet`（add/has/delete，值必须为对象） |
| 1C.6 | 迭代器协议 + `for...of` + 生成器 | ✅ | **完成**：`Symbol.iterator` 协议、`for...of`（数组/字符串/生成器/自定义迭代器）、`function*`/`yield`/`yield*`、生成器 `.next()`/`.return()`/`.throw()`、`[Symbol.iterator]()` 自定义迭代器、展开运算符支持迭代器协议 |
| 1C.7 | 模板字符串 / 解构 / 默认参数 / rest/spread | ✅ | **全部完成**：模板字符串、数组/对象解构（含 rest/default/嵌套）、默认参数、rest/spread 参数与调用、`delete` 运算符 |
| 1C.8 | `Proxy`/`Reflect` | ✅ | **完成**：`Proxy` 构造器（get/set/has/deleteProperty/ownKeys/getPrototypeOf/Symbol.hasInstance trap）、`Proxy.revocable`、`Reflect` 全局对象（get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/apply/construct/defineProperty/getOwnPropertyDescriptor/isExtensible/preventExtensions）；VM 拦截 `getProperty`/`setProperty`/`inOp`/`instanceof`/`getProto`/`OpDelProp`/`OpGetProto`/`OpSpreadObject` 分发到 trap；`Interpreter.currentVM` 为 native 回调提供 VM 上下文；修复 `inOp` 对普通对象的原型链键存在性检查 |
| 1C.9 | ESM 加载器 | ✅ | **完成**：ESM `import`/`export` 语法解析（AST 节点 + parser）、AST→CJS 转换（默认/命名/命名空间导入、重导出、`export *`、`export default`）、`EvalProgram` 直接执行预转换 AST |
| 1C.10 | CJS 加载器 | ✅ | **完成**：`require()`/`module.exports`/`exports`/`__filename`/`__dirname`、模块缓存 + 循环依赖处理（预填充 cache）、嵌套 require 的全局变量 save/restore、JSON 模块加载 |
| 1C.11 | Node.js 模块解析算法 | ✅ | **完成**：相对/绝对路径解析、扩展名补全（`.ts`/`.mts`/`.cts`/`.js`/`.mjs`/`.cjs`/`.json`）、目录解析（`package.json` main 字段 + index 文件）、`node_modules` 逐级向上查找、package.json `type` 字段判定模块类型 |
| 1C.12 | `tsconfig.json` 读取 | ✅ | **完成**：新增 `tsconfig.go`，`tsconfigCache` 沿模块目录树向上查找 `tsconfig.json`（回退 `jsconfig.json`）并缓存解析结果；解析 `compilerOptions.baseUrl`/`paths`；支持 jsonc 格式（`//` 行注释与 `/* */` 块注释容错剥离） |
| 1C.13 | 路径别名（`paths`/`baseUrl`） | ✅ | **完成**：`Resolver.resolvePaths` 实现 TypeScript paths 匹配规则——通配符 `*` key 映射（`@/* → src/*`，提取匹配片段替换 target 中的 `*`）、精确匹配、多 target 顺序尝试、最长匹配优先；`baseUrl` 单独作用时 bare specifier 相对 baseUrl 解析；别名未匹配时回退到 node_modules 查找；ESM `import` 与 CJS `require` 均支持；顺带补全 TS 扩展名解析（`.ts`/`.mts`/`.cts` 加入 Extensions/IndexNames，`.ts` 按 ESM 处理走类型剥离转译） |
| 1C.14 | 字节码缓存 | ✅ | **完成**：实现磁盘字节码缓存，命中时跳过 parse+compile。新增 `engine/const_codec.go`（常量池编解码器，支持 number/string/bigint 三种类型，`*big.Int` 用十进制字符串往返）、`bytecode/serialize.go`（`Module` 的二进制序列化/反序列化，含 `FormatVersion` 版本号 + `ALUKABC1` magic header + FuncTemplate/ClassTemplate/TryTable/Upvalues/LineStarts 全字段）；VM 新增公开方法 `Compile`/`CompileAST`/`RunModule`（拆分编译与执行）；新增 `module/bc_cache.go`（缓存键 = `sha256(绝对路径+mtime+size+格式版本)`，存储于 `node_modules/.aluka/cache/` 下按路径哈希分目录，容错处理所有 I/O 与反序列化错误）；`cjs.go`/`esm.go` 的加载流程接入 `compileOrLoad` 闭包（CJS 编译源码、ESM 编译转换后 AST）；`Loader.SetNoCache` + CLI `--no-cache` 标志强制重编译 |
| 1C.15 | test262 ES5+ES2015 ≥ 60% | ❌ | 未集成 |

#### Phase 1D：TS 转译 + ES2017-2020 — 🔨 进行中（~65%）

| ID | 任务 | 状态 | 说明 |
|----|------|------|------|
| 1D.1 | TS 类型注解剥离 | ✅ | **完成**：`parseTypeAnnotation`/`skipType`/`skipTypeInner`/`skipTypeAtom`/`skipTypePrefix` 递归跳过 TS 类型表达式（联合/交叉/条件/泛型/对象/元组/映射类型）；变量声明、函数参数（含 `?`/默认值/rest）、返回值、类字段处剥离类型注解；类字段（`x: T = init`）在 compiler 中注入构造函数初始化（实例字段→构造函数体首部，静态字段→`OpMakeClass` 后 `OpSetPropTop`） |
| 1D.2 | `interface`/`type` 别名声明删除 | ✅ | **完成**：`parseInterfaceDecl` 跳过 `interface Name<T> extends ... { members }` 返回 `EmptyStmt`；`parseTypeAliasDecl` 跳过 `type Name<T> = TypeExpr;` 返回 `EmptyStmt` |
| 1D.3 | `enum` 转换 | ✅ | **完成**：`parseEnumDecl` 将 `enum Name { A, B = 10, C = 'x' }` 降级为 IIFE（`var Name; (function(Name) { Name["A"]=0; Name[0]="A"; ... })(Name || (Name = {}))`）；数字枚举自动递增 + 反向映射；字符串枚举无反向映射；混合枚举支持 |
| 1D.4 | `namespace` 转换 | ✅ | **完成**：`parseNamespaceDecl` 将 `namespace Name { export function/const/class ... }` 降级为 IIFE（`var Name; (function(Name) { Name.x = ...; })(Name || (Name = {}))`）；`transformExportedDecl` 将 `export var/function/class` 转为 `ns.Name = ...` 赋值；非导出声明保持 IIFE 内局部作用域 |
| 1D.5 | 装饰器（解析后跳过） | ✅ | **完成**：`skipDecorators` 解析并丢弃 `@expr`/`@expr(args)`/`@foo.bar` 装饰器；在 `parseStatement`（类声明前）、`parseExportDecl`（`export @dec class`）、`parseClassMember`（方法/字段前）三处调用；lexer 新增 `@` 为单字符标点 |
| 1D.6 | `as`/`satisfies` 断言剥离 | ✅ | **完成**：`parseConditional` 循环消费 `expr as T`/`expr satisfies T`/链式 `expr as unknown as T`/`expr as const`，仅保留 `expr` |
| 1D.7 | 泛型参数删除 | ✅ | **完成**：`skipTypeParameters` 在函数/类声明处跳过 `<T, U extends X, R = D>`；`trySkipTypeArgs` 在调用表达式和 `new` 表达式处回溯式跳过 `<T>(args)`（区分 `<` 为泛型参数 vs 小于号）；`skipAngleBraces` 处理 `>>`/`>>>` 嵌套泛型 token 拆分 |
| 1D.8 | `import type`/`export type` 删除 | ✅ | **完成**：`import type ...` 整体擦除为 `EmptyStmt`；`export type { ... }`/`export type * from 'mod'` 擦除为 `EmptyStmt`；内联 `{ type X, Y }` 逐个 specifier 擦除（import 和 export 均支持） |
| 1D.9 | `async`/`await` | ✅ | **完成**：`async function` 声明/表达式、`async` 箭头函数、`async` 类/对象方法、`await` 表达式（值/Promise/thenable）；新增 `async.go`（`asyncRunner` 复用生成器式帧挂起，集成 Promise 微任务调度）、`OpAwait` 指令、`FuncTemplate.IsAsync` 标志、`AwaitExpr` AST 节点；parser 新增 `asyncStack` 跟踪异步作用域以正确解析 `await`；错误传播保留原始值（`normalizeException` 处理 `*jsThrow`/`engine.Value`/`error`），rejected promise 经 `await` 抛入帧可被 `try/catch` 捕获；async 函数返回值自动 Promise 包装（resolve 采用 thenable） |
| 1D.10 | `for await...of` / rest in object / `Promise.finally` | ✅ | **完成**：`for await...of`（ES2018 异步迭代协议）已实现——`parseFor` 识别 `for await` 关键字（仅 async 函数内合法，否则语法错误）并设置 `ForOfStmt.IsAwait`；compiler 在 `isAwait` 时改用 `OpGetAsyncIterator` 获取迭代器、并在每次 `iter.next()` 后插入 `OpAwait` 解包 Promise；新增 `OpGetAsyncIterator` 字节码指令与 `VM.getAsyncIterator`（优先 `[Symbol.asyncIterator]()`，回退到 `[Symbol.iterator]()`——后者配合 OpAwait 的 `promiseResolve` 包装实现自动回退）；对象 rest 解构已实现；`Promise.finally` 已实现（1C.4） |
| 1D.11 | ES2019：`Array.flat/flatMap`/`Object.fromEntries`/`trimStart` | ✅ | **完成**：`Array.prototype.flat/flatMap`（支持深度参数与 `Infinity`）、`Object.fromEntries`（支持数组/Map/通用可迭代对象输入）；`trimStart/trimEnd` 已在 1D 前期随 String 方法补齐。顺带补全了一批 ES5/ES2015 基础数组方法（`splice/sort/find/findIndex/some/every/reduceRight/fill/copyWithin`）、迭代器方法（`keys/values/entries`）、ES2022/ES2023（`findLast/findLastIndex/at`）、Array 静态方法（`from/of`）、Object 静态方法族（`create/defineProperty/defineProperties/getOwnPropertyDescriptor(s)/getOwnPropertyNames/is/hasOwn/setPrototypeOf`）、Math 缺失方法与常量（`sign/trunc/cbrt/log1p/expm1/sinh/cosh/tanh/asinh/acosh/atanh/asin/acos/atan/atan2/fround/imul/clz32` + `LOG2E/LOG10E/SQRT1_2`） |
| 1D.12 | ES2020：可选链 `?.`/空值合并 `??`/`BigInt`/`Promise.allSettled` | ✅ | **完成**：空值合并 `??`、可选链 `?.`（成员访问 `a?.b`/计算属性 `a?.[b]`/可选调用 `a?.()`/短路）、`Promise.allSettled` 均已实现；**BigInt**（ES2020）完成——新增 `TypeBigInt` 值类型与 `bigIntValue`（`math/big.Int` 包装，`Float()`/`Int()` 返回 `(0,false)` 阻断 float 路径）；lexer 新增 `TokenBigInt` 与 `n` 后缀检测（支持 `123n`/`0xFFn`/`0o17n`/`0b1010n`/`1_000n`）；AST 新增 `BigIntLit`；compiler 走常量池 `AddConst(engine.BigInt)`；新增 `bigint_ops.go` 集中实现算术（`+ - * / % **`，整除向零截断）、位运算（`& | ^ << >>`，不支持 `>>>`）、比较（BigInt vs BigInt/Number 用 `big.Float` 精确比较）、严格/宽松相等（`5n == 5` 为 true、`5n === 5` 为 false）；vm.go 各算术/位运算 case 加 BigInt 分发；混合 BigInt+Number 算术抛 TypeError；`typeof 123n === "bigint"` 自动工作 |
| 1D.13 | 动态 `import()` | ✅ | **完成**：采用 parser 层 lower 方案（无需新 opcode/AST 节点/compiler 分支）——parser 在语句分发处 peek `import` 后是否紧跟 `(`，若是则走表达式路径；`parsePrimary` 新增 `import` case 把 `import(spec)` 直接 lower 成对内置全局 `__import(spec)` 的 `CallExpr`。`Loader.makeImportFunc` 复用 `require()` 的同步加载链路（`Loader.require`，自动按 CJS/ESM/JSON 分发、缓存、处理循环依赖），再用全局 `Promise.resolve`/`Promise.reject` 把结果包装成已 settled 的 Promise（通过 `engine.Function.Call` 调用静态方法，避免 module→interpreter 循环依赖）。`setGlobals`/`saveGlobals`/`restoreGlobals` 扩展处理 `__import` 全局（与 `require` 同样的 parentPath 闭包，相对路径基于发起模块解析）。支持 `await import(...)`、`.then` 链式、命名/默认导出访问、加载失败返回 rejected Promise |
| 1D.14 | TS conformance ≥ 50% | ❌ | 未集成 |
| 1D.15 | REPL 基础 | ✅ | **完成**：新增 `cmd/aluka/repl.go`——交互式读取-求值-打印循环。采用"累积重放"状态保持方案（每次新输入完整后 Eval 全部历史代码 + 新输入，使 `var`/`function`/`class` 声明跨输入持久；副作用重复执行的已知限制对 REPL 场景可接受）；多行输入检测（`isInputComplete` 跟踪括号/大括号/方括号/单双引号/模板字符串/行注释/块注释的平衡状态，未闭合时显示续行提示符 `.`）；错误恢复（语法错误打印后不终止会话，且不累积出错输入避免错误放大）；表达式结果自动打印（非 undefined/null 时）；点命令支持（`.help`/`.exit`/`.version`）；EOF(Ctrl+D) 退出 |

#### 已实现的 ES 特性清单

以下特性已在 parser + compiler + VM 中完整实现并通过测试：

- **ES5 核心**：变量声明、函数声明/表达式、闭包、`if`/`for`/`while`/`do-while`/`switch`、`try/catch/finally`、`throw`、`break`/`continue`（含 label）、`typeof`/`void`/`delete`/`in`/`instanceof`
- **ES5 内置**：Object/Array/String/Number/Boolean/Math/JSON/Error 方法（Array 含 `splice/sort/find/findIndex/some/every/reduce/reduceRight/fill/copyWithin/indexOf/includes/forEach/map/filter/concat/slice/join/keys/values/entries`；Object 含 `create/assign/keys/values/entries/fromEntries/freeze/is/hasOwn/getPrototypeOf/setPrototypeOf/defineProperty/defineProperties/getOwnPropertyDescriptor(s)/getOwnPropertyNames`；Math 含 `abs/floor/ceil/round/trunc/sign/sqrt/pow/max/min/random/cbrt/log/log1p/log2/log10/exp/expm1/sinh/cosh/tanh/asinh/acosh/atanh/sin/cos/tan/asin/acos/atan/atan2/fround/imul/clz32` + 全部常量）
- **ES2015**：`let`/`const`/块级作用域、箭头函数、模板字符串、解构赋值（数组+对象，含 holes/rest/default/嵌套）、默认参数、rest/spread 参数、`for...of`、spread 展开（数组+对象）、空值合并 `??`、`class` 语法（声明/表达式/继承/super/static/getter/setter/默认构造函数）、`Promise`（构造器 + then/catch/finally 链式调用 + resolve/reject/all/race/allSettled + microtask 队列）、`Symbol`（+ for/keyFor + hasInstance/toPrimitive/toStringTag）、`Map`/`Set`/`WeakMap`/`WeakSet`（完整原型方法 + 迭代器协议 + 构造器 iterable 输入）、`Proxy`（get/set/has/deleteProperty/ownKeys/getPrototypeOf/Symbol.hasInstance trap + revocable）、`Reflect`（get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/apply/construct/defineProperty/getOwnPropertyDescriptor）、`Array.from`/`Array.of`/`Array.isArray`
- **ES2017**：`async`/`await`（`async function` 声明/表达式、`async` 箭头函数、`async` 类/对象方法、`await` 值/Promise/thenable、错误经 `try/catch` 捕获、返回值自动 Promise 包装）
- **ES2018**：`for await...of`（异步迭代协议；`Symbol.asyncIterator` 优先、回退 `Symbol.iterator` + Promise 包装；支持 `break`/`try-catch` 捕获 rejected next、解构绑定；仅 async 函数内合法）、对象 rest 解构（`let {a, ...rest} = obj`）
- **ES2019**：`Array.prototype.flat`/`flatMap`（支持深度参数与 `Infinity`）、`Object.fromEntries`（数组/Map/通用可迭代对象输入）、`Array.prototype.flat` 的迭代器消费、`String.prototype.trimStart`/`trimEnd`、可选 catch 绑定（`catch {}` 无参数）
- **ES2020**：可选链 `?.`（成员访问 `a?.b`、计算属性 `a?.[b]`、可选调用 `a?.()`、方法调用 `a?.b()`/`a.b?.()`、深层链短路、`this` 绑定保持）、动态 `import()`（运行时按 specifier 加载 CJS/ESM/JSON 模块，返回 `Promise<module namespace>`；支持 `await import(...)`、相对路径基于发起模块解析、加载失败返回 rejected Promise）
- **ES2021**：数字分隔符（`1_000_000`、`0xFF_FF`、`0o7777_7777`、`0b1010_1010`，含小数/指数部分 `1_000.500_25`）、逻辑赋值运算符（`||=`/`&&=`/`??=`，含短路语义与左值一次性求值）、`String.prototype.replaceAll`（随 String 方法补齐）
- **ES2022/ES2023**：`Object.hasOwn`、`Array.prototype.at`、`Array.prototype.findLast`/`findLastIndex`、Error cause（`new Error("msg", {cause})` → `err.cause`）、Hashbang 语法（`#!/usr/bin/env aluka` 脚本首行）
- **TypeScript 转译**：类型注解剥离（变量/参数/返回值/类字段）、`interface`/`type` 别名声明擦除、`enum` 降级（数字自动递增 + 反向映射 / 字符串枚举 / 混合枚举）、`namespace` 降级为 IIFE（导出声明转 `ns.Name = ...`）、装饰器解析后丢弃（`@dec`/`@dec(args)`/`@foo.bar`）、`as`/`satisfies`/`as const` 断言剥离、泛型参数删除（函数/类声明 + 调用表达式类型实参）、`import type`/`export type` 擦除（含内联 `{ type X }` specifier）、类字段初始化（实例字段注入构造函数 + 静态字段 `OpSetPropTop`）
- **其他**：`delete` 运算符（实际删除属性）、labeled 语句、函数方法（call/apply/bind）

#### 已知缺失的关键特性

1. ~~模块系统 ESM/CJS（1C.9-1C.11）— 阻塞多文件项目~~ ✅ 已完成
2. ~~可选链 `?.`（1D.12）— 常用语法~~ ✅ 已完成
3. ~~TS 转译（1D.1-1D.8）— 阻塞 `.ts` 文件运行~~ ✅ 已完成
4. ~~ES2019 `Array.flat/flatMap`/`Object.fromEntries`（1D.11）~~ ✅ 已完成
5. ~~`for await...of`（1D.10）— 异步迭代器~~ ✅ 已完成
6. ~~`BigInt`（1D.12）— ES2020 大整数~~ ✅ 已完成（ES2020 P0 特性全部齐备）
7. ~~动态 `import()`（1D.13）— 模块系统已就绪，待接入~~ ✅ 已完成
8. 隐藏类 + IC（1B.5）和自研 GC（1B.6）— 影响性能
9. 普通函数 `this` 绑定：`Array.prototype.find/map` 等的 `thisArg` 第二参数未对非箭头函数生效（引擎既有缺陷，箭头函数闭包可绕过）
10. CJS/ESM interop：`module.exports = func` 整体赋值的 CJS 模块，动态 import 返回的 namespace 不额外包装 `.default`（当前直接返回 exports，简化 interop）
11. ~~顶层 try/catch 既有缺陷~~ ✅ 已修复（v1.3，根因为 `compileStmtValue` 缺 TryStmt 分支 + `findHandlerInFrame` 未跳过 phase==1 handler 导致 rethrow 无限循环）
12. lexer 正则/除法歧义：表达式语句开头的 `/` 在某些上下文被误判为正则字面量起始（如 `10n / 3n` 作为语句开头），需用变量或 `console.log` 包装绕过

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
| 2.1 | 设计模块注册机制（`builtin.Register("fs", NewFS)`） | ✅ `internal/builtin/registry.go` |
| 2.2 | 实现 `console` 完整版（`table`/`group`/`time`/`dir`/`trace`/`assert`） | `internal/runtime/globals/console.go` |
| 2.3 | 实现 `process` 完整版（`stdout`/`stderr`/`stdin`/`on`/`kill`/`exit`/`hrtime`） | `internal/runtime/globals/process.go` |
| 2.4 | 实现 `Buffer`（基于 `[]byte`，Node API 完整） | `internal/runtime/globals/buffer.go` |
| 2.5 | 实现 `TextEncoder/Decoder`/`atob`/`btoa` | `internal/runtime/globals/encoding.go` |
| 2.6 | 实现 `URL`/`URLSearchParams`（WHATWG） | `internal/runtime/globals/url.go` |
| 2.7 | 实现 `AbortController`/`AbortSignal` | `internal/runtime/globals/abort.go` |
| 2.8 | 实现 `Event`/`EventTarget`/`CustomEvent` | `internal/runtime/globals/event.go` |
| 2.9 | 实现完整 Timers（`setTimeout`/`setInterval`/`setImmediate`/`queueMicrotask`） | ✅ `internal/runtime/globals/timers.go` |
| 2.10 | 实现 `node:fs`（sync/async/promises + `createReadStream/WriteStream`） | ✅ `internal/builtin/fs.go`（同步 API；async/promises 待补） |
| 2.11 | 实现 `node:path`（`posix`+`win32` 双实现） | ✅ `internal/builtin/path.go` |
| 2.12 | 实现 `node:os` | ✅ `internal/builtin/os.go` |
| 2.13 | 实现 `node:url`/`node:querystring` | ✅ `internal/builtin/url.go` + `internal/builtin/querystring.go` |
| 2.14 | 实现 `node:events`（`EventEmitter` 完整 API） | ✅ `internal/builtin/events.go` |
| 2.15 | 实现 `node:util`（`inspect`/`promisify`/`format`/`types`/`deprecate`） | ✅ `internal/builtin/util.go` |
| 2.16 | 实现 `node:assert`（strict + loose） | ✅ `internal/builtin/assert.go` |
| 2.17 | 实现 `node:stream`（`Readable`/`Writable`/`Duplex`/`Transform`/`pipeline`/`finished`） | ✅ `internal/builtin/stream.go` |
| 2.18 | 实现 `node:buffer`（与全局 `Buffer` 同源） | 复用 2.4 |
| 2.19 | 实现 `node:crypto`（`createHash`/`createHmac`/`randomBytes`/`scrypt`/`pbkdf2`/`createCipheriv`） | 🔶 `internal/builtin/crypto.go`（已实现 `createHash`/`randomBytes`；HMAC/对称加密/pbkdf2/scrypt 待补） |
| 2.20 | 实现 `node:string_decoder` | ✅ `internal/builtin/string_decoder.go` |
| 2.21 | 实现 `node:http`（基于 Go `net/http`，完整 API） | ✅ `internal/builtin/http.go` |
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
| v0.2 | 2026-08-03 | 新增 §2.0 当前完成状态评估；Phase 1C.7 解构赋值完成（含 `delete` 运算符修复）；`Object` 接口新增 `Delete` 方法；新增 `OpDelProp` 指令 |
| v0.3 | 2026-08-03 | Phase 1C.3 `class` 语法完成（声明/表达式/继承/super/static/getter/setter/默认构造函数）；新增 `OpMakeClass`/`OpGetProto`/`OpCallThis`/`OpConstructThis`/`OpCallThisArgs`/`OpConstructThisArgs` 指令；新增 `AccessorValue` 类型及 `ClassTemplate` 结构；修复 `OpSetPropTop`/`OpSetElemTop` 栈顺序 bug；测试总数 145→160 |
| v0.4 | 2026-08-03 | Phase 1C.4 `Promise` + microtask 完成：新增 `microtask.go`（微任务队列 enqueue/drain）和 `promise.go`（`PromiseValue` + then/catch/finally + resolve/reject/all/race/allSettled + queueMicrotask）；`runModule` 顶层执行后排水微任务；修复 upvalue 共享（`captureUpvalues` 复用现有 upvalue）和 `instanceof` 对自定义值类型（`PromiseValue`/`GeneratorValue`）的原型链查找（`VM.getProto`）；测试总数 173→198 |
| v0.5 | 2026-08-03 | Phase 1C.5 `Symbol`/`Map`/`Set`/`WeakMap`/`WeakSet` 完成：`Symbol` 增强（`Symbol.for`/`keyFor` 全局注册表 + `hasInstance`/`toPrimitive`/`toStringTag` well-known symbols）；新增 `map_set.go`（`MapValue`/`SetValue`/`WeakMapValue`/`WeakSetValue` + 完整原型方法 + 迭代器协议 + 构造器 iterable 输入 + SameValueZero 键相等）；`VM.backingObj` 统一处理自定义值类型的 accessor 查找（修复 `size` getter 不被调用的问题）；`getProto` 新增 Map/Set/WeakMap/WeakSet 分支；测试总数 198→240 |
| v0.6 | 2026-08-03 | Phase 1C.8 `Proxy`/`Reflect` 完成：新增 `proxy.go`（`ProxyValue` + get/set/has/deleteProperty/ownKeys/getPrototypeOf/Symbol.hasInstance trap + `Proxy.revocable`）和 `reflect.go`（`Reflect` 全局对象 + 12 个方法）；VM 拦截 `getProperty`/`setProperty`/`inOp`/`instanceof`/`getProto`/`OpDelProp`/`OpGetProto`/`OpSpreadObject` 分发到 Proxy trap；`Interpreter.currentVM` 为 native 回调提供 VM 上下文；`proxyGetSymbol` 传递实际 Symbol 值给 get trap（支持 `k === Symbol.hasInstance` 比较）；修复 `inOp` 对普通对象的原型链键存在性检查（`Get` 返回 `Undefined, nil` 导致 `in` 总是返回 true）；`Object.getPrototypeOf`/`Object.keys` 支持 Proxy；测试总数 240→264 |
| v0.7 | 2026-08-03 | Phase 1D.9 `async`/`await` 完成：新增 `async.go`（`asyncRunner` 复用生成器式帧挂起/恢复，集成 Promise 微任务调度）、`OpAwait` 指令、`FuncTemplate.IsAsync` 标志、`AwaitExpr` AST 节点；parser 新增 `asyncStack` 跟踪异步作用域以正确解析 `await`，支持 `async function` 声明/表达式、`async` 箭头函数、`async` 类/对象方法；`callClosure` 对 async 函数创建 `asyncRunner` 并返回 Promise；`normalizeException` 处理 `*jsThrow`/`engine.Value`/`error` 保留原始错误值，使 rejected promise 经 `await` 抛入帧可被 `try/catch` 捕获；async 函数返回值经 `promiseResolve` 自动采用 thenable；测试总数 264→290 |
| v0.8 | 2026-08-03 | Phase 1D.11 ES2019 内置方法补全 + 大规模内置对象方法补齐：新增 `array_methods.go`（`Array.prototype.splice/sort/find/findIndex/some/every/reduceRight/fill/copyWithin/keys/values/entries/flat/flatMap/findLast/findLastIndex/at` + `Array.from`/`Array.of`，含负索引、迭代器协议消费、`Infinity` 深度处理）、`object_methods.go`（`Object.create/defineProperty/defineProperties/getOwnPropertyDescriptor/getOwnPropertyDescriptors/getOwnPropertyNames/getOwnPropertySymbols/is/fromEntries/hasOwn/setPrototypeOf/seal/preventExtensions/isFrozen/isSealed/isExtensible`，含 `Object.is` 的 `NaN`/`±0` 同值相等语义、`fromEntries` 的数组/Map/通用迭代器输入）、`math_methods.go`（`sign/trunc/cbrt/log1p/expm1/sinh/cosh/tanh/asinh/acosh/atanh/asin/acos/atan/atan2/fround/imul/clz32` + `LOG2E/LOG10E/SQRT1_2` 常量）；新增测试文件 3 个（`array_methods_test.go`/`object_methods_test.go`/`math_methods_test.go`）共 27 个测试函数；修复 `flat(Infinity)` 因 `numberValue.Int()` 对 `+Inf` 返回垃圾值导致深度计算错误（改为优先用 `Float()` 并 `math.IsInf` 判定）；测试总数 345→372 |
| v0.9 | 2026-08-03 | Phase 1D.10 `for await...of`（ES2018 异步迭代协议）完成：`parseFor` 在 `for` 与 `(` 之间识别 `await` 关键字（仅 async 函数内合法，否则语法错误），经 `parseForOf` 的 `isAwait` 参数设置既有但从未使用的 `ForOfStmt.IsAwait` 字段；compiler `compileForOf` 接受 `isAwait` 标志，在异步路径上改用新指令 `OpGetAsyncIterator` 获取迭代器、并在每次 `OpCallMethod("next")` 后插入 `OpAwait` 以解包 next() 返回的 Promise；新增 `OpGetAsyncIterator` 字节码指令（`opcodes.go` 枚举 + 反汇编名 `GET_ASYNC_ITERATOR`，无操作数）与 `VM.getAsyncIterator`（优先 `[Symbol.asyncIterator]()` 方法，回退到 `getIterator` 的 `Symbol.iterator` 路径——回退场景下 next() 返回普通对象，由 OpAwait 的 `promiseResolve` 自动包装）；复用既有 async/await 的 `asyncRunner` 挂起-恢复机制（`tmpIter`/`tmpResult` 作为真实栈槽在 await 期间保留）；新增 `for_await_test.go` 7 个测试（手写 async iterable、回退到数组/字符串同步迭代器、`Promise.reject` 经 await 抛入被 try/catch 捕获、break 提前退出、解构绑定、非 async 上下文语法错误）；测试总数 372→379 |
| v1.0 | 2026-08-03 | Phase 1D.13 动态 `import()`（ES2020）完成：采用 parser 层 lower 方案（最小改动，无新 opcode/AST 节点/compiler 分支）——parser 语句分发处对 `import` 关键字 peek 下一个 token，若紧跟 `(` 则判定为动态调用，跳出声明路径走表达式语句；`parsePrimary` 新增 `import` case，将 `import(specifier)` 直接 lower 成对内置全局 `__import(specifier)` 的 `ast.CallExpr`（复用现有 CallExpr 编译链路）；`Loader.makeImportFunc`（`loader.go`）复用 `require()` 的同步加载入口 `Loader.require`（自动按 CJS/ESM/JSON 分发、缓存、处理循环依赖），再用全局 `Promise.resolve`/`Promise.reject` 静态方法把结果包装成已 settled 的 Promise（通过 `engine.Function.Call` 调用，避免 module→interpreter 循环依赖）；`cjs.go` 的 `setGlobals`/`saveGlobals`/`restoreGlobals` 扩展处理 `__import` 全局（与 `require` 同样的 parentPath 闭包，相对路径基于发起模块自身路径解析）；新增 `dynamic_import_test.go` 8 个测试（CJS 命名/默认导出、ESM 命名+默认+命名空间、JSON 模块、`await import`、`instanceof Promise` 断言、加载失败 rejected Promise、子目录相对路径解析）；测试总数 379→387 |
| v1.1 | 2026-08-03 | Phase 1C.12 `tsconfig.json` 读取 + 1C.13 路径别名 `paths`/`baseUrl` 完成：新增 `tsconfig.go`——`tsconfigCache` 沿模块目录树向上查找 `tsconfig.json`（回退 `jsconfig.json`）并按目录缓存解析结果（`sync.Mutex` 保护），解析 `compilerOptions.baseUrl`/`paths` 字段；`stripJSONC` 实现 jsonc 容错（剥离 `//` 行注释与 `/* */` 块注释，正确处理字符串字面量内的注释符号）；`Resolver.resolvePaths` 实现 TypeScript paths 匹配规则——通配符 `*` key 映射（`@/* → src/*`，从 specifier 提取匹配片段替换 target 中的 `*`）、精确匹配（无通配符）、多 target 顺序尝试、最长 key 匹配优先；`baseUrl` 单独作用时（无 paths）bare specifier 相对 baseUrl 解析；`Resolver.Resolve` 在 bare specifier 路径上先尝试 paths 别名候选，失败后回退到 `resolveBare`（node_modules 查找）；ESM `import` 与 CJS `require` 均自动走别名解析；顺带补全 TS 扩展名解析（`Extensions`/`IndexNames` 加入 `.ts`/`.mts`/`.cts`，`ModuleType` 将 `.ts`/`.mts` 归为 ESM 走类型剥离转译）；新增 `tsconfig_test.go` 8 个测试（通配符别名、多别名+精确匹配、baseUrl-only、子目录向上查找命中根 tsconfig、jsconfig 回退、jsonc 注释容错、回退 node_modules、CJS require 别名）；测试总数 387→395 |
| v1.2 | 2026-08-03 | 一批 ES2019-ES2023 语法特性修复与新增：**数字分隔符（ES2021）修复**——`lexer.go readNumber` 重写，新增 `readDigitsWithSep` 统一辅助函数，使 `0x`/`0o`/`0b` 字面量及十进制小数/指数部分均支持下划线分隔符（`0xFF_FF`、`0o7777_7777`、`0b1010_1010`、`1_000.500_25`）；**逻辑赋值运算符（ES2021）**——三层实现：`token.go multiPuncts` 添加 `||=`/`&&=`/`??=`（排在对应 2 字符形式之前保证最长匹配）、`parser.go assignOps` 添加三个运算符、`compiler.go` 新增 `compileLogicalAssign` 用 `OpJmpTrueKeep`/`OpJmpFalseKeep`/`OpJmpNullishKeep` 实现短路语义（左值只求值一次，复用 Keep 跳转指令的"满足条件保留栈顶、不满足则 pop"语义）；**Error cause（ES2022）**——`setupErrorCtors` 构造器读取第二参数 options 对象的 `cause` 属性并设置到错误对象；**确认已实现**：可选 catch 绑定（ES2019）、Hashbang（ES2023）；发现并记录**顶层 try/catch 既有缺陷**（顶层代码的 catch 参数 `e` 为 undefined，函数体内正常）；新增 `modern_syntax_test.go` 6 个测试（数字分隔符、Error cause、`||=`/`&&=`/`??=` 含短路与成员表达式）；测试总数 395→401 |
| v1.3 | 2026-08-03 | 顶层 try/catch 缺陷修复 + BigInt（ES2020）完成：**顶层 try/catch 修复**——根因有二：(1) `compileStmtValue` 缺少 `TryStmt` 分支导致顶层最后一条语句为 try 时不产生返回值（新增 `compileTryValue` 值模式编译 try/catch/finally 块）；(2) `findHandlerInFrame` 未跳过 `phase==1`（已进 catch）的 handler，导致 catch 块内 rethrow 重新匹配同一 handler 形成无限循环（修复为 `phase >= 1` 时弹出该 handler 继续向上搜索）；新增 `try_catch_test.go` 6 个测试覆盖顶层 try/catch/finally 返回值与 rethrow；**BigInt（1D.12）完成**——新增 `TypeBigInt` 值类型与 `bigIntValue`（`math/big.Int` 包装，`Float()`/`Int()` 返回 `(0,false)` 阻断 float 路径）；lexer 新增 `TokenBigInt` 与 `n` 后缀检测（`123n`/`0xFFn`/`0o17n`/`0b1010n`/`1_000n`）；AST 新增 `BigIntLit`；compiler 走常量池；新增 `bigint_ops.go` 集中实现算术（`+ - * / % **`，整除向零截断、除零抛 RangeError）、位运算（`& | ^ << >>`，不支持 `>>>` 抛 TypeError）、比较（BigInt vs BigInt/Number 用 `big.Float` 精确比较）、严格/宽松相等（`5n == 5` 为 true、`5n === 5` 为 false）；vm.go 各算术/位运算 case 加 BigInt 分发；`typeof 123n === "bigint"` 自动工作；混合 BigInt+Number 算术抛 TypeError；新增 `bigint_test.go` 6 个测试；测试总数 401→413（**ES2020 P0 特性全部齐备**） |
| v1.4 | 2026-08-04 | Phase 1C.14 字节码缓存完成：实现磁盘字节码缓存，命中时跳过 parse+compile。**engine 层**新增 `const_codec.go`（常量池编解码器 `EncodeConst`/`DecodeConst`，支持 number/string/bigint 三种类型——经穷举验证 compiler 的 `AddConst`/`AddStringConst` 仅注入这三种原始类型，无闭包/原生引用污染；`*big.Int` 用十进制字符串往返；类型标签 + uvarint 长度前缀的紧凑二进制格式）；**bytecode 层**新增 `serialize.go`（`Serialize`/`Deserialize` 全量序列化 `Module`，含 `FormatVersion=1` 版本号 + `ALUKABC1` magic header + FuncTemplate 全字段 Name/NumParams/NumLocals/IsVarArgs/IsGenerator/IsAsync/Code/Constants/Upvalues/TryTable/LineStarts + ClassTemplate 全字段 HasSuper/CtorIdx/Methods）；**VM 层**新增公开方法 `Compile`（parse+compile，不执行）、`CompileAST`（编译预解析 AST）、`RunModule`（执行已编译 Module，导出原 `runModule`）；**module 层**新增 `bc_cache.go`（`bytecodeCache` 磁盘缓存——缓存键 = `sha256(绝对路径+mtime.UnixNano+size+FormatVersion)`，存储于 `node_modules/.aluka/cache/<2位hash>/<key>.bc`，所有 I/O 与反序列化错误容错处理不阻塞运行）；`cjs.go`/`esm.go` 加载流程接入 `compileOrLoad` 闭包（CJS 编译源码闭包、ESM 编译转换后 AST 闭包）；**CLI** 新增 `--no-cache` 标志（`Loader.SetNoCache`）；新增 `serialize_test.go` 6 个测试（序列化往返：空 Module/常量池/Code+TryTable/ClassTemplate/bad magic/版本不匹配）+ `bc_cache_test.go` 4 个测试（写盘+命中/源文件变更失效/--no-cache 禁用/ESM 缓存）；测试总数 413→423 |
| v1.5 | 2026-08-04 | Phase 1D.15 REPL 完成：新增 `cmd/aluka/repl.go`——交互式读取-求值-打印循环，`aluka repl` 子命令启动。**状态保持**采用累积重放方案（每次新输入完整后 Eval 全部历史代码 + 新输入，使 `var`/`function`/`class` 声明跨输入持久——绕过顶层 var 是模块局部而非全局的限制；副作用重复执行的已知限制对 REPL 场景可接受；错误输入不累积以避免放大）；**多行输入检测**（`isInputComplete` 跟踪 `()`/`{}`/`[]`/单双引号/模板字符串/`//`行注释/`/* */`块注释的平衡状态，未闭合时显示续行提示符 `.`）；**错误恢复**（语法错误打印到 stderr 后继续会话）；**表达式结果打印**（非 undefined/null 时自动打印）；**点命令**（`.help`/`.exit`/`.version`）+ EOF(Ctrl+D) 退出；help 文本更新 `repl` 子命令状态；**Phase 1 所有 WBS 任务基本完成** |
| v1.6 | 2026-08-04 | **Phase 2 启动**：内置模块注册机制 + 4 个 P0 模块。**架构层**——`Loader` 新增 `builtins`/`builtinFns` 字段与 `RegisterBuiltin` 方法；`require` 在 `resolver.Resolve` 之前拦截 `node:` 前缀 specifier（`loadBuiltin` 去前缀后查注册表，首次调工厂构造、之后缓存）；CJS `require()` 与 ESM `import` 均自动走内置模块（ESM 经 `transformESMToCJS` 的 `require()` 调用原样透传）；新建 `internal/builtin/` 包（`registry.go` 的 `RegisterAll` 一次性注册所有模块，工厂签名 `func(engine.Context) (engine.Value, error)`，仅用 `engine.*` 构造函数避免循环依赖）。**node:path**（`path.go`）——`join`/`resolve`/`normalize`/`dirname`/`basename`（含 ext 去除）/`extname`（Node.js 隐藏文件语义）/`relative`/`isAbsolute`/`parse`/`format` + `sep`/`delimiter` 属性 + `posix`/`win32` 子对象（平台固定语义）；基于 Go `path`/`path/filepath`。**node:os**（`os.go`）——`platform()`/`arch()`/`type()`/`hostname()`/`homedir()`/`tmpdir()`/`cpus()`/`networkInterfaces()`/`freemem()`/`endianness()` + `EOL` 属性 + `constants`（信号常量）；基于 Go `os`/`runtime`/`net`。**node:url**（`url.go`）——`parse`/`format`/`resolve`/`fileURLToPath`/`pathToFileURL`/`domainToASCII`/`domainToUnicode`；基于 Go `net/url`。**node:util**（`util.go`）——`inspect`/`format`（`%s`/`%d`/`%j`/`%o` 占位符）/`promisify`/`callbackify`/`deprecate`/`isDeepStrictEqual`/`styleText` + `types` 子对象（`isNumber`/`isString`/`isPromise`/`isArray`/`isMap`/`isSet` 等 15 个判断）。新增 `builtin_test.go` 15 个测试（path join/dirname/basename/extname/posix-win32/parse、os platform/cpus/EOL、url parse/resolve/fileConversion、util format/inspect/types/isDeepStrictEqual）；测试总数 423→438 |
| v1.7 | 2026-08-04 | Phase 2 第二批模块：4 个 P0 内置模块。**node:events**（`events.go`）——`EventEmitter` 构造器（支持 `new EventEmitter()`，绕过 `engine.Func` 无 this 绑定的限制——实例方法通过闭包捕获 `emitterState` 实现状态隔离）；`on`/`once`（wrapper 触发后精确自删）/`off`/`removeListener`/`emit`/`listeners`/`listenerCount`/`eventNames`/`removeAllListeners`/`setMaxListeners`/`getMaxListeners`/`prependListener`/`prependOnceListener` + 模块级静态方法 `EventEmitter.on`/`.once`/`.off`/`.listenerCount`。**node:fs**（`fs.go`）——同步 API `readFileSync`（支持 utf8 编码选项）/`writeFileSync`/`appendFileSync`/`existsSync`/`statSync`/`lstatSync`（返回 Stats 对象含 isFile()/isDirectory() 方法）/`mkdirSync`（支持 recursive）/`rmdirSync`/`rmSync`/`readdirSync`（支持 withFileTypes）/`unlinkSync`/`renameSync`/`copyFileSync`/`realpathSync`/`mkdirpSync` + `constants`；基于 Go `os`/`io/fs`。**node:assert**（`assert.go`）——`ok`/`strictEqual`/`notStrictEqual`/`deepEqual`/`deepStrictEqual`/`throws`/`doesNotThrow`/`ifError`/`fail` + `assert.strict` 子对象（严格模式别名）；新增 `ErrAssertion` 错误类型。**node:crypto**（`crypto.go`）——`createHash`（md5/sha1/sha256/sha512，返回 Hash 对象含 update/digest 链式 API）/`randomBytes`（同步，返回 hex 编码）；基于 Go `crypto/*`。端到端验证全部通过（fs 读写/stat/append/unlink、assert ok/strictEqual/deepStrictEqual/throws、crypto sha256/md5/randomBytes、events on/once/emit 链式） |
| v1.8 | 2026-08-04 | Phase 2 第三批模块：node:stream + node:querystring + node:string_decoder。**node:stream**（`stream.go`）——Readable/Writable/Duplex/Transform 构造器，均继承自 EventEmitter（通过 `newEmitterInstance` 获得事件能力，在其上追加流方法）；Readable 支持 `push`/`read`/`pipe`/`pause`/`resume`/`destroy` + flowing 模式（注册 'data' 监听器时自动切换）+ `Readable.from` 工厂 + `push(null)` 结束流并触发 'end'/'close'；Writable 支持 `write`/`end`（触发 'finish'/'close'）/`cork`/`uncork`/`destroy` + 自定义 write 函数；Transform 覆盖 write（写入时调 transform 函数，结果 push 到可读端）；`pipeline(s1, s2, ..., callback)` 串联流（基于 `pipe`）；`finished(stream, cb)` 监听 finish/end/error。**node:querystring**（`querystring.go`）——`parse`（支持多值 key 转数组）/`stringify`/`escape`/`unescape`；基于 Go `net/url`。**node:string_decoder**（`string_decoder.go`）——`StringDecoder` 构造器（`write` 处理多字节字符跨边界、`end` 刷新剩余数据）；基于 Go `unicode/utf8`。端到端验证全部通过（stream data/end/pipe、querystring parse/stringify、string_decoder write/end）；内置模块总数达 11 个 |
| v1.9 | 2026-08-04 | **事件循环基础设施 + node:http 完成**（Phase 2 第四批）。**事件循环**——`Interpreter` 新增 `taskCh`（缓冲 64）/`taskWG`/`stopCh`/`loopOnce` 字段与 `PostTask`/`RunLoop`/`Stop`/`AddRef` 方法（`eventloop.go`）；设计：JS 只在 RunLoop 所在 goroutine 执行（单线程语义），任意 Go goroutine（net/http 回调、定时器到期）经 `PostTask` 投递闭包，任务执行后排空 microtask 队列；`WaitGroup` 跟踪活跃句柄（已投递任务 + 活跃定时器 + AddRef 资源），计数归零时 RunLoop 自动退出；`engine.Context` 接口新增 `PostTask`/`AddRef`，`VM` 转发实现，`stubContext` 同步兜底。**定时器 globals**（`timers.go`）——`setTimeout`/`setInterval`/`setImmediate`/`clearTimeout`/`clearInterval`/`clearImmediate`；基于 `time.AfterFunc`/`Ticker` + `PostTask` 回 JS 线程，创建时 `AddRef`、单次触发/clear 时释放。**CLI 接入**——`loader.Run` 后调 `RunLoop()` 进入事件循环，无 pending 任务自动退出。**node:http**（`http.go`）——`createServer`/`server.listen`（动态端口 0、真实端口经 `net.Listen` 后 `ln.Addr()` 获取）/`server.close`/`server.address`（含 地址对象）/`request`/`get`/`STATUS_CODES`；服务器用 Go `net/http.Server` 在 goroutine 监听，请求到达时构造 JS `IncomingMessage`（method/url/httpVersion/headers + 'data'/'end' 事件）与 `ServerResponse`（writeHead/write/end/setHeader/getHeader/statusCode），`handleRequest` 阻塞等待 JS handler 完成保证响应写入时序；客户端 `newClientRequest` 用 Go `http.Client` 发请求、响应 PostTask 回调；关键修复——(1) 服务器 `listen` 时 `AddRef` 计入活跃度、`close` 时延迟到 close 回调执行后再释放（否则事件循环提前退出）；(2) 'data'/'end' 事件延迟到 handler/回调注册完监听器后发射（否则 body 丢失）；(3) `flushHeadersOnce` 避免重复 WriteHeader；(4) nil URL 容错。新增 `http_test.go` 5 个测试（服务器+客户端完整闭环、请求体 echo、setTimeout/setInterval+clear/setImmediate）；内置模块总数达 12 个，测试总数 438→443；CLI 端到端验证通过（STATUS 200 / METHOD / URL / BODY / CLOSED / EXIT 0） |
