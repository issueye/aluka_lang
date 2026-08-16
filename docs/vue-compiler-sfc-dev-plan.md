# Vue 官方 compiler-sfc 兼容——开发计划

> 关联文档：[vue-compiler-sfc-compat-plan.md](./vue-compiler-sfc-compat-plan.md)（决策分析与实证基线，
> 本文的 M0-M5 里程碑在彼处定义）。
> 性质：可执行任务分解（任务卡 = 目标 / 改动文件 / 步骤 / 验收 / 规模）。
> 约束：全程遵守 AGENTS.md（纯 Go / CGO_ENABLED=0 / jitdiff 三 tier 零失配 / 表驱动测试）。
>
> **进度（2026-08-16）**：M0 ✅ / M1 ✅ / M2 ✅（探针首关即全绿，产物指纹与 node 字节一致；
> G1 及次生缺口见 `tests/conformance/vue-sfc/gaps.md`）/ M4 ✅（`--vue-compiler=official`
> 双后端落地：demo official 编译 SSR/动态 chunk 通过、错误映射带 .vue 文件名、
> webbuild 缓存约束保持）/ M3 M5 待启动。

---

## 0. 范围与非目标

**范围**：aluka 运行时跑通 `@vue/compiler-sfc`；bundler 提供第二编译后端；相关引擎与正则加固。

**非目标**：

- 不替换默认 SFC 子集后端（`--vue-compiler` 默认 `subset`）；
- 不实现 `/u` unicode 码点语义、sticky 完整语义（实测闭包 0 需求，归 pi-compat 主线）；
- 不引入任何 Node/esbuild/Rollup 外部工具链；
- 不预支修复未验证的引擎猜测（一切以差分 gate 驱动）。

---

## 1. 任务总览

| ID | 任务 | Phase | 规模 | 依赖 | 核心产出 |
|----|------|-------|------|------|----------|
| T0 | 差分 gate 落地 | P0 | 0.5d | — | `tests/conformance/vue-sfc/` + CI step |
| T1 | defineProperty 语义修复 | P1 | 4-6d | T0 | 描述符模型 + 矩阵测试 + 探针推进 |
| T2 | 驱动式修复循环 | P2 | 每关 0.5-2d（关数未知） | T1 | gap 台账 + 探针全绿 |
| T3 | 正则加固 | P3 | 3d | —（可与 T2 并行） | 对拍语料 + 护栏 + flag 差分 |
| T4 | official 编译后端 | P4 | 3-4.5d | T2 绿 | `--vue-compiler=official` 端到端 |
| T5 | 性能与收官 | P5 | 1.5-2d | T4 | 基线数据 + 文档 |

核心路径（T0→T1→T2→T4→T5）预计 **8-13 人日 + T2 关卡变量**。

---

## 2. Phase 0（T0）：差分 gate

**目标**：把当前"红"固化为可重复的失败，作为后续所有修复的进度标尺。

**改动文件**：

| 文件 | 动作 |
|---|---|
| `demo/web-bundle-vue-demo/probe.mjs` | 新增（探针脚本，与 fixture 同目录以便裸说明符 `vue/compiler-sfc` 解析） |
| `tests/conformance/vue-sfc/run.sh` | 新增 |
| `.github/workflows/ci.yml` | 修改（ubuntu job，紧随 webbuild step 之后加一步） |

**步骤**：

1. probe.mjs 入参：SFC 源码路径 + 输出目录；执行 `parse → compileScript → compileTemplate`，
   成功打印产物指纹 + `COMPILER_SFC_OK`，失败时把首个异常（name/message/stack 首帧）打到 stdout 后退出非零。
2. run.sh：`node probe.mjs` 取基线输出 → `ALUKA probe.mjs` 取实测 → 状态比对；
   node 缺席 `SKIP`（对齐 webbuild 约定）；aluka 失败时输出截断保留首个 TypeError 栈。
3. ci.yml 增加 step，命令形态与既有 `tests/conformance/webbuild/run.sh` 一致。

**验收**：

- 本地 `bash tests/conformance/vue-sfc/run.sh` 输出 FAIL 且失败原因为
  `Object prototype may only be an Object or null`（当前已知红点）；
- CI 同样复现；node 缺席环境输出 SKIP。

---

## 3. Phase 1（T1）：defineProperty 语义修复

**目标**：属性描述符符合 ES 规范（`ValidateAndApplyPropertyDescriptor`），探针推进到下一关卡。

**已确认事实**：`engine.Object` 接口层只有 `Get/Set/Keys/Delete`，无描述符概念
（`internal/engine/engine.go:84-94`）——修复必须下沉到对象存储层，不是只改
`object_methods.go` 的函数体。

### 任务卡

