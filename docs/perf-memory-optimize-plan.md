# 【性能 / 内存】优化计划

> 项目代号：`aluka` ｜ 文档版本：v1.1 ｜ 日期：2026-08-06
> 前置：性能测试报告（docs/performance-report.md）建立基线；O1/O2 已交付
> IC/superinstruction/热路径基础。
> 本计划面向报告 §7 优先级，分**性能**与**内存占用**两个方向制定任务分解、
> 里程碑与验收标准。

## 1. 基线数据（2026-08-06 实测）

### 1.1 性能基线

| 项 | 值 | 说明 |
|----|----|------|
| 跨引擎差距 | 18x–764x | 调用主导用例最大（callOverhead 764x / methodCall 638x） |
| 解释器主循环 | 15.3% flat / 69.9% cum | VM.run + callClosure 调用链 |
| **Go 分配 + GC** | **约 25%** | mallocgcTiny 4.8% + scanObject 4.3% + mallocgc 3.8% + GC 扫描 |
| 栈操作 | push 3.8% + ensureStack 2.2% + cur 2.0% | 值栈热路径 |
| 启动耗时 | aluka 58ms vs node 85ms | aluka 快约 30% |

### 1.2 内存基线（process.memoryUsage 实测，aluka vs node）

> 注：aluka 的 `heapUsed` 受 Go GC 回收时机影响波动（同负载多次 50-170MB），
> `rss` 稳定（Go 不归还内存给 OS）；下表以 rss 为主基线。

| 负载 | aluka heapUsed(峰) | aluka rss | node rss | rss 差距 |
|------|--------------------|-----------|----------|----------|
| 空程序 | 3.5 MB | 12-16 MB | 30.3 MB | 0.4-0.5x（更优） |
| 200K 数组对象（`{idx, tag}`） | 51.6 MB | 78-83 MB | 67.4 MB | 1.2-1.3x |
| 50K 次字符串拼接（`s += "chunk"+i`） | 91-170 MB | **200 MB** | 67.6 MB | **3.0x** |
| 200K 短生命周期对象 | 88-91 MB | **208 MB** | 82.6 MB | **2.5x** |

关键结论：
- **字符串拼接 O(n²) 累积复制**是最大内存黑洞（rss 3x；node 有 rope/cons-string
  优化故仅 67.6MB）。
- 短生命周期对象 2.5x——对象分配路径（Shape/slots）与 GC 回收效率差异。
- Go runtime 基线轻（rss 12-16MB vs node 30MB），但**内存不归还 OS**，
  长跑/高分配程序 rss 单调增长风险。

## 2. 性能优化方向

> 目标：综合基准相对当前基线提升 ≥30%（调用主导用例 ≥40%）。

### 2.1 任务分解

| ID | 任务 | 说明 | 优先级 | 工作量估 | 状态 |
|----|------|------|--------|---------|------|
| PF-1 | **调用快速路径** | `callClosure`/`invoke` 对小函数（无闭包捕获、参数少）走专用快速路径，跳过通用帧设置/upvalue 链 | P0 | ~300-500 行 | [ ] |
| PF-2 | **小函数内联展开（编译器）** | 编译期内联纯函数调用（无 this/闭包/递归的短函数体），消除调用帧 | P0 | ~400-600 行 | [ ] |
| PF-3 | **字符串拼接合并** | `s += a + b + c` 编译为单次拼接指令（收集操作数一次 concat），消除中间分配 | P0 | ~150-300 行 | [ ] |
| PF-4 | **值栈预分配** | 编译期统计函数最大栈深，帧进入时一次性预留（消除 ensureStack 增量扩容） | P1 | ~150-250 行 | [ ] |
| PF-5 | **原生高阶内置** | `map`/`filter`/`reduce`/`forEach` 对 `[Symbol.iterator]` 数组走 Go 原生实现（免回调逐元素解释） | P1 | ~300-500 行 | [ ] |
| PF-6 | **superinstruction 扩展** | 追加高频指令对（PushInt+CallMethod、LoadLocal+SetProp、PushConst+OpCall 等） | P1 | ~200-400 行 | [ ] |
| PF-7 | **二元运算快速路径** | 数字+数字/字符串+字符串的 binAdd 等内联到指令 case（省 Type 分派） | P1 | ~150-250 行 | [ ] |
| PF-8 | **JIT 评估（P2）** | 基线 JIT（热点函数编译到 Go 闭包）可行性评估；不可行则记录结论 | P2 | 探索 | [ ] |

