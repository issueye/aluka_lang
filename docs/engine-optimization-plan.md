# Aluka 引擎剩余优化开发计划（JIT / 字节码 / AST）

> 文档版本：v1.0 ｜ 日期：2026-08-13
> 代码基线：`5ddc7ef`（feat: 按帧预分配栈 + vmstackcheck 校验门）
> 依据：对 `internal/engine/{jit,bytecode,ast}` 与 `interpreter/vm.go`、`shape.go` 的代码审阅
> 配套文档：[jit-follow-up-development-plan.md](./jit-follow-up-development-plan.md) / [jit-performance-optimization-plan.md](./jit-performance-optimization-plan.md) / [perf-optimization-plan.md](./perf-optimization-plan.md) / [bytecode-spec.md](./bytecode-spec.md)

---

## 1. 目的与完成定义

本计划承接 JIT R1-R5 里程碑与字节码优化器 v1，对引擎三层（AST / 字节码 / JIT）中
**已识别但未落地**的剩余优化空间，按收益与风险排序拆成可实施、可验证的里程碑。

本文回答三个问题：下一步优化做什么、按什么顺序、每一步留下什么证据才算完成。

### 1.1 总体完成定义（缺一不可）

1. **语义不变**：所有优化必须保证与 Tier 0（字节码 VM）逐位一致，含 `NaN`/`±0`/`±Inf`、
   异常时序、`Proxy`/accessor 拦截、副作用顺序；
2. **可回退**：任何优化都有显式 guard，guard 失败回退到上一稳定层，不扩大支持面掩盖问题；
3. **可观测**：新增 IC / trace / 优化 pass 均暴露 `--ic-stats` / `--jit-stats` 或等价指标；
4. **有证据**：每项优化落地时同时提交 benchmark 前后对比、差分测试与（JIT 类）fuzz 回归；
5. **门禁不倒退**：优化不得使既有门禁回退——JIT 三 tier 差分零失配、全量 `go test ./...` 零失败、
   字节码缓存 `FormatVersion` 一致性、冷启动回退 ≤ 5%。

### 1.2 优化原则

1. Tier 0 是最终语义兜底，优化器与 JIT 均不得绕过异常处理、监控、覆盖语义；
2. 任何优化假设必须对应显式 guard，guard 必须先于相关副作用；
3. 字节码层改动必须 bump `serialize.go` 的 `FormatVersion` 并登记 `meta.go` 元数据；
4. JIT 层改动必须走 lowering → verifier → Quick → trace → Native（或显式拒绝）→ 差分 的完整链路；
5. 性能报告必须同时给出 `off / quick / auto` 三档，不得用 Auto 收益掩盖 Tier 0 回退。

---

## 2. 当前基线

### 2.1 三层现状（代码审阅结论）

| 层 | 已完成 | 关键实现 |
|----|--------|---------|
| **AST** | 节点定义 + 接口抽象；编译期一次性使用，字节码缓存命中时跳过 | `ast.go`（接口 + 堆分配节点） |
| **字节码** | 4 轮迭代优化器：常量折叠（number/string/bigint）、跳转穿透、死代码删除、`LOAD+GET_PROP→GET_PROP_LOCAL`、`STORE+LOAD→DUP+STORE`、push-pop 消除 | `bytecode/optimize.go`、`meta.go`（元数据单一事实源） |
| **JIT** | R1-R5 完成、默认 auto：Quick 类型化 IR + Go executor、amd64 Native（W^X/无指针 Frame/safepoint）、属性 PIC 2-4 shape、调用内联 0-4 参数、数组 push 范围 trace、闭包特化、const-only LICM | `jit/ir.go`、`trace.go`、`native_emit_amd64.go`、`property_pic.go` |

### 2.2 性能快照（Windows amd64，2026-08-11）

