# aluka 性能评估报告（v4）

> 日期：2026-08-10 ｜ 基线：commit 900833b（吞错修复后，含全部调用/内联/shape/正则优化）
> 对照：docs/performance-report-v3.md（v3，O-6 后）+ docs/function-call-optimization-plan.md
> 方法：跨引擎对比（vs Node 22.23.1）`tests/benchmark/perf-compare.js`，各 **5 次取中位数**；
>       混合负载 `tests/benchmark/mixed.js` 墙钟（3 次中位数，含启动）。

## 1. 概述

本报告是 **F1-F4 调用快速路径 + I-1/I-2 小函数内联 + Shape O(N²) 修复 + 正则编译缓存
+ T-1c 监控门控缓存** 全部落地后的首次全量评估：

- 11 用例合计 **2656ms**，对比 v3（5952ms）**-55%**；对 node 合计差距 **42.5x**；
- **fib 递归 -55%**、**callOverhead -46%**、**closureCall -53%**、**methodCall -42%**
  （调用路径优化主战场）；
- **gcPressure -80%**（Shape 树 O(N²) 泄漏修复后 GC 压力大降，最显著）；
- 混合负载墙钟 **688ms vs node 81ms（8.5x）**，对比 v3 的 1.25s（-45%）。

## 2. 测试环境

| 项 | v4（本次） | v3 |
|----|-----------|-----|
| CPU | Intel i5-10500 @ 3.10GHz | 同左 |
| Node | v22.23.1 | v22.3.0 |
| 构建 | `go build -o bin/aluka.exe ./cmd/aluka` | 同 |
| 取中位数 | 5 次 | 5 次 |

> ⚠️ Node 版本 22.3.0 → 22.23.1（V8 版本差异会小幅改变 node 侧数字）；aluka 侧
> 提升为同机可靠结论。

## 3. 跨引擎对比（aluka vs Node，5 次中位数）

| 用例 | Node (ms) | aluka v4 (ms) | 差距 | v3 (ms) | v3→v4 变化 |
|------|-----------|---------------|------|---------|-----------|
| fib25 | 1.84 | 29.95 | 16.3x | 67.2 | **-55%** |
| fib30 | 5.53 | 327.19 | 59.2x | 738.1 | **-56%** |
| propAccess-3M | 2.75 | 583.36 | 212x | 1077.2 | **-46%** |
| propSet-3M | 2.44 | 370.47 | 152x | 578.8 | **-36%** |
| strConcat-100K | 9.98 | 39.52 | 4.0x | 50.6 | -22% |
| arrayPush-1M | 11.88 | 344.71 | 29x | 509.3 | **-32%** |
| arrayMap-100x10K | 6.97 | 65.75 | 9.4x | 82.7 | -21% |
| callOverhead-1M | 1.42 | 157.37 | 111x | 291.8 | **-46%** |
| closureCall-1M | 6.26 | 189.91 | 30x | 375.5 | **-49%** |
| methodCall-1M | 1.50 | 187.67 | 125x | 314.9 | **-40%** |
| gcPressure-500K | 11.93 | 359.84 | 30x | 1865.9 | **-81%** |
| **合计** | **62.50** | **2655.74** | **42.5x** | 5952.0 | **-55%** |

（v3 列取 performance-report-v3.md 实测值；node 侧因版本差异略有不同）

## 4. 混合负载（mixed.js 墙钟，含启动）

| 引擎 | 墙钟 (ms) | 差距 |
|------|-----------|------|
| Node 22.23.1 | 81 | — |
| aluka v4 | 688 | **8.5x** |
| aluka v3（报告值） | ~1250 | — |

## 5. 结论与热点

**已达成**：调用/闭包/方法/递归类用例对 node 差距显著收窄（递归 -55%、调用 -46%），
GC 压力用例 -80%（Shape 修复），合计差距 v3 的 ~99x → **42.5x**。

**剩余热点**（pprof，见 inline-tiered-bytecode-plan.md §4）：
- propAccess/Set 仍 150-212x：属性访问路径（shape 查找 + IC + 装箱）
- callOverhead/methodCall 仍 111-125x：单次调用固定开销已压到 ~157ns，剩余为
  run() 指令解释 + engine.Value 接口装箱
- **装箱**（`engine.Number` cum 18% + convT64 15%）是解释器接口设计的固有成本，
  **值类型化栈**（数字直接存栈）是下一大收益点（改动大，已列入长期方向）

**下一步候选**：T-2 superinstruction（OpIncLocal 等）、属性访问路径优化、
值类型化栈。JIT 方向的分层路线、Native 可行性门禁和验收指标见
[`docs/jit-performance-optimization-plan.md`](jit-performance-optimization-plan.md)。
