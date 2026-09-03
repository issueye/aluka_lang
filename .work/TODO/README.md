# aluka Rust 重构 · 总 TODO

> **权威计划**：[rust-reimplementation-plan.md](../../docs/rust-reimplementation-plan.md)（架构 / 8 阶段）
> 与 [rust-reimplementation-devplan.md](../../docs/rust-reimplementation-devplan.md)（M0-M7 / 7 轨道 / 16 项验收）。
> 本文件是**执行视图**：把计划的验收项拆成可勾选待办，每项配一条**可复现的证据**。
> 计划变了要回头同步这里；这里的勾选不改变计划的验收标准。
>
> 每日 TODO 见 `.work/TODO/<YYYYMMDD>/README.md`，模板见 [TEMPLATE.md](./TEMPLATE.md)。

---

## 0. 当前作用域（2026-09-03 定）

**Rust 侧（`aluka_r/`）是唯一开发方向。Go 侧（`aluka_g/`）不做开发**，只保留两种用途：

1. **只读参考**：读 `internal/engine/bytecode/{opcodes,meta,serialize}.go` 反推 ISA 事实；
2. **运行时 oracle**：跑现成的 `aluka_g/bin/aluka.exe` 产出字节码与行为基线。

这条边界**改变了 devplan M0 的形态**（原计划把 verifier 强化放在 Go 侧），必须记清：

| devplan 原定 | 本作用域下的做法 | 理由 |
|---|---|---|
| Go 版 `ValidateModule` 强化到"通过即安全" | **只在 Rust 侧实现**，且从第一天就是完整强度 | 不改 Go 源码；Rust 加载器成为唯一安全边界 |
| `docs/bytecode-spec.md` 提升为规范 | **新写** `docs/aluvm-isa-spec.md` | 原文件是 Go 实现说明且被 AGENTS.md 引用，就地改写会混淆两种身份 |
| golden 语料由 Go 前端产出 | **运行** Go 二进制收割 `.bc`，不改其源码 | `.aluka-cache/*.bc` 就是 `ALUKABC1` 序列化模块 |

代价要说明：Go 版仍会加载未校验的字节码（`bc_cache.load` 拿到 `Deserialize` 结果
直接交给 VM，出错只当缓存未命中）。此缺陷本作用域内**不修**，登记在 §6。

---

## 1. 证据规则（不满足即不许勾选）

「达成证据」不是自我描述，是**别人能重跑并看到同一结果的东西**。三类之一：

| 类型 | 形式 | 例 |
|---|---|---|
| **命令证据** | 命令 + 期望输出关键行（不贴全量日志） | `cargo test -p aluka-bytecode` → `42 passed; 0 failed` |
| **产物证据** | 入库文件路径 + 校验方式 | `.work/evidence/20260903/golden-index.tsv` + 行数 + sha256 |
| **提交证据** | commit hash + 一句话改了什么 | `23d8866` 目录重构，1568 个 100% rename |

四条硬规则：

- **不许用"应该可以""看起来对了"结项**。跑不通就写「未达成 + 卡在哪」，这比假勾选有用。
- **性能类必须写方法学**：交替执行 + 冷却 + min-of-N。Go 侧踩过持续负载 ~5s 后
  降频到 ~20% 的坑，当时差点误判成 JIT 回归；没有方法学的数字直接作废。
- **对拍类必须写清 oracle 是谁**。「Tier 0 是唯一 oracle」的纪律不许松。
- **性能与正确性冲突时正确性优先**，且冲突本身要记进当日 TODO 的「偏差」栏。

---

## 2. 里程碑总览（映射 devplan §2）

进度只在里程碑收口时更新，日常进展落在每日 TODO。

| 里程碑 | 内容 | 轨道 | 状态 |
|---|---|---|---|
| **M-1** | 骨架（11 crate、端到端最小链路、模块注册表看板） | — | ✅ 已完成（`c2a2e66`、`23d8866`） |
| **M0** | ISA 规范化 + verifier + golden 语料 + GC/Value 选型 | F、A1/E | 🔵 进行中 |
| M1 | aluvm 吃 Go 前端字节码（Tier 0 全指令） | A1（+A2 起步） | ⬜ 未开始 |
| M2 | alukac 前端 + 全语言 → **终局前提 1** | A1+A2+B | ⬜ |
| M3 | GC / 数组 / 字符串性能 | A1+B | ⬜ |
| M4 | ISA 发布契约 → **终局前提 2** | F | ⬜ |
| M5 | JIT | E | ⬜ |
| M6 | 工具链 | C | ⬜ |
| M7 | Go 版退役签核 | 全轨 | ⬜ |

