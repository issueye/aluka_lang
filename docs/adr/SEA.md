# ADR：SEA（Single Executable Applications）——永久非目标（以编译产物模式替代）

- 状态：已接受（Accepted）
- 日期：2026-08-06
- 关联：docs/node22-full-api-development-plan.md §M9；cmd/aluka 的
  `aluka build --compile`（detectCompiledPayload / runCompiled）

## 现状（Context）

Node 22 的 SEA 允许把 JS 代码打包进 node 可执行文件：用 postject 把
NODE_SEA_BLOB 注入 node 二进制，`process.execPath` 即产物本体，并提供
`node:sea` 模块（`getAsset` / `getAssetAsSource` / `isSea` 等）读取注入资源。
配套还有 startup snapshot / code cache 等机制。

aluka 已有等价产物模式：`aluka build --compile` 把模块编译进 aluka 可执行
文件尾部（sha256 校验 footer），运行时 `detectCompiledPayload` 检测并
`runCompiled` 执行（见 cmd/aluka/main.go）。

## 决策（Decision）

**Node 形式的 SEA（postject 注入 node 二进制 + `node:sea` 模块）为永久非目标；
aluka 以自有 `build --compile` 编译产物模式作为替代方案。**

- 不实现 postject/NODE_SEA_BLOB 注入机制（与 aluka 二进制无映射关系）。
- 不提供 `node:sea` 模块（require 时按未实现处理），其用途由 `aluka build
  --compile` 覆盖。
- `process.execPath` 语义保持 aluka 自有产物模式，不假装成 SEA 单文件。

## 理由（Rationale）

1. SEA 依赖 Node 二进制自身的打包/加载机制（postject 注入、NODE_SEA_BLOB
   格式、startup snapshot）。aluka 二进制结构不同，复刻该格式没有互操作
   价值。
2. aluka 已实现的 `build --compile` 覆盖了 SEA 的核心使用场景：分发单个
   自包含可执行文件。它是比 SEA 更贴合 aluka 的替代方案。
3. `node:sea` 的 API 面与 aluka 的 embedded 资源模型不同构；提供虚假实现
   会误导开发者。

## 验收（Acceptance）

- [x] `aluka build --compile` 行为不回退（既有产物模式继续可用）。
- [x] `require('node:sea')` 明确报错（模块未实现），不静默返回空对象。
- [x] 本 ADR 记录结论；SEA 不静默计入完成率。

## knownDifference

- 无 `node:sea` 模块。
- Node SEA 产物的"注入式"打包流程与 aluka 的 `build --compile` 产物不同构，
  产物二进制不互通。
