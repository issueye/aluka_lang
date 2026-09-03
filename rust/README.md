# aluka（Rust 重构）

Go 版 aluka 的 Rust 重构工作区。**当前是 M0 骨架**：crate 边界、公共 API 形状
与不变量已固定，实现体随里程碑填充。

计划文档：
- [`docs/rust-reimplementation-plan.md`](../docs/rust-reimplementation-plan.md) —— 功能全景、架构映射、为什么重构
- [`docs/rust-reimplementation-devplan.md`](../docs/rust-reimplementation-devplan.md) —— MVP 里程碑、并行轨道、验收指标

## 快速开始

```bash
cd rust
cargo build                  # 编译全部 crate
cargo test                   # 单元测试
cargo clippy --all-targets   # lint
cargo fmt --all              # 格式化

cargo run -p aluka-cli -- --capabilities   # 打印能力域与内置模块迁移进度
```

## crate 布局

| crate | 职责 | 对应 Go 版 |
|-------|------|-----------|
| `aluka-core` | 值系统、堆、隐藏类、GC 接口 | `internal/engine`（value/shape/gc） |
| `aluka-parser` | 词法、语法树 | `internal/engine/{lexer,parser,ast}` |
| `aluka-bytecode` | 指令集、序列化、优化 | `internal/engine/bytecode` |
| `aluka-compiler` | AST → 字节码，TS/JSX lowering | `internal/engine/compiler` |
| `aluka-vm` | Tier 0 虚拟机 | `internal/engine/interpreter` |
| `aluka-regex` | RegExp 双路引擎 | `internal/engine/regex` |
| `aluka-module` | ESM/CJS 与 Node 解析 | `internal/runtime/module` |
| `aluka-builtins` | `node:*` 内置模块注册表 | `internal/builtin` |
| `aluka-webapi` | Web API 与 Aluka 全局 | `internal/runtime/globals` |
| `aluka-runtime` | 运行时装配门面 | `pkg/aluka` + 装配逻辑 |
| `aluka-cli` | 命令行入口 | `cmd/aluka` |

JIT（`aluka-jit`）在 M4 引入，届时加入 workspace。

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

**行为仲裁**以 Go 版的 conformance 套件为准（`tests/conformance/`）：
迁移期任何语义分歧都以套件跑分为唯一判据，而不是"Rust 侧看起来更合理"。

## 现状与下一步

已完成（M0 骨架）：
- workspace + 11 个 crate 骨架，`cargo build/test/clippy/fmt` 全绿
- 端到端最小链路可跑：`Program` → compile → VM → `Value`（数值加法）
- 内置模块注册表登记 Go 版全部 56 个模块名，兼作迁移进度看板

下一步（M0 验收项，见 devplan）：
1. GC 选型原型 ×2（分代标记-清除 vs RC+循环回收）+ 微基准报告
2. `Value` 表示定案（`enum` vs NaN-box `u64`）
3. 冻结 `aluka-core` 公共 API，解锁轨道 B（内置库）并行开工
