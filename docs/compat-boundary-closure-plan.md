# 兼容边界收口计划：Vue SFC 样式能力、全局对象原型链、微任务顺序

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 日期：2026-08-19
> 依据：2026-08-19 项目问题分析确定的三个遗留兼容边界
> 配套文档：[defect-fixes-plan.md](./defect-fixes-plan.md) ｜ [vue-compiler-sfc-compat-plan.md](./vue-compiler-sfc-compat-plan.md) ｜ [vue-compiler-sfc-merge-notes.md](./vue-compiler-sfc-merge-notes.md) ｜ [node22-api-development-log.md](./node22-api-development-log.md)

---

## 0. 总览

本计划收口三个已确认的兼容性边界，按"引擎正确性 → 语义规范 → 生态可用性"组织为三条独立工作流，可并行推进、独立验收：

| 工作流 | 问题 | 一句话目标 | 规模 | 建议优先级 |
|--------|------|-----------|------|-----------|
| **A：Vue SFC 样式能力** | `<style lang="scss/less">`、`<style module>`、custom block 构建期一律报错，真实 Vue 项目（大量使用 scss）无法接入 | web bundle 支持主流样式写法，能力对齐 Vite 常规路径 | L | P1（A0 技术验证先行） |
| **B：全局对象原型链** | Web API 实例方法挂为自有属性（如 `crypto.getRandomValues`），与 Node 的 prototype 语义有已知偏差 | 全局实例方法迁到 `X.prototype`，own-key / delete / for-in 行为与 Node 22 差分为零 | M | P1 |
| **C：微任务顺序闭环** | 批量 await 场景 `queueMicrotask` / Promise 偶发顺序差异（2026-08-05 遗留，未闭环） | 差分复现 → 定位 → 修复 → 语料回归，关闭 defect-fixes-plan 遗留项 | S–M | **P0（先做）** |

**排序理由**：C 是引擎正确性问题且范围最小、复现成本最低，先闭环避免 A/B 验收时被顺序噪声干扰；B 有现成差分基建（m8 探针）与迁移模式（M8-11 SetProto）；A 价值最大但依赖外部包在构建 VM 内执行的可行性验证（A0），应尽早启动 spike、里程碑可滞后。

---

## 1. 现状与证据（2026-08-19 代码落点）

### 1.1 工作流 A：Vue SFC 样式边界

| 拒绝点 | 位置 | 行为 |
|--------|------|------|
| custom block | `internal/bundler/vue/sfc.go:53`（subset）、`official.go` driver 内 `d.customBlocks` 检查 | 顶层未知标签一律 `custom SFC blocks are not supported` |
| `<style module>` | `internal/bundler/vue/src.go:117`（subset）、`official.go:87` | `is not supported yet` |
| `lang≠css` | `internal/bundler/vue/src.go:123`（subset）、`official.go:92` | `only css` |
| `:deep/:slotted/:global/v-bind` | `internal/bundler/vue/style.go:10 rejectAdvancedScoped` | scoped 高级选择器拒绝 |

已具备的基础设施：

- **official 后端的"构建 VM 内执行项目依赖"模式**（`official.go` officialDriver）：script/template 已通过项目 `node_modules` 的 compiler-sfc 编译，权限模型（与 `aluka run` 相同、仅可信依赖、失败禁止静默回退）可直接复用于 sass/less；
- **虚拟 CSS 模块管线**：`<style>`（含 scoped 纯 CSS）已经 facade 副作用 import 接入 graph 与 watch ExtraFiles；
- **AGENTS.md 约束 3** 当前明确要求这三类写法"必须构建期报错"——本计划即是有意修改该边界，落地时必须同步修订 AGENTS.md / README / `sfc.go` 包注释（见第 7 节）。

### 1.2 工作流 B：全局对象原型链

- `internal/engine/interpreter/interpreter.go:1547 setupGlobalThis`：globalThis 指向普通对象，`globalThis` 自身 surface 与 node 22.23.1 已对齐（m8 探针）；
- 已知偏差（node22-api-development-log M8 验收记录）：**实例方法为自有属性**——`crypto` 自有键含 `getRandomValues`/`randomUUID`/`subtle`，而 Node 22 中它们在 `Crypto.prototype` / `SubtleCrypto.prototype` 上；
- 落点示例：`internal/runtime/globals/crypto_web.go NewWebCrypto` 用 `crypto.Set(...)` 挂自有方法；构造器 `Crypto/SubtleCrypto/CryptoKey` 已按 M8-11 经 `SetProto` 注册，`instanceof` 成立——**迁移所需的构造器 + 原型注册模式已经存在**；
- 可观察差异面：`Object.getOwnPropertyNames(crypto)`、`Object.keys(crypto)`、`delete crypto.getRandomValues`、`crypto.hasOwnProperty('getRandomValues')`、`for...in` 原型链遍历、`console.log(crypto)` 展示形状。

