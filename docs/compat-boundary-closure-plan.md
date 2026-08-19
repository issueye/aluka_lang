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

| ID | 内容 | 验收标准 |
|----|------|---------|
| **B1** | **全量审计**：扩展 m8 探针为"全局实例 × own-keys × 原型链"双跑差分（`tests/compat/node22/`），枚举所有 `typeof x === 'object'` 的全局实例，对比 aluka 与 node 22.23.1 的自有键集合与 `Object.getPrototypeOf` 链 | 产出偏差对象清单（预期至少：`crypto`、`crypto.subtle`、`performance`；以实测为准），清单进本文档附录 |
| **B2** | 迁移基建：抽一个"构造器 + prototype 方法注册"helper（泛化 M8-11 的 SetProto 模式），保证方法描述符 flags（WebIDL：`{writable:true, enumerable:true, configurable:true}`）一次到位 | helper 单测；既有 `instanceof` 断言不回退 |
| **B3** | 按 B1 清单逐对象迁移：P0 = `crypto`/`subtle`/`performance`；P1 = 清单其余 | 每对象一组差分用例：`getOwnPropertyNames`、`keys`、`delete`（实例上删除返回 true 但不删原型方法）、`for...in` 含原型链遍历、`hasOwnProperty` |
| **B4** | 边缘收口：`Object.getPrototypeOf(globalThis)`、`globalThis` 自有属性可枚举性/顺序与 Node 快照对拍；`console`/`process` 等在 Node 中本就是自有属性的对象**保持不动**（防止过度迁移） | 差分全绿；knownDifference 从开发日志移除并记录撤销日期 |

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

| ID | 内容 | 验收标准 |
|----|------|---------|
| **C1** | 语料生成 + 复现：参数化生成批量 await 程序（维度：await 数量 × Promise/nextTick/queueMicrotask 混合 × rejection 比例 × TLA 模块数 × 定时器交错）；跑通双跑差分，固定化最小复现用例入库 | 至少一个稳定复现用例 + 序列差证据；若全部语料零差异，则以 10 万次循环扰动跑测（随机种子语料）兜底确认 |
| **C2** | 定位与修复（按 C1 结论，候选实施点）：① `maybeUnhandledRejection` 改为检查点末尾统一判定（drainJobQueues 返回前扫 pending rejected promises，对齐 Node 时机）；② AwaitPromise 与 RunLoop 检查点对齐（消除 1ms sleep 轮询引入的时序空窗）；③ 其余由复现证据指认 | 最小复现用例差分为零；C1 全语料双跑零差异 |
| **C3** | 回归闭环：语料转正式差分用例组（固定种子集）+ engine 微任务单元回归；`defect-fixes-plan.md` 遗留项改 ✅ 并记录修复提交 | `docs/defect-fixes-plan.md` 状态闭环；conformance 全绿 |

### C.3 风险与边界

| 风险 | 缓解 |
|------|------|
| checkpoint 语义改动波及 promise/async/事件循环全链路 | C2 每步跑 engine 全量 + jitdiff 三 tier 零失配 + `interpreter` 事件循环/timers 测试；分小 PR 落地 |
| unhandledRejection 时机改动影响既有 M2-4 行为（同拍挂处理者不误报） | promise.go 现有单测全保留，新增"先 reject 后同检查点内 catch"边界用例 |
| 若涉及 async 续体编译形态变化需 bump `bytecode/serialize.go FormatVersion` | C2 评审清单固定项；缓存兼容测试跟随 |

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
