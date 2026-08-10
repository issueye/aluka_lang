# aluka 值类型化栈优化实施方案

> 日期：2026-08-10 ｜ 依据：perf-report-v4（合计 42.5x vs node）+ pprof 装箱热点定位
> **⚠️ 状态：阶段 A 已实验，结论为"纯 Go 下不可行"，方案暂停（见 §9）**。
> 目标：消除 JS 数字经 `engine.Value` 接口的**逃逸装箱**（pprof 实测 `engine.Number` cum 18%、
> `convT64` 15.6%、`mallocgcTiny` 12.5%），把 propAccess/Set、gcPressure、arrayPush、fib 类
> 用例再压 **-30~40%**，作为通向 ≤10x 的**必要但不充分**的第一大步。
> 前置基线：commit 900833b（perf-report-v4）。

## 1. 装箱根因（pprof 确认）

`engine.Value` 是 **11 方法接口**（engine.go:97-101）。`numberValue` 虽是 8 字节标量
（`type numberValue float64`），但：

```
engine.Number(n) 返回 Value（interface）
  → convT64（int64 位模式装箱）  ← pprof: 100ms，100% 调用者是 engine.Number
    → mallocgc（堆分配）          ← pprof: mallocgcTiny 12.5%
  → 结果 push 到 v.stack（VM 字段） → 必然逃逸 → 编译器无法内联直存
```

即**每次算术/属性读的中间数字都堆分配**。propAccess-3M 中 `o.a + o.b + o.c` 每轮 3 次
属性读 + 2 次加法，产生多次 Number 装箱；binAdd cum 12-19% 同样含装箱。

> 关键结论：不是"接口设计本身"而是"**数字作为接口逃逸存储**"。VM 栈（75 处直接操作）、
> 对象属性槽（`shape.slots []engine.Value`）、数组元素（`elems []engine.Value`）都用
> `[]engine.Value`，任何数字进出都装箱。

## 2. 目标与边界（诚实预估）

| 用例 | 当前 (ms) | 装箱占比 | 值类型化后预估 | 差距（vs node） |
|------|-----------|----------|----------------|-----------------|
| propAccess-3M | 583 | ~30%+ | ~380-420 | 212x → ~140-150x |
| propSet-3M | 370 | ~25% | ~260-290 | 152x → ~110-120x |
| gcPressure-500K | 360 | ~30% | ~240-280 | 30x → ~20-23x |
| arrayPush-1M | 345 | ~20% | ~260-290 | 29x → ~22-24x |
| fib25/30 | 30 / 327 | ~30% | ~20 / 220 | 16x/59x → ~11x/40x |

**重要边界**：值类型化消除的是**装箱**（~12-30%），但**不消除指令解码/分派/IC 查找**。
node 属性读是 0.9ns/次（JIT 内联），解释器即使零装箱仍需 ~30-50ns/次 → **propAccess
单独到不了 10x**。要全部 ≤10x 需叠加迭代化调用 + O-6 原生化扩展（见 §6）。

## 3. 方案设计：`stackSlot` 标签联合栈

VM 内部栈/槽/数组元素从 `[]engine.Value` 改为带标签的联合结构，**数字用 float64 直存**
（值拷贝，零堆分配），对象/字符串存指针，**仅在边界**（函数参数、属性读写、数组、
builtin 调用）转换回 `engine.Value`。

```go
// stackTag 栈槽类型标签
type stackTag uint8

const (
    tagUndef stackTag = iota
    tagNull
    tagBool
    tagNum      // num 有效（JS number，float64）
    tagStr      // str 有效（*string 或 rope 头）
    tagObj      // obj 有效（*objectValue，含数组/函数/bigint/等）
    tagBigInt   // obj → *bigIntValue
    // ...
)

// stackSlot 是值语义的栈槽：拷贝传递无堆分配（24 字节 struct）。
type stackSlot struct {
    tag stackTag
    num float64        // tagNum
    str *string        // tagStr（短串）或 rope 指针
    obj *objectValue   // tagObj/tagBigInt/...（对象/数组/函数/闭包）
    b   bool           // tagBool
}
```