### 1.3 工作流 C：微任务顺序

- 队列实现在 `internal/engine/interpreter/microtask.go`：`nextTickQueue` + `microtaskQueue`，`drainJobQueues`（第 50 行）为"nextTick 排干 → 微任务排干 → 循环"，**检查点语义本身与 Node 一致**，隔离测试正确；
- 嫌疑点（"批量 await 偶发顺序差异"的两个已知候选，C1 负责证实或排除）：
  1. **unhandledRejection 判定时机**：`promise.go Reject()` 路径在拒绝瞬间经 `maybeUnhandledRejection` **向微任务队列插入一个检查任务**，它在同一检查点内按入队位置执行；Node 的 `unhandledRejection` 派发在检查点末尾 / tick 结束时统一判定，两者在批量 await + 混合 rejection 场景可产生可观察顺序差；
  2. **TLA 驱动与事件循环交错**：`vm.go:3094 AwaitPromise` 以"排干队列 + 轮询 taskCh + 1ms sleep"驱动顶层 await，多个 async 模块（批量 await）各自的 AwaitPromise 循环交错排水，微任务执行时机相对 Node 的顺序图可有偏移（含 `eventloop.go:97/113` 检查点位置）。
- 历史记录：`docs/defect-fixes-plan.md` 总览表 `queueMicrotask 顺序边界 | ⚠️ 遗留 | 隔离测试正确；批量 await 场景偶发顺序差异，非阻断`。

---

## 2. 工作流 A：Vue SFC 样式能力补全

### A.1 目标与非目标

**目标**

1. `<style lang="scss|sass|less">` 在 subset 与 official 两个后端均可编译，产物进入既有 CSS 管线（含 scoped、sourcemap、watch）；
2. `<style module>`（含 `<style module lang="scss">` 组合）实现 CSS Modules：类名作用域重写 + 默认导出映射 + 组件 `useCssModule` 可用；
3. custom block 提供**插件认领**机制；无插件认领时保留明确报错（延续"禁止静默丢弃"约束）。

**非目标（本期明确不做）**

- 自研纯 Go SCSS/Less 编译器（SCSS 图灵完备，成本不成比例；见 A.2 决策）；
- `:deep()/:slotted()/:global()` 与 `v-bind()` in CSS（scoped 高级特性，单列后续里程碑，避免本期 scope 膨胀）；
- PostCSS 插件链、Tailwind 之类上层方案、`<style module>` 的 `module="customName"` 多命名组合（先支持 default，留接口）。

### A.2 技术决策

**决策 A-1：预处理器执行通道 = 构建期内执行项目 node_modules 的 `sass` / `less` 包（复用 official 后端模式）**

- 与 `--vue-compiler=official` 同构：在构建 VM 内 `import` 项目的 `sass`（dart-sass JS 发行版）或 `less`，调 `compileString` / `render` 得到 CSS，再交给 Go 侧既有 scoped/graph CSS 管线；
- 同一通道对两个后端共用：subset 后端遇 `lang≠css` 时也走该通道（不再直接报错），仅"项目未安装对应预处理器包"时构建期报错并给出安装提示；
- 信任模型与 official 相同：执行的是项目自己的依赖，权限等同 `aluka run`，文档同步标注"仅对可信依赖启用"；
- 失败禁止静默回退（源 lang 原样当 CSS 是错误放大器）。

**否决方案**：引入 libsass/CGO 绑定（违反纯 Go 硬约束）；自研 Go SCSS 子集（长期可选，不作为本期路径）。

**决策 A-2：CSS Modules 在 Go/graph 层实现，两后端共享**

CSS Modules 是打包器职责（Vite 中由 postcss-modules 完成，不是 compiler-sfc），因此：

- Go 侧实现类名收集与重写：`generateScopedName` 对齐 Vite 默认规则（实现时以 Vite 6 实测输出为准，写进 fixture 断言）；
- 为每个 `<style module>` 生成虚拟 JS 模块：`export default { original: 'scoped' }`，facade `import styles from '...vue?module...0.css'` 并挂 `__sfc__.__cssModules = { default: styles }`（Vue 运行时 `useCssModule()` 依赖该字段）；
- sourcemap：重写发生在类名粒度，沿用 CSS 管线既有 map 机制，偏移只做行级近似并在文档标注精度。

