# aluka 性能评估报告（v3）

> 日期：2026-08-07 ｜ 基线：commit 2c59ada（O-6 数组高阶回调原生化之后）
> 对照：docs/performance-report-v2.md（v2，O-5 后）+ docs/perf-optimization-plan.md（v3 计划）
> 方法：跨引擎对比（vs Node 22.3.0）+ 同机 O-5↔O-6 增量对照（git worktree 重建 O-5 二进制）
>       + 引擎基准矩阵 + pprof 热点 + --monitor 运行时指标

## 1. 概述

本次报告聚焦 **O-6（数组高阶回调原生化，commit 2c59ada）** 的实测收益：

- perf-compare 11 用例合计 **6146 → 5952ms（-8%）**；arrayMap **159 → 82.7ms（-48%）**，
  对 node 差距 **16x → 8.4x**；
- 混合负载指令数 **44.6M → 28.6M（-36%）**（回调体不再解释执行），墙钟 **1.51 → 1.25s（-17%）**；
- 热点结构改变：`callClosure` cum 83.3%、`convT64` cum 16.4% **双双退出 pprof 顶部**，
  新进 `execNativeCallback`（O-6 原生回调路径）；
- **新发现热点：全局弱引用对象注册表（jsHeap）占 flat ~6% / cum ~10%**，O-5/O-6 均存在，
  与本次优化无关，是下一个可攻击的分配路径热点（建议 O-11）。

## 2. 测试环境

| 项 | v3（本次） | v2（旧报告） |
|----|-----------|--------------|
| CPU | Intel i5-10500 @ 3.10GHz | 同左 |
| Go | go1.25.10 windows/amd64 | — |
| Node | **v22.3.0**（nvmd 本机唯一 22.x） | v22.23.1 |
| 构建 | `go build -o /tmp/aluka.exe ./cmd/aluka` | 同 |
| 对照二进制 | O-5 版本由 worktree `c89cd40` 重建（同机同日） | — |

> ⚠️ Node 由 22.23.1 换为 22.3.0（V8 版本差异会小幅改变 node 侧数字，
> 本报告 node 列均为本次实测）；O-5↔O-6 增量为**同机同日同参数**严格对照。

## 3. 跨引擎对比（aluka vs Node 22.3.0）

`tests/benchmark/perf-compare.js`，固定迭代量，`process.hrtime.bigint()` 计时。
aluka 列取 **5 次中位数**；O-5 列由 O-5 二进制本次实测（单次）。

| 用例 | Node (ms) | aluka v3 (ms) | 差距 v3 | 差距 v2(O-5) | O-5→v3 变化 |
|------|-----------|---------------|---------|--------------|-------------|
| fib25 | 1.45 | 67.2 | 46x | 29x | 72.0→67.2（-7%） |
| fib30 | 8.14 | 738.1 | 91x | 89x | 797.9→738.1（-7%） |
| propAccess-3M | 3.30 | 1077.2 | 326x | 329x | 1120.8→1077.2（-4%） |
| propSet-3M | 2.89 | 578.8 | 200x | 203x | 622.2→578.8（-7%） |
| strConcat-100K | 13.28 | 50.6 | 3.8x | 3.1x | 49.0→50.6（+3% 噪声） |
| arrayPush-1M | 16.21 | 509.3 | 31x | 32x | 532.6→509.3（-4%） |
| **arrayMap-100x10K** | 9.85 | **82.7** | **8.4x** | **16x** | **159.1→82.7（-48%）** |
| callOverhead-1M | 1.54 | 291.8 | 189x | 191x | 302.6→291.8（-4%） |
| closureCall-1M | 8.27 | 375.5 | 45x | 47x | 405.5→375.5（-7%） |
| methodCall-1M | 1.54 | 314.9 | 204x | 188x | 328.6→314.9（-4%） |
| gcPressure-500K | 14.88 | 1865.9 | 125x | 176x | 2076.4→1865.9（-10%） |
| **合计** | **81.4** | **5952.0** | — | — | **6466.6→5952.0（-8%）** |

**结论**：
1. **O-6 直接收益集中于 arrayMap**（-48%，差距 16x→8.4x 减半）；其余用例在 ±3~10% 噪声内。
2. 累计进度（本机）：O-5 前 12713 → O-5 后 6146（-52%）→ O-6 后 5952（**累计 -53%**）。
3. M-P1 里程碑目标（合计 ≤3070ms / arrayMap ≤30ms）**未达成**——O-6 单点收益被
   "输出数组构造 + 数值装箱"限制在 ~2x，需 O-8（宽值消除 convT64）进一步压缩。

