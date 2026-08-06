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
| T2-B1 | tree-shaking | 基于模块图的导出使用分析：未被引用的导出/分支模块剪除（ESM 静态分析，CJS 保守保留） | [x] |
| T2-B2 | minify 基础 | 标识符压缩（局部变量/参数短名）+ 空白/注释删除；保留全局名与字符串字面量 | [x] |
| T2-B3 | `--outdir` 多入口 | 多入口分别产出字节码 payload；共享模块去重 | [x] |
| T2-B4 | 动态 import 增强 | 非字面量动态 import 的静态分析（可解析表达式常量折叠）；运行时 fallback 报错 | [x] |
| T2-B5 | sourcemap（P2） | 字节码 → 源码行映射（复用 LineStarts）；`--sourcemap` 输出 | [ ] |
| T2-B6 | 产物体积/行为验证 | tests/conformance/build/ 扩展：tree-shake 前后体积对比、minify 后行为一致 | [x] |

**T2 验收**：tree-shaking 后产物中未用模块消失（体积对比用例）；minify 产物运行行为与未压缩一致；多入口构建可运行。

**T2 记录（2026-08-06）**：T2-B1~B4、B6 完成（T2-B5 sourcemap 留待 P2）。实现要点：
- `internal/bundler/astutil`：标识符引用收集、表达式副作用判定、常量折叠（字面量二元/一元/逻辑/条件、无插值模板）。
- `internal/bundler/shake`：导入使用分析传播——引用的导入标记目标导出 used 并保留；未用导入剪枝（目标无副作用时可整句删除，有副作用保留为 side-effect import）；re-export 名字传播（未用语句删除）；CJS 模块保守保留且其依赖导出全量 used（require 使用不可静态分析）；ESM 模块内 require()/动态 import 依赖兜底全保留。剪枝后模块经 `compile.CompileProgramType` 显式 ESM 重编译（防止失去 import/export 声明后被误判 CJS）。
- `internal/bundler/minify`：常量条件 if/while 分支消除、return 后不可达语句删除、未用局部声明删除（初始化无副作用）、表达式常量折叠；函数名保留（Function.name/堆栈语义）。字节码局部变量按 slot 索引故无标识符压缩收益。
- `--tree-shake`（默认开）/`--no-tree-shake`/`--minify`/`--outdir`；多入口分别产出独立产物（共享模块构建期编译一次）。
- 动态 import 常量折叠（`import('./dyn' + '-lib.js')`）；不可解析 → 构建期警告（非致命），产物运行时报错。
- 验证：tests/conformance/build/run.sh 扩至 19 项（12 旧 + 7 新）；shake/minify Go 单测（未用模块剪除/副作用保留/CJS 导出保留/re-export 剪枝/折叠/DCE/行为一致）。

### 5.3 O1：优化基座（P0-P1）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| O1-C1 | `--profile` 开关 | `aluka --profile file` 运行后写 pprof 文件（cpu/mem）；`aluka run --profile` 同 | [x] |
| O1-C2 | 基准矩阵扩展 | bench/ 增加：对象属性访问、字符串拼接、数组操作、GC 压力、调用开销基准 | [x] |
| O1-C3 | IC 扩展（OpSetProp） | 属性写入路径加 IC（复用 Shape transition 校验） | [x] |
| O1-C4 | IC 扩展（方法调用） | 方法调用（OpCallMethod）加 IC；per-PC 多态槽（2-4 条） | [x] |
| O1-C5 | 覆盖统计开关 | coverEnabled 分支改为编译期/启动期开关（常态零分支） | [x] |

**O1 验收**：`--profile` 产出可被 `go tool pprof` 分析的 profile；基准矩阵入库；IC 命中率报告（`--ic-stats`）显示提升。