**决策 A-3：custom block = 插件钩子认领，缺省仍报错**

- 在 `internal/project` 既有 web 插件体系上新增钩子（对齐现有钩子注册风格）：`transformCustomBlock({ code, type, attrs, filename }) → JS 代码字符串`；
- 返回字符串 → 生成虚拟 JS 模块并挂 `__sfc__.customBlocks`（对齐 @vitejs/plugin-vue 语义）；无插件认领或插件返回空 → 保留今日报错文案（改述为"no plugin claims custom block `<x>`"）。

### A.3 里程碑

| ID | 内容 | 验收标准 |
|----|------|---------|
| **A0** | **技术验证 spike**：在构建 VM 内执行真实 `sass`（dart-sass JS）与 `less` 包各跑一个非平凡样式（嵌套/变量/mixin/@use），记录可行性、冷启动与增量耗时、产物体积 | 产出结论文档一节（A.5）；若 dart-sass 在当前引擎跑不通，列出缺口清单并在此关闸调整方案（改先支持 less，scss 缺口转引擎兼容工作流） |
| **A1** | 预处理器通道工程化：subset+official 接入、错误位置映射回 SFC 块、watch 依赖（预处理器的 import 链进 ExtraFiles）、`--vue-compiler` 语义不变 | `tests/conformance/webbuild`、`vue-sfc` 新增 scss/less 用例（fixture 见 A.4）；无预处理器包时错误信息含安装提示；失败无静默回退 |
| **A2** | `<style module>`：默认命名 CSS Modules（纯 CSS 先行，A1 合入后打通 lang 组合） | 组件内 `useCssModule().foo` 类名与 Vite 规则一致；`tests/conformance/webbuild` 增加 module 断言；类名 hash 稳定（同输入同输出，进 fixture golden） |
| **A3** | custom block 插件钩子 + 无认领报错 | demo 增加 `<route>`/`<i18n>` 风格用例：插件返回对象合并进组件；无插件时构建失败且信息可读 |
| **A4** | 文档与约束同步（见第 7 节） | AGENTS.md 约束 3、README Vue SFC 段、`sfc.go` 包注释、vue-compiler-sfc-merge-notes 全部更新且互相一致 |

### A.4 测试与 fixture 策略

- vendored fixture 参照 `demo/web-bundle-vue-demo/node_modules` 既有模式：仅入库 `sass`/`less` 的最小闭包 + lockfile；同步更新 regex corpus（`tools/extract-regex-corpus.mjs`）与 Node oracle 语料；
- 按 merge-notes 观察项：入库后测量 clone/checkout/CI cache 影响，超阈值则改为 conformance 环境按需安装（CI cache + 本地 `ALUKA_VUE_SASS_FIXTURE` 门控 skip）；
- official 后端性能基线（冷启动/热 Transform）在 A1 后重测并更新到 merge-notes。

### A.5 风险与边界

| 风险 | 缓解 |
|------|------|
| dart-sass（大体量 JS）在自研引擎跑不动 | A0 先行关闸；缺口转 pi-compat 式兼容清单逐项补；less（纯 JS、体量小）保底 |
| 预处理器 import 链导致 watch 风暴 | ExtraFiles 去重 + 变更集测试（改 `_variables.scss` 触发依赖者重建） |
| CSS Modules hash 规则与 Vite 版本漂移 | golden fixture 锁定当前 Vite 6 实测，规则升级单列变更 |
| 违反现有 AGENTS 约束的窗口期 | A4 与 A1 同 PR 提交，不允许"能力已进、文档未改"的中间态存在超过一个提交 |

---

## 3. 工作流 B：全局对象原型链规范语义

### B.1 目标

全局命名下的 Web API **实例**方法一律挂到对应 `X.prototype`，使 own-key 枚举、`delete`、`hasOwnProperty`、`for...in`、`Object.setPrototypeOf` 边界与 Node 22 行为差分为零；`node22-api-development-log` 中对应 knownDifference 条目撤销。

### B.2 里程碑

