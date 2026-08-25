# 差距补齐计划（Gap Closure Plan）

> 状态：**执行中**（v1.0，2026-08-25）
> 依据：全仓 58 份 plan/报告文档遗留项扫描 + 代码/二进制实测验证（见 §2）。目标：把已记录但未闭环的差距项按「可验证、改动面小、收益清晰」优先补齐，并在文档中回填执行记录，消除状态漂移。

## 1. 背景与范围

项目已有大量计划文档，其中不少差距项长期处于「遗留/待办/未闭环」状态。本文档不做新规划，而是**对既有记录做一次核实与收口**：

- 逐个差距项实测/查码验证，标记「已闭环（文档漂移）」「真实未闭环」「维持现状（有理由不追）」；
- 对真实未闭环项按优先级分阶段补齐，每项附验收标准；
- 执行结果回填本文档 §5，并同步修正被证伪的旧文档记录。

**范围**：engine 全局对象/语言成员、Node 内置模块与差分、bundler（web/compile）、GUI JS 面、文档状态漂移。JIT 深层优化、架构级改造（NaN-boxing、Native 寄存器分配等）已在各自计划中排期，本文档不重复，仅登记状态。

## 2. 差距盘点（2026-08-25 实测验证）

验证方法：`./bin/aluka.exe` 实测（-e 单表达式探针）、源码定位（graph.go/collectDeps、builtins.go/setup*、console.go 等）、对照 Node 22 行为。

### 2.1 全局成员缺失（engine 侧，builtins.go 注册）

| # | 成员 | 实测结果（aluka） | Node 22 | 判定 |
|---|---|---|---|---|
| G1 | `Math.hypot` | TypeError: undefined is not a function | 可用 | **未闭环，补** |
| G2 | `JSON.rawJSON` / `JSON.isRawJSON` | undefined | 可用（ES2025 stage 3，V8 已实现） | **未闭环，补** |
| G3 | `escape` / `unescape` | undefined | 全局函数（deprecated 但存在） | **未闭环，补** |
| G4 | `AggregateError` / `EvalError` / `URIError` | undefined | 三个错误构造器 | **未闭环，补** |
| G5 | `eval` | undefined | 全局函数 | **未闭环，补**（转发 Context.Eval） |
| G6 | `Intl.DisplayNames` / `Intl.Locale` / `Intl.supportedValuesOf` | undefined | 可用 | P2 阶段补（Intl 命名空间收口） |
| G7 | `Atomics` / `WebAssembly` / `SharedArrayBuffer` / `WeakRef` / `Iterator` | undefined | 可用 | **延后**：前两者为架构级（SAB 语义/引擎级），WeakRef 需 GC 集成，见 §4 后续阶段 |

来源：`docs/compat-boundary-closure-plan.md:260`（成员缺失清单，标注遗留）、`:265`（Intl 缺 3 成员）、`docs/jiti-dynamic-import-plan.md:229-231`（eval 未注册）。

### 2.2 console L1 缺口（globals 包）

| # | 成员 | 实测 | 判定 |
|---|---|---|---|
| C1 | `console.profile` / `profileEnd` / `timeStamp` | undefined | **未闭环，补**（no-op 语义对齐 Node：不实测可用、不抛错） |
| C2 | `console.Console` 构造器 | undefined | **未闭环，补**（薄封装：可构造、log/error 可用） |
| C3 | `console.assert` | 已存在 | 已闭环 |

来源：`docs/node22-api-coverage.md:24`（console L1 四项缺失）。

### 2.3 已闭环（文档漂移，需回填）