## 3.1 O-6 增量实测（同机同参：仅差 O-6 改动）

| 负载 | O-5 二进制 | v3（O-6） | 变化 |
|------|-----------|-----------|------|
| perf-compare 合计 | 6466.6ms | 5952.0ms | **-8.0%** |
| perf-compare arrayMap | 159.1ms | 82.7ms | **-48.5%** |
| mixed.js 墙钟（无 monitor） | 1.51s | 1.25s | **-17%** |
| mixed.js 指令数（monitor） | 44.59M | 28.59M | **-36%** |
| mixed.js 调用/分配 | 378,214 / 152,358 | 同 | 0（回调不走 OpCall，计数器不含元素级） |
| mixed.js GC 次数 | 42 | 36 | -14% |
| mixed.js heap 峰值 | 43.5 MB | 47.3 MB | +9%（O-6 输出数组略增） |

## 4. O-6 专项验证（原生回调路径）

检测范围（compiler/native_callback.go）：**箭头函数**、参数 ≤2、无闭包依赖，
体为**单表达式或单 return 块体** → 编译期生成 NativeCallbackDesc 微指令
（CBPushParam/Const/Prop + CBNeg/CBBinOp/CBCmp），运行时 `execNativeCallback`
小栈直求值，跳过每元素完整调用链。函数表达式/复杂回调回退解释器。

实测（200×10K，本机）：

| 回调形态 | 墙钟 (ms) | 指令数 | 路径 |
|----------|-----------|--------|------|
| `x => x * 2`（简单箭头） | 186 | **154K** | O-6 原生 |
| `x => { return x * 2; }`（块体箭头） | 188 | — | 命中（单 return 也检测） |
| `(x, i) => x + i`（双参箭头） | 198 | — | 命中 |
| `function(x){ return x*2; }`（函数表达式） | 335 | **8.15M** | 回退解释器 |
| `x => x % 2 === 0`（filter 简单） | 364 | — | 原生 |
| `function(x){ return x%2===0; }`（filter 回退） | 542 | — | 回退 |

**关键发现**：
1. **指令 -98%**（8.15M → 154K），但**墙钟仅 -46%**（1.8x）——O-6 消灭了回调解释开销，
   剩余成本是 **map 每次调用的输出数组分配 + 元素装箱（convT64）+ push**，占大头。
2. 对用户：性能敏感的 HOF 代码请用**简单箭头**（函数表达式/复杂回调慢 1.8-2x）。
3. `map` 类收益 > `filter`（filter 每元素布尔判定+半量结果 push，原生面占比小）。

## 5. 引擎基准矩阵（go test ./bench，中位数 3 次）

| 基准 | v2(O-5前) | v3(O-5+O-6) | 分配 v3 | 变化 | 归因 |
|------|-----------|-------------|---------|------|------|
| FibVM (fib30) | 3.71s | 738ms | 11.6M | **-80%** | O-5 去每帧 arguments |
| FibAST (fib30) | 5.22s | 4.05s | 38.5M | — | 对照 |
| FibVMSmaller (fib20) | 30.9ms | 5.95ms | 94K | **-81%** | O-5 |
| PropAccess | 38.3ms | 37.5ms | 600K | -2% | — |
| PropSet | 24.0ms | 22.9ms | 300K | -5% | — |
| MethodCall | 141.4ms | 33.2ms | 400K | **-77%** | O-5 |
| StrConcat | 4.6ms | 4.4ms | 80K | -4% | — |
| ArrayPush | 53.4ms | 52.7ms | 1.1M | -1% | — |
| ArrayMap | 35.7ms | 22.1ms | 711K | **-38%** | O-6 |
| CallOverhead | 150.0ms | 31.7ms | 500K | **-79%** | O-5 |
| ClosureCall | 37.7ms | 37.0ms | 600K | -2% | — |
| GCPressure | 179ms | 158ms | 954K | -12% | O-5 余波 |

> 说明：v2 报告 §3 矩阵采集于 O-5 之前，v3 列含 O-5+O-6 累计。

## 6. 混合负载（--monitor，tests/benchmark/mixed.js，O-6 当前）

```
elapsed 1.43s
指令 28.6M（20.1 M/s）｜ 调用 378K ｜ 分配 152K 对象
IC 命中  get 2,999,997/3,321,203（90.3%）  set 0/0  call 0/320,900
heap 峰值 47.3 MB ｜ GC 36 次，暂停累计 2.2ms
```

