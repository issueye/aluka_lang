# aluka JIT 方向性能优化计划

> 文档版本：v2.11
> 日期：2026-08-11
> 性能基线：`docs/performance-report-v4.md`，commit `900833b`
> 关联方案：`docs/hot-function-compile-plan.md`、`docs/inline-tiered-bytecode-plan.md`、
> `docs/typed-value-stack-plan.md`

## 1. 背景与结论

v4 已完成调用快速路径、小函数内联、Shape O(N^2) 修复、正则缓存和监控门控：

| 指标 | v4 结果 | 与 Node 22.23.1 差距 |
|------|---------|----------------------|
| 11 个微基准合计 | 2655.74ms | 42.5x |
| 混合负载（含启动） | 688ms | 8.5x |
| 属性读 / 写 | 583.36ms / 370.47ms | 212x / 152x |
| 普通调用 / 方法调用 | 157.37ms / 187.67ms | 111x / 125x |
| fib30 | 327.19ms | 59.2x |

继续削薄通用解释器仍有价值，但不能消除三个结构性成本：

1. 每条字节码都要解码、分派并维护栈状态；
2. `engine.Value` 接口使数字中间值反复装箱；
3. 属性访问和调用即使 IC 命中，也仍要经过通用 JS 语义分支。

已有实验也限定了可行路线：

- 迭代化调用实测整体 `+7.3%`，已回退，不再投入；
- 32 字节 `stackSlot` 实测整体 `+7%`，受 Go GC 和接口布局限制，已暂停；
- 编译期内联收益有限，说明瓶颈已经从“调用次数”转为“解释执行次数”；
- `NativeCallback` 在 `arrayMap` 上把差距从约 16x 收窄到 9.4x，证明“热点代码脱离
  通用解释循环”是当前最有效的方向。

因此本计划采用**分层执行 + 受限 JIT**：先构建热点画像和可退化的类型化 IR，随后只为
可证明安全的数值热点生成机器码。复杂 JS 语义始终可回退到现有字节码解释器。

> 决策：不把“运行时生成 Go 源码再 `go build`”视为 JIT。Go 不能在进程内动态加载普通
> Go 函数；直接生成机器码虽可做原型，但不属于 Go 官方稳定 ABI，必须先通过独立可行性门禁。

## 2. 目标与非目标

### 2.1 总目标

- 保持 Node 22 兼容性和现有字节码解释器的完整语义兜底；
- 使稳定数值函数、数值循环和单态属性访问退出逐指令解释；
- 微基准合计差距由 42.5x 分阶段压到 **15x 以内**；
- 混合负载由 8.5x 压到 **4x 以内**，且冷启动回退不超过 5%；
- JIT 支持范围内的数值/调用/属性热点力争达到 Node 的 **5-10x**；
- JIT 关闭时行为和 v4 基线一致，JIT 失败时自动回退且结果不可见地保持一致。

以上是路线目标，不是对首个原型的收益承诺。每一阶段都设继续/停止门禁，未达到门禁则
保留已验证的 Tier 1 优化，不继续扩大机器码后端。

### 2.2 首期非目标

- 不实现完整 ECMAScript 优化编译器；
- 不编译 async/generator、`eval`、`with`、Proxy、try/finally、动态参数和复杂闭包；
- 不在首期让机器码直接调用任意 Go 函数或直接持有 Go 对象指针；
- 不做跨进程机器码缓存；
- 不承诺 macOS/arm64 首发支持；
- 不删除解释器，也不以牺牲冷启动和 Node 兼容性换取单一基准成绩。

## 3. 分层执行模型

| 层级 | 名称 | 执行形式 | 适用范围 | 失败处理 |
|------|------|----------|----------|----------|
| Tier 0 | Baseline | 现有字节码 VM | 全部 JS | 最终语义兜底 |
| Tier 1 | Quick JIT | 类型反馈 + 类型化线性 IR 的 Go 执行器 | 数值表达式、简单循环、简单叶子函数 | guard 失败回 Tier 0 |
| Tier 2 | Native JIT | amd64 机器码 | 无分配、无外部调用的数值 trace/函数 | 返回退出码并恢复 Tier 0 |
| Tier 3 | Optimizing JIT | shape/call guard、有限内联、循环优化 | 单态属性、稳定调用点、自递归数值函数 | deopt 到精确字节码 PC |

Tier 1 不是一次性过渡代码。它承担 IR 语义验证、类型反馈消费、guard/deopt 协议验证，并在
不支持机器码的平台上作为长期可用的优化层。已有 `NativeCallbackDesc` 和 `Inlinable` 可逐步
迁移为 Tier 1 的前端能力，避免再增加互不兼容的模式执行器。

执行流程：

```text
字节码函数 / 循环回边
        |
        v
热点计数 + 类型/shape 反馈
        |
        v
候选判定 ---- 不支持/预算不足 ----> Tier 0
        |
        v
构建类型化 IR -> 校验 -> Tier 1 执行
        |
        v
达到更高阈值且平台支持
        |
        v
Native codegen -> W^X 发布 -> Tier 2/3
        |
        +---- guard 失败/中断/异常 ----> deopt -> Tier 0 指定 PC
```

## 4. 热点与反馈系统

### 4.1 VM 本地状态

`FuncTemplate` 属于可序列化、可跨 VM 复用的不可变字节码，不在其中放运行期计数器或原生
代码指针。建议在 VM 内维护：

```go
type funcJITState struct {
    calls       uint32
    backedges   uint32
    tier        uint8
    failures    uint8
    compiling   bool
    feedback    []siteFeedback
    quickCode   *jit.Program
    nativeCode  *jit.NativeCode
}

// key 为当前 module 内 FuncTemplate 的稳定索引，而不是可持久化指针。
type jitStateTable []funcJITState
```

运行时可按 `(module instance, function index)` 定位状态。模块重入、多个 VM 和 worker 不共享
可变状态，避免锁和跨实例污染；后续只共享只读机器码时再单独设计生命周期。

### 4.2 采样点与默认阈值

- 函数入口：普通调用计数；
- 循环回边：只在负跳转目标计数；
- 类型反馈：算术操作数类型、返回值类型、属性 shape、调用目标模板；
- 初始阈值建议：Tier 1 为 `1000 calls` 或 `10000 backedges`，Tier 2 为 Tier 1 执行
  `10000` 次后；最终值由真实负载校准；
- 采用饱和计数和抽样反馈，未开启 `--jit-stats` 时不做每指令全量统计；
- 每个 VM 设置编译时间预算和代码内存预算，避免短命脚本触发编译风暴。

### 4.3 候选过滤

首期只有同时满足以下条件的函数/trace 才进入 Tier 1：

- 非 async、非 generator、无 try/finally、无动态 `arguments`；
- IR 中只含局部变量、常量、数值算术/比较、条件跳转和有限返回；
- 不含可能执行用户代码的隐式转换；
- 数字 guard 明确区分 Number、BigInt 和其他类型；
- 控制流图可还原到精确字节码 PC，任意 guard 点都有完整恢复映射。

候选失败应记录原因并对该函数降温/拉黑，避免每次达到阈值后重复编译。

## 5. JIT IR 设计

新增独立包，建议目录如下：

```text
internal/engine/jit/
  profile.go          热点和反馈数据结构
  ir.go               SSA-lite / 类型化线性 IR
  lower.go            bytecode -> IR
  verify.go           IR 与 deopt 映射校验
  optimize.go         常量折叠、复制传播、DCE、循环优化
  executor.go         Tier 1 Go 执行器
  native.go           NativeCode 生命周期与统一接口
  backend/amd64/      amd64 汇编编码和寄存器分配
  execmem/            Windows/Linux W^X 内存管理
internal/engine/interpreter/
  jit_bridge.go       VM 栈、反馈、进入/退出和 deopt 恢复
```

避免 `jit` 反向依赖 `interpreter`。`interpreter/jit_bridge.go` 把字节码和运行时值转换为 JIT
包只读输入，并负责恢复 VM 状态。

### 5.1 最小 IR

首期 IR 只需要覆盖：

- `ConstF64`、`LoadLocal`、`StoreLocal`；
- `AddF64`、`SubF64`、`MulF64`、`DivF64`、`ModF64`、`NegF64`；
- `EqF64`、`NeF64`、`LtF64`、`LeF64`；
- `Jump`、`Branch`、`Phi`、`Return`；
- `GuardNumber`、`GuardFinite`（只在优化确有需要时使用）；
- `SafepointPoll`、`Deopt(exitID)`。

IR 必须保留 JS Number 的 `NaN`、`Infinity`、`-0` 和除零语义。不能用 Go/CPU 整数运算替代
浮点运算，除非存在范围 guard 且 deopt 映射完整。

### 5.2 优化顺序

第一批只实现可独立验证的优化：

1. 常量折叠和复制传播；
2. 无效存储/不可达块删除；
3. 局部变量寄存器化，消除解释器 push/pop；
4. 循环不变量外提，但只允许无调用、无属性写的纯数值表达式；
5. 单态自调用内联作为 Tier 3 单独里程碑。

