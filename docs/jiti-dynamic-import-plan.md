# jiti 与运行时动态导入支持计划（M0–M4）

> 状态：**M0/M1/M2 已实施并验证（2026-08-24）**；M3/M4 待排期
> 关联文档：[build-compile-plan.md](./build-compile-plan.md)（M2 产物模式）、[node22-api-coverage.md](./node22-api-coverage.md)（API 面对照）
> 触发场景：`aluka_desktop` 项目 `npm run build:gui` 打包 AlukaDesktop.exe 时出现 4 条
> `dynamic import with non-constant specifier cannot be precompiled; it will fail at runtime`
> 警告（agent/src/extensions/loader.ts、jiti/lib/jiti.mjs、plugin-ui.ts、plugin-ui-core.tsx），
> 目标：让运行时（含 `--compile` 产物）支持**非静态路径动态导入**与 **jiti** 运行。

---

## 0. 实施记录（与本文档差异）

- M0：run 模式 jiti + alias 冒烟通过（`aluka_desktop/agent/.smoke/`，已随验证删除）。
- M1：**全部落地**。`Manifest.RootDir`（payload.go）、`EmbeddedResolver.RootDir()`（loader.go 接口 + embedded.go）、
  requireCtx 未命中分支按 RootDir 回退（含 `bun://~BUN/` 虚拟父映射）、data: URL 模块（dataurl.go）、
  build.go 传 RootDir + 警告文案升级。**新增发现并修复**：CJS 包装器缺 `__importMeta` 参数导致
  `import.meta` 在 CJS 中为 undefined（cjs.go / esm.go 注入，bc_cache pipelineVersion 5→6）；
  builtin/module.go createRequire 对 `bun://` 父路径跳过 filepath.Abs。
- M1 偏差：G6（UnresolvedDynamic 记录 specifier）**未实施**——保持 []string 记模块 key，
  留给 M4 静态化探索一并做；构建期警告文案已按新语义更新。
- 评审修复（首次实现后自查）：① data: URL 分支按 `importCtx` 门控——`require('data:…')`
  保持 Node 语义走解析失败；② conformance/build 新增正向用例 7b（变量动态导入 +
  createRequire(import.meta.url) 从磁盘加载），24/24 通过。
- M2：**产物内 jiti 全链路验证通过**（createJiti → babel 懒加载（RootDir 回退）
  → TS 转译 → ESM 执行 → alias 拦截），构建期警告保留但运行时可用。
- 测试：`internal/runtime/module` 新增 dynamic_import_compiled_test.go（变量 specifier /
  bun:// 父 / data: URL / 解码器 / 路径映射单元测试）；bundler/compile 增 RootDir 回环与
  Embedded.RootDir 测试；conformance/build 用例 8 文案更新（新旧语义兼容匹配）。
  根模块 ./... 与 bundler ./... 全量测试通过；conformance build 23/23。
  已知 pre-existing 环境性失败（改动前后一致）：webbuild React/dynamic bundle 执行
  （测试调 node 跑 ESM 产物缺 type:module）、vue-sfc probe fnv 差异。

---

## 1. 背景与目标

桌面项目（`aluka_desktop`）把 `src/main/index.ts` 及其依赖图用 `aluka build --compile --gui`
预编译进单文件 exe。构建期 4 个警告全部来自**非静态路径动态导入**（`import(变量)`），
这类导入无法在构建期预编译进图，当前产物运行时会直接 reject；依赖链里的 jiti 也因此
在 exe 内不可用（项目现状是回退到「原生绝对路径 import + registerVirtualModule」分支，
并把组件内核单独用 vite 打成 `ssr-embedded.mjs` 放在 exe 旁运行时 `import(pathToFileURL(...))`
旁路加载——这依赖「绝对路径 specifier 允许回退文件系统」这一现成通道）。

目标分两层：

1. **T1 运行时动态导入**：`--compile` 产物内 `import(变量)` / `import(pathToFileURL(abs))` /
   `createRequire(...)(相对路径)` 能像 `aluka run` 一样从磁盘现场解析、加载、编译、执行
   未预编译模块（TS/ESM/CJS/JSON/TSX，含 node_modules 解析）；构建期警告消失或降级为提示。
