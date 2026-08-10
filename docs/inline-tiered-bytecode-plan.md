# aluka 小函数内联 + 分层字节码优化实施方案

> 日期：2026-08-10 ｜ 依据：函数调用优化后 pprof（commit 8844791）+ 编译器/VM 结构调查
> 目标：在"调用固定开销已压缩 -30%"（closures 181ms/1M，~95x node）基础上，通过
> **①小函数内联**（消除调用本身）与 **②分层字节码**（削减解释循环分派开销）把调用/属性
> 类热点再降 **40-60%**，逼近纯 Go 解释器天花板。
> 前置基线：commit 5ca0691。

## 1. 现状与动机

pprof（300 万次 `add(i,1)`，660ms 采样）：

| 热点 | flat | cum | 说明 |
|------|------|-----|------|
| VM.run | 27% | **91%** | 指令解码 + switch 分派 + **递归 run()** 主导 |
| binAdd / isBigInt | 9% / 4.5% | — | 数字运算的类型分派 |
| ensureStack / push | 7.6% / 7.6% | — | 栈操作（部分已优化）|
| doCall | 1.5% | 25.8% | 调用入口（已接入快速路径）|

run() 内层循环每迭代固定开销（调查确认）：
1. `frame = v.cur()`（vm.go:351）——每迭代一次 `frames[len-1]` 查找
2. operand 解码（vm.go:359-360）——**4 次边界检查 + 4 次字节读**拼 24 位
3. `engine.MetricsEnabled()` + `engine.OOMTriggered()`（vm.go:379-382）——**每指令 2 个跨包原子 load**
4. `v.local()`（vm.go:331-333）——OpLoadLocal/StoreLocal 内部**再调一次 `v.cur()`**
5. `fastCallClosure`/`callClosure` 里 **递归 `v.run()`**——每 JS 帧一层 Go 栈 + 前导重取

编译器侧：O-6 的 `analyzeSimpleCallback`（compiler.go:2611-2613）已能识别"纯箭头单表达式函数"
并生成 NativeCallback 描述——这是"可内联判定"的现成先例。`compileFunction`（compiler.go:2544）
在编译体时维护 `funcCtx.upvalueIndex`，可精确知道函数是否引用闭包变量。

## 2. 小函数内联（Inline）

### 2.1 目标
编译期把"可内联小函数"的调用点**展开为内联体指令序列**，消除 OpCall/帧设置/OpReturn
及一次递归 run()。对 `const add=(a,b)=>a+b; add(i,1)` 这类热点，单次调用从"分派+建帧+解释
+拆帧"变成"求实参→存槽→内联体 3 条指令"。

### 2.2 可内联判定（编译器，compileFunction 时）
满足全部条件才标记 `FuncTemplate.Inlinable`：
- 非 async / 非 generator / 无 rest / 无默认值 / 无解构参数（复用 O-6 判定骨架）
- 体为**单表达式**（`(a,b)=>expr`）或**单条 `return expr;`**（普通函数子集）
- 编译体时 `upvalueIndex` 为空（**无闭包捕获**）且不引用 `__this__`/`__newTarget__`/`arguments`
- 不递归（函数名在体内未被引用——upvalue 判定已覆盖，因递归即捕获自身）
- 参数 ≤ 8（槽位重映射限制）

### 2.3 编译器改动

**I-1 判定与标记**（无行为变化，可独立验证）：
- `compileFunction` 编译体后回查 `fc.upvalueIndex` 与体 AST 形态 → 设置 `tmpl.Inlinable`
- 序列化（serialize.go）增加 Inlinable 字段（缓存格式版本 +1）

**I-2 调用点展开**（最安全子集：const 绑定的内联函数）：
- 编译器维护 `inlineCandidates map[string]int`（当前作用域内 `const f = <函数表达式>` /
  `let f = <函数表达式>` → funcIdx）
