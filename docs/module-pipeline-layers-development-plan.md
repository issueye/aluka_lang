# aluka 模块管线分层重构后续开发计划

> 文档版本：v1.0
> 日期：2026-08-13
> 当前实现基线：分支 `refactor/module-pipeline-layers` 第一阶段（SourceUnit 引入）
> 适用范围：JS/TS 前端解析、ESM/CJS 模块分类、打包器 graph/shake/minify、ESM lower / wrapper、
> 字节码编译、runtime loader、字节码磁盘缓存与 build --compile payload

## 1. 目的与完成定义

### 1.1 背景

此前 JS/TS 解析、ESM/CJS 判定、tree-shake/minify、ESM lower、wrapper 与字节码编译混合在
同一批函数里：同一源码在 build 管线中被多次 `ParseModule`，shake 与 minify 阶段互相覆盖
（tree-shake 删除的 import/export 会被 minify 从原始源码重新恢复），扩展名判定与 AST 语法
判定存在两套权威，`.ts` 先按 ESM 处理、随后 `HasESMDecls` 又回退 CJS。这导致真实项目打包
出现「构建成功但运行时 `module not found in payload`」、帧槽越界等难以定位的问题。

分层重构的目标是把管线改成**单向阶段流**，每个阶段只消费上一阶段的产物，不再从原始源码
重新推断已确定的信息。

### 1.2 目标分层

```text
Source (读取/BOM/大小写规范化扩展名)
   ↓
前端：JS 语法解析 + TS 类型擦除（当前为 parser 内 strip-only）
   ↓
模块分类：SourceKind（JavaScript/TypeScript/JSON）
          ModuleKind（ESM/CommonJS/Script）
   ↓
优化：tree-shake → minify（同一 ModuleIR，固定顺序）
   ↓
ESM lower（仅 ModuleESM）→ wrapper（ESM AST wrapper / CJS 字符串 wrapper）
   ↓
字节码编译 → 磁盘缓存 → payload 序列化
   ↓
runtime 执行（compiled / cached / fresh）
```

### 1.3 完成定义

同时满足以下六项才算本计划完成：

1. **单一事实来源**：每个源文件只被读取、解析一次；模块类型与阶段状态由显式
   `SourceUnit`/`EntryData` 元数据携带，任何阶段不得重新读取原始源码来推断。
2. **单向阶段流**：阶段顺序固定（parse → classify → shake → minify → lower → wrap →
   compile），`TransformStage` 只增不减；非法阶段跳转返回可诊断错误。
3. **扩展名优先**：`.mjs/.mts` 固定 ESM、`.cjs/.cts` 固定 CJS、`.json` 独立；仅隐式
   `.js/.ts` 允许由 `package.json` type 决定，并可做一次语法提升；大小写规范统一。
4. **TS 策略一致**：`.ts/.mts/.cts` 的 strip-only 诊断（enum/namespace 等）在 run、build、
   cache 三条路径行为一致。
5. **可缓存、可验证**：字节码缓存 key 与 payload manifest 至少携带规范化 SourceKind、
   ModuleKind 与管线/编译版本；任何分类或阶段语义变化都可使旧缓存失效或由版本拒绝。
6. **兼容性不回归**：现有 JS/CJS/ESM 语义、字节码 VM 行为、jitdiff 三 tier 与 build
   conformance 保持通过；`Parse/ParseModule/CompileFile/CompileProgramType` 旧 API 作为
   facade 保留，直至调用点全部迁移。

## 2. 当前基线

### 2.1 已完成（第一阶段，refactor/module-pipeline-layers）

| 范围 | 能力 | 关键文件 |
|------|------|----------|
| 显式 IR | `SourceKind` / `ModuleKind` / `TransformStage` / `SourceUnit` | `internal/runtime/module/source_unit.go` |
| 统一分类 | `Resolver.SourceModuleKind`（大小写规范化、扩展名优先） | `internal/runtime/module/resolver.go` |
| 前端入口 | `ParseSourceUnit`（TS policy + ParseModule 一次完成，记录 TLA/Stage） | `internal/runtime/module/source_unit.go` |
| 编译分层 | `ParseFileUnit` → `CompileSourceUnit`；`CompileFile` 委托新路径 | `internal/bundler/compile/compile.go` |
| 一次解析 | `graph.Build` 保存 `Result.SourceUnits`，依赖收集复用同一 AST | `internal/bundler/graph/graph.go` |
| shake 复用 | tree-shake 分析/重编译不再磁盘重 parse，直接消费 `SourceUnit` | `internal/bundler/shake/shake.go` |
| 阶段元数据 | `EntryData` 携带 `SourceKind`/`Stage`，删除含义模糊的 `Transformed bool` | `internal/bundler/compile/payload.go` |
| 所有权边界 | 已 lower/wrap 的模块不参与源 ESM 分析；shake 后模块不再被 minify 从原始源码恢复 | `internal/bundler/shake/shake.go`、`cmd/aluka/build.go` |

