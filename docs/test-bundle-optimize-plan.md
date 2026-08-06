# 【测试器 / 打包器 / 优化】开发计划

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 日期：2026-08-06
> 前置：Phase 6 测试器（M5 已收官）、Phase 7 打包器（B2 已收官）、Phase 8 优化（部分基础已落地）

## 1. 背景与目标

aluka 的核心运行时能力（Node 22 兼容 M1-M5、打包 B2 payload 自附着）已收官。
本计划聚焦三个工程化方向：

| 方向 | 现状 | 目标 |
|------|------|------|
| **测试器** | `aluka test` 可跑 mock/snapshot/coverage，node:test 差分 14/14 | 补齐套件钩子/标记/子测试/过滤/报告，达到 Node 22 test runner 常用面 |
| **打包器** | `--compile` 单入口 payload 自附着可用 | tree-shaking/minify/多入口/动态 import，对齐 esbuild 常用子集 |
| **优化** | shape/IC/GC/字节码 VM 已有，IC 覆盖面窄、无 profile 手段 | 建立可测的基准矩阵与 profile 工具链，按热点逐项优化 |

**约束**：纯 Go、CGO 禁用、自研核心组件（不引入 V8/QuickJS 等外部引擎）。

## 2. 三方面基线现状（实测探测 2026-08-06）

### 2.1 测试器

| 能力 | 现状 | 位置 |
|------|------|------|
| describe/it/test/beforeEach/afterEach | ✅ 套件链钩子 | internal/builtin/test.go:69-123 |
| mock.fn/mock.method/restoreAll | ✅ Node 22 语义（无 reset） | test.go:254-317 |
| TestContext（t.assert/t.diagnostic/snapshot） | ✅ | test.go:461-555 |
| `--coverage` 行级报告 | ✅（LineStarts + VM 统计） | cmd/aluka/main.go:227-261 |
| async 用例 | ✅（AwaitPromise 驱动） | test.go:421-440 |
| 套件级 before()/after() | ❌ 仅 beforeEach/afterEach | — |
| it.skip/it.todo/describe.skip | ❌ t.skip/todo 为空桩 | test.go:533-554 |
| t.plan 校验 | ❌ 空操作 | test.go:540 |
| 子测试 t.test(name, fn) | ❌ 空桩 | test.go:549 |
| `--test-name-pattern`/`--test-only` | ❌ | — |
| TAP 输出 / `--test-reporter` | ❌ 自有 ok/not ok 格式 | main.go:206-215 |

### 2.2 打包器

| 能力 | 现状 | 位置 |
|------|------|------|
| `--compile` 单入口 payload 自附着 | ✅ 基座+payload+footer（sha256） | internal/bundler/compile/payload.go |
| 静态模块图（import/export/require + 动态 import 字面量） | ✅ | internal/bundler/graph/graph.go:72 |
| 字节码序列化嵌入加载 | ✅ FormatVersion=9 | compile/embedded.go |
| tree-shaking | ❌ 收集全部静态可达模块 | — |
| minify | ❌（build.go 显式拒绝） | cmd/aluka/build.go:54 |
| `--outdir` 多入口 | ❌ 显式拒绝 | build.go:53,59-61 |
| 动态 import 非字面量 | ❌ 仅字面量 | graph.go:151 |
| sourcemap | ❌ | — |

### 2.3 优化

| 能力 | 现状 | 位置 |
|------|------|------|
| Hidden class（Shape + transition） | ✅ | internal/engine/shape.go:14-66 |
| 全局 IC 表（2048 槽单条） | ✅ 但仅 OpGetProp 一个调用点 | shape.go:85-121、vm.go:1808-1812 |
| 三色标记-清除 GC（weak.Pointer 弱引用） | ✅ | internal/engine/gc.go:23-138 |
| 字节码 VM（~215 opcode 大 switch） | ✅ | vm.go:303+ |
| pprof/profile 支持 | ❌ 全仓无 runtime/pprof | — |
| 基准矩阵 | ❌ 仅 fib 微基准 | bench/fib_test.go |
| superinstruction / per-PC 多态 IC | ❌ | — |
| 分代 GC / 年轻代 | ❌ 全堆 DFS | gc.go:95 |

## 3. 缺口分级

| 级别 | 定义 | 本计划项 |
|------|------|---------|
| **P0** | 影响常用开发流程的缺失 | T1 钩子/标记/子测试；T2 tree-shaking/minify；O1 profile+基准 |
| **P1** | 常用但可绕过的缺失 | T1 过滤/报告；T2 多入口/动态 import；O1 IC 扩展 |
| **P2** | 增强项 | T1 并发执行；T2 sourcemap；O2 superinstruction |
| **P3** | 远期/可选 | T1 跨文件隔离；O2 分代 GC |

