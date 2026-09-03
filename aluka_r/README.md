# aluka（Rust 重构）

Go 版 aluka 的 Rust 重构工作区。**当前是 M0 骨架**：crate 边界、公共 API 形状
与不变量已固定，实现体随里程碑填充。

面向 AI 代码助手与新人的工程指南（约束、命令、测试约定、开发流程）见
[AGENTS.md](./AGENTS.md)。

## 这是什么，以及不是什么

- **是**：aluka 的下一代实现。终局是**取代 Go 版**，不是长期双实现。
- **暂时不是**：可用产物。八项退役门禁全过之前，正式实现仍是
  [`../aluka_g/`](../aluka_g/)；本工作区红灯不阻塞 Go 版发布。

取代有两个硬前提，任一不满足则不得退役 Go 版：

1. **完全兼容 JS/TS 语法**（M2 达成）——ECMAScript 全量 + TS 注解剥离 + JSX/TSX，
   以 test262 子集与全部 conformance 套件为判据；
2. **字节码升格为 ISA 契约**（M4 达成）——前端与运行时解耦，后续新语法只需
   在前端产出合规字节码即可接入平台。

依据：本仓库 Go 实现 17.8 万行 Go 代码（源码 12.6 万 + 测试 5.2 万）、
四个已证伪的内存模型实验（ADR）、以及性能报告。技术动机不是"Go 不行"，而是
Go 的 GC + interface 内存模型对上分配密集的 JS 引擎负载有**结构性上限**
（无法做 bump 分配年轻代、无法 NaN-box 槽位、无法 unboxed 数值入 interface），
Rust 的显式内存模型天然放开这三条路。

## 快速开始

```bash
cargo build                       # 编译全部 crate
cargo test                        # 单元测试（21 个测试目标）
cargo clippy --all-targets -- -D warnings
cargo fmt --all

cargo run -p aluka-cli -- --capabilities   # 能力域 + 内置模块迁移进度
```

`--capabilities` 当前输出 10 个能力域（含依赖边）与 58 个内置模块的迁移状态：

```
capabilities (10):
  Console
  Timers
  ...
  Fetch <- [Streams, Url, Events]
  Aluka <- [Fetch, Streams, Crypto]

builtin modules: 58 registered
  native:  0
  bridged: 0
  planned: 58
```

`planned: 58` 不是停滞，是**看板**：注册表登记了 Go 版全部 58 个
`RegisterBuiltin` 模块名，逐模块从 `Planned → ForeignBridge → Native` 推进，
迁移进度因此可被机器读取而不是靠人回忆。

## 架构：alukac 前端 + aluvm 后端 + ISA 契约

```
                  ┌─ ISA 契约（.aluc / .alua，版本化 + 能力位）──┐
 JS / TS / JSX ─→ │ alukac（前端：词法/语法/降级/字节码优化）      │
 未来新语法   ─→  │                                              │
                  └──────────────────┬───────────────────────────┘
                                     ↓ 字节码
                  ┌──────────────────────────────────────────────┐
                  │ aluvm（后端：加载 + verifier + VM + JIT + GC） │
                  └──────────────────────────────────────────────┘
```

字节码是两个组件之间的**唯一接口**。crate 按归属分组，跨组依赖只允许经
`aluka-bytecode`——"前端直接调用后端类型"必须拒绝，否则契约退化为空文。

| crate | 组件 | 职责 | 对应 Go 版 |
|-------|------|------|-----------|
| `aluka-bytecode` | **契约** | ISA：指令集、元数据、序列化、verifier、优化 | `internal/engine/bytecode` |
| `aluka-parser` | 前端 | 词法、语法树（JS + TS 注解 + JSX） | `internal/engine/{lexer,parser,ast}` |
| `aluka-compiler` | 前端 | AST → 字节码，TS 剥离 / JSX lowering | `internal/engine/compiler` |
| `aluka-core` | 后端 | 值系统、堆、隐藏类、GC 接口 | `internal/engine`（value/shape/gc） |
| `aluka-vm` | 后端 | Tier 0 虚拟机 | `internal/engine/interpreter` |
| `aluka-regex` | 后端 | RegExp 双路引擎 | `internal/engine/regex` |
| `aluka-module` | 后端 | ESM/CJS 与 Node 解析 | `internal/runtime/module` |
| `aluka-builtins` | 后端 | `node:*` 内置模块注册表 | `internal/builtin` |
| `aluka-webapi` | 后端 | Web API 与 Aluka 全局 | `internal/runtime/globals` |
| `aluka-runtime` | 后端 | 运行时装配门面 | `pkg/aluka` + 装配逻辑 |
| `aluka-cli` | 工具链 | 命令行入口 | `cmd/aluka` |