验证：`CGO_ENABLED=0 go test ./... -count=1`、`go test -tags vmstackcheck
./internal/engine/interpreter/...`、jitdiff 三 tier、`go build ./...` 均通过。

### 2.2 剩余关键缺口

> 本表随里程碑推进持续刷新。

| 类别 | 缺口 | 风险 |
|------|------|------|
| payload | Manifest/`EntryInfo` 尚未持久化 SourceKind 与 ModuleKind；`PayloadVersion` 未 bump | 产物无法自证分类上下文，旧 payload 与未来语义漂移 |
| runtime | loader 仍自行组合 TS check、HasESMDecls 判定、ESM 回退、transform、wrapper，未迁移到 `SourceUnit` | run 与 build 分类不一致的现状保留 |
| 优化阶段 | minify 尚未成为共享 AST 上的独立阶段（对 shake 后模块保护性跳过） | shake+minify 组合优化收益未恢复 |
| ESM lower | `TransformESMToCJS` 非纯函数：`rewriteImportedIdentifiers` 反射原地改写嵌套节点 | 重复调用/并行/缓存存在状态污染风险 |
| CJS wrapper | 仍是 `WrapCJSSource` 字符串拼接，未走 AST wrapper | 与 ESM AST wrapper 两套形态，`hasESMSyntax` 文本扫描误判 |
| 缓存 | 字节码缓存 key 未纳入 SourceKind/ModuleKind/pipeline version | 同源不同分类语义复用旧字节码 |
| TS 策略 | `checkUnsupportedTS` 仅 runtime loader 调用，build graph/compile 未调用 | 同一 `.ts` 文件 run 拒绝而 build 接受 |
| AST 复用 | 无 clone API，模块 IR 无法并行/复用 | 多入口去重、阶段幂等与并发编译受限 |

## 3. 实施原则

1. **不重新解析**：任何阶段不得从原始源码 `ParseModule` 恢复被之前阶段删除或改写的结构；
   需要时从 `SourceUnit` 复制或 clone。
2. **一次分类**：模块类型在 `ParseFileUnit`/`ParseSourceUnit` 一次确定；之后只在显式
   `.js/.ts` 兼容路径允许一次语法提升，绝不修改显式扩展语义。
3. **固定顺序**：`parse → classify → shake → minify → lower → wrap → compile`；`Stage`
   只增不减，顺序错误返回诊断而非静默跳过。
4. **Tier 0 为 oracle**：每个 pass 的语义等价性以字节码 VM（`--jit=off`）为权威，配合
   jitdiff 与现有优化等价性测试。
5. **版本纪律**：字节码布局变化才 bump `FormatVersion`；SourceKind/ModuleKind 或管线阶段
   元数据进入缓存/payload 时，bump 缓存 key 方案与 `PayloadVersion`。
6. **兼容 facade**：旧 API（`Parse`/`ParseModule`/`CompileFile`/`CompileProgramType`/字符串
   `ModuleType`）保留为薄封装，逐步迁移调用点后再删除。
7. **小步验收**：每个里程碑交付可独立运行的测试矩阵与产物验证，不把多个 pass 合并成一次
   「大重构」。

## 4. 路线与依赖

```text
P0 管线固化（第一阶段，已落地，收尾回归）
   ↓
P1 统一前端与编译 artifact
   ├─ 模块分类单一化
   ├─ TS 策略统一（run/build 一致）
   ├─ 缓存 key 纳入分类/版本
   └─ payload 持久化 SourceKind/ModuleKind
   ↓
P2 优化阶段化
   ├─ minify 接入共享 AST（恢复 shake+minify 组合）
   ├─ ESM lower 独立纯 pass
   ├─ CJS AST wrapper（或显式保留字符串 wrapper 并文档化）
   └─ AST clone + 阶段顺序校验
   ↓
P3 运行时/缓存/payload 对齐
   ├─ loader 迁移到 ParseSourceUnit + CompileSourceUnit
   ├─ 移除 hasESMSyntax 文本扫描
   └─ compiled payload 完整 round-trip 与 conformance 扩展
   ↓
P4 TS strip 独立与产品化
   ├─ parser 产物分离 / 独立 tsstrip pass
   └─ coding-agent 全量打包回归与 ADR
```