2. **T2 jiti 可用**：`aluka run` 与编译产物内都能直接 `import('jiti')` + `createJiti` +
   `jiti.import(file)` 加载 `.ts/.js/.mts/.mjs/.cts/.cjs` 与 TSX（经 jiti 的 babel 转译器），
   覆盖桌面项目「运行时加载用户扩展/插件」场景，替代 `ssr-embedded.mjs` 旁路。

非目标（本期不做）：VM `SourceTextModule` 真实实现、`vm.cachedData`、构建期把动态导入
静态化收集进 payload（见 M4 探索项）、进程级运行时沙箱。

---

## 2. 现状分析

### 2.1 动态导入的执行链路（run 模式与产物模式共用）

- parser 把 `import(spec[, opts])` 无条件 lower 成普通调用 `__import(spec, opts)`（
  `internal/engine/parser/expr.go:976-1004`），**不检查 specifier 是否字面量**；
  `import.meta` 同样 lower 成 `__importMeta()`。
- `__import` 作为模块词法参数注入（ESM：`internal/runtime/module/esm.go:64-84`；CJS：
  `cjs.go:18-22`），实现是 `Loader.makeImportFunc(modulePath)`（
  `internal/runtime/module/loader.go:539-596`）：`args[0].String()` 取 spec →
  `requireWithAttributes` → `requireCtx(spec, parent, importCtx=true)`（loader.go:191-308），
  结果用全局 Promise 包装成已 settle 的 Promise（loader.go:699-745）。
- `requireCtx` 统一入口：`file://` 规范化（loader.go:195）→ `bun:sqlite`/`aluka:*` 拦截 →
  内置模块（loader.go:228-235）→ 虚拟模块 → **embedded 分支（产物模式）** → 文件系统分支
  （`ResolveImport`/`Resolve` → JSON/ESM/CJS 分流，loader.go:278-307）。

**结论：`aluka run` 模式对 `import(变量)` 已原生支持，无任何字面量门禁。**

### 2.2 产物模式（--compile）的缺口

embedded 分支（loader.go:242-276）：

```go
if key, ok := l.embedded.ResolveEmbedded(specifier, parentPath); ok { …预编译模块… }
if !filepath.IsAbs(specifier) && !filepath.IsAbs(parentPath) {
    return …, fmt.Errorf("module: compiled mode: cannot load external module %q from %q (not embedded; …)", …)
}
```

- `ResolveEmbedded` 查构建期映射 `manifest.Resolutions`（`internal/bundler/compile/embedded.go:34-41`）。
- **未命中时只放行绝对路径**。产物模式下父模块 key 是**虚拟路径**（相对入口目录、`/` 分隔，
  `internal/bundler/graph/graph.go:165-171` `virtualKey`），故相对/裸 specifier 一律被拒 →
  `rejectImport` → 运行时 rejected Promise。这与 `cmd/aluka/build.go:365-368` 的警告一致。
- 绝对路径 specifier（`import(pathToFileURL(abs))`）已可用——桌面项目 `ssr-embedded.mjs`
  旁路正是吃这个通道。
- `import.meta.url` 产物模式为 `bun://~BUN/<虚拟key>`（loader.go:615-621），`createRequire(import.meta.url)`
  得到的 parent 是 `bun://~BUN/...`，相对 require（如 jiti 的 `createRequire(import.meta.url)("../dist/babel.cjs")`）
  同样被拒。
- 构建期判定：`graph.collectDeps`（graph.go:425-465）只把字面量 `import('x')`/`require('x')`
  （含常量折叠 `astutil.FoldConst` 兜底）收进图；折叠失败的记入 `Result.UnresolvedDynamic`
  （graph.go:450-458），仅记模块 key、丢了 specifier 本身。变量 `require()` **静默漏过**（不警告、运行时同样失败）。

### 2.3 jiti@2.7.0 宿主依赖审计

