# aluka JIT 后续开发详细计划

> 文档版本：v1.0
> 日期：2026-08-11
> 当前实现基线：`docs/jit-performance-optimization-plan.md` v2.11
> 适用范围：当前 Quick JIT、amd64 Native JIT、trace、guard/deopt、代码缓存及产品化工作

## 1. 目的与完成定义

本文把 JIT 总规划中尚未完成的内容拆成可实施、可验证的后续里程碑。它不替代总体架构文档，
而是回答以下问题：下一步具体做什么、按什么顺序做、每一步需要留下什么证据，以及满足什么条件
才能认为 JIT 已完成并允许默认启用。

JIT 开发的最终完成定义同时包含以下六项，缺一不可：

1. **语义正确**：受支持路径与 Tier 0 一致；guard、异常和 deopt 不丢失状态、不重复副作用；
2. **平台安全**：Windows/Linux amd64 均满足 W^X、GC、抢占、race 和代码释放门禁；
3. **覆盖有效**：数值循环、稳定属性、稳定调用和已声明的数组/闭包热点确实进入目标 tier；
4. **性能达标**：11 项合计不高于 Node 的 15x，mixed 不高于 4x，冷启动回退不超过 5%；
5. **可运维**：编译预算、代码缓存、统计、诊断、关闭和回退行为完整；
6. **可默认开启**：完成跨平台连续 CI 与长期 soak，没有未解释的崩溃、泄漏或语义差分。

在上述条件全部满足前，默认模式继续保持 `--jit=off`。单项微基准达到目标不能替代整体完成定义。

## 2. 当前基线

### 2.1 已完成能力

| 范围 | 当前能力 |
|------|----------|
| 热点系统 | VM-local 调用/回边计数、拒绝缓存、后台编译、分层 guard 熔断 |
| Quick JIT | 类型化 IR、数值算术/比较/控制流、短路逻辑、nullish、位运算、自递归 |
| 值域 | Number、Boolean、null/undefined、Object，以及受限 opaque String/BigInt |
| Native JIT | Windows/Linux amd64 后端、无 Go 指针 Frame、W^X、数值函数和 trace |
| deopt | 多出口 `exitID/resumePC`、dirty locals、属性写回、最多 8 槽操作数栈恢复 |
| Tier 3 | 两路 callee/property PIC、有限内联、属性读写、数组 push、numeric-upvalue closure |
| 生命周期 | VM 关闭、重配置、LRU 淘汰、后台编译未安装结果的 RX 释放 |
| 可观测性 | `--jit-stats`、IR/asm dump、分层失败统计、Native 双执行验证 |

当前 Windows amd64 快照：11 项合计约 `12.0x Node`，mixed 约 `2.2x Node`，已经达到性能目标，
但这只是单机快照，不足以证明产品化完成。

### 2.2 剩余关键缺口

| 类别 | 缺口 | 风险 |
|------|------|------|
| 正确性 | 更广语法生成式差分、异常/副作用 deopt、String/BigInt/Symbol 完整状态 | 错误结果或重复副作用 |
| 平台 | Linux 实机 CI 尚无成功记录，缺少长期抢占和 RX 生命周期 soak | 崩溃、RWX、释放泄漏 |
| 覆盖 | 更多调用约定、数组访问、闭包形态、属性未命中成本 | 性能只对固定基准有效 |
| 优化 | 阈值自动调优、优化 pass、编译/代码预算校准 | 冷负载倒退或编译风暴 |
| 产品化 | 默认 auto、跨平台门禁、回滚策略、正式性能报告 | 无法安全发布 |

## 3. 实施原则

1. Tier 0 始终是最终语义兜底，不删除、不绕过异常处理和监控语义；
2. 任何优化假设必须对应显式 guard，guard 必须发生在相关副作用之前；
3. Native 代码不保存 Go 指针、不调用 Go、不分配对象、不执行 getter/Proxy/用户回调；
4. Quick 可以执行 Go 代码，但涉及分配或用户代码的操作必须有明确提交点和异常映射；
5. 每个新增 opcode 依次完成 lowering、verifier、Quick、trace、Native 支持或显式拒绝、差分测试；
6. 每个性能改动同时报告 JIT off/quick/auto，不能用 Auto 收益掩盖 Tier 0 回退；
7. 任一硬门禁失败时保留上一稳定 tier，不扩大支持面来掩盖问题。

## 4. 路线与依赖

```text
R0 基线固化
 |
 v
R1 正确性闭环 ---------> R3 Quick 语义覆盖
 |                         |
 v                         v
R2 平台与运行时门禁 ---> R4 Tier 3 热点扩展
                           |
                           v
                        R5 性能与预算调优
                           |
                           v
                        R6 产品化与默认 auto
```

R1 与 R2 的部分任务可以并行，但 R1、R2 未完成前不得开始默认 auto；R3/R4 的新能力必须复用
R1 建立的差分框架；R5 必须在功能和安全边界稳定后进行，避免用性能数据固化错误语义。

工作量以单名熟悉代码库的工程师日估算，仅用于排序，不是交付日期承诺。

