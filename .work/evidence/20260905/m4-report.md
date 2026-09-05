# M4 里程碑证据：ISA 发布契约（feat/m4-isa-contract，2026-09-05）

## 任务对账（devplan §M4）

| 任务 | 状态 | 载体 |
|---|---|---|
| 定 `.aluc`/`.alua` 格式（调试段+剥离） | ✅ | `docs/isa-aluc-spec.md` + `aluka-bytecode/src/aluc.rs` |
| 拆 alukac/aluvm；aluka 便利壳 | ✅（M2 起已分立，本轮补格式支持） | `crates/aluka-cli/src/bin/{alukac,aluvm}.rs`、`main.rs` |
| eval/new Function 与 toString 策略 | ✅ | `docs/adr/0003-m4-isa-contract-policy.md` |
| 兼容窗口 + ISA 版本递增权限 | ✅ | 同 ADR 0003（容器/ISA 版本分离、N-1 窗口、评审门、能力位逃生舱） |
| **玩具 DSL 前端零改后端** | ✅ | 新 crate `crates/alisp`（极简 Lisp） |

## 验收核心：DSL 前端零改后端跑通

- `crates/alisp` **只依赖 `aluka-bytecode` 公共 API**（`Instr`/`Op`/`Constant`/
  `FuncTemplate`/`serialize_aluc`/`verify`），use 面即证明（lib.rs 头部）。
- 提交切分即 git 证明：M4-1/2（契约与工具，aluka-bytecode+cli）先行独立
  提交；M4-5/6（alisp 前端）提交**仅新增 crates/alisp + Cargo.toml members
  一行**——DSL 落地全程未触碰 aluka-vm/aluka-compiler 等后端 crate。
- e2e：`demo.lisp`（defn 递归 fact/if/算术/字符串/print）→ `alisp` 产
  `.aluc`（ALUKACC1 容器）→ `aluvm` 嗅探执行 → 输出
  `5 / 120 / hello lisp / 42` 全部正确；`cargo test -p alisp` 3 用例 +
  `aluka-bytecode` 4 容器单测守护。

## 格式要点（详见 isa-aluc-spec.md）

- `.aluc` = ALUKACC1 容器（版本 1）内嵌完整 ALUKABC1 payload——容器布局
  与 ISA 语义版本解耦；调试段（源路径+函数名表）可 `--strip-debug` 剥离。
- `.alua` = 确定性文本汇编转储（.module/.func/.const/pc: OP/.try/.endfunc）。
- `aluvm` 魔数嗅探双格式；`alukac --format=aluc|alukabc1`（默认互通格式，
  既有 e2e 全兼容）。

## 命令证据
- `cargo test` → 56 目标全绿（+alisp 3 用例、+aluc 4 单测）；
  `cargo clippy --all-targets -D warnings` → 0 错误；fmt --check 通过。
- 手工验收：`alisp demo.lisp -o demo.aluc && aluvm run demo.aluc` 输出全对。

## 已知限制（登记）
1. `.alua` 汇编器（文本→字节码）不在 M4 范围（仅有转储方向）。
2. eval/new Function 在 Rust 侧维持受限模式抛错（Go 侧可用）——策略 ADR
   已定钩子设计，两实现对拍探针规避该差异；toString 两侧同为降级形态。
3. Lisp v1 无字符串内嵌转义以外的类型（无浮点字面量/布尔字面量），无
   while/let；作为玩具验收足够。