包结构：`node_modules/jiti/lib/*.mjs` 是薄 ESM 包装，主实现是 rspack 打的 CJS bundle
（`dist/jiti.cjs` 190KB + `dist/babel.cjs` 1.5MB 自打包 babel 全家桶），零第三方运行时依赖。
ESM 执行全部走**非静态动态 import**（`import(id)`、`import(pathToFileURL(tmpfile))`、
data: URL），CJS 编译唯一手段是 `vm.runInThisContext`。

| 宿主能力 | jiti 是否依赖 | Aluka 现状 | 备注 |
|---|---|---|---|
| 动态 import（变量 specifier） | **是（核心）** | run ✓ / 产物 ✗ | T1 目标 |
| `node:module.createRequire` | 是 | ✓（`internal/builtin/module.go:50-68`） | 产物模式相对解析受 2.2 限制 |
| `Module._nodeModulePaths` | 是 | ✓（module.go:175-192） | |
| `require.cache` 读写 | 是（moduleCache 选项） | 空对象、不参与加载（loader.go:450） | 语义缺口，生态低频 |
| `node:vm.runInThisContext` | **是（CJS 编译唯一手段）** | ✓（`internal/builtin/vm.go:57`） | |
| `Module._compile` / `require.extensions` | 否（bundle 中 0 引用） | stub / 空对象（module.go:133-135、loader.go:451） | jiti 不需要；生态项入 M3 |
| 全局 `eval` | 否（bundle 无 eval） | **未注册**（`interpreter/builtins.go` setupGlobalFuncs 无 eval） | M3 |
| `node:fs` 同步族（read/exists/stat/realpath/mkdir/write/access/unlink） | 是 | ✓（`internal/builtin/fs.go`，产物模式下 fs 不设防、可自由读盘） | |
| `node:path` / `node:url`（fileURLToPath/pathToFileURL） | 是 | ✓（builtin/path.go、builtin/url.go） | Windows 盘符场景已有回归（p1_test.go） |
| `node:os.tmpdir` / `process`（cwd/env/platform/pid） | 是 | ✓（os 内置、globals/process.go） | `process.versions.aluka` 已存在（process.go:106-111） |
| `node:perf_hooks`/`node:crypto`/`node:util`/`node:tty`/`node:assert` | 打包的内部库用 | 基本 ✓ | 冒烟验证项 |
| 全局 `Buffer` | 是（data: URL base64） | ✓ | |
| TS/TSX 转译 | 自打包 babel，**懒加载**（`createRequire(import.meta.url)("../dist/babel.cjs")`） | 宿主的 TS strip 不参与 | 转译在 jiti 内完成，宿主只需执行 JS |

**结论：jiti 必需的宿主 API 面（除动态导入产物模式缺口外）已全部具备**；
其 `transform` 懒加载依赖的 `createRequire(import.meta.url)("../dist/babel.cjs")` 相对解析
是产物模式下的第二处关键缺口（见 2.2）。

### 2.4 桌面项目现状

- `plugin-ui.ts`：双形态——node 桥（Node 子进程内 jiti+esbuild+React）与 embedded 内核
  （`src/main/ssr-out/ssr-embedded.mjs`，vite 库模式打成无外部依赖 ESM，`build-gui.mjs` 拷贝到
  exe 旁，运行时代码探测 3 个候选路径后 `import(pathToFileURL(abs))` 加载——绝对路径通道）。
- `agent/src/extensions/loader.ts`：Node 环境走 `createJiti`（自定义 transform 直接
  `require(dist/babel.cjs)`、`fsCache:false`、alias 重定向 typebox/@aluka 包）；Aluka 环境
  （`process.versions.aluka` 探测）走原生 `import(pathToFileURL(...))` +
  `node:module.registerVirtualModule`。也就是说**同一份代码在 Aluka 下已绕开 jiti**，但绕开
  的代价是：exe 内动态导入能力缺失时只能依赖绝对路径通道 + 虚拟模块，TS/TSX 由宿主 parser
  strip（不转 JSX/装饰器等），且 jiti 依赖链（1.5MB babel）白白被静态图带上。
- `ssr-build.mjs` 产物必须先构建、拷贝位置约定脆弱（警告即来自 `ssr-embedded.mjs` 缺失）。

---

## 3. 关键缺口汇总