| ID | 内容 | 验收标准 | 状态 |
|----|------|---------|------|
| **B1** | **全量审计**：扩展 m8 探针为"全局实例 × own-keys × 原型链"双跑差分（`tests/compat/node22/`），枚举所有 `typeof x === 'object'` 的全局实例，对比 aluka 与 node 22.23.1 的自有键集合与 `Object.getPrototypeOf` 链 | 产出偏差对象清单（预期至少：`crypto`、`crypto.subtle`、`performance`；以实测为准），清单进本文档附录 | ✅ 2026-08-20（附录 B1） |
| **B2** | 迁移基建：抽一个"构造器 + prototype 方法注册"helper（泛化 M8-11 的 SetProto 模式），保证方法描述符 flags（WebIDL：`{writable:true, enumerable:true, configurable:true}`）一次到位 | helper 单测；既有 `instanceof` 断言不回退 | ✅ 2026-08-20 |
| **B3** | 按 B1 清单逐对象迁移：P0 = `crypto`/`subtle`/`performance`；P1 = 清单其余 | 每对象一组差分用例：`getOwnPropertyNames`、`keys`、`delete`（实例上删除返回 true 但不删原型方法）、`for...in` 含原型链遍历、`hasOwnProperty` | ✅ 2026-08-20（P0+EventTarget+navigator） |
| **B4** | 边缘收口：`Object.getPrototypeOf(globalThis)`、`globalThis` 自有属性可枚举性/顺序与 Node 快照对拍；`console`/`process` 等在 Node 中本就是自有属性的对象**保持不动**（防止过度迁移） | 差分全绿；knownDifference 从开发日志移除并记录撤销日期 | ✅ 2026-08-20（可枚举性对齐；残留项见 B1.3） |

### B.3 风险

| 风险 | 缓解 |
|------|------|
| 方法上原型后走原型链查找，属性访问 IC/Shape 命中率下降 | 迁移前后跑 `propAccess`/`propSet` 基准（bench/results 口径），劣化超阈值（建议 >5%）先优化 IC 原型链路径再继续迁移 |
| 用户代码以 own-key 探测 API（如 `Object.keys(crypto).length`） | 与 Node 一致正是目标；差分语料覆盖 |
| `structuredClone`/序列化路径对原型方法的假设 | engine 全量 + node22 全量差分回归（见第 6 节） |

---

## 4. 工作流 C：queueMicrotask / 批量 await 顺序闭环

### C.1 方法论：复现先行

顺序类缺陷最忌凭嫌疑直接改。C1 先建立"顺序追踪差分"工具，把"偶发"变成"可重复"：

- 每个任务回调输出单调序号 + 来源标签（`nextTick` / `micro` / `then` / `await-resume` / `unhandledRejection`），程序结束时打印完整顺序序列；
- 同一程序 aluka 与 node 22 双跑，`diff` 序列即为偏差证据（复用 `tests/compat/node22/` 双跑框架）。

### C.2 里程碑

| ID | 内容 | 验收标准 | 状态 |
|----|------|---------|------|
| **C1** | 语料生成 + 复现：参数化生成批量 await 程序（维度：await 数量 × Promise/nextTick/queueMicrotask 混合 × rejection 比例 × TLA 模块数 × 定时器交错）；跑通双跑差分，固定化最小复现用例入库 | 至少一个稳定复现用例 + 序列差证据；若全部语料零差异，则以 10 万次循环扰动跑测（随机种子语料）兜底确认 | ✅ 2026-08-20（`microtask-corpus/` 60 例，22 例复现） |
| **C2** | 定位与修复（按 C1 结论，候选实施点）：① `maybeUnhandledRejection` 改为检查点末尾统一判定（drainJobQueues 返回前扫 pending rejected promises，对齐 Node 时机）；② AwaitPromise 与 RunLoop 检查点对齐（消除 1ms sleep 轮询引入的时序空窗）；③ 其余由复现证据指认 | 最小复现用例差分为零；C1 全语料双跑零差异 | ✅ 2026-08-20（见 C 完成记录；TLA 串行化为结构性已知差异，单列） |
| **C3** | 回归闭环：语料转正式差分用例组（固定种子集）+ engine 微任务单元回归；`defect-fixes-plan.md` 遗留项改 ✅ 并记录修复提交 | `docs/defect-fixes-plan.md` 状态闭环；conformance 全绿 | ✅ 2026-08-20 |

### C.3 风险与边界