VM 栈 `v.stack []stackSlot`；`push`/`pop`/`peek` 改为 stackSlot；数字指令
（OpPushInt/OpAdd/OpSub/比较/`++i`）直接操作 `slot.num`。

### 边界转换（唯一的 engine.Value 点）
- **属性读写** `getProperty`/`setProperty`：槽读写 `slotToValue`/`valueToSlot`。槽内部
  存 stackSlot；IC 命中直接读 num。
- **数组元素**：`ArrayValue.elems []stackSlot`（或保持 []engine.Value，视收益）。
- **函数调用**：`fastCallClosure` 参数/返回值转 engine.Value（调用链内部可局部优化）。
- **builtin/原生函数**：边界统一 `toValue(slot)` / `toSlot(value)`。

### 共享内存结构
`engine.Value` **保留**（API 与 builtin/globals 不变），只在 VM 栈/槽/数组内部用
stackSlot。所有 `v.stack` 直接操作点（vm.go 75 处）与相关指令需改造。

## 4. 分阶段实施

### 阶段 A —— 栈直存数字（核心，独立可验证）
**范围**：VM 栈 `[]engine.Value` → `[]stackSlot`；`push/pop/peek/reserveUndefined`；
字面量/算术/比较/一元指令（OpPushInt/PushConst 数字/OpAdd/OpSub/OpMul/OpDiv/OpMod/
OpNeg/OpBitAnd.../OpEq/OpLt...）；`binAdd` 等改为 `num` 直算。
**保留**：属性/数组/函数调用边界先转 engine.Value（保语义）。
**预期**：纯算术/循环类（`s+=i`、fib 递归算术、callOverhead 的 s++）**-20~30%**。
**工程量**：~800-1200 行（栈 + ~30 条指令重写 + 边界转换）。
**风险**：中高——所有读栈顶的指令（OpDup/OpSwap/OpPop/方法调用参数/IC 槽）要适配
stackSlot；需全量回归 + 差分。

### 阶段 B —— 属性槽值类型化
**范围**：`shape.slots` 与属性读写路径对数字直存（IC 命中直接读 num，set 直接写 num）。
**预期**：propAccess/Set **额外 -20~30%**。
**工程量**：~600-900 行。
**风险**：中——shape/slots 被 engine 层广泛使用（markFromRoots、GC、原型链），
stackSlot 需实现 Value 语义的只读视图或按需转换。

### 阶段 C —— 数组元素值类型化
**范围**：`ArrayValue.elems` 数字直存；arrayPush/GetElem/SetElem。
**预期**：arrayPush/gcPressure 额外 -10~20%。
**工程量**：~400-600 行。
**风险**：中。

### 阶段 D（可选）—— 调用参数/返回值直传
fastCallClosure 参数布置与返回值用 stackSlot 直传，消除函数调用的装箱。
**预期**：callOverhead/methodCall 额外 -15~25%。
**工程量**：~300-500 行。
**风险**：高（调用边界最复杂）。

## 5. 关键风险与缓解

| 风险 | 等级 | 缓解 |
|------|------|------|
| stackSlot 全指令适配遗漏（读栈顶语义差异）| 高 | 阶段 A 先跑全量测试 + 差分 conformance；仅算术指令走直存，其他转换 |
| 属性槽 stackSlot 与 engine 层（GC 标记/原型链/Proxy）交互 | 中 | 阶段 B 只在 VM 读写路径直存，engine 层暴露 toValue 视图 |
| 与内联（I-2）/原生回调（O-6）冲突 | 低 | 内联体指令同样受益；O-6 回调参数仍走 engine.Value |
| 性能收益不达预期 | 低 | 每阶段独立基准门禁（§7），不达标可回退 |

## 6. 组合路线（通向 ≤10x）

值类型化栈是**必要**（消除装箱），叠加：

1. **迭代化调用**（fastCallClosure 内联进 run() 循环）：callOverhead/methodCall/fib 再降 1.5-2x
2. **O-6 原生化扩展**（把常见循环体/属性访问模式编译为 Go 直执行）：arrayMap 已达 9.4x，
   扩展覆盖 propAccess/循环累加模式
