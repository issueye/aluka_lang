# ADR：数字表示的分阶段优化（slab 装箱 → NaN-boxing）

> 状态：Stage 1、Stage 2 已实施（perf/nan-boxing 分支）
> 背景：docs/performance-report-v6.md §9 —— 数字装箱占剩余分配的 41%

## 背景

`Value` 是 `interface{}`，`numberValue` 原为值类型 `float64`：每次装入接口
（操作数栈 push、属性存储）都触发 `runtime.convT64` 堆分配（Go 仅缓存
0-255 小整数）。gcPressure 剖面：每迭代 ~3.6 次装箱分配。

完整 NaN-boxing（`Value = uint64` 单字表示）需改写全仓库 ~3700 个方法
调用点（`.Type()`/`.String()`/`.AsObject()` 等）与数千处类型标注，
是多周级工程。本 ADR 定义分阶段路线，Stage 1 以极小改造面捕获大部分收益。

## Stage 1：slab 装箱指针数字（已实施）

`numberValue` 从 `float64` 改为**单指针字结构体**（pointer-shaped struct）：

```go
type numberBox struct{ v float64 }
type numberValue struct{ b *numberBox }  // 装入 interface 零分配（指针直存数据字）
```

数字单元经 64KB slab 原子 bump 分配（`newNumber`）：

- 快路径：一次 `atomic.Add` + 边界检查（~几 ns），替代 `mallocgc`（~25ns）
- slab 耗尽后加锁换新块；旧块由存活 box 指针保活（放大上界 = 一个块）
- box 仅含 float64（无指针）→ GC 扫描 slab 为无指针块，成本极低
- 并发正确性：索引唯一性由 CAS 语义保证；写旧块与换块并发安全
  （该索引在旧块内唯一归属本协程，指针保活旧块）。附并发压力测试
  （`number_slab_test.go`：8 协程 × 跨多块分配，逐值校验无覆写）。

### 语义代价（已审计）

interface 相等比较变为指针比较（同值双 box 不等）。影响面审计结论：

- JS 语义相等（`==`/`===`）走 `equality.go` 的 `Float()` 数值比较——不受影响
- `structuredClone` 的 `seen` map 仅对象键、`domain.forwarders` 仅事件
  对象键——不受影响
- JIT 测试中 6 处 `value != engine.Number(n)` 直接比较已改为数值比较
  （`numEq` 辅助）
- `undefined/null/bool` 单例与值类型比较不受影响

### 实测（同机，对照 v6 报告数据）

| 指标 | 优化前 | Stage 1 后 |
|------|-------:|----------:|
| gcPressure 每迭代分配 | 10.1 | **4.1（累计 14.1 → 4.1，-71%）** |
| gcPressure-500K（auto 中位） | ~337ms | **~279ms（较原始 401ms 累计 -30%）** |
| fib25（auto） | 2.78ms | **1.6ms——反超 Node 22（1.80ms）** |
| fib30（auto） | 44.5ms | 9.6ms（8.0x → **1.7x**） |
| propSet-3M | 4.4x | **2.6x** |
| arrayPush-1M | 5.6x | **3.5x** |
| callOverhead-1M | 2.6x | **1.5x** |
| 长跑内存（200 万数字） | — | 堆峰值 6.9MB，无放大 |

质量门禁：全量测试、jit 套件、jitdiff 三档零失配。

## Stage 2（候选）：对象槽位 tagged 数组

`objectValue.slots []Value` / `ArrayValue.elems []Value` 改为 `[]uint64`
NaN-boxed 存储：数值属性/元素完全免 box。读写边界（`getSlot` 返回 Value）
仍会装箱，但 VM 热路径（算术消费、JIT 特化）可直读 f64。改造面：
engine/value.go + shape IC + jit property PIC。

## Stage 3（远期）：Value 全量 NaN-boxing

`type Value uint64`，双精度 NaN-boxing 编码（指针/数字/标记值）。消除
全部接口分派与装箱，操作数栈可换成 `[]uint64`。改造面 ~3700 调用点，
建议以机械改写 + jitdiff oracle 分批推进。前置条件：Stage 2 落地并稳定。

## 备选方案与否决理由

- **sync.Pool 回收 box**：不可行——box 一旦发布（存入对象/栈）生命周期
  不可追踪，回收即悬垂。
- **大范围小整数缓存**：循环计数可达百万级，缓存命中率不足。
- **VM 双栈（数值栈 + 引用栈）**：需重写 run() 全部 ~200 个 opcode 的
  栈纪律，风险/收益比劣于 Stage 1+2 渐进路线。

## Stage 2 实施记录（同分支跟进）：按 CPU 剖面重定目标

Stage 1 落地后，原 Stage 2（槽位 tagged 数组）的前提消失——数字装箱已由
slab 消灭（槽位存储不再产生分配）。CPU 剖面（gcPressure 200K，160ms 样本）
显示剩余热点为：

1. `aeshashbody` 18.75%（Shape.lookup 字符串哈希，字面量创建逐属性查找）
2. `math.mod` 12.5%（i % 100 走硬件 fmod）
3. `sync/atomic.Add` 12.5%（slab bump + register 计数）

实施两项（调整为 Stage 2 实际内容）：

**对象字面量站点缓存**：同一 (模板,PC) 的字面量键序列恒定——首次执行经
`engine.ResolveLiteralShape` 解析 shape 与 pair→slot 索引并缓存于 VM，
后续经 `engine.NewObjectFromShape` 零哈希/零 transition 直接构建。

**取模整数快路径**：`fastInt64`（|x|<2^53 精确整数）双操作数走 int64 取模
（硬件 fmod 约慢一个量级）。语义对齐 jitdiff oracle 修正两处边界：
零值输入（fmod(-0,x)=-0）与零余数符号随被除数（fmod(-1,1)=-0），
配 700+ 输入对拍测试（mod_fastpath_test.go）。

**实测**（交错 A/B，10 轮）：gcPressure 中位 300 → **249ms（-17%）**；
较原始基线 401ms **累计 -38%**。perf-compare 合计 vs Node：
**6.2x**（v6 8.8x、v5 13.6x）；fib25 0.7x、propSet 2.2x、closureCall 1.5x。
门禁：全量测试、jitdiff 三档零失配。

原"槽位 tagged 数组"目标并入 Stage 3 一并考虑（uint64 Value 下自然消解）。
