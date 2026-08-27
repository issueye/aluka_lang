# aluka 性能评估报告（v7）

> 日期：2026-08-27 ｜ 当前：commit 2e3d474（main）｜ §10 复测：commit 3c35766
> 对照：docs/performance-report-v6.md（v6，2026-08-15，commit 42a7025）
> 方法：jitbench 三档 off/quick/auto × 5 轮轮转（`tests/benchmark/perf-compare.js` +
> `mixed.js`，结果归档 `bench/results/jit-20260827-windows-amd64.json`）；
> Node v22.23.1 同脚本各 5 次取中位数；引擎内指标另跑 Go 微基准（`go test ./bench -bench . -benchmem`）。

## 1. 概述

本轮为 GUI WebView2 能力迭代后的引擎/运行时性能快照。重点结论：

- **JIT 收益显著且稳定**：auto 相对 Tier 0（off）在计算/调用/属性密集负载上
  **11-55x**（propAccess 55.3x、methodCall 34.6x、propSet 32.4x、callOverhead 29.0x、
  fib30 26.7x），离散度（相对 MAD）普遍 <10%；
- **对 Node 22 合计差距 6.7x**（auto 453.0ms vs Node 67.9ms，11 项）。
  **gcPressure 单例 25.2x 且占 aluka 总耗时 68%**——剔除该项后合计差距仅 **1.6x**；
- **强项稳固**：fib25 **0.92x（反超 Node）**、closureCall 1.12x（近持平）；
  冷启动 45ms vs Node 69ms（快 1.5x）；空闲 RSS 13.3MB vs 28.9MB（省一半）；
- **两处 JIT 盲区负收益**：strConcat auto/off **0.80x**（MAD 2.9%，稳定信号）、
  gcPressure auto/off 0.90x（MAD 8.8%，弱信号，与 v6 §10 勘误口径一致——分配类
  循环含 NEW_OBJECT/NEW_ARRAY 直接拒编）；arrayMap 三档持平（JIT 未接管高阶回调主体）；
- **内存回归信号**：200K 短生命周期对象负载下 aluka RSS 129.7MB vs Node 81.7MB
  （+59%，heapUsed 101.2 vs 34.9MB）——与 v6 §7"稳态 RSS 持平"的口径不同（本轮为
  mem-probe 峰值增长路径），但趋势值得排查，指向对象表示密度与回收节奏。

> **§1–§8 为修复前基线**（commit 2e3d474）。其后落地的 `ArrayValue` 表示优化
> （commit 3c35766）已复测并修订了本文对 gcPressure 的归因——**见 §10**。

## 2. 测试环境

| 项 | 值 |
|----|-----|
| OS | Windows 11（win32 10.0.26200 x64） |
| CPU | 13th Gen Intel i5-13420H（12 逻辑核） |
| Go | go1.25.10 |
| 构建 | `CGO_ENABLED=0 go build -o bin/aluka ./cmd/aluka`（aluka 0.2.0-dev） |
| 对照引擎 | Node v22.23.1（本机） |
| 方法 | jitbench `-reps 5 -rotate`（三档轮转防漂移）；Node 直跑 5 次取中位；各基准串行执行避免 CPU 干扰 |

## 3. 引擎执行：aluka auto vs Node 22.23.1（同机中位）

| 用例 | Node (ms) | aluka auto (ms) | 差距 |
|------|----------:|----------------:|-----:|
| fib25 | 1.86 | 1.72 | **0.92x（反超）** |
| closureCall-1M | 7.51 | 8.41 | **1.12x** |
| fib30 | 6.03 | 9.21 | 1.53x |
| callOverhead-1M | 1.51 | 3.58 | 2.37x |
| methodCall-1M | 1.60 | 4.05 | 2.53x |
| propSet-3M | 2.57 | 7.04 | 2.74x |
| arrayMap-100x10K | 7.01 | 19.25 | 2.75x |
| propAccess-3M | 2.78 | 7.71 | 2.77x |
| arrayPush-1M | 13.71 | 44.31 | 3.23x |
| strConcat-100K | 11.10 | 40.69 | 3.67x |
| gcPressure-500K | 12.17 | 307.00 | **25.2x** |
| **合计（11 项）** | **67.9** | **453.0** | **6.7x** |

**对比 v6**：v6 §3 为 8.8x，v6 §11 报告分阶段数字表示优化后 6.2x——本轮 6.7x 与之
同量级（不同轮次噪声 ~±10%）。差距结构不变：仍由 gcPressure 单项主导（占 aluka
总耗时 68%），其余 10 项合计 146.0ms vs Node 55.7ms，差距仅 ~2.6x。

