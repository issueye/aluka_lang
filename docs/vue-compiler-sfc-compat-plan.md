# Vue 官方 compiler-sfc 兼容与引擎加固计划

> 状态：~~分析定稿，待评审排期~~ → **已合入 main 并完成 M0-M5**（见 vue-compiler-sfc-dev-plan/merge-notes；2026-08-25 gap-closure-plan D6 回填）。
> 关联：`docs/static-build-plan.md`（web bundle / SFC 子集）、`docs/pi-compat-plan.md`（真实世界兼容）。
> 前置实证：2026-08-16 探针实测（见 §1）。

---

## 0. 结论摘要

- **不回滚任何既有代码**。Go SFC 子集编译器、AST define 机制、vendored Vue fixture 全部保留，
  官方 compiler-sfc 以"第二后端"形态叠加，默认关闭。
- **第一个确定性阻塞是 `Object.defineProperty` 部分描述符语义缺失**（7 行可复现），
  这是引擎一致性 bug，与本计划是否推进无关都该修。
- **正则引擎的实证压力远低于预期**：compiler-sfc 整个依赖闭包（含 @babel/parser、postcss）
  的 601 个正则字面量中，`/u`、`/y`、`/s` 真实用量为 0，无 `\p{}` 属性转义、无具名组、
  无后行断言字面量。babel 的 unicode 处理走查表不走正则。本计划对正则的投入是
  **正确性护栏与差分对拍**，不是新特性开发。
- **核心方法论是"驱动式发现"**：把 compiler-sfc 探针固化为 aluka vs node 差分 gate，
  修一个 bug、推进一关，直到 `COMPILER_SFC_OK`。不预支修复未验证的猜测。

---

## 1. 实证基线

### 1.1 探针记录

探针脚本（`parse` + `compileScript` + `compileTemplate` 编译一个 `<script setup>` SFC）：

| 运行时 | 结果 |
|---|---|
| Node 22 | 全通。产出官方语义：script setup 展开 + `__isScriptSetup` 标记 + hoisted 模板 render |
| Aluka（`bin/aluka`） | 深入依赖链后失败于 postcss 懒加载类初始化 |

Aluka 侧已验证通过的环节（这些不用重做）：

- `vue/compiler-sfc` 子路径 exports 解析（import 条件 → `index.mjs`）
- ESM ↔ CJS 互操作（`_interopRequireDefault` / `_interopRequireWildcard` / WeakMap 缓存路径）
- 834 KB rollup CJS 产物的解析与执行（lexer/parser/compiler/VM 全链路）
- `namespace` 上下文关键字回落（本轮 `internal/engine/parser/stmt.go` 修复，实测必需）

### 1.2 阻塞点最小复现（7 行）

```js
function Node(opts) { this.opts = opts; }
Node.prototype.walk = function () { return 'walked'; };
Object.defineProperty(Node, 'prototype', { writable: false });
console.log(typeof Node.prototype);
// node:  "object"     aluka:  "undefined"   ← 缺陷
```

postcss 的 `_createClass` 用部分描述符冻结类原型的可写性，随后
`_inheritsLoose` 中 `Object.create(superClass.prototype)` 拿到 `undefined`，
抛出探针看到的 `Object prototype may only be an Object or null`。

### 1.3 依赖闭包

`vue/compiler-sfc` → `@vue/compiler-sfc` → `@vue/compiler-core`（→ `@babel/parser`、
`entities`、`estree-walker`、`source-map-js`）+ `postcss` + `magic-string`。
**全部纯 JS、零原生依赖**——fixture 中已存在（当前未使用），无新增 vendor 需求。

### 1.4 正则需求扫描（关键输入）

对闭包内 8 个主要分发文件扫描（启发式字面量提取 + `/*__PURE__*/` 注释去噪）：

| 特性 | 实测用量 | 结论 |
|---|---|---|
| 正则字面量总数 | ~601（去噪后） | 量大但形态朴素 |
| `i` / `g` / `m` flag | 60 / 74 / 2 | 常规形态，现引擎已覆盖 |
| `/u` unicode 模式 | **0** | 无需求 |
| `/y` sticky | **0** | 无需求 |
| `/s` dotAll | 0 | 回溯引擎已支持，未用到 |
| `\p{...}` 属性转义 | 0 | 无需求 |
| 具名组 `(?<name>)` | 0 | 无需求 |
| 后行断言 `(?<=`/`(?<!` | 0（字面量） | 回溯引擎已支持（express 依赖需要过） |
| `\u{...}` 码点转义 | 0 | 无需求 |
| 动态 `new RegExp(...)` | 17 处 | 均为简单字符串拼接模式（前缀/后缀转义） |

