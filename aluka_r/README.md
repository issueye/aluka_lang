# aluka（Rust 重构）

Go 版 aluka 的 Rust 重构工作区。**当前是 M0 骨架**：crate 边界、公共 API 形状
与不变量已固定，实现体随里程碑填充。

终局是**Rust 版取代 Go 版**（不是长期双实现）。两个硬前提：完全兼容 JS/TS
语法（M2）与 ISA 字节码契约落地（M4）。八项退役门禁见 devplan §8。

计划文档：
- [`docs/rust-reimplementation-plan.md`](../docs/rust-reimplementation-plan.md) —— 功能全景、架构映射、为什么重构
- [`docs/rust-reimplementation-devplan.md`](../docs/rust-reimplementation-devplan.md) —— MVP 里程碑、并行轨道、验收指标
- [`docs/adr/jvm-style-bytecode-architecture.md`](../docs/adr/jvm-style-bytecode-architecture.md) —— ISA 契约决策（已采纳）

## 快速开始

```bash
cd rust
cargo build                  # 编译全部 crate
cargo test                   # 单元测试
cargo clippy --all-targets   # lint
cargo fmt --all              # 格式化

cargo run -p aluka-cli -- --capabilities   # 打印能力域与内置模块迁移进度
```

## 架构：alukac 前端 + aluvm 后端 + ISA 契约

字节码是两个组件之间的**唯一接口**（JVM 式分离）。跨组依赖只允许经
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

**verifier 归契约而非后端**：它定义"什么样的字节码是合法的"——前端可用它
自检产物，后端在加载期强制执行。Rust JIT 会信任 verifier 的结论来省略运行期
检查，所以它的强度直接决定 unsafe 代码的安全论证（见 plan §6.4）。

后续加入：`aluka-gc`（M3）、`aluka-jit`（M5）、`aluka-cc` / `aluka-vm-bin`
（M4 拆二进制）、`aluka-pkg` / `aluka-bundler` / `ffi`（M6）。

## 工程约定

**unsafe 默认禁用**。workspace lint 设 `unsafe_code = "deny"`；只有 GC 分配器、
JIT 机器码发射与 FFI 边界可显式解禁，且每处需 `SAFETY` 注释说明不变量
（见计划文档 §6.4）。

**几条从 Go 版实验里买来的约束**，实现时不要重犯：
- 槽位不能用无指针的 `u64` 存对象引用——除非 GC 自管可达性（Go 版因此悬垂，
  见 `docs/adr/stage2-nanbox-slots-rejected.md`）；
- 带指针对象不能简单 arena bump——存活对象会 pin 整块并级联保活
  （`docs/adr/object-arena-rejected.md`，RSS 放大 22–71x）；
- 字节码布局变更必须递增 `FORMAT_VERSION`，否则旧磁盘缓存被误读；
- 模块解析条件必须挂在 resolver 实例上，不能做成进程级全局（Go 版曾因此
  让运行时与打包器互相污染）。

**行为仲裁**以 Go 版的 conformance 套件为准（`../aluka_g/tests/conformance/`）：
迁移期任何语义分歧都以套件跑分为唯一判据，而不是"Rust 侧看起来更合理"。

## 现状与下一步

已完成（M0 骨架）：
- workspace + 11 个 crate 骨架，`cargo build/test/clippy/fmt` 全绿
- 端到端最小链路可跑：`Program` → compile → VM → `Value`（数值加法）
- 内置模块注册表登记 Go 版全部 58 个模块名，兼作迁移进度看板

下一步（M0 验收项，见 devplan §2）：
1. **ISA 规范化**：把 `docs/bytecode-spec.md` 提升为规范（校验规则 + 一致性
   用例 + 扩展指令能力位协商）；Go 版 verifier 强化到"通过即安全"
2. **golden 字节码语料 ≥200 例**（Go 前端产出）——这是前后端并行的地基，
   M1 验收要求 aluvm 对它 100% 行为一致
3. GC 选型原型 ×2（分代标记-清除 vs RC+循环回收）+ 微基准报告
4. `Value` 表示定案（`enum` vs NaN-box `u64`）。注意 `Value` 是后端内部表示、
   **不进 ISA**，所以这个决策在契约冻结后仍可演进
5. 冻结 `aluka-core` 公共 API，解锁轨道 B（内置库）并行开工

第 1、2 项完成后，A1（后端）与 A2（前端）即可完全并行：A1 吃 Go 前端产出的
字节码，A2 的产物喂 Go VM 验证，互不阻塞。