| # | 缺口 | 位置 | 打通方式 |
|---|---|---|---|
| G1 | 产物模式相对/裸 specifier 磁盘回退被 abs-only 门槛卡死 | loader.go:273-275 | manifest 补 `RootDir`，回退基准改为 RootDir + 虚拟父目录（M1） |
| G2 | `bun://~BUN/` 虚拟父无法参与相对解析（createRequire 场景） | loader.go:618 | NormalizeModulePath / 回退前剥离前缀映射到 RootDir（M1） |
| G3 | data: URL 动态导入不支持（jiti ESM 主路径之一） | loader.go requireCtx 前端 | 识别 `data:text/javascript;base64,` 就地编译执行（M1/M2） |
| G4 | babel.cjs 懒加载相对 require 在产物内不可达 | jiti lib/jiti.mjs lazyTransform | 随 M1 的 RootDir 回退自然可达；或 agent 侧显式加载（M2 方案 b1/b2） |
| G5 | 全局 `eval`（已补，2026-08-25）、`Module._compile`/`require.extensions`/`require.cache` stub | builtins.go、module.go、loader.go:450-451 | M3（jiti 不依赖，生态兼容项） |
| G6 | `UnresolvedDynamic` 只记模块 key 不记 specifier，构建期信息不足 | graph.go:450-458 | 记录 specifier 供诊断/后续静态化（M1 附带 / M4 探索） |

---

## 4. 里程碑方案

### M0 基线验证（前置调研收尾，~0.5 天）

- 在 `aluka run` 下冒烟 jiti：加载 `agent/node_modules/jiti`，`createJiti` + `jiti.import`
  一个含 TS/别名/ESM 的样本，记录实际缺口（perf_hooks/crypto/util 等小 API 的假实现是否触发）。
- 用桌面项目最小样本复现 4 条警告，固化「产物内 4 个点名模块各自的失败 specifier 形态」。
- **退出条件**：jiti 在 run 模式跑通；产物模式失败点清单与本文一致。

### M1 编译产物运行时磁盘动态导入（T1，约 2–3 天）

目标：产物内 `import(变量)` / 相对 require / `bun://~BUN/` 父路径全部回退磁盘加载。

1. **manifest 补 RootDir**（`internal/bundler/compile/payload.go`）
   - `Manifest` 增 `RootDir string json:"rootDir,omitempty"`（构建机入口目录绝对路径，
     `graph.Result.RootDir` 现成，graph.go:53,116）。
   - `PackWithOptions`/`Pack` 增 rootDir 参数（或加 `PackOptions.RootDir`），`cmd/aluka/build.go:500`
     传 `graphResult.RootDir`。旧产物（无该字段）行为退化为现有 abs-only 门槛——**向后兼容，
     `PayloadVersion` 可不 bump**（manifest 为 JSON，旧二进制读新产物忽略未知字段）。
2. **EmbeddedResolver 扩口**（`internal/bundler/compile/embedded.go` + `loader.go:22-31`）
   - 接口增 `RootDir() string`，`NewEmbedded` 保存 manifest.RootDir；模块侧（root module）
     只需随接口调用，无跨模块新 import。
3. **requireCtx embedded 未命中分支改造**（`loader.go:273-276`）
   - 回退基准：rootDir 非空时，虚拟父路径 `bun://~BUN/<key>` 剥离前缀 → `filepath.FromSlash` →
     `filepath.Join(rootDir, filepath.Dir(key))` 得磁盘父目录；specifier 为相对/裸名 →
     `ResolveImport(spec, absParent)`（import 语境，含 node_modules/TS 替身/扩展名补全）→
     落入既有文件系统分支（cache → JSON → ESM → CJS，loader.go:278-307 → `loader_pipeline.go:49-61`
     `loadModuleFile`：ParseFileUnit → compileUnit（bcCache）→ RunPrecompiled）。
   - 失败错误文案区分「磁盘上不存在（给出候选绝对路径）」与「无 RootDir 的旧产物 not embedded」。