---

## 3. M0 待办（当前里程碑，逐条可勾选）

验收总门：**ISA 规范可据以独立实现前后端** + **golden 语料 ≥200 例覆盖全指令**
+ **GC 策略定案** + **`aluka-core` 公共 API 冻结**。

### F 轨 · ISA 规范化

- [ ] **F1 反推 ISA 事实表**：从 `aluka_g/internal/engine/bytecode/` 提取 106 条
      opcode 的数值、名称、操作数类型、栈效果，落成机器可读表（TSV/JSON）
  - 证据：表文件 + 行数 = 106 + 与 `meta.go` 逐条 diff 为空
  - 注意：`meta.go` 的 `opMeta` 是稀疏 `[256]*OpMeta` 数组，**漏登记能编译通过**，
    只有 `meta_test.go` 的遍历测试拦得住；Rust 侧要用穷尽 `match` 换成编译期保证
- [ ] **F2 写 `docs/aluvm-isa-spec.md`**：逐指令规范（编码、操作数、栈效果、
      异常条件、可观察副作用），补齐 Go 文档缺的四块
  - 现状缺口（已核实）：无逐指令表（106 条里文档只举例约 30 条）、无 opcode
    数值、无异常语义、无强制类型转换语义、无完整文件布局、无 verifier 契约
  - 证据：规范文件 + 「第三方可实现」自检清单逐项打勾
- [ ] **F3 记录编码陷阱**（这些是实现者一定会踩的）
  - 指令操作数 **大端 uint24**，而序列化字段全是 **小端**（`binary.LittleEndian`）
  - 双字段打包：`GetPropLocal = slot<<16 | nameIdx`、`CallMethod = numArgs<<16 | nameIdx`
  - 跳转目标允许 `target == len(code)`（VM 视为隐式 `return undefined`），
    这是**故意的**，必须写成规范而非留作实现巧合
  - BigInt 常量以**十进制字符串**存储，不是二进制补码
  - 证据：规范中有「陷阱」小节 + 每条附 Go 侧出处行号
- [ ] **F4 校验规则成文**：把 §3-verifier 的每条检查写成编号规则（V1…Vn），
      标注「Go 侧已有 / Go 侧缺失」
  - 证据：规则表 + 与 F5 的测试用例一一对应（每条规则至少 1 个反例）
- [ ] **F5 golden 语料 ≥200 例**：源码 + Go 前端产出的 `.bc` + 期望行为
  - 种子来源：`optimize_equivalence_test.go` 的 28 个案例（现成、覆盖
    const 折叠 / 死代码 / try-finally / 生成器 / async / 可选链 / class）
  - 覆盖率判据：**全 106 条指令至少各出现 1 次**，用 F1 的表统计
  - 证据：语料索引文件（源码路径、`.bc` sha256、指令覆盖计数）+ 覆盖率报告显示 106/106
  - 风险：`.bc` 缓存键含绝对路径与 mtime，**不可直接复现**；语料必须记录
    「怎么重新生成」而不是只存二进制

### A1 轨 · aluvm verifier 与技术原型

- [ ] **A1-1 Rust verifier 实现 F4 全部规则**，强度目标：通过即内存安全
  - 必须覆盖 Go 侧**缺失**的部分（已核实缺失）：跨块栈深合流一致性、栈下溢、
    try 表结构合法性（`StartPC<EndPC`、handler 在 body 外、区域正确嵌套）、
    `EndPC`/`CatchEndPC`/`FinallyEndPC` 三个边界字段（Go 侧完全未校验）、
    函数/类模板索引、try 表索引、`UpvalueCapture` 自身的 index 范围、
    常量池条目的**类型**正确性（Go 侧只查下标不查类型）
  - 可借的结构模板：`aluka_g/internal/engine/jit/ir_verify.go` 已经在做
    跨边深度合流 + 下溢拒绝 + 目标越界拒绝，是现成参照
  - 证据：`cargo test -p aluka-bytecode` 全绿 + 每条 V 规则有 1 个拒绝用例
- [ ] **A1-2 `Value` 表示定案**：`enum` vs NaN-box `u64` 微基准（复制/算数/分发）
  - 证据：基准报告 + 决策记录（写进 `docs/adr/`）
  - 备忘：`Value` 是后端内部表示、**不进 ISA**，所以契约冻结后仍可换