| 风险 | 缓解 |
|------|------|
| checkpoint 语义改动波及 promise/async/事件循环全链路 | C2 每步跑 engine 全量 + jitdiff 三 tier 零失配 + `interpreter` 事件循环/timers 测试；分小 PR 落地 |
| unhandledRejection 时机改动影响既有 M2-4 行为（同拍挂处理者不误报） | promise.go 现有单测全保留，新增"先 reject 后同检查点内 catch"边界用例 |
| 若涉及 async 续体编译形态变化需 bump `bytecode/serialize.go FormatVersion` | C2 评审清单固定项；缓存兼容测试跟随 |

### C 完成记录（2026-08-20）

**C1 工具与语料**（`tests/compat/node22/microtask-corpus/`）：
- `gen.mjs`：种子化（mulberry32）参数化生成——async 函数数（2..5）× 每函数 await 数（1..3）× await 目标形状（已决 promise then / queueMicrotask / nextTick / rejected 定向 / setTimeout(0)）× 顶层交错（micro/tick/then）× TLA 模块数（1..3，ESM 目录）；每回调立即打一行标签，stdout 顺序即调度序列证据。
- `run.sh`：逐用例 aluka vs node 双跑行级对比。seed 42 / 60 例首轮 **22 例复现**（15 CJS + 7 TLA）。

**C2 修复**（按复现证据指认，两个真实偶发根因）：
1. **unhandledRejection 检查点末尾统一判定**（`promise.go`/`microtask.go`）：原实现在 reject 瞬间把检查任务按入队位置插入微任务队列——事件提前于后续微任务触发，且"同检查点内稍后挂 catch"会假阳性。改为 `unhandledQueue` 登记（rejection FIFO）+ `drainJobQueues` 排干后统一判定（监听器再入队则回到排水循环直至稳定）；`unhandledReported` 保证一次性派发。
2. **同刻到期定时器 FIFO**（`globals/timers.go`）：原实现每个 setTimeout 独立 `time.AfterFunc`，多个 0ms 定时器并发竞争投递 taskCh，实测 case-0049 20 次出现 2 种序列（`ti:final` 偶发提前）。改为集中式到期队列：`(deadline, seq)` 最小堆 + 单一派发 goroutine 依序 PostTask（Node timer list 语义；setInterval 仍走 Ticker）。
3. **AwaitPromise 1ms 轮询分析结论**：sleep 窗口只引入任务派发**延迟**、不改变顺序（taskCh FIFO 保持）；语料与差分均无顺序型证据指向它，保持现状。
4. **TLA 多模块求值串行化（结构性已知差异，未在本工作流修）**：aluka loader 对每个 TLA 模块 `AwaitPromise` 串行驱动到完成（mod1 完成后 mod2 体才启动）；Node 为全体模块体先同步执行到首个 TLA await，再经共享微任务链交错完成（期间 nextTick 不抢占、队列排空后的检查点才排）。数据语义正确、仅跨模块事件观测顺序不同；修复需编译器/loader 级 TLA 求值重构（import 挂起传播），单列后续工作项。语料中 6 个 TLA 用例稳定复现此差异。

**C3 回归闭环**：
- 最小复现正式差分用例：`tests/compat/node22/diff/c2-microtask-order.cjs`（检查点末尾判定 + FIFO + 同拍 catch 不误报 + 跨 tick catch 迟到仍派发）、`c2-timer-fifo.cjs`（同刻 FIFO + 微任务期注册排序 + 回调内再注册排序）——两者在修复前基线二进制上 FAIL、修复后 PASS（timer 用例 30/30 稳定）。
- 语料稳定性：seed 42 / 60 例连续 5 轮 **54/60**（CJS 全绿，6 TLA 为上述结构性差异）。
- 全 workspace `go test` 绿；jitdiff 三 tier 零失配；node22 差分 59+2/66+2（7 个失败与基线逐字节一致，存量）。
- `docs/defect-fixes-plan.md`「queueMicrotask 顺序边界」⚠️ → ✅（2026-08-20），含结构性残留说明。

---

## 5. 排期与依赖

```text
C1 复现语料 ──> C2 定位修复 ──> C3 回归闭环 ──> 更新 defect-fixes-plan
   │（C 全程不依赖 A/B，最先启动）
   │
B1 审计探针 ──> B2 helper ──> B3 逐对象迁移 ──> B4 边缘收口 ──> knownDifference 撤销
   │（B3 期间跑属性访问基准，防 IC 劣化）
   │
A0 spike（与 C1 并行启动）──> A1 预处理器通道 ──> A2 CSS Modules ──> A3 custom block ──> A4 文档同步
   │
   └─ A0 关闸：dart-sass 不可运行 → 先 less，scss 转引擎兼容清单
```