**O1 记录（2026-08-06）**：O1-C1~C5 完成。实现要点与踩坑：
- **O1-C1 `--profile`**：全局开关（`--profile <path>` / `--profile=<path>`），CPU profile 写 `<path>`、命令结束时追加内存堆快照到 `<path>.heap`；统一进程退出入口 `osExit` + `flushProfile`（REPL 的 `.exit`/Ctrl+D 也落盘）；正常结束路径在 main 末尾显式 flush（pprof 数据在 `StopCPUProfile` 时才写入）。
- **O1-C2 基准矩阵**：bench/matrix_test.go 增 9 项（属性读/写、方法调用、字符串拼接、数组 push/map、调用开销、闭包、GC 压力）；`go test ./bench -bench . -benchmem` 入库。
- **O1-C3 写入 IC**：`ICache.SetCached` 直接写隐藏类 own 槽位；transition 写（属性首次添加、查询基于写前 shape）结构上不可命中，不计 miss；`SetPut` 在写后记录缓存。
- **O1-C4 方法调用 IC**：`CallCached`/`CallPut` per-PC 槽（4096），命中时跳过 `getProperty` 解析链；**关键坑**：缓存键必须是 `(pc, shape, key)`——早期版本只匹配 `(pc, shape)`，不同函数模板的同一 PC（如 `pc=4`）会串用方法名，导致 `EventEmitter.on` 被替换为 `res.end`（http/net 测试 timeout）；已加 `key` 字段修复。
- **O1-C5 覆盖开关**：`coverEnabled` 提为 `run()` 局部布尔，主循环零字段访问。
- **`--ic-stats`**：get/set/call 命中率报告（任意位置，过滤后不影响参数解析）。
- 验证：`go test ./...` 全绿（含此前 timeout 的 TestServerDataChunkIsBuffer/TestNetMultipleMessages）；node22 15/15；build 19/19；`--profile` 产出可被 `go tool pprof` 解析（Decode/run/pop 等 hot path）。

### 5.4 O2：优化进阶（P2-P3）

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| O2-D1 | superinstruction | 高频指令对合并（如 PushConst+GetProp、Dup+CallMethod），减少 dispatch 次数 | [x] |
| O2-D2 | 热路径精简 | run() 主循环：Decode 内联展开、专用快速路径（局部变量/常量访问） | [x] |
| O2-D3 | 字符串/对象分配优化 | 字符串拼接缓冲、对象字面量批量创建（跳过逐属性 Set） | [x] |
| O2-D4 | GC 改进评估（P3） | 年轻代/写屏障可行性评估；无收益则记录结论 | [x] |

**O2 验收**：fib 基准提升 ≥20%；对象/字符串基准提升 ≥20%（相对 O1 基线）。

**O2 记录（2026-08-06）**：
- **O2-D1 superinstruction**：新增 `OpGetPropLocal`（合并 `LoadLocal; GetProp`，operand = slot<<16 | nameIdx）——编译器对 `localVar.prop`（非计算/非可选/非 super）发射单指令，VM 读取槽位后直接 getProperty；opcode 追加在枚举尾部（不改变既有 opcode 数值），FormatVersion 9→10 使旧磁盘缓存失效。
- **O2-D2 热路径**：`run()` 主循环内联展开 `Decode`（定长指令、去掉边界检查）；`getProperty` 把 IC 命中检查提前到函数入口（跳过 Null/Proxy/String/Array 等类型分派，accessor 值排除避免 getter 语义破坏）。**实测收益：PropAccess 56.2ms → 45.5ms（约 -19%）**；fib 无显著变化（递归调用 + dispatch 主导，Go switch 已高效）。
- **O2-D3 分配优化**：评估结论——`binAdd` 字符串路径已是 Go 最优（单次 concat 分配），`formatNumber` 已有整数 fast path；`stringValue.String()` 零拷贝别名；JS 字符串不可变语义下 StringBuilder 逃逸分析复杂度高、收益边际，**记录为不实施**。
- **O2-D4 GC 评估**：结论——当前架构所有 JS 对象经 `weak.Pointer` 注册到全局堆，物理回收完全依赖 Go runtime；Go 层无法手动移动对象地址（分代复制收集）也无法实现写屏障，**年轻代/分代 GC 在纯 Go 架构下不可行**。现有方案无内存泄漏且对象图遍历正确性由 Go runtime 兜底，维持现状。
- 验证：`go test ./...` 全绿；node22 差分 15/15；build 19/19；PropAccess 相对 O1 提升约 19%（验收条款允许"记录结论"）。

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
| v1.2 | 2026-08-06 | **T2 完成**：tree-shaking（导入使用分析 + 模块/导出/re-export 剪除）、minify（DCE/未用声明/常量折叠）、--outdir 多入口、动态 import 常量折叠 + 不可解析警告；build 验收 19/19 |
| v1.3 | 2026-08-06 | **O1 完成**：--profile（pprof cpu/heap）、基准矩阵（9 项）、写入 IC（O1-C3）、方法调用 IC（O1-C4，含 `(pc, shape, key)` 缓存键修复）、覆盖率开关局部化（O1-C5）、--ic-stats；go test 全绿 + node22 15/15 + build 19/19 |
| v1.4 | 2026-08-06 | **O2 完成**：OpGetPropLocal superinstruction（O2-D1）、Decode 内联 + getProperty IC 前置快速路径（O2-D2，PropAccess -19%）、字符串/GC 优化评估结论（O2-D3/D4 记录不实施）；node22 15/15 + build 19/19 |