4. **data: URL 支持**（`loader.go` requireCtx 前端）
   - 识别 `data:text/javascript;base64,`（及 `;charset=utf-8` 变体）：解码后按 ESM/CJS 判定
     （源码含 import/export 升 ESM，复用 `source_unit.go:161-176` 语法提升）走
     `TransformESMToCJS + WrapESMAST + vm.CompileAST + RunPrecompiled`，key 用 data: URL 原文。
     jiti ESM 主路径用它，避免 tmp 文件回退。
5. **构建期提示升级**（`cmd/aluka/build.go:365-368`）
   - 文案改为提示「运行时会从磁盘动态加载未预编译模块」并列出候选绝对路径（配合 G6：
     `UnresolvedDynamic` 从记 key 改为记 `key → specifier` 列表）。
6. **测试**
   - 单元（runtime/module）：构造带 RootDir 的 manifest + 变量 specifier 用例（含
     bun:// 父、相对 .ts/.json、node_modules 解析、失败 reject 文案）；沿用
     `dynamic_import_test.go` 表驱动风格。
   - 集成（cmd 级）：临时项目 `aluka build --compile` 含 `import('./ext/'+name+'.ts')` →
     运行产物验证磁盘加载与 bcCache 命中；data: URL 直载。
   - 回归：`make test` 全 workspace；`tests/conformance/build`（23 项）、`webbuild`（11 项）；
     **FormatVersion 不动**（无字节码格式变化），jitdiff 不受影响。
   - 兼容：旧产物（无 rootDir）行为不变。
- **退出条件**：产物内 4 个警告模块的动态导入全部可执行（jiti 本身除外，见 M2）；
  conformance/全量测试通过。

### M2 jiti 于产物内可用（T2，约 2–3 天）

1. **run 模式冒烟**（承接 M0）：`import('jiti')` → `createJiti` → `jiti.import(.ts)`；
   补缺失小 API（如 perf_hooks.now、crypto.createHash 若有缺口）。
2. **babel.cjs 可达性**（关键决策点，二选一）：
   - **b1（推荐）现场解析**：M1 的 RootDir 回退让 `createRequire(import.meta.url)("../dist/babel.cjs")`
     在 exe 旁真实目录树中解析成功（前提：agent 目录与 node_modules 随 exe 一同分发——桌面项目
     已按此分发）。payload 不增重，无需改 jiti 用法。
   - b2（离线）Bun `jiti/static` 思路：loader.ts 侧把 babel.cjs 显式静态引用/注册虚拟模块，
     babel 进 payload（zlib 后约 +400KB），exe 可脱离 node_modules 转译。作为 `--offline` 开关。
3. **desktop 侧 loader.ts 分支调整**：isAlukaRuntime 分支改优先 `jiti.import(file)`（含 alias/
   interopDefault 配置），jiti 不可用时回退现有原生 import + registerVirtualModule；`plugin-ui.ts`
   的 embedded 内核加载保持兼容（不强制迁移）。
4. **测试**
   - runtime 侧：`tests/compat/node22` 增 jiti 差分用例（Node vs Aluka 双跑：createJiti 加载
     含 TS/别名/ESM/TSX 样本，对比导出与报错）。
   - 桌面侧：`npm run build:gui` 全链路 + 扩展 `.ts` 加载 + 插件 React 渲染冒烟；
     `ssr-embedded.mjs` 缺失警告不再出现（或降级为 info）。
- **退出条件**：exe 内 jiti 加载用户扩展跑通；`tests/compat/node22` 差分零失配。

### M3 Node 兼容 API 面补齐（生态收益，与 jiti 无强依赖，约 2 天）

按优先级（每项独立成 commit，配 node22 差分）：

1. ~~全局 `eval(src[, filename])`~~：**已实施（2026-08-25，gap-closure-plan P1-5）**——
   `interpreter/builtins.go` setupGlobalFuncs 注册，转发 `Context.Eval`；行为：非字符串
   参数原样返回、字符串在全局作用域求值（间接 eval 语义，看不到调用方局部变量，已在
   gap-closure-plan §6 标注）。剩余三项（`Module._compile`/`require.extensions`/
   `require.cache`）继续按 M3 排期。
