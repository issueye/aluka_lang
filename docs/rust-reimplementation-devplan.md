# aluka Rust 重构——详细开发计划（MVP 里程碑 + 并行轨道）

> 状态：规划稿（v0.2）｜日期：2026-09-03｜依赖：docs/rust-reimplementation-plan.md（架构与阶段）
> 原则：strangler 逐子系统替换；conformance 套件为唯一行为仲裁；每里程碑有可演示交付物

---

## 0. 总览

- 4 条并行轨道 + 2 条支撑流，公共前置是 **M0 技术原型**（GC 选型——一切并行的前提）。
- MVP 定义分三档（§3），每档可独立对外演示。
- 假设人力：4-6 名 Rust 工程师 + 现有作者做仲裁/Go 侧对接；可增减按"轨道×人月"折算。
- 月 = 有效 4 周；里程碑验收 = 演示 + conformance 数字 + 性能门，不达标不进入下一里程碑（可裁剪范围但不可裁剪验收）。

---

## 1. 并行轨道与依赖矩阵

### 轨道总览

| 轨道 | 内容 | 依赖 | 并行性 |
|------|------|------|--------|
| **A 引擎核心** | Value/Shape/GC/VM/字节码/JIT（§§4 的 M0-M5 引擎侧） | 无（自举最早） | 主链，其他轨道对齐它的 Value API |
| **B 内置库移植** | node:* 59 模块 + Web API + Aluka API 的 Rust 实现 | 轨道 A 的 `Value`/`Context` API（冻结版） | 与 A 并行：先 FFI 到 Go builtin 过渡，API 冻结后逐模块 Rust 化 |
| **C 工具链** | pkgmanager/bundler/Vue SFC/CLI | 引擎 API（`Eval` 级足够） | 完全独立；早期即可用 Go CLI 壳 + FFI 驱动 Rust 库 |
| **D 测试与一致性** | conformance 迁移、jitdiff/fuzz 移植、bench 基线 | 随 A/B 进展持续 | 恒并行，串起各轨道验收 |
| **E JIT 后端** | Quick IR → Cranelift / 自研 amd64 | A 的 VM 稳定 + 字节码冻结 | 晚启动（M4），可与 D 并行 |

### 依赖矩阵（× = 阻塞关系）

```
        A      B      C      D      E
A       —     API    Eval   跑      字节码
B       ×A    —      —      跑      跑
C       ×A    —      —      跑      —
D       ×A/B  ×A/B   ×A/B   —       —
E       ×A    —      —      跑      —
```

---

## 2. 里程碑分解（M0→M6，含任务清单与验收）

### M0：技术原型（3 周，A+E 联合）
**目的**：解除最大技术风险（GC 选型），冻结 A/B 接口。
任务：
- [ ] Value 表示原型：NaN-box u64 vs enum，微基准（复制/算数/分发）
- [ ] GC 原型 ×2：分代标记-清除（bump 年轻代+卡表）；RC+循环回收
- [ ] 以 fib30 / 对象创建循环为负载，输出两份性能报告
- [ ] Shape/IC 原型 + 字节码序列化格式草案
- [ ] 冻结 `aluka-core` 的 `Value/Heap/Shape` 公共 API（B 轨道的输入）
验收：M0 报告选定 GC 策略 + API 冻结；两份原型性能对比表；回归到 Go 版基线对照。

### M1：MVP-1 可运行解释器（2.5 个月）
**目的**：`aluka run hello.js` + 命令行 + 最小 conformance 闭环。
任务：
- [ ] lexer/parser（AST 全量语法，不含类型注解）
- [ ] 字节码 VM：算术/比较/控制流/闭包/对象字面量
- [ ] CJS 模块最小集（require + 循环依赖）
- [ ] 内置：console/process/fs 最小（path read/write）
- [ ] CLI：run + --version + 退出码语义
- [ ] conformance：移植 node 11 用例（先 A 轨跑通 3/11 再全量）
- [ ] 轨道 D：bench 基线脚本接入（理解 diff 方法学）
交付物：`aluka` 二进制跑通 hello.js / fib30 / 简单文件 IO。
验收：node 主用例 ≥8/11；fib30 ≤ Go 版 2×（40ms 级，暂不追求快）。