| 用例 | Tier 0 (off) | Quick | Auto Native | Auto/Tier 0 |
|------|-------------|-------|-------------|-------------|
| 数值循环 `sum(3M)` | 458.21ms | 86.67ms | 5.74ms | 79.8x |
| 属性读 `propAccess-3M` | 572.96ms | 204.54ms | 7.76ms | 73.9x |
| 属性写 `propSet-3M` | 372.32ms | 121.29ms | 7.16ms | 52.0x |
| 调用 `callOverhead-1M` | 156.35ms | 49.11ms | 4.01ms | 39.0x |
| 方法调用 `methodCall-1M` | 175.25ms | 51.05ms | 3.64ms | 48.1x |
| 闭包 `closureCall-1M` | 190.64ms | 2.61ms | 2.15ms | 88.7x |
| 11 项合计 | 2637.80ms | 1107.91ms | 698.62ms | 3.78x |
| `mixed.js` 墙钟 | 471.09ms | 244.13ms | 199.79ms | 2.36x |

### 2.3 已识别的优化缺口（依据 §1 的代码审阅）

| 编号 | 层 | 缺口 | 证据 | 优先级 |
|------|-----|------|------|--------|
| O1 | 字节码 | 全局变量访问无 IC | `vm.go:582` `globalObj.Get` 每次 map 查找 | P0 |
| O2 | 字节码 | 数组索引访问做字符串转换 | `vm.go:2280` 每次 `strconv.Atoi(key)` | P0 |
| O3 | JIT | Native 无寄存器分配（locals 不寄存器化、MaxStack≤8 只用 xmm0-7） | `native_emit_amd64.go` `emitLoadF64/StoreF64` 内存往返 | P1 |
| O4 | JIT | LICM 仅 const-only，`arr.length` 未 hoist | `optimize.go` `OptimizeProgram` 无运行时不变量提升 | P1 |
| O5 | JIT | 通用数组下标读写无 trace（仅 push 范围特化） | `candidate.go` `OpGetElem` 无 trace 特化 | P2 |
| O6 | 字节码 | superinstruction 覆盖不足（SET_PROP_LOCAL/链式 GET_PROP/CALL+POP/增量） | `optimize.go` 仅 GET_PROP_LOCAL + DUP+STORE | P2 |
| O7 | JIT | `quickValue` 16 字节未紧凑化 | `ir.go:1268` struct{float64;kind;ref;bool} | P2 |
| O8 | 字节码 | 常量池未去重、死局部槽未复用、frame 每次 re-fetch | `vm.go:507` / `compiler.go` | P3 |
| O9 | JIT | 内联限 0-4 参数叶子；分支布局未优化 | `candidate.go` / `native_emit_amd64.go` | P3 |
| O10 | AST | 节点堆分配 + 接口分派（仅首次编译受益） | `ast.go` | P3 |

---

## 3. 里程碑划分与路线

```
M0 基线与验证框架固化
      │
      ├──> M1 字节码低垂果实（O1 全局 IC + O2 数组索引）    [P0，独立]
      │
      ├──> M2 Native 寄存器分配（O3）                      [P1，最高技术含量]
      │
      ├──> M3 循环优化（O4 LICM 扩展 + O5 数组读写 trace）  [P1/P2，依赖 M2 部分能力]
      │
      ├──> M4 字节码 superinstruction 与杂项（O6/O8）       [P2/P3，独立]
      │
      └──> M5 Quick 紧凑化与内联扩展（O7/O9 + AST O10）    [P2/P3，收尾]
```

| 里程碑 | 主题 | 层 | 预计工作量（工程师日） | 前置 |
|--------|------|-----|----------------------|------|
| M0 | 基线与验证框架固化 | 全层 | 2-3 | — |
| M1 | 全局 IC + 数组索引去字符串化 | 字节码 | 3-5 | M0 |
| M2 | Native 寄存器分配 | JIT | 8-12 | M0 |
| M3 | LICM 扩展 + 通用数组读写 trace | JIT | 8-12 | M1、M2 |
| M4 | superinstruction 扩展 + 字节码杂项 | 字节码 | 4-6 | M0（可与 M2 并行） |
| M5 | Quick NaN-boxing + 内联扩展 + AST 收尾 | JIT/AST | 6-10 | M2、M3 |