| 里程碑 | 主题 | 预计工作量 | 前置依赖 |
|--------|------|------------|----------|
| R0 | 基线、证据和测试清单固化 | 2-3 日 | 当前 v2.11 |
| R1 | 生成式差分与完整 deopt 正确性 | 8-12 日 | R0 |
| R2 | Linux、W^X、GC/抢占与长期 soak | 6-10 日 + soak | R0，可与 R1 并行 |
| R3 | Quick JIT 语义和控制流覆盖 | 8-12 日 | R1 |
| R4 | 调用、属性、数组和闭包热点扩展 | 10-15 日 | R1、R3 |
| R5 | 优化 pass、阈值和预算调优 | 5-8 日 | R2、R4 |
| R6 | 默认 auto 与发布验收 | 5-8 日 | R1-R5 |

### 4.1 里程碑启动状态

| 里程碑 | 启动状态 | 说明 |
|--------|----------|------|
| R0 | 交付物齐备，验收未过 | R0-1 至 R0-5 交付物均已产出（2026-08-11）；但 §5.3 稳定性验收（连续两轮中位数偏差 ≤5%）在本机未通过，需安静/固定电源环境复核后才可宣称 R0 完成 |
| R1 | 部分完成 | R1-1/R1-2/R1-3/R1-4/R1-8 已落地（2026-08-11）：生成式差分框架（PR 1,000 例 + nightly 100,000 例无差分）、值域组合、异常差分（BigInt 除零/getter-setter/回调/OOM/取消/中断）、deopt 状态模型（含 pending exception 正式恢复映射与 verifier 拒绝）、失败产物与单命令重放；缺 R1-5 至 R1-7 |
| R2 | 部分完成 | Linux 后端和 CI job 已写入，尚无 Linux runner 成功记录和长期 soak |
| R3 | 部分完成 | Number 主路径、短路、nullish、String/BigInt opaque 值和严格相等已落地 |
| R4 | 部分完成 | 两路 PIC、有限内联、属性、push 和单一 upvalue 模式已落地 |
| R5 | 部分完成 | 已有预算、LRU 和后台编译，缺优化 pass 与数据驱动阈值校准 |
| R6 | 未开始 | 默认仍为 off，尚未满足默认 auto 检查表 |

“部分完成”只描述启动基线，不代表通过该里程碑；每个里程碑仍以对应章节的完整完成条件为准。

## 5. R0：基线与证据固化

### 5.1 目标

建立后续所有正确性和性能判断使用的唯一基线，避免不同二进制、机器状态或脚本版本混用。

### 5.2 任务

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| R0-1 ✅ | 固化环境信息 | Go/Node/OS/CPU/commit/电源策略记录 | 每份结果可追溯到同一源码和二进制 |
| R0-2 ✅ | 统一三 tier 基准入口 | off/quick/auto 一次构建、顺序轮换执行脚本 | 自动输出原始样本、中位数和离散度 |
| R0-3 ✅ | 建立正确性覆盖矩阵 | opcode、值类型、函数/trace、Quick/Native、deopt 测试表 | 每个现有能力都有至少一个权威测试 |
| R0-4 ✅ | 建立结果归档格式 | `bench/results/jit-<date>-<platform>.json` 及摘要 | 结果包含参数、版本、统计和失败原因 |
| R0-5 ✅* | 冻结当前快照 | Windows 正式 5 次中位数报告 | 11 项、mixed、冷启动和专项基准齐全 |

### 5.3 验收

```powershell
go test ./... -count=1
go vet ./internal/engine/jit/... ./internal/engine/interpreter ./internal/engine ./cmd/aluka ./bench
go test ./bench -run '^$' -bench 'JITColdStart' -benchtime=50x -count=5
```

**完成条件**：相同源码连续两轮基准的中位数偏差不超过 5%，全部结果具备环境和 JIT 统计，
覆盖矩阵不存在“代码已宣称支持但无测试”的条目。

### 5.4 实施记录