三条工作流互相独立；唯一软依赖：**A/B 的差分验收建议在 C2 合入后执行**，避免微任务顺序噪声污染输出序断言。

## 6. 回归测试与验收总纲（对齐 AGENTS 测试约定）

| 改动面 | 必跑 |
|--------|------|
| 工作流 A（graph/resolver/printer/emit/Vue backend） | 构建 `./bin/aluka` 后：`tests/conformance/webbuild/run.sh` + `tests/conformance/vue-sfc/run.sh`；影响共享 graph/shake/minify 时加跑 `tests/conformance/build/run.sh` |
| 工作流 B（globals/engine 语义） | `tests/compat/node22/` 双跑差分全量；engine 模块全量（`cd internal/engine && GOWORK=off go test ./...`）；属性访问基准对比；test262 子集（property/prototype 相关） |
| 工作流 C（microtask/promise/事件循环） | engine 全量 + **jitdiff 三 tier 零失配**（`go test ./internal/engine/interpreter/jitdiff/ -count=1`）+ C1 语料差分 + node22 差分 |
| 涉及字节码布局 | bump `bytecode/serialize.go FormatVersion` + `optimize_equivalence_test.go` 对拍 |
| fixture 入库 | lockfile + regex corpus + Node oracle + 性能/体积基线同步（见 A.4） |

## 7. 文档与约束同步（每个工作流的收官步骤）

1. **AGENTS.md 约束 3**：从"custom block、`lang` 预处理器、`<style module>` 仍必须构建期报错"改写为新边界（预处理器 = 项目依赖执行 + 信任模型说明；custom block = 插件认领制，无认领仍报错；`:deep/v-bind` 等维持报错）；
2. **README**：Vue SFC 段、能力清单、CLI 表格同步（`--vue-compiler` 语义不变，无需新 flag）；
3. **代码内注释**：`internal/bundler/vue/sfc.go` 包注释、`src.go rejectUnsupportedStyle`、`official.go` fail 文案与实际行为对齐；
4. **knownDifference 台账**：`node22-api-development-log.md` M8 原型链条目随 B4 撤销；`defect-fixes-plan.md` queueMicrotask 条目随 C3 改 ✅；
5. 本文档每个里程碑完成时回填"完成记录"（日期 + 提交 + 证据），状态列与完成记录保持一致，避免再现"清单 ⬜ / 记录已填"的漂移。

---

## 附录 B1：全局实例原型链审计清单（2026-08-20 实测）

审计工具：`tests/compat/node22/probe/protos.cjs`（双跑 JSON 差分，已注册进 `run-probe.sh`）；分类报告 `tools/audit-protos.mjs`。基线 node 22.23.1（冻结版）。

### B1.1 P0 迁移目标（Web API 实例，B3 范围）

| 实例 | Node 期望自有键 | Node 原型链（ctor: 自有键） | aluka 现状 |
|------|----------------|---------------------------|-----------|
| `crypto` | `[]`，`[object Crypto]` | `Crypto.prototype{constructor,getRandomValues,randomUUID,subtle}` → `Object.prototype` | 方法/`subtle` 为自有属性；`Crypto.prototype` 存在但为空且其 proto 为 null；无 toStringTag |
| `crypto.subtle` | `[]`，`[object SubtleCrypto]` | `SubtleCrypto.prototype{encrypt,decrypt,deriveBits,deriveKey,digest,exportKey,generateKey,importKey,sign,unwrapKey,verify,wrapKey,constructor}` → `Object.prototype` | 全部方法为自有属性 |
| `performance` | `[]`，`[object Performance]` | `Performance.prototype{17 方法/getter}` → `EventTarget.prototype{addEventListener,constructor,dispatchEvent,removeEventListener}` → `Object.prototype` | 9 个键全为自有（含 `timeOrigin`——Node 中是原型 getter）；proto 为 null |
| `navigator` | `[]`，`[object Object]` | `Navigator.prototype{constructor,hardwareConcurrency,language,languages,platform,userAgent}`（全 getter）→ `Object.prototype` | 6 个属性全为自有数据属性；proto 为 null |

delete 行为差（自有属性模型）：`delete crypto.randomUUID` / `delete performance.now` / `delete navigator.userAgent` 在 aluka 后方法不可达（`undefined`），Node 中仍可用——迁移后应与 Node 一致。