### M2：MVP-2 全语言 + 模块（3.5 个月，A+B 首交）
**目的**：JS 语言全量 + TS 剥离 + Node 兼容可跑真实包。
任务（A 轨）：
- [ ] ES2015-2024 全特性（class/生成器/async/TLA/Proxy/BigInt/装饰器）
- [ ] RegExp 双路引擎（RE2 风格 + 回溯）
- [ ] TS/TSX 源码级转译
- [ ] 模块系统完整：ESM/CJS/package.json exports/imports/循环/TLA
- [ ] 字节码缓存 + FormatVersion
任务（B 轨首交）：
- [ ] FFI 桥：Rust VM → Go builtin（cgo 句柄表，对象生命周期管理）
- [ ] node:* 直接可用的 20 个高频模块（fs/url/path/events/util/assert/stream/buffer/timers/os/string_decoder/querystring）
交付物：跑通 `npm i express && aluka app.js`（express 真实 HTTP 链路）。
验收：node22 17/17 + express conformance；」TS 样例（含 JSX）转译正确。

### M3：MVP-3 性能对齐（GC/数组专项，2.5 个月，A+B 深化）
**目的**：分配与内存模型上量（本仓库 Go 版最痛的点）。
任务：
- [ ] GC 落地（M0 选定方案完整实现：写屏障/卡表/根集/shadow stack）
- [ ] 数字内联槽、packed elements（f64 数组）、array holes 位图
- [ ] Array 方法批量快路径（push/map/filter/reduce）
- [ ] B 轨：剩余内置模块 FFI 补全 + 高频模块 Rust 化（crypto/zlib/http 网络层）
- [ ] A 轨：字符串 rope + 拼接优化（对齐 Go 版 ME-1 结论）
交付物：gcPressure 形态（500K 循环、1% 保留）与 node 对拍。
验收：**gcPressure ≤ node 3×**（Go 版 21×）；arrayPush ≤ node 5×；fib30 ≤ node 3×。

### M4：JIT（3 个月，E 轨主力）
**目的**：解释器之上的性能主链。
任务：
- [ ] Quick IR（常量折叠/store-load/不可达删除）→ Cranelift / 自研后端
- [ ] 属性 PIC（shape guard）、去优化恢复、栈映射
- [ ] 闭包/upvalue/数组下标等已有特化等价物
- [ ] 移植 jitdiff 生成式差分 + fuzz（保持"三 tier 零失配"传统）
交付物：propAccess/callOverhead 与 node 对拍表。
验收：propAccess ≤ node 5×；closureCall ≤ node 5×；jitdiff≥3 千例零失配。

### M5：工具链迁移（2.5 个月，C 轨主力，与 M4 并行）
任务：
- [ ] pkgmanager：registry/semver/hoisting/lockfile/workspace
- [ ] bundler：compile 产物（payload+manifest+footer）与 web 打包（graph/shake/minify/chunk/UMD）
- [ ] Vue SFC subset 后端 + official（FFI 调原 Go/Vue compiler-sfc 或独立 Node 进程）
- [ ] GUI 桥（WebView2/WKWebView）+ dev/watch
交付物：`aluka build --compile` 与 `--target=web` 对 Go 版产物 diff 一致。
验收：build/webbuild conformance 全绿（24/24、13/13）；产物可反编译预览一致。

### M6：打磨与交接（1.5 个月）
- AIP/IPC、monitor、heap snapshot、inspector CDP、node:test、repl 完善
- 全 conformance 终检 + Miri/unsafe 审计 + 性能终检表
- 文档/ADR 迁移（新增 rust 架构决策记录）
验收：25 套 conformance 全绿；bench 矩阵与 Go 版等价或更优；Go 版进入维护模式。

---

## 3. MVP 定义（横切片，供演示/融资/发布）

- **MVP-1**（M1 末）：`aluka run` hello/fib/CLI —— 静态单二进制、零依赖。
- **MVP-2**（M2 末）：`npm i` 生态可玩 —— express/React SSR 可跑，TS 编译。
- **MVP-3**（M3 末）：性能基准达标 —— gcPressure ≤3× node；打包器可用（M5 后可并入）。
+ 每 MVP 附带：conformance 报告 + 性能对拍表 + 5 分钟演示脚本。