- [ ] **A1-3 GC 原型 ×2**：分代标记-清除（bump 年轻代 + 卡表）vs RC + 循环回收
  - 负载：fib30 + 对象创建循环；对照 Go 版基线
  - 证据：两份报告 + 选型 ADR
  - 前车之鉴（必须避开，Go 侧已证伪）：
    - 槽位不能用无指针 `u64` 存对象引用（除非 GC 自管可达性）——
      `docs/adr/stage2-nanbox-slots-rejected.md`
    - 带指针对象不能简单 arena bump（存活对象 pin 整块 + 级联保活，
      RSS 放大 22-71×）——`docs/adr/object-arena-rejected.md`
- [ ] **A1-4 冻结 `aluka-core` 公共 API**（`Value`/`Heap`/`Shape`），解锁 B 轨
  - 证据：API 文档 + 冻结声明写进 `aluka_r/AGENTS.md`（离改动最近的文档必须包含
    约定，只写在 `.work` 里等于没有）

### D 轨 · 支撑

- [ ] **D1 golden 对拍脚本**：逐例跑 Rust 加载器 + verifier，与期望比对
  - 证据：脚本 + 一次全量跑的输出（`N/200 passed`）
- [ ] **D2 bench 基线接入**：沿用交替执行 + 冷却 + min-of-N
  - 证据：脚本 + 首份基线数据
- [ ] **D3 CI 门禁**：Rust job 已有 build/test/clippy(`-D warnings`)/fmt；
      补 golden 对拍步骤
  - 证据：CI 配置 diff + 一次绿色运行

---

## 4. 反复出现的坑（每次动手前扫一眼）

来自 Go 侧的真实教训，Rust 侧同样适用：

| 坑 | 表现 | 对策 |
|---|---|---|
| **热降频** | 持续负载 ~5s 后性能掉到 ~20%，看着像代码回归 | 交替执行 + 冷却 + min-of-N；用纯计算对照程序确认是环境而非代码 |
| **基准被优化掉** | 循环结果没人用 → 被判无副作用整段消除，首轮 6ms 后续 101ms | 结果写进 sink 数组 |
| **时钟分辨率** | Windows 上 Go `time.Since` ≈546µs，20 万次采样里 19.99 万个零差值 | 把迭代量放大到单次读数 >50ms |
| **手算期望值** | 差分用例的期望值靠脑算，错了却以为是引擎 bug | 先读 Tier 0 实际输出，再用 Node 交叉验证 |
| **元数据漏登记** | 稀疏数组漏一项照样编译通过（Go 侧 `opMeta: [256]*OpMeta` 实测如此） | 用穷尽 `match`，让漏登记变成编译错误。**已实测**：故意加一个未登记的 `Op` 变体 → `error[E0004]: not covered`（见 `20260903` §3） |
| **验证脚本自身有坑** | CRLF 仓库里 grep 输出带 `\r`，链接检查会把全部有效链接误报 MISS | 校验前先 `tr -d '\r'`；工具失效比代码失效更隐蔽 |

---

## 5. 目录约定

```
.work/
├── TODO/
│   ├── README.md            ← 本文件（总 TODO）
│   ├── TEMPLATE.md          ← 每日模板
│   └── <YYYYMMDD>/README.md ← 当日 TODO + 证据
└── evidence/<YYYYMMDD>/     ← 当日产物证据（报告、语料索引、基准数据）
```

`.work/` 入库：证据要能被别人翻出来复核，放在 gitignore 里就失去意义。
但**不要**往 `evidence/` 塞大二进制（golden `.bc` 语料放
`aluka_r/tests/golden/`，`.work` 只存索引与 sha256）。

---

## 6. 遗留 / 已知不修（作用域外，登记备查）

| # | 项 | 说明 | 何时处理 |
|---|---|---|---|
| 1 | Go 版加载字节码不校验 | `bc_cache.load` 拿 `Deserialize` 结果直接交 VM，出错当缓存未命中；magic + version 是唯一防线 | Go 侧不开发，不修；Rust 侧从第一天就校验 |
| 2 | `optimize.go` 残留调试打印 | `ZZRELOC non-jump operand rewritten` 直接 `fmt.Printf` 到 stdout，且触发条件本应不可能 | 同上；Rust 侧改成返回错误 |
| 3 | `Decode` 截断容忍 | 尾部不足 4 字节时静默解成 operand 0 而非报错 | Rust 侧 verifier 必须拒绝 |
| 4 | 栈效果两处实现 | `meta.go` 的 `Pops`/`Pushes` 与 `maxstack.go` 的 switch 可能漂移 | Rust 侧只留一处（穷尽 `match`） |
| 5 | node22 用例会改写入库文件 | 跑 conformance 会改 `tests/conformance/node22/cases/node_trace.1.log`（运行产物却入了库） | Go 侧问题，记录备查 |
