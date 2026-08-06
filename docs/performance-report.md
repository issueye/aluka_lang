# aluka 性能测试报告

> 日期：2026-08-06 ｜ 版本：main（O2 之后，commit e54c1cf + 性能工具补充）
> 报告用途：建立当前性能基线，定位热点，为后续优化提供量化依据。

## 1. 概述

本报告从四个维度量化 aluka（纯 Go JS/TS 运行时）的性能现状：

1. **引擎基准矩阵**——12 项 Go benchmark（`go test ./bench -bench . -benchmem`），
   度量 aluka 自身各热路径的耗时/分配。
2. **跨引擎对比**——11 项同 JS 代码在 aluka 与 Node 22（V8 JIT）下对比，
   给出差距倍数。
3. **热点分析**——`--profile`（pprof cpu）剖析典型混合负载的耗时分布。
4. **启动耗时**——普通模式 / `--compile` 编译产物 / Node 三者对比。

## 2. 测试环境

| 项 | 值 |
|----|----|
| CPU | AMD Ryzen 7 5700U（8 核 16 线程） |
| OS | Windows 10.0.26200 x64 |
| Go | go1.26.5 windows/amd64 |
| Node | v22.23.1 |
| 构建 | `go build -o aluka.exe ./cmd/aluka`（默认优化，无 ldflags） |
| 复现 | `go test ./bench -bench . -benchmem -count=3`；`tests/benchmark/perf-compare.js` |

## 3. 引擎基准矩阵（aluka 自身）

`benchtime=1s, count=3`，数值取中位数。

| 基准 | 耗时 | 内存 | 分配数 | 说明 |
|------|------|------|--------|------|
| FibVM（fib30） | 4.57 s | 760 MB | 35.8M | 递归调用 + 算术 + 控制流 |
| FibAST（fib30） | 5.22 s | 1.62 GB | 38.5M | AST 解释器（基线参考） |
| FibVMSmaller（fib20） | 37.6 ms | 6.2 MB | 291K | 轻量递归 |
| PropAccess | 44.3 ms | 4.8 MB | 600K | 100K 循环 ×3 属性读 |
| PropSet | 28.4 ms | 2.4 MB | 300K | 100K 属性写 |
| MethodCall | 182.8 ms | 24.8 MB | 1.1M | 100K 方法调用 |
| StrConcat | 136.0 ms | 259 MB | 80K | 10K 次字符串拼接 |
| ArrayPush | 68.9 ms | 25.7 MB | 1.1M | 100K 数组追加 |
| ArrayMap | 50.0 ms | 20.1 MB | 910K | 20×10K map 高阶 |
| CallOverhead | 174.7 ms | 25.6 MB | 1.2M | 100K 空函数调用 |
| ClosureCall | 45.3 ms | 4.8 MB | 600K | 100K 闭包调用 |
| GCPressure | 259 ms | 25.7 MB | 1.05M | 50K 短生命周期对象 |

观察：
- **StrConcat 内存异常高（259 MB）**——JS 字符串不可变语义下 `s += "x" + i`
  每步 2 次分配，且结果字符串随循环线性增长（总长度 ~45K，但分配了 259MB，
  即每次拼接都复制全串，O(n²) 累积）。
- **fib30 分配 35.8M 次**——每次递归调用都伴随函数帧/返回值对象分配。
- 除法式调用主导用例（MethodCall/CallOverhead）耗时最高（170-180ms/100K），
  印证调用路径是核心热点。

## 4. 跨引擎对比（aluka vs Node 22）

同代码（`tests/benchmark/perf-compare.js`，固定迭代量，`process.hrtime.bigint()` 计时）。

| 用例 | Node (ms) | aluka (ms) | 差距 |
|------|-----------|-----------|------|
| fib25 | 3.34 | 368.6 | **110x** |
| fib30 | 9.02 | 4809.7 | **533x** |
| propAccess-3M | 3.86 | 1296.0 | 336x |
| propSet-3M | 2.70 | 772.3 | 286x |
| strConcat-100K | 20.05 | 9278.4 | 463x |
| arrayPush-1M | 19.99 | 618.8 | 31x |
| arrayMap-100x10K | 10.63 | 195.6 | **18x** |
| callOverhead-1M | 2.45 | 1870.7 | **764x** |
| closureCall-1M | 8.64 | 439.1 | 51x |
| methodCall-1M | 2.96 | 1889.1 | 638x |
| gcPressure-500K | 22.30 | 3029.8 | 136x |