---

## 4. Gantt 时间线（并行视图，月份 = 有效 4 周）

```
月       1   2   3   4   5   6   7   8   9  10  11  12  13  14  15  16  17
A 引擎   [M0][-- M1 ----][----- M2 -----][-- M3 --][-- M4 --][---- M6 ---]
B 内置   ..............[FFI 过渡][-- Rust 化 --][-- 补全 --][合并 A]
C 工具链  ........................[-- M5 --]....................[并入]
D 测试   [==== 持续：conformance 迁移 / fuzz / bench 基线 ====]
E JIT    ..................[M4 主力：Quick IR→Cranelift→PIC]....
         ^M0 原型(3周)   ^MVP-1        ^MVP-2         ^MVP-3
```

并行关键：**B 的 FFI 过渡与 A 的 M1-M2 同时进行**（B 只需 A 的 Value API 冻结，
不需要 VM 完成）；**C 跟踪 A 的 Eval 接口即可先行**——使得 6 条链在 17 个月内
都占用（人力 4-6 人：A×2、B×1-2、C×1、D×0.5 + 作者仲裁）。

---

## 5. 每周节奏与门禁

- 周一：轨道站会（阻塞、风险、diff 门禁项）
- 周五：CI 门禁——`cargo test` + conformance 快照（node 3 条核心）+ bench 冒烟
- 每里程碑：冻结该里程碑验收表，不达成不许进入下一里程碑（范围可裁剪，验收不可裁剪）
- 每两周：与 Go 版差分对拍（A/B 双跑除性能外行为必须一致）

---

## 6. 并行风险与缓解

| 风险 | 缓解 |
|------|------|
| A/B 接口漂移（B 依赖的 Value API 频繁变） | M0 冻结 API + API 变更需跨轨评审 |
| FFI 边界双 GC（Go builtin 的 weak 与 Rust 堆） | 句柄表统一管理；对象生命周期以 Rust 侧为准；FFI 隔离测试 |
| C 轨工具链与 A 轨输出格式漂移 | C 使用"编译产物规范"而非引擎内部 API（通过 CLI 子命令集成测试） |
| D 轨 conformance 迁移滞后 | 每轨道投入 D 的人力占比固定（≥0.5 人月/里程碑） |
| 并行导致的合并冲突 | 单仓库 worktree 分支 + 逐里程碑合并，禁止长分支 |

---

## 7. 验收汇总（12 项硬指标）

| # | 指标 | 目标 |
|---|------|------|
| 1 | node conformance | ≥8/11（M1）→ 11/11（M2）|
| 2 | node22 conformance | 17/17（M2 末）|
| 3 | express 链路 | 通过（M2 末）|
| 4 | gcPressure vs node | ≤3×（M3）|
| 5 | arrayPush vs node | ≤5×（M3）|
| 6 | fib30 vs node | ≤3×（M3）|
| 7 | propAccess vs node | ≤5×（M4）|
| 8 | closureCall vs node | ≤5×（M4）|
| 9 | jitdiff 迁移 | ≥3 千例零失配（M4）|
| 10 | build/webbuild | 24/24、13/13（M5）|
| 11 | 全 conformance | 25 套全绿（M6）|
| 12 | Miri/unsafe 审计 | 0 高危（M6）|

---

## 8. 附录：轨道 B 内置模块 Rust 化排序（按依赖热度）

第一批（M2 FFI 后直接可用）：fs、url、path、events、util、assert、stream、
buffer、timers、os、string_decoder、querystring、module、process、console
第二批（M3 Rust 化）：crypto、zlib、http、https、net、tls、dns、child_process、
worker_threads、perf_hooks、readline、repl、sqlite
第三批（M4-M5）：v8、inspector、vm、dgram、http2、cluster、wasi、test、
diagnostics_channel、async_hooks、domain、punycode
 
各模块验收：node22 双跑 diff 零差异 + 专项 conformance（tests/compat/node22 现有用例复用）。