工作量仅用于排序，不是交付日期承诺。M1 与 M4 属字节码层可并行；M2 是 M3/M5 的前置。

---

## 4. M0：基线与验证框架固化

### 4.1 目标

在动任何优化前，固化可复现的性能基线与验证协议，防止后续用噪声数据固化错误结论。

### 4.2 任务

| ID | 任务 | 输出 |
|----|------|------|
| M0-1 | 建立三层（off/quick/auto）基准矩阵快照，覆盖 §2.2 全部用例 + 新增全局访问 / 数组索引 / 链式属性 / 循环累加 4 类微基准 | `bench/` 新增用例 + 快照归档 |
| M0-2 | 固化测量协议：每项 5 次中位数、安静环境、`--jit-stats` / `--ic-stats` 同时采集 | `docs/performance-report-*.md` 协议段落 |
| M0-3 | 建立"优化前后对比"模板（每项优化必须附 off/quick/auto 三档 + 指令数/IC 命中率/guard 统计） | 对比模板 |

### 4.3 完成定义（完成事实）

- [ ] 基准连续两轮中位数偏差 ≤ 5%（环境噪声可控）；
- [ ] 新增 4 类微基准可在 `make bench` 或等价命令一键复现；
- [ ] 全局访问 / 数组索引用例存在 **Tier 0 可观测的热点指标**（IC miss 计数或指令计数），可量化 M1 收益；
- [ ] 对比模板被至少一个后续里程碑实际引用。

### 4.4 风险

| 风险 | 应对 |
|------|------|
| 本机噪声（历史上 ±30%） | 固定安静环境；偏差超 5% 时暂停优化、先定位噪声 |

---

## 5. M1：字节码低垂果实（O1 + O2）

### 5.1 目标

落地两项投入小、见效快、全代码受益（含非 JIT 路径）的字节码层优化。

### 5.2 任务

| ID | 任务 | 输出 | 说明 |
|----|------|------|------|
| M1-1 | 全局变量 IC：`OpLoadGlobal/OpStoreGlobal` 增加按 (name → 全局对象槽位) 的缓存，命中时直读槽位，全局 shape 变化自动失效 | `interpreter` + `shape.go` 扩展 | 仿照对象属性 `icEntry`，验证 globalObj 的 shape 后直读 |
| M1-2 | 数组索引去字符串化：新增 `OpGetElemInt/OpSetElemInt`（编译期可确定的非负数值下标），VM 直读 `Elems()`，跳过 `strconv.Atoi` | `bytecode/opcodes.go` + `compiler` + `vm.go` | 需登记 `meta.go` 元数据 + bump `FormatVersion` |
| M1-3 | 非确定下标走运行时快分支：key 为纯数字字符串时在 VM 侧直接解析，不落通用属性路径 | `vm.go` | 覆盖 `arr[expr]` 动态下标 |

### 5.3 完成定义（完成事实）

- [ ] `--ic-stats` 新增全局访问命中率，微基准下 miss 率显著下降；
- [ ] 数组索引微基准 Tier 0 提升可量化（目标：去 Atoi 后索引循环 ≥ 20% 提升，以 M0 快照为准）；
- [ ] 语义零回归：全量 `go test ./...` 零失败；test262 子集 8/8 无回归；node22 差分 18 场景无回归；
- [ ] `OptimizeModule` 对拍（`optimize_equivalence_test.go`）通过；`FormatVersion` 已 bump 且旧缓存拒绝正确；
- [ ] 全局 IC 对 `Proxy`/`delete`/`Object.defineProperty` 变更全局属性的语义与 Tier 0 一致（差分用例覆盖）。

### 5.4 风险

| 风险 | 应对 |
|------|------|
| 全局对象属性被删除/重定义后 IC 失效不彻底 | 以 globalObj 的 shape 版本号做 guard；`delete`/`defineProperty` 走失效路径 |
| `OpGetElemInt` 与负索引/稀疏数组语义不一致 | 仅对"非负整数且 < len"走快路径，其余回退通用路径 |

### 5.5 实施记录（2026-08-13）

