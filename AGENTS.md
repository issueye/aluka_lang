# AGENTS.md — aluka 仓库工作区总览

> 面向 AI 代码助手与新人。本仓库含两个平级子项目，**活跃的工程纪律与开发流程
> 全部收敛在 Rust 侧**，入口：[aluka_r/AGENTS.md](./aluka_r/AGENTS.md)（动手前必读）。

| 子项目 | 角色 | 说明 |
|---|---|---|
| [`aluka_r/`](./aluka_r/) | **活跃开发**（Rust 重实现） | 工程纪律、内置库规范、GC/ISA 契约、每日 TODO 与证据体系 → [aluka_r/AGENTS.md](./aluka_r/AGENTS.md) |
| [`aluka_g/`](./aluka_g/) | 只读参考（Go 版） | 当前唯一正式产物；语义 oracle 与对拍基线，**不改其源码** → [aluka_g/AGENTS.md](./aluka_g/AGENTS.md) |
| [`docs/`](./docs/) | 两实现共享的跨实现文档 | ADR、架构决策（GC 选型见 `docs/adr/0002-aluka-r-gc-selection.md`） |

共享文档（`.work/TODO/`、`.work/evidence/`）挂在其父目录；每日工作流
（开工先在当日 TODO 登记任务列表、复审后才许本地提交）的完整定义见
[aluka_r/AGENTS.md](./aluka_r/AGENTS.md) 的「开发流程」与「工作流硬约束」两节，
**对全仓库生效**。