**解读**：babel-parser 的 identifier 合法性判断走 unicode 查表而非正则；
"按 compiler-core 需要加固正则"的真实含义是**高频调用的朴素正则必须语义精确、
性能稳定、且有灾难性回溯护栏**，而不是补 unicode 高级特性。

---

## 2. 关键点一：既有代码处置——不回滚

### 2.1 保留清单

| 既有资产 | 处置 | 理由 |
|---|---|---|
| Go SFC 子集编译器（`internal/bundler/vue/sfc.go`） | **保留，保持默认后端** | 快（纯 Go 微秒级）、离线、行为可控、零引擎依赖；Vite 式产物契约（`import { h } from 'vue'`）与官方后端产物同构，demo/测试基建全部沿用 |
| AST define 机制（`emit/printer.go` + `webProductionDefines`） | 保留 | 与编译后端正交：无论谁生成 render，`process.env.NODE_ENV` 都需替换 |
| vendored Vue fixture（demo `node_modules/`） | 保留 | 官方后端直接复用其中的 `@vue/compiler-sfc`，无需新增 vendor |
| `namespace` 解析回落（`parser/stmt.go`） | 保留 | 已被实测证明为必需；顺带补表驱动单测（评审遗留项） |

### 2.2 需要改造的接缝

1. **后端抽象**：`vue.TransformSFC` 收敛为接口（`Transform(src, name, filename) (string, error)`），
   子集实现保持现签名；新增 official 实现。`graph.Build` **已持有 `vm *interpreter.VM`**
   （`graph.go:72`），注入点现成——在 Build 增加编译后端选项（或包级注册），`graph.go:132`
   的调用点改为走接口。
2. **CLI 开关**：`aluka build --target=web --vue-compiler=official`（默认 `subset`）。
   两个后端产物语义不同（见 §5.4），必须显式选择，**禁止静默回退**——official 失败就报错，
   不能悄悄落回子集产出不同语义的代码。
3. **错误映射**：official 后端抛出的 JS 异常需转成带 `.vue` 文件位置信息的 Go 构建错误
   （探针驱动确定字段：`name/message/stack` 已可用）。

### 2.3 回滚策略

后端选择是构建期开关，默认值不变（subset）。official 后端出问题等于功能不存在，
主路径零影响；引擎侧修复（§3）全部是"向规范对齐"方向，不引入行为开关。

---

## 3. 关键点二：补齐 `Object.defineProperty` 语义

### 3.1 现状缺陷（`internal/engine/interpreter/object_methods.go:57-96`）

```go
// 数据属性路径（现状）
if hasOwn(desc, "value") {
    v, _ := desc.Get("value")
    _ = o.Set(key, v)
} else {
    _ = o.Set(key, engine.Undefined())   // ← 描述符未含 value 时，把现值重置为 undefined
}
```

三条缺陷：

1. **部分描述符丢 value**（已复现）：描述符只给 `writable/enumerable/configurable`
   时，未指定的 `value` 必须保留现值，当前被重置为 `undefined`。
2. **属性标志不执行**：`writable:false` 不生效（赋值仍可穿透）、`enumerable:false`
   不影响 Keys 遍历、`configurable:false` 不阻止重定义/删除。
3. **校验缺失**：getter 与 `value/writable` 混用应抛 TypeError；非可配置属性的
   非等值重定义应抛 TypeError；非可扩展对象定义新属性应抛 TypeError。

### 3.2 规范语义对照（修复验收标准）

依据 ES 规范 `ValidateAndApplyPropertyDescriptor`（ES2023 6.2.5）：

| 描述符形态 | 语义 |
|---|---|
| 部分字段 | 只覆盖出现字段，其余保留现值（含 accessor↔data 转换时丢弃对侧字段） |
| `value` 与 `get/set` 同现 | TypeError |
| `configurable:false` 后重定义（非等值） | TypeError |
| 目标不可扩展（`Object.preventExtensions`）且键不存在 | TypeError |
| `writable:false` 后赋值 | 静默失败（非严格）/ TypeError（严格模式） |

### 3.3 修复设计要点

- 属性存储引入完整描述符（value / writable / enumerable / configurable / accessor），
  `Set` 与删除路径先查标志；`Keys`/枚举路径过滤 `enumerable:false`。
