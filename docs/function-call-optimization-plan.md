# aluka 函数/闭包调用优化实施方案

> 日期：2026-08-10 ｜ 依据：pi 兼容性压测（`bench-js2.js` 实测）+ 调用路径代码分析
> 目标：把普通函数/闭包/方法调用的每次固定开销压缩到可接受范围，closureCall/callOverhead
> 对 node 差距从当前 **~117x** 降至 **≤60x**，为 pi 等重 JS 应用的交互响应争取可用性。
> 前置基线：commit 8a421d7（shape O(N²) 修复 + 正则编译缓存后）。

## 1. 现状基线（2026-08-10 实测，bench-js2.js，100 万次）

| 用例 | aluka ms | node ms | 对 node 差距 | 说明 |
|------|----------|---------|--------------|------|
| closures（箭头函数 `add(i,1)` 循环调用）| 258 | 2.2 | **~117x** | 纯调用固定开销主导 |
| obj-field-access（`o.a.b.c.d` 2×10⁵）| 35 | 0.9 | ~39x | 属性访问（另一热点，见 §7）|
| array-map（100 元素 × 5000）| 90 | 5.9 | ~15x | 回调（O-6 已原生化，剩余为属性/装箱）|
| string-concat / split | 125 / 114 | 31 / 9.6 | 4x / 12x | 字符串（非本次范围）|

**单次调用固定开销 ≈ 258ns**（vs node JIT 2.2ns）。这是 pi"回车后等待 + 流式渲染卡顿"的主要组成之一。

## 2. 调用路径逐项开销分解（普通字节码函数，如 `add(a,b)=a+b`）

每次 `add(i,1)` 的完整路径与固定成本（基于 vm.go 代码分析）：

```
OpCall
  ├─ doCall (vm.go:1169)
  │   ├─ args := make([]Value, numArgs) + copy   ← ① 堆分配 + 参数拷贝（1/N 次调用）
  │   └─ v.stack = v.stack[:argStart-1]           ← ② 弹栈
  ├─ invoke (vm.go:1513)
  │   ├─ engine.BumpCalls()                       ← ③ gated 原子 load（每次）
  │   └─ callee.(*vmClosure) 类型断言             ← ④ 分派
  ├─ callClosure (vm.go:1579)
  │   ├─ v.module 保存 + defer 恢复               ← ⑤ 即使同模块也 save/restore
  │   ├─ frame := vmFrame{...}                    ← ⑥
  │   ├─ reserveUndefined(NumLocals)              ← ⑦ ensureStack + 逐槽填 undefined
  │   ├─ v.stack[base]=this; 参数循环拷回栈       ← ⑧ 参数第二次拷贝（从 ① 的 args）
  │   └─ v.frames = append(...)                   ← ⑨ slice（可能扩容）
  ├─ run() 解释循环（执行 OpAdd 等）
  └─ doReturn (vm.go:1697)
      ├─ closeUpvalues(frame.base)                ← ⑩ 函数调用（即使 openUpvalues 为空）
      ├─ v.stack = v.stack[:frame.base]           ← ⑪
      └─ v.frames = v.frames[:len-1]              ← ⑫
```

**关键发现**：
- 参数经历 **两次拷贝**（doCall→args slice，args slice→新帧栈），中间 slice 每次堆分配。
- `v.module` 保存/恢复 + `defer` **每次调用**都执行，即使 callee 与当前同模块（绝大多数情况）。
- `closeUpvalues` 对无闭包函数是空遍历，但仍是每次调用的函数调用开销。
- `BumpCalls` 每次调用都做一次 gated 原子 load（监控关闭时为零开销判断，但仍在热路径）。
- `reserveUndefined` 逐槽循环填充（O-4b 曾提出用 `copy` 批量写，未实施）。

## 3. 优化目标

| 里程碑 | 目标（相对 258ns/次基线） | 实测（commit 8844791 后） | 对 node 差距 | 状态 |
|--------|--------------------------|--------------------------|--------------|------|
| M-F1 | 普通调用 -40~50% | closure-call **-30%**、plain/arrow -26%、method-call **-21%**、five-args -14% | closures ~95x | ✅ 核心已实施 |
| M-F2 | 累计 -50~60% | fib25 **-33%**（doReturn 瘦身叠加） | — | ✅ 部分 |
| M-F3 | 累计 -55~65% | BumpCalls 降级已并入 F1（pprof 6% 原子 load 消除） | — | ✅ 已并入 |
| M-F4 | 累计 -60~70% | frames 预分配已并入 | — | ✅ 已并入 |
| M-F5 | 同模板调用特化 | — | — | ⬜ 待实施 |

## 4. 任务分解（按投入产出排序）

### F1 ✅ 已实施 —— 栈上参数传递 + 帧快速布置（核心，消除 ①+⑧）

**现状**：`doCall` 把栈上参数拷到 `args []Value`（①），`callClosure` 再拷回新帧栈（⑧）。
**方案**：新增快速调用路径 `fastCallClosure`（`invoke` 内对 `*vmClosure` 且满足条件的分支）：

1. **参数原地成为新帧参数槽**。栈布局 `… callee arg0…argN-1` → 弹出 callee 后把 `thisVal`
   写入 callee 槽，`frame.base = argStart-1`，则 `arg0…argN-1` 已在 `[base+1, base+1+N)`，**零拷贝**。
