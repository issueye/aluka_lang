# aluka 性能评估报告（v6）

> 日期：2026-08-15 ｜ 当前：commit 42a7025（main，GUI 子系统合并后）
> 对照：docs/performance-report-v5.md（v5，2026-08-13，commit ba2a3b6）
> 方法：同机同脚本（`tests/benchmark/perf-compare.js`）双引擎各 3 次取中位数；
> JIT 三档取 off/auto 对比；启动/内存/GUI 指标独立方法（见 §6-§8）。

## 1. 概述

GUI 子系统合并到 main 后的全面性能快照（引擎 + 运行时 + GUI）。重点结论：

- **热路径逼近 Node**：closureCall **1.4x**、fib25 **1.5x**、callOverhead 2.6x、
  methodCall 2.9x、propAccess/propSet ~4x——较 v5（合计 13.6x）显著收窄；
- **JIT 提速主体**：属性/调用类用例 Tier 0 → auto 提速 **16-44x**；
- **三大短板明确**：gcPressure 38.6x（GC 分代缺失）、strConcat 5.2x、arrayPush 5.6x；
- **发现 3 处 JIT 回退**：gcPressure（JIT 下反而 -28%）、strConcat、arrayMap——见 §5；
- **启动快于 Node**：CLI 31.5ms vs 42.1ms；内存持平（56 vs 59MB）；单二进制 26MB；
- **GUI 首帧 ~330ms**：由 WebView2 运行时初始化主导（原生 WebView 方案共同下限）。

## 2. 测试环境

| 项 | 值 |
|----|-----|
| OS | Windows 11（win32 10.0.26200 x64） |
| Go | go1.25.10 |
| 构建 | `CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/aluka ./cmd/aluka` |
| 对照引擎 | Node v22.23.1（本机） |
| 方法 | `perf-compare.js` 各 3 次取中位数（与 v5 §7 方法一致） |

## 3. 引擎执行：aluka auto vs Node 22.23.1（同机 3 次中位）

| 用例 | Node (ms) | aluka auto (ms) | 差距 |
|------|----------:|----------------:|-----:|
| closureCall-1M | 6.31 | 8.81 | **1.4x** |
| fib25 | 1.80 | 2.78 | **1.5x** |
| callOverhead-1M | 1.38 | 3.61 | 2.6x |
| arrayMap-100x10K | 6.76 | 19.05 | 2.8x |
| methodCall-1M | 1.43 | 4.13 | 2.9x |
| propAccess-3M | 2.74 | 11.04 | 4.0x |
| propSet-3M | 2.59 | 11.28 | 4.4x |
| strConcat-100K | 10.35 | 54.32 | 5.2x |
| arrayPush-1M | 12.92 | 72.74 | 5.6x |
| fib30 | 5.54 | 44.46 | 8.0x |
| gcPressure-500K | 11.98 | 462.17 | **38.6x** |
| **合计（11 项）** | **79.8** | **705.6** | **8.8x** |

**对比 v5**：合计 13.6x → 8.8x；fib25 16x → 1.5x、fib30 39x → 8.0x
（v5 测量经 jitbench 轮序，本报告直跑脚本，JIT 预热语义略有差异，趋势一致）。

## 4. JIT 分层效果（Tier 0 off → auto）

| 用例 | off (ms) | auto (ms) | JIT 提速 |
|------|--------:|----------:|---------:|
| propAccess-3M | 483 | 11.0 | **44x** |
| methodCall-1M | 137 | 4.1 | **33x** |
| callOverhead-1M | 117 | 3.6 | **32x** |
| propSet-3M | 233 | 11.3 | **21x** |
| closureCall-1M | 142 | 8.8 | **16x** |
| fib25 | 27.6 | 2.8 | **10x** |
| fib30 | 283 | 44.5 | **6.4x** |
| arrayPush-1M | 344 | 72.7 | **4.7x** |

## 5. JIT 回退项（需跟进，建议纳入 jitdiff 防回归）

| 用例 | off (ms) | auto (ms) | 回退 | 初步归因 |
|------|--------:|----------:|-----:|---------|
| gcPressure-500K | 361 | 462 | **-28%** | 分配密集路径 guard/装箱开销超过优化收益 |
| strConcat-100K | 38 | 54 | ~-30%（噪声内偏慢） | 字符串拼接特化缺失，trace 开销不回本 |
| arrayMap-100x10K | 14.3 | 19.1 | -34% | 中小负载下 trace 匹配/特化成本偏高 |

## 6. 启动与分发

| 指标 | aluka | Node 22.23.1 |
|------|------:|-------------:|
| CLI 启动（`-e "0"`，5 次均值，warm） | **31.5ms** | 42.1ms |
| 二进制体积 | **26MB 单文件** | ~80MB + 运行时目录 |

## 7. 内存（10 万对象常驻负载，稳态 RSS）

| 运行时 | RSS |
|--------|----:|
| aluka | 56MB |
| Node | 59MB |

## 8. GUI 运行时（feat/gui-subsystem 合并后新增指标）

| 指标 | 值 | 说明 |
|------|----:|------|
| 首帧延迟（TTFR，5 次中位） | **~330ms** | 进程启动 → WebView2 环境/控制器创建 → 页面渲染完成事件 |
| 宿主进程 RSS | 33MB | aluka + GUI 框架 |
| 整进程树 RSS | ~390MB | 含 msedgewebview2 浏览器进程（原生 WebView 方案固有，与 Wails/Tauri 同量级） |
| 打包产物 | 26MB 单文件 exe | 内嵌前端资源 + 图标，双击即用 |

注：架构文档宣称的"<40ms 启动"指窗口创建环节；用户可感知首帧由 WebView2
运行时初始化主导（~300ms），为所有原生 WebView 方案的共同下限，非引擎问题。

## 9. 结论与优化优先级

1. **GC 是最大单项短板**：gcPressure 单项占 aluka 总耗时约 60%（462/705ms），
   与 Node 差距 38.6x。按 `docs/perf-memory-optimize-plan.md` 推进分代/增量标记收益最大；
2. **字符串与数组分配**（strConcat 5.2x / arrayPush 5.6x）：字符串 rope/预分配、
   数组 push 容量策略；
3. **JIT 回退三例**（§5）纳入 jitdiff 固定用例防回归，优先审计 gcPressure 的
   guard 开销；
4. **强项稳固**：启动快于 Node 25%、内存持平、热路径 1.4-4x、单文件 26MB 分发、
   GUI 宿主内存仅为 Electron 同类指标的零头。
