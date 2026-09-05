# ADR 0003：M4 ISA 发布契约策略——eval、Function.prototype.toString、兼容窗口

> 状态：已接受（M4 落地，2026-09-05）
> 关联：`docs/isa-aluc-spec.md`（.aluc/.alua 格式规范）、
> `docs/rust-reimplementation-devplan.md` §M4

## 1. `eval` / `new Function` 策略 —— 受限模式 + 源码求值钩子

**决定**：M4 起采用**受限模式**——`eval` / `new Function` 调用时抛出带
明确错误码的异常（`ERR_VM_DYNAMIC_EVAL_DISABLED`），不静默、不假实现。
未来放开路径已定型：`Vm::set_source_evaluator(hook)` ——由宿主
（aluka-cli，持有 aluka-parser/aluka-compiler 依赖）注入编译钩子，把源码
编译为函数模板追加进模块（含模板索引偏移换算约定）后执行。该设计在不
破坏 ISA 分层（aluka-vm 零前端依赖）的前提下支持完整动态求值，登记为
后续里程碑项。

**理由**：aluka-vm 执行字节码而非源码；把编译器带进后端违反 ISA 分层
（AGENTS 约束 3）。受限模式 + 钩子是「契约纯净性」与「动态性」的最优折中，
与 Node 的 `--disallow-code-generation-from-strings` 语义对齐。

## 2. `Function.prototype.toString` —— 降级形态

**决定**：M4 起 `fn.toString()` 返回 Node 风格的降级形态
`"[Function: <name>]"`（原生函数）/ `"[Function: <name> (lambda)]"` 等按
函数类别；**不承诺返回源片段**。Go oracle 实测同为降级
（`"[Function: foo]"`），两实现对拍一致。

**理由**：字节码不保留源片段（调试段 v2 的行号表不足以还原文本）；返回
合成文本会制造「源码保真」假象。Node 自身对原生函数也是降级形态。

## 3. 兼容窗口与 ISA 版本递增权限

**决定**：
1. `.aluc` 容器版本（ALUC_CONTAINER_VERSION，当前 1）与 ISA 语义版本
   （内层 payload version，当前 30）**独立演进**：容器布局变更递增前者，
   指令集/常量编码变更递增后者。
2. **兼容窗口承诺**：同一 ISA 语义主版本内，`.aluc` 产物跨 aluvm 补丁
   版本保证可执行；跨次版本（ISA 语义版本递增）时 aluvm 必须同时兼容
   上一 ISA 主版本读取（N-1 窗口）。
3. **核心 ISA 递增权限（架构评审门）**：新增/修改操作码、变更常量编码、
   变更 verifier 规则，必须先落 ADR（含 stack_effect 穷尽登记、golden
   语料新增、jitdiff 影响评估）并全量回归，不允许顺手改。
4. **逃生舱**：能力位（capability bits，容器 flags 已预留）作为未来
   扩展指令的分发机制，避免「一刀切版本墙」锁死优化空间。

## 4. 拆二进制

`alukac`（编译）/ `aluvm`（执行）自 M2 起即为独立二进制；`aluka` 为便利
壳（等价 `java`：直接编译+执行源码，产物进缓存）。M4 增量：aluvm 魔数
自动嗅探（ALUKACC1 / ALUKABC1 双格式）、alukac `--format`/`--strip-debug`。