### B1.2 耦合的引擎前提（B3 验收依赖，随 B3 一起修）

> 2026-08-20 更新：以下 4 项已随 B3/B4 全部修复，详见附录 B 完成记录。

1. **for-in 不遍历原型链（引擎 bug）**：`for (k in Object.create({a:1}))` aluka 得 `[]`，Node 得 `["a"]`。不修则迁移后 `for (k in crypto)` 仍与 Node 差异（Node 枚举原型上可枚举方法）。→ 已修（OpEnumKeys）
2. **Object.prototype 成员可枚举**：aluka 中 `Object.keys(Object.prototype)` 有 6 键、描述符 `enumerable:true`；Node 中 `[]`（全不可枚举）。与 #1 耦合：for-in 一旦走原型链，可枚举的 Object.prototype 成员会泄漏进所有 for-in。→ 已修（sweepBuiltinEnumerability）
3. **Object.prototype 面**：aluka 缺 `__defineGetter__` / `__defineSetter__` / `__lookupGetter__` / `__lookupSetter__` / `__proto__` / `toLocaleString`（6/12）。→ 已修（12/12）
4. **null 原型根源**：Go 侧 `engine.NewObject()` 创建的对象原型为 null（JS 字面量 `{}` 才接 objectProto）——全局实例与其 prototype 对象普遍如此，`Math.hasOwnProperty` 直接抛 TypeError。→ Web API 实例已迁移；Math/JSON/Reflect 已补链（console/process/Intl 残留属 B1.3 超范围记录）。

### B1.3 超范围记录（不在工作流 B 内修，供其他工作流/缺陷台账引用）

> 2026-08-20 更新：`Performance`/`Navigator` 构造器已随 B3 注册；Math/JSON/Reflect 与 globalThis 的可枚举性、Math/JSON/Reflect 原型链与 toStringTag 已随 B4 修复；console/process/Intl 与成员缺失项仍为遗留。

- ~~**可枚举性（B4 部分）**~~：已修——Math/JSON/Reflect 成员、globalThis 的 ES 内建属性不可枚举；globalThis 可枚举集合 = Node 的 15 个（插入顺序仍有差，遗留）。
- **toStringTag（遗留）**：console/process/globalThis 之外的残留——console/process 按计划保持不动；Intl 无 tag。
- ~~**成员缺失（部分已修，2026-08-25 gap-closure-plan P1）**~~：`Math.hypot`、`JSON.isRawJSON/rawJSON`、`eval`/`escape`/`unescape`、`AggregateError`/`EvalError`/`URIError` 已补齐（含 `new TypeError() instanceof Error` 原型链修复与内置 prototype 链到 %Object.prototype%）；`Intl.DisplayNames/Locale/supportedValuesOf`、`Atomics`、`WebAssembly`、`Iterator`、`WeakRef`、`SharedArrayBuffer`、`ReadableStreamBYOBRequest` 仍为遗留（见 gap-closure-plan §4 P5/P6）。
- ~~**globalThis 自有 `hasOwnProperty` 键**~~：已修（误注册移除，经原型链解析）；`Aluka`/`Bun`/`URLPattern`/`gc` 为已知超集（Bun 兼容，保留）。
- **console/process**：按计划保持不动（Node 中本就是自有属性模型；`process` 的 `on/off/emit` 在 Node 挂 `EventEmitter` 原型链，属已知偏差，不在 B 内迁移）。
- **globalThis 原型链（遗留）**：Node 为 `globalThis → (V8 单键中间对象) → Object.prototype`，aluka 直连 Object.prototype；中间层为 V8 global proxy 细节，不追。
- **函数对象枚举性（遗留）**：普通函数 `name`/`length`/`prototype` 仍可枚举（Node 不可枚举）；闭包创建热路径加 attrs 映射有分配成本，留待后续以 shape 级方案处理。
- **Intl 命名空间（遗留）**：成员可枚举 + null 原型 + 缺 3 成员。

### B1 完成记录

- 2026-08-20：`probe/protos.cjs` + `run-probe.sh` 注册 + `tools/compare-probe.mjs`/`tools/audit-protos.mjs` 落库；node 22.23.1 vs aluka 双跑，偏差分类如上（P0 命中计划预期并新增 navigator；另发现 for-in 原型链缺失、Object.prototype 面与可枚举性两个耦合引擎前提）。

---

## 附录 B 完成记录（2026-08-20）

### B2：迁移基建 ✅

