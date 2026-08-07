# aluka 性能优化实施方案（v3）

> 日期：2026-08-07 ｜ 依据：docs/performance-report-v2.md（评估）+ 池化专项评估（§11）
> 目标：在当前基线（O-5 后，11 用例合计 6146ms，对 node 差距 3x–191x）基础上，
> 分里程碑推进至**整体 -75%**（对 node 差距 ≤30x），并给出长期架构方向。
> 前置基线：commit 569fc3a（O-5 已实施，含字节码缓存 v12 修复）。

## 1. 现状基线（2026-08-07 实测，Intel i5-10500）

| 用例 | 基线 ms（O-5 后） | 对 node 差距 | 主要成本 |
|------|-------------------|--------------|----------|
| fib25 / fib30 | 72 / 787 | 29x / 89x | 递归调用帧 + 算术 |
| propAccess-3M | 1114 | 329x | 属性读 + 装箱 |
| propSet-3M | 601 | 203x | 属性写 |
| strConcat-100K | 43 | 3.1x | rope 节点/平串创建 |
| arrayPush-1M | 540 | 32x | 数组扩容 + 装箱 |
| arrayMap-100x10K | 162 | 16x | 回调每元素全帧解释 |
| callOverhead-1M | 291 | 191x | 帧设置/参数拷贝 |
| closureCall-1M | 401 | 47x | 闭包调用 |
| methodCall-1M | 325 | 188x | 方法调用 |
| gcPressure-500K | 1810 | 176x | 对象创建/GC |
| **合计** | **6146** | — | — |

pprof 热点（调用主导负载，1.19s 采样）：VM.run 17.7%（cum 88.2%）、push 12.6%、
pop 8.4%、mallocgc 4.2%、binAdd 3.4%、ensureStack 3.4%、compareValues 3.4%、
isBigInt 3.4%、convT64 2.5%（cum 17.7%）、doCall 2.5%（cum 13.5%）。

## 2. 优化目标

| 里程碑 | 目标（相对 O-5 基线） | 对 node 差距 | 状态 |
|--------|----------------------|--------------|------|
| M-P0 | 保持 -52%（O-5 已达成） | callOverhead ≤191x | ✅ |
| M-P1 | **累计 -75%**（合计 ~3070ms） | 调用类 ≤60x | 🚧 |
| M-P2 | **累计 -85%** | 调用类 ≤30x，综合 ≤60x | ⬜ |
| M-P3 | 调用类对 node <10x（架构级） | — | ⬜ |

## 3. 任务分解

### P0 —— 已实施/已排除（勿重复投入）

| ID | 任务 | 结论 |
|----|------|------|
| O-5 | 跳过未引用 `arguments` 的函数每帧 arguments 对象创建（编译器 `usedArguments` 检测 → `NoArgumentsObject`，含箭头词法继承；序列化 v12） | ✅ 已实施，**-52%**；回归用例 17-arguments.cjs |
| O-1 | 调用参数切片池（doCall 等 6 处 make 复用） | ❌ 实测无提升（Go 逃逸栈分配 + 池化开销抵消），已回退 |
| O-2 | int→字符串 intern 表 | ❌ 实测无提升（strConcat 瓶颈在 rope/平串创建，非数字转换），已回退 |
| O-3 | 值栈预分配（PF-4） | ✅ 核实已实现：`callClosure` 已 `reserveUndefined(NumLocals)` 帧进入一次预留；剩余优化见 O-4 的栈槽填充成本 |

### P1 —— 调用/回调路径（M-P1，预计 -20~30%）

| ID | 任务 | 方案 | 预期 | 工作量 | 风险 |
|----|------|------|------|--------|------|
| **O-6** | **数组高阶回调原生化**（最高优先级） | `map/filter/reduce/forEach` 已 Go 原生循环，但每元素 `fn.callWith` 走完整调用链（帧+解释）。方案：编译期识别"简单回调"（箭头函数、体为单一表达式、无闭包依赖、参数 ≤2）→ `FuncTemplate.NativeCallback` 微指令描述（CBPushParam/Const/Prop + CBNeg/CBBinOp/CBCmp）；Go 侧数组高阶对该标记回调**直接求值**（`execNativeCallback` 小栈，跳过 callClosure/run），非简单回调回退 `callWith`。 | arrayMap 类 **5-20x**；混合负载 -30%+ | ~300-500 行 | ✅ **已实施（2026-08-07）**：接入 map/filter/forEach/reduce/reduceRight/find/findIndex/some/every/findLast/findLastIndex/flatMap/sort/toSorted/Array.from；修复多参数箭头 nil 条目误拒绝；修复序列化错位（v13）；差分用例 m6-array-hof.cjs（47 断言）+ round-trip 测试 + 编译器检测测试 |
| **O-7** | 方法调用 IC 覆盖回调调用 | 现状 callIC（per-PC 槽，CallCached/CallPut）仅覆盖 `OpCallMethod`；`map` 回调等 `OpCall`（函数值调用）无缓存。方案：`OpCall` 增加 per-PC 函数值槽（同 callee 复用 invoke 结果/快路径）。 | 回调场景 -10% | ~100 行 | 函数值易变（回调每次不同则失效）；需命中率统计门禁 |

### P1 —— 类型分派/指令实现（M-P1 辅助，-5~8%）

