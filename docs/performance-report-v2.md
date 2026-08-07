# aluka 性能评估报告（v2）

> 日期：2026-08-07 ｜ 基线：commit 9bfd56b（M7-M9 + 监控器之后）
> 对照：docs/performance-report.md（v1，2026-08-06，O2 之后）
> 方法：跨引擎对比（vs Node 22.23.1）+ pprof 热点 + --monitor 运行时指标

## 1. 测试环境

| 项 | v2（本次） | v1（旧报告） |
|----|-----------|--------------|
| CPU | Intel i5-10500 @ 3.10GHz | AMD Ryzen 7 5700U |
| Node | v22.23.1 | v22.23.1 |
| 构建 | `go build ./cmd/aluka` | 同 |

> ⚠️ 两版 CPU 不同，绝对耗时仅作趋势参考；**分配数/占比/差距倍数**可比。

## 2. 跨引擎对比（aluka vs Node 22.23.1）

`tests/benchmark/perf-compare.js`，固定迭代量，`process.hrtime.bigint()` 计时。

| 用例 | Node (ms) | aluka v2 (ms) | 差距 v2 | 差距 v1 | aluka 自身变化 |
|------|-----------|---------------|---------|---------|----------------|
| fib25 | 2.30 | 279 | 121x | 110x | 368→279（-24%） |
| fib30 | 8.36 | 3695 | 442x | 533x | 4810→3695（-23%） |
| propAccess-3M | 3.32 | 1091 | 329x | 336x | 1296→1091（-16%） |
| propSet-3M | 2.95 | 599 | 203x | 286x | 772→599（-22%） |
| strConcat-100K | 13.61 | **42** | **3.1x** | 463x | 9278→42（**-99.5%**） |
| arrayPush-1M | 16.42 | 525 | 32x | 31x | 619→525（-15%） |
| arrayMap-100x10K | 9.45 | 153 | 16x | 18x | 196→153（-22%） |
| callOverhead-1M | 1.57 | 1547 | 985x | 764x | 1871→1547（-17%） |
| closureCall-1M | 8.46 | 394 | 47x | 51x | 439→394（-10%） |
| methodCall-1M | 1.68 | 1604 | 954x | 638x | 1889→1604（-15%） |
| gcPressure-500K | 12.11 | 2126 | 176x | 136x | 3030→2126（-30%） |

**结论**：
1. **全部用例 aluka 侧绝对耗时改善 10%–99.5%**（绳串/对象批量构造/指令分派优化的累积）。
2. **strConcat 从 463x 收窄到 3.1x**——ME-1 rope 字符串（commit 107b617）近乎追平 V8，内存从 259MB → 1.2MB（-99.5%）。
3. 调用主导用例（callOverhead/methodCall/fib）差距仍最大（442x–985x）——v2 中 node 端数字更小（JIT 更强），差距被放大；**调用路径仍是第一优化面**。

## 3. 引擎基准矩阵（go test ./bench）

| 基准 | v1 耗时 | v2 耗时 | 分配 v2 | 说明 |
|------|---------|---------|---------|------|
| FibVM (fib30) | 4.57s | 3.71s | 35.8M | 每递归帧仍分配 |
| FibVMSmaller | 37.6ms | 30.9ms | 291K | |
| PropAccess | 44.3ms | 38.3ms | 600K | IC get 命中 ~91% |
| PropSet | 28.4ms | 24.0ms | 300K | |
| MethodCall | 182.8ms | 141.4ms | 1.10M | 每调用 make(args) 分配 |
| StrConcat | 136.0ms | **4.6ms** | 80K | rope；80K 分配仍偏高 |
| ArrayPush | 68.9ms | 53.4ms | 1.10M | 每 push 装箱 |
| ArrayMap | 50.0ms | 35.7ms | 911K | 回调解释执行 |
| CallOverhead | 174.7ms | 150.0ms | 1.20M | 每调用 make(args)+arguments 对象 |
| ClosureCall | 45.3ms | 37.7ms | 600K | |
| GCPressure | 259ms | 179ms | 954K | |

## 4. 热点分析（pprof，混合负载 15.3s 采样）

| 函数 | flat% | 说明 |
|------|-------|------|
| `VM.run` | 13.5% (cum 73.6%) | 指令分派（已内联 Decode） |
| `scanobject` (GC) | 5.9% | 对象图扫描 |
| `callClosure` | 5.8% (cum 73.6%) | 调用链 |
| `push` | 5.2% | 值栈压入 |
| `mallocgcTiny` | 4.7% | 小对象分配 |
| `mallocgc` | 4.5% | 常规分配 |
| `typePointers.next` | 3.5% | GC 指针扫描 |
| `mallocgcSmallScanNoHeader` | 3.3% | 小对象分配+扫描 |
| `reserveUndefined` | 2.7% | 栈槽填充 |
| `convT64` | 2.2% | **数值装箱**（engine.Number/IntValue 100% 来自此） |
| `isBigInt` | 1.8% | 算术指令中的 BigInt 判定 |
| `ensureStack` | 2.0% | 值栈扩容拷贝 |

**分配 + GC 合计 ≈ 25%**（scanobject+mallocgcTiny+mallocgc+typePointers+mallocgcSmallScan+convT64+greyobject+findObject），与 v1 持平——**仍是最大可优化面**。v2 新增可识别热点：`convT64`（Value 接口装箱）、`isBigInt`（类型分派）、`args := make([]engine.Value, numArgs)`（每次调用）。