不在第一批实现激进类型推断。所有类型结论必须来自静态证明或运行时 guard。

## 6. Native JIT 的 Go 边界

### 6.1 调用协议

直接把任意 Go 函数指针转换后调用不受 Go ABI 保证。Native JIT 原型使用每个平台固定的
汇编 trampoline，在 Go ABI 与自定义 JIT ABI 间转换：

```text
Go VM -> arch trampoline -> generated code -> arch trampoline -> Go VM
```

生成代码只接收一个不含 Go 指针的 `JITFrame`：数值局部变量区、退出状态、恢复 PC、执行
预算和结果。原生代码执行期间：

- 不调用 Go，不分配 Go 对象，不触发 JS getter/Proxy/用户回调；
- 不把 Go heap 指针写入原生代码页或跨退出保存；
- 每个循环按执行预算退出到 Go，给 GC、取消和 OOM 检查提供机会；
- trampoline 返回后由 Go 执行 `runtime.KeepAlive` 并恢复 VM；
- 任何异常路径返回状态码，不做跨原生帧的 Go panic/longjmp。

原型必须单独验证 Go 1.25.x 的 GC、栈增长、异步抢占、race 构建和 Windows CFG/DEP 行为。
若这些门禁无法稳定通过，则 Tier 2 停止，Tier 1 继续作为正式方案。

### 6.2 可执行内存

- Windows amd64：`VirtualAlloc` 分配 RW，写入后 `VirtualProtect` 切换为 RX；
- Linux amd64：`mmap` RW，写入后 `mprotect` 切换为 RX；
- 禁止常驻 RWX 页面；修改代码时重新申请或先回到 RW，且执行期间不可并发修改；
- 代码页按 VM/代码缓存统一释放，达到预算后按 LRU/低热度淘汰；
- 机器码不写入磁盘缓存。字节码/IR 缓存与 Go 版本、架构、CPU feature、JIT ABI 版本绑定。

### 6.3 平台顺序

1. Windows amd64：当前主要开发/基准环境；
2. Linux amd64：CI 和服务端常见环境；
3. arm64：在 amd64 收益稳定后评估；
4. macOS：受 hardened runtime、`MAP_JIT` 和签名约束，首期不承诺。

未支持平台自动使用 Tier 1，不影响功能。

## 7. Guard、失效与退优化

每个优化假设都必须是显式 guard，不能依赖“基准里通常如此”。首期退出原因至少包括：

| 退出原因 | 处理 |
|----------|------|
| 局部变量不是 Number | 恢复到该操作前的字节码 PC |
| 发现 BigInt/String/对象转换 | 回 Tier 0，当前 trace 降温 |
| 执行预算耗尽 | 保存数值局部变量，回 Go 做中断/GC/OOM 检查后可重入 |
| shape/callee 改变（Tier 3） | IC 更新，相关 native code 失效或转为多态 |
| 抛出异常或需调用用户代码 | 在副作用发生前退出，由解释器完成操作 |
| 代码缓存被淘汰 | 原子切回 Tier 1/Tier 0 入口，再释放代码页 |

每个 deopt exit 保存：`resumePC`、当前虚拟寄存器到 VM local/operand stack 的映射、已发生
副作用标记和返回位置。恢复后不得重复执行已经完成的属性写、调用或迭代器推进。

首期原生 IR 不包含外部副作用，因此 deopt 只需恢复局部变量和 PC；属性写、调用等在 Tier 3
引入时才扩大映射范围。

## 8. 属性访问与调用的后续策略

v4 最大差距来自属性访问和调用，但它们不应成为第一个 Native JIT 目标，因为机器码直接读取
Go 对象布局会绑定未公开结构、GC 和写屏障。分两步处理：

### 8.1 Tier 1 单态特化

- 反馈记录 `(pc, shapeID, slot)` 与 `(pc, callee template)`；
- Go 侧入口 guard shape/callee，命中后执行专用 IR；
- 无调用、无对象写的循环可把稳定属性值提升为 trace 输入；
- 属性值变化、prototype/accessor/Proxy 一律回通用路径。

### 8.2 Tier 3 受控扩展

- 只通过稳定 runtime stub 访问对象，不在生成代码中硬编码 Go struct offset；
- 需要调用 runtime stub 的代码先退出到 Go，初期不从机器码回调 Go；
- 叶子数值函数可生成 direct entry；调用点以 callee identity guard 保证正确；
- 自递归数值函数在栈深/执行预算 guard 下编译，用于验证 `fib30`；
- 多态调用、闭包逃逸、constructor、getter/setter、Proxy 保持 Tier 0。

## 9. 里程碑与继续/停止门禁

| 里程碑 | 内容 | 关键交付 | 性能门禁 |
|--------|------|----------|----------|
| J0 | 基准与热点基础设施 | 热点/反馈表、`--jit-stats`、JIT 专项基准 | JIT 关闭零变化；auto 冷负载 <=2% 回退 |
| J1 | Tier 1 Quick JIT | IR、校验器、Go 执行器、数值 guard/deopt | 支持用例 >=1.5x；合计目标 <=30x |
| J2-S | Native 可行性 spike | amd64 emitter、trampoline、W^X、执行预算 | 数值 kernel 比 Tier 1 >=2x，稳定性门禁全过 |
| J2 | Native 数值 trace | 循环、分支、数值局部变量、代码缓存 | 支持的循环用例对 Node <=10x；合计 <=22x |
| J3 | 属性/调用特化 | shape/callee guard、有限内联、自递归 | prop/call/fib 支持子集 <=10-15x；合计 <=15x |
| J4 | 产品化 | 后台编译、预算调优、Linux amd64、默认 auto | mixed <=4x；冷启动回退 <=5% |

### J2-S 的硬停止条件

满足任一项即不进入 J2，保留 Tier 1：

- 需要常驻 RWX 内存或关闭系统安全机制；
- GC、抢占、栈增长或代码释放压力测试不稳定；
- 数值 kernel 相对 Tier 1 提升不足 2x；
- trampoline/平台维护成本无法被隔离在 `backend` 和 `execmem`；
- 机器码正确性无法通过随机差分和 sanitizer/崩溃隔离测试。

## 10. 基准与验收体系

### 10.1 保留 v4 基准

每次里程碑固定运行：

```powershell
go build -o bin/aluka.exe ./cmd/aluka
bin/aluka.exe tests/benchmark/perf-compare.js
bin/aluka.exe tests/benchmark/mixed.js
go test ./... -count=1
```

`perf-compare.js` 继续 5 次取中位数，并同时记录 JIT off/auto 两组结果。性能门禁使用同一二进制、
同一机器、固定电源策略；报告中同时给绝对时间、相对 v4 和相对 Node 的差距。

### 10.2 新增 JIT 专项基准

- `jit-numeric-loop`：纯数值 loop、分支、`NaN/-0/Infinity`；
- `jit-hot-leaf-call`：稳定叶子函数和多调用点；
- `jit-self-recursion`：可控递归深度与退出预算；
- `jit-monomorphic-prop`：稳定 shape 读、写后失效、prototype/accessor 回退；
- `jit-polymorphic-bailout`：类型/shape/callee 周期变化，验证不会反复重编译；
- `jit-cold-start`：大量只执行一次的函数，验证采样和内存开销；
- `jit-code-cache`：超过预算后的淘汰、重入和并发 VM 生命周期。

### 10.3 正确性门禁

- IR 单元测试：每条指令、CFG、Phi、校验失败和 deopt map；
- 随机差分：同一受限程序分别以 Tier 0/Tier 1/Tier 2 执行，比较值和异常；
- 数字语义：`NaN`、`-0`、Infinity、除零、位运算截断、BigInt 混合错误；
- 失效语义：类型变化、shape 变化、方法替换、闭包捕获变化；
- 全量 `go test ./...`、Node 22 diff/conformance、字节码 round-trip；
- 压力测试：多 VM、模块重入、代码缓存反复淘汰、长循环中断、`--max-memory`；
- Native 后端用独立进程跑崩溃用例，避免测试进程被非法机器码直接终止后丢失诊断。

## 11. 可观测性与开关

建议新增：

| 开关 | 用途 |
|------|------|
| `--jit=off|quick|auto` | 关闭、只用 Tier 1、允许 Native；开发期默认 `off` |
| `--jit-threshold=N` | 调整热点阈值，仅用于实验 |
| `--jit-backedge-threshold=N` | 调整循环回边编译阈值 |
| `--jit-trace-budget=N` | 限制单次 Quick/Native 连续执行的循环回边数 |
| `--jit-code-cache=SIZE` | 设置每个 VM 的 Native RX 代码缓存上限，支持字节/KB/MB/GB |
| `--jit-stats` | 输出候选、编译、guard、deopt、失效、代码内存统计 |
| `--jit-dump=ir|asm` | 调试构建导出 IR/反汇编，不在普通运行输出源码/常量 |
| `ALUKA_JIT_VERIFY=1` | 双执行 Native/Tier 1 并比较结果；不等同于 Tier 0 全语义差分，仅测试环境 |