- **风险点：shape/IC 联动**。冻结属性会使隐藏类转移失效假设（IC 快路径假设可写），
  修复必须让 `defineProperty` 触发相关 shape 的 IC 失效，跑 `--ic-stats` 与
  jitdiff 三 tier 确认无失配（AGENTS.md 测试约定）。
- `Object.defineProperty` 与 `Object.defineProperties`、`Reflect.defineProperty`
  共享同一内部实现，一次修复三处受益。

### 3.4 测试矩阵

1. 单元表驱动：`{value, writable, enumerable, configurable, get, set}` 出现/缺失的
   组合矩阵 × 新建属性/重定义/冻结后重定义/不可扩展目标。
2. **node22 差分**：7 行复现（`_c.cjs` 形态）进 `tests/compat/node22/`——项目差分
   传统对齐。
3. test262 子集回归（property descriptor 相关 chapter）。
4. 全仓回归 + jitdiff 三 tier（属性写入路径是 JIT 热路径，guard 变更需零失配）。

---

## 4. 关键点三：正则引擎加固与优化

### 4.1 现状

两层架构（`internal/engine/regex/`）：

- **主路径**：JS 语法 → Go RE2 翻译（`translate.go`），复用标准库，性能好；
- **回退路径**：自研回溯引擎（`backtrack.go`），覆盖 lookaround（含后行）与反向引用
  （express 生态需要），支持 dotAll；
- 已有编译缓存（`regex.go`，sync 复用）。

### 4.2 加固清单（按优先级）

| 级别 | 项 | 说明 |
|---|---|---|
| P0 | **双引擎对拍差分** | 同一 pattern 在 RE2 翻译层与回溯引擎分别执行，结果必须一致；语料 = 闭包内提取的 601 个字面量 + 17 个动态构造形态。回退切换边界（何种 pattern 落回溯引擎）要有明确测试 |
| P0 | **灾难性回溯护栏** | 回溯引擎加步数上限/超时（编译器场景 pattern 固定输入可控，风险低，但护栏必须有，防止单个畸形输入卡死构建） |
| P1 | **高频朴素正则的正确性精确化** | `i` flag 的 unicode 大小写折叠边界、`m` flag 行首尾、字符类转义边界——用量最大（i=60/g=74），逐类对照 node 差分用例 |
| P1 | **动态 RegExp 构造语义** | `new RegExp(string, flags)` 的转义/异常路径（17 处实测使用），含非法 pattern 的 SyntaxError 形态对齐 |
| P2 | `/u` 码点语义 | **实测无需求**（闭包内 0 用量），列入 Node 兼容主线（pi-compat）按真实包驱动排期，不占本计划带宽 |
| P2 | sticky `lastIndex` 语义 | 同上，实测 0 用量 |

### 4.3 性能度量与优化

- 基线：探针全通后，测"编译单个 SFC"耗时 vs Node（预期有差距，VM vs 原生 JIT）。
- 度量点：regex 编译缓存命中率、回溯引擎触发率（理想情况编译器链路几乎不落回溯引擎）。
- 优化仅在度量后做（ suspected: 字面量 pattern 的预编译复用已有；避免翻译层重复分配）。
- **量化目标定基线后再定**（M5），避免拍脑袋承诺倍数。

---

## 5. 补充分析点

### 5.1 驱动式发现机制（方法论核心）

修复 defineProperty 后**必然还有下一关**（postcss 深层 / @babel/parser 的解析边角），
预先枚举是浪费。机制：

1. `tests/conformance/vue-sfc/probe.mjs` 入库（即本轮探针，参数化输入 SFC）；
2. `run.sh` 双跑 aluka 与 node，输出差分（沿用 webbuild runner 模式）；
3. 每次失败 → `docs/` 或 runner 输出记录 gap（现象 / 最小复现 / 规范依据）→ 修复 → gate 推进；
4. gate 变绿（`COMPILER_SFC_OK`）即 M2 完成。

### 5.2 引擎语义风险预判（不预支修复，仅备查）

探针已越过 WeakMap、`Symbol.iterator` for-of、模板字面量、class 表达式；
下一批高概率关卡：class static block / `Symbol.toStringTag` /
`String.prototype.replace` 的 `$<name>`/`$&` 替换模式 / 对象字面量 getter 简写 /
`Array.prototype` holes 语义。每项以差分用例驱动，命中才修。

### 5.3 差分测试基建落地

- 新增 `tests/conformance/vue-sfc/`：`probe.mjs` + `run.sh`（node 不可用则 SKIP，
  对齐既有 conformance 约定）；
