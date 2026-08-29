# AGENTS.md

> 面向 AI 代码助手（与新人）的工程指南。先读本文，再读 [README.md](./README.md) 与 [docs/](./docs/)。

Aluka 是一个**用纯 Go 实现的、API 行为兼容 [Bun](https://bun.sh/) 的 JavaScript/TypeScript 运行时**。核心组件（JS 引擎、模块系统、事件循环、TS 转译器、RegExp 引擎、GC、包管理器、打包器）全部自研。

- 模块路径：`github.com/aluka-lang/aluka`
- Go 版本：`1.25.x`（见根 `go.mod` 与各子模块 `go.mod`，CI 使用 `1.25`）
- 单仓多 module：各 `internal/<pkg>/go.mod` 路径与今日 import 相同；根模块与子模块用 `replace` 指向相对目录。提交 `go.work`。仓库根 `./...` **不会**进入嵌套 module，全量测试用 `make test`（`go test $(go list -f '{{.Dir}}/...' -m)`）。跨模块新 import 必须写入对应 `go.mod` 的 `require` + `replace`。改 engine 可只测 engine 模块（`cd internal/engine && GOWORK=off go test ./...`）。详见 [docs/adr/go-modules.md](./docs/adr/go-modules.md)。
- CLI 入口：`./cmd/aluka`

---

## 不可违背的约束（动手前必读）

这些是项目级硬性约束，任何改动都不得破坏：

1. **纯 Go，禁用 CGO**。所有构建带 `CGO_ENABLED=0`，引擎代码用 `//go:build !cgo`。
   - 唯一例外：JIT 的 race 检测测试（`go test -race` 需要 cgo），仅限本地/CI 测试，不影响产物。
2. **核心组件自研**。禁止引入第三方 JS 引擎（V8/QuickJS/Goja 等）。
3. **JSX/TSX 已支持，Vue SFC 有明确边界**。源码级 JSX/TSX 由自研 parser/compiler lowering；`.vue` 默认使用纯 Go subset，复杂语法可显式选择 official 后端。已接入 `<script src>` / `<template src>` / `<style>`（含 scoped，纯 CSS）；custom block、`lang` 预处理器、`<style module>` 仍必须构建期报错。
4. **单二进制，静态编译，零运行时依赖**。新增外部依赖需谨慎评估，且必须纯 Go（无 CGO）。
5. **API 行为兼容 Bun/Node.js**。新增内置模块或 Web API 时，以 Node.js / Bun 的真实行为为权威基准（见 `tests/conformance/` 差分测试）。

---

## 常用命令

```bash
# 构建（产物到 bin/aluka）
make build
# 等价于：
CGO_ENABLED=0 go build -o bin/aluka ./cmd/aluka

# 全量单元测试
make test            # 全 workspace 模块；根目录 ./... 不会进入嵌套 go.mod

# 覆盖率
make cover

# Lint（需本地安装 golangci-lint）
make lint

# 跨平台发布产物（linux/darwin/windows × amd64/arm64）
make release

# 运行
./bin/aluka -e "console.log(1+1)"
./bin/aluka run hello.js
./bin/aluka hello.ts        # run 的简写，.ts 自动转译
./bin/aluka repl

# 引擎/缓存选择
./bin/aluka --vm  app.js    # 字节码 VM（默认）
./bin/aluka --ast app.js    # AST 解释器
./bin/aluka --no-cache app.js   # 禁用磁盘字节码缓存
./bin/aluka --no-bytecode-opt app.js   # 禁用编译管线默认的字节码优化

# Web bundle（与 --compile 互斥）
./bin/aluka build --target=web --outdir dist src/index.ts
./bin/aluka build --target=web --format=umd --global-name=App --outdir dist src/index.ts
./bin/aluka build --target=web --vue-compiler=official --outdir dist src/index.html
./bin/aluka build --target=web --watch --outdir dist src/index.html
./bin/aluka dev --host 127.0.0.1 --port 3000 --outdir dist src/index.html

# JIT 相关（amd64 平台，默认 --jit=auto；--jit=off 关闭）
./bin/aluka --jit=auto --jit-threshold=1 --jit-backedge-threshold=2 app.js
./bin/aluka --jit=auto --jit-stats app.js     # 候选/编译/guard/deopt 统计（含 R5 聚合行）
./bin/aluka --jit=auto --jit-dump=ir app.js   # dump 已验证 IR（asm 见 --jit-dump=asm）

# JIT 差分（Tier 0 为 oracle；nightly 10 万例需 JITDIFF_NIGHTLY=1）
CGO_ENABLED=0 go test ./internal/engine/interpreter/jitdiff/ -count=1

# JIT fuzz（失败输入自动存 testdata/fuzz）
CGO_ENABLED=0 go test ./internal/engine/jit/ -run='^$' -fuzz=FuzzVerifyProgram -fuzztime=60s

# 针对某包跑测试（示例）
CGO_ENABLED=0 go test ./internal/engine/interpreter/...
CGO_ENABLED=0 go test ./internal/engine/jit/... ./internal/engine/interpreter
```

### 一致性测试（conformance，需先构建 `./bin/aluka`）

```bash
ALUKA=./bin/aluka bash tests/conformance/node/run.sh       # Node.js 官方测试子集（11/11）
ALUKA=./bin/aluka bash tests/conformance/build/run.sh      # build --compile 产物（23/23）
ALUKA=./bin/aluka bash tests/conformance/webbuild/run.sh   # React/TSX/chunk/ESM-CJS-UMD（11/11）
ALUKA=./bin/aluka bash tests/conformance/vue-sfc/run.sh    # Node/Aluka compiler-sfc 探针对拍（1/1）
ALUKA=./bin/aluka bash tests/conformance/express/run.sh    # express 真实 HTTP 链路
ALUKA=./bin/aluka bash tests/conformance/npm/run.sh        # 真实 npm 包加载
```

详见 [README.md → 一致性测试](./README.md)。

---

## 代码仓库布局

```
cmd/aluka/                 CLI 入口：main.go / build.go / compiled.go / install.go / repl.go
internal/
  engine/                  自研 JS 引擎（独立 module：internal/engine/go.mod）
    lexer/                 词法分析
    parser/                递归下降 + Pratt 解析器
    ast/                   AST 节点定义
    compiler/              AST → 字节码
    bytecode/              指令集 / 序列化 / 优化（opcodes.go, serialize.go, optimize.go）
    interpreter/           AST 解释器 + 字节码 VM（Date/URI/structuredClone/V8 堆栈/promise/async/regex）
                           JIT 桥接 jit_bridge.go：热点探测、trace 匹配与特化路径（数组索引/批处理、闭包 upvalue、callback 纯度）
                           JIT 测试 jit_*_test.go（soak/reconfigure/adaptive/r5-cache-budget/guard_mutation）、r4_*_test.go、side_effect_deopt_test.go
      jitdiff/             JIT 差分框架（生成式差分，Tier 0 唯一 oracle；PR 1000 例 / nightly 10 万例）
    regex/                 RE2 翻译快路径 + 自研回溯引擎（UTF-16 索引/预算护栏/Node oracle）
    jit/                   JIT（默认 auto；Quick 类型化 IR + amd64 Native 机器码两层，不支持平台 fallback Tier 0）
      ir.go/trace.go       Quick IR + trace 编译 + deopt/exception 出口恢复
      optimize.go          IR 优化 pass（常量折叠/store-load 消除/不可达块删除）
      property_pic.go      属性 PIC（2-4 shape 自适应）；candidate.go 候选过滤+拒绝缓存；quick_ops.go String/BigInt/宽松相等
      native_emit_amd64.go amd64 发射（Mod/位运算，pow 显式拒绝）；fuzz_test.go 5 个 Go fuzz target
      native/              原生代码发布 / W^X（execmem_*.go）/ 崩溃隔离 / GC-抢占压力 / Frame 无指针审计（平台分文件）
    equality.go            宽松相等/ToNumber 共享 helper（Quick/Trace 复用；禁止 JIT 反向依赖 interpreter）
    engine.go              Engine/Context/Value 接口抽象
    shape.go               隐藏类 + 内联缓存（IC）
    gc.go                  标记-清除 GC
  runtime/
    globals/               全局对象：console/process/Buffer/URL/fetch/Intl/timers/streams...
                           aluka*.go = Aluka 特有 API（兼容 Bun，含 SQL/Redis/S3/shell）
    module/                ESM/CJS 模块系统 + 字节码缓存 + .ts 导入 / TLA
  builtin/                 Node.js 内置模块（fs/http/net/crypto/sqlite/test/...，文件名即模块名）
  pkgmanager/              npm 兼容包管理器（semver/registry/resolver/installer/lockfile/workspace/config）
  bundler/                 build --compile + --target=web（graph/shake/minify/emit/webemit/Vue SFC；独立 module）
  gui/                     跨平台桌面 GUI 框架（Windows WebView2 / macOS WKWebView；参考 Wails v3 架构，无 CGO）
  ipc/                     Aluka 原生 IPC 协议（AIP：16B 帧头、全双工并发客户端/服务端、管道传输）
  project/                 web 工作台（配置 / 插件 / HTML 入口 / 写盘；JS emit 在 bundler/webemit）
  monitor/                 --monitor 性能/内存指标（独立 module，依赖 engine）
tests/
  conformance/             一致性测试脚本（node/test262/npm/install/express/build/webbuild/vue-sfc/node22）
  compat/node22/           Node 22 差分 conformance（aluka vs node22 双跑对比）
bench/                     性能基准（fib/jit/matrix + cmd/jitbench）
pkg/aluka/                 嵌入式 Go API（NewRuntime/Eval/RunFile——Go 宿主嵌入 JS 运行时的公共面）
docs/                      需求 / 开发计划 / 兼容计划 / 性能报告 / 优化计划 / ADR
docs/adr/                  架构决策记录（ADR）
```

**速记**：新增 Node 内置模块 → `internal/builtin/`；新增面向 Go 宿主的公共嵌入 API → `pkg/aluka/`（保持薄封装，转发 internal 实现）；新增 Web API / 全局 → `internal/runtime/globals/`；新增 Aluka（Bun 兼容）API → `internal/runtime/globals/aluka*.go`；新增 IPC/插件通信 → `internal/runtime/globals/aluka*.go` + `internal/ipc/`；新增桌面 GUI 能力 → `internal/gui/`；新增 web 构建/项目编排 → `internal/project/`。

---

## 代码风格与约定

- **格式化由工具保证**：`gofmt` + `goimports`（`.golangci.yml` 已启用，CI 强制）。不要手调缩进。
- **缩进**：Go 用 Tab；`*.yml/yaml/md/json/toml` 用 2 空格（见 `.editorconfig`）。
- **行尾**：LF，去尾随空格，文件末尾留一个换行。
- **注释语言**：项目以**中文注释为主**，接口/公开类型注释可中英混用。新增代码请对齐周边文件的语言风格。
- **包文档**：每个包应有 `// Package xxx ...` 文档注释（参考 `internal/engine/engine.go`、`cmd/aluka/main.go`）。
- **测试风格**：表驱动 + 子测试（`t.Run`），对齐同包已有 helper（如 `internal/engine/interpreter` 的 `vmEvalStr`）。测试代码放宽 `errcheck/dupl/gocritic`（见 `.golangci.yml`）。

---

## 测试约定

- **首选表驱动测试**，用例集中、可读。先看同目录 `_test.go` 的既有写法再动手。
- **环境门控测试**（无对应环境时 `t.Skip`，不要让本地/CI 失败）：
  - `TEST_REDIS_URL` —— 活 Redis（`Aluka.Redis` 命令级测试）
  - `TEST_DATABASE_URL` —— 活 Postgres（`Aluka.SQL` Postgres 路径）
  - `ALUKA_JIT_SOAK=1` —— 长时 soak（轮数 ×20）
  - `JITDIFF_NIGHTLY=1` —— 10 万用例 JIT 差分（仅 scheduled/manual 触发）
- **JIT 差分（jitdiff）**：新增/修改 JIT opcode、快路径或 guard 时，必须保证 `internal/engine/interpreter/jitdiff/` 三 tier（off/quick/auto）零失配（Tier 0 唯一 oracle）；新能力补对应 Kind + 固定用例。
- **JIT fuzz**：`jit/fuzz_test.go` 等 5 个 Go fuzz target 覆盖 verifier/trace compiler/deopt resume/native lowering/artifact replay；改动 IR/trace/deopt 恢复后建议 `go test -fuzz` 片段回归。
- **词法行终止符规范化**：字符串/模板字面量中的裸 CRLF/CR 必须规范化为 LF（ES TV/TRV 语义，lexer readTemplate/readEscape）；破坏此语义会使 CRLF 行尾的 vendored 包（如 compiler-sfc）生成代码混入 
（vue-sfc conformance 对拍红灯）。改 lexer 后跑 `internal/engine/lexer` + vue-sfc conformance。
- **正则差分与预算**：改 `internal/engine/regex/` 或 RegExp/String 调用路径时，必须跑 `CGO_ENABLED=0 go test ./internal/engine/regex ./internal/engine/interpreter -count=1`。JavaScript 可见索引统一为 UTF-16；legacy 模式按 code unit、`u` 模式按 code point。预算耗尽必须传播 `ErrBacktrackLimit`/RangeError，禁止折叠为“无匹配”。compiler-sfc corpus 由 `tools/extract-regex-corpus.mjs` AST 提取，`testdata/node_oracle.jsonl` 是 Node 22 裁判语料。
- **Web/Vue conformance**：改 graph、resolver、printer、Vue backend 或 web emit 时，构建 `./bin/aluka` 后至少跑 `tests/conformance/webbuild/run.sh` 与 `tests/conformance/vue-sfc/run.sh`；影响共享 graph/shake/minify 时同时跑 `tests/conformance/build/run.sh`。
- **test262 回归**：每个 ES 新特性尽量配 test262 子集回归（`tests/conformance/test262`）。
- **差分测试**：行为对齐 Node 时，优先在 `tests/compat/node22/` 加 aluka vs node22 双跑用例。
- 跑完整 JIT 套件（含 race）：
  ```bash
  CGO_ENABLED=0 go test ./internal/engine/jit/... ./internal/engine/interpreter
  CGO_ENABLED=1 go test -race ./internal/engine/jit/... ./internal/engine/interpreter
  ```

---

## 架构关键概念（改代码前需了解）

- **双引擎**：AST-walking 解释器（`--ast`）与字节码 VM（`--vm`，**默认**）。两者共享 lexer/parser/ast/compiler/bytecode。抽象层在 `internal/engine/engine.go`（`Engine/Context/Value` 接口）。
- **字节码磁盘缓存**：VM 默认把编译产物缓存到 `.aluka-cache/`。**改动字节码布局/常量编码/编译器输出时，必须同步 bump `internal/engine/bytecode/serialize.go` 的 `FormatVersion`**，否则旧缓存会被误读或报 version mismatch。
- **字节码元数据与优化**：指令集规范见 `docs/bytecode-spec.md`——`internal/engine/bytecode/meta.go` 是操作数语义/栈效果的**单一事实来源**（String/HasOperand/优化器分类均由它派生，**新增指令必须登记元数据**）。`OptimizeModule`（optimize.go：常量折叠/不可达删除/融合/跳转穿透，多轮迭代）是 `vm.Compile/CompileAST` 编译管线默认步骤（`--no-bytecode-opt` 关闭；build 按 `--bytecode-opt` 显式对齐）。**改动优化器/指令形态后必须跑 `internal/engine/interpreter/optimize_equivalence_test.go` 对拍与 jitdiff 三 tier 零失配**。
- **隐藏类 + 属性描述符 + 内联缓存（IC）**：`internal/engine/value.go` / `shape.go`。普通对象用 Shape/slots，descriptor flags 与 extensibility 在同一语义层执行；Array 的 index/`length`、Proxy traps/invariants、Reflect、Symbol own keys 是 exotic 路径，禁止通过 `unwrapObjectValue` 或 JIT/IC 直写绕过。新增属性快路径必须对 writable/configurable/extensible、array holes/accessor/length 做 guard，并补 Tier 0/JIT 回归。`--ic-stats` 可看命中率。
- **JIT 分层**：`internal/engine/jit/`。**默认 auto**（Windows amd64 实机门禁通过；`--jit=off` 一键回滚，Linux 验证经 CI 口子后续补齐）。分两层：Quick（类型化 IR，跨平台，可执行 Go 代码）与 Native（amd64 原生机器码，W^X/崩溃隔离/safepoint/OSR，无 Go 指针 Frame）；guard 失败与异常经 `DeoptExit`（含 pending exception）恢复完整 VM 状态回 Tier 0；**不支持平台自动 fallback**（结果与 JIT Off 一致）。改动 JIT 需跑差分/fuzz（见「测试约定」）。平台分文件用构建标签：`*_amd64.go` / `*_linux.go` / `*_windows.go` / `*_unsupported.go`。
- **GC**：自研标记-清除（`internal/engine/gc.go`），与 JIT 协同（safepoint、异步抢占）。
- **模块系统**：ESM + CJS + Node 解析算法 + 循环依赖，`internal/runtime/module/`。`.ts` 相对导入、import attributes、路径别名（`paths`/`baseUrl`）、top-level await 均已支持。exports 条件属于 `Resolver` 实例：运行时/compiler loader 使用 Node 条件，web graph 使用 browser 条件；禁止恢复进程级全局条件，否则同进程 official compiler 与浏览器依赖解析会互相污染。
- **TS 转译**：类型注解剥离在 parser/compiler 层完成（非独立编译器），`internal/engine/parser/` 与 `internal/engine/compiler/`。
- **打包器**：`aluka build --compile` = 基座二进制 + payload（预编译字节码 + manifest）+ footer；`aluka build --target=web` = graph → shake/minify → JS/CSS/HTML emit，可输出 ESM/CJS/UMD、动态 chunk、sourcemap，并由 `--watch`/`aluka dev` 复用。web 路径不写 `.aluka-cache`。设计见 `docs/build-compile-plan.md` 与 `docs/static-build-plan.md`。
- **Vue SFC 双后端**：`internal/bundler/vue/`。默认 `subset` 是纯 Go 子集；`--vue-compiler=official` 在构建 VM 内执行项目 `node_modules` 的 compiler-sfc，权限与 `aluka run` 相同，只能用于可信依赖。official 生成 facade/script/template 独立虚拟模块并保留 named exports；失败禁止静默回退。`<script src>`/`<template src>`/`<style>`（含 scoped，纯 CSS）经 graph 虚拟 CSS 模块与 watch ExtraFiles 接入；custom block、`lang≠css`、`<style module>` 仍明确拒绝。升级 vendored Vue/compiler-sfc fixture 时同步更新 lockfile、regex corpus、Node oracle、性能与体积基线，见 `docs/vue-compiler-sfc-merge-notes.md`。

---

## 实现新功能时的注意事项

- **新增 Node 内置模块**：在 `internal/builtin/` 加 `<modname>.go`，注册到模块表；对照 Node 行为补 `tests/compat/node22/` 差分用例。
- **新增全局 / Web API**：放 `internal/runtime/globals/`，参考 `console.go`/`fetch.go` 的注册方式。
- **新增 Aluka（Bun 兼容）API**：放 `internal/runtime/globals/aluka*.go`，以 Bun 同名 API 行为为准；`Bun` 是兼容别名。
- **新增/修改字节码指令**：改 `internal/engine/bytecode/opcodes.go`，并 **bump `FormatVersion`**。
- **新增/修改 Web bundle 或 Vue SFC**：graph/resolver/printer/emit 改动必须同时考虑主 bundle 与动态 chunk；Vue backend 维持 subset 默认和 official 显式选择。新增 SFC block 支持必须同步依赖图、watch 输入、错误位置映射和资产输出，禁止只读 `descriptor.*.content` 后静默丢弃 external `src`。
- **平台相关代码**：用构建标签分文件（`_unix`/`_windows`/`_amd64`/`_unsupported`），保持公共逻辑在无后缀文件里。禁止让无 CGO 构建失败。
- **新增第三方 Go 依赖**：必须纯 Go、无 CGO；先评估必要性。
- **设计先行**：较大特性先在 `docs/` 写计划/设计文档（参考既有 `*-plan.md`），架构决策进 `docs/adr/`。

---

## 提交与 CI

- **分支**：默认在 `main`；如需改动请先开分支（除非用户明确要求直接提交到 main）。**仅在用户要求时才 commit / push**。
- **CI 门禁**（`.github/workflows/ci.yml`）：
  - 三端（ubuntu/macos/windows）全 workspace 模块 `go test` + CLI smoke
  - lint（golangci-lint，`--timeout 5m`）
  - 跨平台构建（5 目标）+ 跨平台 `--compile` 产物结构校验
  - **JIT Linux amd64 专项门禁**：W^X 深度校验、崩溃隔离、fallback、`-race`、`GOGC=20/100` 压力、PR soak、nightly 10 万差分
- 提交前本地自查：
  ```bash
  CGO_ENABLED=0 go build ./...
  CGO_ENABLED=0 go test $(go list -f '{{.Dir}}/...' -m)
  make lint   # 若已安装 golangci-lint
  ```
- **提交信息**：沿用既有约定（`feat:` / `fix:` / `test:` / `docs:` / `bench:` / `ci:` / `refactor:` 等前缀，正文可中文）。

---

## 进一步阅读

- [README.md](./README.md) —— 项目总览、能力清单、已知限制、快速开始
- [docs/requirements-analysis.md](./docs/requirements-analysis.md) —— 需求与约束
- [docs/development-plan.md](./docs/development-plan.md) —— 分 Phase 开发计划（主蓝图）
- [docs/development-roadmap.md](./docs/development-roadmap.md) —— 路线图
- [docs/pi-compat-plan.md](./docs/pi-compat-plan.md) —— 真实世界兼容计划
- [docs/vue-compiler-sfc-merge-notes.md](./docs/vue-compiler-sfc-merge-notes.md) —— official compiler-sfc 安全/功能边界、fixture 升级与合并后观察项
- [docs/adr/](./docs/adr/) —— 架构决策记录
- JIT 专项（`docs/`）：
  - [jit-performance-optimization-plan.md](./docs/jit-performance-optimization-plan.md) —— JIT 总体架构与性能优化主文档
  - [jit-follow-up-development-plan.md](./docs/jit-follow-up-development-plan.md) —— JIT 后续里程碑（R0–R6）：完成定义、deopt/副作用协议、平台门禁、覆盖与预算调优的实施记录
  - [jit-coverage-matrix.md](./docs/jit-coverage-matrix.md) —— JIT 正确性覆盖矩阵（opcode × 值类型 × tier 的权威测试索引）
- [docs/](./docs/) —— 各专项计划与性能报告（`*-plan.md` / `performance-report-*.md`）