| # | 记录项 | 实测/查码结论 | 回填目标文档 |
|---|---|---|---|
| D1 | require 非字面量参数不折叠（test-bundle-optimize-plan §7 跟踪项） | `graph.go:446-456` 已有 FoldConst 分支并记 unresolved（commit a81f328）；webemit `emit.go:42` 对 UnresolvedDynamic 构建期报错 | test-bundle-optimize-plan.md §7 标记已闭环 |
| D2 | web target 非字面量 require 无拦截（同 §7） | 随 D1：unresolved 合并入 `UnresolvedDynamic`，webemit 报错路径覆盖 require 与 __import 两种来源 | test-bundle-optimize-plan.md §7 |
| D3 | 循环体块级 `let/const` 非按迭代绑定（coding-agent-bundle-report §3.4，阻塞 zod） | `/tmp/loopbind.js` 实测 for-let/for-in-const/while-let 三组闭包捕获与 Node 输出逐字节一致 | coding-agent-bundle-report.md 补状态 |
| D4 | ME-8 payload 压缩「已规划未实施」（memory-optimization-plan:87） | performance-and-functionality-evaluation.md 记录 PayloadVersion v3 压缩已落地（39MB→3.2MB，-91.8%），README 有 `--analyze/--max-payload` | memory-optimization-plan.md §ME-8 |
| D5 | perf-optimization-plan M-P2/M-P3 仍 ⬜ | README 状态：JIT 达成 ~12x / mixed 2.2x | perf-optimization-plan.md 状态回填 |
| D6 | vue-compiler-sfc-compat-plan「待评审排期」 | merge-notes/dev-plan 显示已合入 main、M0-M5 完成 | vue-compiler-sfc-compat-plan.md 状态区 |
| D7 | GUI 半完成项（aluka-gui-api-review P1：win.off/app.unregisterRPC/Windows/getWindowById/openExternal/setHTML） | Phase B/C（commit 8818fcb/801d3f1）已落地：`Aluka.gui.app.unregisterRPC`、`app.getWindows/getWindowById`、`shell.openExternal`、`dialog` 均存在；`win.off` 经真实窗口实例可达（Go 侧实现 + JS 接线于 aluka_gui.go:836） | aluka-gui-api-review.md P1 状态回填；notify/clipboard 仍为 P2 缺口 |
| D8 | console L1（node22-api-coverage.md:24） | 本次会话补齐（见 §3 P2） | node22-api-coverage.md 状态回填 |
| D9 | vue-sfc conformance 基线失败（AGENTS 记录 1/1，实测 FAIL=1） | HEAD（a81f328）干净构建同样 FAIL：probe fnv=80404d9f len=887 vs node bdabb9d8 len=871，为**存量漂移**（compiler-sfc 探针输出 16 字节差异），非本会话改动引入 | 登记为 §4 跟踪项 T1 |

### 2.4 维持现状（有理由不追，本文档不实施）

- globalThis 原型中间层 = V8 细节（`compat-boundary-closure-plan.md:263`，已决策不追）。
- 函数 name/length/prototype 可枚举性：留待 shape 级方案（`:264`）。
- `O-4b reserveUndefined 批量写`（function-call-optimization-plan:87）：收益小，暂缓。
- web 插件 HMR/emitFile 等（web-plugin-hook-fixes-plan 范围声明的后续面）。
- 动态 import 构建期静态化 M4（jiti-dynamic-import-plan M4 探索项）：本期不承诺。

## 3. 分阶段执行计划

### P1 — engine 全局成员补齐（✅ 2026-08-25 完成）

| 项 | 改动位置 | 验收（实测） |
|---|---|---|
| P1-1 `Math.hypot` | `internal/engine/interpreter/math_methods.go` | `Math.hypot(3,4)`===5、空参→0、全零→+0、`hypot(3,'4')`===5、NaN 优先于 Infinity——与 Node 逐项一致 |
| P1-2 `JSON.rawJSON`/`isRawJSON` | `internal/engine/interpreter/builtins.go`（setupJSON + valueToJSON 内联原始文本） | 与 Node 22 实测一致：数字/合法 JSON 字符串/null/true/BigInt→'1'；undefined/NaN/对象/非 JSON 字符串→SyntaxError；symbol→TypeError；`Object.keys(r)`→['rawJSON']（writable:false）；拷贝丢失 marker；stringify 内联 `{"a":100}`；顺带修复存量 `JSON.stringify(NaN)` 报错→"null" |
| P1-3 `escape`/`unescape` | `builtins.go`（setupGlobalFuncs） | `escape('a b@*_+-./')`、中文→%uXXXX、≤0xFF→%XX、代理对双 %u；unescape 逆操作与字面保持——与 Node 一致 |
| P1-4 `EvalError`/`URIError`/`AggregateError` | `builtins.go`（setupErrorCtors） | 三个构造器可用；AggregateError errors 数组+message+cause；**修复存量原型链缺陷**：`Error.prototype` 现指向共享 errorProto，`new TypeError() instanceof Error` 从 false 修正为 true，`{EvalError,URIError,AggregateError}` 构造器均 `instanceof Error` 成立 |
| P1-5 全局 `eval` | `builtins.go`（setupGlobalFuncs） | `eval('1+2')`===3；非字符串原样返回；间接 eval 语义（看不到调用方局部变量，文档标注） |
| P1-6 内置 prototype 链 | `builtins.go`/`date.go`/`typedarray.go` | **修复存量缺陷**：Array/String/Number/Boolean/BigInt/Error/Date/TypedArray/ArrayBuffer/DataView 的 prototype 现均链到 %Object.prototype%（此前仅 functionProto），`[].hasOwnProperty` 等自 undefined 修复为 function，与 Node 一致 |
| P1-7 回归 | `globals_gap_test.go`（新增 6 组 70+ 表驱动用例） | `go test ./internal/engine/interpreter/...` 全绿（含 jitdiff 三 tier 零失配） |