- defineProperty 修复用例进 `tests/compat/node22/`（manifest/probe/diff 结构沿用）；
- CI 接入点复用现有 conformance 触发方式，不新增 workflow。

### 5.4 双后端产物契约差异（bundler 集成细节）

| 维度 | subset（默认） | official |
|---|---|---|
| script 形态 | 选项式 `export default` + `__sfc__.render` | `compileScript` 展开（含 `<script setup>`、bindingMetadata） |
| 模板产物 | `render(_ctx)` + `_h(...)` 子集调用 | `createElementBlock`/`openBlock`/hoisted 优化 |
| 支持范围 | 模板子集，`<style>`/`<script setup>`/指令构建期拒绝 | 官方全集 |
| 依赖 | 仅 `vue` runtime helper（~2.7 MB 闭包） | 需 compiler 链（fixture 已含） |

official 产物 import `vue` 后进入 graph 正常解析——与 subset 同一出口，
emit/define/chunk 管线零改动。

### 5.5 构建性能与缓存

- VM 内执行 compiler 链的耗时是 official 后端主要成本；
- `@vue/compiler-sfc` 等依赖位于 demo `node_modules/`，**`.aluka-cache` 字节码缓存
  可复用**（模块级缓存键不变），二次构建应显著快于冷启动——列入 M5 度量。

### 5.6 安全与确定性边界

official 后端在构建期执行 node_modules 内任意 JS——权限与 `aluka run` 相同，
无新增攻击面，但需在 CLI help 与文档明示（"构建即执行依赖代码"）；
不自动启用、不在 `--target=web` 默认路径隐式触发。

---

## 6. 里程碑

| 里程碑 | 内容 | 完成定义（DoD） |
|---|---|---|
| M0 差分 gate | `tests/conformance/vue-sfc/` 探针 + 双跑 runner 入库 | 本地与 CI 均能复现当前"红"（defineProperty TypeError）；node 缺席时 SKIP |
| M1 defineProperty | 描述符语义修复（§3.3） | 矩阵单测 + node22 差分 + test262 子集 + jitdiff 三 tier 零失配全绿；探针推进到下一关卡 |
| M2 驱动式修复循环 | 逐关修复至探针全通 | `run.sh` 输出 `COMPILER_SFC_OK`；每关有 gap 记录与最小复现 |
| M3 正则加固 | 双引擎对拍 + 回溯护栏 + 朴素正则精确化 | 601 字面量语料对拍零差异；护栏有超时测试；i/m flag 差分用例绿 |
| M4 official 后端 | `--vue-compiler=official` + 错误映射 + 后端抽象 | 真实 demo 以 official 编译出 ESM/CJS/UMD 且浏览器/SSR 验证通过；失败不静默回退 |
| M5 性能与收官 | 耗时基线/优化 + 缓存复用度量 + 文档 | 冷/热构建耗时数据入库文档；README/static-build-plan 更新后端说明 |

依赖顺序：M0 → M1 → M2 →（M3 可与 M2 并行）→ M4 → M5。

---

## 7. 风险与回滚

| 风险 | 缓解 |
|---|---|
| M2 关卡数量未知（可能远不止 defineProperty 一关） | 驱动式机制天然限定范围；每关独立可交付，随时停在任意绿点 |
| defineProperty 修复触动 shape/IC/JIT 热路径 | jitdiff 三 tier 门禁 + `--ic-stats` 对照（AGENTS.md 硬约定） |
| official 后端构建耗时不可接受 | 默认 subset 不受影响；缓存复用（§5.5）；性能数据先行再决定是否推荐 |
| 引擎修复引入行为回归 | 全部为规范对齐方向；test262 + node22 差分 + 全仓 `go test ./...` 门禁 |

---

## 8. 验证命令汇总

```bash
# 差分 gate（M0 起）
bash tests/conformance/vue-sfc/run.sh

# defineProperty 修复验证（M1）
CGO_ENABLED=0 go test ./internal/engine/interpreter -run TestDefineProperty -v
CGO_ENABLED=0 go test ./internal/engine/interpreter/jitdiff/ -count=1   # 三 tier 零失配

# 正则对拍（M3）
CGO_ENABLED=0 go test ./internal/engine/regex/... -count=1

# 全量回归（每个里程碑）
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./... -count=1

# official 后端端到端（M4）
go run ./cmd/aluka build --target=web --vue-compiler=official \
  --outdir demo/web-bundle-vue-demo/dist demo/web-bundle-vue-demo/index.html
```
