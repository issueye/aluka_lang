# AGENTS.md — aluka Rust 重构工作区

> 面向 AI 代码助手（与新人）的工程指南。先读本文，再读 [README.md](./README.md)
> 与共享计划 [../docs/rust-reimplementation-plan.md](../docs/rust-reimplementation-plan.md)。
> 每日待办与证据落在 [../.work/TODO/](../.work/TODO/README.md)，见本文「开发流程」。

`aluka_r/` 是 aluka 的 Rust 实现，与 `aluka_g/`（Go 实现，当前唯一正式产物）平级，
`../docs/` 为两者共享。终局是 Rust 版**取代** Go 版，但**八项退役门禁全过之前
Go 版仍是正式版**：这里不做"两边都改、互为备份"的开发。

---

## 不可违背的约束（动手前必读）

项目级硬性约束，任何改动都不得破坏：

1. **`unsafe` 默认禁用**。workspace lint 设 `unsafe_code = "deny"`、
   `missing_docs = "warn"`，11 个 crate 全部 `[lints] workspace = true` 继承。
   只有 GC 分配器、JIT 机器码发射、FFI 边界可显式解禁，且**每处 unsafe 必须带
   `// SAFETY:` 注释论证不变量**。CI 跑 `cargo clippy --all-targets -D warnings`，
   警告即失败。
2. **行为以 Go 版为 oracle**。语义分歧不靠"Rust 侧看起来更合理"裁定，靠 conformance
   与差分对拍。`aluka_g/` 只读参考 + 跑二进制产出基线，**不改它的源码**。
3. **跨组件依赖只能经 ISA 契约**。`aluka-bytecode` 是前端（alukac）与后端（aluvm）
   之间的唯一接口。任何"parser 直接 import vm 的类型""compiler 调 core 的
   `Value`"都必须拒绝——否则契约退化为空文，前后端并行也就没了。
4. **元数据是单一事实来源，且必须是编译期保证**。新增/改指令必须在 `Op::stack_effect`
   的穷尽 `match` 里登记，漏一条就编译失败。
   - 这是刻意与 Go 版拉开的差距：Go 侧 `opMeta` 是稀疏 `[256]*OpMeta` 数组，
     **漏登记能编译通过**，只有遍历测试拦得住。不要为了少写 match 分支退回稀疏表。
   - **数量别搞混**：`aluka-bytecode::Op` 当前只有 **7** 个变体（骨架最小集），
     Go 侧完整指令集是 **106** 条。106 是 M0 反推与后续对齐的目标，不是现状。
5. **单二进制、静态、零运行时依赖**。新增外部 crate 先评估必要性；JIT 后端优先
   Cranelift（纯 Rust），避免引入 C 依赖。
6. **`FORMAT_VERSION` 纪律**。改字节码布局 / 常量编码 / 序列化字段必须递增
   `aluka-bytecode` 的 `FORMAT_VERSION`，否则旧磁盘缓存被误读。Rust 侧格式与
   Go 侧 `ALUKABC1` 是两个独立命名空间（Rust 自 `FORMAT_VERSION = 1` 起），
   但**读 Go 产物时按 Go 的版本号（当前 30）解析**，不要混。

---

## 常用命令

在 `aluka_r/` 下执行。工具链基线：`rustc 1.95` / `cargo 1.95`（`rust-version = 1.85`）。

```bash
cargo build                       # 全部 crate
cargo test                        # 21 个测试目标
cargo clippy --all-targets -- -D warnings   # lint（CI 门禁，警告即失败）
cargo fmt --all                   # 格式化；CI 用 --check
cargo run -p aluka-cli -- --capabilities    # 能力域 + 内置模块迁移进度看板

# 单 crate / 单测
cargo test -p aluka-bytecode
cargo test -p aluka-core -- --nocapture

# 基准：尚无 [[bench]] 目标，M0 的微基准需先建 benches/。
# bench profile 已配置为保留符号与帧指针（debug = true）供剖析。
cargo bench -p aluka-core
```

Go 侧对照（只读，不改）：`cd ../aluka_g && CGO_ENABLED=0 go build ./...`；
跑现成二进制 `./bin/aluka.exe -e "..."`。

