# Aluka 打包器开发计划 —— `aluka build --compile`（子方案 B2：payload 自附加）

> 项目代号：`aluka` ｜ 文档版本：v1.0 ｜ 日期：2026-08-06
> 依据：2026-08-06 代码审查 + 架构分析（字节码序列化、模块系统、引擎引导链路全量核实，`go test ./...` 全绿）
> 配套文档：[开发计划文档](./development-plan.md)（Phase 7）/ [需求分析文档](./requirements-analysis.md）/ [Pi 兼容计划](./pi-compat-plan.md）

---

## 1. 背景与目标

### 1.1 为什么选择 B2

`aluka build --compile` 的目标是生成**单文件原生可执行**（Bun 同语义：`bun build --compile --outfile app`）。两条子路线：

| 子方案 | 机制 | 优点 | 缺点 |
|--------|------|------|------|
| B1 | 生成 Go 源码 + `go build` | 实现简单 | 用户机器需装 Go 工具链；违背"单二进制分发" |
| **B2（选定）** | 字节码 payload 自附加到 aluka 基座 | 无 Go 依赖、真·单文件、启动零开销、字节码平台无关（跨平台只需换基座） | 需实现 payload 格式 + 启动检测 + Loader 存储抽象 |

B2 成立的前提已核实：**字节码序列化格式稳定**（`bytecode.Serialize/Deserialize` + `FormatVersion=9`，磁盘缓存日常使用）、**执行路径已通**（`vm.RunModule` 直接运行反序列化模块）、**引擎引导与文件模式同构**（globals + builtin 注册零差异）、**常量池无序列化缺口**（仅 number/string/bigint，正则/对象走指令）。

### 1.2 目标

1. `aluka build --compile --outfile app ./src/index.ts` 产出单文件可执行
2. `./app` 在**无 aluka 安装、无 Go 工具链**的机器上运行（Windows 主目标，跨平台见 M4）
3. 产物支持完整模块图（多文件 + 静态依赖，express 级依赖树）、TLA、动态 import 字面量、JSON 资源
4. 产物模式下 `process.argv`/`import.meta`/错误堆栈语义对齐 Bun 编译产物
5. 文件模式（`aluka run`）零回归

### 1.3 范围边界（本计划不含）

- ❌ 纯 JS bundle 模式（`bun build` 语义的 tree-shaking/minify/sourcemap/多 target 输出）——B2 产物直接执行字节码，无需 JS 输出；相关能力作为**后续扩展**（见 §8）
- ❌ `Bun.build` JS API（需求 4-R4，依赖扩展完成后）
- ❌ JSX、browser target
- ❌ 构建期 tree-shaking（产物内未引用模块仍会嵌入——体积优化后置）

---

## 2. 现状与可复用资产

| 资产 | 位置 | 复用方式 |
|------|------|----------|
| 字节码序列化 `Serialize/Deserialize` | `internal/engine/bytecode/serialize.go`（v9） | 直接复用（payload 内模块流格式） |
| 常量池编解码 `EncodeConst/DecodeConst` | `internal/engine/const_codec.go` | 直接复用 |
| 编译不执行 `vm.CompileAST` | `internal/engine/interpreter/vm.go:139` | 构建期管线入口 |
| 执行预编译模块 `vm.RunModule` | `internal/engine/interpreter/vm.go:149` | 产物模式执行路径（磁盘缓存同路径） |
| ESM→CJS 转换 `transformESMToCJS` | `internal/runtime/module/esm.go:182` | **提为公共函数**（现为 loader 私有，构建期需调用） |
| 模块包装 `wrapESMAST` | `internal/runtime/module/esm.go:128` | **提为公共函数**（同上） |
| 模块解析 `Resolver.Resolve` | `internal/runtime/module/resolver.go` | 构建期静态解析复用 |
| TLA 等待 `AwaitPromise` | `internal/runtime/module/esm.go:105` | 产物模式入口沿用 |
| 引擎引导 `registerRuntimeGlobals` + `builtin.RegisterAll` | `cmd/aluka/main.go:114` / `internal/builtin/registry.go` | 产物模式启动零改动复用 |
| 磁盘缓存装载模式（参考） | `internal/runtime/module/bc_cache.go` | store 层设计参照 |

---

## 3. 总体架构

### 3.1 产物布局

```
┌─────────────────────────────────┐
│ aluka 基座（完整运行时，零改动）  │ ← 含引擎/globals/builtin/CLI 分发
├─────────────────────────────────┤
│ payload                         │
│  ├─ payload header（magic+版本） │
│  ├─ manifest（JSON）             │ ← 入口/模块表/资源表/校验和
│  ├─ 模块字节码流（Serialize）     │ ← 每源文件一个 bytecode.Module
│  └─ 资源数据（JSON/后续图片等）   │
├─────────────────────────────────┤
│ footer: magic(8) + offset(8)    │ ← 固定 16 字节，启动检测入口
└─────────────────────────────────┘
```

### 3.2 构建期管线（`aluka build --compile`）

```
入口文件
  → 模块图收集（静态遍历 import/require，复用 Resolver，含动态 import 字面量）
  → 逐模块：读源码 → TS 剥离（parser）→ transformESMToCJS → wrapESMAST
           → vm.CompileAST → bytecode.Serialize
  → manifest 组装（模块表/资源表/入口/格式版本/校验和）
  → 复制基座 + 追加 payload + footer → outfile
```

### 3.3 运行期管线对比

| 步骤 | 文件模式（现状） | 产物模式（B2） |
|------|------------------|----------------|
| 启动 | 参数分发 → run/repl/... | **footer 检测命中 → 直接进入产物模式** |
| 引擎引导 | `registerRuntimeGlobals` + `RegisterAll` | 完全相同 |
| 模块加载 | `os.ReadFile` + Resolver + parse + transform + compile | `EmbeddedStore` 取出预编译 `bytecode.Module` → `vm.RunModule` |
| 未命中 | 文件系统解析 | 报错（Bun 产物同语义：不加载外部文件） |

---

## 4. 里程碑与任务分解（WBS）

### M1：payload 格式 + 启动检测 + 单模块产物（核心链路验证）

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| B2.1.1 | payload 格式定义（header/manifest/模块流布局 + PayloadVersion） | `internal/bundler/compile/payload.go` | [ ] |
| B2.1.2 | payload 打包器（单模块：打包/解包/校验和） | 同上 | [ ] |
| B2.1.3 | footer 自检测（`main()` 最早期读自身尾部 16 字节，零开销回退） | `cmd/aluka/main.go` 产物分支 | [ ] |
| B2.1.4 | 产物模式引导（复用引擎引导 + 加载入口模块 + RunLoop） | `cmd/aluka/compiled.go` | [ ] |
| B2.1.5 | `transformESMToCJS`/`wrapESMAST` 提为模块包公共函数（重构，含回归） | `internal/runtime/module/esm.go` | [ ] |
| B2.1.6 | `aluka build --compile` CLI 最小实现（`--outfile`/入口参数） | `cmd/aluka/build.go` | [ ] |
| B2.1.7 | M1 测试：单入口 hello.ts 产物可执行 | `tests/conformance/build/` | [ ] |

**M1 验收**：`aluka build --compile --outfile app hello.ts` → `./app` 输出 hello；无 payload 的 `aluka run` 行为不变；`go test ./...` 全绿。

### M2：模块图收集 + Loader 存储抽象 + 多文件依赖

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| B2.2.1 | 静态模块图收集器（import/export/require + 动态 import 字面量，复用 Resolver；循环依赖/重复引用去重） | `internal/bundler/graph/graph.go` | [ ] |
| B2.2.2 | `EmbeddedStore` 读取层（`Open`/`Has`；M2 全量内存，预留偏移懒加载） | `internal/bundler/compile/store.go` | [ ] |
| B2.2.3 | **Loader 存储抽象**：`require`/`import` 命中 store 直接执行预编译模块；未命中报错；`l.cache`/循环依赖语义复用 | `internal/runtime/module/loader.go` | [ ] |
| B2.2.4 | 多模块打包（模块表偏移索引 + 字节流合并） | `internal/bundler/compile/` | [ ] |
| B2.2.5 | 构建期转换管线完善（TS 剥离/转换/编译逐模块全链） | `internal/bundler/compile/` | [ ] |
| B2.2.6 | M2 测试：多文件 + node_modules 静态依赖（express 级）产物可运行 | `tests/conformance/build/` | [ ] |

**M2 验收**：express 依赖树（静态可达部分）完整嵌入并运行；产物内 ESM/CJS 混合加载正确；循环依赖行为与文件模式一致。

### M3：语义修正层（产物模式差异对齐）

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| B2.3.1 | 虚拟路径语义：`__filename`/`__dirname`/`import.meta`（构建时相对路径，`bun://` 风格 URL） | `loader.go` makeImportMetaFunc 分支 | [ ] |
| B2.3.2 | `process.argv` 产物语义（argv[1] = 虚拟入口路径，后续为应用参数） | `cmd/aluka/compiled.go` | [ ] |
| B2.3.3 | 动态 `import()` 命中/未命中语义（字面量命中 store；变量形式 reject 带清晰报错） | `loader.go` makeImportFunc 分支 | [ ] |
| B2.3.4 | JSON 资源嵌入与加载（复用 `loadJSON`；manifest 资源表） | `internal/bundler/compile/` + loader | [ ] |
| B2.3.5 | 错误堆栈 SourceFile 虚拟路径核对（构建期已写入字节码，验证即可） | 测试验证 | [ ] |
| B2.3.6 | M3 测试：argv/import.meta/TLA/JSON 资源/错误堆栈语义 | `tests/conformance/build/` | [ ] |

**M3 验收**：与 Bun 编译产物对照，argv/import.meta/错误信息语义一致（Node 22 + Bun 差分验证）；TLA 入口正常。

### M4：跨平台 + 校验 + 测试套件 + 文档

| ID | 任务 | 输出 | 状态 |
|----|------|------|------|
| B2.4.1 | footer sha256 校验 + 损坏降级（回退正常模式并告警） | `cmd/aluka/compiled.go` | [ ] |
| B2.4.2 | 跨平台矩阵：CI 构建 6 平台基座 + 同一 payload（字节码平台无关） | `.github/workflows/ci.yml` | [ ] |
| B2.4.3 | 体积与性能基线（payload 大小、产物启动耗时 vs Bun） | `bench/` | [ ] |
| B2.4.4 | 完整测试套件 + 回归门禁（conformance 脚本接入 CI） | `tests/conformance/build/run.sh` | [ ] |
| B2.4.5 | 文档同步（README 快速开始 + 本计划状态表） | `README.md` / 本文件 | [ ] |

**M4 验收**：三端 CI 全绿；Windows 产物在无 Go 环境的机器运行通过；README 文档与实现一致。

---

## 5. 关键设计

### 5.1 payload 字节布局

```
[payload header]  magic(8) "ALUKABDL" | PayloadVersion(u32) | manifestLen(u32) | dataLen(u32)
[manifest]        JSON：入口/模块表（虚拟路径 → offset,len,formatVersion）/资源表/平台/构建时间
[模块字节码流]    每模块 = bytecode.Serialize 输出（复用现有格式，含 FormatVersion）
[资源数据]        JSON 等原始字节
[footer]          magic(8) "ALUKAFTR" | payloadOffset(u64) | payloadLen(u64) | sha256(32)
```

### 5.2 版本策略

- `PayloadVersion`（独立于 `FormatVersion`）：payload 自身布局变更时递增
- manifest 内记录字节码 `FormatVersion`：运行时校验，不匹配则报"产物由不兼容版本构建"
- 字节码格式升级（`FormatVersion` 递增）后旧产物自然失效，行为与磁盘缓存一致

### 5.3 Loader 存储抽象接口

```go
// store 抽象：产物模式下替代文件系统读取
type SourceStore interface {
    Has(path string) bool
    Open(path string) ([]byte, error)        // 源码（JSON 资源等）
    OpenModule(path string) (*bytecode.Module, error) // 预编译模块（直接 RunModule）
}
```

文件模式 Loader 保持现有行为；产物模式注入 store 实现。改动收敛在 `loader.go` 的读取入口，`l.cache`/循环依赖/`module.exports` 语义完全复用。

### 5.4 虚拟路径语义

- 模块键：构建时相对于入口的路径（如 `/src/index.ts`），稳定且跨机器一致
- `__filename`/`__dirname` 基于虚拟路径；`import.meta.url` 用 `bun://` 协议（对齐 Bun 产物）
- 错误堆栈 SourceFile 显示虚拟路径（构建期写入字节码，运行时零成本）

---

## 6. 验收标准（总纲）

1. `aluka build --compile --outfile app ./src/index.ts` 产物可直接执行，等价于 `aluka run ./src/index.ts` 的输出
2. 产物在无 aluka/Go 环境的机器上运行（M1 起即可验证）
3. 文件模式全部现有测试零回归（`go test ./...` + `tests/conformance/` 4 个脚本）
4. 产物模式关键语义与 Bun 编译产物差分一致（argv/import.meta/TLA/动态 import 报错）
5. 每个里程碑完成：对应 Go 回归测试 + conformance 用例 + 文档状态表更新

## 7. 测试策略

```
tests/conformance/build/
├── run.sh                  # 主入口：构建产物 → 执行 → 断言输出
├── hello/                  # M1：单文件 TS 产物
├── multi/                  # M2：多文件 + ESM/CJS 混合 + 循环依赖
├── deps/                   # M2：node_modules 静态依赖（安装后打包）
├── semantics/              # M3：argv / import.meta / 动态 import / JSON 资源
└── tla/                    # M3：top-level await 入口
```

- 单元测试：payload 打包/解包往返、footer 检测（构造/不构造 payload 的基座）、store 命中/未命中
- 差分测试：产物输出 vs `aluka run` 输出 vs Node/Bun 对照（M3 起）
- 门禁：`go vet ./...` + `go test ./... -count=1` + 4 个 conformance 脚本 + build conformance

## 8. 风险与边界

| 风险 | 影响 | 应对 |
|------|------|------|
| Loader 存储抽象侵入现有加载链 | 文件模式回归 | 抽象收敛于读取入口；每里程碑跑全量测试 + express demo 回归 |
| `FormatVersion` 升级后旧产物失效 | 用户需重新构建 | 报错信息明确；与磁盘缓存失效行为一致，可接受 |
| Windows 上产物读自身文件 | 文件共享锁 | 读共享打开（`os.Open` 默认兼容）；产物运行时不做写回操作 |
| 产物体积（基座 30MB + payload） | 分发成本 | 与 Bun 编译产物（~90MB）相比有优势；upx 压缩作为后续优化 |
| 静态图漏收集（极端动态 require） | 产物运行报错 | 构建期告警 + 运行期清晰报错；与 Bun 产物限制一致 |
| 后续扩展（tree-shake/minify） | 超出本计划 | 本计划专注 --compile；bundle 模式能力在 M4 后评估（依赖本计划全部完成） |

## 9. 版本记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-08-06 | 初稿：B2 路线选定、M1-M4 里程碑、WBS 分解、payload 格式设计 |

---

## 附：与开发计划 Phase 7 的对应关系

| Phase 7 WBS | 本计划 | 说明 |
|-------------|--------|------|
| 7.6 `--compile` 单文件可执行 | M1-M4 | B2 路线（原计划为"Go embed + 各平台 builder"的 B1，本计划修正为 B2） |
| 7.1 模块图构建 | M2（B2.2.1） | 静态图，范围收敛到"收集可达模块" |
| 7.7 资源处理（JSON） | M3（B2.3.4） | JSON 先行，CSS/二进制后置 |
| 7.8 `aluka build` 命令 | M1（B2.1.6） | `--compile` 子集先行 |
| 7.9 兼容性测试 | M1-M4 各里程碑 | `tests/conformance/build/` |
| 7.2/7.3/7.4/7.5（tree-shake/minify/target/sourcemap） | 后续扩展 | B2 产物不产出 JS，相关能力依赖后续评估 |