| ID | 子任务 | 改动文件 | 内容 | 规模 |
|----|--------|----------|------|------|
| T1.1 | 存储层审计（只读） | `internal/engine/interpreter/`（object 实现、`shape.go`） | 摸清属性存储结构、Set/Delete/Keys 实际路径、IC 对"属性可写"的假设点；产出审计记录（附在 gap 台账首篇） | 0.5d |
| T1.2 | 描述符模型 | 对象存储实现 + `shape.go` | 属性存储引入 `writable/enumerable/configurable` 标志（accessor 已有 `SetAccessor` 路径，打通互斥）；`defineProperty` 按部分描述符合并——未出现字段保留现值，accessor↔data 转换丢弃对侧 | 1.5-2d |
| T1.3 | 标志执行 | 存储层 Set/Delete/Keys 路径 | `writable:false` 赋值拦截（非严格静默 / 严格 TypeError）；`configurable:false` 拦截删除与重定义；`Keys()` 过滤 `enumerable:false`（接口文档已如此承诺，需实现兑现） | 1d |
| T1.4 | 校验 | `object_methods.go` | getter 与 `value/writable` 同现 → TypeError；非可配置属性非等值重定义 → TypeError；不可扩展目标定义新键 → TypeError。`Object.defineProperties` 复用同一路径；审计 `Reflect.defineProperty` 是否存在，存在则共享，不存在记 gap | 0.5-1d |
| T1.5 | shape/IC 联动 | `shape.go` + 相关 IC | 冻结/重定义属性触发 shape 转移与 IC 失效；`--ic-stats` 命中率对照；**jitdiff 三 tier 零失配**（属性写入是 JIT 热路径，硬门禁） | 1d |
| T1.6 | 测试收口 | `internal/engine/interpreter/object_methods_test.go` 等 | 表驱动矩阵：描述符字段出现/缺失组合 × {新建, 等值重定义, 冻结后重定义, 不可扩展目标, accessor↔data 转换}；7 行复现进 `tests/compat/node22/` 差分；test262 property descriptor 子集 | 1d |

**验收（全部满足）**：

```bash
CGO_ENABLED=0 go test ./internal/engine/interpreter -run 'Descriptor|DefineProperty' -v
CGO_ENABLED=0 go test ./internal/engine/interpreter/jitdiff/ -count=1   # 三 tier 零失配
CGO_ENABLED=0 go test ./... -count=1
bash tests/conformance/vue-sfc/run.sh   # 失败点不再是 prototype TypeError（推进一关）
```

---

## 4. Phase 2（T2）：驱动式修复循环

**目标**：逐关推进探针至 `COMPILER_SFC_OK`。关卡数量不可预知（defineProperty 之后
大概率还有 postcss 深层 / babel 解析边角），用机制限定范围而非预付估计。

**改动文件**：

| 文件 | 动作 |
|---|---|
| `tests/conformance/vue-sfc/gaps.md` | 新增（gap 台账：编号 / 现象 / 最小复现 / 规范依据 / 修复 commit） |
| 各关卡对应的引擎实现文件 | 按需 |

**每关标准循环**（顺序不可颠倒）：

1. probe 失败输出 → 提取现象，登记 gap（含完整栈首帧）；
2. 缩到**独立最小复现**（独立 `.cjs`/`.mjs` 或 Go 单测，脱离 compiler-sfc）；
3. 差分用例**先写先红**（进 `tests/compat/node22/` 或对应包单测）；
4. 修复（仍遵守规范对齐方向，不加行为开关）；
5. gate 复跑确认推进；全量回归 + jitdiff 若动热路径。

**止损线**：某关卡属于大型整块缺失（如某 builtin 全缺、或需新语言特性）时，
停止硬啃，升级为独立计划评审——不允许为一个 demo 目标在引擎里开大洞。

**验收**：`run.sh` 双跑输出一致，含 `COMPILER_SFC_OK`；gaps.md 每条有闭环记录。

---

## 5. Phase 3（T3）：正则引擎加固（与 T2 并行）

**目标**：高频朴素正则的语义精确性 + 回退引擎护栏。实证依据：闭包 601 字面量
以 `i/g/m` 为主，`/u /y /s`、`\p{}`、具名组、后行断言实测 0 用量。

| ID | 子任务 | 改动文件 | 内容 | 规模 |
|----|--------|----------|------|------|
| T3.1 | 语料提取 | `tools/extract-regex-corpus.mjs`（新增）+ `internal/engine/regex/testdata/corpus.txt` | 扫描 fixture 依赖闭包，提取正则字面量（含 flag）与 17 个动态构造形态，落 testdata | 0.5d |
| T3.2 | 双引擎对拍 | `internal/engine/regex/parity_test.go`（新增） | 同一 pattern 分别经 RE2 翻译层与回溯引擎执行，结果必须一致；需要测试钩子显式选择引擎路径（暴露内部构造或编译选项）；语料 + 构造用例逐条对拍 | 1d |
| T3.3 | 回溯护栏 | `internal/engine/regex/backtrack.go` | 回溯步数上限（防灾难性回溯挂死构建）；超限行为定义为"匹配失败 + 计数器"（不抛错，语义保守）；护栏用例：经典指数 pattern 在上限内返回 | 1d |
| T3.4 | flag 差分 | `internal/engine/regex/regex_test.go` | `i` 的 unicode 大小写折叠边界、`m` 的行锚语义，逐类 node 差分断言（沿用 `bt_debug_test.go` 的"期望值以 V8 实测为准"注释约定） | 0.5d |