- 指令吞吐 ~20M/s 不变（解释器速度未动，O-6 是"少解释"而非"解释更快"）。
- **call IC 仍全 miss（0/320,900）**——O-7（OpCall 函数值 IC）未实施；但因 O-6，
  回调已不再依赖 IC，此短板对 HOF 影响减弱。
- IC get 命中 90.3% 与 v2 持平。

## 7. 热点分析（pprof，重混合负载 /tmp/mixed-heavy.js）

同负载 O-5 与 O-6 二进制各采集一次（无 --monitor 干扰）：

| 函数 | O-5 flat% | O-6 flat% | O-6 cum% | 说明 |
|------|-----------|-----------|----------|------|
| `VM.run` | 10.8 | 11.4 | 79.6 | 指令分派 |
| `weak.runtime_makeStrongFromWeak` | 6.1 | 5.8 | 10.3 | **jsHeap 弱引用注册表（新量化）** |
| `mallocgcTiny` | 8.3 | 5.3 | 9.5 | 小对象分配 |
| `VM.push` | 4.7 | 4.2 | 6.4 | 值栈压入 |
| `VM.pop` | — | 3.4 | 3.4 | |
| `mallocgc` | 3.6 | 2.1 | 20.4 | 常规分配 |
| `scanobject` | 2.0 | 2.7 | 15.3 | GC 对象图扫描 |
| `isBigInt` | 1.6 | 2.4 | 2.4 | 算术 BigInt 判定 |
| `binAdd` | 1.8 | 2.1 | 6.4 | 加法 |
| `ensureStack` | 2.3 | 1.9 | 1.9 | 值栈扩容 |
| `execNativeCallback` | — | 1.3 | 8.5 | **O-6 原生回调（新进）** |
| `callClosure` | 2.3 | **退出 top30** | — | **回调不再解释执行** |
| `convT64` | 1.6 (cum 16.4) | **退出 top30** | — | **回调装箱消失** |

**O-6 改变了热点结构**：解释器侧（callClosure/convT64/isBigInt）显著下移；
分配 + GC 合计仍约 25-30%，叠加弱引用注册表 ~10% cum——**分配路径是当前第一优化面**。

## 8. 新发现热点：jsHeap 弱引用注册表（建议 O-11）

`internal/engine/gc.go`：每个 JS 对象创建（NewObject/NewArray/NewFunction）都执行

```go
func register(obj *objectValue) {
    jsHeapGlobal.mu.Lock()
    jsHeapGlobal.objects[weak.Make(obj)] = struct{}{}   // 全局锁 + map 插入
    jsHeapGlobal.alloc++
    if jsHeapGlobal.alloc%4096 == 0 { jsHeapGlobal.sweepLocked() } // 全表扫 weak.Value()
    jsHeapGlobal.mu.Unlock()
    BumpAlloc() // 仅此被 gated
}
```

- **注册本身无条件执行**（与 --monitor/--max-memory 无关）；每 4096 分配还全表
  `weak.Value()` 解引用清扫。pprof：`makeStrongFromWeak` flat ~6% / cum ~10%，
  O-5 与 O-6 均存在。
- 这是 `--monitor` 功能（9bfd56b）引入的成本，目前由所有正常执行路径承担。
- **建议**：注册改为惰性（仅 --monitor/--max-memory 或全局 `gc()` 显式触发时启用），
  或分片锁/批量队列替代全局 map；预计回收分配路径 ~5-8%。
- 注意：--monitor 开启时该路径被放大（带 monitor 的 pprof 中 OOMTriggered 也到 7.6%），
  常规 profiling 请勿加 --monitor。

## 9. 回归状态（O-6 变更门禁）

| 项 | 结果 | 说明 |
|----|------|------|
| `go test ./...` | ✅ 全绿 | — |
| diff 套件 | 48/66 | 18 个失败与 O-5 二进制**逐一相同**（fs 路径/constants/sqlite/websocket/worker 等环境差异），非 O-6 回归 |
| conformance | 15/17 | 2 个失败（03-require-esm、16-m7-test-core）与 O-5 相同；**17-arguments（O-5 回归用例）PASS** |
| m6-array-hof.cjs | ✅ PASS | O-6 差分用例（47 断言，map/filter/reduce/sort/Array.from 等与 node 逐行一致） |
| 序列化 round-trip | ✅ 绿 | serialize_test.go 锁定 v13 字节流（修复 36B 丢弃缓冲错位） |
| 编译器检测测试 | ✅ 绿 | 8 简单命中 + 12 拒绝回退 |

> ⚠️ 偶发挂起说明：diff 套件全量跑有一次卡死 600s（疑似 m4-dns/http 等真实网络用例瞬时挂起）；
> 复测所有用例（aluka+node 双端，各 25s 超时）全部正常退出，套件常规 ~45s 完成。
> run-diff.sh 无逐用例超时保护，可考虑加 `timeout` 兜底。

