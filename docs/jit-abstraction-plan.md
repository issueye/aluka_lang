# JIT 抽象方案（设计文档）

> 状态：**仅设计，未实现**。本文是 JIT 层后端抽象的架构方案，供后续
> 里程碑评审后实施。当前 JIT 层已具备分层基础（IR 可移植、native 平台
> 分文件），本文定义把剩余耦合（`Program` 内嵌 native 产物、bridge 直连
> 具体类型）收敛为接口的具体路径，并为 arm64 等新后端铺路。

## 1. 现状与目标

### 1.1 当前架构

```
interpreter/jit_bridge.go（~3200 行，tier 决策/缓存/驱逐/deopt 编排）
        │ 直接持有 *jit.Program / *jit.TraceProgram 具体类型
        ▼
internal/engine/jit/
  ├─ ir.go        Program：IR + PIC 守卫 + trace 协议 + native 产物 平铺一个 struct
  ├─ trace.go     TraceProgram{program *Program}，native 字段透传
  ├─ native_program.go / native_trace.go   native 编译/执行/生命周期（耦合 Program）
  ├─ native_emit_amd64.go                  compileNativeProgram（唯一后端入口）
  └─ native/      纯 ABI：Code/Frame/execmem/call（无 engine 依赖，平台分文件）
```

### 1.2 目标架构

```
interpreter/jit_bridge.go
        │ 仅依赖 Backend/Executable 接口 + Program（纯 IR）
        ▼
internal/engine/jit/
  ├─ backend.go    Backend 接口（编译入口注册点）+ Executable 接口（产物句柄）
  ├─ ir.go         Program：纯 IR（native 字段与方法全部剥离）
  ├─ trace.go      TraceProgram：纯 trace IR（不再穿透 native 字段）
  ├─ native_emit_amd64.go    amd64 后端（实现 Backend；nativeInputPlan/Frame 偏移内部化）
  └─ native/       纯 ABI 不变（Frame/Code/execmem/call）
```

### 1.3 抽象收益

- **Program 回归纯 IR**：IR 的验证/审计/序列化不再夹杂后端状态；
- **多后端并存**：Quick（Go 执行 IR）与 Native（机器码）通过统一执行协议
  （`ExitReason`/`DeoptExit`）互操作，新后端（arm64）只动 emit 层；
- **bridge 解耦**：tier 决策不再感知 `HasNative()` 等 Program 细节，
  改为检查独立的 native 产物引用；
- **测试隔离**：native lowering（`FuzzNativeLowering`）改走后端接口。

## 2. 现有耦合清单（改造对象）

### 2.1 Program 结构体上的 native 字段（ir.go:91-117）

| 字段 | 位置 | 说明 |
|------|------|------|
| `nativeCode *jitnative.Code` | ir.go:97 | 机器码句柄（可空） |
| `nativePlan *nativeInputPlan` | ir.go:105 | 输入规划（参数/属性/callee guard） |
| `nativeNumberArgs uint16` | ir.go:106 | Number 参数位掩码 |
| `nativePreassigned uint64` | ir.go:107 | 预置 frame 槽位掩码 |
| `nativeTrace bool` | ir.go:108 | trace 降级产物标记 |

### 2.2 Program 上的 native 方法（native_program.go）

`CompileNative`/`CompileNativeForDump`/`compileNative`（含 `hasExceptionExit`
拒绝）/`CloneForNative`/`AdoptNativeFrom`/`HasNative`/`NativeSize`/
`NativeDisassembly`/`Close`/`ExecuteNative*`/`lowerNativeInputs*`。

### 2.3 TraceProgram 的 native 透传（native_trace.go）

`compileNative`/`HasNative`/`NativeSize`/`Close`/`ExecuteNativeBudgetDetailed*`
全部穿透 `t.program.nativeCode/nativePlan`；`commitNativeTraceFrame` 两阶段
提交读 `frame.Status` 脏位。

### 2.4 bridge 依赖面（jit_bridge.go）

- `quickJITState.program *jit.Program` + `HasNative()`/`NativeSize()`/
  `NativeDisassembly()`/`Close()`/`AdoptNativeFrom()`/`CloneForNative()`
  等生命周期调用；
- 执行入口 `state.program.ExecuteNativeBudgetWithSafepoint(...)`（:1450）与
  trace 侧 `ExecuteNativeBudgetVerifiedWithSafepoint`（:2969）；
- 后台编译：`queueNativeCompile` → `CloneForNative` 快照 → goroutine
  `CompileNative` → `pollNativeCompiles` → `adoptNative`（`AdoptNativeFrom`）。

### 2.5 既有抽象边界（保留不动）

- `compileNativeProgram(p *Program, retainDebugBytes ...bool) (*jitnative.Code, error)`
  （native_emit_amd64.go:26）——**唯一后端编译入口**，非 amd64 平台返回
  `native.ErrUnsupported`（native_emit_unsupported.go，9 行）；