统计至少包括：各 tier 执行次数、编译耗时、代码字节数、候选拒绝原因、guard 命中率、各
exitID 的 deopt 次数、代码缓存命中/淘汰次数。`--jit-stats` 还分别输出
`quickGuardDisabled`、`traceGuardDisabled`、`nativeGuardDisabled`、
`nativeTraceGuardDisabled` 和 `calleeGuardDisabled`，用于区分函数/trace、Quick/Native 与
callee PIC 的熔断来源。默认关闭时不得在每条指令上增加原子操作。

## 12. 风险与控制

| 风险 | 等级 | 控制措施 |
|------|------|----------|
| JS 语义误优化 | 高 | 保守候选、显式 guard、随机差分、精确 deopt |
| Go ABI/GC 不支持动态代码栈帧 | 高 | 无 Go 指针 JITFrame、无 native->Go 调用、预算退出、J2-S 硬门禁 |
| 可执行内存安全 | 高 | W^X、页级生命周期、无磁盘机器码缓存、输入 IR 校验 |
| 编译暂停影响交互 | 中高 | 首期小 IR 同步限时；产品化后后台编译；全局预算 |
| 代码膨胀和内存泄漏 | 中 | 每 VM 上限、LRU、模块/VM 释放测试、统计可见 |
| 多平台维护成本 | 中 | backend/execmem 隔离；Tier 1 为所有平台兜底 |
| 基准过拟合 | 中 | mixed、真实 pi/Express 负载和冷启动共同验收 |
| 与字节码缓存不兼容 | 低 | 仅持久化可移植 IR；格式变更递增 `FormatVersion` |

## 13. 实施顺序与任务拆分

```text
J0-1 热点计数与 VM-local 状态
 -> J0-2 类型/shape/callee 反馈
 -> J0-3 统计、基准和冷启动门禁
 -> J1-1 IR + verifier + bytecode lowering
 -> J1-2 Tier 1 数值 executor
 -> J1-3 guard/deopt + 随机差分
 -> J2-S1 amd64 emitter + 独立 kernel 测试
 -> J2-S2 trampoline + W^X + GC/抢占压力测试
 -> [继续决策]
 -> J2 数值 trace 接入 VM
 -> J3 shape/call guard 与有限内联
 -> J4 后台编译、缓存和默认 auto
```

每个任务独立提交。J0/J1 不依赖 Native JIT，可先稳定落地；J2-S 必须是可整体移除的实验层，
不得让解释器或 IR 因平台代码产生反向依赖。

## 14. 与现有优化计划的关系

- `hot-function-compile-plan.md` 的 S-2 类型特化和 S-3 原生化扩展归入本计划 Tier 1；
- `inline-tiered-bytecode-plan.md` 的 T-2 superinstruction 可继续做，但定位为 Tier 0 保底优化，
  不阻塞 J0/J1；
- 迭代化调用 S-1 和 32 字节 `stackSlot` 已有负收益结论，不重新启动；
- `NativeCallbackDesc` 是 IR lowering 的可复用原型，后续应合并到统一 IR，避免多个专用解释器；
- 属性 IC/Shape 是 Tier 3 guard 的反馈来源，但机器码首期不直接依赖 Go 对象内存布局。

## 15. 第一阶段完成定义

“开始往 JIT 方向”在第一阶段不是直接生成任意 JS 机器码，而是完成以下可验收基础：

1. JIT off/quick/auto 开关和 VM-local 热点状态；
2. 函数入口、循环回边、类型/shape/callee 反馈；
3. 可校验的最小类型化 IR 和字节码到 IR 的精确映射；
4. Tier 1 数值执行器及 guard/deopt；
5. JIT 专项基准、随机差分、统计和冷启动门禁；
6. 一份 J2-S 原型报告，明确 Native JIT 在 Go 1.25、Windows/Linux amd64 下继续或停止。

只有这六项完成，JIT 才从“特定模式优化”升级为可持续扩展的运行时能力。

## 16. 实施快照（2026-08-11）

当前代码已完成第二轮可运行原型；Windows amd64 上 J3/J4 的综合性能门禁已达到，默认仍为
`--jit=off`，因为 Linux 实机和长期产品化门禁尚未完成。

| 里程碑 | 当前状态 | 已落地内容 | 仍缺内容 |
|--------|----------|------------|----------|
| J0 | 完成 | VM-local 调用/回边热点、拒绝缓存、generation、结构化候选拒绝原因、CLI 统计；冷函数只保留轻量计数，达到阈值后才提升为完整 state | 持续校准真实负载阈值 |
| J1 | 基本完成 | 类型化 IR、CFG/栈校验、Number 算术/幂/比较/跳转、逻辑非、数值一元加、`&&/||/??` 短路、`!=/!==` 与 32 位位运算 lowering、Go executor、guard 回退、自递归、可恢复 trace 预算；Quick trace 支持多出口 `exitID/resumePC`、最多 8 槽的已建模操作数栈恢复，并只提交运行期实际写入的局部变量；String/BigInt 可作为 opaque Quick 值参与 truthiness、nullish、Return、严格相等和栈恢复；固定种子 Tier 0/Tier 1/Tier 2 集成差分和 try/catch 返回验证；R1-5 副作用 prepare/validate/commit 两阶段提交协议（属性写/数组 append/upvalue 写/调用 guard 失败/异常/OOM/取消/中断附近无重复、无遗漏、无部分提交，verifier 显式拒绝非法副作用状态） | 更广语法生成式差分、String/BigInt 算术、关系比较或宽松相等 |
| J2-S | amd64 原型完成 | 固定 trampoline、无 Go 指针 Frame、RW -> RX W^X、释放与 GC 压力测试；Windows 线性 kernel 约 `2.3ns/op`；Linux `mmap/mprotect` 后端已接入；非法指令由子进程崩溃测试隔离；新增跨平台 RX 区域生命周期计数与归零门禁 | Linux 运行时 CI、更多抢占/竞态门禁 |
| J2 | 数值子集完成 | Number 参数/常量/局部变量、四则运算、`DUP/SWAP/NEG`、比较分支和数值循环的 amd64 native；Native trace 支持多个 `exitID`、预算恢复、最多 8 个 Number 操作数栈 spill 和 dirty-local 精确写回；函数与 trace 共用每 VM LRU RX 代码缓存；可输出真实 x86 Intel 反汇编 | 更广语法和控制流的生成式 Tier 0/Tier 1/Tier 2 差分 |
| J3 | 基本完成 | 两路 callee PIC 均可 Native、有限叶子 IR 内联、guarded direct Quick call、两路 own Number property PIC；Quick/Native trace 支持已有 own data Number 属性写，属性值在语义出口或预算 yield 后由 Go 安全写回；新增严格 `Array.prototype.push` 数值范围批量 trace 与 numeric-upvalue closure trace；第三 shape/target/类型变化稳定关闭 Native 并保留 Quick，跨 local 对象别名回退 | 更多调用约定、数组/闭包/方法覆盖 |
| J4 | 部分完成 | 冷函数延迟分配、结构化诊断、`--jit-dump`、冷启动基准；大于等于 128 条 IR 的 Native 后台编译；Quick/Native 函数、自递归和 trace 共用预算 safepoint，支持 OOM 与嵌入方取消；短命 VM 和待安装后台代码的 RX 释放压力门禁；Windows mixed `2.2x Node`、11 项合计 `12.0x Node` | Linux 实机运行、默认 auto、长时间 RX/GC/抢占 soak 和跨平台综合门禁 |

已接入的开发期开关：

```text
--jit=off|quick|auto
--jit-threshold=<n>
--jit-backedge-threshold=<n>
--jit-trace-budget=<n>
--jit-code-cache=<size>
--jit-stats
--jit-dump=ir|asm
ALUKA_JIT_VERIFY=1
```

2026-08-11 在同一 Windows amd64 二进制上顺序执行 5 次并取每项中位数；Node 版本为
22.23.1，`mixed.js` 也使用 5 次进程墙钟中位数：

| 用例 | Node | JIT off | Quick JIT | Auto | Auto / Node |
|------|------|---------|-----------|-------------|-------------|
| `fib30` | 5.38ms | 335.24ms | 188.12ms | 188.91ms | 35.1x |
| `propAccess-3M` | 2.56ms | 572.96ms | 204.54ms | 7.76ms | 3.0x |
| `propSet-3M` | 2.38ms | 372.32ms | 121.29ms | 7.16ms | 3.0x |
| `callOverhead-1M` | 1.33ms | 156.35ms | 49.11ms | 4.01ms | 3.0x |
| `closureCall-1M` | 6.20ms | 190.64ms | 2.61ms | 2.15ms | 0.3x |
| `methodCall-1M` | 1.46ms | 175.25ms | 51.05ms | 3.64ms | 2.5x |
| 11 项合计 | 58.37ms | 2637.80ms | 1107.91ms | 698.62ms | 12.0x |
| `mixed.js` 进程墙钟 | 89.31ms | 471.09ms | 244.13ms | 199.79ms | 2.2x |

