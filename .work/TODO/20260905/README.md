# 2026-09-05 · 每日 TODO

> 总 TODO 见 [../README.md](../README.md)；证据规则见其 §1（**不满足即不许勾选**）。

**里程碑**：流程约定　|　**上一日**：[20260904](../20260904/README.md)

---

## 1. 今日目标（1-3 条，写「完成什么」而非「做什么」）

1. AGENTS.md 写入两条工作流硬约束：**任务开工前必须在当日 TODO 登记 TODO 列表**、
   **任务完成后必须复审改动内容，复审无问题才允许本地提交**——并当日按新流程示范执行
   （本文件即开工登记，AGENTS.md 改动经复审后本地提交）。

---

## 2. 待办清单

| # | 待办 | 状态 | 关联总 TODO 项 |
|---|------|------|---------------|
| 1 | AGENTS.md「每日节奏」增补开工强制项（当日 TODO 登记任务列表） | [x] | 流程约定 |
| 2 | AGENTS.md「提交与 CI」增补本地提交前置复审步骤 | [x] | 流程约定 |
| 3 | 复审 AGENTS.md diff 无误后本地提交（docs: 前缀，不推送） | [x] | 流程约定 |
| 4 | 主项目根目录新建 AGENTS.md 指向 aluka_r/AGENTS.md（子项目纪律入口） | [x] | 流程约定 |
| 5 | 复审新增文件后本地提交 | [x] | 流程约定 |

---

---

## 会话 2：梯队推进（目标：完成第一、二梯队，第三梯队落地可行项）

### 1'. 本会话目标（可判定完成态）

1. 第一梯队全清：fib(25) 在 aluvm 正常输出 75025；then 链式语义/JSON.parse/
   自定义可迭代 for...of 与 Go 逐字对拍；alukac 非 ASCII 字面量不再 mojibake。
2. 第二梯队全清：minor GC 生产启用（写屏障覆盖全部老写新变异点），全量测试
   与压力语料保持绿；aluka-core Heap 偏差在 ADR 收口。
3. 第三梯队落地：AsyncResource.bind 静态、http Agent 连接池、TLS rustls 评估
   （纯 Rust provider 环回可跑则落地，否则记录证据与解锁条件）；其余重项
   （vm 源码求值/http2 客户端/dns PTR/zstd 熵编码/worker eval/ALS 传播）
   逐项记录设计草图与解锁条件。

### 2'. 本会话待办清单

| # | 待办 | 状态 |
|---|------|------|
| T1 | 解释器线程大栈：fib(25) 探针对拍 Go（75025） | [ ] |
| T2 | promise.then 返回新 promise（链式/异常传播/then 返回 promise 采纳） | [ ] |
| T3 | JSON.parse（成功路径 + 错误路径对拍） | [ ] |
| T4 | 自定义可迭代对象 for...of（Symbol.iterator 接入 GetIterator） | [ ] |
| T5 | alukac 非 ASCII 字面量 mojibake 修复 | [ ] |
| T6 | 写屏障（老写新变异点清单化）+ minor 生产启用 + 压力回归 | [ ] |
| T7 | aluka-core Heap 收口：ADR 0002 增补落地偏差决定 | [ ] |
| T8 | async_hooks.AsyncResource.bind 静态注册修复 | [ ] |
| T9 | http Agent keepAlive 连接池（同源复用 TcpStream） | [ ] |
| T10 | TLS rustls + rustls-rustcrypto 评估 spike（环回 HTTPS echo） | [ ] |
| T11 | 其余第三梯队重项：设计草图 + 解锁条件登记（evidence 报告） | [ ] |
| T12 | 全量门禁 + 复审 + 提交（每里程碑一提交） | [ ] |

## 3. 达成证据

- 产物证据：`aluka_r/AGENTS.md` 新增「工作流硬约束」节（开工先登记 / 复审后才许
  提交两条门禁）与「提交与 CI」的前置复审条目；当日 TODO 本文件即开工登记示范工件。
- **复审记录**（门禁执行）：逐块阅读 `git diff AGENTS.md`，确认 ①两个 hunk 与
  §2 #1/#2 登记一一对应；②纯新增文本、无夹带无关改动；③文档改动无测试依赖。
  复审通过 → 本地提交。
- 提交证据：`docs(agents): 工作流增补当日 TODO 前置登记与本地提交前置复审两条硬门禁`。
- 产物证据（#4/#5）：根目录 `AGENTS.md`（15 行指针文件，声明 aluka_r 活跃开发 /
  aluka_g 只读 oracle / docs ADR 分工，工作流硬约束对全仓库生效）。
  **复审记录**：Write 全文已读；引用目标逐一核实存在（`aluka_g/AGENTS.md`、
  `docs/adr/0002-...md`）；diff --staged 仅 1 文件 +15 行，无夹带。
  提交证据：`docs: 主项目根目录新增 AGENTS.md...`（78a0849）。

## 4. 偏差

无——「仅在用户要求时 commit/push」既有规则保持不变，复审门禁置于其前。

## 5. 阻塞

无。

## 6. 明日入口

- 递归栈溢出修复（已登记遗留，最优先）；minor GC 写屏障变异点审计。
