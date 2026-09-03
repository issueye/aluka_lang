# ADR：Stage 2 对象槽位 NaN-boxing——GC 可见性否决

> 状态：原型实验否决（GC 可见性陷阱）
> 日期：2026-09-03
> 分支：feat/object-arena（Stage 2 实验，未进 main）
> 关联：docs/adr/number-representation-stages.md 的 Stage 2 定义

## 方案（ADR 原定义）

`objectValue.slots []Value` 与 `ArrayValue.elems []Value` 改为 `[]uint64`
NaN-boxed 存储：数值属性/元素免 box，槽位 16B→8B 且无指针，GC 扫描与
分配字节同时下降。

## 否决理由：uint64 里的指针对 Go GC 不可见

Go 的标记-清除按堆对象的**类型位图**扫描指针。`[]uint64` 是无指针类型，
槽里 NaN-box 编码的对象指针不会被 GC 追踪——被引用对象提前回收，槽
变成悬垂。这使"对象生命周期委托 Go runtime"的整个架构失效。

### 决定性实验（复现见分支说明）

```go
type marker struct{ magic [64]byte }

// A：interface 槽（现状）
slots[0] = m; weak := weak.Make(m); m = nil
// 8 轮 GC + 24 万对象分配压力后：
//   weak 生效 → "存活"，解引用 magic = 0xAB ✓

// B：uint64 NaN-box 槽（Stage 2 编码）
slots[0] = uint64(uintptr(unsafe.Pointer(m))); weak := weak.Make(m); m = nil
// 同样压力后：
//   weak 失效 → "已回收"，解引用 magic = 0（内存已被复用）✗ 悬垂
```

差异唯一且可归因：槽位类型从 `interface{}`（GC 可见指针）换成
`uint64`（GC 不可见）。interface 槽的读取使用了槽值（编译器不消除存储），
两臂条件完全对称。

## 波及面

- 数值槽免 box 的意义在本架构内已由数字 slab（Stage 1）实现：数字本身
  无指针、每 64KB 一次 malloc 已摊销，免 box 的剩余收益只剩"少一次
  指针间接"，不构成推翻架构的理由。
- 若坚持 NaN-boxed 槽，只能自研完整 GC：手动维护根集（global、帧栈、
  闭包 upvalue、原型链、模块表）+ 标记 + 清除 + 与 Go weak 的协调。
  这与现有"Object 引用 = Go 引用"的一切（WeakMap、monitor 存活统计、
  markFromRoots 遍历）冲突，工作量与风险等同重写 GC，且 Go 无法搬移
  对象地址，仍无 young-gen/copying 收益。
- 纯 Go 生态佐证：goja、quickjs-go、dnq 均以 interface/原生持有为槽，
  无一采用 NaN-box 槽位。

## 结论与替代

**否决 `[]uint64` NaN-boxed 槽位。** 保留 interface 槽（GC 可见），
把 Stage 2 的原始收益目标（扫描/字节下降）拆到两个兼容实现：

1. **数字直存**（小步，本分支可继续）：在 interface 的 data 字内直接
   存放 float64 的位模式——Go 对"pointer-shaped 单字结构"零分配装入
   interface，数字从此免 slab、免指针间接。注意：numberValue 结构体
   本身仍占据 data 字，读取时按类型断言解位。
   （需要验证 Go 编译器对 this 形态的确切处理，作为下一步原型。）
2. **ADT 化槽位（更大步，ADR 既定 Stage 3 前奏）**：保持 `[]Value`，
   但引入"槽 = 数字箱或接口"的统一 8 字表示……结论同上述：interface
   单字是唯一无指针且 GC 可见的载体，改变它必然坠入本 ADR 的陷阱。

分配的字节/扫描目标最终只能靠 **Stage 3 全量 NaN-boxing（Value=uint64，
~3700 调用点重写）** 或 **对象本体瘦身（tagged slots 的相邻方案：
ObjectValue 字段压缩、外提 proto/ext）** 达成——两者都必须在"GC 可见"
约束内设计。

## 复现

本 ADR 的对照实验是独立的 main 包，未入库（避免仓库内 unsafe 测试）。
如需在 CI 复现：将"B 臂：uint64 槽 + weak + 压力循环"写为单文件
Go 程序运行即可，条件与上文一致。