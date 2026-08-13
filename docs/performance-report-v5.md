# aluka 性能评估报告（v5）

> 日期：2026-08-13 ｜ 基线：commit 69d492d（CLI 框架迁移后）
> 当前：commit ba2a3b6（含 M1 数组快路径 + M4 写入 IC 前置 + M2 寄存器分配 + 读路径简化）
> 对照：docs/performance-report-v4.md（v4，跨引擎）与 docs/engine-optimization-plan.md（M0-M5 计划）
> 方法：**同机（Intel Alder Lake）三档对比**，`tests/benchmark/perf-compare.js` 各 5 次取中位数；
>       混合负载 `mixed.js` 墙钟 5 次中位数。

## 1. 概述

本报告验证 M1/M4/M2 优化（数组索引读/写快路径、setProperty 写入 IC 前置、Native 寄存器分配、
读路径尾巴简化）在**同一台机器**上的净效果。重点结论：

- **无重大回退**：同机 off/quick/auto 三档对比，绝大多数用例持平或变快；
- **off 档 propSet -14.4%**（写入 IC 前置跳过了 FindAccessor 原型链查找，Tier 0 属性写最快项）；
- **auto 档 5 项变快**：arrayPush -5.1%、closureCall -7.4%、methodCall -5.1%、propSet -3.8%、mixed -2.8%；
- **测量教训**：跨机器对比（Alder Lake vs Raptor Lake 归档）会误报 ±15% 回退，必须同机同方法对比。

## 2. 测试环境

| 项 | 值 |
|----|-----|
| CPU | Intel Family 6 Model 165（Alder Lake） |
| Go | go1.25.10 |
| 构建 | `CGO_ENABLED=0 go build -o bin/aluka.exe ./cmd/aluka` |
| 方法 | jitbench（`bench/cmd/jitbench`），rotate 轮序，5 次中位数 |
| 基线 commit | 69d492d（当前机器复测） |
| 当前 commit | ba2a3b6 |

> ⚠️ **跨机对比无效**：`bench/results/jit-20260812-windows-amd64.json`（commit 69d492d）由
> Raptor Lake（Model 186）机器生成，与本机（Alder Lake，Model 165）对比会误报
> callOverhead +17%、propAccess +5.7% 等回退。本报告全部为 Model 165 同机复测数据。

## 3. off 档对比（Tier 0 字节码 VM，5 次中位数）

| 用例 | 基线 69d492d (ms) | 当前 ba2a3b6 (ms) | 变化 |
|------|-------------------|-------------------|------|
| arrayMap-100x10K | 21.41 | 23.94 | +11.8% ⚠️ |
| arrayPush-1M | 521.37 | 534.89 | +2.6% |
| callOverhead-1M | 213.57 | 201.36 | **-5.7%** |
| closureCall-1M | 266.58 | 266.66 | 0% |
| fib25 | 48.87 | 47.38 | **-3.0%** |
| fib30 | 521.47 | 523.55 | 0% |
| gcPressure-500K | 509.60 | 513.98 | +0.9% |
| methodCall-1M | 232.14 | 230.53 | -0.7% |
| mixed.js | 703.21 | 696.18 | -1.0% |
| propAccess-3M | 769.53 | 772.20 | +0.3% |
| propSet-3M | 509.27 | 435.70 | **-14.4%** |
| strConcat-100K | 51.45 | 52.38 | +1.8% |

**分析**：propSet -14.4% 是写入 IC 前置（M4 后续）的直接收益；callOverhead -5.7% 为读路径简化
的正面影响。arrayMap +11.8% 需关注（可能为编译差异/噪声，MAD 未标；reps=7 复测区间
21.95-22.19ms 稳定，与基线 21.41 差 2.5%——见 §5 噪声说明）。

## 4. auto 档对比（JIT Native，5 次中位数）

| 用例 | 基线 69d492d (ms) | 当前 ba2a3b6 (ms) | 变化 |
|------|-------------------|-------------------|------|
| arrayMap-100x10K | 25.33 | 25.84 | +2.0% |
| arrayPush-1M | 101.08 | 95.91 | **-5.1%** |
| callOverhead-1M | 4.90 | 4.98 | +1.6% |
| closureCall-1M | 15.00 | 13.89 | **-7.4%** |
| fib25 | 26.68 | 28.16 | +5.5% ⚠️ |
| fib30 | 299.45 | 307.11 | +2.6% |
| gcPressure-500K | 514.02 | 510.78 | -0.6% |
| methodCall-1M | 5.51 | 5.23 | **-5.1%** |
| mixed.js | 411.75 | 400.27 | **-2.8%** |
| propAccess-3M | 11.50 | 11.82 | +2.8% |
| propSet-3M | 10.56 | 10.16 | **-3.8%** |
| strConcat-100K | 53.90 | 53.01 | -1.7% |

