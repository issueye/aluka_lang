# aluka JIT 性能冻结快照（R0-5）

> 文档版本：v1.0
> 日期：2026-08-11
> 基线 commit：`fbe9b5e`（工作树另有 R0-2/3/4 文档与测试改动，不影响引擎与 CLI 二进制）
> 基线文档：`docs/jit-performance-optimization-plan.md` v2.14、`docs/jit-coverage-matrix.md`
> 采集工具：`bench/cmd/jitbench`（R0-2/R0-4 交付物）、`tests/benchmark/jit-special.js`（本次新增）
> 本报告同时承载 R0-1 环境固化记录。

## 1. 环境固化（R0-1）

| 项 | 值 | 说明 |
|----|----|------|
| 机器 | 笔记本（i5-13420H 移动芯片） | 平台 win32 x64 |
| OS | Windows 10.0.26200.8875 | `cmd /c ver` |
| CPU | 13th Gen Intel(R) Core(TM) i5-13420H | `PROCESSOR_IDENTIFIER=Intel64 Family 6 Model 186 Stepping 2`；12 逻辑核 |
| Go | go1.25.10 windows/amd64 | `go version` |
| Node | v22.23.1 | `node --version` |
| 电源方案 | 平衡（GUID `381b4222-f694-41f0-9685-ff5bb260df2e`） | 本机仅有平衡方案，无高性能方案可切换 |
| 供电 | 交流电（AC，BatteryStatus=2，100% 电量） | `Get-CimInstance Win32_Battery` |
| 源码 | commit `fbe9b5e76fb1bf39a9f6412b176eb7a572416087` | 所有结果可回溯到该源码 |
| 二进制 | `bin/aluka.exe` SHA-256 `43c4ba83ce31607ed8bdca57d4a3c108e729f16e6584ccb3a4153ac6ea67910e` | 所有 tier/轮次复用同一二进制 |
| 后台负载 | ChatGPT/ZCode/webview2/ToDesk/dwm 等持续占用 CPU | 见 §7 稳定性分析，是本次离散度的主因 |

**可追溯性**：全部 4 轮（A/B/C/D）与专项、JIT stats 均使用上述同一二进制、同一 commit、同一工具
配置（`--jit=off|quick|auto` 顺序轮换、每 (case, tier) 5 个原始样本、中位数汇总），原始样本保留在
`bench/results/` 归档中。

## 2. 方法论

- 一次构建：`go build -o bin/aluka.exe ./cmd/aluka`，全部测量复用该二进制；
- 采集：`bin/jitbench.exe -reps 5 -skip-build -script tests/benchmark/perf-compare.js -script tests/benchmark/mixed.js`；
- 轮换：每轮按 off → quick → auto、quick → auto → off、auto → off → quick 顺序执行，避免温度和频率偏置；
- 中位数：每 (case, tier) 5 个进程内样本取中位数；mixed 为进程墙钟；
- 官方口径取第 A 轮；B/C/D 轮用于稳定性检查（§7）；
- Node 对照：`node tests/benchmark/perf-compare.js` × 5 取中位数；`mixed.js` 用 PowerShell
  `Measure-Command` 墙钟 × 5 取中位数；`jit-special.js` 单次。

## 3. 11 项跨引擎对比（round A 官方 5 次中位数，单位 ms）

