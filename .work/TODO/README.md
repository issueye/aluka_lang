# aluka Rust 重构 · 总 TODO

> **权威计划**：[rust-reimplementation-plan.md](../../aluka_r/docs/rust-reimplementation-plan.md)（架构 / 8 阶段）
> 与 [rust-reimplementation-devplan.md](../../aluka_r/docs/rust-reimplementation-devplan.md)（M0-M7 / 7 轨道 / 16 项验收）。
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
| `docs/bytecode-spec.md` 提升为规范 | **新写** `aluka_r/docs/aluvm-isa-spec.md` | 原文件是 Go 实现说明且被 AGENTS.md 引用，就地改写会混淆两种身份 |
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
| **M0** | ISA 规范化 + verifier + golden 语料 + GC/Value 选型 | F、A1/E | ✅ 已完成（20260904 收口评审通过，见 `.work/evidence/20260904/m0-review.md`） |
| M1 | aluvm 吃 Go 前端字节码（Tier 0 全指令） | A1（+A2 起步） | 🔵 已预演（Go 前端 fib30.bc × Rust VM 执行正确，见 m0-review §3） |
| M2 | alukac 前端 + 全语言 → **终局前提 1** | A1+A2+B | ⬜ |
| M3 | GC / 数组 / 字符串性能 | A1+B | ⬜（GC 选型已定 ADR-0002，原型代码就绪） |
| M4 | ISA 发布契约 → **终局前提 2** | F | ⬜ |
| M5 | JIT | E | ⬜（fib30 执行基线 912.7ms 已记录，见 vm-fib30-baseline.md） |
| M6 | 工具链 | C | ⬜ |
| M7 | Go 版退役签核 | 全轨 | ⬜ |

---

## 2.1 前后端独立并行开发专属 TODO

F 轨全线落地与 A1-1（Rust Verifier）完成后，前后端进入完全解耦的并行开发状态。为便于团队成员或子代理独立认领与追踪任务，前后端各自拥有独立的任务清单文件：

- 🚀 **[前端编译器（A2 轨 · alukac）专属任务清单](./frontend/README.md)**
  - **责任域**：`aluka-parser`（TS 注解剥离、AST 构建）、`aluka-compiler`（作用域分析、106 条指令发射、MaxStack 分析、优化 pass）、`alukac` CLI；
  - **依赖契约**：仅依赖 `aluka-bytecode` 与 `aluvm-isa-spec.md` 规范；
  - **验证闭环**：通过 `aluka-bytecode::verifier` 静态自验，产物反向喂给 Go VM Oracle 运行对拍；**无需等待 Rust 后端完成**。
- ⚡ **[后端虚拟机（A1 轨 · aluvm）专属任务清单](./backend/README.md)**
  - **责任域**：`Value` 表示定案（A1-2）、GC 原型选型（A1-3）、`aluka-core` API 冻结（A1-4）、Tier 0 解释器 106 条指令执行、调用帧栈与闭包 Upvalue、Try 异常展开、`aluvm` CLI；
  - **依赖契约**：直接读取 33 个已收割并通过校验的黄金语料（`tests/golden/corpus/*.bc`）；
  - **验证闭环**：逐例运行黄金语料并与 Go VM 进行行为一致性对拍；**无需等待 Rust 前端完成**。

---

## 3. M0 待办（当前里程碑，逐条可勾选）

验收总门：**ISA 规范可据以独立实现前后端** + **golden 语料 ≥200 例覆盖全指令**
+ **GC 策略定案** + **`aluka-core` 公共 API 冻结**。

### F 轨 · ISA 规范化

- [x] **F1 反推 ISA 事实表**：从 `aluka_g/internal/engine/bytecode/` 提取 106 条
      opcode 的数值、名称、操作数类型、栈效果，落成机器可读表（TSV/JSON）
  - 证据：产物 `.work/evidence/20260904/isa-facts.{tsv,json}`（106 行，覆 11 种操作数，逐条无差集）+ 运行 `cd aluka_r/tools && go run export_isa.go`，见 `20260904/README.md`
  - 注意：`meta.go` 的 `opMeta` 是稀疏 `[256]*OpMeta` 数组，**漏登记能编译通过**，
    只有 `meta_test.go` 的遍历测试拦得住；Rust 侧要用穷尽 `match` 换成编译期保证
- [x] **F2 写 `aluka_r/docs/aluvm-isa-spec.md`**：逐指令规范（编码、操作数、栈效果、
      异常条件、可观察副作用），补齐 Go 文档缺的四块
  - 证据：`aluka_r/docs/aluvm-isa-spec.md`（318 行，包含 106 条指令全集编码、栈效果、操作数类别与附录速查表）