2. 只 `reserveUndefined(NumLocals - 1 - numArgs)` 补齐未传形参与局部变量（避免整帧填充）。
3. 快速路径条件（不满足回退现有 `callClosure`）：
   - 非 generator/async（这些在 `callClosure` 前部已分支）
   - `cl.module == v.module`（跳过 ⑤）
   - `ArgumentsSlot < 0 || NoArgumentsObject`（O-5 已覆盖多数）
   - 非 varargs、实参不超过形参（多余实参需弹栈丢弃）
4. `doCall` 与 `doCallMethod`（IC 命中路径）对快速路径直接调用，不构造 args slice。

**实测**（commit 8844791，1M 次，aluka）：closure-call 258→181ms（**-30%**）、plain/arrow-call
-26%、method-call 312→246ms（**-21%**）、fib25 21→14ms（-33%）、five-args -14%。
**风险**：中高——栈布局改动是 VM 核心，已通过全量回归（22 包 + 跨模块/varargs/arguments/async）。

### F2 ✅ 已实施（部分）—— doReturn / closeUpvalues 快速路径

**方案**：
1. `doReturn` 内联 `if len(frame.openUpvalues) > 0 { v.closeUpvalues(frame.base) }`（消除 ⑩ 的
   空调用；无闭包函数 openUpvalues 恒空）。✅
2. `reserveUndefined` 的逐槽循环改用批量写入（O-4b 遗留项）——⬜ 未实施（收益小，暂缓）。

### F3 ✅ 已实施（部分）—— invoke 分派瘦身 + BumpCalls 降级

**方案**：
1. `BumpCalls` 的 gated 原子 load 改为 VM 级 `callCountEnabled bool`（NewVM 时从
   `engine.MetricsEnabled()` 缓存，监控需在 VM 创建前开启——CLI `--monitor` 已满足）。
   热路径零原子操作（pprof 中 `atomic.(*Bool).Load` 6% 已消除）。✅
2. `invoke` 的 `AsFunction()` 通用分支后置（vmClosure/NativeMethod 已提前）——⬜ 已足够，
   快速路径已绕过 invoke，收益小。

### F4 ✅ 已实施 —— 帧/栈预分配

**方案**：
1. `v.frames` 创建时预分配容量 128（runModule），append 免扩容。✅
2. `v.stack` 初始容量与增长策略评估——⬜ 暂缓（收益小）。

### F5（后续/可选）—— 同模板重复调用特化

**现状**：`OpCall` 对函数值调用无缓存（O-7 计划针对方法调用；此处理普通调用）。
**方案**：per-PC 记录"上次 callee 闭包 + 模板"，命中且满足快速路径条件时跳过
类型断言直接进 `fastCallClosure`（同 pc 反复调用同一回调的典型模式）。
**预期**：额外 -10~20%。
**工作量**：~120 行。
**风险**：中（需命中率门禁，回调对象变化则失效）。

## 5. 验证与验收

1. **基准**：`bench-closure.js`（普通调用/闭包捕获调用/方法调用各 1M 次，aluka vs node 差分），
   复跑 `bench-js2.js` 确认综合变化。
2. **正确性门禁**：
   - `go test ./... -count=1` 全绿
   - 既有调用类回归：fib 递归、多参数、rest、`arguments`（17-arguments.cjs）、
     generator/async、跨模块闭包（CJS getter）、`new` 构造、`super()` 链
   - pi 差分：m2/m3/m6 用例（node22 差分 conformance）
3. **性能门禁**：M-F1 后 closures 单次 ≤155ns；M-F4 累计 ≤100ns（约 ≤45x node）。
4. **监控一致性**：`--monitor` 的调用计数在 F3 改动后仍正确（monitor_test 更新）。

## 6. 实施顺序与里程碑验收

```
F1（核心，栈上参数）→ F2（return 瘦身）→ F3（分派/计数）→ F4（预分配）→ F5（同模板特化，可选）
```

每步独立提交 + 基准验证，避免一步到位引入难排查回归。F1 完成后即可对 pi 交互场景做一次
A/B 验证（回车到首渲染延迟）。

## 7. 关联热点（本计划之外，另行排期）

| 热点 | 差距 | 方案方向 |
|------|------|----------|
| obj-field-access | ~39x | 属性读路径：shape 查找 + IC 命中分支瘦身；栈上临时对象（`{a,b,c}` 字面量）避免逐槽 set |
| array-map | ~15x | O-6 已原生化，剩余为元素装箱 + 属性访问，随 F1/F3 部分改善 |
| 字符串拼接 | ~4x | rope 节点/平串创建（已有专项评估，非调用问题）|

## 8. 长期架构方向

纯 Go 解释器每指令固定开销（分派 + 接口装箱）决定了天花板（约 50-100x node）。要进一步突破：
- **小函数内联**：编译器对"叶子小函数"（体短、无闭包、无 arguments）在调用点展开（JIT 的第一步）；
- **分层字节码**：热路径指令（OpCall/OpAdd/OpGetProp）用 switch 直分派替代接口方法调用；
- **回调 Go 侧直执行扩展**（O-6 模式）：把更多"JS 薄包装 → Go 实现"（如字符串工具、路径操作）下沉。

这些属于架构级改动，建议在 F1-F4 落地并验证收益后评估。
