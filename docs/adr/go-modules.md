# ADR：单仓多 Go module + replace

- 状态：已接受（Accepted）
- 日期：2026-08-18
- 关联：子系统表面积拆分；`go.work`、各 `internal/*/go.mod`

## 现状（Context）

仓库曾是单一 module `github.com/aluka-lang/aluka`。包 DAG 已经分层（engine 不引用 runtime/builtin），但同一个 module 内任何包都能 import 任何 `internal/...`，无法在编译期阻止反向依赖，测试/CI 也无法按子系统隔离。

## 决策（Decision）

**单仓多 module：每个子系统一个 `go.mod`，import 路径保持 `github.com/aluka-lang/aluka/internal/<pkg>`，版本一律 `v0.0.0`，消费方用相对路径 `replace`。提交 `go.work` 作为团队约定。**

当前 module：

- 叶子：`engine`、`ipc`、`gui`、`cli`、`pkgmanager`；`monitor` 只依赖 engine
- 运行时：`runtime` → engine/ipc/gui；`builtin` → engine/runtime
- 工具链：`bundler` 生产代码只依赖 engine/runtime；`go.mod` 里的 builtin 仅测试（official Vue / webconfig fixture）。`project` → bundler/engine/runtime/builtin
- 根模块 `github.com/aluka-lang/aluka`：`cmd/aluka`、`tests/`、`demo/`、`bench/` 胶水，require 全部子模块

约束：

1. 不拆成多个 GitHub 仓库。
2. 不把 `internal/engine` 再切成 lexer/parser/jit 多个 module。
3. 不把 import 改成 `github.com/aluka-lang/aluka/engine`（另一次搬迁）。
4. 子模块不发 proxy、不打 tag；本地与 CI 都靠 `replace`（`GOWORK=off` 时仍能在子目录 `go test`）。
5. 跨模块新增 import 必须出现在对应 `go.mod` 的 `require` + `replace`。

## 理由（Rationale）

1. **显式依赖图**：反向边（例如 bundler → project）会变成 module 解析失败，而不是一次 `go test ./...` 里悄悄编译通过。
2. **保留 `internal/` 路径**：Go 的 internal 规则按 import 前缀生效；路径仍在 `github.com/aluka-lang/aluka/...` 下，兄弟 module 可以引用，同时避免 500+ 文件改 import。
3. **`replace` + `go.work`**：workspace 让仓库根 `go test ./...` 覆盖所有 `use`；`replace` 让单模块目录在 `GOWORK=off` 下仍可测，防止只靠 workspace 漏写 replace。
4. **不减小二进制**：`cmd/aluka` 链接期仍拉齐用到的模块；拆包收益是测试边界与依赖纪律。

## 验收（Acceptance）

- 仓库根（有 `go.work`）`CGO_ENABLED=0 go test $(go list -f '{{.Dir}}/...' -m)` 通过。根目录单独的 `./...` **不会**进入带子 `go.mod` 的目录，必须按 workspace 模块列出。
- `GOWORK=off` 下在 `internal/engine` 与 `internal/pkgmanager` 目录 `go test ./...` 通过。
- `go list -m all` 在根模块看到子模块经 replace 解析到相对路径。
- bundler 生产 import 不含 `internal/project`；official Vue 生产代码不含 `internal/builtin`。
- `make build` 仍产出 `bin/aluka`。

## 非目标

- 不把 graph/compile 的 `*interpreter.VM` 改成接口。
- 不把 JIT/lexer 再拆成独立 module。
- 不把 `go.work` 当作开发者私有文件忽略。