- `compileVarDecl` 遇函数表达式 Init 时登记；作用域退出时清除
- `compileCall`（CallExpr）时：callee 为标识符且命中 `inlineCandidates`、目标
  `tmpl.Inlinable`、实参数 ≤ 形参数 → **展开**：
  1. 在调用者帧预留 `1+numParams` 个新局部槽（this 槽 + 参数槽），slot 号记为新 base
  2. 依次编译实参表达式 → `OpStoreLocal` 到对应参数槽（**实参求值保留调用者当前栈**）
  3. 复制内联体指令（参数/局部槽号 + base 偏移重映射），末尾以结果值替代 OpReturn
  4. 展开失败/callee 未静态解析 → 回退现有 OpCall 路径
- 展开后的体无 try/catch/闭包（判定已保证），无跳转（单表达式），重映射仅参数槽

**I-3 扩展（后续）**：
- 普通函数（单 return 语句、无 this/arguments）
- 多个 return / 简单分支
- `let` 可重绑定变量的内联失效追踪

### 2.4 风险与回归
- 槽位重映射是编译器核心，展开错误会产生错误结果而非崩溃——必须覆盖：实参副作用顺序、
  嵌套调用（内联体内再调用）、展开点栈深、`arguments.length` 依赖（判定排除）
- 回归：现有调用类用例（closure/arguments/递归/默认值/解构）+ 差分（node22 m2/m3/m6）
- 与 O-6 NativeCallback 互斥：Inlinable 判定与 NativeCallback 判定一致时，数组高阶
  仍走原生化（Go 侧），普通调用点走内联——两者不冲突

## 3. 分层字节码（Tiered Bytecode）

### 3.1 目标
把解释循环的固定开销与分派次数压到纯 Go 可实现的最小，为内联之外的通用代码提速。

### 3.2 L1 分派瘦身（run() 每迭代固定开销，pprof 直接依据）

| ID | 改动 | 收益依据 |
|----|------|----------|
| T-1a | **frame 指针 dirty-flag 缓存**：外层取 `frame`，循环顶 `if frameDirty { frame=v.cur() }`；仅在可能递归的 case（OpCall 系/OpNew/OpGetProp/OpGetElem/OpSetElem/OpGetIterator/OpIn/OpInstanceof 等，约 25 个标签）返回后置 dirty | 每迭代省 1 次 `frames[len-1]` |
| T-1b | **operand 单次读取**：`insn := binary.BigEndian.Uint32(code[pc:])`，op=高 8 位、operand=低 24 位 | 4 次边界检查+4 字节读 → 1 次 |
| T-1c | **监控 gated 缓存**：仿 `callCountEnabled`，VM 加 `insnsEnabled`/`oomEnabled`（NewVM 读取；`--max-memory` 未设时 oom 检查整条消失） | 每指令 2 个跨包原子 load → 0 |
| T-1d | **`v.local()` 用缓存 frame**：OpLoadLocal/StoreLocal/GetPropLocal/doCallThis 改为 `&v.stack[cachedFrame.base+slot]` | 每条 Load/Store 再省 1 次 cur() |

### 3.3 superinstruction 扩展（operand 24 位编码）

| 指令 | 合并 | 编码 | 收益 |
|------|------|------|------|
| OpIncLocal / OpDecLocal | `LoadLocal+PushInt1+Add+StoreLocal`（`++i`/`--i`）| `slot` | 4 指令→1，循环/计数热点 |
| OpGetElemLocal | `LoadLocal+GetElem`（`localArr[i]`）| `slot` | 数组循环热 |
| OpSetElemLocal | `LoadLocal+SetElemTop`（`localArr[i]=v`）| `slot` | 同上 |
| OpSetPropLocal | `LoadLocal+SetProp`（`localVar.prop=v`）| `slot<<16\|nameIdx`（slot≤255）| 与 OpGetPropLocal 对称 |
| OpCall1 / OpCall0 | arity 特化 OpCall | 无 operand | doCall 去通用循环 |

- 新 opcode 只追加到 `OpEnd` 前（opcodes.go 注释约束）；同步更新 `HasOperand`/`opNames`
- 编译器 optimize.go 熔合规则 + 边界守卫（slot/nameIdx 上限），参照 OpGetPropLocal 先例
- 潜伏 bug：compiler.go:2486 直接发射 OpGetPropLocal **缺 `slot<=0xFF` 守卫**（optimize.go 有），顺带修复