CI（`../.github/workflows/ci.yml` 的 `rust` job）：build / test / clippy `-D warnings`
/ fmt `--check`。**Rust 侧红灯不阻塞 Go 版发布**——Go 版仍是唯一正式产物。

---

## 目录与 crate 布局

```
aluka_r/
├── Cargo.toml          workspace（11 members）+ workspace.lints
├── crates/
│   ├── aluka-bytecode/  ★ 共享契约：ISA 指令集、元数据、序列化、verifier、优化
│   ├── aluka-parser/    前端：lexer / ast
│   ├── aluka-compiler/  前端：AST → 字节码
│   ├── aluka-core/      后端：value / object / shape / gc
│   ├── aluka-vm/        后端：Tier 0 解释器
│   ├── aluka-regex/     后端：RegExp
│   ├── aluka-module/    后端：ESM/CJS resolver（条件挂实例，不做进程级全局）
│   ├── aluka-builtins/  后端：node:* 注册表（兼迁移看板）
│   ├── aluka-webapi/    后端：Web API 能力域
│   ├── aluka-runtime/   后端：装配门面
│   └── aluka-cli/       工具链：命令行入口
└── docs/               仅 Rust 侧的专项计划（跨实现的进 ../docs/）
```

★ `aluka-bytecode` 是**唯一允许被前后端同时依赖**的 crate。verifier 归它而非
归后端——它定义"什么样的字节码合法"，前端用它自检产物，后端在加载期强制执行。
JIT 会信任 verifier 的结论来省略运行期检查，所以 verifier 强度直接决定 unsafe
代码的安全论证强度。

后续按里程碑加入：`aluka-gc`（M3）、`aluka-jit`（M5）、
`aluka-cc` / `aluka-vm-bin`（M4 拆二进制）、`aluka-pkg` / `aluka-bundler` /
`ffi`（M6）。

---

## 代码风格与约定

- **格式化交给工具**：`cargo fmt --all`，不手调。CI 用 `--check` 强制。
- **注释语言**：对齐 Go 版与周边文件——**中文为主**，公开 API 文档注释可中英混用。
- **每个 crate / 模块要有文档注释**（`missing_docs = "warn"` 已开启，别关掉它）。
- **`Value` 故意不实现 `PartialEq`**。JS 相等与 Rust 派生语义不同（`NaN !== NaN`、
  `-0 === 0`、`0.1 + 0.2 !== 0.3`，且对象比引用、原始值比内容）。判类型用
  `Value::kind()` 或 `matches!(v, Value::Num(_))`，不要为了省事加一个"看着像
  相等"的 `PartialEq`——那会造出一整类静默错判。原因已写在 `value.rs` 的类型
  文档注释里，改约定要同步改那里。
- **穷尽 `match`，不用 `_ =>` 兜底**。尤其是 `Op::stack_effect` 这类元数据函数：
  加新指令时必须能在编译期被"缺一支"卡住。
- **`#[must_use]`** 标注返回 `Result`/新值的纯函数，与现有 crate 保持一致。

---

## 测试约定

- **表驱动 + 子测试**，对齐同目录已有写法。
- **断言类型/判别式，不断言 JS 相等语义**（见上条 `Value`）。
- **对拍必须写清 oracle 是谁**。三 tier（off / quick / auto）里 Tier 0 是唯一
  oracle，这条纪律从 Go 侧继承，不许松。
- **golden 语料**：`aluka-bytecode` 的加载期对拍用例。语料记录「如何重新生成」
  而非只存二进制——Go 侧 `.bc` 缓存键含**绝对路径与 mtime**，跨机不可复现。
- **新增指令 = 必须同时新增 golden 用例**，否则覆盖率报告不会到 106/106。

---

## 开发流程：TODO 与证据（每次会话都做）

Rust 重构周期长、跨里程碑，因此**每天的工作都要留下可复核的待办与证据**。
体系在 [../.work/TODO/](../.work/TODO/README.md)：

```
.work/TODO/
├── README.md              总 TODO：作用域、证据规则、M0-M7 待办、坑清单、遗留登记
├── TEMPLATE.md            每日模板
└── <YYYYMMDD>/README.md   当日 TODO + 证据
.work/evidence/<YYYYMMDD>/ 当日产物证据（报告、索引、基准数据）
```

