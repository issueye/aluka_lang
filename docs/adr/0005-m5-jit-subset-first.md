# ADR 0005：M5 JIT 策略——子集先行的 Cranelift 后端与覆盖矩阵分期

> 状态：已接受（M5 启动，2026-09-05）
> 关联：`docs/rust-reimplementation-devplan.md` §M5、ADR 0002（GC）、
> `docs/adr/0003-m4-isa-contract-policy.md`

## 决定

1. **后端选 Cranelift**（devplan 低风险优先项）：纯 Rust、无 C/asm 依赖，
   满足 AGENTS 约束 5；x64/aarch64 双后端白得。
2. **子集先行（分阶段覆盖矩阵）**：
   - **J1（本次落地）**：数值域子集——`PushConst/LoadLocal/StoreLocal/
     Add/Sub/Mul/Div/Lt/Gt/Eq/Jmp/JmpFalsePop/Return`，全部 f64 语义
     （布尔以 1.0/0.0 表示）。值表示：JIT 侧以裸 f64 承载，与 VM 的
     NaN-box-free `Value::Number` 逐位对齐。
   - **J2（后续）**：字符串/对象属性 PIC（shape guard）+ 调用（含
     closureCall）+ 去优化恢复 + 栈映射。
   - **J3（后续）**：数组下标特化、生成器/async 交互。
   每阶段以 jitdiff 生成式差分（解释器 vs JIT 零失配）为晋级门禁，
   对齐 Go 版 jit-coverage-matrix 传统。
3. **Quick IR**：JIT 前置 peephole（常量折叠 `PushConst a; PushConst b;
   Add → PushConst a+b` 等）在 `aluka-jit` 内实现；AST 级常量折叠/死代码/
   jump 优化已存在于 `aluka-compiler/src/opt.rs`（M2 起）。
4. **热点识别与去优化**：J1 无 OSR/去优化——函数入口即 JIT（全部子集
   函数可安全编译）；跨出子集（遇到非子集操作码）→ 编译期拒绝，回退
   解释器（无部分去优化需求）。J2 引入 guard 后再设计去优化协议。

## 理由

- 子集先行把「机器码正确性」与「全语言覆盖」解耦：jitdiff 生成式差分在
  子集内可穷举强度验证（≥3 千例零失配），为 PIC/去优化等高阶特性提供
  可信的机器码地基。
- f64 数值域与 JS Number 语义天然对齐（IEEE-754 双精度，除零→Infinity
  一致），是零失配差分的最小可靠面。

## 后果

- **正面**：M5 任务 1（Quick IR → Cranelift）与 jitdiff 项在 J1 落地；
  热循环性能获得数量级改善空间（见 jitbench 基线）。
- **负面/已知代价**：propAccess/callOverhead ≤ node 5× 的验收依赖 J2
  （PIC + 调用约定），J1 阶段该两项以**解释器基线 vs node** 的对拍表
  登记现状（差距与路径），不虚标达成。
- **中立**：`aluka-jit` 为独立 crate，仅依赖 aluka-bytecode 公共 API 与
  Cranelift，遵循 M4 确立的契约分层。