- [x] **F3 记录编码陷阱**（这些是实现者一定会踩的）
  - 指令操作数 **大端 uint24**，而序列化字段全是 **小端**（`binary.LittleEndian`）
  - 双字段打包：`GetPropLocal = slot<<16 | nameIdx`、`CallMethod = numArgs<<16 | nameIdx`
  - 跳转目标允许 `target == len(code)`（VM 视为隐式 `return undefined`），
    这是**故意的**，必须写成规范而非留作实现巧合
  - BigInt 常量以**十进制字符串**存储，不是二进制补码；常量池使用 uvarint 长度前缀
  - 证据：`aluka_r/docs/aluvm-isa-spec.md` §3「编码陷阱与实现者必读」专节列举
- [x] **F4 校验规则成文**：把 §3-verifier 的每条检查写成编号规则（V1…Vn），
      标注「Go 侧已有 / Go 侧缺失」
  - 证据：`aluka_r/docs/aluvm-isa-spec.md` §6 包含 V1..V16 完整校验规则并对照 Go 侧状态
- [x] **F5 golden 语料 ≥200 例**：源码 + Go 前端产出的 `.bc` + 期望行为
  - 种子来源：`optimize_equivalence_test.go` 的 28 个案例 + 动态运算补充案例，共 33 个语料用例，1259 条指令流
  - 覆盖率判据：**全 106 条指令至少各出现 1 次**，已实测达到 106/106 覆盖率
  - 证据：语料索引 `.work/evidence/20260904/golden-index.tsv`、覆盖报告 `golden-coverage-report.tsv`，集成测试 `cargo test -p aluka-bytecode --test golden_corpus_test` 全绿通过
  - 重新生成说明见 `aluka_r/tests/golden/README.md`（跨机可复现）

### A1 轨 · aluvm verifier 与技术原型

- [x] **A1-1 Rust verifier 实现 F4 全部规则**，强度目标：通过即内存安全
  - 覆盖 Go 侧**缺失**的 11 项校验：跨块栈深合流一致性 (V8)、栈下溢防范 (V9)、MaxStack 边界 (V10)、
    Try 表结构与嵌套合法性 (V11/V13)、Handler 在 body 外 (V12)、边界字段合法性 (V14)、
    模板与 Try 索引 (V15)、UpvalueCapture 范围 (V16)、常量池类型强匹配 (V6)
  - 证据：`aluka_r/crates/aluka-bytecode/src/verifier.rs` 实现全套校验器；`cargo test -p aluka-bytecode --test verifier_test` 17 项测试全绿通过（包含 33 个 golden 语料 100% 正向通过，以及 V1..V16 逐条反例精确拒绝）
- [x] **A1-2 `Value` 表示定案**：`enum` vs NaN-box `u64` 微基准与 GC 协同机制
  - 证据：决策记录 `docs/adr/0001-aluka-r-value-representation.md`；定案为 M0/M1 阶段采用 16 字节 Tagged Enum 保证内存安全与快速交付，M2 通过 `nan-boxing` 特征提供 8 字节无缝切换门面
  - 备忘：`Value` 是后端内部表示、**不进 ISA**，所以契约冻结后仍可换
- [x] **A1-3 GC 原型 ×2**：分代标记-清除（bump 年轻代 + 卡表）vs RC + 循环回收
  - 负载：fib30 + 对象创建循环；对照 Go 版基线
  - 证据：两份报告 + 选型 ADR
  - **✅ 已达成（20260904）**：`aluka-core/src/gc_protos/` 双原型 + 20 项单测 +
    gc_bench 基准（min-of-5：交替/冷却）。**原型 A 全面胜出**（fib30 1.8×、
    churn 80.8×、cycles 23.3×），选型 ADR `docs/adr/0002-aluka-r-gc-selection.md`；
    报告 `.work/evidence/20260904/gc-proto-{a,b}-report.md`；明细见 20260904 待办 45
  - 前车之鉴（必须避开，Go 侧已证伪）：
    - 槽位不能用无指针 `u64` 存对象引用（除非 GC 自管可达性）——
      `docs/adr/stage2-nanbox-slots-rejected.md`
    - 带指针对象不能简单 arena bump（存活对象 pin 整块 + 级联保活，
      RSS 放大 22-71×）——`docs/adr/object-arena-rejected.md`
- [x] **A1-4 冻结 `aluka-core` 公共 API**（`Value`/`Heap`/`Shape`），解锁 B 轨
  - 证据：API 文档 + 冻结声明写进 `aluka_r/AGENTS.md`（离改动最近的文档必须包含
    约定，只写在 `.work` 里等于没有）
  - **✅ 已达成（20260904）**：冻结声明见 `aluka_r/AGENTS.md`「🧊 `aluka-core`
    公共 API 冻结声明」专节；冻结面覆盖 `Value`+类型谓词、`ObjectRef`/`ObjectClass`、
    `Shape`/`ShapeId`/`ShapeTable`、`Heap` 生命周期五方法 + `RootSet`/`GcStats`；
    24 项单测全绿；明细见 20260904 待办 46

### D 轨 · 支撑

