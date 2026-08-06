# ADR：process.permission 与 process.report

- 状态：已接受（Accepted）
- 日期：2026-08-06
- 关联：docs/node22-full-api-development-plan.md §M9；internal/runtime/globals/
  process.go

## 现状（Context）

Node 22：

- **process.permission**：Permission Model 仅在 `--permission` 启动时启用，
  此时 `process.permission = { has(scope, reference) }`，用于门控
  fs/child_process/worker_threads/module 访问。默认（无 `--permission`）时
  `process.permission === undefined`。
- **process.report**：始终存在。属性面：compact / directory / excludeEnv /
  excludeNetwork / filename / reportOnFatalError / reportOnSignal /
  reportOnUncaughtException / signal；方法面：getReport() 返回结构化 report
  对象，writeReport([filename]) 把 JSON 落盘并打印
  `Writing Node.js report to file: <path>` / `Node.js report completed`
  （stderr）。

aluka：此前两者均缺失；无权限模型，也无 report 落盘能力。

## 决策（Decision）

1. **process.permission：提供 `has()` 方法面，恒返回 `false`（拒绝一切）**。
   完整权限模型（CLI `--permission` 解析、fs/child/worker/module 各模块的
   访问检查点）为永久非目标。
2. **process.report：实现完整方法面 + 属性面**。getReport() 返回形状与
   Node 一致的 report 对象（header 含 Node 同名的 23 个键，含 workers 数组
   等）；writeReport() 将 JSON 落盘（固定或生成文件名）并输出与 Node 一致
   的 stderr 提示行。

## 理由（Rationale）

1. 权限模型需要全链路钩子（每个文件/子进程/worker/模块入口的检查点），且
   Node 默认不启用（需 `--permission`）。实现它的成本与默认收益不符；
   `has()` 恒 false 是安全的 deny-by-default 默认，等价于 Node 以
   `--permission` 启动且未授予任何 scope。
2. report 是诊断面，形状对齐 + Go 标准库写 JSON 即可满足包生态的探测需求
   （如判断 `process.report` 存在、调用 writeReport 落盘）。
3. 任务要求提供 `has` 方法面，故 aluka 始终暴露该对象（而非 Node 默认的
   undefined），差异以 knownDifference 记录。

## 验收（Acceptance）

- [x] `process.report` 属性面/方法面与 Node 对齐；差分用例
      tests/compat/node22/diff/m9-4-process.cjs 通过（report 键面、getReport
      形状、writeReport 落盘 + stderr 提示）。
- [x] `process.permission.has(scope[, reference])` 存在且返回 boolean（不
      崩溃）。
- [x] 本 ADR 记录结论；权限模型不静默计入完成率。

## knownDifference

- **process.permission**：Node 默认（无 `--permission`）为 `undefined`；aluka
  始终暴露对象（`has()` 恒 false）。行为差异仅在"探测对象存在性"的代码中
  可见；`if (process.permission) ...` 模式 aluka 会走 has() 分支返回 false，
  Node 走 else 分支。
- **process.report**：getReport() 的动态字段值（cwd / processId /
  dumpEventTime / commandLine 等）为 aluka 环境值，与 Node 逐字段不同；键
  面与类型一致。writeReport 的 JSON 内容为 aluka 版本（Node 的 report 有
  V8 堆栈等字段，aluka 为最小集合）。

## 未来可逆性

若后续实现 `--permission` CLI 与模块级访问钩子，可将 `has()` 从恒 false
升级为真实门控；该决策可逆。report 字段可随运行时能力逐步补齐。