## 4. JIT 分层效果（jitbench，5 轮中位）

| 用例 | off (ms) | quick (ms) | auto (ms) | auto vs off | auto MAD |
|------|--------:|-----------:|----------:|------------:|---------:|
| propAccess-3M | 426.5 | 243.4 | 7.71 | **55.3x** | 2.5% |
| methodCall-1M | 140.3 | 62.4 | 4.05 | **34.6x** | 5.4% |
| propSet-3M | 227.9 | 143.5 | 7.04 | **32.4x** | 7.5% |
| callOverhead-1M | 103.7 | 59.9 | 3.58 | **29.0x** | 5.3% |
| fib30 | 246.2 | 113.6 | 9.21 | **26.7x** | 1.3% |
| closureCall-1M | 121.0 | 8.22 | 8.41 | **14.4x** | 14.4% |
| fib25 | 19.8 | 8.9 | 1.72 | **11.5x** | 3.5% |
| arrayPush-1M | 391.3 | 47.3 | 44.3 | **8.8x** | 5.6% |
| arrayMap-100x10K | 19.9 | 18.2 | 19.3 | 1.04x | 15.5% |
| gcPressure-500K | 277.5 | 279.7 | 307.0 | **0.90x ⚠** | 8.8% |
| strConcat-100K | 32.7 | 37.7 | 40.7 | **0.80x ⚠** | 2.9% |
| mixed.js | 444.0 | 269.5 | 276.7 | 1.60x | 3.2% |

要点：

- **Native（auto）在循环/调用/属性/整型数组路径全面接管**；quick 单独贡献最大的
  是 closureCall（14.7x，闭包 upvalue 特化生效，native 与 quick 持平）。
- **mixed.js 中 quick（1.65x）略优于 auto（1.60x）**：分配主导的混合负载里
  native 编译开销无对应回报，属预期形态。
- 与 v6 §4 对比，各属性/调用用例的 JIT 提速从 16-44x 上探至 11-55x，Tier 0
  基线与 auto 的剪刀差进一步扩大——R4 系列（PIC/特化路径）持续生效。

## 5. JIT 盲区与回退项（建议跟进）

1. **strConcat auto/off 0.80x（本轮唯一稳定负收益）**：MAD 2.9% 排除噪声。
   v6 §5 同向（当时 0.87x）。建议用 `--jit-stats` 审计字符串拼接路径上
   trace 候选的 guard/deopt 成本，确认是"白编译"还是回退惩罚。
2. **gcPressure 0.90x**：循环含 NEW_OBJECT/NEW_ARRAY，JIT 直接拒绝编译
   （v6 §10 勘误已确认），余下差异为分配主导负载下的噪声 + 少量候选探测开销。
   真正的解法在 GC/分配路径而非 JIT。（**复测更新**：表示优化后 auto/off 翻正为
   0.99x，残余 17.8x 的主因重新定位到 Tier 0 创建/索引路径，见 §10.5。）
3. **arrayMap 三档持平**：Tier 0 的 map 原生快路径已达标，JIT 未对回调主体
   迭代器加速；closureCall 已达 Node 1.12x，可向 map/filter/reduce 循环体延伸特化。
4. **off tier 基线偏低**：propAccess-3M 在 off 下 426.5ms（≈142ns/次读）。
   JIT 已兜住该路径，但 off 是 `--jit=off` 一键回滚档与不支持平台兜底，
   monomorphic IC 命中路径值得单独看一眼。

## 6. Go 微基准（引擎内指标）

`CGO_ENABLED=0 go test ./bench -bench . -benchmem -count=1`（97s，32 项全过）。

### 6.1 双引擎与冷启动

| 指标 | 值 | 说明 |
|------|----:|------|
| fib(30) 字节码 VM | 11.25ms | 298 allocs/op |
| fib(30) AST 解释器 | 2128.8ms | **VM ≈ AST 的 189x**；AST 仅作语义 oracle |
| JIT 冷启动（256 函数，off→auto） | 3.82→3.77ms | auto 无可测冷启动惩罚 |

### 6.2 字节码优化器（OptCompile/OptRun 对拍）

| 负载 | 编译 opt/noopt | 运行 opt/noopt | 运行收益 |
|------|--------------:|---------------:|---------:|
| deadExpr300 | 3.09ms / 1.29ms | **31.7µs / 7.98ms** | **252x**（不可达删除） |
| loopFold | 63.8µs / 35.1µs | 20.5ms / 39.1ms | 1.9x |
| fib30 | 32.4µs / 20.0µs | 253.8ms / 249.7ms | 持平 |
| loopArith | 53.0µs / 28.6µs | 331µs / 314µs | 持平 |