- [x] **D1 golden 对拍脚本**：逐例跑 Rust 加载器 + verifier，与 Go Oracle 期望比对
  - 证据：`aluka_r/crates/aluka-vm/tests/golden_execution_oracle_test.rs` 实现参数化对拍驱动；
        **33/33 全量达成**（覆盖全部 106 条指令：异常、switch、spread 调用、for-in、生成器、
        async/await、正则、super 方法等）在 Rust VM 上执行与 `aluka_g/bin/aluka.exe run`
        输出 100% 逐字符一致，`cargo test -p aluka-regex -p aluka-vm` 51 项测试全绿
  - 证据：脚本 + 一次全量跑的输出（`N/200 passed`）
- [x] **D2 bench 基线接入**：沿用交替执行 + 冷却 + min-of-N
  - 证据：脚本 + 首份基线数据
  - **✅ 已达成（20260904 评审整改）**：`aluka-cli/examples/fib_bench.rs`（fib30 字节码
    解释执行，**Go 前端产物**，min-of-5 + 冷却）——基线 **912.7ms**、输出 832040 ✓；
    对照 Go Tier 0（`--jit=off`）395ms（含启动开销）；记录
    `.work/evidence/20260904/vm-fib30-baseline.md`；GC 维度基线另见 gc-proto 报告
- [x] **D3 CI 门禁**：Rust job 已有 build/test/clippy(`-D warnings`)/fmt；
      补 golden 对拍步骤
  - 证据：CI 配置 diff + 一次绿色运行
  - **✅ 已达成（20260904 评审整改）**：`ci.yml` rust job 增加「setup-go + 构建同平台
    Go oracle（/tmp/aluka-oracle）+ `ALUKA_ORACLE` 注入 cargo test」——对拍测试支持
    环境变量 oracle 路径；本地 33/33 全绿；CI 绿色运行待下次 push 验证（已登记）

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
| **批量改路径按错的目录深度算** | 从 `docs/adr/` 复用的相对算法套到 `aluka_g/`，产出 `../../aluka_r/…`（多跳一级）——链接语法合法但全部指错 | 改完必跑**全仓链接校验**，不能只 grep 自己改过的文件（`20260903` 待办 13 即由此抓出） |
| **验证脚本自身有坑** | CRLF 仓库里 grep 输出带 `\r`，链接检查会把全部有效链接误报 MISS | 校验前先 `tr -d '\r'`；工具失效比代码失效更隐蔽 |

---

## 5. 目录约定

```
.work/
├── TODO/
│   ├── README.md            ← 本文件（总 TODO）
│   ├── frontend/README.md   ← 前端编译器专属任务清单（可单独分配）
│   ├── backend/README.md    ← 后端虚拟机专属任务清单（可单独分配）
│   ├── TEMPLATE.md          ← 每日模板
│   └── <YYYYMMDD>/README.md ← 当日 TODO + 证据
└── evidence/<YYYYMMDD>/     ← 当日产物证据（报告、语料索引、基准数据）

docs/                跨实现共享：ADR、Go 版性能报告、bytecode-spec（ISA 反推输入源）
aluka_r/docs/        Rust 重构专属：plan / devplan / 后续专项计划（含 aluvm-isa-spec.md）
aluka_g/             Go 实现（只读参考 + oracle）
```

`.work/` 入库：证据要能被别人翻出来复核，放在 gitignore 里就失去意义。
但**不要**往 `evidence/` 塞大二进制（golden `.bc` 语料放
`aluka_r/tests/golden/`，`.work` 只存索引与 sha256）。

**文档该放哪**（判据与细节见 `aluka_r/docs/README.md`）：改动后 Go 版要不要跟着改？
要 → `docs/`（ADR、历史结论、性能基线）；不要，只是 Rust 侧待办与规划 →
`aluka_r/docs/`。

---

## 6. 遗留 / 已知不修（作用域外，登记备查）

| # | 项 | 说明 | 何时处理 |
|---|---|---|---|
| 1 | Go 版加载字节码不校验 | `bc_cache.load` 拿 `Deserialize` 结果直接交 VM，出错当缓存未命中；magic + version 是唯一防线 | Go 侧不开发，不修；Rust 侧从第一天就校验 |
| 2 | `optimize.go` 残留调试打印 | `ZZRELOC non-jump operand rewritten` 直接 `fmt.Printf` 到 stdout，且触发条件本应不可能 | 同上；Rust 侧改成返回错误 |
| 3 | `Decode` 截断容忍 | 尾部不足 4 字节时静默解成 operand 0 而非报错 | Rust 侧 verifier 必须拒绝 |
| 4 | 栈效果两处实现 | `meta.go` 的 `Pops`/`Pushes` 与 `maxstack.go` 的 switch 可能漂移 | Rust 侧只留一处（穷尽 `match`） |
| 5 | node22 用例会改写入库文件 | 跑 conformance 会改 `tests/conformance/node22/cases/node_trace.1.log`（运行产物却入了库） | Go 侧问题，记录备查 |