| 日期 | 条目 | 证据 |
|------|------|------|
| 2026-08-11 | R0-2 ✅ 统一三 tier 基准入口 `bench/cmd/jitbench` | 14 个单元测试（输出解析、统计、轮换、聚合、失败记录）；Windows 实机 `-reps 3` 轮换顺序为 off/quick/auto → quick/auto/off → auto/off/quick，逐 (case, tier) 输出原始样本、中位数、min/max、均值与相对 MAD；`go test ./... -count=1`、`go vet`（含 `./bench`）、`git diff --check` 通过；冷启动 `JITColdStart` 50x5 中位数 off 2.353ms / auto 2.447ms（auto 回退约 3.9%，低于 5% 门禁）；`go test -race` 在本机因 Windows TSan 无法分配影子内存（error code 87，基线同样失败）而不可执行，需由 R2 Linux CI 覆盖 |
| 2026-08-11 | R0-4 ✅ 结果归档格式 `bench/results/jit-<date>-<platform>.json` | `schemaVersion: "1"` 契约由 `validateReport` 强制：参数（config）、版本/环境（platform/cpu/goVersion/alukaVersion/commit）、统计（cases + summary，含逐 tier 中位数与 `vsOff` 加速比）、失败原因（failures，缺失样本必须有失败记录解释）；归档写盘前校验，`-out` 指向目录时按 `jit-<YYYYMMDD>-<goos>-<goarch>.json` 命名；新增 8 个单元测试（摘要计算、15 条校验规则、写读 round-trip、命名约定）及构建目录回归测试，累计 23 个通过；实机生成 `bench/results/jit-20260811-windows-amd64.json` 并通过 round-trip 校验 |
| 2026-08-11 | R0-3 ✅ 正确性覆盖矩阵 `docs/jit-coverage-matrix.md` | 覆盖 45 个 IR opcode（按 Quick 函数 / Quick trace / Native 列），7 类值（Number/Boolean/nullish/Object/String/BigInt/Symbol），函数/trace/Native 生命周期、guard/deopt、平台与可执行内存；每个能力行标注权威测试引用，全部引用逐一核对存在且通过；审计发现唯一缺口——v2.11 宣称 Symbol 值 guard 回 Tier 0 但无测试——新增 `TestJITSymbolValuesGuardBackToTier0`（函数 + trace 覆盖 `===` 与 truthiness，断言三模式结果一致、guard 失败被记录、Auto 无 verify 失败），审计后不存在“已宣称支持但无测试”条目 |
| 2026-08-11 | R0-1 ✅ 环境固化 | 记录于 `docs/performance-report-r0-5.md` §1：OS Windows 10.0.26200.8875、CPU 13th Gen i5-13420H（笔记本）、Go 1.25.10、Node 22.23.1、commit `fbe9b5e`、二进制 SHA-256 `43c4ba83…`、电源方案“平衡”（本机唯一）、交流电 100%；全部 4 轮与专项均复用同一二进制，原始样本存于 `bench/results/` 归档 |
| 2026-08-11 | R0-5 ✅* 冻结快照 `docs/performance-report-r0-5.md` | 11 项 5 次中位数（Auto 合计 949.71ms，13.8x Node；off 3140.23ms）、mixed 墙钟（Auto 295.29ms，2.6x Node）、冷启动 50x5（off 2.858ms / auto 3.946ms，+38.1%）、专项 4 项（numeric loop 7.56ms / callee inline 198.48ms / external props 8.97ms / prop write 7.85ms，均 5 次中位数）与 JIT stats 齐全；**R0 §5.3 稳定性门禁（连续两轮中位数偏差 ≤5%）未通过**：A-B 19.1% / B-C 26.8% / C-D 64.2%，根因为本机仅有“平衡”电源方案 + 后台负载（ChatGPT/ToDesk/webview2/ZCode），已如实记录，待安静/固定电源环境复核 |

R0-4 边界：归档名按日按平台（同日重跑会覆盖同名文件，需要保留历史时用 `-out` 指定完整路径）。
R0-3 边界：矩阵只声明现有能力，未落地项（生成式差分、String/BigInt 算术、宽松相等、Native spill 等）
进入矩阵的时机见矩阵 §8 维护约定；Linux 实机/长期 soak/race 由 R2 承担，不记入矩阵。
R0-5 标记 `✅*` 表示**交付物已产出但 R0 整体验收未通过**：11 项、mixed、冷启动与专项数据齐全，
但 §5.3 稳定性门禁（连续两轮中位数偏差 ≤5%）在本机（平衡电源 + 后台负载）实测未过
（A-B 19.1% / B-C 26.8% / C-D 64.2%）。在安静环境、固定电源策略下复核通过前，不得宣称 R0 完成，
默认 `--jit=off` 维持不变。

## 6. R1：正确性与 deopt 闭环

### 6.1 目标

把正确性证据从固定模板扩展为语法生成、异常和副作用序列，并让所有 JIT 退出恢复完整 VM 状态。

### 6.2 任务

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| R1-1 ✅ | AST/语法生成式差分 | 固定种子表达式、循环、嵌套分支生成器 | Tier 0/Quick/Native 每日不少于 10,000 例无差分 |
| R1-2 ✅ | 值域组合 | `NaN/Inf/-0`、nullish、Boolean、String、BigInt、Symbol、对象 identity | 每种值参与短路、比较、返回和 guard 变化 |
| R1-3 ✅ | 异常差分 | 除零 BigInt、getter/setter 抛错、回调抛错、OOM、取消 | 异常类型、消息、catch PC 和副作用日志一致 |
| R1-4 ✅ | deopt 状态描述 | locals、operand stack、属性提交、pending exception、resume PC 映射 | verifier 能拒绝缺失、歧义或越界映射；pending exception 进入 `DeoptExit` 并经 Quick/VM 恢复链路实际使用 |
| R1-5 | 副作用提交协议 | prepare/validate/commit 或等价两阶段协议 | 退出后不重复属性写、调用、upvalue 写或数组 append |
| R1-6 | 随机 guard 失效 | shape、callee、类型、prototype、accessor 在热身后变化 | 第三 shape/target 稳定回退且 RX 正确释放 |
| R1-7 | fuzz 入口 | IR verifier、trace compiler、deopt decoder fuzz test | 任意输入不 panic、不越界、不发布非法代码 |
| R1-8 ✅ | 差分失败最小化 | 保存 seed、源码、tier、IR、统计和最小复现 | CI 差分可在本机用单条命令复现 |

