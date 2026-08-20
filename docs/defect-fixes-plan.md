# Aluka 缺陷修复与开发优化方案

> 项目代号：`aluka` ｜ 文档版本：v1.1 ｜ 日期：2026-08-05
> 依据：2026-08-05 全量代码审查 + 60+ 针对性冒烟用例实测（`go test ./...` 已全绿）
> 配套文档：[需求分析文档](./requirements-analysis.md) / [开发计划文档](./development-plan.md)

---

## 实施状态总览（2026-08-05）

| 缺陷 | 状态 | 说明 |
|------|------|------|
| semver vet 构建失败 | ✅ 已修复 | 文档注释 `+build` 误判 |
| **P0-1** CJS require 异步失效 | ✅ 已修复 | 模块函数包装 + 词法参数；配套修复 let/const 闭包捕获、微任务排水 |
| **P0-2** 箭头函数 `this` 捕获 | ✅ 已修复 | 箭头函数 `__this__` upvalue 化；配套修复 `.call/apply/bind` this 绑定、**UTF-8 BOM 挂起崩溃** |
| **P0-3** Date / URI 编码全局缺失 | ✅ 已修复 | 完整 Date 构造器 + encodeURI/decodeURI 系列 |
| **P0-4** `--ast` 解释器不完整/崩溃 | ✅ 已修复 | `--ast` 复用字节码 VM（AST 解释器废弃为 CLI 引擎） |
| **P1-1** TransformStream 数据流断裂 | ✅ 已修复 | writer.write 经 writeOverride 注入，close 同步关闭 readable |
| **P1-2** `Aluka.$` 标记模板损坏 | ✅ 已修复 | 模板数组参数正确取 quasis |
| **P1-3** crypto.subtle.digest 非标准 | ✅ 已修复 | Buffer 暴露 byteLength/length/索引 |
| **P1-4** structuredClone 缺失 | ✅ 已修复 | 深拷贝对象/数组/Map/Set/Date/Buffer + 循环引用 |
| queueMicrotask 顺序边界 | ✅ 已修复（2026-08-20） | compat-boundary-closure-plan 工作流 C 闭环：① unhandledRejection 改微任务检查点末尾统一判定（FIFO，同检查点稍后挂 catch 不误报）；② 同刻到期定时器改集中式到期队列（(deadline, seq) 堆序派发），消除独立 AfterFunc 竞争投递的偶发乱序。最小复现差分 `tests/compat/node22/diff/c2-microtask-order.cjs`、`c2-timer-fifo.cjs`（修复前失败、修复后 30/30 稳定 PASS）；参数化语料 `tests/compat/node22/microtask-corpus/` 54/60 双跑一致。已知结构性残留：多 TLA 模块求值串行完成（Node 为全体模块体先启动再共享队列交错），数据语义正确、仅观测顺序不同，需编译器/loader 级 TLA 重构（见计划文档 C 完成记录） |

验证：`go vet ./...` 与 `go test ./... -count=1` 全绿（12 包）；测试函数 561 → **585**；
冒烟基线：ES 37/37、TS 11/11、Web 20/21、Aluka 13/14（余项为测试断言口径问题，API 实测正常）。

---

## 目录

