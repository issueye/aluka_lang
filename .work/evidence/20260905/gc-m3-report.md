# M3 落地证据：分代标记-清除 GC（feat/gc-m3 分支）

## 实现摘要
- GC 引擎落在 `aluka-vm/src/gc.rs`（侧表：ages/is_free/young_free/old_free + 统计），
  `HeapObject` 增加 `Free` 占位变体 + `trace_refs` 图遍历；全部分配经
  `push_object` 漏斗（复用空闲槽位或追加），2 万次分配阈值触发 major 回收。
- 根集两大来源（漏报根=悬垂不变量）：
  1. VM 结构图：栈/局部/全局/内置单例/微任务宏任务队列/挂起 async 帧/
     生成器状态/try 挂起异常/模块导出缓存/注册表单例；
  2. 静态持有表：集中式 root provider 快照（promise 组合器/timers 解析器
     预存值/symbol 注册表），新增持 Value 的静态必须登记。
- **对 ADR 落地计划的偏差（已记录）**：
  1. 真实回收器落在 aluka-vm 而非 aluka-core 的 `Heap`——VM 的
     `HeapObject`/`Value` 与 core 类型不同型且 core 不可反向依赖；
  2. age 以侧表实现而非枚举字段（15+ 变体侵入面考量，语义等价）；
  3. 生产触发为 major-only：minor/记忆集机制已实现并有单元测试
     （minor_collects_young_only_and_promotes），但写屏障需覆盖全部
     老写新变异点，审计完成前不启用（悬垂风险）。

## 验收证据
- 命令证据：`cargo test` → 55 个测试目标全绿（含 gc_stress e2e 压力：
  40 万+ 分配、约 20 轮 major、循环结构、闭包、注册符号，输出与 Go 逐字一致）；
  `cargo clippy --all-targets -D warnings` → 0 错误；fmt --check 通过。
- GC 单元矩阵 9 项：无根回收/全局存活/图追踪/环回收/晋升/槽位复用重置/
  注册符号保活/minor 年轻代语义/常量锚定。
- **性能基线**（方法学：交替执行 + 轮间冷却 100ms + min-of-5；负载 = 2 万次
  迭代 fib + 10 万分配 churn + 5 万三节点环，双引擎输出逐字一致）：
  | 构建 | min-of-5 |
  |---|---|
  | aluvm release（GC 开启） | **127 ms** |
  | Go oracle aluka.exe | 118 ms |
  持平（噪声内）。debug 构建同负载 491 ms（对照用）。

## 遗留登记（顺手发现，超出本作用域）
1. **JS 深递归原生栈溢出**：fib(20) 即栈溢出，pre-GC 构建（c5c9808）同样
   复现——既有引擎限制（解释器每 JS 调用的 Rust 栈帧过大），与 GC 无关；
   Go oracle fib(25) 正常。需单独立项（加大解释器线程栈 / 帧堆分配）。
2. minor GC 启用前置条件：全部「老对象写入年轻引用」变异点的写屏障审计。