## 4. 里程碑规划

| 里程碑 | 内容 | 工作量估 | 依赖 | 验收 |
|--------|------|---------|------|------|
| **T1 测试器增强** | before/after、skip/todo、t.plan、子测试、name-pattern、TAP | ~500-700 行 | M5（已有基座） | node:test 差分扩展 4 项全绿 |
| **T2 打包器增强** | tree-shaking、minify、多入口、动态 import | ~700-900 行 | B2（已有图/产物管线） | build 差分用例 + 产物体积/行为验证 |
| **O1 优化基座** | pprof 开关、基准矩阵、IC 扩展（OpSetProp/方法调用/per-PC） | ~400-600 行 | 无 | `--profile` 可用 + 基准矩阵入库 + IC 命中率提升 |
| **O2 优化进阶** | superinstruction、热路径精简、GC 改进评估 | ~500-800 行 | O1 | fib + 对象/字符串基准提升 ≥20% |

## 5. 里程碑详述

### 5.1 T1：测试器增强（P0-P1）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| T1-A1 | before()/after() 套件级钩子 | 套件进入/退出时执行（Node 语义：before 在套件内首个用例前，after 在末个用例后） | [x] |
| T1-A2 | skip/todo 标记 | `it.skip`/`it.todo`/`describe.skip`/`it(name, {skip:true})`；skip 计入总数不失败，todo 输出标记 | [x] |
| T1-A3 | t.plan(n) 校验 | 记录断言计数，用例结束比对（Node 语义：断言数不符 → 失败） | [x] |
| T1-A4 | 子测试 t.test(name, fn) | TestContext.test 递归注册并执行，FullName 用 `父 > 子` 层级 | [x] |
| T1-A5 | `--test-name-pattern` | 正则过滤用例名（Node 语义：匹配则运行） | [x] |
| T1-A6 | `--test-only` | 只跑标记 `{only:true}` 的用例；无标记时跑全部 | [x] |
| T1-A7 | TAP 输出 / `--test-reporter` | 默认保持现有格式，`--test-reporter tap` 输出 TAP 13（Node 兼容） | [x] |
| T1-A8 | 并发执行（P2） | `--test-concurrency` 控制用例并行（Worker 隔离） | [ ] |

**T1 验收**：node:test 差分新增 4 项（钩子顺序/skip 计数/plan 校验/子测试层级）全绿；`aluka test --test-name-pattern` 过滤行为与 node22 一致。

**T1 记录（2026-08-06）**：T1-A1~A7 完成（T1-A8 并发执行留待 Worker 隔离成熟）。实现要点：
- 执行器按注册顺序混合遍历（children），修复原 suites-first 顺序错误；before/after 套件级钩子（before 失败 → 套件全 fail）。
- skip 不执行（# SKIP）；todo 执行（# TODO，失败不计）；`--test-only` 下非 only 完全隐藏（Node 语义，非 SKIP）；`--test-name-pattern` 不匹配同样隐藏。
- t.plan 只计 t.assert 调用；t.skip() 抛内部错误标记跳过。
- 子测试走微任务调度（VM.EnqueueMicrotask）：async 父 + `await t.test()` 完全对齐 Node；同步父未 await → 子测试 cancelledByParent + 父 `1 subtest failed`（Node 22.14+ 实测语义）；cancelled 独立统计。
- 差分：15-test-runner.cjs（`//@test` 标记 → run.sh 用 `node --test`/`aluka test` + 输出归一化）；差分框架 15/15 全绿。

### 5.2 T2：打包器增强（P0-P1）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| T2-B1 | tree-shaking | 基于模块图的导出使用分析：未被引用的导出/分支模块剪除（ESM 静态分析，CJS 保守保留） | [ ] |
| T2-B2 | minify 基础 | 标识符压缩（局部变量/参数短名）+ 空白/注释删除；保留全局名与字符串字面量 | [ ] |
| T2-B3 | `--outdir` 多入口 | 多入口分别产出字节码 payload；共享模块去重 | [ ] |
| T2-B4 | 动态 import 增强 | 非字面量动态 import 的静态分析（可解析表达式常量折叠）；运行时 fallback 报错 | [ ] |
| T2-B5 | sourcemap（P2） | 字节码 → 源码行映射（复用 LineStarts）；`--sourcemap` 输出 | [ ] |
| T2-B6 | 产物体积/行为验证 | tests/conformance/build/ 扩展：tree-shake 前后体积对比、minify 后行为一致 | [ ] |