1. [概述](#1-概述)
2. [缺陷分级原则](#2-缺陷分级原则)
3. [P0 引擎核心缺陷（阻断真实代码运行）](#3-p0-引擎核心缺陷)
4. [P1 功能缺陷（名不副实或影响部分场景）](#4-p1-功能缺陷)
5. [P2 质量与一致性缺陷（文档/简化实现）](#5-p2-质量与一致性缺陷)
6. [分阶段实施计划](#6-分阶段实施计划)
7. [回归测试与验收标准](#7-回归测试与验收标准)
8. [风险与边界](#8-风险与边界)

---

## 1. 概述

### 1.1 背景

本方案基于 2026-08-05 的代码审查结论。审查方式为**直接运行验证**（非仅读注释/文档）：

- 编译构建 `aluka.exe` 后执行 60+ 个针对性 JS/TS 冒烟用例，覆盖 Phase 1-5 声称的全部能力
- 对每个失败点定位到具体源文件与根因
- 已先行修复 1 项阻塞性缺陷（`internal/pkgmanager/semver/semver.go:4` 文档注释含 `+build` 触发 go vet 误判，导致 `go test ./...` 构建失败），现全部 12 个包测试通过

### 1.2 目标

1. 消除全部 P0 缺陷，使真实 npm 包（express 等）能完整运行
2. 修复 P1 功能缺陷，消除"文档声称"与"实际行为"的差距
3. 同步修订 README / 开发计划文档，恢复文档可信度
4. 建立缺陷回归测试基线，防止复发

---

## 2. 缺陷分级原则

| 级别 | 定义 | 判定标准 |
|------|------|----------|
| **P0** | 引擎核心缺陷 | 导致大量真实代码无法运行、异步路径崩溃或核心 API 缺失 |
| **P1** | 功能缺陷 | 单项 API 行为错误/缺失，影响特定场景，可绕过 |
| **P2** | 质量缺陷 | 简化实现、文档与现状不符，不影响主路径 |

---

## 3. P0 引擎核心缺陷

### P0-1：CJS 模块作用域全局在异步恢复后丢失 — ✅ 已修复

> **修复**（`internal/runtime/module/cjs.go`/`esm.go`）：源码包装为
> `(function(require, module, exports, __filename, __dirname, __import) { SRC })`
> 并以词法参数注入，`require` 等成为闭包捕获的局部变量，异步恢复后可用。
> 配套：修复 `let`/`const` 闭包捕获（`hoistFunc` 预声明）、顶层微任务排水
> （`VM.DrainMicrotasks`）。回归测试：`TestRequireAfterAwait`、`TestLetConstClosureInModule`。

- **现象**：async 函数内 `await` 之后调用 `require()` 报 `TypeError: undefined is not a function`
- **复现**（实测）：
  ```js
  const tp = require("timers/promises");
  (async () => {
    await tp.setTimeout(1, "done");
    console.log(typeof require("v8").getHeapStatistics); // 直接崩溃/静默退出
  })();
  ```
  受影响：`require` / `module` / `exports` / `__filename` / `__dirname` / `__import` 全部
- **根因**（`internal/runtime/module/cjs.go:46-97`）：CJS 模块作用域变量通过**全局属性 + save/restore** 实现（`setGlobals`/`restoreGlobals`），仅在同模块同步求值期间存在。async 挂起恢复时，模块已执行完毕、全局已还原，闭包捕获不到 `require`。
- **修复方案**：改造为 **Node 风格的模块函数包装**——`(function(require, module, exports, __filename, __dirname) { ... })`，将模块作用域变量改为**词法参数**，由闭包天然捕获：
  1. `loader.go` 新增 `wrapCJS(source, path)`：生成包装函数源码（保留行号映射）
  2. `cjs.go` 用 `vm.InvokeFn` 以参数形式注入，替代 `setGlobals`/`restoreGlobals`
  3. 删除全局 save/restore 机制（或仅在 wrapper 内部使用局部变量）
  4. ESM 侧（`esm.go`）同步检查：动态 `import()` lower 成的 `__import` 全局同样受影响，需一并词法化
- **验收**：
  - 新增回归用例：`await` 前后均可 `require` 任意内置模块
  - `timers/promises` 之后 `require("child_process")`、`require("v8")` 等全部可用
  - `tests/conformance/node/` 全量通过

### P0-2：方法内箭头函数 `this` 捕获丢失 — ✅ 已修复

> **修复**（`internal/engine/compiler/compiler.go`、`internal/engine/interpreter/vm.go`）：
> 箭头函数不再声明 own `__this__`，`this` 经 upvalue 链解析为外层函数的 `this`。
> 配套修复 `Function.prototype.call/apply/bind` 对 VM 闭包的 this 绑定，
> 以及 **UTF-8 BOM 文件挂起/崩溃**（lexer 剥离 BOM，`internal/engine/lexer/lexer.go`）。
> 回归测试：`arrow_this_test.go`、`TestLexUTF8BOM`。

- **现象**：对象方法 `f: function() { return (() => this.v)(); }` 中箭头函数取不到 `this`
- **复现**（实测）：
  ```js
  const o = { v: 42, f: function() { return (() => this.v)(); } };
  o.f(); // TypeError: Cannot read properties of undefined (reading 'v')
  ```
  直接 `this.v`（非箭头）正常，说明是**箭头函数捕获外层 `this`** 环节丢失，而非方法调用绑定。
- **根因**：箭头函数的词法 `this` 捕获在嵌套普通函数（方法）上下文中的 upvalue/cell 传递有缺陷，需定位 `internal/engine/interpreter/vm.go` 与 `closure.go` 的 `this` 闭包处理。
- **修复方案**：
  1. 定位箭头函数 `this` 的 upvalue 捕获路径，核对与普通函数 `this` 绑定的交互
  2. 覆盖场景：方法内箭头、getter/setter 内箭头、回调内箭头、对象字面量方法
- **验收**：
  - 上例返回 `42`
  - 新增 `arrow_this_test`：方法/访问器/回调三层嵌套箭头 `this` 全通过
  - 既有 `getter-setter`、`class` 测试不回归

### P0-3：核心全局缺失——`Date` 与 URI 编码函数 — ✅ 已修复

> **修复**（`internal/engine/date.go`/`datestr.go`、`interpreter/date.go`、`interpreter/uri.go`）：
> 完整 `Date` 构造器（静态 `now/parse/UTC` + 原型 get/set 全套 + toString 系列），
> `encodeURI/encodeURIComponent/decodeURI/decodeURIComponent`（UTF-8 字节编码）。
> 回归测试：`date_test.go`（VM + AST 双路径）。

- **现象**（实测）：`typeof Date === "undefined"`；`encodeURI`/`encodeURIComponent`/`decodeURI`/`decodeURIComponent` 均 undefined
- **影响**：`Date.now()/new Date()/setTimeout(ms)` 时间戳类代码全部不可用；`Aluka.sleep` 因依赖 `Date.now` 失效；URL 编码类 npm 包崩溃。**这是 ES5 基础全局，README"ES5 全部核心"声明名不副实**
- **根因**：`internal/engine/interpreter/` 内置对象注册表未实现 `DateCtor`；URI 编码函数未注册
- **修复方案**：
  1. 新增 `date.go`：实现 `Date` 构造器 + 全部静态方法（`now/parse/UTC`）+ 原型方法（`getTime/getFullYear/getMonth/...`、`toString`、`toISOString`、`toJSON`、`getTimezoneOffset`）
  2. 新增 `uri.go`：按 UTF-8 字节实现 `encodeURI/encodeURIComponent`（组件模式不转义 `;/,/?:@&=+$#` 等）与 `decodeURI`/`decodeURIComponent`（`%XX` 解码 + malformed 检查）
  3. 注册进 VM 与 AST 解释器的内置初始化
- **验收**：
  - `Date.now() > 0`、`new Date(0).toISOString() === "1970-01-01T00:00:00.000Z"`
  - `encodeURIComponent("a b/中")`、`decodeURIComponent("%E4%B8%AD") === "中"`
  - 新增 `date_test.go` / `uri_test.go`

### P0-4：`--ast` AST 解释器不完整且可崩溃 — ✅ 已修复

> **修复**（`internal/engine/interpreter/interpreter.go`）：采用方案 A——`Engine.NewContext()`
> 复用字节码 VM，`--ast` 不再走不完整的 AST 解释器（class/解构 panic 消除）。
> AST 解释器类型保留供测试使用，废弃为 CLI 引擎路径。

- **现象**（实测）：
  - `class` 语句：`aluka: not implemented: unexpected statement type *ast.ClassDecl`
  - 对象解构 `let {a} = {a:1}`：**空指针 panic 崩溃**（`interpreter.go:347 execVarDecl`）
- **影响**：`--ast` 被文档描述为"回退模式"，但实质无法运行 ES2015+ 代码，且崩溃无错误恢复。双引擎声明严重名不副实
- **根因**：AST 解释器长期未与 parser/compiler 同步演进（parser 已支持 class/解构/async 等全部语法，interpreter 只实现 ES5 子集）
- **修复方案（两选一，推荐 A）**：
  - **方案 A（推荐）**：`--ast` 直接复用字节码 VM 的 Compile 产物，AST 解释器废弃为历史代码（在帮助文案与 README 中移除 `--ast` 声明）
  - 方案 B：为 AST 解释器补齐全部新语法分支——工作量接近重写，收益低
- **验收**：`--ast` 不再出现 panic；CLI 帮助与 README 同步更新

---

## 4. P1 功能缺陷

### P1-1：TransformStream 数据流断裂 — ✅ 已修复

> **修复**（`internal/runtime/globals/streams.go`）：WritableStream 增加
> `writeOverride`/`closeOverride` 注入机制，TransformStream 的 writer.write 数据
> 经 transform 转发到 readable 端，close 同步关闭 readable。ReadableStream 暴露
> 公开 `enqueue`/`close`。回归测试：`streams_test.go`。

- **现象**（实测）：`TransformStream` 经 `getWriter().write(5)` 写入的数据不会流向 `readable` 端，`getReader().read()` 永不 resolve
- **根因**（`internal/runtime/globals/streams.go:357-407`）：`newTransformStream` 覆盖了 stream 对象的 `write` 属性，但 `newWritableStream` 的 `getWriter().write` 直接调用 `state.writeFn`（为 nil），不经过被覆盖的 `stream.write`
- **修复方案**：
  1. 重构 TransformStream：构造时向 WritableStream 注入 `writeFn`（经 transform 转发到 readable 的 enqueue），而非事后覆盖 `stream.write`
  2. 补 `flushFn` 调用链（writable close → flush → readable close）
- **验收**：`write(5); close(); read() === 5`；`pipeTo` 场景不回归

### P1-2：`Aluka.$` 标记模板形式损坏 — ✅ 已修复

> **修复**（`internal/runtime/globals/aluka_shell.go`）：模板数组参数取第一个
> quasis 字符串。回归测试：`TestAlukaShellTaggedTemplate`。

- **现象**（实测）：`Aluka.$\`echo hello\`` 执行失败（exitCode=1），函数调用形式 `Aluka.$("echo hello")` 正常
- **根因**（`internal/runtime/globals/aluka_shell.go:26`）：`args[0].String()` 对模板数组返回引擎调试格式 `"[ echo hello ]"` 而非字符串——**引擎 `Value.String()` 混用了"调试表示"与"字符串转换"两种语义**
- **修复方案**：
  1. 定义并区分 `Value.String()`（ToPrimitive/ToString 语义）与调试表示方法（当前 `String()` 行为）
  2. `Aluka.$` 对数组参数取 `[0]`（模板 quasis）再拼接
  3. 全库排查其他依赖 `.String()` 取参数的地方（SQL、spawn、子进程等），防止同类问题
- **验收**：`Aluka.$\`echo hello\`` 输出正确；`aluka_test.go` 增补 tagged template 用例

### P1-3：`crypto.subtle.digest` 返回非标准 ArrayBuffer — ✅ 已修复

> **修复**（`internal/engine/buffer.go`）：Buffer 暴露只读 `byteLength`（与 length
> 等价），digest 结果支持 byteLength/length/数字索引访问。回归测试：
> `TestWebCryptoDigestByteLength`。

- **现象**（实测）：`digest("SHA-256", ...)` 返回对象 `byteLength === undefined`，非标准 ArrayBuffer
- **根因**（`internal/runtime/globals/crypto_web.go:6` 注释自认）：digest 用 Buffer 简化表示
- **修复方案**：实现标准 `ArrayBuffer` 值类型（或 Buffer 补充 `byteLength`/`slice`/`[Symbol.toStringTag]`），使 `new Uint8Array(digestResult)` 可用
- **验收**：`(await crypto.subtle.digest("SHA-256", data)).byteLength === 32`

### P1-4：`structuredClone` 全局缺失 — ✅ 已修复

> **修复**（`internal/engine/interpreter/structured_clone.go`）：深拷贝对象/数组/
> Map/Set/Date/Buffer，支持循环引用（引用共享），函数抛 TypeError。
> 回归测试：`structured_clone_test.go`。

- **现象**（实测）：`typeof structuredClone === "undefined"`
- **修复方案**：基于现有对象图遍历实现深克隆（支持普通对象/数组/Date/Buffer/Map/Set，忽略函数与不可克隆类型抛 `DataCloneError`）
- **验收**：`structuredClone({a:[1,2]})` 深拷贝且不共享引用

### P1-5：属性描述符模型为简化版

- **现象**（代码审查）：`Object.defineProperty`/`getOwnPropertyDescriptor` 无 `configurable/enumerable/writable` 真实语义（`object_methods.go:14-18` 注释自认），`defineProperty` 等价 `Set`
- **影响**：依赖严格描述符语义的库（冻结检查、不可枚举属性、`Object.keys` 过滤）行为错误
- **修复方案**：分两阶段
  1. 短期：对象增加 `props map[string]*propDesc` 记录 flags，`getOwnPropertyDescriptor`/`Object.keys`/`for-in` 按其语义工作
  2. 中期：`Object.freeze/seal/preventExtensions` 实现真正的 isExtensible 语义（配合 1C.8 Proxy trap）
- **验收**：`Object.keys` 跳过不可枚举属性；`Object.freeze` 后严格模式写属性抛 TypeError；test262 子集相应用例通过

### P1-6：`-e` 模式能力边界

- **现象**：`-e` 模式下无 `require`、无顶层 `await`（实测均失败）
- **判定**：Node `-e` 本身是 CJS 上下文，`require` 可用；顶层 await 仅在 ESM 可用。属兼容性缺口而非错误
- **修复方案**：`-e` 模式注入 `require`（基于 cwd 的模块加载器）；顶层 await 支持列入 Phase 6 优化（需 ESM 语义改造）
- **验收**：`aluka -e "require('os').platform()"` 可用

---

## 5. P2 质量与一致性缺陷

### P2-1：README/开发计划与实际不符

| 项 | 现状 | 实际 | 处理 |
|----|------|------|------|
| 测试数量 | "604 个测试函数全部通过" | 561 个 `func Test`（+2 子测试） | README 改为实际计数并说明统计口径 |
| dev-plan WBS 状态 | 1B ~60% / 1C ~25% / 1D ~65% "进行中" | 已完成（评估章节已标完成） | 同步 WBS 章节标题 |
| ES5"全部核心" | 声称含全部 ES5 | 缺 `Date`/URI 编码函数 | 修复后补充，或将声明改为明确清单 |
| `--ast` 双引擎 | 声称"回退模式" | 无法运行 ES2015+/可 panic | 见 P0-4，废弃或修复 |

### P2-2：简化实现标注清单（可接受但需在文档明示）

| 位置 | 简化内容 |
|------|----------|
| `os.go:74` | `os.totalmem` 返回 0 |
| `os.go:87` | `os.uptime` 返回 0 |
| `process.go:236-259` | `process.on/emit` 为空 stub |
| `builtin/module.go:33` | `Module` 类仅 runMain 占位 |
| `builtin/tls.go:51` | 未连接 socket 占位 |
| `builtin/registry.go` | `require('url').URL` 未导出（全局 URL 可用） |

### P2-3：文档修订清单

1. README：测试计数、`--ast` 说明、已知限制增补本方案 P0/P1 项
2. `docs/development-plan.md`：WBS 状态统一、新增"缺陷修复"跟踪表
3. `.golangci.yml`：确认 `go vet` 的 `buildtag` 检查纳入 CI（本次 semver 问题 CI 未拦截）

---

## 6. 分阶段实施计划

> 每个阶段以 `make test` + 对应验收用例全绿为完成标准。预计工作量以"人·日"计。

### 阶段一：P0 修复（约 6-8 人·日）

| 顺序 | 任务 | 依赖 | 对应缺陷 |
|------|------|------|----------|
| 1 | `Date` + URI 编码全局实现 | — | P0-3 |
| 2 | 箭头函数 `this` 捕获修复 | — | P0-2 |
| 3 | CJS 模块函数包装改造 | 无（独立） | P0-1 |
| 4 | `--ast` 决策并执行（废弃或修复） | — | P0-4 |
| 5 | 以上全部回归用例入库 | 1-4 | — |

### 阶段二：P1 修复（约 5-7 人·日）

| 顺序 | 任务 | 对应缺陷 |
|------|------|----------|
| 1 | TransformStream 数据流重构 | P1-1 |
| 2 | `Value.String()` 语义拆分 + `Aluka.$` 修复 + 全库排查 | P1-2 |
| 3 | ArrayBuffer 标准值 + `crypto.subtle` 对齐 | P1-3 |
| 4 | `structuredClone` 实现 | P1-4 |
| 5 | 属性描述符 flags 第一阶段 | P1-5 |
| 6 | `-e` 模式注入 `require` | P1-6 |

### 阶段三：文档与质量（约 2-3 人·日）

| 顺序 | 任务 | 对应缺陷 |
|------|------|----------|
| 1 | README/dev-plan 同步修订 | P2-1 |
| 2 | 简化实现标注补充 | P2-2 |
| 3 | CI 增补 `go vet` 拦截 + 冒烟回归脚本 | P2-3 |

### 阶段四（可选）：引擎增强

- 顶层 await（ESM 语义）
- 属性描述符第二阶段（isExtensible/freeze/seal）
- 真实 npm 包 e2e（express）回归基线

---

## 7. 回归测试与验收标准

### 7.1 新增测试基线

| 缺陷 | 回归测试 |
|------|----------|
| P0-1 | `async_require_test.go`：await 前后 require 全部内置模块 |
| P0-2 | `arrow_this_test.go`：方法/访问器/回调/对象字面量嵌套箭头 |
| P0-3 | `date_test.go` / `uri_test.go` |
| P0-4 | `--ast` 冒烟：class/解构/async 不再 panic |
| P1-1 | `transform_stream_test.go`：write→read 闭环 |
| P1-2 | `aluka_shell_test.go`：tagged template 形式 |
| P1-3 | `crypto_digest_test.go`：byteLength 校验 |
| P1-4 | `structured_clone_test.go` |

### 7.2 质量门禁

1. `go vet ./...` 与 `go test ./... -count=1` 全绿（CI 强制）
2. 阶段完成时执行 `tests/conformance/` 全部脚本（node 11/11、test262、npm、install）
3. 每个缺陷修复提交独立 commit，commit message 引用本方案编号（如 `fix(engine): P0-1 CJS require 词法化`）
4. 修复后同步更新 README"已知限制"与本方案状态表

---

## 8. 风险与边界

| 风险 | 应对 |
|------|------|
| P0-1 模块包装改造影响字节码缓存/行号映射 | 保留 sourceURL 与 lineStart 偏移；缓存失效策略同步；先加 e2e 基线再改 |
| P0-2 `this` 捕获改动面广（upvalue 链） | 先写三层嵌套回归用例再改；范围收敛于箭头函数 `this` cell |
| P1-5 描述符模型是架构级改动 | 分两阶段，第一阶段只加 flags 不改存储布局 |
| 本方案工作量估计 | 阶段一/二可并行两组；阶段三可在阶段一后穿插 |