Auto 在 11 项合计上相对 Tier 0 约 `3.78x`，mixed 增益约 `2.36x`。Native trace 把属性读从
Tier 0 的 572.96ms 降到 7.76ms，把属性写从 372.32ms 降到 7.16ms；guarded noop/method
call 把两项分别降到 4.01ms/3.64ms，数组范围 trace 把 push 降到约 63ms，numeric-upvalue
closure trace 把闭包调用从 190.64ms 降到约 2.2ms。11 项合计已达到 J3 的 `<=15x` 门禁，
但本轮 callback 直接路径后 mixed 已降至约 `2.2x`，首次达到 J4 的 `<=4x` 门禁；这只是
Windows 单机快照，不能替代 Linux 实机、长期 RX/GC/抢占和冷启动回归，因此仍不支持提前把
`auto` 改为默认。

J2 数值循环专项（`sum(3_000_000)`，单次结果，回边阈值 10000）：

| Tier 0 | Quick JIT | Auto Native | Native 相对 Tier 0 |
|--------|-----------|-------------|--------------------|
| 458.21ms | 86.67ms | 5.74ms | 79.8x |

J3 专项（前两项为单次结果，属性写为本轮 5 次中位数）：

| 用例 | Tier 0 | Quick JIT | Auto Native | Native 相对 Tier 0 |
|------|--------|-----------|-------------|--------------------|
| 单态 callee 内联调用 1M 次 | 254.54ms | 207.90ms | 179.77ms | 1.42x |
| 外部对象三属性累加 3M 次 | 579.04ms | 160.54ms | 6.46ms | 89.6x |
| 已有 own Number 属性写 3M 次 | 369.80ms | 94.70ms | 7.28ms | 50.8x |

本轮 trace 统计为 `tracesCompiled=1`、`tracesExecuted=1`、`traceYields=45`、`guardFailures=0`。属性循环
trace 在退出时一次性写回数值局部变量；shape 或属性类型 guard 失败时不提交临时状态，并在
当前调用帧内停止重复尝试。长 trace 现在按 `TraceBudget` 在已完成的循环回边 yield，恢复到
loop header 后重新经过 VM safepoint。Native emitter 也会在回边递减预算，耗尽时把 resume
offset 写入无指针 Frame、返回 Go 执行统一 poll，再从原机器码偏移继续；300 万次循环在默认预算
下产生 45 次 Native yield。poll 会检查 OOM 并调用可选的 `jit.Config.Safepoint`；返回错误时以
`Interrupted` 退出并进入 VM 的现有 throw 路径，不会被计为 guard 或 JIT 错误。

Quick trace 已能为同一 trace 的多个外跳转分配独立 `exitID`，每个出口保存确定的 `resumePC` 和
局部变量恢复槽位；执行器额外跟踪 dirty locals，因此只提交当前执行路径实际发生的写入。Native
trace 使用返回状态 `3 + exitID` 传递语义出口，复用无指针 Frame 的 `Status` 字段保存 dirty-local
位图，预算 yield 的机器码偏移继续保存在 `Resume`，两类恢复信息不会混用。结构化统计按
`(function, backedgePC, exitID, resumePC)` 聚合 deopt 次数。手工字节码和真实 JS VM 集成测试均已
让同一 backedge 实际命中两个不同出口，并验证不同恢复 PC、局部变量提交和预算恢复。

Native 数值循环已通过固定种子差分测试（Tier 1 对 Native，覆盖 `NaN`、`+0/-0`、无穷和
四则运算），比较分支也验证了 NaN 按 JS 关系运算返回 false。`ALUKA_JIT_VERIFY=1` 会对成功的
Native 结果同步执行 Tier 1 并比较值；属性写 trace 会先快照受控属性和 locals，在隔离的 Quick
guard 副本上计算预期结果，恢复原状态后再运行 Native，最后比较出口、恢复 PC、locals 和属性值。
不一致时安装 Quick 结果并释放对应 RX 代码，避免重复或遗留 Native 副作用。该机制用于开发期
Native 后端验证，不能替代完整的 Tier 0/Tier 1/Tier 2 语义差分。原生代码缓存默认上限为 4MB，
`--jit-code-cache` 可调小以测试 LRU；淘汰时先释放 RX 页，再让对应函数继续使用 Tier 1。

新增的 VM 集成差分使用固定种子 `20260811`，同一控制流数值函数分别以 Tier 0、Quick 和 Native
执行 132 组输入，并逐位比较 Number（NaN 按语义等价、`+0/-0` 按位区分）。另有 260 组固定种子
trace 差分覆盖嵌套循环、分支、多出口、预算 yield、`NaN`、无穷和 `-0`；另有 132 组属性写
差分覆盖随机数值、`NaN`、无穷、`-0` 和预算 yield。Native trace 同步执行 Quick 验证，当前无
差异；人为扰乱 Native 属性槽映射的测试也验证了 mismatch 后可恢复 Quick 属性和 locals 结果。
`--jit-dump=ir`
输出 verifier 后的 IR，`--jit-dump=asm` 使用 `x86asm` 解码实际发布的机器码并输出 Intel 语法；
普通运行不保留机器码副本。Native 非法指令测试在独立子进程执行，父测试进程只检查失败退出。

J0 冷启动门禁新增 `BenchmarkJITColdStart`：每轮创建 VM、编译并各调用一次 256 个唯一函数。
Windows amd64 上 `-benchtime=50x -count=5` 的最新中位数为 `off 1.951ms/op`、`auto 1.987ms/op`，
本轮 `auto` 回退约 `1.9%`，低于 J0/J4 的 2%/5% 门禁；延迟 state 使 auto 额外分配保持约
14 次。该结果仍需在 Linux CI 和真实短命 CLI 负载上复核。

J3 调用特化以捕获闭包身份作为 guard：稳定叶子目标会展开到调用者 IR，不满足内联约束的目标
使用 guarded direct Quick call；每个模板保存基线 IR 和两个 callee 版本，两路内联版本都可生成
独立 Native 代码，第三目标回退。同一模板的不同闭包实例按实际捕获目标选择版本，不会无 guard
地共享 callee。两份 RX 代码按合计字节计入同一 LRU 单元，淘汰、验证失败、重配置和关闭时一起释放。
Native 属性循环不会读取 Go 对象布局，入口在 Go 侧验证 own data Number 的 shape/slot 并把
当前值写入无指针 Frame；写操作只修改 Frame 中的标量，语义出口和预算 yield 后再由 Go 两阶段
校验并写回已有属性。不同 local 指向同一对象的同名写会回退 Quick，accessor、Proxy、prototype、
新增属性和非 Number 值不会进入该路径。Quick、trace 和 Native 入口现支持两个 shape 的 property
PIC，第三个 shape 或类型变化回退。已编译闭包绕过重复热点/特化检查，
被拒绝的 frame 会缓存结果，避免每个后续回边重复查表；三指令 `return this.x` getter 由成本模型
留在 Tier 0。大 IR Native codegen 在 Program 副本上后台执行，VM 线程完成 LRU 准入和安装，
`ConfigureJIT`/`Close` 会排空未安装结果。

当前结论：J0、J1、J2-S、J2 数值子集和 J3 Windows 性能门禁已完成；属性读写、调用、数组
范围和 numeric-upvalue closure 均有受保护路径，11 项合计 `12.0x`、mixed `2.2x` 已达到
对应 Windows 快照门禁。更完整的语法生成式差分、Linux 实机运行、带异常的 deopt、长期
GC/抢占和默认 auto 产品化仍未验收，因此不能把 `auto` 改为默认模式。

当前开启 instruction metrics 时，VM 仍在 bridge 入口整体绕过 JIT，以保持逐指令计数精确。
`--max-memory` 不再禁用 JIT：Quick 函数在循环回边和自递归调用轮询，Quick trace 在提交完成迭代
后轮询，Native 函数和 trace 则在机器码预算退出后保留 Frame 并轮询；OOM 被消费后抛出可捕获的
JS `RangeError`。带属性写的中断保留已完成迭代，不重复或回滚已提交副作用。`--jit-stats` 新增
`safepointPolls` 和 `interruptions`，分层长循环、递归、属性写及 OOM 测试均已覆盖该协议。

v1.5 新增受保护的数组 push 范围 trace，仅识别编译器生成的固定形态
`for (let i = start; i < bound; i++) array.push(i)`：receiver 必须是具体
`*engine.ArrayValue`，`push` 必须仍是当前 `Array.prototype.push` identity，induction 和
bound 必须是有限非负整数，且 receiver、bound local 在 trace 内不可写。Quick/Auto 入口在 Go
侧完成这些 guard，调用 `ArrayValue.AppendNumberRange` 一次扩容并只同步一次 length；每个
`TraceBudget` chunk 后复用统一 safepoint。机器码不接收数组或 Go 指针，因此该特化不改变
Native ABI；方法替换、Proxy、非数组和非安全数字会立即回退 Tier 0。`--jit-stats` 新增
`arrayPushSites` 与 `arrayPushYields`。