- **M1-2/3 数组索引读快路径 ✅ 落地**：`vm.go` 新增 `tryArrayIndexGet`（number key + ArrayValue
  直读元素，绕过 `propertyKeyOf` 的 number→string 与 `getProperty` 的 `strconv.Atoi` 双重转换）。
  未引入新指令——运行时类型判断即可覆盖 `arr[i]` 动态下标，`OpGetElemInt`（常量下标）收益更小故不再做。
  benchmark（jit.Off，循环体 5 次固定索引读）基线 112.0ms → 优化后 93.4ms，**~16.6% 提升**（5 次中位数，可复现）。
  语义精确对齐：非负整数且 ≤ 2^53-1 走快路径，越界返回 undefined 不查原型，负数/非整数/NaN/±Inf 回退。
- **M1-2 写侧：数组索引写快路径 ✅ 落地**：`value.go` 新增 `ArrayValue.SetIndex`（复用 Set 数值索引
  核心逻辑，去掉 string/Atoi），`vm.go` 新增 `tryArrayIndexSet` 接入 `OpSetElem/OpSetElemTop`。
  benchmark（jit.Off，循环体 5 次固定索引写）基线 184.0ms → 优化后 107.5ms，**~41.6% 提升**。
  写侧收益远大于读侧：慢路径除 Atoi 外还有 setProperty 的 Proxy 检查 + FindAccessor 原型链遍历 +
  length 同步。语义精确对齐：越界自动稀疏填充 + 同步 length；负数/非整数/NaN/±Inf 回退普通属性路径。
- **M1-1 全局变量 IC ❌ 测量否定，已回退**：仿照对象属性 IC 在 `OpLoadGlobal/OpStoreGlobal` 接入
  `GetCached/SetCached` 后，benchmark 无收益（全局对象 shape.index 的 map 查找本就很快，IC 省下的
  开销被类型断言 + 哈希抵消），且会污染 IC 表（与对象属性访问竞争 2048 槽）。依据"基于测量证据"
  原则回退。真正的全局变量优化需编译期确定全局槽位（context slot），不在本里程碑范围。
- 测试：`m1_optimize_test.go` 新增读/写快路径用例 + 快/慢路径语义对照 + benchmark；全量 `go test ./...`
  零失败，jitdiff 三 tier 零失配。

---

## 6. M2：Native 寄存器分配（O3）

### 6.1 目标

为 amd64 Native 后端引入寄存器分配，消除热 local 的内存往返，是 JIT 层剩余收益最大的一项。

### 6.2 任务

| ID | 任务 | 输出 | 说明 |
|----|------|------|------|
| M2-1 | 值活跃区间分析（liveness）与线性扫描分配器 | `jit/` 新增 `regalloc.go` | 以 IR 基本块为单位，构建 def-use 链 |
| M2-2 | 热 local 提升：循环内多次读写的数值 local 驻留 XMM（xmm0-xmm15 全用），仅语义出口/deopt 点写回无指针 Frame | `native_emit_amd64.go` 重构 | 保持"Native 不保存 Go 指针"约束 |
| M2-3 | `MaxStack` 放宽（8 → 受 Frame 约束的更大值），操作数栈 spill 策略 | `native_program.go` + Frame 布局 | spill 槽仍走无指针 Frame，兼容 deopt 恢复 |
| M2-4 | deopt 状态恢复同步：寄存器化 local 在 guard 失败 / 异常 / yield 出口统一写回 Frame，`resumePC` 语义不变 | `trace.go` + `native_trace.go` | 复用既有 dirty-local 两阶段提交协议（R1-5） |
| M2-5 | 差分与 fuzz 回归扩展 | `jitdiff` + `fuzz_test.go` | 覆盖寄存器溢出、跨调用存活、异常出口 |

### 6.3 完成定义（完成事实）

