# Web 插件钩子缺陷修复计划

> 日期：2026-08-17
> 依据：当前实现评审（P2 × 6）
> 前置：[项目工作台 + Vite 风格插件](../internal/project) 已落地；本文只修钩子语义与写盘边界，不扩 HMR / `this.resolve`。

**Goal:** 让已挂上的 Vite 同名钩子在失败路径、`config`/`resolveId`/`transform`、产物路径上行为可预期，并补测试。

**Architecture:** 不改管线分层。Go 仍调度 `plugin.Host`；`ConfigJSON` 对外保持 JSON 字符串，JS 侧改传可变对象；`resolveId(false)` 与 Rollup 对齐为 external；CSS/JSON 走已有 `Transform`；写盘把 `name` 锁在 `outDir` 内。

**Tech Stack:** 纯 Go（`CGO_ENABLED=0`），测试表驱动，`go test` 相关包。

---

## 范围

| 修 | 不修（本轮） |
|----|----------------|
| `generateBundle` 文件名逃逸 + watch 误删 | HMR、`this.emitFile` / `this.resolve` |
| `BuildStart` 后失败不调 `closeBundle` | 把 `closeBundle` 挪到 `writeBundle` 之后（仍按原计划：BuildWeb 结束即 close，write 在 `WriteAssets`） |
| `config` 收到 JSON 字符串、浅合并看不到 `build.outDir` | 完整 Vite `ResolvedConfig` |
| `resolveId: false` 不当 external | 完整 Rollup `external` 数组 / 函数 |
| CSS/JSON（含 generated CSS）跳过 `transform` | 入口 `resolveId`、虚拟模块 `this` 绑定 |
| 补对应测试 | **多入口重复 start/close**：每个 entry 一次 `BuildWeb` = 一次独立 build，保持现状 |

---

## Task 1: 产物路径锁在 `outDir`

**Files:**
- Modify: `internal/project/write.go`
- Modify: `internal/project/webbuild.go`（`generateBundle` 合入前同样校验，避免非法 key 进 `assets`）
- Test: `internal/project/webbuild_test.go`（或新 `write_test.go`）

**行为：**

```go
// assetTarget 把 name 解析到 outDir 下。拒绝空、绝对路径、盘符、以及
// filepath.Rel(outDir, dest) 以 ".." 开头的路径。
func assetTarget(outDir, name string) (string, error)
```

- `writeAssets` / tracked 清理都走 `assetTarget`，禁止再裸 `Join`。
- `BuildWeb` 合入 extra 时非法 `name` 返回 error（不要静默丢，避免插件以为写出了）。
- `--outfile` 主产物仍可指向 outDir 外（现有 CLI 语义），仅约束 `assets` map 的 `name`。

**测试：**

| 用例 | 期望 |
|------|------|
| `plugin-manifest.json` | 写在 `outDir` 下 |
| `assets/x.js` | 写在 `outDir/assets/x.js` |
| `../escape.txt`、`..\\escape.txt`、绝对路径 | `WriteAssets` / `BuildWeb` extra 报错 |
| watch：上一轮合法文件本轮消失 | 仍删除；从未成功写出的逃逸路径不在 `written` 里 |

**命令：** `CGO_ENABLED=0 go test ./internal/project -count=1`

---

## Task 2: `closeBundle` 失败路径必达

**Files:**
- Modify: `internal/project/webbuild.go` `BuildWeb`

**实现：** `BuildStart` 成功后：

```go
var closeErr error
defer func() {
	closeErr = host.CloseBundle()
}()
// bundleEntry + GenerateBundle + 合入 extra
// 返回时：构建 err 优先；否则 closeErr
```

注意：成功路径不要 `CloseBundle` 调两次（去掉函数末尾那次显式调用，只留 defer）。

**测试：** mock `Host`：`BuildStart` 后 `bundleEntry` 失败（不存在的入口文件即可）→ `CloseBundle` 调用次数为 1。可把计数 Host 放在 `webbuild_test.go`。

**命令：** 同上。

---

## Task 3: `config` 传对象 + 摊平 `build.*`

**Files:**
- Modify: `internal/bundler/plugin/host.go` `JSHost.ConfigJSON`
- Modify: `internal/project/config.go` 仅当需要在 Go 侧再摊平一次（优先在 Host 合并后的 JSON 里摊平，ApplyConfig 仍 `json.Unmarshal` → `Result`）
- Test: `internal/bundler/plugin/host_test.go`、`internal/bundler/webconfig/load_test.go` 或 `internal/project` 集成一条

**JS 调用约定（保持 Host 接口仍是 JSON in/out）：**