| ID | 任务 | 方案 | 预期 | 工作量 |
|----|------|------|------|--------|
| **O-4** | isBigInt 合并进类型分派 | `isBigInt`（1.8% flat，100% 来自 VM.run）在 binAdd/binSub/compareValues 等操作符中独立调用。方案：操作符实现改为一次 type switch 判定 number/bigint/string/object，消除独立 isBigInt 调用与二次分派。 | 算术/比较类 -3~5% | ~80 行 |
| O-4b | push/pop 热路径瘦身 | push 12.6% + pop 8.4%：ensureStack(1) 调用 + 接口装箱。方案：push 内联 ensureStack 快速路径（容量足够时零调用）；评估 `reserveUndefined` 填充用 `copy` 批量写（避免逐槽循环）。 | 综合 -2~4% | ~60 行 |

### P2 —— 架构级（M-P2/M-P3）

| ID | 任务 | 方案 | 预期 | 工作量 | 风险 |
|----|------|------|------|--------|------|
| **O-8** | **值表示宽值化**（NaN-boxing/tagged union） | `engine.Value` 从 Go interface{} 改为 16B 结构体（tag + union：数字直接内嵌、对象为指针），消除 convT64 装箱（2.5% flat / 17.7% cum）与 push/pop/Type() 接口间接调用。全引擎改造（引擎 + 内置 + 全局 API）。可分期：先引擎核心（栈/指令），再内置模块。 | 综合 +15~25%（全用例受益） | 数千行（分期 3-4 个 PR） | 最大回归风险（所有 Value 接触点）；需冻结 API 快照 + 全量 diff/conformance 门禁 |
| **O-9** | rope 展平缓存 | `ropeStringValue.flat atomic.Pointer[string]` 已存在；补：长 rope `String()` 后缓存，重复输出避免重复展平。 | 拼接+输出类 -30% | ~50 行 | 低 |
| **O-10** | JIT 可行性评估（PF-8） | 评估"基线 JIT"（热点函数编译到 Go 闭包）或 tiered 方案；输出 ADR（实现/替代/非目标）。 | 调用类根本性改善（长期） | 探索 | 高风险，先评估后决策 |

## 4. 里程碑与验收

### M-P1：调用/回调路径收窄（O-6 + O-7 + O-4 + O-4b）
**验收**：
- perf-compare 合计 ≤3070ms（-75%）：arrayMap ≤30ms（-80%）、callOverhead ≤150ms、
  methodCall ≤160ms、fib30 ≤400ms；prop/str/array 不倒退（±10% 内）。
- 新增差分用例 m6-array-hof.cjs（map/filter/reduce 简单与复杂回调、稀疏、
  自定义 Symbol.iterator 数组、thisArg、错误传播），与 node 22.23.1 逐行一致。
- 回归：go test 绿、diff 65/65、conformance 17/17；`--monitor` 指令数不倒退。

### M-P2：类型分派 + 架构第一步
**验收**：
- O-8 第一阶段（引擎核心 Value 宽值化）落地；综合 ≥-85%。
- O-9 rope 缓存落地；strConcat 对 node ≤2x。

### M-P3：长期
- O-10 输出 ADR；调用类对 node <10x（如 JIT 或等效方案）。

## 5. 执行顺序与依赖

```
M-P1：O-6（回调原生化，最大头）→ O-7（callIC 覆盖）→ O-4（isBigInt 合并）
      → O-4b（push/pop 瘦身）
M-P2：O-8 分期（引擎核心→内置→全局）→ O-9（rope 缓存）
M-P3：O-10（JIT 评估 ADR）
依赖：O-6 依赖编译器 FuncTemplate 标记（与 O-5 的 NoArgumentsObject 同机制）；
      O-8 依赖全量回归门禁（冻结 API 快照）。
```

## 6. 风险与回退

| 风险 | 应对 |
|------|------|
| O-6 改变回调语义（this/参数/错误） | 仅对可静态判定安全的回调走原生；其余回退 `fn.callWith`；差分用例锁定 |
| O-8 宽值化回归面大 | 分期 PR；每期过 diff 65/65 + conformance 17/17 + go test + bench 不倒退；保留回退提交 |
| 缓存格式变更（O-6 新增 FuncTemplate 字段） | 递增 `bytecode.FormatVersion`（v12→v13）并序列化新字段——O-5 的教训：漏 bump 导致优化被旧缓存掩盖 |
| O-4 操作符分派改动影响 BigInt/混合类型 | 保持现有语义（type switch 与 isBigInt 判定等价）；差分 m2-buffer-typedarray 等算术用例 |

## 7. 复现与门禁

```bash
# 基线/回归
go build -o /tmp/aluka.exe ./cmd/aluka
/tmp/aluka.exe tests/benchmark/perf-compare.js        # 目标合计 ≤3070ms（M-P1）
go test ./bench -bench . -benchmem -benchtime=1s      # 引擎矩阵
go test ./...                                          # 全量
NODE=…/node22.23.1 bash tests/compat/node22/diff/run-diff.sh          # 65/65
NODE=…/node22.23.1 bash tests/conformance/node22/run.sh               # 17/17
# 热点复测
/tmp/aluka.exe --profile p.out <混合负载> && go tool pprof -top p.out
/tmp/aluka.exe --monitor <混合负载>                     # 指令/分配/GC 指标
```

## 8. 关联文档

- 评估：docs/performance-report-v2.md（§2.1 O-5 实测、§11 池化评估）
- 既有计划：docs/perf-memory-optimize-plan.md（PF-1~8 / ME-1~8 任务映射）
- 本次增量任务 ID：O-6/O-7/O-4/O-4b/O-8/O-9/O-10（相对评估报告 §6）