编译期一次性代价 +62%~139%（数十 µs 级），换来死代码/折叠负载的稳态收益——
默认开启（`--no-bytecode-opt` 关闭）的取舍成立。

### 6.3 R4/R7 专项

| 基准 | off | quick | auto | auto/off |
|------|----:|------:|-----:|---------:|
| R4_7ModLoop | 22.5ms | 25.9ms | 2.51ms | **9.0x** |
| R4_7BitwiseLoop | 84.0ms | 43.9ms | 3.35ms | **25.1x** |
| R4_7PowStaysQuick | — | 28.54ms | 28.53ms | quick≈auto（native 按设计拒绝 pow） |
| R4_8SideExit | 7.04ms | 6.88ms | 6.29ms | 1.12x |

注：`BenchmarkJITPropertyPICPolymorphic4` 中 quick/auto（15.9ms）高于 off（3.7ms）
为该微基准口径所致——每迭代新建 VM + Threshold=1，短负载把 trace 编译开销全额计入；
属性路径稳态收益以 §4 CLI 数据（55.3x）为准，非回退。

## 7. 启动与内存

### 7.1 CLI 冷启动（`-e "0"` × 5 中位，bash 毫秒计时）

| 运行时 | 中位 | 样本 |
|--------|-----:|------|
| aluka | **45ms** | 43 / 72 / 44 / 45 / 51 |
| Node | 69ms | 77 / 60 / 63 / 69 / 74 |

注：与 v6 §6 的 31.5ms 口径不同（外部 shell 计时含进程创建与 bash 开销，系统性
偏高），跨运行时同口径对比结论不变：**aluka 快约 1.5x**。

### 7.2 内存（`tests/benchmark/mem-probe.js`）

| 阶段 | aluka RSS | Node RSS | aluka heapUsed | Node heapUsed |
|------|----------:|---------:|---------------:|--------------:|
| baseline | **13.3MB** | 28.9MB | 4.8MB | 3.7MB |
| 200K 对象数组 | 80.0MB | 66.5MB | 67.9MB | 24.0MB |
| +50K 字符串拼接 | 88.1MB | 67.0MB | 76.0MB | 28.2MB |
| +200K 短生命周期对象 | **129.7MB** | 81.7MB | 101.2MB | 34.9MB |

**空闲态 aluka 内存减半，负载态增长显著快于 Node**（最终 RSS +59%、heapUsed ~2.9x）。
与 §3/§5 的 gcPressure 短板同源：对象表示密度（数字装箱、内联槽位）与回收节奏
（CLI 默认 GOGC=400 放宽回收）。v6 §11 的 slab/nan-boxing 工作位于
`perf/nan-boxing` 分支——**若尚未合入 main，可同时解释本轮耗时与内存两处偏离**，
合入计划是最大单项杠杆。

## 8. 结论与优化优先级

1. **GC 与分配路径（收益最大）**：gcPressure 既慢 25x 又贡献总差距的 68%，
   负载态内存 +59% 与之同源。推进分代/增量标记，并明确 `perf/nan-boxing`
   （Stage 1-2.5）合入 main 的时间点；（**复测更新**：数组表示部分已落地，见 §10；
   残余 17.8x 的主嫌改为 Tier 0 值创建与索引存取路径，见 §10.6。）
2. **strConcat JIT 负收益审计**：小而确定的异常（0.80x、MAD 2.9%），
   `--jit-stats` 定位后修复或显式拒绝该 trace 候选；
3. **高阶回调特化延伸**：closureCall 路线（Node 1.12x）已验证，向
   map/filter/reduce 迭代器主体延伸可再收 2.75x 一项；
4. **off tier monomorphic IC 复查**：回滚兜底档的 142ns/属性读偏低；
5. **强项稳固**：启动 1.5x、空闲内存减半、单二进制 26MB、fib25 反超 Node、
   JIT 全链路（三档差分 + fuzz）保持门禁零失配。

## 9. 数据与复现

```bash
# 三档 CLI 基准（自动写 R0-4 归档，本轮产物：
#   bench/results/jit-20260827-windows-amd64.json）
go run ./bench/cmd/jitbench -reps 5 -out bench/results

# Go 微基准
CGO_ENABLED=0 GOWORK=off go test ./bench -bench . -benchmem -count=1

# Node 对照
for i in 1 2 3 4 5; do node tests/benchmark/perf-compare.js; done
node tests/benchmark/mem-probe.js
```

## 10. 修复后复测：ArrayValue 表示优化（commit 3c35766）