- [ ] jitdiff 三 tier（off/quick/auto）零失配，含新增的寄存器压力用例（>8 个活跃值、跨基本块存活）；
- [ ] 5 个 Go fuzz target 运行无 panic / 无 RX 泄漏，种子语料更新；
- [ ] `ALUKA_JIT_VERIFY=1` 下 Native 与 Quick 结果逐位一致；
- [ ] 数值循环 / 属性循环 / 混合算术 microbenchmark 相对 M0 快照提升可量化（目标：数值循环 ≥ 1.5x、属性循环 ≥ 1.3x，具体以 M0 为准）；
- [ ] 冷启动回退 ≤ 5%，`--jit-stats` 无新增未解释 deopt；
- [ ] 不支持平台自动 fallback 到 Quick/Tier 0 行为不变。

### 6.4 风险

| 风险 | 应对 |
|------|------|
| 寄存器化与 deopt 恢复不一致导致状态丢失 | 强制：任何可能出口处寄存器→Frame 写回必须与 dirty-local 位图一致，verifier 显式校验 |
| spill 与无指针 Frame 布局冲突 | spill 槽仅存 float64 标量，不进 Go 指针区 |
| 分配器复杂度引入正确性回归 | 分阶段：先局部（循环体）后全局；每阶段跑 jitdiff + fuzz |

---

## 7. M3：循环优化（O4 LICM 扩展 + O5 数组读写 trace）

### 7.1 目标

把循环体内可 hoist 的运行时计算（`arr.length` 等）与通用数组下标读写纳入 Native 特化。

### 7.2 任务

| ID | 任务 | 输出 | 说明 |
|----|------|------|------|
| M3-1 | LICM 扩展到运行时循环不变量：识别 trace 内不可写的属性/局部，带 guard 提升出循环 | `jit/optimize.go` | guard：被 hoist 对象的 shape + 属性值在循环内不变 |
| M3-2 | 通用数组下标读写 trace：`OpGetElem/OpSetElem` 数值下标 + packed 数组（非稀疏）→ 边界 guard + 直读槽位 | `jit/` 新增 array trace | 对标 V8 elements-kind，guard 覆盖 length/密度/元素类型 |
| M3-3 | 数组越界与稀疏数组回退协议 | 同上 | 越界 → 语义出口 / yield；稀疏 → 稳定回退 Tier 0 |
| M3-4 | 差分与 fuzz 覆盖数组边界 / 越界 / 改 length / 稀疏场景 | `jitdiff` + fuzz | 固定种子 + 随机 mutation |

### 7.3 完成定义（完成事实）

- [ ] `for(i<arr.length)` 形态在 `--jit-stats` 中显示 length 访问被 hoist（guard 计数 + 指令数下降）；
- [ ] 数组读写 trace 在 jitdiff 三 tier 零失配，含越界 / 稀疏 / 改 length 边界用例；
- [ ] 数组读写 microbenchmark 相对 M0 提升可量化（目标：≥ 1.3x）；
- [ ] 副作用两阶段提交协议（R1-5）对数组写仍然成立：guard 失败不重复、不遗漏写；
- [ ] 全量测试零失败、test262 8/8、node22 差分无回归。

### 7.4 风险

| 风险 | 应对 |
|------|------|
| 稀疏数组 / 越界语义复杂 | 保守识别 packed 数组；任何不确定形态回退 Tier 0 |
| LICM guard 遗漏导致读到过期 length | guard 显式覆盖 length 与 shape；写 length 的路径不进入 trace |

---

## 8. M4：字节码 superinstruction 扩展与杂项（O6 + O8）

### 8.1 目标

扩充 superinstruction 覆盖、压缩常量池与帧，属字节码层收益中等但独立的优化，可与 M2 并行。

### 8.2 任务

| ID | 任务 | 输出 | 说明 |
|----|------|------|------|
| M4-1 | `LOAD_LOCAL+SET_PROP → SET_PROP_LOCAL`、链式 `GET_PROP` 融合、`CALL+POP` 融合、循环增量指令（`x += n`） | `optimize.go` | 复用现有 rewriteGroup 框架 |
| M4-2 | 常量池去重（同值 string/number/bigint 复用索引） | `bytecode` / `compiler` | 需评估对 IC key 稳定性影响 |
| M4-3 | 死局部槽复用（活跃区间分析缩小 `NumLocals`） | `compiler` | 帧更小，减少分配 |
| M4-4 | VM 主循环 frame re-fetch 优化（仅 call 后重取） | `vm.go` | 指针版本号或调用类指令后重取 |