### 6.3 deopt 完整状态要求

每个可执行 exit 必须证明以下状态之一：已恢复，或在副作用发生前退出而无需恢复。

| 状态 | 最低要求 |
|------|----------|
| PC | 指向 Tier 0 可安全继续的字节码边界 |
| locals | 只提交当前路径已完成的写入，保留 `-0/NaN` 位语义 |
| operand stack | 深度和顺序由 verifier 锁定；Quick 可持有建模值，Native 仅 Number spill（≤8 槽） |
| 属性/数组/upvalue | 提交点前 guard 全部通过；失败时不留下部分写入 |
| 异常 | 不跨 Native 栈传播；回到 Go 后进入现有 `handleThrow` |
| safepoint | 只在完整迭代或完整提交点退出，恢复后不得重复迭代 |

**R1-4 状态模型（2026-08-11 完成）**：每个 `OpTraceExit` 携带 `exitID` → `DeoptExit{ID, ResumePC,
LocalSlots, StackDepth, StackValues, PendingException}`。`PendingException`（`engine.Value`，nil =
无）是异常出口的正式 pending-exception 状态：trace 编译遇到 `OpThrow` 时产生 exception exit
（在 throw 位置直接放置 `OpTraceExit`，不新增 IR opcode），Quick 执行器把栈顶原始 JS 值移入
`PendingException` 并丢弃其余操作数栈（JS 异常展开语义），VM 恢复时经 `*jsThrow` 将原始值送入
现有 `handleThrow`/try-catch-finally 状态机；Native 编译拒绝含 exception exit 的程序（机器码无法
表示 Go 指针/engine.Value），Auto 稳定回退 Quick。verifier 对 map 强制：exitID 必须落在
`traceExitDepths` 内（缺失/越界/负 ID 一律拒绝，即使栈为空）；同一 exit 在可达路径上的栈深必须
一致（歧义/预置冲突拒绝）；栈深 ≤8 槽（非法深拒绝）；exception exit 必须带栈顶异常值（栈下溢
拒绝）且 exception map 与 deopt map 一一对应（截断/扩展 map 均拒绝）。`CompileTraceWithGuards` 在 Verify 后
拒绝不可达 exit。`TestVerifyRejectsInvalidDeoptMaps` 覆盖 7 类非法 map，
`TestVerifyRejectsInvalidExceptionMaps` 覆盖截断/扩展 exception map 与异常值缺失，
`TestExceptionExitCompilesAndExecutes`/`TestNativeRejectsExceptionExit`/`TestSameDeoptExitPendingException`
覆盖编译、执行、Native 拒绝与 `SameDeoptExit` 比较。

### 6.4 验收命令

```powershell
go test ./internal/engine/jit/... ./internal/engine/interpreter -count=1
go test -race ./internal/engine/jit/... ./internal/engine/interpreter `
  -run 'JIT|Trace|Native|Deopt|Exception|Property|Array|Closure|Safepoint|OOM' -count=1