### P2 — console L1 补全（✅ 2026-08-25 完成）

| 项 | 改动位置 | 验收（实测） |
|---|---|---|
| P2-1 `console.profile/profileEnd/timeStamp` | `internal/runtime/globals/console.go` | Node 22 行为一致：静默 no-op，不输出不抛错 |
| P2-2 `console.Console` 构造器 | `console.go`（`newConsoleInstance` 重构 + ctor，实例方法自有属性闭包） | `new Console({stdout,stderr})` 分流正确；stderr 缺省回退 stdout；`new Console(stdout[,stderr])` 位置参数；stdout 缺失抛 TypeError（对齐 ERR_CONSOLE_WRITABLE_STREAM）；`console instanceof console.Console`===true；Console.prototype.assert/log 存在 |
| P2-3 回归 | `console_gap_test.go`（3 组新测试） | `go test ./internal/runtime/globals/` 全绿 |

### P3 — bundler 收口（2026-08-25 完成）

| 项 | 内容 | 验收（实测） |
|---|---|---|
| P3-1 webemit 报错文案覆盖 require | `UnresolvedDynamic` 从 `[]string` 升级为 `[]UnresolvedDep{Source, Spec, RequireCtx}`（graph.go）；webemit 报错与 `--compile` 警告按语境区分（顺带落实 jiti 计划 G6：specifier 诊断文本，astutil.ExprText） | web 构建：`web target requires a string literal for non-literal require() (name) (source index.js)`；compile 警告：`non-literal require with non-constant specifier (name)`；动态 import 文案保持且带 spec 文本 |
| P3-2 `new URL('./x', import.meta.url)` 进依赖图 | graph.collectDeps 收集 URLRef（相对路径 + import.meta.url 基址，import.meta 经 parser lower 为 `__importMeta()`）；finishWalk 读文件、按入口目录换算产物路径、越界/缺失静默跳过；emit printer 新增 RewriteURL hook（NewExpr 分支）；webemit 资产透出 | e2e：`src/pages/page.js` 的 `new URL('../logo.png', …)` → dist/logo.png 资产 + chunk 改写 `new URL("../logo.png", __importMeta().url)`（相对 assets/ 正确解析）；入口 `./pages/pic.png` → dist/pages/pic.png；越界/裸名/绝对路径/非 import.meta 基址均原样保留。graph+webemit 4 组新测试全绿；build/webbuild conformance 24/24、13/13 |
| P3-3 `--compile` sourcemap（T2-B5） | 复用 web target sourcemap/LineStarts | 登记 §4 P10，本会话未承诺 |

### P4 — 核查结论 + 文档回填（✅ 2026-08-25）

| 项 | 内容 | 结论 |
|---|---|---|
| P4-1 GUI JS 面核查 | aluka-gui-api-review P1 半完成项 | **多数已在 Phase B/C 落地**（D7）：`Aluka.gui.app.unregisterRPC/getWindows/getWindowById`、`shell.openExternal`、`dialog` 均存在；`win.on/win.off` 在真实窗口实例上可用（Go 侧接线完毕）。**仍缺**：`notify`/`clipboard`（P2 级，维持登记） |
| P4-2 文档回填 | §2.3 D1-D9 四处漂移 | compat-boundary-closure-plan 成员清单、test-bundle-optimize-plan §7、node22-api-coverage L1、aluka-gui-api-review P1 状态同步修正（见提交） |

## 4. 后续阶段（登记不排期）

- **P5 全局成员二批**：`Intl.DisplayNames`/`Locale`/`supportedValuesOf`（Intl 命名空间可枚举性 + null 原型 + toStringTag 一并收口）。
- **P6 架构级成员**：`SharedArrayBuffer`+`Atomics`（内存模型语义）、`WeakRef`（GC 集成）、`WebAssembly`（引擎级）、`Iterator`（ES2024 迭代器 helpers）、`Symbol.prototype`（boxing + toString/description 方法族）。
- ~~**P7 jiti 生态**~~：**✅ 2026-08-25 完成（jiti M3 全部四项 + node:module.register
  hooks 链）**——`Module._compile`（CompileModuleSource）、`require.extensions`
  （自定义加载器，JS 侧赋值即生效）、`require.cache`（共享对象、注入/删除重载，
  与内部缓存同步）、`module.register` + resolve/load/initialize hooks 链
  （loader_hooks.go；jiti/register 全链路与 Node 22 逐字节一致）。node22 差分新增
  probe/hooks.cjs（0 diff）；遗留：`registerHooks`（Node 22.15+ API）仍为方法面 stub。