### 2.2 验收标准

- 基准矩阵（bench/matrix_test.go）：MethodCall/CallOverhead 相对当前
  （182.8ms/174.7ms per 100K）提升 ≥40%。
- 跨引擎对比（perf-compare.js）：callOverhead/methodCall 差距从 764x/638x
  收窄至 ≤450x；fib25 差距从 110x 收窄至 ≤70x。
- 综合（propAccess/strConcat/arrayPush 加权）：≥30%。
- 回归：`go test ./...` 全绿；node22 15/15；build 19/19 不倒退。

## 3. 内存占用优化方向

> 目标：峰值堆内存相对当前基线削减 ≥50%（字符串拼接场景 ≥70%）。

### 3.1 任务分解

| ID | 任务 | 说明 | 优先级 | 工作量估 | 状态 |
|----|------|------|--------|---------|------|
| ME-1 | **字符串 rope / 延迟拼接** | `s += ...` 在 VM 侧维护分段串（rope 节点），`String()`/比较/索引时才展平；消除 O(n²) 累积复制 | P0 | ~400-600 行 | [x] |
| ME-2 | **对象字面量批量分配** | `{a,b,c}` 编译期预知 shape：一次分配 slots 容量 + 批量写入（跳过逐属性 Set/transition） | P0 | ~200-350 行 | [x] |
| ME-3 | **数组增长策略** | ArrayValue 追加按指数增长容量（当前可能逐元素 append 扩容）；稀疏数组转 map 存储 | P1 | ~200-350 行 | [ ] |
| ME-4 | **函数帧复用** | 帧对象池（嵌套调用复用已回收帧），降低 fib 类递归的帧分配 | P1 | ~250-400 行 | [ ] |
| ME-5 | **字符串 intern / 常量去重** | 模块常量池字符串去重（相同字面量共享）；属性名 intern 表 | P1 | ~150-300 行 | [ ] |
| ME-6 | **数值属性打包** | 全数字槽位对象（如 `{idx: 0, tag: 1}` 型）用 `[]float64` 打包存储（可选，评估收益后实施） | P2 | ~300-500 行 | [ ] |
| ME-7 | **GC 触发调优 + 运行时监控** | 分配阈值/清扫周期参数化（当前固定 gcSweepEvery=4096）；`--monitor` 报告峰值/指令/调用/IC 等指标；`--max-memory` 内存上限（软上限 + 看门狗 + VM 安全点抛 RangeError） | P2 | ~100-200 行 | [x] |
| ME-8 | **payload/字节码压缩** | `--compile` 产物 payload 常量池压缩（字符串共享 + 简单 LZ 风格） | P2 | ~300-500 行 | [ ] |

### 3.2 验收标准

- 内存探针（tests/benchmark/mem-probe.js）：50K 拼接场景 rss 从 200MB → ≤90MB
  （-55%）；200K 短生命周期对象 rss 从 208MB → ≤120MB（-42%）。
- 基准矩阵 GCPressure 分配数（105万）削减 ≥40%。
- 常驻：长跑程序 rss 稳定（无泄漏，`--mem` 报告可观测）。
- 回归：`go test ./...` 全绿；node22 15/15；build 19/19 不倒退。

## 4. 执行顺序与依赖

```
性能：PF-3（拼接合并，兼内存）→ PF-1/PF-2（调用路径）→ PF-4 → PF-5/PF-6/PF-7
内存：ME-1（rope，兼性能）→ ME-2（对象批量）→ ME-3/ME-4 → ME-5/ME-6 → ME-7/ME-8
交叉：PF-3 与 ME-1 二选一推进（都针对字符串拼接；先评估 rope 成本再定）
```