go test ./... -count=1
```

CI 快速集使用固定种子至少 1,000 例；nightly 使用至少 100,000 例和多个 seed。任何 mismatch、
panic、非法内存访问、重复副作用或不可复现失败均视为未完成。

**完成条件**：覆盖矩阵中所有已支持 opcode/值类型组合都有 Tier 0 对照；连续 5 次 CI 与一次
nightly 无差分；所有语义出口具备 verifier 可证明的恢复映射。

### 6.5 实施记录（R1-1 / R1-2 / R1-3 / R1-4 / R1-8）

| 日期 | 条目 | 证据 |
|------|------|------|
| 2026-08-11 | R1-1 ✅ 生成式差分框架 `internal/engine/interpreter/jitdiff` | 14 种用例形态（expr/branch/loop/strictEq/looseEq/propRead/propWrite/push/closure/call/getter/callbackThrow/proxy/deoptPrefix），固定种子可复现，Tier 0 为唯一 oracle；**PR 集 1,000 例通过**（quickHit=440 / autoNativeHit=337 / guardFailures=2644），按 Kind 断言 8 类 Quick 与 5 类 Native 命中；**nightly 100,000 例复核通过**（5 seed × 20,000，131s，均零差分），`.github/workflows/ci.yml` 的每日 `jit-differential-nightly` job 自动执行并在失败时上传产物 |
| 2026-08-11 | R1-2 ✅ 值域组合 | 随机生成器覆盖 Number 边界、Boolean、nullish、String、BigInt、Symbol 和对象 identity；新增结构化 `valueDomainCases`，逐值标记并实际执行 return/shortCircuit/comparison/guardChange，`TestValueDomainOperationCoverage` 对每个值运行 off/quick/auto 并要求全语料真实产生 guard failure；strictEq/looseEq 继续用受控值对覆盖身份语义，未支持路径明确回退 Tier 0 |
| 2026-08-11 | R1-8 ✅ 失败最小化 | 失败时保存 `case.js`、`meta.json`、`SUMMARY.txt` 与单命令重放；`Params.Verify` 已接入 Auto 执行与重放；Artifact 保留首次 mismatch 的 Result/EvalErr/Stats，仅从重跑补 IR，避免时序故障被重跑覆盖；`TestArtifactRoundTrip` 使用合成 mismatch 验证原始差异落盘，另有 passing-result 拒绝与 Verify 生效测试 |
| 2026-08-11 | 框架发现并修复 2 个 Tier 0 引擎 bug | ① BigInt/NaN 关系比较：NaN panic、反向误判及数字路径 `NaN > 3` 修复为统一 `compareBool` 哨兵处理。② parser 泛型/比较消歧：括号深度避免吞掉关系表达式，同时恢复 CallExpr/NewExpr 泛型调用与函数类型内部嵌套泛型闭合；新增比较链、调用结果泛型与嵌套函数类型回归。两个修复均不改变默认 `--jit=off` 或 Native ABI |
| 2026-08-11 | verifier deopt map 拒绝（R1-4 前置） | 收紧 `OpTraceExit` 校验：缺失/越界/负 exitID 一律拒绝（原仅栈非空时拒绝）；`TestVerifyRejectsInvalidDeoptMaps` 覆盖缺失/越界/负 ID/歧义深度（同一 exit 两条路径不同栈深）四类，并修正 `TestNativePropertyWriteVerifyRestoresQuickResultOnMismatch` 补合法 deopt map；`jit_bridge.go` 增加 trace IR dump（`JIT dump tier=trace`），使失败产物含 trace 级 IR |
| 2026-08-11 | R1-3 ✅ 异常差分 | `jitdiff` 新增 5 个 Kind（BigIntDivZero/GetterSetterThrow/OOM/Cancel/Safepoint）+ `RunHook`（OOMBytes/TriggerOOM/CancelAfter/CancelErr），生成器 Version 1→2；差分夹具显式启用 `InterpreterSafepoints`，使解释循环回边与 JIT budget yield 共用回调，同时不改变默认嵌入行为；取消保持独立 `Error`，不再伪装为 OOM；固定用例扩至 17 个，异常均进入同一 catch 路径并保留逐步事件日志，延迟中断验证已提交迭代无重复/遗漏；差分发现并修复 BigInt `/` lexer 与 BigInt `++/--` 问题，审核另修复 JIT `--` 曾错误降低为 `1-x`；`TestUpdateExpressionAcrossJITTiers` 锁定三 tier 前后缀语义；PR 1,000 例与 nightly 100,000 例（5 seed）均零差分 |
| 2026-08-11 | R1-4 进行中：deopt map 加固 | `DeoptExit{ID, ResumePC, LocalSlots, StackDepth, StackValues}` 的现有恢复映射增加 verifier 拒绝规则（§6.3）；`TestVerifyRejectsInvalidDeoptMaps` 覆盖 7 类非法 map，`TestDeoptExitMapIntegrity` 审计对齐 ResumePC、去重 local 槽、合法栈深；固定用例 -17 验证属性写 guard 失败前无部分写入 |
| 2026-08-11 | R1-4 ✅ pending exception 正式恢复映射 | `DeoptExit` 增加 `PendingException engine.Value`（nil = 无）；trace 编译 `OpThrow` 为 exception exit（throw 位置直接放 `OpTraceExit`，不新增 IR opcode），Quick 执行器把栈顶原始 JS 值移入 `PendingException` 并丢弃其余操作数栈，VM 恢复经 `*jsThrow` 以原始值进入 `handleThrow`/catch-finally；Native 编译拒绝 exception exit（`lowerNativeInputsForMode` 检查），Auto 稳定回退 Quick；`SameDeoptExit` 比较 pending exception（Number 按位含 NaN、字符串按值、对象按 identity）；verifier 拒绝截断/扩展 exception map 与异常值缺失。测试：jit 包 `TestExceptionExitCompilesAndExecutes`/`TestNativeRejectsExceptionExit`/`TestVerifyRejectsInvalidExceptionMaps`/`TestSameDeoptExitPendingException`；interpreter 包 8 个 `TestDeoptExceptionExit*`（数字/字符串/对象 identity/非空栈丢弃/finally 重抛/嵌套 catch/guard 失败/Auto 回退/deopt stats）；jitdiff 固定用例 -18 与 artifact 保存/重放测试 |

R1-1/R1-2/R1-3/R1-4/R1-8 已完成。R1-5（副作用两阶段提交协议）、R1-6（随机 guard
失效）、R1-7（fuzz 入口）未在本轮完成；当前 Windows 已通过计划规定的 JIT race 子集和 jitdiff race，
但仍不能替代 R2 的 Linux 实机、连续 CI 与长期 soak 门禁。

## 7. R2：平台和运行时安全门禁

### 7.1 目标

在真实 Windows/Linux amd64 上证明生成代码、Go runtime 和代码生命周期可以长期共存。

### 7.2 任务

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| R2-1 | Linux 实机 CI | `jit-linux` job 的可追溯成功记录 | 主分支连续 5 次通过，不以交叉编译替代 |
| R2-2 | W^X 验证 | Windows protection 与 Linux `/proc/self/maps` 检查 | 不存在同时可写可执行的 JIT 区域 |
| R2-3 | GC/抢占压力 | Native 执行时并发 GC、异步抢占、栈增长测试 | race 和普通构建均无 crash/hang |
| R2-4 | 生命周期 soak | 创建/关闭 VM、后台编译、LRU 淘汰循环 | RX 区域数和字节最终回到基线 |
| R2-5 | 重配置竞态 | `ConfigureJIT`、`Close`、pending compile 交错 | 不安装过期代码、不 double free、不泄漏 |
| R2-6 | 崩溃隔离 | 非法机器码继续只在子进程执行 | 测试进程能报告失败，不污染其他测试 |
| R2-7 | 平台降级 | arm64/macOS/不支持环境显式使用 Quick/Tier 0 | 功能测试通过且不申请可执行内存 |

### 7.3 soak 分级

| 级别 | 时长 | 用途 | 通过条件 |
|------|------|------|----------|
| PR | 5 分钟 | 快速生命周期和抢占回归 | 零失败、RX 回基线 |
| Nightly | 30-60 分钟 | GC/race/后台编译压力 | 零 crash、hang、race、持续增长 |
| Release | 至少 8 小时 | 发布前长期稳定性 | 内存/RX 无单调增长，结果无差分 |

建议 soak 同时轮换 `GOGC=20/100/off`、默认异步抢占、1KB/4MB 代码缓存，以及频繁 VM
创建/关闭。`GOGC=off` 只用于隔离问题，不能作为通过门禁的唯一配置。

**硬停止条件**：发现 RWX 页面、Go 指针进入 Native Frame、无法回收的 RX 增长、不可隔离的
Native crash，或必须关闭 GC/抢占才能稳定运行。触发后停止扩大 Native 支持，保留 Quick。

**完成条件**：Windows 和 Linux amd64 连续 5 次完整 CI 通过，release soak 满足 8 小时要求，
关闭后 RX 计数回到起始基线，race 无报告。

## 8. R3：Quick JIT 语义覆盖

### 8.1 目标

让 Quick 成为稳定的跨平台优化层，并作为 Native 的可信语义参考，而不是只覆盖微基准模板。

### 8.2 优先顺序

| ID | 能力 | 实施要求 | 完成条件 |
|----|------|----------|----------|
| R3-1 | 全原始值 truthiness/nullish | 加入 Symbol；保持 invalid 与真实 undefined 分离 | `!`, `&&`, `||`, `??` 跨值类型差分通过 |
| R3-2 | 严格相等补全 | 在现有 String/BigInt/对象基础上加入 Symbol identity | `===/!==` 无 coercion，Native Number 结果一致 |
| R3-3 | 宽松相等 | 抽取共享 primitive equality helper | 不在 JIT 复制另一套 ToPrimitive；对象一律 guard |
| R3-4 | String 运算 | `+` 拼接、同类型关系比较 | 分配只发生在 Quick；异常/rope 语义与 Tier 0 一致 |
| R3-5 | BigInt 运算 | 同类型算术、比较、位运算 | 除零/负指数异常一致；Native 显式拒绝 |
| R3-6 | 控制流 | 条件表达式、更多 switch/嵌套短路形态 | CFG 合流栈深一致，外跳均有 deopt map |
| R3-7 | opcode 拒绝成本 | 编译期候选过滤和稳定拒绝缓存 | 不支持 opcode 不在每个回边重复编译 |

R3-3 到 R3-5 若需要共用语义，应把无解释器依赖的纯 helper 下沉到 `internal/engine` 或新的
内部语义包；禁止从 `jit` 反向依赖 `interpreter`。

### 8.3 完成条件

- Quick 对覆盖矩阵中的已支持组合零 guard failure；
- Auto 对不支持的 Native 类型最多两次失败后稳定使用 Quick；
- 新增能力至少包含 IR 单测、函数集成、trace 集成和 Tier 0 差分；
- instruction metrics 开启时仍按现有约定绕过 JIT；
- Quick 冷启动和 JIT off 性能不突破 R5 的回退预算。

## 9. R4：Tier 3 热点覆盖扩展

### 9.1 目标

从固定 benchmark 形态扩展到实际代码中常见的稳定调用、属性、数组和闭包模式，同时保持 Go
对象只在 Go 入口 guard 和提交阶段访问。

### 9.2 任务

| ID | 工作项 | 范围 | 完成条件 |
|----|--------|------|----------|
| R4-1 | 调用约定扩展 | 0-4 参数数值叶子、返回 Boolean、多个调用点 | 参数顺序/this/异常一致，第三 target 稳定回退 |
| R4-2 | 闭包扩展 | 多个 numeric upvalue、只读捕获、非逃逸闭包 | upvalue identity/别名 guard 完整 |
| R4-3 | 属性 PIC | 2-4 个稳定 own data shape，自适应上限 | accessor/Proxy/prototype 不进入快路径 |
| R4-4 | 属性未命中成本 | 缓存拒绝、减少重复 shape/slot 探测 | 多态负载相对当前无回退，统计可解释 |
| R4-5 | 数值数组索引 | packed Number read/write、length guard | hole、稀疏数组、原型索引和 Proxy 回 Tier 0 |
| R4-6 | 数组批处理 | push 之外的安全 range、map/filter/reduce 模式 | callback purity 和元素类型由编译器/guard 证明 |
| R4-7 | Native opcode | Mod/Pow/位运算按收益逐项评估 | 每项相对 Quick 有收益且不改变 ABI，否则保留 Quick |
| R4-8 | side-exit 成本 | 减少重复桥接、恢复和状态查表 | deopt 后不在同一 frame 重试已失败版本 |

### 9.3 每条快路径的必要证据

1. 一个正向命中测试，统计必须证明进入目标 tier；
2. 类型、shape/callee identity、prototype/accessor/Proxy 的负向测试；
3. 第三 shape/target 或连续类型变化后的熔断测试；
4. safepoint/OOM 中断后的已完成前缀测试；
5. Native verify mismatch 人工注入后的 Quick 结果恢复测试；
6. 对应专项 benchmark 和综合 benchmark，不得只报告命中用例。

**完成条件**：新增路径全部满足六类证据；11 项合计继续不高于 Node 15x；多态/未命中负载相对
当前 Auto 不倒退超过 5%，JIT off 不受影响。

## 10. R5：优化、阈值和预算调优

### 10.1 目标

在正确性与覆盖面稳定后降低编译成本、代码体积和不必要 guard，使性能目标可以跨负载复现。

### 10.2 任务

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| R5-1 | IR 优化 pass | 常量折叠、复制传播、DCE、不可达块删除 | 优化前后固定种子差分一致 |
| R5-2 | 循环优化 | 纯数值 LICM、局部寄存器化 | 只移动无调用、无属性写、无异常操作 |
| R5-3 | 编译阈值 | calls/backedges/Quick executions 自适应模型 | 短命脚本少编译，长热点仍及时晋级 |
| R5-4 | 编译预算 | 每 VM 时间、队列长度、并发数限制 | 编译风暴下解释执行仍可前进和关闭 |
| R5-5 | 代码缓存 | LRU 权重、热度、两路 PIC 合计计费 | 实际字节不超过预算，淘汰后 RX 释放 |
| R5-6 | trace budget | 延迟、吞吐、safepoint 响应联合校准 | 取消/OOM 响应时间有确定上限 |
| R5-7 | 未命中观测 | 编译收益、guard 率、deopt 率、淘汰率 | `--jit-stats` 能解释是否应晋级/降级 |

### 10.3 性能门禁

| 指标 | 必须达到 | 说明 |
|------|----------|------|
| JIT off | 相对冻结基线回退不超过 2% | JIT 关闭不能为新功能付费 |
| auto 冷启动 | 相对 off 回退不超过 5% | 包含短命 CLI 和 256 函数 benchmark |
| 11 项合计 | 不高于 Node 15x | 5 次中位数、同一构建和机器 |
| mixed | 不高于 Node 4x | 包含进程启动 |
| Native 数值循环 | 不高于 Node 10x | 必须实际命中 Native |
| 编译内存 | 不超过 VM 代码/编译预算 | 包含 pending 和双 PIC 版本 |
| safepoint | 取消/OOM 无无限延迟 | 上限由 trace budget 测试证明 |

目标值是硬门禁；11 项 `<=10x` 和 mixed `<=3x` 作为伸展目标，不能替代稳定性工作。

**完成条件**：Windows/Linux 各自完成正式 5 次中位数报告；所有硬门禁通过；结果离散度和 JIT
统计合理；不存在依赖单一专用形态掩盖综合退化的情况。

## 11. R6：产品化与默认 auto

### 11.1 目标

把已验证的 JIT 从开发期开关转为可回滚、可诊断、跨平台行为明确的正式运行模式。

### 11.2 任务

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| R6-1 | 配置契约 | CLI、嵌入 API、环境变量优先级文档 | off/quick/auto 行为和错误信息稳定 |
| R6-2 | 平台策略 | Windows/Linux amd64 auto；其他平台 Quick/off | 不支持平台不申请 executable memory |
| R6-3 | 预算默认值 | 阈值、trace budget、代码缓存默认值 | 由 R5 数据支持，不硬编码 benchmark 特例 |
| R6-4 | 自动降级 | compile/verify/guard/生命周期错误回退 | 用户代码结果不受影响，错误可观测 |
| R6-5 | 诊断输出 | stats、IR/asm dump、版本/ABI 标识 | 默认无噪声，显式开启时信息完整 |
| R6-6 | 发布与回滚 | release note、关闭 JIT 方法、回滚条件 | 线上问题可用一个开关恢复 Tier 0 |
| R6-7 | 默认切换 | 将支持平台默认改为 auto | 仅在最终检查表全部签署后执行 |

### 11.3 默认 auto 最终检查表

- [ ] R1 所有正确性和 deopt 条目完成；
- [ ] Windows/Linux amd64 连续 5 次 CI 通过；
- [ ] 两个平台 release soak 至少 8 小时通过；
- [ ] 正式 5 次中位数性能报告满足全部硬门禁；
- [ ] race、GC、抢占、LRU、关闭和重配置测试通过；
- [ ] 不支持平台降级测试通过；
- [ ] `--jit=off` 可完全关闭热点状态和 executable memory；
- [ ] verify mismatch 能释放 Native 并安装正确 Quick 结果；
- [ ] 文档、CLI help、嵌入 API 和 release note 已更新；
- [ ] 回滚负责人和触发条件明确。

**完成条件**：检查表无未完成项，并在默认 auto 构建上重新执行全量测试、跨平台 CI、正式基准和
release soak。默认切换本身不允许与新的 opcode/快路径同批进行，便于单独回滚。

## 12. 统一验证矩阵

### 12.1 每个开发任务

```powershell
gofmt -w <changed-go-files>
go test ./internal/engine/jit/... ./internal/engine/interpreter -count=1
go test ./... -count=1
go vet ./internal/engine/jit/... ./internal/engine/interpreter ./internal/engine ./cmd/aluka ./bench
```

涉及共享状态、代码缓存、后台编译、GC 或关闭协议时必须追加：

```powershell
go test -race ./internal/engine/jit/... ./internal/engine/interpreter -count=1
```

### 12.2 平台矩阵

| 平台 | Quick | Native | race | W^X | soak | 发布要求 |
|------|-------|--------|------|-----|------|----------|
| Windows amd64 | 必须 | 必须 | 必须 | VirtualProtect 验证 | 8h | 完整 |
| Linux amd64 | 必须 | 必须 | 必须 | mmap/mprotect + maps | 8h | 完整 |
| Linux arm64 | 必须 | 不要求 | Quick race | 不申请 RX | 1h | 降级 |
| macOS amd64/arm64 | 必须 | 不要求 | Quick race | 不申请 RX | 1h | 降级 |

交叉编译只能证明构建兼容，不能记作实机运行、W^X、GC、抢占或 soak 通过。

### 12.3 性能采集规则

1. 同一 commit 构建一次 CLI，所有 tier 复用该二进制；
2. off/quick/auto 轮换顺序执行，避免温度和频率偏置；
3. 每项至少 5 次取中位数，同时保存所有原始样本；
4. 报告 Node 版本、绝对时间、相对 off、相对 Node、JIT 统计；
5. 单项波动超过 10% 时重查环境，不直接选择较好样本；
6. 性能报告不得把不同日期、不同脚本或不同机器的数据合并为同一结论。

## 13. 交付与评审规则

每个任务的变更说明至少包含：

- 支持的精确语法/值类型和明确不支持项；
- 新增或改变的 guard 与 deopt 点；
- 是否改变 Native ABI、Frame 布局或 executable memory；
- 正向命中、负向回退、异常、副作用和 tier 统计测试；
- 执行过的验证命令及结果；
- 性能数据或“不影响现有 benchmark 形态”的说明；
- 文档实施快照和覆盖矩阵更新。

以下改动必须单独评审，不与普通 opcode 扩展混合：Native ABI 变更、trampoline 变更、默认模式
切换、代码页权限变更、后台编译生命周期变更、deopt map 格式变更。

## 14. 风险与停止条件

| 风险 | 触发信号 | 处理 |
|------|----------|------|
| 语义漂移 | Tier 差分、异常或副作用日志不同 | 关闭对应版本，最小化 seed，先修正确性 |
| Native 不稳定 | crash、hang、非法 PC、抢占失败 | 停止 Native 扩展，保留 Quick |
| RX 泄漏 | VM 关闭后区域/字节不回基线 | 禁止默认 auto，定位所有 ownership 路径 |
| 编译风暴 | pending 队列、CPU 或内存持续增长 | 收紧预算、拒绝缓存和后台并发数 |
| 多态退化 | guard/deopt 率高且反复重编译 | 降温/熔断，不扩大 PIC 无上限 |
| 冷启动倒退 | auto 相对 off 超过 5% | 延迟状态、提高阈值或保持默认 off |
| 综合收益不足 | 单项快但 11 项/mixed 退化 | 回退专用路径或调整成本模型 |

任何硬停止条件都不等于项目失败：Tier 1 Quick 是正式可用层。若 Native 无法满足安全门禁，最终
产品可以在相关平台固定使用 Quick，但不能宣称 Native JIT 完成。

## 15. 推荐执行顺序

近期迭代严格按以下顺序推进：

1. R0-1 至 R0-4：先建立结果和覆盖证据；
2. R1-1、R1-2、R1-8：建立可复现生成式差分；
3. R1-5 至 R1-6：补副作用提交协议与随机 guard 失效；
4. 并行完成 R2 Linux CI、W^X 和短/长 soak；
5. 在差分框架保护下实施 R3 原始值与控制流覆盖；
6. 逐条实施 R4 热点，每条都通过六类必要证据；
7. R5 统一调优阈值、预算和优化 pass；
8. 完成 R6 最终检查表后，单独变更默认 auto。

下一次开发应推进 R1-5 至 R1-7（完成副作用提交协议、随机 guard 失效与 fuzz 入口），复用
jitdiff 框架的事件日志、pending exception 状态与失败产物机制；R0 里程碑的稳定性验收
（安静环境复核）可并行推进，R1/R2 的任何工作不得以 R0 验收未过为由扩大支持面。