**T2 验收**：tree-shaking 后产物中未用模块消失（体积对比用例）；minify 产物运行行为与未压缩一致；多入口构建可运行。

### 5.3 O1：优化基座（P0-P1）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| O1-C1 | `--profile` 开关 | `aluka --profile file` 运行后写 pprof 文件（cpu/mem）；`aluka run --profile` 同 | [ ] |
| O1-C2 | 基准矩阵扩展 | bench/ 增加：对象属性访问、字符串拼接、数组操作、GC 压力、调用开销基准 | [ ] |
| O1-C3 | IC 扩展（OpSetProp） | 属性写入路径加 IC（复用 Shape transition 校验） | [ ] |
| O1-C4 | IC 扩展（方法调用） | 方法调用（OpCallMethod）加 IC；per-PC 多态槽（2-4 条） | [ ] |
| O1-C5 | 覆盖统计开关 | coverEnabled 分支改为编译期/启动期开关（常态零分支） | [ ] |

**O1 验收**：`--profile` 产出可被 `go tool pprof` 分析的 profile；基准矩阵入库；IC 命中率报告（`--ic-stats`）显示提升。

### 5.4 O2：优化进阶（P2-P3）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| O2-D1 | superinstruction | 高频指令对合并（如 PushConst+GetProp、Dup+CallMethod），减少 dispatch 次数 | [ ] |
| O2-D2 | 热路径精简 | run() 主循环：Decode 内联展开、专用快速路径（局部变量/常量访问） | [ ] |
| O2-D3 | 字符串/对象分配优化 | 字符串拼接缓冲、对象字面量批量创建（跳过逐属性 Set） | [ ] |
| O2-D4 | GC 改进评估（P3） | 年轻代/写屏障可行性评估；无收益则记录结论 | [ ] |

**O2 验收**：fib 基准提升 ≥20%；对象/字符串基准提升 ≥20%（相对 O1 基线）。

## 6. 执行顺序与依赖

```
T1 ──→ T2                    （测试器 → 打包器，主线）
O1 ──→ O2                    （优化基座 → 进阶，可并行）
T1/T2 与 O1/O2 无交叉依赖，可双线并行
```

- **T1 依赖 M5**（node:test 基座已收官）
- **T2 依赖 B2**（模块图 + payload 管线已收官）
- **O1 无依赖**（pprof 是标准库能力）
- **O2 依赖 O1**（需要基准基线衡量收益）

## 7. 风险与已知限制

| 风险/限制 | 应对 |
|-----------|------|
| tree-shaking 对 CJS 的保守处理（动态属性访问无法静态分析） | CJS 模块保守保留（esbuild 同策略）；仅 ESM 精确剪枝 |
| minify 改变错误消息/堆栈行号 | 仅压缩标识符与空白，不动字符串/数字字面量；sourcemap 提供行号还原 |
| 字节码产物体积（无压缩） | minify 作用于字节码常量池中的源码字符串与标识符名 |
| 并发测试（T1-A8）的共享状态隔离 | 每用例独立 VM（参考 runTestFile 模式），跨文件天然隔离 |
| superinstruction 增大编译器复杂度 | 仅在 O1 基准证明热点后实施；收益不达标则记录并跳过 |

## 8. 验收策略

1. **测试器差分**：扩展 `tests/conformance/node22/cases/`——钩子顺序、skip/todo 计数、plan 校验、子测试层级、name-pattern 过滤，全部 node22 双跑对比
2. **打包器验证**：`tests/conformance/build/` 扩展——tree-shake 体积对比、minify 行为等价、多入口产物可运行
3. **优化度量**：`go test ./bench/ -bench .` 双基线（O1 前/后）对比；`--profile` + `go tool pprof` 报告入库
4. **回归**：每里程碑 `go test ./...` 全绿 + node22 差分框架不倒退

## 9. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-08-06 | 初稿：三方面基线现状（实测探测）、缺口分级 P0-P3、里程碑规划 T1/T2/O1/O2、验收策略 |
| v1.1 | 2026-08-06 | **T1 完成**：before/after、skip/todo、t.plan、子测试（微任务调度 + await/取消语义）、--test-name-pattern/--test-only/--test-reporter tap；差分框架 15/15（新增 15-test-runner.cjs + run.sh `//@test` 模式） |
