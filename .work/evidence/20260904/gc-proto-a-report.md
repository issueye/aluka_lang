# GC 原型 A 评测报告：分代标记-清除（非移动）

> 日期：2026-09-04　|　实现：`aluka_r/crates/aluka-core/src/gc_protos/generational.rs`
> 基准：`aluka_r/crates/aluka-core/examples/gc_bench.rs`（`cargo run --release --example gc_bench`）
> 方法学：交替执行 + 轮间冷却 100ms + **min-of-5**（总 TODO §1 硬规则）
> 环境：Windows 10.0.26200 x64，rustc release（opt-level=3）

## 设计

- 单一 slab（`Vec<Option<ProtoObject>>`），句柄 = 下标（非移动，回收不修补引用）；
- 新对象进年轻代 free-list（等价 bump 分配的池化）；
- 老年代对年轻代的引用经**写屏障**进入记忆集（remembered set，卡表的按对象粒度等价物）；
- minor 回收只扫年轻代（根 = 根集中年轻对象 + 记忆集老对象的年轻槽位）；
- minor 存活 ≥2 次晋升老年代；major 回收全堆标记-清除。

## 结果（min-of-5）

| 场景 | 负载 | 耗时 | GC 次数 | 回收对象 |
|---|---|---|---|---|
| fib30_tree | 269 万次递归分配/弃置 | **171.3 ms** | minor 21 | 259.99 万 |
| churn | 20 万分配，10% 存活 | **19.8 ms** | minor 2 | 18.0 万 |
| cycles | 5 万个三节点环 | **16.6 ms** | minor + major | 13.5 万 |

## 正确性

20 项单元测试全绿（`cargo test -p aluka-core`）：存活集保护、环垃圾回收
（标记-清除天然处理环，无需特殊机制）、老→新记忆集保活、两次存活晋升、
major 跨代清扫。

## 观察

1. **分配快路径廉价**：free-list pop + 写对象头，fib30 全程 171ms（含 269 万分配 + 21 次 minor）；
2. **吞吐与暂停解耦**：churn 场景 2 次 minor 清 18 万对象仅 ~6ms/次，暂停集中且可预算；
3. **标记成本与存活集线性**：cycles 场景 15 万存活（含 5 千环）major 清扫 13.5 万垃圾一次完成；
4. 与现有 VM 堆（`Vec<HeapObject>` 句柄索引）同构，落地时主要补：标记位图、free-list、写屏障三件。