P1 与 P2 可部分并行（依赖关系仅在 shake/minify 顺序与 lower/wrap 阶段）；P3 依赖 P1 的
分类/缓存结论；P4 中独立 tsstrip 是可选范围（用户已确认采用渐进式，不强制拆完整 TS AST）。

工作量以单名熟悉代码库的工程师日估算，仅用于排序，不是交付日期承诺。

| 里程碑 | 主题 | 预计工作量 | 前置依赖 |
|--------|------|------------|----------|
| P0 | 管线固化（已完成） | 已完成 | 当前分支 |
| P1 | 统一前端与编译 artifact | 4-6 日 | P0 |
| P2 | 优化阶段化 | 5-8 日 | P0，可与 P1 并行 |
| P3 | 运行时/缓存/payload 对齐 | 4-6 日 | P1 |
| P4 | TS strip 独立与产品化 | 6-10 日 | P1-P3 |

## 5. 里程碑详情

### 5.1 P0：管线固化（已完成）

交付物已落地并验证：

- `SourceUnit`/`SourceKind`/`ModuleKind`/`TransformStage`；
- 扩展名优先分类器（大小写规范化）；
- `CompileFile` → `ParseFileUnit` + `CompileSourceUnit`；
- `graph.Build` 单次解析并保存 `SourceUnits`；
- tree-shake 复用共享 AST，不再磁盘重 parse；
- `EntryData` 阶段元数据，删除 `Transformed bool`；
- 阶段所有权边界与 minify 保护性跳过。

完成条件（已达）：全量 `go test ./...`、vmstackcheck、jitdiff、`go build ./...` 通过。

### 5.2 P1：统一前端与编译 artifact

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| P1-1 | 模块分类单一化 | 删除 `Resolver.ModuleType` 字符串权威，改为统一走 `SourceModuleKind`；`compile.go` 移除 `HasESMDecls`/`filepath.Ext(key)==".mjs"` 重复判定，只保留隐式 `.js/.ts` 一次提升 | build 与 runtime 对同一路径得到相同 ModuleKind；分类矩阵测试覆盖 |
| P1-2 | TS 策略统一 | `checkUnsupportedTS` 纳入 `ParseSourceUnit`（已部分实现），并在 graph/compile 路径确认调用 | 同一 `.ts/.mts/.cts` 在 run 与 build 中诊断一致；新增 run-vs-build 对拍用例 |
| P1-3 | 缓存 key 版本化 | 字节码缓存 key 纳入规范化 SourceKind、ModuleKind 与管线/编译版本（常量） | 同路径在不同分类/版本下不复用旧字节码；冷/热路径测试覆盖 |
| P1-4 | payload 持久化 | `EntryInfo`/Manifest 增加 SourceKind、ModuleKind；bump `PayloadVersion`；deserialize/compiled 运行读取 | payload round-trip 保持分类信息；entry/dependency 运行行为一致 |
| P1-5 | 分类矩阵测试 | `.js/.mjs/.cjs/.ts/.mts/.cts` × package type absent/module/commonjs × 纯脚本/import-export/TLA/type-only，含大写扩展 | 每格断言 ModuleKind、TLA、TS 诊断与编译语义 |

### 5.3 P2：优化阶段化

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| P2-1 | minify 接入共享 AST | `minifyModule` 不再磁盘重 parse，在 `SourceUnit` 上执行；移除对 shake 后模块的保护性跳过 | `--optimize`（shake+minify+bytecode-opt）产物与单独 shake/单独 minify 语义一致；tree-shake 后依赖不恢复 |
| P2-2 | ESM lower 独立纯 pass | 将 `TransformESMToCJS`/`rewriteImportedIdentifiers` 拆为独立 pass，输入输出分离或显式 clone；重复调用幂等 | lower 输入 AST 不变；CJS 模块绝不进入该 pass；幂等性测试 |
| P2-3 | CJS wrapper 形态决策 | 评估并实现 CJS AST wrapper，或显式保留 `WrapCJSSource` 并记录语义契约 | wrapper 只接收已分类 artifact；`hasESMSyntax` 文本扫描被分类器替代或明确隔离 |
| P2-4 | AST clone API | 为 `ast` 增加结构深拷贝（覆盖所有节点类型/接口/切片） | clone 后修改不影响原 AST；节点类型反射测试覆盖 |
| P2-5 | 阶段顺序校验 | `TransformStage` 只增不减校验；非法顺序返回诊断 | 乱序调用返回错误而非静默；测试覆盖 |