**verifier 归契约而非后端**：它定义"什么样的字节码是合法的"——前端可用它自检
产物，后端在加载期强制执行。Rust JIT 会信任 verifier 的结论来省略运行期检查，
所以它的强度直接决定 unsafe 代码的安全论证。

后续加入：`aluka-gc`（M3）、`aluka-jit`（M5）、`aluka-cc` / `aluka-vm-bin`
（M4 拆二进制）、`aluka-pkg` / `aluka-bundler` / `ffi`（M6）。

## 工程约定

**unsafe 默认禁用**。workspace lint 设 `unsafe_code = "deny"`，11 个 crate 全部
继承；只有 GC 分配器、JIT 机器码发射与 FFI 边界可显式解禁，且每处需 `SAFETY`
注释说明不变量（见 [AGENTS.md](./AGENTS.md)）。

**几条从 Go 版实验里买来的约束**，实现时不要重犯：

- 槽位不能用无指针的 `u64` 存对象引用——除非 GC 自管可达性
  （Go 版因此悬垂，见 [`../docs/adr/stage2-nanbox-slots-rejected.md`](../docs/adr/stage2-nanbox-slots-rejected.md)）；
- 带指针对象不能简单 arena bump——存活对象会 pin 整块并级联保活
  （[`../docs/adr/object-arena-rejected.md`](../docs/adr/object-arena-rejected.md)，RSS 放大 22–71×）；
- 字节码布局变更必须递增 `FORMAT_VERSION`，否则旧磁盘缓存被误读；
- 模块解析条件必须挂在 resolver 实例上，不能做成进程级全局（Go 版曾因此让
  运行时与打包器互相污染）；
- 指令元数据必须是**编译期**保证：Go 侧 `opMeta` 是稀疏 `[256]*OpMeta` 数组，
  漏登记能编译通过；Rust 侧用穷尽 `match` 让漏登记直接编译失败。

**行为仲裁**以 Go 版的 conformance 套件为准（`../aluka_g/tests/conformance/`）：
迁移期任何语义分歧都以套件跑分为唯一判据，而不是"Rust 侧看起来更合理"。

## 开发流程

每天的工作留一份可复核的待办与证据，体系在
[`../.work/TODO/`](../.work/TODO/README.md)：总 TODO（执行视图）+ 每日模板 +
当日文件。核心纪律是**证据必须是别人能重跑并看到同一结果的东西**（命令输出 /
入库产物 / commit），不许用"应该可以"结项；性能数字必须带方法学
（交替执行 + 冷却 + min-of-N），否则热降频会伪装成代码回归。
规则细节见 [AGENTS.md 的「开发流程」](./AGENTS.md)。

## 现状与下一步

已完成（M0 骨架）：

- workspace + 11 个 crate 骨架，`cargo build/test/clippy/fmt` 全绿
- 端到端最小链路可跑：`Program` → compile → VM → `Value`（数值加法）
- 内置模块注册表登记 Go 版全部 58 个模块名，兼作迁移进度看板

下一步（M0 验收项，见 [devplan §2](../docs/rust-reimplementation-devplan.md)）：

1. **反推 ISA 事实表**：从 Go 侧导出 106 条 opcode 的数值/操作数/栈效果
2. **写 `../docs/aluvm-isa-spec.md`**：逐指令规范，补 Go 文档缺的 opcode 数值、
   异常语义、强制转换语义、完整文件布局、verifier 契约
3. **golden 字节码语料 ≥200 例**：跑 Go 二进制收割，判据是全 106 条指令各出现 ≥1 次
4. **Rust verifier**：从第一天就是"通过即安全"完整强度（含 Go 侧缺失的跨块栈深
   合流、try 表结构、模板索引边界等）
5. GC 选型原型 ×2 + `Value` 表示定案 + 冻结 `aluka-core` 公共 API

第 1-4 项完成后，A1（后端）与 A2（前端）即可完全并行：A1 吃 Go 前端产出的
字节码，A2 的产物喂 Go VM 验证，互不阻塞——这是把 M1 关键路径缩短一整个前端
工期（约 3 个月）的来源。