## 5. 运行时指标（--monitor，混合负载 16.7s）

```
指令 386.7M（23.2 M/s）｜ 调用 358K ｜ 分配 59K 对象
IC 命中  get 2,999,997/3,300,607（90.9%）  set 0/0  call 0/300,301
heap 峰值 51 MB ｜ GC 326 次，暂停累计 11.7ms（0.07%）
```

- 解释器吞吐 ~23M 指令/s；GC 暂停占比可忽略（0.07%）。
- **call IC 全 miss**（0/300K）：map/filter 回调走普通 `OpCall`，未命中 `callIC` 槽——方法调用缓存覆盖面不足。

## 6. 优化点清单（按 pprof 占比与预期收益）

### P0 —— 分配/GC 削减（合计 ~25%，先做低垂果实）

| # | 优化点 | 依据 | 预期 | 工作量 |
|---|--------|------|------|--------|
| O-2 | **小整数→字符串 intern**：`'x' + i` 中 `i→string` 每次 `strconv.FormatInt` 分配；缓存 -1e6..1e6 | StrConcat 80K 分配/10K 次拼接仍偏高 | 拼接类再降 30-50% | ~50 行 |
| O-3 | **值栈预分配**（PF-4）：编译期函数最大栈深，帧进入一次预留 | `ensureStack` 2.0% + `reserveUndefined` 2.7% | 综合 -3~5% | ~150 行 |
| O-4 | **isBigInt 合并进类型分派**：操作符实现一次 type switch 判定 number/bigint/string | `isBigInt` 1.8%（100% 来自 VM.run） | 算术类 -2~4% | ~80 行 |
| O-1 | ~~调用参数切片复用~~ | **实测 doCall 的 make(args) 仅 0.31% cum——不值得单独做**（MethodCall 热点在回调执行，见 O-6） | — | — |

### P0 —— 调用/回调路径（差距最大的用例面）

| # | 优化点 | 依据 | 预期 | 工作量 |
|---|--------|------|------|--------|
| O-6 | **数组高阶回调原生化**（PF-5）：`map/filter/reduce/forEach` 对原生 ArrayValue + **简单回调（箭头函数单表达式、无闭包依赖）走 Go 侧直接执行**，跳过每元素完整帧+解释 | doCallMethod cum **71%**（混合负载 9000 万次回调调用全走解释器） | arrayMap 类 **5-20x**（混合负载大头） | ~300 行 |
| O-5 | **callClosure 快速路径**（PF-1）：无闭包捕获/少参数函数跳过帧初始化 + **跳过 arguments 对象创建**（每帧 `NewArray`，line 1634） | callClosure 5.8% flat；fib30 35.8M 分配 | fib/调用类 -20~40% | ~300 行 |
| O-7 | **方法调用 IC 覆盖回调**：`map` 回调等 `OpCall` 场景复用 callIC 槽 | call IC 0/300K miss | 回调场景 -10% | ~100 行 |

### P1 —— 架构级（长期）

| # | 优化点 | 依据 | 预期 | 工作量 |
|---|--------|------|------|--------|
| O-8 | **值表示宽值化（NaN-boxing/tagged union）**：`Value` 从 interface{} 改为 16B struct，消除 `convT64` 装箱与接口间接调用 | convT64 2.2% + push 5.2% + 全接口派发 | 综合 10-20%（最大收益） | 数千行（架构级） |
| O-9 | **rope 展平缓存**：`String()` 多次调用重复展平同一 rope | 长串拼接后反复输出场景 | 拼接输出类 -30% | ~50 行 |
| O-10 | **JIT 评估**（PF-8）：基线 JIT 或 tiered 可行性 | 调用类差距 442-985x 的根本解 | 根本性 | 探索 |

## 7. 建议执行顺序

```
回调面（最大头）：O-6（简单回调 Go 侧直接执行）→ O-7（callIC 覆盖）
调用面：O-5（调用快速路径+去 arguments）→ O-4（isBigInt）→ O-3（栈预分配）
拼接面：O-2（int→str intern）
架构：O-8（宽值）需专项评估；O-9 随手；O-10 留 P2
```

## 8. 复现

```bash
go build -o /tmp/aluka.exe ./cmd/aluka
/tmp/aluka.exe tests/benchmark/perf-compare.js        # vs node 22.23.1
go test ./bench -bench . -benchmem -benchtime=1s      # 引擎矩阵
/tmp/aluka.exe --profile p.out tests/benchmark/mixed.js  # pprof
go tool pprof -top p.out
/tmp/aluka.exe --monitor tests/benchmark/mixed.js     # 运行时指标
```

## 9. 与 v1 报告的差异说明

- 新增 --monitor/--max-memory 运行时观测能力（可复现本文 §5 数据）。
- strConcat 由 v1 的"最大内存黑洞"变为"最接近 node 的用例"（463x→3.1x，
  内存 259MB→1.2MB），归因 ME-1 rope 字符串 + ME-2 对象批量构造（commit 107b617）。
- 调用/回调路径取代字符串拼接成为第一优化面；混合负载中 map 高阶回调
  占 doCallMethod cum 71%，是当前最集中的单一热点。