2. `Module.prototype._compile(code, filename)`：Loader 暴露 CJS 源码编译入口
   （复用 `cjs.go:18-22` WrapCJSSource → `vm.Compile` → `RunPrecompiled`），
   `builtin/module.go:133-135` 由 stub 改真实实现。
3. `require.extensions` 钩子：requireCtx 在 loadCJS/loadJSON 前查 `require.extensions[ext]`
   （JS 函数调用），loader.go:451 空对象改为共享真实对象。
4. `require.cache` 生效：requireCtx 缓存命中前查 `require.cache[resolvedPath]`（loader.go:450）。

- **退出条件**：四项均有 node22 差分用例并零失配；`make test` 通过。
- 备注：jiti 已确认不依赖上述四项；若人力紧张可整体后移至下一迭代。

### M4 探索项（本期不承诺）

- 构建期动态导入静态化：`UnresolvedDynamic` 记录 specifier（G6）后，构建期对可折叠前缀
  做尽力解析/目录扫描并嵌入；`--embed-dir <dir>` 把运行期目录打进 payload（源码+按需编译），
  实现 Bun `--compile + dynamic import` 的完全离线语义。
- `vm.SourceTextModule` 真实实现（Node 生态长尾）。

---

## 5. 风险与开放问题

- **磁盘依赖**：M1/M2 后产物运行期需要 exe 旁的真实文件树（外部分发目录），不再「单文件即全部」；
  与 `--compile` 的离线叙事冲突——文档需明示边界（等价于 Bun 的运行时动态加载而非重打包；
  完全离线要靠 M4）。
- **信任模型**：运行时磁盘加载不做校验，与 `aluka run` 相同（本地可信代码）；不做沙箱承诺。
- **路径语义**：`bun://~BUN/` 前缀剥离与 Windows 盘符/`/`↔`\` 混合要覆盖（虚拟 key 恒 `/`
  分隔，join 前 FromSlash）；`import.meta.resolve` 产物模式按虚拟父解析同样受益于 RootDir
  回退（loader.go:641-655）。
- **PayloadVersion 策略**：推荐不 bump（JSON 加性字段，新旧互读安全）；若评审认为需严格校验
  版本则 bump 到 4 并同步旧产物提示。
- **体积权衡**：b2 方案 babel 进 payload 约 +400KB（zlib 后），默认不开。
- **与 ssr-embedded 共存**：M2 完成后桌面项目可逐步弃用旁路文件，但保留兼容回退，避免破坏
  已发布产物。

---

## 6. 关键代码位置速查

| 环节 | 位置 |
|---|---|
| 构建期警告 | `cmd/aluka/build.go:365-368` |
| 依赖收集/UnresolvedDynamic | `internal/bundler/graph/graph.go:425-465`（G6 记录点 :450-458） |
| RootDir / 虚拟 key | graph.go:53,116 / :165-171 |
| manifest/打包 | `internal/bundler/compile/payload.go`（Manifest :97-118，PackWithOptions :133+） |
| 嵌入式存储 | `internal/bundler/compile/embedded.go:34-68` |
| 产物运行入口 | `cmd/aluka/compiled.go:85,109,146` |
| 运行时加载中枢 | `internal/runtime/module/loader.go`（requireCtx :191-308，回退门槛 :273-275，makeImportFunc :539-596，import.meta url :615-621，require/extensions :413-455） |
| 文件模式加载链 | `internal/runtime/module/loader_pipeline.go:49-61`；`esm.go:31-136`；`cjs.go:18-22` |
| node:module | `internal/builtin/module.go`（createRequire :50-68，_compile stub :133-135，_nodeModulePaths :175-192） |
| node:vm | `internal/builtin/vm.go:57`（runInThisContext） |
| 全局 eval（缺） | `internal/engine/interpreter/builtins.go` setupGlobalFuncs |
| 格式版本 | `internal/engine/bytecode/serialize.go:77`（FormatVersion=29，本期不动） |
| 桌面项目 | `desktop/apps/desktop/scripts/build-gui.mjs`（ssr-embedded 拷贝）、`ssr-build.mjs`、`src/main/plugin-ui.ts`、`agent/src/extensions/loader.ts:374-483`、`agent/node_modules/jiti`（2.7.0） |