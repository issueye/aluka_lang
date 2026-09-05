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
| T1 | 解释器线程大栈：fib(25) 探针对拍 Go（75025） |[x] |
| T2 | promise.then 返回新 promise（链式/异常传播/then 返回 promise 采纳） |[x] |
| T3 | JSON.parse（成功路径 + 错误路径对拍） |[x] |
| T4 | 自定义可迭代对象 for...of（Symbol.iterator 接入 GetIterator） |[x] |
| T5 | alukac 非 ASCII 字面量 mojibake 修复 |[x] |
| T6 | 写屏障（老写新变异点清单化）+ minor 生产启用 + 压力回归 |[x] |
| T7 | aluka-core Heap 收口：ADR 0002 增补落地偏差决定 |[x] |
| T8 | async_hooks.AsyncResource.bind 静态注册修复 |[x] |
| T9 | http Agent keepAlive 连接池（同源复用 TcpStream） |[x] |
| T10 | TLS rustls + rustls-rustcrypto 评估 spike（环回 HTTPS echo） |[x] |
| T11 | 其余第三梯队重项：设计草图 + 解锁条件登记（evidence 报告） |[x] |
| T12 | 全量门禁 + 复审 + 提交（每里程碑一提交） |[x] |

### 会话 2（梯队推进）证据

- 命令证据：`cargo test` → 56 目标全绿（core_semantics 21、gc 11、tls_spike 1 等）；
  `cargo clippy --all-targets -D warnings` → 0 错误；`cargo fmt --all --check` 通过。
- 提交证据：`03419fd`（第一梯队 11 文件 +845）、`ef8cf16`（第二梯队 6 文件 +147）、
  `5a5ef46`（第三梯队 9 文件 +1098）。
- 详见 `.work/evidence/20260905/tier3-report.md`（T8-T11 详情与缓办项设计草图）、
  gc_stress 在 minor+major 混合下抓出数组 push/spread 漏屏障悬垂并修复。
- 复审记录：各里程碑提交前逐一验证探针双引擎一致 + 全量门禁；分支
  feat/engine-tiers 合并回 feat/rust-skeleton（快进）后重跑门禁。



---



---

## 会话 4：M5 推进（JIT，子集先行策略）

### 目标（可判定完成态）

1. 新 crate `aluka-jit`：字节码算术子集 → Cranelift 纯 Rust 后端 →
   机器码执行；ADR 0005 固化子集先行与覆盖矩阵分期。
2. jitdiff：生成式差分 ≥3000 例（解释器 vs JIT 零失配）。
3. 性能基线：热循环 JIT vs 解释器 vs node（方法学：交替+冷却+min-of-5），
   propAccess/callOverhead 对拍表登记（子集外部分记入矩阵后续）。

### 待办清单

| # | 待办 | 状态 |
|---|------|------|
| M5-1 | ADR 0005 策略 + aluka-jit crate 骨架（Cranelift 依赖） |[x] |
| M5-2 | 子集编译器：算术/局部/比较/if/循环 → Cranelift IR → 执行 |[x] |
| M5-3 | Quick IR：JIT 前常量折叠 peephole |[x] |
| M5-4 | jitdiff 生成式差分 ≥3000 例零失配 |[x] |
| M5-5 | 性能基线（JIT vs 解释器 vs node,方法学）+ 对拍表 |[x] |
| M5-6 | 全量门禁 + 提交 + 合并主线推送 |[x] |

## 会话 3：M4 推进（ISA 发布契约）

### 目标（可判定完成态）

1. `.aluc`/`.alua` 格式发布（ALUKACC1 容器 + 调试段 + 剥离选项），aluvm
   魔数自动嗅探，alukac `--format`/`--strip-debug`。
2. eval/new Function、Function.prototype.toString、兼容窗口与 ISA 版本
   递增权限的策略 ADR 落盘。
3. **验收核心**：玩具 Lisp 前端（新 crate `alisp`，只依赖 aluka-bytecode
   公共 API）产出 `.aluc`，aluvm 跑通——全程零改后端（git 证明：DSL 提交
   不触碰 aluka-vm）。

### 待办清单

| # | 待办 | 状态 |
|---|------|------|
| M4-1 | .aluc/.alua 规范文档 + aluka-bytecode 序列化/反序列化 + strip |[x] |
| M4-2 | aluvm 嗅探 + alukac --format/--strip-debug |[x] |
| M4-3 | 拆二进制现状确认与登记 |[x] |
| M4-4 | 策略 ADR（eval/toString/兼容窗口/版本权限） |[x] |
| M4-5 | alisp crate（sexp 解析 + ISA 代码生成 + .aluc 输出） |[x] |
| M4-6 | 验收 e2e + 后端零改证明 + 全量门禁 |[x] |
| M4-7 | 提交 + 合并主线 + 推送 |[x] |

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

### 会话 4（M5）证据

- 命令证据：`cargo test` → 64 目标全绿（含 jitdiff 3200 例、minimal 4、
  peephole 5、jitbench 1）；clippy -D warnings 0 错误；fmt 通过。
- **jitdiff**：3200 例生成式差分，解释器 vs JIT f64 **逐位相等**，0 失配。
- **性能基线**（交替+冷却 50ms+min-of-5，hot_loop 20 万轮，结果逐位一致）：
  解释器 14.02ms / JIT 0.34ms（**41.6× 加速**）/ node 预热 0.095ms。
- **如实登记未达成项**：propAccess/callOverhead ≤ node 5× 属 J2（PIC+调用
  约定）范围，J1 子集不含属性访问与调用，对拍表记现状与路径（见
  `.work/evidence/20260905/m5-report.md`）；JIT 尚未挂进解释器热点触发。

### 会话 3（M4）证据

- 命令证据：`cargo test` → 59 目标全绿；clippy -D warnings 0 错误；fmt 通过。
- 提交证据：`84a80e6` 契约（6 文件 +437：spec/aluc.rs/aluvm 嗅探/alukac 选项/
  ADR 0003）、`31a42d6` alisp 前端（7 文件 +894，**后端 crate 触碰计数 0**）。
- 验收：demo.lisp → alisp 产 .aluc → aluvm 输出 5/120/hello lisp/42；
  contract_e2e 3 用例 + aluc 容器 4 单测。
- 复审记录：alisp 提交 diff --name-only 过滤后端 crate 计 0（零改后端证明）；
  合并主线后全量门禁重跑绿。

