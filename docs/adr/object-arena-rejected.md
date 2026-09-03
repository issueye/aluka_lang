# ADR：对象 arena 批量分配——原型实验否决（纯 Go 引擎）

> 状态：已否决（原型实验定量否决）
> 日期：2026-09-03
> 分支：feat/object-arena（本次实验产物，未进 main）

## 背景

gcPressure 分析显示分配路径是 21x 差距的主要来源之一：强制物化场景
aluka 590 ns/obj vs node 24 ns/obj。数字 slab（`numberValue` 的 64KB bump
分配器）已在引擎内证明"slab 分配"可行且零 GC 成本。本 ADR 验证同一思路
能否推广到带指针的 JS 对象（objectValue/ArrayValue）。

## 方案

把 N 个对象放进一个大块（`[]objectValue`），块内 bump 分配，一次
`mallocgc` 覆盖多个对象——对齐 V8 young generation 的分配形态。

## 关键差异：数字 slab 为什么安全、对象 slab 为什么不安全

数字 slab 成立的两个前提，对象都不具备：

1. **numberBox 无指针**：Go GC 扫描 slab = 零成本（万级对象只扫块头）；
   objectValue 全是指针槽（shape/slots/proto/ext + 内嵌槽位），一个块的
   指针数 = 块内对象数 × 每对象指针数，扫描总量不变。
2. **无级联保活**：dead numberBox 的 8 字节对 GC 无影响；dead objectValue
   的指针槽 **继续保活其引用目标**（内层对象/数组/原型链），级联放大。

外加第三条：**整块 pin**。块内任一对象被外部引用（keep 数组、模块缓存、
返回值），整个块及其全部（含已死）元素都保留——GC 无法只回收数组的
一部分元素。

## 原型实验（复现：feat/object-arena 分支的测量脚本思路）

模拟 gcPressure 对象形态（3 属性对象，指针槽指向级联目标），300 万次
分配，保留率 1/100 与 1/1000，块大小 32 与 128，min-of-3：

| 保留率 | 策略 | 吞吐 | GC 后堆（RSS） |
|--------|------|------|----------------|
| 1/100 | mallocgc（现状） | 96 ms | 2.0 MB |
| 1/100 | arena × 32 | 57 ms（**1.7x**） | 43 MB（**22x**） |
| 1/100 | arena × 128 | 44 ms（**2.2x**） | 139 MB（**71x**） |
| 1/1000 | arena × 32 | 53 ms（1.3x） | 4.5 MB（11x） |

### 解读

- 吞吐收益真实（1.2–2.2x），来自分配次数减少；
- RSS 灾难：保留率 1% 时放大 22–71x，且随块大小线性恶化。即使保留率
  降到 1‰，32 块仍放大 11x；
- 放大构成：整块 pin（存活块内已死元素）+ 死元素指针槽级联保活目标。
- gcPressure 真实形态（keep 数组每 100 留 1）恰好落在最坏区间。

## 否决的缓解方案

| 方案 | 否决理由 |
|------|----------|
| 无淘汰 slab | 实验直接否决（RSS 22–71x） |
| weak 淘汰块（块内 weak 全失效才释放） | 块内 1 个存活即不可释放；保留率 1% 时几乎处处命中，退化到与无淘汰相同 |
| 帧级 arena（region）+ 编译器逃逸保证 | 只对"不逃逸闭合对象"有效，而 gcPressure 的对象图（o→{y,arr}→keep）整图逃逸；且编译器逃逸分析=新子系统，收益场景与 Node 靠标量替换覆盖的场景重叠 |
| 句柄间接层（arenaID,idx） | 热路径属性访问增加间接层，与 JIT 优化方向相悖；块 pin 问题不变 |

## 结论与替代

**纯 Go（对象是 Go 堆对象、正确性委托 Go runtime）的约束下，带指针对象
的 arena 批量分配不可行**——吞吐 2x 换 20–70x RSS，且级联放大。

分配路径的剩余优化空间应投向：

1. **缩小对象本体 + 降低指针密度**（tagged slots，ADR 既定 Stage 2）：
   slots/elems 改 `[]uint64` NaN-boxing 后，数值槽位免装箱且无指针，
   每次 malloc 的字节量与 GC 扫描量同时下降（这才是"分配 590ns→~150ns"
   的可行路径）；
2. **GC 调度现代化**：`debug.SetMemoryLimit` + 自适应，替代固定 GOGC 与
   每 2s 的 freeOSLoop。

## 相关

- docs/adr/number-representation-stages.md（数字 slab 的成立前提）
- docs/performance-report-v7.md（gcPressure 剖面）