- ~~**P8 `-e` 模式 require 注入**~~（defect-fixes-plan P1-6）：**✅ 2026-08-25 完成**——execute() 注入基于 cwd（[eval] 虚拟模块语义）的 require 与动态 import()，内置模块与 process.getBuiltinModule 同步可用。
- **P9 Node22 差分 7 存量失败归因**（compat-boundary-closure-plan:308）+ M10 门禁逐项。
- **P10 工具链**：T1-A8 `--test-concurrency`、AST 对拍临时文件清理（G4）、`--compile` sourcemap。
- ~~**T1 vue-sfc conformance 存量失败归因**~~：**✅ 2026-08-25 修复**——根因：lexer 模板字面量未按 ES 规范把裸行终止符序列（CRLF/CR）规范化为 LF（TV/TRV），vendored compiler-sfc（CRLF 行尾 dist）生成代码混入 
（16 字节漂移）。修复：`readTemplate` cooked/raw 双规范化 + `readEscape` 行续整体删除 CRLF；`FormatVersion` 29→30（字符串常量内容变化，旧缓存失效）。vue-sfc conformance PASS=1/0 FAIL；node conformance 11/11（顺带修复 run.sh 相对 ALUKA 路径在 cd 后失效的 harness 存量 bug）。

## 5. 执行记录

| 日期 | 项 | 结果 |
|---|---|---|
| 2026-08-25 | 差距盘点 v1.0 | §2 实测数据落档；D1/D2/D3 确认已闭环 |
| 2026-08-25 | P1-1 ~ P1-7 | ✅ 全局成员 + 错误原型链修复 + 内置 prototype 链修复，interpreter/jitdiff 全绿 |
| 2026-08-25 | P2-1 ~ P2-3 | ✅ console L1（profile/profileEnd/timeStamp/Console），globals 全绿 |
| 2026-08-25 | P3-1 ~ P3-3 | ✅ unresolved 结构化诊断 + new URL 资产进图与改写；build 24/24、webbuild 13/13 conformance |
| 2026-08-25 | P4-1 ~ P4-2 | ✅ GUI 核查（大部落地，notify/clipboard 遗缺）+ 文档回填 |
| 2026-08-25 | P7 jiti 全支持 | ✅ register + hooks 链 / _compile / require.extensions / require.cache 真实化；jiti/register 与 Node 逐字节一致；hooks 探针 0 diff；35 包全绿 |
| 2026-08-25 | T1 修复（评审跟进） | ✅ lexer 行终止符规范化（CRLF→LF）+ FormatVersion 30 + node conformance harness 修复；vue-sfc 1/1、node 11/11、全套 conformance 绿 |
| 2026-08-25 | 函数 length 面 | ✅ NewFunctionLen/NewNativeMethodLen 变体；Module._compile=3、register=1、createRequire=1 与 Node 一致（hooks 探针覆盖防回归） |
| 2026-08-25 | P8 `-e` require 注入 | ✅ execute() 注入基于 cwd 的 loader：require/import()/内置模块/process.getBuiltinModule 可用；require('./rel') 与动态 import 与 Node 输出一致 |
| 2026-08-25 | 文档全面同步 | ✅ §2.3 全部漂移回填完成：D3（bundle-report 循环绑定已修）、D4（memory ME-8 已闭环）、D5（perf M-P2/P3）、D6（vue-compat 已合入）、H5（requirements JSX 过期说法）；README conformance 数字（build 24/24、webbuild 13/13）与 -e require 示例；AGENTS 增词法行终止符规范约束 + node conformance 11/11；node22-api-coverage module 行（jiti M3 注记） |

## 6. 风险与边界

- `JSON.rawJSON` 为 stage 3 提案：Node 22（V8 12.x）已实现，以 Node 实测行为为准；stringify 内联 rawJSON 文本时不二次转义。
- `eval` 直接调用需绑定当前作用域：当前实现为全局作用域求值（间接 eval 语义），看不到调用方局部变量——已在本文档标注，jiti M3 若需直接 eval 再升级。
- P3-2（new URL 进图）边界：仅相对路径 + import.meta.url 基址组合；越出入口目录/文件缺失/裸名/绝对路径/非 import.meta 基址一律原样保留不报错；UMD/CJS wrap 产物暂不改写（文档登记）。
- AggregateError 的 `errors` 属性按构造参数原样存放（Array 值）。