### 5.4 P3：运行时/缓存/payload 对齐

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| P3-1 | loader 迁移 | `loadESMModule`/`loadCJS` 改为 `ParseSourceUnit` + `CompileSourceUnit`，移除内部自组合的 TS check/HasESMDecls/回退/transform | run/require/动态 import 与 build 使用同一前端与编译管线 |
| P3-2 | 移除文本回退 | `hasESMSyntax`（strip comments 扫描）替换为分类器产物；显式 `.cjs/.cts` 含 ESM-only 语法给出诊断 | 无文本扫描误判路径；回归用例覆盖模板字符串/正则等边界 |
| P3-3 | payload round-trip | compiled artifact 保存并恢复 SourceKind/ModuleKind/TLA；entry 与依赖分类一致 | conformance build 套件（含 `.mts/.cts`、大小写扩展）通过 |
| P3-4 | conformance 扩展 | `tests/conformance/build` 与 `tests/compat/node22` 增加扩展名/TS 策略差分用例 | 与 Node/Bun 行为一致或按文档记录差异 |

### 5.5 P4：TS strip 独立与产品化

| ID | 工作项 | 交付物 | 完成条件 |
|----|--------|--------|----------|
| P4-1 | parser 产物分离（可选） | 评估并实现 parser 输出带 `SyntaxMode`/type-only 节点或显式 `ParseTS` API | 不破坏既有 `Parse/ParseModule` 调用；TS 语法回归通过 |
| P4-2 | 独立 tsstrip pass | 类型注解/泛型/`as`/`satisfies`/interface/type 擦除从 JS 语法解析中拆出（或文档化保留 strip-only 边界） | run/build/cache 三路径行为一致；test262 + coding-agent 回归 |
| P4-3 | coding-agent 全量打包 | 从 `src/aluka/cli.ts` 直接构建普通/tree-shake/`--optimize` 产物并复制资产 | `--version`/`--help`/offline 非交互路径通过；动态 import/worker 功能边界记录 |
| P4-4 | 文档与 ADR | `docs/adr/` 记录分层决策（单一分类权威、阶段单向流、扩展名优先）；更新本计划状态表 | ADR 与实现一致；CI 门禁无回归 |

## 6. 测试矩阵（贯穿全部里程碑）

| 维度 | 取值 | 断言 |
|------|------|------|
| 扩展名 | `.js/.mjs/.cjs/.ts/.mts/.cts` + 大写变体 | ModuleKind、SourceKind、TLA 与扩展一致 |
| 内容 | 纯脚本 / import-export / re-export / star namespace / TLA / 动态 import / type-only / enum / namespace | 分类不回退；诊断一致；执行等价 |
| package type | absent / module / commonjs | 仅隐式 `.js/.ts` 受影响 |
| 路径 | run / require / 动态 import / build graph / shake / minify / bytecode-opt / payload | 全路径同一分类与阶段 |
| 缓存 | cold / warm / 分类或版本变化 | 缓存 key 隔离，不复用旧语义 |
| 阶段 | shake→minify / minify→shake / 乱序 | 固定顺序生效；乱序诊断 |
| 优化组合 | 单独 shake / 单独 minify / `--optimize` | 与无优化 oracle 语义等价 |
| 差分 | jitdiff 三 tier、build conformance、node22 compat | 零失配或按文档记录差异 |

## 7. 风险与回滚

| 风险 | 说明 | 缓解 |
|------|------|------|
| parser 内 TS strip 依赖 | 当前类型擦除嵌入 parser，拆层可能改变 lookahead/ASI | 保留 `Parse/ParseModule` facade；P4-1 显式标记为可选 |
| ESM lower 非纯函数 | `rewriteImportedIdentifiers` 反射原地修改共享节点 | P2-2 优先落地 clone/纯化；阶段间禁止共享可变 AST |
| 字符串 wrapper | CJS 语义依赖 `WrapCJSSource` 词法包装 | P2-3 决策后再替换；期间保持行为不变 |
| 缓存复用错误 | 旧字节码可能跨分类复用 | P1-3 缓存 key 版本化，先于任何 lower/wrapper 变更 |
| 回滚开关 | 打包优化可用 `--no-tree-shake`/`--no-bytecode-opt` 回退 | 分层重构不改变 CLI 语义；任一硬门禁失败保留上一稳定 tier |

## 8. 验收顺序

1. P1 交付后跑 `go test ./...` + `tests/conformance/build/run.sh` + jitdiff；
2. P2 交付后跑 `--optimize` 组合产物与 shake/minify 单独对拍；
3. P3 交付后跑 payload round-trip 与 compiled artifact 执行；
4. P4 交付后跑 coding-agent 真实产物（普通/tree-shake/optimize）与资产 smoke；
5. 每阶段完成时更新本计划「当前基线」与完成状态，作为 CI 可追踪证据。