Windows amd64、Node 22.23.1、同一已编译 CLI、5 次进程中位数的数组专项结果：

| 用例 | Node | JIT off | Quick JIT | Auto（范围 trace） |
|------|------|---------|-----------|--------------------|
| `arrayPush-1M` | 11.20ms | 348.18ms | 65.55ms | 62.98ms |

相对上一版 Auto 的约 353.75ms，范围 trace 约降低 82%；但该结果仍约为 Node 的 `5.6x`，
不能单独改变综合性能门禁或把 `auto` 改为默认模式。当前完整 11 项快照仍以本节上方表格为
主口径，数组专项作为增量结果单列，避免把不同基准脚本和机器状态混合比较。

v1.6 新增 numeric-upvalue closure trace，只识别调用者的固定形态
`for (; i < bound; i++) sum += fn()`，且 `fn` 必须严格是无参数
`() => ++capturedNumber`。入口 guard closure identity、唯一 upvalue identity、Number 类型和循环
局部变量；open upvalue 若与 caller 的 callee/index/bound/sum local 指向同一槽位则拒绝，避免
`sum += (() => ++sum)()` 一类读写别名改变求值顺序。每个 chunk 在 Go 标量中按原 JS 顺序逐次执行
`current++` 与 `sum += current`，随后同时写回共享 upvalue、sum 和 induction local；safepoint
中断只保留完整 chunk。callee 替换、别名和非 Number 均无副作用回退 Tier 0。该路径同样不把
Go 指针放入 Native Frame，Auto 模式复用受保护的 Go trace。`--jit-stats` 新增
`closureUpvalueSites` 与 `closureUpvalueYields`。

Windows amd64、同一已编译 CLI、5 次中位数：`closureCall-1M` 的 Quick/Auto 分别为
`2.61ms/2.15ms`，相对 Tier 0 `190.64ms` 约提升 `88.7x`。11 项合计由 v1.5 的 Auto
`957.06ms` 进一步降到 `698.62ms`，相对 Node `58.37ms` 为 `12.0x`，首次通过 J3 的
`<=15x` 门禁。该专用形态比 Node 的通用闭包调用循环更快不代表通用调用性能已经超越 Node；
它只说明 guard 后的批量标量执行有效。

v1.7 对 O-6 `NativeCallback` 做了两层低风险收紧：callback 微解释器改用固定栈数组，
`callCb2/3/4` 为常见参数个数提供栈上参数载体；对编译器已证明为纯箭头且输入全为 Number
的 `x => x * K`、`x => x % K === C`、`(acc, x) => acc + x`，数组 map/filter/reduce
在一次 Number guard 后直接执行，结果数组仍按原 `ArrayValue` 创建和原型规则返回。任一元素
非 Number、callback 不是箭头/描述缺失、或模式不匹配均回退原 NativeCallback/完整调用，避免
改变 JS coercion、this 或副作用语义。新增测试覆盖字符串 fallback、数值结果和 reduce 字符串
拼接。

分段基准（Windows amd64，5 次中位数）显示 `map-300x10K` 从约 `113ms` 降至约 `42ms`，
`filter-reduce-50x10K` 从约 `69ms` 降至约 `38ms`；完整 `mixed.js` Auto 墙钟为
`199.79ms`，Node 为 `89.31ms`，约 `2.2x`，首次通过 J4 的 `<=4x` mixed 门禁。该结果
仍只代表 Windows 单机；Linux 实机、长期 GC/抢占、后台编译压力和默认 auto 仍未验收。

v1.8 补齐 `NativeCallback` 数值直接路径的 `NaN`、`Infinity` 和 `-0` 边界测试。测试同时发现
Tier 0 VM、AST 解释器和 callback 微解释器的手写零除分支只按被除数选择无穷符号，导致
`1 / -0` 错误返回 `Infinity`；负零字面量本身仍由 `PUSH_INT 0; NEG` 正确保留。三条路径现统一
使用 IEEE-754 浮点除法，与 Quick/Native JIT 的既有实现一致，并新增 `1 / -0`、`-1 / -0`、
`0 / -0` 以及 callback 除法回归。快速 map/filter/reduce 的边界断言改用 `Object.is` 直接检查
结果中的 `-0`，避免测试依赖另一条除法路径而掩盖根因。本修复不改变上述性能快照。

v1.9 扩大统一数值 IR 的比较覆盖：`!=` 与 `!==` 在两侧均为 Number 时 lowering 为
`NeF64`，Quick executor、Quick trace 和 amd64 Native emitter 共用同一 NaN 语义（NaN 与任何
值均不相等）。Native emitter 对 `UCOMISD` 的 `SETNE` 与 `SETP` 结果做合并，避免把 NaN
误判为相等；非 Number、BigInt 或其他动态语义仍由 guard 回退 Tier 0。新增固定输入跨 Tier
差分覆盖相等、不同、NaN、`-0/+0`，并验证编译 trace/Native 返回 VM 后仍能进入 `try/catch`；
另有 data property 热身后替换为 getter 的测试，确认回边 guard 失效后解释器仍能继续执行并
在下一次 getter 抛异常时正确进入 `catch`。
本轮没有重新采集性能数字，语法扩展不改变既有基准形态。

v2.0 将 Linux 平台门禁写入 `.github/workflows/ci.yml` 的独立 `jit-linux` job：Ubuntu amd64
运行 Native JIT/W^X 测试、race 与 safepoint/GC 相关用例，重复执行 JIT 测试五次，并运行
`--jit=auto` CLI smoke。当前工作站没有 WSL、Docker 或 Linux runner，故这些步骤已完成 YAML
解析和 Windows 等价命令验证，但尚无远端 CI 成功记录；在该记录出现前，J4 仍保持“部分完成”，
默认模式仍为 `--jit=off`。

v2.1 增加 Windows/amd64 可执行的 Native RX 缓存压力测试：32 个独立函数在 1KB 代码预算下
反复编译、淘汰并在每轮触发 Go GC，验证结果、缓存上限、淘汰释放和错误计数。该测试通过后仍
不能替代 Linux 实机的 W^X 与长期抢占验证，但把代码缓存生命周期从短样例扩展到可重复的压力门禁。

v2.2 补齐位运算的语义与 Tier 1 覆盖。Tier 0 VM 和 AST 解释器统一使用 JavaScript 的
`ToUint32`/`ToInt32` 规则，修复 64 位宿主机上 `2147483648 | 0`、`-1 >>> 0`、小数截断、
`NaN` 和 `-0` 等边界结果；一元 `~` 也复用同一转换。Quick IR、Quick executor 和 Quick
trace 新增 `&`、`|`、`^`、`<<`、`>>`、`>>>`，数字 guard 失败时回到 Tier 0。amd64 Native
emitter 暂不接收这些整数 ABI 指令，Auto 在 Native 编译拒绝后回退 Quick/trace，避免把浮点
寄存器协议误用于 32 位整数语义。新增 Tier 0/Quick/Auto 固定输入差分测试，确认回退后的
结果一致；本轮没有改变 Native 性能快照或默认 `--jit=off` 门禁。

v2.3 增加 Native 可执行内存的全局生命周期计数（发布区域数与字节数），并在 Linux/Windows
amd64 上加入 32 个 RX 区域的发布、重复关闭和 Go GC 后归零测试。计数只用于诊断和生命周期
门禁，不进入 Native ABI，也不替代操作系统级 W^X 检查；`Code.Close` 在释放失败时保留句柄，
允许调用方重试，避免把失败释放误记为已回收。该门禁补强了短生命周期 VM 和缓存淘汰的证据，
但仍不能替代 Linux CI 的长期抢占和真实 runner 记录。

同一轮将一元 `~` 接入 Quick IR 和 Quick trace，使用与 Tier 0 相同的 `ToInt32` 转换；Native
仍会因当前整数 ABI 限制而拒绝该 opcode，Auto 继续回退到 Quick/解释器。

另外，函数和 trace 的 guard 现在区分“Native 失败”和“Quick 失败”：Native 连续两次因
shape/类型输入失败后只关闭该 Native 版本，保留 Quick；Quick 连续两次最终 guard 失败才将
该 VM-local 状态熔断到 Tier 0。callee PIC 的第三个目标也采用相同策略，避免在多态调用点
反复尝试同一 Native/Quick guard。新增第三 shape 与第三 target 的稳定回退测试，验证结果、
Native RX 释放和 Quick 状态均保持可用。