### 3.4 L2 迭代化调用（可选，风险最高）
把 `fastCallClosure` 覆盖的调用**内联进 run() 循环**：push 帧后刷新 frame/tmpl/code 局部继续
循环；doReturn 时 pop 帧；frames 空则返回。非快速调用（invoke/跨模块/async）仍递归。
- 收益：消除每 JS 帧一次 Go 递归 run()（cum 91% 的最大头）
- 风险：module 快照/恢复、异常 unwrap、帧指针一致性——需在 T-1a 的 dirty-flag 基础上做
- 单独里程碑，充分回归后再合

## 4. 实施顺序与里程碑

| 里程碑 | 内容 | 预期 | 状态 |
|--------|------|------|------|
| I-1 | 可内联判定 + FuncTemplate.Inlinable（无行为变化）| 字节码标记可验证 | ✅ 52fe876 |
| I-2 | const 绑定单表达式箭头函数调用点展开 | closures 类 -20~40% | ✅ 3e66138（收益有限，见下）|
| T-1 | L1 分派瘦身（T-1a~d）| 综合 -5~10% | 🔶 T-1c 完成；T-1b 回退；T-1a/d 待评估 |
| T-2 | superinstruction（OpIncLocal 等）| 循环类 -10~20% | ⬜ |
| I-3 / T-3 | 内联扩展 / 迭代化 | — | ⬜ 后续 |

**实测结果与调整**：
- **I-2**：行为正确（22 包全绿 + 字节码断言 OpCall 被展开替换），但基准仅
  inline-arrow 174ms/1M vs closure-call 177ms——收益有限。原因：fastCallClosure
  已高度优化（单次调用 ~175ns），内联体指令（StoreLocal×2 + 体指令）与调用路径
  指令数相近，而**每指令解释成本（~20ns）主导**。内联价值在后续 T-2
  （superinstruction 合并内联体指令）与寄存器分配放大。
- **T-1b**（operand 单次 `binary.BigEndian.Uint32`）：实测更慢——x86 BSWAP 不如
  编译器优化的手动移位解包，**已回退**。
- **T-1c**（监控/OOM 开关缓存到 VM 字段）：完成，默认热路径零原子 load。
- **T-1a/d**（帧指针 dirty-flag 缓存 + v.local 用缓存帧）：pprof 显示收益来自
  cur()/local()（~8%），但帧指针缓存在 ~25 个递归指令后的刷新易错，风险高——
  待迭代化改造（T-3）时一并处理更安全。
- **pprof 关键发现**：循环基准（`s+=i`）剩余热点是 **装箱**（`engine.Number`
  cum 18% + convT64 15.5% + mallocgcTiny 10%）与 binAdd/isBigInt——engine.Value
  接口设计的固有成本，非分派问题。**值类型化栈**（把数字直接存栈，避免 interface
  装箱）是后续最大收益点，改动大（§6 长期方向）。

## 5. 验证与验收

1. **基准**：`bench-call.js`（closures/methodCall/递归）+ `bench-js2.js` + 新增循环基准（`++i` 计数）
2. **正确性**：
   - `go test ./... -count=1` 全绿
   - 内联回归：实参副作用顺序、嵌套调用、多调用点、arguments/闭包/递归（不内联）、
     `const f=...` 后重新赋值（I-2 只内联 const 不可变绑定）
   - node22 差分（m2/m3/m6）
   - 字节码序列化 round-trip（Inlinable 字段）
3. **性能门禁**：I-2 后 closures 单次 ≤130ns；T-1+T-2 后综合调用类 ≤70x node

## 6. 长期方向

- 迭代化 + 寄存器分配（把局部槽改寄存器化，减少栈访问）——纯解释器进一步提速的关键
- 内联扩展到动态分派（shape 命中时内联 getter/setter）
- 热函数分层（简单函数 L2 直筒，复杂函数 L1 通用）——真正的 tiered JIT 雏形