针对 §5.2/§8.1 定位的数组建模成本，`internal/engine/value.go` 落地三处表示改动：
`length` 的 writable 由 `ArrayValue.lengthWritable` 布尔位承载（不再为 `{length}`
单条目 eager 建 attrs map，也不占 slots）；`present` 洞位图延迟物化（dense 稳态
`present==nil`）；索引属性 flags 改惰性 map。仅值层，未 bump `FormatVersion`。

### 10.1 方法学与三个测量陷阱

基线用 `git worktree add .worktrees/prefix HEAD` 单独构建修复前二进制，测毕移除，
工作树不受影响。所有 A/B 均为**顺序均衡配对**（奇数轮 pre→post、偶数轮 post→pre），
各 6 次取中位。三个必须避开的坑：

1. **产物过期**：`bin/aluka.exe` 若未随源码重建，测的是旧实现——本轮曾因此一度
   得出"数组案无改善"的错误结论（121.2 vs 121.1MB 的假象）。复测前必须重建。
2. **`perf-compare.js` 的 `timeIt` 无 warmup**：单案相对 MAD 达 2%–54%（methodCall
   甚至出现 0.00 样本）。因此**只有超出噪声带的案可作判据**；无数组参与的案
   （propSet/closureCall/methodCall…）±20% 漂移既不是收益也不是回归。
3. **Node 的标量替换**：不逃逸的对象/数组字面量会被 V8 整体消除（`[]`×500K 仅需
   3.1ms），直测数字假快到失真。成本归因必须用环形 sink（`R[i&1023] = …`）强制
   逃逸，并同时跑**无数组对照组**扣除机器噪声。

### 10.2 同口径耗时（auto 直跑，配对中位，ms）

| 用例 | Node | 修复前 | 修复后 | 后/Node | Δ |
|------|-----:|------:|-------:|--------:|---:|
| fib25 | 1.87 | 1.65 | **1.25** | **0.67x（反超）** | -24.2% |
| closureCall-1M | 6.15 | 8.50 | 9.52 | 1.55x | +12.0% † |
| fib30 | 5.49 | 9.11 | 9.32 | 1.70x | +2.3% † |
| callOverhead-1M | 1.76 | 3.65 | 3.35 | 1.90x | -8.2% † |
| methodCall-1M | 1.81 | 3.00 | 3.65 | 2.02x | +21.7% † |
| propSet-3M | 2.76 | 5.98 | 6.49 | 2.35x | +8.5% † |
| propAccess-3M | 2.97 | 7.46 | 7.34 | 2.47x | -1.6% † |
| arrayMap-100x10K | 7.85 | 16.74 | **14.38** | 1.83x | -14.1% |
| arrayPush-1M | 13.02 | 45.65 | 46.25 | 3.55x | +1.3% † |
| strConcat-100K | 10.18 | 38.70 | 39.61 | 3.89x | +2.4% † |
| gcPressure-500K | 12.23 | 282.55 | **217.80** | **17.8x** | **-22.9%** |
| **合计（11 项）** | **66.1** | **423.0** | **359.0** | **5.4x** | **-15.1%** |

† = 不含数组分配，Δ 落在该案 MAD 带内，不作收益/回归判据。对 Node 合计差距
**6.7x → 5.4x**（§3 口径为轮转档，此处为配对直跑，两者不可直接互比）。

### 10.3 gcPressure 三档与盲区复核（jitbench 轮转，ms）

| 档 | 修复前 | 修复后 | Δ |
|----|-------:|-------:|--:|
| off | 277.5 | 232.5 | -16.2% |
| quick | 279.7 | 250.3 | -10.5% |
| auto | 307.0 | **235.7** | **-23.2%** |
| **auto/off** | **0.90x ⚠** | **0.99x** | **JIT 负收益消除** |

`mixed.js` auto 276.7→274.9（持平）；**strConcat auto/off 0.80x→0.76x，盲区未变**，
与本修复无关（该路径无数组参与），§5.1 仍待办。注：复测轮整体离散度高于上午轮
（methodCall auto MAD 60%），故此处只取比值结论。

### 10.4 内存与表示成本（配对复现，200K 驻留）