- `engine.Context` 新增 `ObjectPrototype() Object`（Interpreter/VM/stub 三实现），供全局注册把接口原型接到 `%Object.prototype%`。
- `internal/runtime/globals/interface.go`：`RegisterInterface(ctx, WebInterface{Name, Tag, Base, Ctor})` —— ctor.prototype `{w:false,e:false,c:false}`、proto.constructor 不可枚举、原型链接 Base/Object.prototype、Symbol.toStringTag 非可枚举；方法经 `proto.Set`（wec 全 true，WebIDL 一致）、访问器经 `engine.SetAccessor`。
- 引擎补 `Object.prototype.toString` 的 `Symbol.toStringTag` 协议（ES2020 20.1.3.6 step 5，此前恒 `[object Object]`）。
- 单测 `interface_test.go`（描述符/原型链/instanceof/delete/hasOwnProperty/new 抛 TypeError/多级 Base）。

### B3：迁移与耦合引擎修复 ✅

**迁移对象（node22 protos 探针差分清零）**：
- `crypto`（自有键空；`getRandomValues`/`randomUUID` 上原型；`subtle` 为原型访问器恒返回共享实例）
- `crypto.subtle`（12 方法 + wrapKey/unwrapKey 上 `SubtleCrypto.prototype`）
- `CryptoKey`（内部状态存 Symbol 键槽位，own keys 空；type/extractable/algorithm/usages 原型 getter）
- `performance`（9 键全迁移；补齐 Node 22 的 17 键原型面：clearMeasures/clearResourceTimings/eventLoopUtilization/nodeTiming/onresourcetimingbufferfull/setResourceTimingBufferSize/timerify/toJSON；链 `Performance.prototype → EventTarget.prototype → Object.prototype`；注册全局 `Performance` 构造器）
- `navigator`（6 属性为 `Navigator.prototype` getter；注册全局 `Navigator`；无 toStringTag——对齐 Node）
- `EventTarget`（方法上原型 + Symbol 槽位监听状态；AbortSignal.prototype → EventTarget.prototype；修复 Go 侧触发路径经 `eventTargetAddListener`/`eventTargetDispatch` 直调）
- `node:perf_hooks` 模块改为增强既有原型（此前重挂空原型导致回归，已修复并回归 m6-4 差分 PASS）

**耦合引擎修复**：
- for-in 原型链遍历：新 opcode `OpEnumKeys`（meta 登记，JIT 白名单外自动拒编译），VM/AST 共用 `engine.EnumerateForInKeys`；**FormatVersion 28 → 29**
- `Object.prototype` 补齐 12 键（新增 `toLocaleString`/`__defineGetter__`/`__defineSetter__`/`__lookupGetter__`/`__lookupSetter__`/`__proto__` 访问器）
- 内建枚举性清扫（`sweepBuiltinEnumerability`）：全部内建构造器静态方法、原型成员、Math/JSON/Reflect 成员统一不可枚举；class 原型 constructor 与方法不可枚举

### B4：边缘收口 ✅

- globalThis：`[[Prototype]] = %Object.prototype%`（移除误注册的自有 `hasOwnProperty` 键，babel 风格 `hasOwnProperty.call` 经原型链解析）；`Symbol.toStringTag = 'global'`
- globalThis 可枚举性：engine 侧（setupBuiltins 末尾）+ globals 侧（`SweepGlobalEnumerability`，注册完成后调用）双段清扫，白名单 = Node 22 的 15 个可枚举全局；`Object.keys(globalThis)` 与 Node 集合一致（顺序仍有插入序差，残留）
- Math/JSON/Reflect：补原型链 + `Symbol.toStringTag`
- knownDifference 撤销：`node22-api-development-log.md` M8 记录 2026-08-20 撤销条目

### B 验收证据（2026-08-20）

- node22 protos 探针：crypto/crypto.subtle/performance/navigator/deleteTest 全清零；Math/JSON/Reflect/Intl/console/process/globalThis 残留差异均为 B1.3 记录的超范围项
- node22 差分套件 59/66；7 个失败与基线二进制逐字节一致（存量，非本工作流回归）
- 全 workspace `go test` 绿；jitdiff 三 tier 零失配；optimize_equivalence 通过；test262 子集 8/8
- 属性访问基准（PropAccess/Polymorphic/Miss/Set ×3）：基线对比 −6.5% ~ +2.2%（噪声内，未超 5% 阈值）
- conformance：build 23/23；webbuild/vue-sfc 失败项与基线一致（环境性存量）
