# ADR：aluka_r GC 选型——分代标记-清除（原型 A）

> 状态：已接受（T-BE-02 评测定案，M3 落地）
> 日期：2026-09-04
> 关联：`docs/adr/0001-aluka-r-value-representation.md`（Value 表示定案）、
> `docs/adr/object-arena-rejected.md`（裸 arena 陷阱）、
> `docs/adr/stage2-nanbox-slots-rejected.md`（无指针槽位陷阱）
> 评测证据：`.work/evidence/20260904/gc-proto-a-report.md`、`gc-proto-b-report.md`

## 决策

引擎主回收器采用**原型 A：分代标记-清除（非移动）**。候选 B（引用计数 +
备份计数循环回收）评测落败，不作为主回收器；其「释放分散、死期确定」的
优点记录在案，若未来出现低分配率 + 暂停敏感的运行形态可复议。

## 评测数据（min-of-5，同负载同生命周期模型，release 构建）

| 场景 | 原型 A 分代标记-清除 | 原型 B 引用计数+循环回收 | A 优势 |
|---|---|---|---|
| fib30_tree（269 万递归分配） | **171.3 ms** | 311.2 ms | 1.8× |
| churn（20 万分配，10% 存活） | **19.8 ms** | 1596.6 ms | **80.8×** |
| cycles（5 万三节点环） | **16.6 ms** | 385.5 ms | 23.3× |

方法学：交替执行 + 轮间冷却 100ms + min-of-5（总 TODO §1 硬规则）。

## 理由

1. **JS 分配形态是天敌匹配**：引擎负载 = 高频小对象、瞬时树形激活记录。
   RC 对每次引用写/弃置收税（fib30 1.8×），批量弃置场景（churn）被
   逐对象递归释放拖到 80×；标记-清除的批量清扫天然适配。
2. **对象头更瘦**：B 需要每对象 `strong` + `buffered` + `color` 三个
   额外字段支撑循环检测；A 只需一个 `age` 字节（分代晋升）。
3. **非移动设计与现状同构**：句柄 = slab 下标，回收不修补引用，与现有
   VM 的 `Vec<HeapObject>` 堆模型一一对应——落地只需补标记位图、
   free-list、写屏障三件，不迁移调用方。
4. **两条铁律已守住**（继承 Go 侧否决实验的教训）：句柄是堆内下标而非
   裸指针（回收器自管可达性）；清除只在 free-list 槽位粒度进行，绝无
   「存活对象钉住整块 arena」的裸 bump。

## 后果

- **正面**：M0 的 GC 选型验收项闭环；M3 落地有可复用的原型代码
  （`aluka-core/src/gc_protos/generational.rs`）与正确的单元测试矩阵。
- **负面 / 已知代价**：
  - 标记-清除有全局暂停（当前 minor ~6ms/次；M3 若超预算再做增量标记）；
  - 记忆集按对象粒度（卡表按页粒度的空间优化留 M3 评测）；
  - 引用计数的「死期确定」特性放弃——JS 语义无 finalizer 硬需求，可接受。
- **中立**：原型 B 代码保留在 `gc_protos/refcount.rs`（20 项测试中 5 项
  专测其正确性），作为对照实现供后续性能回归比对。

## 落地计划（M3）

1. 把原型 A 的算法移植进 `aluka-core/src/gc.rs` 的 `Heap` 真实实现
   （替换 M0 占位），对象模型从 `ProtoObject` 换为 VM 的 `HeapObject`；
2. VM 侧接入：操作数栈/局部槽位/上值/全局表全部纳入 `RootSet`（漏报根 =
   悬垂，见 `gc.rs` 模块文档不变量）；
3. `HeapObject` 增加 `age: u8`；`set_property` 路径加写屏障；
4. 以 `gc_bench` 同款负载回归，并与 Go 版 `aluka.exe` 的 fib30 基线对拍
   （性能类任务必须带方法学，总 TODO §1）。