- **PF-3 与 ME-1 二选一**：PF-3（编译期合并操作数）改动小、立即见效；
  ME-1（rope）根治 O(n²) 但改动大。建议先 PF-3（P0），评估后决定是否上 rope。
- **PF-1/PF-2 依赖 O2 的调用路径现状**（callClosure 已识别为热点）。
- **ME-2 依赖隐藏类**（O1 已交付 Shape/transition，批量分配直接复用）。
- 性能与内存可双线并行（共享：拼接优化、帧复用、对象分配）。

## 5. 风险与已知限制

| 风险/限制 | 应对 |
|-----------|------|
| rope 改变字符串语义（索引/长度/比较复杂度） | 展平时机精确控制（String()/索引/哈希/比较前）；差分框架验证 |
| 小函数内联破坏 Function.name/arguments/递归 | 仅内联可静态判定安全（无 this/闭包/递归/arguments）的函数 |
| 原生高阶内置与自定义 Symbol.iterator 冲突 | 仅对原生 ArrayValue 走快路径，其余回退解释器 |
| 帧复用与 async/生成器挂起冲突 | 仅复用非挂起帧；asyncRunner/生成器帧不参与池 |
| 数值属性打包改变类型语义（NaN/Infinity） | 打包存储仅对"已验证全数值槽"的对象；写入非数值时解包回通用表示 |

## 6. 验收与复现

```bash
# 性能基线/回归
go test ./bench -bench . -benchmem -count=3
node  tests/benchmark/perf-compare.js   # 或 aluka
aluka tests/benchmark/perf-compare.js

# 内存基线/回归
aluka tests/benchmark/mem-probe.js
node  tests/benchmark/mem-probe.js   # 对照

# 热点分析
aluka --profile prof.out <负载>
go tool pprof -top prof.out

# 回归
go test ./... && (cd tests/conformance/node22 && ALUKA=aluka bash run.sh)
```

### 6.1 ME-1 / ME-2 实施结果（2026-08-06）

实现：
- ME-1：长字符串使用延迟展平 rope；短串仍直接合并；展平结果原子缓存；
  `.length` 不触发展平。VM、AST 解释器与 stub 的字符串加法统一走该路径。
- ME-2：无 spread、计算键和访问器的普通对象字面量批量构造最终 Shape，
  slots 一次定长分配；复杂字面量保留逐属性兼容路径。字节码格式升至 v11。

实测（同机，优化前后均 `count >= 3`）：

| 指标 | 优化前 | 优化后 | 结果 |
|------|--------|--------|------|
| BenchmarkStrConcat 耗时 | 71.9 ms/op | 4.48 ms/op | **-93.8%（16.0x）** |
| BenchmarkStrConcat 分配字节 | 259.3 MB/op | 1.20 MB/op | **-99.5%** |
| strConcat-100K | 9278.4 ms | 42.8 ms | **-99.5%（217x）** |
| strConcat 相对 Node 差距 | 463x | 2.6x | 达标（目标 ≤450x） |
| 50K 拼接后 rss | 200 MB | 73.9-74.1 MB | **-63%**，达标（目标 ≤90MB） |
| BenchmarkGCPressure 分配数 | 1.054M | 0.954M | -9.5%，未达 -40% 长期目标 |
| BenchmarkGCPressure 分配字节 | 24.6 MB/op | 21.4 MB/op | -13.0% |
| 200K 短生命周期对象后 rss | 208 MB | 132-146 MB | -30%~-37%，未达 ≤120MB |

回归：`go test ./...`、`go vet ./...`、node22 15/15、build 19/19 全通过。
对象压力 rss 仍受 Go GC 时机影响明显；后续应按计划继续 ME-3/ME-4，并以
`GCPressure` 分配数而非单次 rss 作为主要判据。

## 7. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.1 | 2026-08-06 | 完成 ME-1 rope 与 ME-2 对象字面量批量分配；补充性能、内存和回归实测 |
| v1.0 | 2026-08-06 | 初稿：基于性能测试报告基线与内存实测，分性能/内存两方向 16 项任务（PF-1~8、ME-1~8），含验收标准、执行顺序、风险与复现 |