v2.4 把 RX 生命周期门禁从裸 `native.Code` 扩展到真实 VM：第一组连续创建并关闭 32 个短命
Auto VM，每个 VM 都同步安装并执行 Native 代码，关闭和周期性 GC 后全局 RX 区域数/字节数必须
恢复到测试前基线；第二组让 8 个大 IR 的后台编译完成并发布 RX、但故意不轮询安装，随后直接
`VM.Close`，验证 pending channel 排空、后台代码释放和计数归零。两组测试本机重复 20 次、race
重复 5 次通过，并由 Linux JIT job 的重复正则覆盖。该结果补齐短生命周期和 pending compile
关闭协议，长时间 Linux soak 与真实 runner 成功记录仍是独立门禁。

v2.5 扩大 Tier 1 语法：`**`、逻辑非 `!` 和数值一元 `+` 已接入类型化 IR、verifier、Quick
函数与 Quick trace。`+` 仅在输入已 guard 为 Number 时执行 identity，并在 amd64 Native 中
生成零指令语义节点，因此逐位保留 `-0`；字符串/对象继续回退 Tier 0。`Pow` 和 `Not` 暂不进入
Native ABI，Auto 在编译拒绝后使用 Quick。新增 Tier 0/Quick/Auto 差分覆盖幂、`NaN/-0`、布尔
结果和循环 trace，并验证字符串一元加的 guard fallback。

该差分同时发现公共 `engine.Number.Bool` 把 `NaN` 当作 truthy，导致 Tier 0 的 `!NaN`、条件
表达式和逻辑或违反 JavaScript ToBoolean。现已统一为 `0`、`-0`、`NaN` 均 falsy，并新增 VM/AST
回归；Quick 原有 NaN truthiness 不再与 Tier 0 分歧。

v2.6 为 guard 分层熔断补齐结构化可观测性。函数和 trace 各自维护 Quick 与 Native 连续失败
计数：Native 成功会同时清除两层计数，Quick 成功只清除 Quick 计数，避免 Native guard 失败后
由 Quick 回退成功错误地重置 Native 熔断进度。达到阈值时只递增一次对应的 disabled 计数；
Native 熔断仅释放 Native RX 代码并保留 Quick，Quick 熔断才把该 VM-local 候选降回 Tier 0。

新增回归分别覆盖 Quick 函数字符串一元加、Quick trace 类型变化、Native 函数第三 shape、
Native trace 第三 shape 和 callee PIC 第三 target。测试同时检查 guard 失败次数、对应 disabled
计数、Native 代码字节归零及 Quick 后备路径继续执行。CLI smoke 也验证 `--jit-stats` 会输出五类
熔断计数；该统计只在显式启用 stats 时采集，不改变默认 `--jit=off`。

v2.7 将 Number 子集的逻辑与 `&&`、逻辑或 `||` 接入函数和 trace lowering。IR 新增 keep-branch：
短路跳转路径保留左值作为表达式结果，求右值路径先弹出左值；verifier 为两条 successor 分别传播
栈深，并拒绝栈深不一致的合流。Quick executor 对 Number/Boolean 使用已有 ToBoolean guard；amd64
Native 使用有序且非零比较生成 truthiness，确保 `0`、`-0`、`NaN` 均为 falsy。非受支持类型仍在
任何副作用发生前 guard 回 Tier 0，`??` 暂不纳入该 Number-only 路径。

本轮新增固定种子语法生成器，不再只对固定函数模板随机化输入：40 个嵌套算术/一元/`&&/||`
表达式分别使用 6 组包含 `NaN`、正负无穷和 `-0` 的输入，对 Tier 0、Quick 和 Native 做 240 组
逐值差分，并要求全部调用真实命中目标 tier。另有循环集成差分强制命中 Quick trace、Native trace、
预算 yield 和 Native/Quick 双执行校验；IR 与机器码单测独立覆盖短路保留值及错误 CFG 合流。

v2.8 将 nullish coalescing `??` 接入函数与 trace lowering，并新增 `jump_nullish_keep` IR。Quick
值域不再用同一个零值同时表示“真实 undefined”和“未建模值”，而是区分 invalid、undefined、
null、Boolean、Number 与 Object；因此缺失参数/undefined/null 会精确选择 RHS，`false`、`0/-0`、
`NaN` 和对象会保留左值，字符串等未建模类型仍触发 guard 回 Tier 0。这个拆分也让 `&&/||`
对 Boolean、nullish 和 Object 的 Quick truthiness 与 Tier 0 一致。

amd64 Native 仍是 Number specialization：入口参数 guard 为 Number 后，左值必定非 nullish，机器码
直接保留左值并跳过 RHS；null/undefined 输入在执行任何 JIT 副作用前退出到 Quick。新增函数和
循环 trace 跨层差分验证数值 Native 命中、nullish Quick 回退、两次 Native guard 后的分层熔断、
RX 字节归零与后续 Quick 执行；固定种子表达式生成器也加入嵌套 `??`。

v2.9 补齐 trace 语义退出的操作数栈 deopt map。verifier 为每个 `exitID` 推断并锁定退出栈深，
不同路径以不同栈深合流时拒绝编译；Quick exit 在提交 dirty locals/属性后携带按栈底到栈顶排列的
`StackValues`。VM 只允许在回边栈为空时进入 trace，并在设置 `resumePC` 前验证栈深、通过现有
upvalue-safe 扩容路径恢复操作数，因此解释器恢复后不会丢失或重复消费短路表达式的左值。

Native 不把 `engine.Value` 或其他 Go 指针放入 ABI。amd64 emitter 在 `trace_exit` 前把最多 8 个
Number 栈槽写入 pointer-free `Frame.Locals` 的保留尾部，Go 侧按 exit map 重建 Number；代码生成
同时检查 locals、属性临时槽与 spill 总和不超过 32。`ALUKA_JIT_VERIFY=1` 的双执行比较现已包含
exit 栈值，校验失败回 Quick 时也恢复 Quick 的栈状态。新增外部 keep-branch 手工字节码覆盖
`0/-0/NaN` 的 Quick/Native 逐位一致，并通过 VM bridge 模拟恢复后的 `STORE_LOCAL`。本版本的
String、BigInt 及带任意用户副作用的栈中间值仍保守回 Tier 0；其中前两类的受限 Quick 支持见
v2.10，Native ABI 没有随之扩大。

v2.10 将 String 和 BigInt 加入 Quick 的受限 opaque 值域。两类值以原 `engine.Value` 引用保存，
只允许执行 ToBoolean、nullish 判断、短路分支、Return 和 trace exit 栈恢复；空/非空字符串与
零/非零 BigInt 的 truthiness 由现有 Value 语义提供。数值算术、比较、属性或调用仍要求原有
类型 guard，不会把 String/BigInt 隐式转换为 Number，也不会扩大 Native Frame。Auto 遇到这两类
入口时由 Native guard 退出并稳定使用 Quick。

新增函数级跨层差分覆盖 `&&`、`||`、`??` 和 `!` 的 16 个 String/BigInt 结果；另将空字符串和
零 BigInt 放入外部 keep-branch 的 trace exit 栈，恢复后的 Tier 0 先消费原值，再调用只执行一次
并抛错的宿主函数。Quick 和 Auto fallback 均验证原值未丢失、副作用未重复、异常继续传播；Number
对照组仍要求真实命中 Native 并通过双执行校验。String/BigInt 算术、比较、Native spill，以及
getter/Proxy/调用等任意副作用状态的通用 deopt map 仍是后续工作。

v2.11 将严格相等从 Number-only 的 `Eq/Ne` 中拆出独立 `StrictEq/StrictNe` IR。Quick 函数和
trace 对 undefined、null、Boolean、Number、String、BigInt 及对象身份执行无 coercion 比较：
不同类型直接不等，`NaN` 不等于自身，`+0/-0` 相等，BigInt 按整数值比较，对象只按 identity
比较。未建模的 Symbol 等值仍 guard 回 Tier 0；宽松 `==/!=` 继续只在两侧均为 Number 时进入
JIT，避免在 JIT 包复制一套不完整的 ToPrimitive/ToNumber 语义。

amd64 emitter 在 Number 入口 guard 下让 `StrictEq/StrictNe` 复用现有有序/无序浮点比较，新增
`NaN` 和普通相等/不等分支测试。VM trace 差分先以 Number 输入实际命中 Native，再依次使用
String、BigInt、Boolean、nullish 和相同/不同对象；Native 连续 guard 失败后只关闭 Native trace，
其余调用稳定使用 Quick，结果与 Tier 0 一致且 Native 双执行无差异。String/BigInt 算术、关系
比较、宽松相等和 Native spill 仍不在本轮范围。