结论：
- 整体差距 **18x–764x**，属"字节码解释器 vs JIT 编译"的典型量级。
- **调用主导用例差距最大**（callOverhead 764x / methodCall 638x / fib30 533x）——
  V8 对内联小函数有强优化，解释器每调用固定开销被放大。
- **数组高阶方法差距最小**（arrayMap 18x）——`map` 是原生内置，回调本身短，
  解释器负担占比低。
- 启动差距反向（见 §6）：**aluka 启动比 Node 快约 30%**。

## 5. 热点分析（pprof）

典型混合负载（fib(22) + 100 万属性读 + 3 万字符串拼接 + 30 万数组 push +
300×10K map），`--profile` 采样 19.4s：

| 函数 | flat% | cum% | 说明 |
|------|-------|------|------|
| `VM.run`（解释器主循环） | 15.3% | 69.9% | dispatch + 各指令执行 |
| `callClosure`（函数调用） | 5.6% | 69.9% | 调用链（经 run 累积） |
| `tryDeferToSpanScan`（GC） | 6.2% | 8.4% | Go GC 扫描 |
| `mallocgcTiny`（分配） | 4.8% | 9.4% | 小对象分配 |
| `scanObject`（GC） | 4.3% | 14.7% | 对象图扫描 |
| `mallocgc`（分配） | 3.8% | 23.6% | 常规分配 |
| `VM.push`（栈压入） | 3.8% | 5.9% | 值栈操作 |
| `ensureStack`（栈扩容） | 2.2% | 2.2% | 值栈增长拷贝 |
| `VM.cur`（当前帧） | 2.0% | 2.0% | 帧访问 |

关键发现：
1. **解释器 dispatch 本身 ~15% flat**（前 O2 优化已将 Decode 内联），其余 ~55%
   cum 为指令实现/调用/栈操作——纯解释器本质。
2. **Go 分配 + GC 合计约 25%**——显著高于预期。对象字面量、数组元素、
   字符串拼接、函数返回值全部产生 Go 堆分配；小对象（`mallocgcTiny`）与
   大对象扫描（`scanObject`）各占约 5%。**这是比 dispatch 更值得投入的
   优化面**（O2-D3 曾评估分配优化"收益边际"，pprof 数据显示该结论需修正）。
3. `ensureStack` 2.2%——值栈扩容拷贝（可预分配优化）。

## 6. 启动耗时对比

`console.log("hello")` 单文件，各引擎取 5 次最优：

| 模式 | 启动耗时 |
|------|---------|
| aluka 普通模式 | **59 ms** |
| aluka `--compile` 产物 | **58 ms** |
| Node 22 | 85 ms |

- aluka 启动比 Node 快约 **30%**（轻量 Go 运行时 vs V8+Node 初始化）。
- `--compile` 产物与普通模式几乎相同（payload 反序列化开销被更快的加载抵消；
  产物体积 33.2 MB 主要来自基座本身，payload 仅数 KB）。

## 7. 结论与优化方向

**现状**：解释器为主、IC/隐藏类/superinstruction 已生效（PropAccess 相对
O1 提升约 19%）；与 V8 差距 18x–764x，符合解释器量级；启动性能优于 Node。

**优化优先级（按 pprof 占比）**：

| 优先级 | 方向 | 预期 |
|--------|------|------|
| P0 | **分配/GC 削减（~25% 占比）**：字符串拼接中间结果合并、函数帧/返回值复用、数组字面量批量分配 | 综合负载提升 15-25% |
| P0 | **调用路径**：`callClosure`/`invoke` 内联小函数快速路径（同 O2 记录） | 调用主导用例（fib/方法调用）显著 |
| P1 | 值栈预分配（`ensureStack` 2.2%） | 栈扩容场景 |
| P1 | `map`/`filter` 等原生高阶内置扩展覆盖 | 缩小 arrayMap 类差距 |
| P2 | JIT（基线 JIT 或 tiered） | 根本性缩小跨引擎差距（长期） |

**已顺带修复的 API 缺口**（性能测试工具链依赖）：
- `process.hrtime.bigint()`（Node API，此前缺失）
- `Number(bigintValue)`（BigInt→Number 转换此前返回 NaN）

## 8. 复现方式

```bash
# 引擎基准矩阵
go test ./bench -bench . -benchmem -count=3

# 跨引擎对比
node  tests/benchmark/perf-compare.js
aluka tests/benchmark/perf-compare.js

# 热点分析
aluka --profile prof.out <负载脚本>
go tool pprof -top prof.out

# 启动耗时
time (for i in $(seq 1 10); do aluka hello.js; done)
```