- `jitnative.Code`/`Frame`：纯 ABI（Frame 零 Go 指针；Status 0=完成、
  2=预算让出、trace 语义退出 3+exitID）；
- `jit.Config`/`jit.Stats`/`Safepoint` 函数类型；
- 平台分文件：`native/execmem_{linux,windows,unsupported}.go`、
  `native/call_{amd64,unsupported}.go`、`native_emit_{amd64,unsupported}.go`。

## 3. 接口设计

### 3.1 Backend（编译后端）

```go
// jit/backend.go（新增）
// Backend 抽象 IR → 机器码的编译后端。当前唯一实现：amd64
// （native_emit_amd64.go 的 compileNativeProgram 收敛为此接口的实现）。
// 新增后端（如 arm64）只需新平台文件实现 Compile，bridge 零改动。
type Backend interface {
	// Name 返回后端名（诊断/dump 用，如 "amd64"）。
	Name() string
	// Supported 报告当前平台/构建是否可编译并执行机器码
	// （非 amd64 或非 windows/linux 返回 false）。
	Supported() bool
	// Compile 将已验证的 IR 编译为可执行产物。p 必须已通过
	// Program.Verify；trace 为 true 时按 trace 输入规划降级
	// （lowerNativeTraceInputs 语义）。编译失败返回 error，
	// 调用方按拒绝处理（计入拒绝缓存，不重试）。
	Compile(p *Program, trace bool, opts CompileOptions) (Executable, error)
}

// CompileOptions 携带编译期选项。
type CompileOptions struct {
	// RetainDebugBytes 保留机器码副本供 Disassembly/调试（--jit-dump=asm）。
	RetainDebugBytes bool
}
```

### 3.2 Executable（机器码产物句柄）

```go
// Executable 是机器码产物的不透明句柄（替代 Program 上的 nativeCode）。
type Executable interface {
	Size() int
	DebugBytes() []byte
	Disassembly() string
	Close() error
	// ExecuteBudgetWithSafepoint 以预算执行到语义出口（返回 ExitReason
	// 与 DeoptExit 兼容的执行协议；语义与现 ExecuteNativeBudget* 一致）。
	ExecuteBudgetWithSafepoint(thisVal engine.Value, args []engine.Value,
		budget uint32, poll Safepoint) (engine.Value, ExitReason, uint64, error)
}

// TraceExecutable 是 trace 产物的执行接口（native_trace.go 的
// ExecuteNativeBudgetDetailedWithSafepoint 语义）。
type TraceExecutable interface {
	Executable
	ExecuteBudgetDetailedWithSafepoint(locals []engine.Value, budget uint32,
		poll Safepoint) (DeoptExit, ExitReason, uint64, error)
}
```

### 3.3 注册点

```go
// backend 变量按构建标签选择：amd64 → nativeAMD64{}；其余 → unsupported{}。
var backend Backend
```

- `native_emit_amd64.go`：`type amd64Backend struct{}` 实现 `Compile`，
  内部持有现有 `lowerNativeInputs/lowerNativeTraceInputs` 与 Frame 布局
  偏移（nativeResultOffset 等常量**收敛进后端内部**，不再出现在 Program）；
- `native_emit_unsupported.go`：`type unsupportedBackend struct{}`，
  `Supported() = false`、`Compile` 返回 `native.ErrUnsupported`。

### 3.4 bridge 侧改造（P3）

```go
type quickJITState struct {
	program *jit.Program   // 纯 IR（不再含 native 字段）
	native  jit.Executable // nil = 未安装/已驱逐；由 backend 编译所得
	// ...其余字段不变（热点/拒绝/自适应/预算/驱逐统计）
}
type quickTraceState struct {
	program  *jit.TraceProgram
	native   jit.TraceExecutable
	// ...
}
```

- `HasNative()` → `state.native != nil`；
- `ExecuteNativeBudgetWithSafepoint` → `state.native.ExecuteBudgetWithSafepoint`；
- 后台编译：`queueNativeCompile` 快照 `program.Clone()`（纯 IR 深拷贝）→
  goroutine `jit.Backend().Compile(clone, trace, opts)` → 结果经 channel
  回来直接安装为 `state.native`（`AdoptNativeFrom` 的"两个 Program 之间
  搬字段"步骤消失，产物天然独立）；
- 驱逐（`dropNative`/LRU）只释放 `state.native`，不再触碰 Program。

## 4. 分阶段实施路径

### P1：接口定义 + 注册点（零行为变化）

- 新增 `jit/backend.go`（Backend/Executable/CompileOptions + 包级
  `Backend()` 访问器）；
- `native_emit_amd64.go`/`native_emit_unsupported.go` 各自实现
  `Compile`（内部直接转发现有 `compileNativeProgram`，保持 Program
  字段读写不变）；
- 新增 `backend_test.go`：平台能力断言（amd64 → Supported、非 amd64 →
  ErrUnsupported）+ 编译-执行-释放生命周期冒烟；