## 10. 启动耗时

`console.log("hello")`，PowerShell Measure-Command，7 次取中位数：

| 引擎 | 启动耗时 |
|------|---------|
| aluka | **34.9 ms** |
| Node 22.3.0 | 72.0 ms |

aluka 启动快 **2.1x**（与 v1 结论一致，轻量 Go 运行时 vs V8+Node 初始化）。

## 11. 优化点清单更新

### 状态

| ID | 优化点 | 状态 |
|----|--------|------|
| O-5 | 去每帧 arguments 对象 | ✅（v2 已实施，-52%） |
| **O-6** | **数组高阶回调原生化** | **✅（本次报告）**：arrayMap -48%、混合负载指令 -36%、墙钟 -17%；M-P1 目标未达（见下） |
| O-1/O-2 | args 池 / int→str intern | ❌ 实测无提升（v2 §11） |
| O-3 | 值栈预分配 | ✅ 已核实实现 |

### 下一步（按 pprof 占比与预期收益）

| 优先级 | 优化点 | 依据 | 预期 |
|--------|--------|------|------|
| **P0** | **O-11 弱引用注册表惰性化/分片**（新） | makeStrongFromWeak flat 6% / cum 10% | 分配路径 -5~8% |
| P0 | **O-8 宽值化**（NaN-boxing/tagged union） | arrayMap 剩余成本=输出数组装箱；convT64 仍在 gcPressure/arrayPush 路径 | arrayMap 5x+、综合 +15-25%（架构级） |
| P0 | **O-7 callIC 覆盖 OpCall** | call IC 0/320,900 | 调用类 -10% |
| P1 | O-4 isBigInt 合并 / O-4b push/pop 瘦身 | isBigInt 2.4%、push 4.2%+pop 3.4% | 各 -2~4% |
| P1 | O-9 rope 展平缓存 | — | 拼接输出类 -30% |
| P2 | O-10 JIT 评估 | 调用类 189-204x 的根本解 | 长期 |

### M-P1 差距分析（为什么 -8% 而非 -75%）

- 计划预期 O-6 给 arrayMap "5-20x"：实测 **1.8-2x**。原因：perf-compare 的 map
  回调体极简（`x*2`），O-6 消灭的"回调解释"只占该用例总成本的一部分；
  剩余大头是 **输出数组构造 + 装箱 + push**（200 次 map × 10K 元素 = 2M 次
  元素写入）。指令数虽降 53x，墙钟瓶颈已转移到分配/装箱路径。
- 结论：**HOF 类用例要继续压缩必须动值表示（O-8 消灭 convT64）或数组快速路径**，
  仅靠 O-6 类"少解释"已触顶；调用类用例（189-204x）需要 O-7 或 O-8。

## 12. 复现

```bash
go build -o /tmp/aluka.exe ./cmd/aluka
/tmp/aluka.exe tests/benchmark/perf-compare.js            # vs node 22.3.0（5 次取中位）
go test ./bench -bench . -benchmem -benchtime=1s -count=3 # 引擎矩阵
/tmp/aluka.exe --monitor tests/benchmark/mixed.js          # 运行时指标（§6）
/tmp/aluka.exe --profile p.out tests/benchmark/mixed.js && go tool pprof -top p.out  # 热点（勿加 --monitor）
# O-5 对照二进制（本次报告方法）
git worktree add /tmp/aluka-o5 c89cd40 && cd /tmp/aluka-o5 && go build -o /tmp/aluka-o5.exe ./cmd/aluka
# 回归
go test ./...
ALUKA=/tmp/aluka.exe NODE=<node22> bash tests/compat/node22/diff/run-diff.sh
ALUKA=/tmp/aluka.exe NODE=<node22> bash tests/conformance/node22/run.sh
```

## 13. 与 v2 报告的差异说明

- **mixed.js 按文档规格重建**（v1/v2 引用的原文件未入库）：fib22 + 1M 属性读 + 30K 拼接
  + 300K push + 300×10K map，另加 50×10K filter/reduce 以覆盖 O-6 面；已随本报告入库。
- Node 版本 22.23.1 → 22.3.0（node 侧数字不可直接与 v2 对比，差距列以本次 node 实测为准）。
- 首次量化弱引用注册表开销（O-11），并给出 O-6 的"指令 vs 墙钟"解耦分析。
- diff/conformance 在**本机**为 48/66、15/17（环境差异），v2 的"65/65、17/17"为开发机数据。