| 指标 | 修复前 | 修复后 | Δ | Node |
|------|-------:|-------:|--:|-----:|
| `[i]` 数组 heapUsed | 121.3MB | **68.7MB** | **-43%** | 16.3MB |
| `[i]` 数组 RSS | 133.0MB | **80.3MB** | **-40%** | 50.6MB |
| `{x:i}` 对象 heapUsed（对照） | 40.4MB | 40.4MB | 0% | 11.7MB |
| 字符串 / 装箱数字（对照） | 20.5 / 9.6MB | 20.6 / 9.6MB | 0% | 13.5 / 5.8MB |
| `NewArray(1)` | 520B / 4 allocs | **263B / 1 alloc** | -49% / -75% | — |
| mem-probe `after-200K-array` | 67.9MB | 67.5MB | 0%（该stage无逐元素数组） | 24.0MB |
| mem-probe 末态 heapUsed | 101.2MB | 86.4MB | -15% | 35.4MB |
| mem-probe 末态 RSS | 129.7MB | 112.9MB | -13%（样本 109.2–120.9，修复前为单样本） | 82.0MB |

§7.2 的"负载态内存快于 Node"两项中，**数组主导的那一项已解决一半**（121→69MB，
距 Node 仍 4.2x）；纯对象驻留 40.4 vs 11.7MB（3.5x）与数字/字符串两项未受本修复影响。

### 10.5 成本归因（强制逃逸环形 sink，500K 次，配对）

| 变体 | Node | 修复前 | 修复后 | Δ |
|------|-----:|-------:|-------:|--:|
| `[]` 空数组字面量 | 5.6 | 368.4 | **280.3** | **-23.9%** |
| `[i]` | 8.4 | 367.4 | 293.5 | -20.1% |
| `[i, i+1]` | 7.1 | 388.0 | 316.8 | -18.4% |
| `[]` + `push`×2 | 14.4 | 662.1 | 561.7 | -15.2% |
| `{x, y:{z}, arr:[…]}` | 15.2 | 504.4 | 433.9 | -14.0% |
| `{x:i}`（对照组，无数组） | 5.5 | 276.0 | 271.7 | -1.6% |
| `{x,y,z}`（对照组，无数组） | 6.8 | 296.2 | 282.6 | -4.6% |
| 增长到 1000 的 push（每元素） | 15.8ns | 18.8ns | 19.6ns | 1.2x of Node |

数组案 -14%~-24% 全部远超对照组 -1.6%~-4.6% 的噪声带 → 收益真实且**局限于数组路径**。

### 10.6 归因修订与优先级

- §5.2/§8.1 的方向正确但**量级判断偏乐观**：数组表示确是 gcPressure 的真实成本项
  （-23%），却只解释差距的一小段，修复后仍 **17.8x**。
- **新的主嫌**：纯对象字面量路径完全未受影响（`{x:i}` 迭代含 sink 存/读约 543ns
  vs Node 约 11ns，49x），而含 NEW_OBJECT/NEW_ARRAY 的循环被 JIT 直接拒绝编译，
  于是全部落在 **Tier 0 的值创建 + 索引 set/get** 上。后续优先级第 1 项据此改写为：
  Tier 0 对象/数组字面量创建与索引存取路径（bump-pointer 式分配、字面量批量
  store 融合），或让 JIT 接受含分配的循环 / 提供 native 分配快路径。
- **独立小项**：增长型 push 单元素已 1.2x 接近 Node，但 `arrayPush-1M` 仍 3.6x
  → 大数组 realloc + memcpy 与旧 buffer 回收是另一条路径；strConcat 0.76x 沿用 §5.1。
- **门禁**：engine 模块全测通过、`TestArray*` 9/9（新增 `array_repr_test.go`）；
  conformance node 11/11、build 24/24、webbuild 13/13、vue-sfc 1/1。

### 10.7 复现（§10 专用）

```bash
# 修复前基线二进制（测毕 git worktree remove --force .worktrees/prefix）
git worktree add .worktrees/prefix HEAD
(cd .worktrees/prefix && CGO_ENABLED=0 go build -o bin/aluka-prefix.exe ./cmd/aluka)

CGO_ENABLED=0 go build -o bin/aluka.exe ./cmd/aluka   # 必须重建，见 §10.1-1

# 顺序均衡配对：奇数轮 pre→post，偶数轮 post→pre，各 6 次取中位
for i in 1 2 3 4 5 6; do .worktrees/prefix/bin/aluka-prefix.exe tests/benchmark/perf-compare.js; \
  ./bin/aluka.exe tests/benchmark/perf-compare.js; node tests/benchmark/perf-compare.js; done

# 驻留分阶段（K ∈ obj|arr|str|num）与强制逃逸归因
./bin/aluka.exe stage.js 200000 arr
./bin/aluka.exe escape.js 500000
```

探针脚本未入库（本机临时件）：`stage.js` 按类驻留 200K 数据并报 heapUsed/RSS，
`escape.js` 用 `R[i&1023]=…` 环形 sink 强制逃逸并含无数组对照组。若需长期回归，
建议把二者收进 `tests/benchmark/` 并挂进 conformance 之外的性能守门脚本。
