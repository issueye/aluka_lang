# aluka_r/docs

Rust 重构的**计划与执行文档**目录。

## 当前内容

| 文件 | 作用 |
|------|------|
| `rust-reimplementation-plan.md` | 功能全景、alukac/aluvm 架构、8 阶段路线、Go 版退役门禁 |
| `rust-reimplementation-devplan.md` | M0-M7 里程碑、7 条并行轨道、16 项验收指标、Gantt |

后续 Rust 侧专项计划（ISA 规范 `aluvm-isa-spec.md`、GC 选型报告等）也落这里。

## 与仓库根 `docs/` 的边界

本目录放**"要为它排期干活"的文档**：重构计划、里程碑、专项设计。
`../../docs/` 放**已经定论的跨实现记录**：

| 留在 `../../docs/` | 为什么不留在这里 |
|---|---|
| `docs/adr/*.md` | ADR 是架构决策记录。`jvm-style-bytecode-architecture.md` 的结论直接约束 Go 版（退役门禁、字节码契约），Go 侧也要读；ADR 流程本身也在那边 |
| `docs/adr/{object-arena,stage2-nanbox-slots}-rejected.md` 等 | 这是**Go 侧实验证伪的历史结论**，作者与主语都是 Go；本目录的文档只**引用**它们，不收录 |
| `docs/bytecode-spec.md` | 描述 Go 现有实现，是反推 ISA 的输入源，不是 Rust 的产出物 |
| `docs/performance-report-*.md` | Go 版性能基线报告 |

一句话判据：**这份文档改动后，Go 版要不要跟着改？** 要 → `../../docs/`；
不要（只是 Rust 侧的待办与规划）→ 这里。

## 引用写法约定

跨目录引用统一用**仓库根相对**的纯文本路径，不依赖所在目录深度：

```
docs/adr/jvm-style-bytecode-architecture.md      ← 共享记录
aluka_r/docs/rust-reimplementation-plan.md       ← 本目录
aluka_g/internal/engine/bytecode/                ← Go 实现
```

同目录互引可直接写文件名（如 plan 引 devplan）。markdown 链接则按文件实际位置
写相对路径（`../docs/...`、`../../docs/...`）——只有链接需要"能点"，纯文本引用
追求的是"在任何文件里长得一样、好 grep"。
