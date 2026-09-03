# ADR-0001：Aluka-R 运行时值表示（Value Representation）架构决策

> 状态：**已定案（Accepted）**  
> 日期：2026-09-04  
> 决策人：Aluka 架构组  
> 关联：  
> - `docs/adr/stage2-nanbox-slots-rejected.md`（Go 版 NaN-box 槽位被 GC 机制否决的经验教训）  
> - `docs/adr/object-arena-rejected.md`（Go 版对象 Arena 被否决记录）  
> - `.work/TODO/README.md`（A1-2 任务项）  

---

## 1. 背景与前车之鉴

在 Go 原型阶段（`aluka_g`），曾尝试引入 8 字节的 `uint64` NaN-boxing 槽位（见 `docs/adr/stage2-nanbox-slots-rejected.md`）。然而，因为 Go 运行时依赖类型位图（Type Bitmap）进行精确指针扫描，无指针的 `uint64` 槽位导致堆内对象被 Go GC 提前回收，引发严重的悬垂指针（Dangling Pointer）问题，最终该方案被彻底否决并回退为 `interface{}` 槽位。

在 Rust 重构版本（`aluka_r`）中，运行时的内存模型发生了根本性变革：
1. **宿主无黑盒 GC**：Rust 没有 Go 运行时的黑盒 GC 扫描，内存生命周期由我们自主研发的垃圾回收器与堆空间显式掌管；
2. **堆对象句柄化**：`ObjectRef(u32)` 是堆内槽位下标（句柄）而非裸指针。GC 在进行搬移（Compacting/Copying）时只需更新堆内部映射，栈与局部槽位内的句柄跨 GC 周期始终有效；
3. **跨平台与 Tier 0 快速推进**：Aluka-R 既面向服务器端（x86_64, aarch64），又明确面向 WebAssembly（wasm32/WASI）。

---

## 2. 方案对比与评估

| 评估维度 | 方案 A：16 字节 Tagged Enum（当前基准） | 方案 B：8 字节 NaN-Boxing | 方案 C：8 字节 Pointer Tagging |
| :--- | :--- | :--- | :--- |
| **内存占用** | 16 字节（8B Tag + 8B Payload） | 8 字节（IEEE-754 扩展 NaN） | 8 字节（利用地址低 3 位对齐） |
| **类型安全性** | 100% 安全 Rust，无 `unsafe`，编译器穷尽性检查 | 包含大量位运算与 `unsafe` 解码，易致 UB | 需要强制对齐，涉及裸指针转换 |
| **跨平台一致性** | 64 位 / 32 位 / wasm32 完全一致 | wasm32 或 32 位下浮点 NaN 行为需要特殊处理 | 32 位与 64 位指针宽度不一致 |
| **调试与排障** | `derive(Debug)` 可读性极佳，直观打印类型 | 调试器中是一长串十六进制整数，排障成本高 | 需特殊 pretty-printer |
| **GC 协同成本** | `ObjectRef(u32)` 句柄直接解耦，无需担心宿主可见性 | 句柄低 32 位编码可行，但浮点 Canonicalization 需额外开销 | 裸指针不利于 GC 对象移动整理 |

---

## 3. 架构决策

### 3.1 阶段性分层策略（Two-Tier Strategy）

1. **M0 ~ M1 阶段（功能完备与正确性优先）**：
   - **全面采用方案 A（16 字节 Tagged Enum）**。
   - 定义：
     ```rust
     #[repr(C)]
     #[derive(Debug, Clone, Copy, PartialEq)]
     pub enum Value {
         Undefined,
         Null,
         Boolean(bool),
         Number(f64),
         Object(ObjectRef),
     }
     ```
   - 核心优势：构建快速、100% 内存安全、零 UB 风险，使团队在 M0/M1 阶段能够把全部精力聚焦在“字节码正确性、解释器循环完善与黄金语料对拍”上，避免陷入底层位编码导致的诡异 Crash。

2. **M2 阶段（高吞吐性能优化期）**：
   - 引入 Cargo Feature `nan-boxing`；
   - 通过统一的门面方法屏蔽内部表示差异：
     ```rust
     impl Value {
         pub const fn is_number(&self) -> bool;
         pub const fn is_nullish(&self) -> bool;
         pub const fn to_boolean(&self) -> bool;
         pub fn as_number(&self) -> Option<f64>;
         pub fn as_object(&self) -> Option<ObjectRef>;
     }
     ```
   - 上层的 `aluka-vm` 解释循环、内联缓存（IC）与 `aluka-compiler` 均面向 `Value` 的抽象门面编程，无缝切换 8 字节表示。

---

## 4. 结论

- **定案**：当前阶段采用 16 字节 Tagged Enum 作为正式规范；
- **GC 协同原则**：堆对象严格使用 `ObjectRef(u32)` 句柄持有，严禁在栈槽位中存放未注册的裸指针；
- **任务标记**：总清单中 `A1-2` 顺利关闭。