v2.12 落地 R0-2 统一三 tier 基准入口。新增 `bench/cmd/jitbench` Go 命令：CLI 缺失或 `-rebuild`
时只构建一次，按轮次轮换 off/quick/auto 的执行顺序（避免温度和频率偏置），运行
`tests/benchmark/perf-compare.js`（逐 case `name: ms` 行）与 `tests/benchmark/mixed.js`
（无行输出时按进程墙钟计样），自动输出每个 (case, tier) 的原始样本、中位数、min/max、均值与
相对 MAD 离散度，并可经 `-out` 写入 `bench/results/jit-<date>-<platform>.json` 结果归档
（参数、commit/平台/Go/aluka 版本、统计、失败原因；该落盘只是测量记录，R0-4 的 schema 校验
与归档契约仍是独立任务）。Windows amd64 实机 `-reps 3` 验证：轮换顺序
off/quick/auto → quick/auto/off → auto/off/quick；propAccess-3M 中位数 off 659ms / quick 243ms /
auto 8ms，mixed 墙钟 off 553ms / quick 284ms / auto 278ms，与 v1.6 快照一致；冷启动
`JITColdStart` 50x5 中位数 off 2.353ms / auto 2.447ms（auto 回退约 3.9%，低于 5% 门禁）。
新增 14 个单元测试覆盖输出解析（含 JIT stats/IC stats/裸数字噪声过滤）、统计计算、轮换顺序、
聚合、墙钟模式与失败记录；`go test ./... -count=1`、`go vet`（含 `./bench`）、`git diff --check`
通过。`go test -race` 在本机因 Windows TSan 无法分配影子内存（error code 87，基线同样失败）
不可执行，race 门禁交由 R2 Linux CI 覆盖。该条目不改变任何引擎代码、默认 `--jit=off` 门禁或
性能快照。

v2.13 落地 R0-4 结果归档格式。`bench/cmd/jitbench` 的 `-out` 从测量落盘升级为带版本契约的归档：
新增 `schemaVersion: "1"`、`summary` 摘要（每 (case, tier) 的中位数与 `vsOff` 相对 off 加速比，
off 恒为 1）、`validateReport` 校验器（强制参数/版本/统计/失败原因字段；每个 (case, tier) 要么有
等于 reps 的原始样本、要么有对应的失败记录解释缺失；摘要必须覆盖全部 case；失败记录必须完整且
round 在界内）。写盘前必过校验，`-out` 指向目录时按 `jit-<YYYYMMDD>-<goos>-<goarch>.json`
命名。新增 8 个单元测试（摘要、15 条校验规则、JSON 写读 round-trip、命名约定），jitbench 包共
22 个测试通过；实机生成 `bench/results/jit-20260811-windows-amd64.json`（schemaVersion 1、
summary 含 vsOff、config/版本/失败字段齐全）并 round-trip 校验通过。`go test ./... -count=1`、
`go vet`（含 `./bench`）、`gofmt`、`git diff --check` 均通过；race 门禁同 v2.12，仍受 Windows
TSan 环境限制，交由 R2 Linux CI。归档按日按平台命名，同日重跑覆盖同名文件；Node 对照与 5 次
中位数正式报告仍是 R0-5/R5 的独立交付。该条目不改变任何引擎代码、默认 `--jit=off` 门禁或性能快照。

v2.14 落地 R0-3 正确性覆盖矩阵。新增 `docs/jit-coverage-matrix.md`：按 45 个 IR opcode（Quick 函数 /
Quick trace / Native 三列）、7 类值（Number/Boolean/nullish/Object/String/BigInt/Symbol）、函数/trace/
Native 生命周期、guard/deopt、平台与可执行内存建立能力→权威测试映射，每个能力行标注测试引用；
新增 `TestJITSymbolValuesGuardBackToTier0` 补齐审计发现的唯一缺口（v2.11 宣称 Symbol 值 guard 回
Tier 0 但无测试）：函数级与 trace 级同时覆盖 `===` 与 truthiness 的 Symbol 输入，断言三模式结果一致
（identity 语义）、guard 失败被记录、Auto 不产生 verify 失败。矩阵全部测试引用逐一核对存在且通过，
审计后不存在“代码已宣称支持但无测试”的条目。该条目不改变任何引擎执行语义、默认 `--jit=off` 门禁
或性能快照；`go test ./... -count=1`、`go vet`、`gofmt`、`git diff --check` 均通过。

v2.15 落地 R0-1 环境固化与 R0-5 冻结快照。新增 `tests/benchmark/jit-special.js`（J2/J3 专项形态：
数值循环、单态 callee 内联、外部对象属性累加、own 属性写，输出 `name: ms` 供 jitbench 采集）与
`docs/performance-report-r0-5.md`（同时承载 R0-1 环境记录）。同一二进制（SHA-256
`43c4ba83…`）完成 4 轮 ×5 次采集：11 项 Auto 合计 `949.71ms`（13.8x Node）、off `3140.23ms`；
mixed 墙钟 Auto `295.29ms`（2.6x Node）；专项 numeric loop `7.56ms` / callee inline `198.48ms` /
external props `8.97ms` / prop write `7.85ms`（均 5 次中位数），与 J2/J3 快照同量级；冷启动 50x5
off `2.858ms` / auto `3.946ms`。**诚实结论：R0 §5.3 稳定性验收未通过**——连续轮对中位数最大偏差
A-B 19.1% / B-C 26.8% / C-D 64.2%，根因为本机仅有“平衡”电源方案（无高性能方案）+ 采集期间后台
负载（ChatGPT/ToDesk/webview2/ZCode）；冷启动 auto 相对 off +38.1% 同样判为环境离群（R0-2 安静
状态下为 +3.9%）。因此 R0-1/R0-5 交付物齐全，但 R0 里程碑验收挂起，默认 `--jit=off` 不变；复核
须在安静环境与固定电源策略下进行。该条目不改变任何引擎执行语义或覆盖矩阵。

v2.16 落地 R1-1/R1-2/R1-8 生成式差分框架 `internal/engine/interpreter/jitdiff`。固定种子生成 14 种
用例形态（表达式/分支/循环/严格相等/宽松相等/属性读/属性写/数组 push/闭包/调用/getter/回调抛错/
Proxy/deopt 前缀），覆盖 R1-2 值域（Number 边界 NaN/±Inf/-0/1e-320/1e308/2^53+、Boolean、
null/undefined、String、BigInt、Symbol、对象 identity）；Tier 0 为唯一 oracle，逐 (case, tier)
比较返回值/异常类型/异常消息/事件日志；未支持路径（String/BigInt 算术、非数字宽松相等、Proxy、
getter/setter、Symbol）明确标为 Tier 0 对照或预期 guard 回退。结构化值域用例逐值执行返回、短路、
严格比较和热身后 guard 变化；PR 集按 Kind 断言 8 类 Quick 与 5 类 Native 命中。**PR 集 1,000 例与
nightly 100,000 例（5 seed × 20,000，复核 131s）均零差分通过**；每日 CI job 自动运行 nightly，
失败时保存 seed/源码/
逐 tier IR+JIT stats/复现命令到 `bench/results/jitdiff/`，单命令重放
`go test ./internal/engine/interpreter/jitdiff -run 'TestReplayFailure' -artifact <dir>`。
**框架发现并修复 2 个 Tier 0 引擎 bug**：① BigInt/NaN 关系比较对 NaN panic 且反向误判
（顺带发现数字路径 `NaN > 3` 竟为 true 的既有语义错误），统一 `compareBool` NaN 哨兵处理并补
回归；② parser `skipAngleBraces` 把比较 `<`（如 `1 / v < 0`、`5 < ((x) > (y))`）误当 TS 泛型
参数吞吃任意源码，修复括号深度平衡，并保留 CallExpr/NewExpr 泛型调用及嵌套函数类型。Artifact 保留
首次 mismatch 且按记录的 Verify 配置重放；verifier 收紧拒绝缺失/越界/负/歧义 deopt map，并补 trace
级 IR dump。`go test ./...`、`go vet`、JIT/jitdiff race、`gofmt`、`git diff --check` 均通过；Linux
实机连续 CI 与长期 soak 仍由 R2 验收。
该轮未改默认 `--jit=off`、未扩大 Native ABI；引擎修复只影响 Tier 0 正确性，不改变性能快照口径。

v2.17 落地 R1-3 异常差分，并推进 R1-4 deopt map 加固。`jitdiff` 新增 5 个 Kind（BigIntDivZero/
GetterSetterThrow/OOM/Cancel/Safepoint）与 `RunHook`（OOMBytes/TriggerOOM/CancelAfter/CancelErr），
生成器 Version 1→2；固定用例扩至 17 个。确定性异常（BigInt 除零、getter/setter 抛错、回调抛错）
在 guard 回退后同点抛出，三 tier 事件日志与副作用前缀精确一致；OOM/嵌入方取消/safepoint 中断
通过差分夹具显式启用 `InterpreterSafepoints`，在解释循环回边与 JIT budget yield 上复用同一回调
（默认嵌入行为不变）；取消保持独立 `Error`，异常进入同一 catch 路径；
延迟中断通过 `count == last + 1` 验证已提交迭代无重复/遗漏。差分框架发现并修复 3 个 Tier 0 引擎 bug：① BigInt
字面量后 `/` 被误当 regex（`regexAllowedAfter` 缺 TokenBigInt，`1n / 0n` 曾为语法错误）；② `++/--`
对 BigInt 抛 TypeError（新增 `OpInc/OpDec` 字节码，compileUpdate 与 VM 运行期保持类型，JIT
lowering 展开为 Number 序列保住既有 `i++` 循环支持，arrayPush/closure 匹配器适配新字节码）；
③ AST `evalUpdate` 对 BigInt 同样修复；审核另修复 JIT `--` 降低顺序曾计算为 `1-x`，新增三 tier
回归。R1-4 对现有 `DeoptExit` 恢复映射补充 verifier 拒绝规则（缺失/越界/负 ID/歧义/预置冲突/
栈深过深/预置越界，7 类测试）+ `TestDeoptExitMapIntegrity`；pending exception 尚未建模，R1-4 未完成。
PR 1,000 例与 nightly 100,000 例（5 seed，140.73s）均零差分。该轮未改默认 `--jit=off`、未扩大
Native ABI 与 W^X 生命周期；`OpInc/OpDec` 为新增字节码（追加在 iota 尾部，序列化兼容）。