1. 把 `in` 反序列化成 `engine.Object`（顶层字段：`outDir`/`assetsDir`/`base`/`minify`/`vueCompiler`/`alias`/`define`/`source`）。
2. 第二参 `env`：`{ command: "build", mode: "production" }`。
3. `config(config, env)`：
   - `undefined`/`null`：收下一次（允许就地改 `config` 对象）。
   - 返回对象：浅合并进当前对象。
   - 返回 thenable：仍报 `async hook is not supported`。
4. 合并后若存在 `build` 对象，把 `build.outDir` / `build.assetsDir` / `build.minify` 拷到顶层（与 `default-loader.js` 的 `normalize` 对齐），再 `JSON.stringify` 给 `ApplyConfig`。

`configResolved`：继续传最终扁 JSON 字符串即可（已含 CLI 套用后的 outDir 等）。

**测试：**

```js
{ name: "c", config(cfg) { cfg.outDir = "from-plugin"; } }  // 就地改
{ name: "n", config() { return { build: { outDir: "nested" } }; } }
```

Apply 后 `opts.OutDir` 含项目根拼接（相对路径规则不变）。CLI `--outdir` 仍赢。

**命令：** `CGO_ENABLED=0 go test ./internal/bundler/plugin ./internal/project ./internal/bundler/webconfig -count=1`

---

## Task 4: `resolveId(false)` → external

**Files:**
- Modify: `internal/bundler/plugin/host.go` `ResolveId`
- Modify: `internal/bundler/graph/graph.go` `finishWalk` 解析分支
- Test: `internal/bundler/plugin/host_test.go`、`internal/bundler/graph/graph_test.go`

**语义（Rollup）：**

| 返回值 | Host | graph |
|--------|------|--------|
| `undefined`/`null` | `ok=false` | 下一个插件 / 默认解析 |
| `false` | `ok=true, id=""` | **external：不 walk、不写入 Resolutions**（与 builtin 一样 skip） |
| 非空字符串 / `{ id }` | `ok=true, id` | 按现有虚拟/绝对路径 walk |

`JSHost.ResolveId` 在 `stringResult` 之前判断 `TypeBoolean && !b`。

`finishWalk` 现为 `ok && pid != ""` 会把 `false` 掉进文件系统。改为：

```go
pid, ok, perr := r.host().ResolveId(...)
if perr != nil { return perr }
if ok {
    if pid == "" {
        continue // external
    }
    resolved = pid
    err = nil
} else if generated...
```

**测试：** `main.ts` `import "ext:skip"`，插件 `resolveId` 对应该 specifier 返回 `false` → Build 成功且该 spec 不在 `Resolutions`、无对应 SourceUnit。

**命令：** `CGO_ENABLED=0 go test ./internal/bundler/plugin ./internal/bundler/graph -count=1`

---

## Task 5: CSS/JSON 走 `transform`

**Files:**
- Modify: `internal/bundler/graph/graph.go` `walk`（generated CSS 分支 + 磁盘 css/json 分支）
- Test: `internal/bundler/graph/graph_test.go`

**改法：** 两处读到源码后都调用 `r.host().Transform(fsPath, code)`，再写入 `Assets`。已走 `walkSource` 的 load 拦截路径不要双重 transform（`walkSource` 已 transform）。

**测试：** 插件 `transform(_, id)` 若 `id` 以 `.css` 结尾则追加 `/*p*/`；入口 `import "./a.css"` → `Assets` 里 CSS 含 `/*p*/`。

**命令：** `CGO_ENABLED=0 go test ./internal/bundler/graph -count=1`

---

## Task 6: 回归

**命令：**

```bash
CGO_ENABLED=0 go test ./internal/bundler/plugin ./internal/bundler/webconfig ./internal/bundler/graph ./internal/project ./cmd/aluka -count=1
CGO_ENABLED=0 go build -o bin/aluka ./cmd/aluka
# 有 bash 时：
ALUKA=./bin/aluka bash tests/conformance/webbuild/run.sh
ALUKA=./bin/aluka bash tests/conformance/vue-sfc/run.sh
```

demo：`aluka build --target=web demo/vue3-run-build-demo/index.html` 仍有 meta + `plugin-manifest.json`。

---

## 实施顺序

1 → 2 → 4 → 5 → 3 → 6

（3 的对象/合并面最大，放在路径与 resolve 稳定之后。）

不要在本计划里顺手改 cmd 多入口、不要把 `closeBundle` 挪到写盘之后、不要引入 `BuildWebV2`。

提交：仅在明确要求时 commit；信息不写进本计划强制步骤。