### 8.3 完成定义（完成事实）

- [ ] 每条新 superinstruction 登记 `meta.go` 元数据 + bump `FormatVersion`；
- [ ] `optimize_equivalence_test.go` 对拍通过；指令数下降可量化（`OptimizationStats` 记录）；
- [ ] 字节码缓存旧版本拒绝正确、新版本往返无差；
- [ ] 全量测试零失败；jitdiff 三 tier 无回归（superinstruction 可能改变 JIT lowering 输入）；
- [ ] 帧大小 / 常量池体积在代表性项目（express demo）上有可量化下降。

### 8.4 风险

| 风险 | 应对 |
|------|------|
| 常量去重影响 IC key / 序列化兼容 | 去重仅限编译期新增常量，不合并既有索引；充分差分 |
| superinstruction 破坏 JIT candidate 识别 | 同步更新 `candidate.go` 的 opcode 白名单与 lowering |

### 8.5 实施记录（2026-08-13）

- **M4-1 SET_PROP_LOCAL_TOP ❌ 测量否定，已回退**：实现 `LOAD_LOCAL+SET_PROP_TOP → SET_PROP_LOCAL_TOP`
  融合（opcodes/meta/optimize/vm/FormatVersion 21→22 + JIT candidate/trace 展开），验证融合确实发生
  （`o.a=1;o.b=2` 均融合）。但 benchmark（jit.Off，循环体 5 次局部对象属性写）基线 96.3ms → 优化后 96.9ms，
  **无收益**（±1% 噪声）。
- **重要发现（引出新优化方向）**：属性写无收益的根因是 `setProperty` 的 `FindAccessor`（原型链遍历找
  accessor）位于 IC（`SetCached`）**之前**，每次写都遍历原型链，稀释了 superinstruction 省下的 LOAD_LOCAL
  dispatch。对比：属性读的 `getProperty` 把 IC（`GetCached`）放在**最前**，命中即返回，故 GET_PROP_LOCAL 有
  19% 收益、SET_PROP_LOCAL_TOP 却无。
- **写入 IC 前置 ✅ 落地（~33.5%）**：把 `SetCached` 提到 `FindAccessor` 之前（Proxy 之后），own 数据属性
  命中时直写槽位、跳过 FindAccessor 的 shape 查找；`SetCached` 新增 accessor 槽位拒绝（命中但槽位为
  AccessorValue 时返回 false，回退 FindAccessor 调 setter），保证 getter/setter 语义不变（own 数据属性按
  JS 语义遮蔽原型 accessor）。benchmark（jit.Off，循环体 5 次对象属性写）基线 101.5ms → 优化后 67.5ms，
  **~33.5% 提升**（5 次中位数）。测试覆盖 accessor setter 调用 / 原型 setter / delete 后重写；全量零失败，
  jitdiff 三 tier 零失配。
- 其余 M4 项（链式 GET_PROP、CALL+POP、常量去重、槽位复用、frame re-fetch）经评估收益均属低垂果实已摘尽
  的"尾部"，优先级下调，不再单独推进。

---

## 9. M5：Quick 紧凑化 + 内联扩展 + AST 收尾（O7 + O9 + O10）

### 9.1 目标

收尾低优先级项：Quick 值紧凑化、内联扩展，以及 AST 层仅首次编译受益的分配优化。

### 9.2 任务