- 门禁：jitdiff 三 tier 零失配 + fuzz 全绿（本阶段纯新增，不应有任何变化）。

### P2：Program 剥离 native 字段（核心重构）

- 删除 `Program` 的 `nativeCode/nativePlan/nativeNumberArgs/
  nativePreassigned/nativeTrace` 字段与 `native_program.go` 的
  native 方法（迁移语义到后端内部）；
- `lowerNativeInputs*`/`nativeInputPlan`/`nativePropertyInput`/
  `nativeCalleeGuard` 移入 `native_emit_amd64.go`（后端私有）；
- `Program.Clone()`（纯 IR 深拷贝）替代 `CloneForNative`；
- `Program.Verify` 中 trace 协议相关检查保留（IR 层），native 相关
  检查移入后端 Compile；
- `FuzzNativeLowering`（fuzz_test.go:283）改走 `Backend().Compile`，
  保留"不发布机器码只跑 lowering"与"发布后 Close 无 RX 泄漏"两条断言；
- `native_program_test.go`/`native_trace_test.go` 的用例迁移到
  后端视角（经 Backend 接口构造/执行/释放）。

### P3：bridge 改造

- `quickJITState`/`quickTraceState` 增加 `native` 字段，替换所有
  `HasNative()/NativeSize()/ExecuteNative*/Close()` 调用点；
- 后台编译链（queueNativeCompile → pollNativeCompiles → adoptNative）
  改为"纯 IR 快照 + Compile + 安装 Executable"；
- `verifyNativeResult`/`dumpJITASM`/`NativeDebugBytes` 改读 Executable；
- 统计字段（NativeCompiled/NativeCodeBytes/NativeEvictions 等）语义不变。

### P4：测试迁移与门禁

- jitdiff（off/quick/auto 三 tier oracle）全量回归；
- 3 个 fuzz target（FuzzVerifyProgram/FuzzCompileTrace/FuzzNativeLowering）
  回归（FuzzNativeLowering 已改走接口）；
- `jit_*_test.go`（soak/reconfigure/adaptive/r5-cache-budget/
  guard_mutation）、`r4_*_test.go`、`side_effect_deopt_test.go`、
  `native_runtime_stress_*`、W^X 深度校验、崩溃隔离、`-race`、
  `GOGC=20/100` 压力全量通过；
- conformance：build/node22/express 回归。

## 5. 风险与回滚

| 风险 | 缓解 |
|------|------|
| 重构引入行为漂移 | jitdiff 三 tier 是唯一 oracle：每阶段（P1→P2→P3）独立提交并全量差分，任何失配立即回退该阶段 |
| 后台编译竞态 | `AdoptNativeFrom` 的"编译产物独立化"简化了安装路径（不再跨 Program 搬字段）；P3 前保持 channel + generation 语义不变 |
| native ABI 变更 | Frame 布局/调用约定**不在本方案改动范围**；`native/` 子包零改动，任何 ABI 调整需单独评审 |
| fuzz 依赖具体类型 | FuzzNativeLowering 的 lowering 入口随后端内部化，测试改为后端接口调用，断言（不 panic/无 RX 泄漏）不变 |
| 统计口径漂移 | Stats 字段集合与语义不变（仅访问路径变化），`--jit-stats` 输出逐字段对拍 |

回滚策略：本方案为纯重构（无行为变更），任一阶段差分失败即 revert 该
阶段 commit，不影响其它里程碑。

## 6. arm64 后端铺路示例（目标形态）

```go
// native_emit_arm64.go（未来）
//go:build arm64 && (windows || linux)

type arm64Backend struct{}

func (arm64Backend) Name() string      { return "arm64" }
func (arm64Backend) Supported() bool   { return true }
func (arm64Backend) Compile(p *Program, trace bool, opts CompileOptions) (Executable, error) {
	// 复用 lowerNativeInputs 语义（Portable），发射 arm64 机器码，
	// 经 jitnative.Publish 发布；Frame 布局与 amd64 共享（纯数据 ABI）。
	...
}
```

新增后端只动：emit 文件 + `native/call_<arch>.go` 调用约定 + 平台分文件
注册 `backend` 变量。bridge/Program/测试框架零改动。

## 7. 明确不做的事

- **不统一 Quick/Native 执行接口**：两者能力不对称（Native 无 Go 指针、
  无 engine.Value、无 exception exit——`hasExceptionExit` 就是证据），
  统一接口会退化为"最弱共同点"；保持 `ExitReason`/`DeoptExit` 协议分层；
- **不移出 tier 编排状态机**：热点计数/自适应阈值/编译预算/LRU 驱逐深度
  依赖 VM 内部状态（vmFrame/vmClosure/v.stack），留在 bridge 是正确的
  策略归属；
- **不改 native ABI**：Frame 布局、Status 编码、调用约定原样保留。