**验收**：

```bash
CGO_ENABLED=0 go test ./internal/engine/regex/... -count=1   # 对拍零差异 + 护栏绿
```

---

## 6. Phase 4（T4）：official 编译后端

**目标**：`aluka build --target=web --vue-compiler=official` 用官方 compiler-sfc 编译
`.vue`，产物进入既有 emit/define/chunk 管线。

**已确认事实**：`graph.Build(vm *interpreter.VM, resolver *module.Resolver, entry string)`
已持有 VM（`graph.go:72`），`graph.go:132` 是唯一 `TransformSFC` 调用点；
`module.NewLoader(ctx engine.Context)`（`loader.go:68`）可加载 ESM 驱动模块。

| ID | 子任务 | 改动文件 | 内容 | 规模 |
|----|--------|----------|------|------|
| T4.1 | 后端接口 | `internal/bundler/vue/backend.go`（新增）、`sfc.go` | `type Compiler interface { Transform(src, name, path string) (string, error) }`；现有 `TransformSFC` 包装为 `SubsetCompiler`；行为不变 | 0.5d |
| T4.2 | official 驱动器 | `internal/bundler/vue/official.go` + `driver.mjs`（go:embed） | driver 经 `module.NewLoader` 导入，调用 `parse/compileScript/compileTemplate`（`bindingMetadata` 贯通），结果经 `globalThis.__alukaSfcResult` 回传；`graph.Build` 加编译后端参数并透传到调用点 | 1.5-2d |
| T4.3 | CLI 开关 | `cmd/aluka/build.go`（buildFlags/buildOptions） | `--vue-compiler=subset|official`（默认 subset，非法值报错）；`bundleWebEntry` 透传 | 0.5d |
| T4.4 | 错误映射 | `official.go` | JS 异常（name/message/stack 首帧）转带 `.vue` 文件位置的 Go 构建错误；**失败即报错，禁止静默回退 subset** | 0.5d |
| T4.5 | 集成验证 | `cmd/aluka/main_test.go`、conformance | `TestWebBuildVueOfficialBackend`：official 编译 demo → SSR 断言 script setup 产物路径（`__isScriptSetup`/hoisted 标记）→ ESM/CJS/UMD 三格式；`run.sh` 扩展 official 分支 | 1d |

**验收**：demo 以 official 编译，浏览器与 Node SSR 双验证通过；subset 路径产物与改动前逐字节一致（回归保障）。

---

## 7. Phase 5（T5）：性能与收官

| ID | 子任务 | 内容 | 规模 |
|----|--------|------|------|
| T5.1 | 基线数据 | 冷/热（`.aluka-cache` 复用 demo `node_modules` 字节码缓存）构建耗时 vs Node 基线，数据写入本计划附录 | 0.5d |
| T5.2 | 度量 | regex 编译缓存命中率、回退引擎触发率（临时 instrumentation 或 `--monitor` 扩展）；据数据决定是否做针对性优化 | 0.5-1d |
| T5.3 | 文档 | README（双后端说明 + "构建即执行依赖代码"安全声明）、`docs/static-build-plan.md` 状态表、CLI help | 0.5d |

**验收**：性能数据入档；文档与实际行为一致；`make test` 全绿。

---

## 8. 依赖与顺序

```
T0 ──► T1 ──► T2（关卡循环）──► T4 ──► T5
              ▲
T3 ───────────┘（并行，不阻塞主线）
```

- T4 硬前置 = T2 全绿（官方后端依赖引擎跑通编译器链）；
- T3 与 T2 并行推进，互不阻塞；
- 每个 Phase 结束打 tag 式提交（`feat(engine):` / `test(conformance):` / `feat(bundler):` 前缀，对齐仓库约定）。

---

## 9. 风险登记册

| 风险 | 概率 | 缓解 |
|------|------|------|
| T2 关卡数远超预期 | 中 | 止损线机制（§4）；每关独立可交付，可停在任意绿点沉淀价值 |
| T1 触动 IC/JIT 热路径引发回归 | 中 | jitdiff 三 tier 硬门禁 + `--ic-stats` 对照 + test262 |
| official 后端构建耗时不可接受 | 低 | 默认 subset 零影响；缓存复用 + T5 数据先行再定推荐策略 |
| driver 的 VM 互操作出现新语义缺口 | 中 | 本质是 T2 的延伸关卡，同一差分机制覆盖 |
| 语料对拍暴露两引擎既有分歧 | 低 | 分歧即 bug，按差分修复，不引入开关 |

---

## 10. 总门禁（每 Phase 收口必跑）

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go test ./internal/engine/interpreter/jitdiff/ -count=1   # 动引擎热路径时
bash tests/conformance/vue-sfc/run.sh
make lint   # golangci-lint 可用时
```

**项目级完成定义**：探针双跑一致输出 `COMPILER_SFC_OK`；demo 支持 `--vue-compiler=official`
三格式产物并通过浏览器/SSR 验证；全部 gap 闭环记录在 `tests/conformance/vue-sfc/gaps.md`。