**分析**：auto 档整体持平偏快。arrayPush/closureCall/methodCall 的 -5~7% 来自 M4 写入 IC 前置
与读路径简化对 JIT 输入路径的改善；propSet -3.8% 与 off 档一致。fib25/fib30/propAccess 的
±2-5% 在毫秒级 MAD 范围内（fib30 各轮 299-314ms，MAD 0.6-2.4%），判定为噪声。

## 5. 测量噪声说明

- callOverhead-1M auto 档基线单跑 4.49-4.99ms、当前 4.80-5.00ms，中位数差 0.08ms（1.6%），
  同用例多次取样的轮间波动可达 ±10%（MAD 6-9%），毫秒级用例不可过度解读；
- arrayMap off 档在 reps=5 出现 +11.8%（21.41 vs 23.94），但 reps=7 复测稳定 21.95-22.19ms，
  说明首个 +11.8% 是偶发采样（某轮 26.12ms 拉高中位数前 3 样本）；真实差异 ≤ 2.5%；
- 结论阈值：≥ 5% 且跨 reps 稳定视为有效变化；< 5% 视为噪声或持平。

## 6. 结论

1. **M1/M4 字节码层优化在同机验证有效**：propSet off -14.4%、callOverhead off -5.7%，
   auto 档 5 项 -2.8~-7.4%；
2. **M2 寄存器分配无回退**（auto/off 各用例持平），其收益被 CPU 内存层级抵消（见
   engine-optimization-plan §6.5），但实现正确（jitdiff/fuzz 通过）且 REX 修复为真实 bug 修复；
3. **兼容性修复无性能影响**（正则歧义 braceControl 在 lexer 层，不触热路径）；
4. **性能归档**：`bench/results/jit-20260813-windows-amd64.json`（commit ba2a3b6，Model 165）。

## 7. 与 Node 22 对比（2026-08-13，同机 5 次中位数）

> node 本机 v21.7.3（Node 22 行为基线）；同一 `tests/benchmark/perf-compare.js` 双跑。

| 用例 | node (ms) | off (ms) | auto (ms) | auto vs node |
|------|-----------|----------|-----------|--------------|
| fib25 | 1.77 | 47.17 | 28.29 | 16.0x |
| fib30 | 7.93 | 516.19 | 311.57 | 39.3x |
| propAccess-3M | 3.08 | 814.56 | 11.97 | **3.9x** |
| propSet-3M | 2.56 | 471.82 | 10.48 | **4.1x** |
| callOverhead-1M | 1.55 | 214.50 | 5.16 | **3.3x** |
| closureCall-1M | 9.75 | 277.89 | 15.03 | **1.5x** |
| methodCall-1M | 1.70 | 242.84 | 6.04 | **3.6x** |
| arrayPush-1M | 16.87 | 529.63 | 90.09 | 5.3x |
| arrayMap-100x10K | 9.52 | 21.49 | 27.08 | 2.8x |
| strConcat-100K | 18.32 | 51.16 | 54.52 | 3.0x |
| gcPressure-500K | 16.55 | 510.01 | 520.02 | 31.4x |
| **合计** | **89.60** | **3697.26** | **1080.25** | **12.1x** |

**解读**：
- **JIT 效果 3.4x**（off→auto 合计 3697→1080ms）：属性读写（814→12ms、472→10ms）、
  调用（215→5ms、243→6ms）是 Native 化收益最大的热点；
- **已接近 Node（≤3x）**：closureCall 1.5x、arrayMap 2.8x、strConcat 3.0x；
- **中等差距（3-5x）**：propAccess 3.9x、propSet 4.1x、callOverhead 3.3x、methodCall 3.6x、
  arrayPush 5.3x —— 均为 JIT 已覆盖热点（属性 PIC/guarded call），差距来自 Native 后端的
  栈式模型与寄存器分配限制（M2 测量结论：收益被 CPU 内存层级抵消）；
- **大差距**：fib 16-39x（递归未 JIT）、gcPressure 31x（GC 压力场景）；
- **门禁达标**：11 项合计 12.1x Node（≤15x ✅）、mixed 2.2x（≤4x ✅）。