| ID | 任务 | 输出 | 说明 |
|----|------|------|------|
| M5-1 | `quickValue` NaN-boxing（16 → 8 字节） | `jit/ir.go` | Number 用 double 本体，非 Number 用 NaN 空间编码 kind/ref |
| M5-2 | 内联扩展：参数 >4、有限深度非叶子、方法 IC 命中直接内联 | `jit/candidate.go` + bridge | 成本模型控制，超预算回退 |
| M5-3 | 分支布局：热路径基本块排序 / likely 分支 fall-through（需 profile 数据） | `native_emit_amd64.go` | 无 profile 时保守不排序 |
| M5-4 | AST 节点分配池化 / 位置信息 side-table（仅 `--no-cache` 首次编译受益） | `ast.go` + `compiler` | 评估收益后决定是否落地 |

### 9.3 完成定义（完成事实）

- [ ] NaN-boxing 后 Quick 层内存带宽下降可量化，jitdiff 三 tier 零失配；
- [ ] 内联扩展后 call/method microbenchmark 相对 M0 提升，且 deopt/guard 统计无异常增长；
- [ ] AST 优化（若落地）首次编译延迟下降可量化，字节码产物逐字节不变；
- [ ] 全量测试 + test262 + node22 差分 + jitdiff + fuzz 全部零回归；
- [ ] 最终归档一份完整的 off/quick/auto 三档性能报告，对照 M0 快照。

### 9.4 风险

| 风险 | 应对 |
|------|------|
| NaN-boxing 编码错误引入值语义破坏 | 全量差分 + 针对性 NaN/-0/Inf/大整数用例 |
| 内联过度导致代码膨胀 | 严格成本模型 + LRU 淘汰 + 编译预算门禁 |

---

## 10. 依赖关系图

```
        ┌──── M0 基线与验证框架 ────┐
        │                          │
        ▼                          ▼
┌────── M1 ──────┐          ┌───── M2 ─────┐          ┌───── M4 ─────┐
│ 全局IC+数组索引 │          │ 寄存器分配    │          │ superinst+杂项│
└───────┬────────┘          └──────┬───────┘          └──────────────┘
        │                          │
        └──────────► M3 ◄──────────┘
              LICM+数组trace
                      │
                      ▼
                    M5（收尾）
```

M1、M2、M4 均只依赖 M0，可并行；M3 依赖 M1（数组 trace 复用索引快路径）+ M2（寄存器 spill 能力）；M5 依赖 M2/M3。

---

## 11. 验证与门禁体系

每项优化落地时，强制通过以下门禁（复用项目既有测试约定）：

| 门禁 | 命令 | 适用层 |
|------|------|--------|
| 全量单测 | `CGO_ENABLED=0 go test ./...` | 全层 |
| JIT 差分 | `go test ./internal/engine/interpreter/jitdiff/ -count=1` | JIT |
| JIT fuzz | `go test ./internal/engine/jit/ -run='^$' -fuzz=FuzzVerifyProgram -fuzztime=60s` | JIT |
| 字节码优化对拍 | `go test ./internal/engine/interpreter/ -run OptimizeEquivalence` | 字节码 |
| race（JIT 相关） | `CGO_ENABLED=1 go test -race ./internal/engine/jit/... ./internal/engine/interpreter` | JIT |
| conformance | node 11/11、test262 8/8、node22 18 场景、build 19/19、express 6/6 | 全层 |

性能报告必须同时给出 `off / quick / auto` 三档，并以 M0 快照为对照，禁止单档夸大。

---

## 12. 变更记录

| 版本 | 日期 | 变更 |
|------|------|------|
| v1.5 | 2026-08-13 | 读路径对称检查：IC 已前置到位，两处尾巴简化为代码质量改进（无性能收益） |
| v1.4 | 2026-08-13 | M4 后续：写入 IC 前置落地（~33.5%） |
| v1.3 | 2026-08-13 | M4 实施：SET_PROP_LOCAL_TOP 测量否定回退；发现 setProperty FindAccessor 前置优化机会 |
| v1.2 | 2026-08-13 | M1 写侧：数组索引写快路径落地（~41.6%） |
| v1.1 | 2026-08-13 | M1 实施：数组索引读快路径落地（~16.6%）；全局 IC 测量否定回退 |
| v1.0 | 2026-08-13 | 初稿：基于 JIT/字节码/AST 代码审阅，制定 M0-M5 里程碑与完成定义 |
