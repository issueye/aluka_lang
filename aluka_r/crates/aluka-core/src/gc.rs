//! 垃圾回收接口。
//!
//! 与 Go 版的关键差别：**存活判定由本 crate 负责**，不再委托宿主语言的
//! 回收器。Go 版把 JS 对象实现为 Go 结构体，因此存活等于"Go GC 还没回收"，
//! 连带失去了 bump 分配、对象搬移与 NaN-box 槽位三种可能（见
//! `docs/adr/object-arena-rejected.md`、`stage2-nanbox-slots-rejected.md`）。
//!
//! # 根集必须显式提供
//!
//! Rust 的调用栈上没有可供扫描的类型信息，所以 GC 不做隐式栈扫描：
//! VM 需要通过 [`RootSet`] 把当前可达的起点（全局对象、帧内局部、操作数
//! 栈、闭包捕获单元、模块表、FFI 借出的句柄）交给 GC。漏报根 = 悬垂，
//! 这是本模块最重要的不变量。
//!
//! # 现状
//!
//! M0 阶段只固定接口。具体策略（分代标记-清除 vs 引用计数 + 循环回收）
//! 由 M0 的两份原型基准定夺，见 `aluka_r/docs/rust-reimplementation-devplan.md`
//! 的 M0 验收项。

use crate::object::{ObjectClass, ObjectRef};
use crate::value::Value;

/// GC 的根集：一次回收的可达性起点。
///
/// VM 每次触发回收前重建（或增量维护）它。不在根集中、也不被根集可达
/// 对象引用的堆对象即为垃圾。
#[derive(Debug, Default)]
pub struct RootSet {
    roots: Vec<Value>,
}

impl RootSet {
    /// 创建空根集。
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// 登记一个根。原始值（数值、布尔等）登记无害但无意义。
    pub fn push(&mut self, value: Value) {
        self.roots.push(value);
    }

    /// 遍历已登记的根。
    pub fn iter(&self) -> impl Iterator<Item = Value> + '_ {
        self.roots.iter().copied()
    }

    /// 根的数量。
    #[must_use]
    pub fn len(&self) -> usize {
        self.roots.len()
    }

    /// 根集是否为空。
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.roots.is_empty()
    }

    /// 清空根集，供下一轮重建复用底层容量。
    pub fn clear(&mut self) {
        self.roots.clear();
    }
}

/// 一次回收的统计，供 `--monitor` 与调优使用。
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct GcStats {
    /// 累计分配对象数
    pub allocated: u64,
    /// 上次回收后存活对象数
    pub live: u64,
    /// 已完成的回收次数
    pub collections: u64,
}

/// 对象堆：分配与回收的入口。
///
/// M0 阶段为占位实现（只记账不回收），用于把 VM 与内置库的接口先跑通；
/// 真实分配器与回收算法在 M3 落地（`devplan` 的 M3 里程碑）。
#[derive(Debug, Default)]
pub struct Heap {
    next_index: u32,
    stats: GcStats,
}

impl Heap {
    /// 创建空堆。
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// 分配一个对象并返回其句柄。
    ///
    /// `class` 决定对象头之后的载荷布局。当前实现只递增句柄计数——它足以
    /// 让上层跑通接口，但不会真正存储对象，因此**尚不能解引用**。
    pub fn allocate(&mut self, class: ObjectClass) -> ObjectRef {
        let _ = class;
        let handle = ObjectRef(self.next_index);
        self.next_index = self.next_index.wrapping_add(1);
        self.stats.allocated += 1;
        self.stats.live += 1;
        handle
    }

    /// 以 `roots` 为起点执行一次回收，返回回收后的统计。
    ///
    /// 占位实现只累加回收计数：真实实现要在这里完成标记（沿根集遍历
    /// 对象图）与清除/搬移。
    pub fn collect(&mut self, roots: &RootSet) -> GcStats {
        let _ = roots;
        self.stats.collections += 1;
        self.stats
    }

    /// 当前统计快照。
    #[must_use]
    pub fn stats(&self) -> GcStats {
        self.stats
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn root_set_records_and_clears() {
        let mut roots = RootSet::new();
        assert!(roots.is_empty());

        roots.push(Value::Number(1.0));
        roots.push(Value::Undefined);
        assert_eq!(roots.len(), 2);
        assert_eq!(roots.iter().count(), 2);

        roots.clear();
        assert!(roots.is_empty());
    }

    #[test]
    fn allocate_hands_out_distinct_handles_and_counts() {
        let mut heap = Heap::new();
        let a = heap.allocate(ObjectClass::Ordinary);
        let b = heap.allocate(ObjectClass::Array);

        assert_ne!(a, b);
        assert_eq!(heap.stats().allocated, 2);
        assert_eq!(heap.stats().live, 2);
    }

    #[test]
    fn collect_advances_collection_counter() {
        let mut heap = Heap::new();
        heap.allocate(ObjectClass::Ordinary);

        let roots = RootSet::new();
        let stats = heap.collect(&roots);
        assert_eq!(stats.collections, 1);
    }
}
