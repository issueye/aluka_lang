# ADR：WebAssembly 与 WASI——go/no-go 结论

- 状态：已接受（Accepted）
- 日期：2026-08-06
- 关联：docs/node22-full-api-development-plan.md §M9；internal/builtin/wasi.go

## 现状（Context）

Node 22 内置 V8 的 WebAssembly 引擎，并基于 WASI preview1/unstable 提供
`node:wasi` 模块（WASI 类、getImportObject/start/initialize、46 个系统调用
导入函数）。Node 在模块加载时输出 `ExperimentalWarning: WASI is an
experimental feature`。

aluka 没有 WebAssembly 运行时：无 `WebAssembly` 全局对象，无法
`WebAssembly.instantiate()`。

## 决策（Decision）

**本次（M9）对 WASM 采取 no-go，对 WASI 采取"方法面 stub + 明确非运行"的
替代方案；未来引入纯 Go WASM 运行时是保留的、可逆的选项。**

1. **WASM（`WebAssembly` 全局 + Module/Instance/Table/Memory 等）：不实现**
   ——独立的解释器/JIT 子系统，超出 M9 范围；不采用 cgo 引入
   V8/Wasmtime/WasmEdge。
2. **WASI（`node:wasi`）：提供与 Node 22 对齐的类/方法面**：
   - `new WASI({version:'preview1'|'unstable'})`：options 校验与错误码/消息
     对齐 Node（ERR_INVALID_ARG_TYPE / ERR_INVALID_ARG_VALUE /
     ERR_OUT_OF_RANGE）。
   - `wasiImport`：46 个 preview1 系统调用函数；调用时抛
     `ERR_WASI_NOT_STARTED`（与 Node 中未成功 start 的行为一致）。
   - `start(instance)` / `initialize(instance)`：校验顺序与 Node 一致
     （started 标记 → instance/exports/memory → _start/_initialize）；
     memory 校验后真正执行 WASM 需要运行时，属当前非目标。
   - `getImportObject()`：`{ wasi_snapshot_preview1 | wasi_unstable }`。
3. **不冒充稳定 API**：WASI 以实验性方法面提供，实验地位由本 ADR 与
   internal/builtin/wasi.go 注释标记；aluka 不输出 ExperimentalWarning
   （避免污染差分输出，Node 的警告行被 run-diff.sh 按 `(node:` 前缀过滤）。

## 理由（Rationale）

1. 纯 Go 生态有成熟的 WASM 解释器（如 wazero），未来 go/no-go 时优先纯 Go
   方案，不引入 cgo 运行时依赖。
2. WASI 的"类/方法面 + 明确非运行"是零运行时成本的兼容入口，可让依赖
   `require('wasi')` 做特性探测的包正常走通；而 WASM 执行面若虚假实现会
   造成不可诊断的错误，宁缺毋滥。
3. Node 本身将 WASI 标为 experimental——以方法面 stub 呈现是诚实且符合
   Node 语义的层级，不算夸大能力。

## 验收（Acceptance）

- [x] `require('wasi')` 可加载，导出 `WASI`；类/方法面与 Node 对齐。
- [x] 差分用例 tests/compat/node22/diff/m9-3-wasi.cjs 通过（方法面 + 成功/
      失败 + 错误码对齐）。
- [x] start/initialize 对无效 instance/exports/memory 的错误与 Node 逐字段
      一致；已启动标记语义一致（ERR_WASI_ALREADY_STARTED）。
- [x] 本 ADR 记录 go/no-go 结论，不静默计入完成率。

## knownDifference

- 无 `WebAssembly` 全局：`WebAssembly.instantiate` / `new
  WebAssembly.Instance` 不可用（WASM no-go 的直接结果）。
- `wasi.start()` / `initialize()`：即使内存校验通过也无法真正执行 WASM
  （抛 ERR_WASI_NOT_IMPLEMENTED 或等价错误）。
- 不输出 ExperimentalWarning（Node 在模块加载时输出）。
- unstable 版本的导入函数名以 preview1 集合近似（Node 内部同名绑定）。
- `wasiImport` 函数在 start 后调用：Node 会执行真实系统调用，aluka 始终抛
  ERR_WASI_NOT_STARTED（aluka 无法到达"成功 start"状态）。

## 未来 go/no-go 更新条件

若引入纯 Go WASM 运行时（如 wazero）且其支持 WASI preview1，则可将 WASI
从 stub 升级为可执行实现，并重新评估 `WebAssembly` 全局面；该决策可逆。