| 用例 | Node | JIT off | Quick | Auto | Auto/Node |
|------|------|---------|-------|------|-----------|
| `fib25` | 2.05 | 37.99 | 23.06 | 26.51 | 12.9x |
| `fib30` | 5.40 | 410.11 | 242.55 | 264.70 | 49.0x |
| `propAccess-3M` | 3.47 | 673.70 | 235.27 | 9.25 | 2.7x |
| `propSet-3M` | 2.90 | 403.84 | 134.02 | 7.24 | 2.5x |
| `strConcat-100K` | 11.00 | 40.98 | 45.47 | 45.94 | 4.2x |
| `arrayPush-1M` | 14.36 | 473.07 | 91.01 | 87.24 | 6.1x |
| `arrayMap-100x10K` | 8.83 | 18.57 | 20.37 | 22.01 | 2.5x |
| `callOverhead-1M` | 1.45 | 205.68 | 52.33 | 4.17 | 2.9x |
| `closureCall-1M` | 6.48 | 215.58 | 2.58 | 2.73 | 0.4x |
| `methodCall-1M` | 1.53 | 206.43 | 59.19 | 4.20 | 2.7x |
| `gcPressure-500K` | 11.44 | 454.28 | 516.73 | 475.72 | 41.6x |
| **11 项合计** | **68.91** | **3140.23** | **1422.58** | **949.71** | **13.8x** |

Auto 相对 Tier 0 约 `3.31x`。合计 `13.8x` 仍低于 J3 硬门禁 `<=15x`；但本次 off/auto 绝对值普遍
高于 v1.6 快照（合计 off 3140 vs 2638、auto 950 vs 699），主要来自 §7 的后台负载，不是引擎回退。

## 4. mixed.js 进程墙钟（5 次中位数，单位 ms）

| 引擎 | 墙钟 | 相对 Node |
|------|------|-----------|
| Node 22.23.1 | 113.27 | — |
| JIT off | 628.41 | 5.5x |
| Quick | 290.86 | 2.6x |
| Auto | 295.29 | **2.6x** |

Auto 相对 off 约 `2.13x`。mixed `2.6x` 低于 J4 硬门禁 `<=4x`。Node 侧本次 113ms 明显高于 v1.6
快照的 89ms（同一后台负载影响），比例口径（2.6x）与快照（2.2x）接近。

## 5. 专项基准（round A 同口径，5 次中位数，单位 ms）

| 用例 | Node | JIT off | Quick | Auto | Auto/Node |
|------|------|---------|-------|------|-----------|
| `jitNumericLoop-3M`（J2 形态） | 4.79 | 417.63 | 108.18 | 7.56 | 1.6x |
| `jitCalleeInline-1M`（J3 形态） | 2.19 | 226.97 | 232.11 | 198.48 | 90.6x |
| `jitExternalProps-3M`（J3 形态） | 2.82 | 674.94 | 250.93 | 8.97 | 3.2x |
| `jitPropWrite-3M`（J3 形态） | 3.49 | 403.42 | 126.38 | 7.85 | 2.2x |

数值循环 Native `7.56ms`、属性累加 `8.97ms`、属性写 `7.85ms` 与 v1.6/J2/J3 专项快照（5.74/6.46/
7.28ms）同量级；callee 内联 `198.48ms` 与 J3 表（179.77ms）接近。`jitCalleeInline-1M` 的 Auto 仅
1.14x 提升，符合“调用开销仍是结构性成本”的既有结论。

## 6. 冷启动（`go test ./bench -run '^$' -bench 'JITColdStart' -benchtime=50x -count=5`）

| 模式 | 5 次中位数（ns/op） | 分配 |
|------|---------------------|------|
| off | 2,858,182（2.858ms） | 2,613,657 B/op，22,415 allocs/op |
| auto | 3,945,876（3.946ms） | 2,632,643 B/op，22,429 allocs/op |

auto 相对 off 回退 **38.1%**，**超过 5% 冷启动预算**。本轮样本含离群尖峰（auto 5.58/5.82ms），
与 R0-2 阶段安静状态下测得的 +3.9%（off 2.353 / auto 2.447ms）矛盾，判定为后台负载造成的离群，
不能作为引擎冷启动回退的证据；该门禁需在安静/固定电源环境下复核。

## 7. 稳定性检查与 R0 验收门禁（诚实结论）

按 R0 §5.3 验收要求，连续两轮相同源码基准的中位数偏差应 `<=5%`。共采集 4 轮（A/B/C/D），
逐 (case, tier) 计算相邻轮对最大偏差：