### 每日节奏

1. **开工**：复制 `TEMPLATE.md` 到 `.work/TODO/<今日>/README.md`。
   先读昨天的「明日入口」与「未达成与阻塞」，别从空白开始。
2. **写目标**：填 §1，1-3 条，写「完成什么」的**可判定完成态**。
   反例「研究 verifier」；正例「verifier 拒绝全部 V 规则反例，`cargo test` 全绿」。
3. **干活**：待办粒度按**半天内能判定成败**切。跨天的写成里程碑级待办放回总 TODO。
4. **收工**：逐条补 §3 证据，填 §4 偏差、§5 阻塞、§6 明日入口。

### 证据规则（不满足即不许勾选）

「达成证据」不是自我描述，是**别人能重跑并看到同一结果的东西**。三类之一：

| 类型 | 形式 | 例 |
|---|---|---|
| **命令证据** | 命令 + 期望输出关键行（不贴全量日志） | `cargo test -p aluka-bytecode` → `42 passed; 0 failed` |
| **产物证据** | 入库文件路径 + 校验方式 | `golden-index.tsv` + 行数 + sha256 |
| **提交证据** | commit hash + 一句话改了什么 | `23d8866` 目录重构，1568 个 100% rename |

四条硬规则：

- **不许用"应该可以""看起来对了"结项**。没跑通就写「未达成 + 卡在哪 + 已排除什么
  + 下一步试什么」——诚实的空栏比假勾选有用。
- **性能类必须写方法学**：交替执行 + 冷却 + min-of-N。Go 侧踩过持续负载 ~5s 后
  降频到 ~20% 的坑，当时差点把环境现象误判成 JIT 回归。**没有方法学的数字作废**。
- **对拍类必须写清 oracle**。
- **正确性优先于性能**，冲突本身要记进 §4 偏差栏。

### 状态标记

`[ ]` 未做　`[x]` 完成且证据已补　`[~]` 进行中　`[!]` 阻塞（写清卡在哪）
　`[-]` 放弃（写清为什么）

### 回写总 TODO 与计划文档

- 里程碑内的待办完成后，在总 TODO §3 勾选；**进度只在里程碑收口时更新总览表**，
  日常进展留在每日文件，避免两边反复改。
- 总 TODO 与 devplan 不一致时：**devplan 是权威**，改总 TODO 对齐它。
  若发现 devplan 本身有错（数字、验收项），改 devplan 并在当日 §4 记一笔。
- 顺手发现的缺陷若超出当前作用域，**不要就地修**，登记到总 TODO §6 遗留清单。
  其中能转成新实现必测项的（例如 Go 侧 verifier 的缺口），直接写进对应的
  Rust 待办验收标准。

---

## 提交与 CI

- **分支**：默认 `main`；改动先开分支（除非用户明确要求直接提交 main）。
  **仅在用户要求时才 commit / push**。
- **提交信息**：沿用仓库既有前缀（`feat:` / `fix:` / `docs:` / `refactor:` /
  `test:` / `ci:`），正文可中文。Rust 侧建议带 crate 范围，如
  `feat(aluka-bytecode): verifier 补齐跨块栈深合流`。
- 提交前自查：
  ```bash
  cargo build --all-targets
  cargo test
  cargo clippy --all-targets -- -D warnings
  cargo fmt --all --check
  ```

---

## 进一步阅读

- [README.md](./README.md) —— 工作区总览与现状
- [../docs/rust-reimplementation-plan.md](../docs/rust-reimplementation-plan.md) —— 功能全景、alukac/aluvm 架构、8 阶段、退役门禁
- [../docs/rust-reimplementation-devplan.md](../docs/rust-reimplementation-devplan.md) —— M0-M7 里程碑、7 轨道、16 项验收
- [../docs/adr/jvm-style-bytecode-architecture.md](../docs/adr/jvm-style-bytecode-architecture.md) —— 字节码升格 ISA 契约（已采纳）
- [../.work/TODO/README.md](../.work/TODO/README.md) —— 执行视图：当前待办与坑清单
- Go 侧参照（只读）：[../aluka_g/AGENTS.md](../aluka_g/AGENTS.md)、
  `../aluka_g/internal/engine/bytecode/`（ISA 事实来源）
