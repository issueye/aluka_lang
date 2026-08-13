# ADR：模块管线分层——单一分类权威、单向阶段流、扩展名优先

- 状态：已接受（Accepted）
- 日期：2026-08-13
- 关联：docs/module-pipeline-layers-development-plan.md；`internal/runtime/module`
  （SourceUnit/分类器）、`internal/bundler/{graph,shake,compile}`、`cmd/aluka/build.go`

## 现状（Context）

此前 JS/TS 解析、ESM/CJS 判定、tree-shake/minify、ESM lower、wrapper 与字节码编译
混合在同一批函数中：

1. 同一源码在 build 管线中被多次 `ParseModule`（compile → graph 依赖收集 → shake →
   minify 各解析一次）。
2. `.ts` 先按 ESM 处理、随后 `HasESMDecls` 又回退 CJS；`Resolver.ModuleType` 字符串
   判定与 bundler 的 AST 语法判定并存，两套权威。
3. tree-shake 删除的 import/export 会被 minify 从原始源码重新解析恢复，导致
   「构建成功但产物运行时 `module not found in payload`」。
4. `TransformESMToCJS` 反射原地改写共享 AST，重复/并行消费存在状态污染风险。
5. runtime loader 自行组合 TS check、HasESMDecls、ESM 回退、transform、wrapper，
   run 与 build 的分类行为不一致。

## 决策（Decision）

**建立显式 SourceUnit 中间表示，模块管线改为单向阶段流。**

- 每个源文件只读取、解析一次，结果保存在 `SourceUnit`（携带 `SourceKind`、
  `ModuleKind`、`TransformStage`、AST、TLA 状态）。
- 模块类型采用**扩展名优先**：`.mjs/.mts` 固定 ESM，`.cjs/.cts` 固定 CJS，`.json`
  独立；仅隐式 `.js/.ts` 由 `package.json` type 决定，并允许一次 AST 语法提升。
  扩展名判断统一小写规范化。
- 阶段固定顺序：`parse → classify → shake → minify → lower → wrap → compile`；
  `TransformStage` 只增不减（`MarkStage` 校验），ESM lower 先深拷贝再变换。
- graph 只做解析与依赖收集（延迟编译），优化与最终编译在 buildOne 统一执行；
  度量编译使用 `ast.DeepCopy` 克隆，不破坏共享 AST。
- runtime loader 与 bundler 共用同一前端入口（`ParseFileUnit`/`ParseSourceUnit`），
  CJS 保留字符串 wrapper 并记录形参契约。
- 移除 `hasESMSyntax` 文本扫描回退；显式 `.cjs/.cts` 含 ESM 语法给明确诊断。

## 理由（Rationale）

1. **单一事实来源**：分类与阶段状态由元数据携带，任何阶段不重新读取原始源码推断，
   从根上消除「恢复已删除依赖」「同一文件多重分类」类 bug。
2. **扩展名优先贴近 Node/Bun**：`.mjs/.cjs/.ts/.cts` 的语义是确定的；语法回退只
   保留在 typeless 的 `.js/.ts` 兼容路径，行为可预期。
3. **可缓存、可验证**：缓存 key 纳入 pipelineVersion + SourceKind + ModuleKind，
   payload v2 持久化分类上下文，旧缓存/旧产物被明确版本隔离。
4. **渐进式**：保留 parser 内 TS strip-only（类型擦除），不引入完整 TS 类型 AST，
   控制迁移风险；独立 tsstrip pass 作为后续可选方向。

## 验收（Acceptance）

- `CGO_ENABLED=0 go test ./... -count=1`、`go test -tags vmstackcheck
  ./internal/engine/interpreter/...`、jitdiff 三 tier 全部通过。
- 分类矩阵测试：`.js/.mjs/.cjs/.ts/.mts/.cts` × package type × 内容 × 大写变体。
- `--optimize` 产物与无优化产物输出一致（组合管线不互相覆盖）。
- runtime run 与 build 对同一 `.ts/.cjs` 的 TS/ESM 诊断一致。
- coding-agent 从 `src/aluka/cli.ts` 直接构建 plain/shaken/optimize 三档产物，
  `--version`/`--help`/offline 非交互路径通过。

## 非目标（Non-Goals）

- 不实现完整 TS 类型 AST 与独立 tsstrip pass（保留 parser 内 strip-only）。
- 不把 CJS wrapper 从字符串替换为 AST wrapper（保留 `WrapCJSSource`）。
- 不保证与 Node SEA / postject 的互操作（见 ADR：SEA）。