3. **superinstruction**（OpIncLocal/OpGetElemLocal 等）：循环/计数类

| 里程碑 | 内容 | 预计合计 |
|--------|------|----------|
| M-V1 | 阶段 A（栈直存） | ~42.5x → ~30x |
| M-V2 | +阶段 B（属性槽） | ~30x → ~22x |
| M-V3 | +阶段 C/D + 迭代化 | ~22x → ~16x |
| M-V4 | +O-6 原生化扩展 | 部分用例 ≤10x |

**全部用例 ≤10x** 仍需热函数编译（JIT 雏形，单独里程碑，见
inline-tiered-bytecode-plan.md §8）。

## 7. 验证与验收

1. **基准门禁**（每阶段）：`perf-compare.js` 受影响用例（propAccess/Set、fib、
   gcPressure、arrayPush、callOverhead/methodCall）+ `mixed.js` 墙钟；要求阶段预期内
   `-X%`，不达标回退。
2. **正确性门禁**：`go test ./...` 全绿 + node22 差分（m2/m3/m6）+ 装箱回归
   （`s+=i`、`o.a*b`、JSON 数值、toFixed 等）。
3. **pprof 复测**：mallocgcTiny / convT64 / engine.Number 应从 pprof 顶部消失。
4. **GC 语义**：stackSlot 的 obj 指针须参与 GC 标记（markFromRoots 从栈遍历 obj）。

## 8. 结论

值类型化栈是当前**收益最明确的单项优化**（消除 ~12-30% 装箱开销），也是通向 ≤10x 的
必经之路。单独实施后合计差距预计 **42.5x → ~30x**；配合迭代化 + O-6 原生化扩展可达
**~16x**（部分用例 ≤10x）。全部用例 ≤10x 需 JIT 级热函数编译。

## 9. 阶段 A 实验结论（2026-08-10，已回退）

阶段 A（`stackSlot` 标签联合栈）**已完整实施并通过全量测试（22 包绿）**，基准结果：

| 用例 | v4 基线 (ms) | stackSlot (ms) | 变化 |
|------|-------------|----------------|------|
| propAccess-3M | 583 | 545 | **-7%** ✓ |
| fib25/30 | 30 / 327 | 30 / 338 | ~0 / +3% |
| propSet-3M | 370 | 406 | +10% |
| callOverhead-1M | 157 | 171 | +9% |
| closureCall-1M | 190 | 223 | +17% |
| methodCall-1M | 188 | 214 | +14% |
| gcPressure-500K | 360 | 436 | +21% |
| **合计** | **2656** | **2833** | **+7%** |

**结论**：
1. **stackSlot 为 32 字节**（`engine.Value` 16 + `float64` 8 + tag，8 字节对齐），比原
   interface 大 2 倍。变量/闭包/对象密集用例的 LoadLocal/StoreLocal 栈拷贝开销抵消了
   装箱收益（propAccess 的纯加法链 -7%，但整体 +7%）。
2. **纯 Go 无法做 compact tagged value**（V8 的 SMI+pointer 方案）：
   - `unsafe.Pointer` 字段存数字位模式会被 Go GC 当指针扫描 → 误标记/内存损坏
   - `uintptr` 字段不参与 GC 跟踪 → 存对象指针会泄漏/悬垂
   - 故栈元素无法压缩到 16 字节，**值类型化栈在纯 Go 接口/GC 约束下收益不成立**。

**已回退**（git checkout，回到 v4 基线 2656ms）。`stackSlot` 方案暂停。

**替代方向**（重新排期）：
- **双栈**：`numStack []float64`（8 字节）只服务纯数字算术指令，值栈保持 engine.Value；
  需解决两栈操作数序一致性（复杂度高，收益待验证）。
- **迭代化调用 + O-6 原生化扩展**（见 inline-tiered-bytecode-plan.md）：把更多 JS 模式
  下沉到 Go 侧直执行，绕过解释器装箱（已在 arrayMap 验证 16x→9.4x）。
- **热函数编译（JIT 雏形）**：唯一能全部 ≤10x 的途径。