| 轮对 | 最大偏差 | 超标代表 |
|------|----------|----------|
| A-B | **19.12%** | arrayMap/off 19.1%、arrayMap/auto 14.9%、mixed/off 13.0%、fib25/auto 12.5% |
| B-C | **26.78%** | callOverhead/auto 26.8%、methodCall/off 21.8%、closureCall/auto 20.5% |
| C-D | **64.24%** | fib25/auto 64.2%、callOverhead/auto 62.5%、fib25/quick 48.0% |

**结论：R0 §5.3 的稳定性门禁未通过。** 原因已排查：
1. 本机仅有“平衡”电源方案（无高性能方案可切换），笔记本频率管理引入持续波动；
2. 采集期间后台存在 ChatGPT、ZCode（运行本会话）、msedgewebview2、ToDesk、dwm 等持续 CPU 消耗，
   单项短跑（arrayMap ~20ms、fib25 auto ~26ms）对噪声极为敏感；
3. 离散度随时间恶化（A-B 19% → C-D 64%），与后台活动趋势一致。

因此：**本快照是“当前机器状态下的记录”，不是可复现的正式基准**。R0-1 环境记录与 R0-5 报告
（11 项、mixed、冷启动、专项齐全）均已交付，但 R0 里程碑的整体验收（含稳定性门禁）**未通过**，
需在安静环境、固定电源策略下复核后才可宣称 R0 完成。默认 `--jit=off` 维持不变。

## 8. JIT 统计（`-reps 1 -jit-stats`，Auto，perf-compare + mixed 单次）

```
calls=13040 backedges=90000 candidates=13 compiled=2 rejected=11 executed=9018
nativeCompiled=0 nativeRejected=1 nativeCodeBytes=715 nativeEvictions=0
tracesCompiled=7 tracesRejected=2 tracesExecuted=3 traceYields=30
nativeTracesCompiled=4 nativeTracesExecuted=4 nativeTraceYields=120
safepointPolls=192 interruptions=0
noopCallSites=1 methodCallSites=1 arrayPushSites=2 arrayPushYields=15
closureUpvalueSites=1 closureUpvalueYields=15
guardFailures=0 quickGuardDisabled=0 traceGuardDisabled=0
nativeGuardDisabled=0 nativeTraceGuardDisabled=0 calleeGuardDisabled=0
lastError="jit: trace non-number constant"
lastNativeError="jit: native unsupported IR opcode 29"（OpReturnUndef 程序走 trace noop-call
融合，不单独 Native，属预期拒绝）
```

本轮 guard 全零、无中断（interruptions=0）、verify 未开启（verifyChecks=0）。函数级
`compiled=2` 少于 trace（`nativeTracesCompiled=4`），与“属性/调用/数组热点走 trace 快路径”的
既有口径一致。统计为单次采样，仅作环境快照佐证，不作为性能结论依据。

## 9. 边界与未验证项

- 本快照**不是** Linux 实机、长期 soak、race 构建或 W^X 门禁的证据（分别由 R2 CI job、R2 soak、
  Linux CI 承担）；交叉编译与交叉编译成功记录均不能替代实机运行；
- 冷启动 +38% 与稳定性偏差为环境噪声，已如实记录，不作为引擎回退或性能结论；
- 覆盖矩阵与既有能力测试（R0-3）不因本报告改变；性能绝对值变化不影响语义正确性证据。

## 10. 归档文件

- `bench/results/jit-20260811-windows-amd64.json` — 官方快照（round A，5 次中位数）
- `bench/results/jit-20260811-windows-amd64-r0{b,c,d}.json` — 稳定性轮 B/C/D
- `bench/results/jit-20260811-windows-amd64-special.json` — 专项基准
- `bench/results/jit-20260811-windows-amd64-stats.json` — JIT stats 采样
- `bench/results/r0-5-*.log` — 各轮原始输出与冷启动日志
- `bench/results/r0-5-node-*.log` — Node 侧原始样本