v2.18 完成 R1-4 pending exception 正式状态。`DeoptExit` 增加 `PendingException engine.Value`
（nil = 无）：trace 编译 `OpThrow` 为 exception exit（在 throw 位置直接放置 `OpTraceExit`，不新增
IR opcode，避免被普通跳转 fixup 误判），Quick 执行器把栈顶原始 JS 值移入 `PendingException` 并
按 JS 异常展开语义丢弃其余操作数栈；VM 恢复经 `*jsThrow` 将原始值送入现有 `handleThrow`
try/catch/finally 状态机（catch 参数获得原始值，finally 重抛与外层 catch 不丢失不重复）；Native
编译在 `lowerNativeInputsForMode` 拒绝含 exception exit 的程序（机器码无法表示 Go 指针/engine.Value），
Auto 稳定回退 Quick；`SameDeoptExit` 比较 pending exception（Number 按位含 NaN、字符串按值、对象
identity）；verifier 拒绝截断/扩展 exception map 与异常值缺失；IR dump 标注 `(exception)`。测试：
jit 包 4 个（编译/执行/Native 拒绝/verifier 拒绝/SameDeoptExit 10 子用例）、interpreter 包 8 个
场景（数字/字符串/对象 identity 抛错、finally 重抛、嵌套 catch、guard 失败进 catch、Auto 回退、
deopt stats 记录 exception exit）、jitdiff 固定用例 -18 与 artifact 保存/重放。PR 1,000 例与
nightly 100,000 例（5 seed，154.7s）零差分。该轮未改默认 `--jit=off`、未扩大 Native ABI 与
W^X 生命周期；exception exit 仅由 trace 内 `OpThrow` 触发，`if (cond) throw` 的 throw 块在
backedge 后仍走普通 guard 回退路径（文档说明）。

v2.19 完成 R1-5 副作用两阶段提交协议，把 trace 内的延迟提交骨架形式化为
prepare/validate/commit 并补齐结构性原子性与 verifier 拒绝。审计确认的既有顺序保持不变：
属性写（`OpSetProp`）在写点 guard 后记录到延迟状态，只在语义 exit（含 exception exit）与预算
回边 yield 提交；数组 push 特化每 chunk 一次原子 `AppendNumberRange`；numeric-upvalue closure
特化在 chunk 末尾一次性写回 upvalue + sum + index local；guarded noop 调用在调用前做 callee
identity guard，用户调用本身从不进入 trace。本轮改动：① Quick trace `commitSideEffects`
validate-all → 快照原值 → store-all，中途失败回滚已 store 属性，部分提交结构性不可能；
② Native `commitNativeTraceFrame` 同一协议，store 失败从 Malformed 错误改为回滚 + (false, nil)，
走干净 Yielded/重放路径（避免 Quick 在部分写之上重放）；③ verify 路径
`restoreNativePropertyValues` 记录恢复前当前值，失败回滚已恢复项；④ verifier 拒绝
`OpSetProp`/`OpGuardNoopCall`/`OpGuardMethodGet` 出现在非 trace 程序，并拒绝 trace guard 索引
越界（`traceCallGuards`/`traceMethodGuards`）。测试：jit 包 3 个（verifier 5 类拒绝 + 合法对照、
跨 3 个 budget slice 提交恰一次、已提交 slice 后 guard 失败零写入）；interpreter 包 4 个
`TestDeopt*` 场景（提交先于异常、调用 guard 失败无部分写 + 调用抛错进 catch、push 中断
`A.length === A[A.length-1]+1`、upvalue 中断 `sum === N(N+1)/2`，均 off/quick/auto 一致且断言
trace 真实执行）；jitdiff 固定用例扩至 23 个（-19 调用 guard 失败 + 属性写前缀、-20 push + 取消、
-21 upvalue + 取消、-22 属性写 + throw + finally、-23 属性写 + OOM）与
`TestArtifactRoundTripSideEffect` 重放。PR 1,000 例与 nightly 100,000 例（5 seed）零差分。
该轮未改默认 `--jit=off`、未扩大 Native ABI 与 W^X 生命周期、不改变任何性能快照口径；
store-after-validate 分歧在单线程语义下不可达，回滚是协议的结构性保证而非可达路径。

R1-5 审核补强：原实现曾把逐项 validate/store 交错执行，第二项验证失败会留下第一项写入；现已
改为 Quick、Native commit 和 Native verify restore 三条路径统一 validate-all 后 store-all，并用
两属性后项失效回归锁定。verifier 进一步拒绝伪造 deopt map 后走函数返回，防止绕过提交点。
Native Verify 的 Quick 预执行不再调用嵌入 safepoint，避免同一切片双轮询。方法 guard profiling、
Quick 和 Native 统一使用不走 accessor/原型/Proxy 的普通对象 own data property 读取；不安全
receiver 明确回退 Tier 0，Proxy trap 计数在 off/quick/auto 精确一致。

v2.20 完成 R1-6 随机 guard 失效与稳定回退。jitdiff 新增 `KindGuardMutation`，8 类 mutation
（1st/2nd/3rd property shape、Number→String/BigInt/nullish/object、绑定 callee 身份替换、
trivial method target 替换、own method→accessor、own method→prototype method（delete 后原型链）、
数组 push 被替换/receiver 变非数组、closure upvalue 类型/身份变化）以
"warmup 调用 → 调用边界 mutation 语句 → post-mutation 调用"的调度内嵌在 case 源码，
seed/源码/mutation 调度随 artifact 保存、单命令重放完全一致。固定用例扩至 31 个
（-24..-31），`TestGuardMutationFixedCases` 断言每类 off/quick/auto 零差分且实际命中目标
guard（TracesCompiled/Compiled ≥ 1 且 GuardFailures ≥ 1，非仅 Tier 0）；accessor 用例用
getter 内 LOG 计数证明 getter 只在 Tier 0 每迭代恰好执行一次（JIT 内零调用）；
interpreter 包断言第三 shape/target 连续失败后 Native 禁用、RX 字节释放、VM 关闭后全局
可执行内存回基线。差分发现并修复 Tier 0 引擎 bug：方法调用 IC（O1-C4 `CallCached`）未检查
deleted map，delete own 方法后 IC 仍返回被删闭包而非原型链方法，补独立回归。profiling/guard
安全审计：`engine.GuardedMethodLookup` 已统一用于方法 guard 收集/执行，纯 objectValue 链数据
查找，非 plain receiver/原型链接口回退，accessor/Proxy 不触发用户代码。PR 1,000 例与 nightly
100,000 例（5 seed）零差分。该轮未改默认
`--jit=off`、未扩大 Native ABI 与 W^X 生命周期、不改变任何性能快照口径。

## 17. 下一轮优先级

后续任务拆分、依赖顺序、里程碑和逐项完成条件见
[`docs/jit-follow-up-development-plan.md`](jit-follow-up-development-plan.md)。

| 优先级 | 工作项 | 完成条件 |
|--------|--------|----------|
| P0 正确性 | 扩大到更多语法的生成式差分、Linux amd64 实机 CI、带异常和更多副作用的 deopt | Tier 0/1/2 差分和 race 测试稳定；Linux W^X/GC/抢占实机门禁通过 |
| P1 覆盖面 | 短生命周期对象/GC 热点、更多调用约定、带副作用/异常的 deopt、调用/属性未命中成本削减 | 不扩大 Go 指针或 ABI 风险；第三 target/shape 稳定回退；保持 11 项 `<=15x` 并让 mixed 优于当前快照 |
| P2 产品化 | 阈值自动调优、后台编译压力、代码缓存长期运行、正式 5 次中位数报告 | mixed 达到 `<=4x Node`、冷启动回退 `<=5%`、无长期 RX 泄漏后，才评估默认 `auto` |

执行顺序固定为 P0 -> P1 -> P2。任何阶段出现语义差分、W^X/GC/抢占不稳定或综合性能倒退，
立即停在上一层并保留 Tier 0/Tier 1 回退，不用扩大支持语法来掩盖门禁失败。
