# coding-agent 打包分析报告：当前问题与剩余工作

> 目标：用 aluka 运行时打包 `E:\code\github\pi\packages\coding-agent`（TS 项目，
> `bun build --compile` 产物形态），对齐 `package.json` 中 `build:binary` 的行为。
> 日期：2026-08-13

---

## 一、打包链路现状（已完成）

### 1.1 构建命令

```bash
cd E:\code\github\pi\packages\coding-agent
aluka build --compile --outfile pi-aluka/pi.exe dist/cli.js
```

- 产物 52.5MB，2528 模块，tree-shake 9 模块。
- 入口 `dist/cli.js`（tsgo 编译产物）。构建期告警：`../../ai/src/env-api-keys.ts` 等
  4 个模块的动态 import specifier 非常量，无法预编译（运行时动态 import 会失败）。

### 1.2 资产布局（对齐 bun 的 copy-binary-assets）

coding-agent 的 `config.js` 已内置 aluka 检测（`process.versions.aluka`），且
aluka 编译模式下 `import.meta.url = "bun://~BUN/..."`，命中其 `isBunBinary` 分支——
资产从**可执行文件旁目录**读取。因此与 bun 一样，需要把资产复制到 exe 旁边：

```
pi-aluka/
  pi.exe
  package.json  README.md  CHANGELOG.md   # VERSION 取自 package.json（0.78.0 ✓）
  theme/*.json                             # 主题（此前 ENOENT 已解决）
  assets/*.png                             # 交互模式资产
  export-html/ template.html/css/js + vendor/*.js
  docs/  examples/
  photon_rs_bg.wasm                        # 图片处理（从 exe 旁目录读取）
```

### 1.3 已通过的验证

| 项目 | 状态 |
|---|---|
| `pi.exe --version` | ✓ 0.78.0 |
| `pi.exe --help` | ✓ |
| `aluka run dist/cli.js --help`（未打包） | ✓ |
| 可选链/for-of/maxstack 专项用例（vs Node 对拍） | ✓ 一致 |

---

## 二、已修复的 aluka 引擎 bug（本次会话）

### 2.1 修复 1：ComputeMaxStack 回边虚高发散（编译期卡死）

- **文件**：`internal/engine/bytecode/maxstack.go`
- **现象**：可选链短路路径在链尾残留链内值（见 2.2），污染 JMP_FALSE_POP 后继
  深度后经回边带回循环头，worklist 每轮 +1 永不收敛——**编译期死循环**。
- **修复**：新增 `settled` 集合；回边向已收敛 PC 携带更大深度时丢弃。
  sound 性：MaxStack 仅用于帧栈预分配（生产构建裸 append 自动扩容），
  `-tags vmstackcheck` 构建保留越界断言兜底。
- **回归测试**：`internal/engine/bytecode/maxstack_regression_test.go`
  （手搓虚高回边字节码，旧代码永不返回，新代码收敛且峰值 = 2）。

### 2.2 修复 2：可选链短路栈残留

- **文件**：`internal/engine/compiler/compiler.go`（+ vm.go 注释）
- **现象**：OpOptionalJump 短路只弹栈顶压 undefined，链内残留值留在栈上，
  污染后续指令（`?? 默认值`、后续属性读取、循环内累计全错）。
- **修复**：链尾生成短路清理块（暂存结果 → POP × 残留 → 压回结果），新增
  `optChainPushCount`/`optChainDelta`/`optionalChainResiduals` 链值计数，
  member 链首不重复计数（isMember 修正），嵌套链继承外层计数。
- **回归测试**：`internal/engine/interpreter/vm_optional_chain_regression_test.go`
  （12 用例，含 coding-agent settings-manager 原形态）。

### 2.3 修复 3：for-of 无声明迭代变量 CLOSE_UPVALUES 越界

- **文件**：`internal/engine/compiler/compiler.go`
- **现象**：`for (x of ...)`（赋给已有变量）无迭代槽，仍发射
  CloseUpvalues(NumLocals) → validate 报 slot out of range（编译失败）。
- **修复**：仅 VarDecl 且含 Decls 时发射（`hasIterBindings` 门控）。
- **回归测试**：`vm_optional_chain_regression_test.go` 中
  `TestVMForOfNoDeclBindingRegression`（5 用例，含闭包/break/continue 路径）。

### 2.4 修复 4：TS 类型断言后接二元运算符解析失败

- **文件**：`internal/engine/parser/parser.go`（`parseBinary`）
- **现象**：`pi.getFlag("mcp-config") as string | undefined ?? configOverridePath`
  报 `expected ';' but got "??"`——`as`/`satisfies` 剥离发生在 `parseBinary` 返回后
  的 `parseConditional` 里，剥离后二元循环已退出，`??` 无人认领。
- **修复**：把 `as T`/`satisfies T` 剥离移入 `parseBinary` 循环内（断言绑定在
  单目级，剥离后继续循环），`(a as T) ?? b` / `a as T + b` / 链式断言均正确。
- **回归测试**：`internal/engine/interpreter/typescript_test.go` 的
  `TestTSTypeAssertion` 新增 6 个无括号用例。

### 2.5 配套

- `FormatVersion` 21 → 23（`internal/engine/bytecode/serialize.go`，含版本注释）。
- 清理全部调试残留：ZZVALIDATE printf、zz_dump*.go/txt、parser 步数上限
  guard（skipAngleBraces 每条路径必前进，guard 死代码）、VM.local panic。
- 全量 `CGO_ENABLED=0 go test ./...` 通过（exit 0）。

---

## 三、当前问题（未修复）——循环体块级绑定非按迭代

### 3.1 现象

zod v4 的 `_installLazyMethods` 用 `for (const key in methods) { const fn = methods[key]; ... }`
在循环体内声明 `const fn` 并被 getter 闭包捕获。aluka 中所有 getter 闭包了
**同一个槽位**（最后一次迭代的 fn），导致 `z.string().min(1)` 实际执行了
`slugify()`，进而把 `$ZodCheckOverwrite` 实例当函数调用：

```
TypeError: { _zod: { def: { check: overwrite, tx: [Function: ] }, ... } } is not a function
```

最小复现（node 输出 `AAA BBB CCC`，aluka 输出 `CCC CCC CCC`）：

```js
function install(proto, methods) {
  for (const key in methods) {
    const fn = methods[key];
    Object.defineProperty(proto, key, { get() { return () => fn.name; } });
  }
}
```

### 3.2 影响面（bisect 结果）

| 形态 | node | aluka |
|---|---|---|
| `for (let i...)` 体 `const x = i` 被闭包捕获 | 0,1,2 | **2,2,2** ❌ |
| `for (const k of ...)` 体 `const y = k` | 1,2,3 | 1,2,3 ✓ |
| `for (const k in ...)` 体 `const z = k` | a,b,c | **c,c,c** ❌ |
| `while` 体 `const q = w` | 0,1,2 | **2,2,2** ❌ |
| `for (const k in ...)` **头**变量 | a,b,c | **c,c,c** ❌ |
| `for (let k of ...)` **头**变量 | 1,2,3 | 1,2,3 ✓ |

结论：**for-of 路径正确**（有 per-iteration 机制）；~~**classic for / for-in /
while 的循环体块级 `let/const` 没有按迭代新绑定**，闭包共享同一 upvalue 槽；
`for...in` 头变量同样缺失（与 for-of 头变量对比）~~
**→ 已修复**（2026-08-25 gap-closure-plan D3 实测：for-let / for-in-const /
while-let 三组闭包捕获与 Node 22 输出逐字节一致，本报告 §3.4 所述 zod v4
阻塞场景已消除）。

### 3.3 根因

`compileForOf`/`compileFor`（头 let 被捕获时）有 per-iteration 机制：
迭代槽 + 每轮迭代末尾 `OpCloseUpvalues(iterationSlotStart)` 封存本轮 upvalue，
下一轮重开 → 闭包拿到当轮快照。而 `compileFor`（体块）、`compileWhile`、
`compileForIn` **没有对循环体块内的块级声明做等价处理**：体块级 `const/let`
的槽位静态分配一次，各轮闭包捕获同一个活槽，循环结束后全部读到终值。

### 3.4 阻塞点（打包主链路）

`pi.exe --print`（非交互主链路）→ 加载用户扩展 `pi-mcp-adapter` → 依赖 zod v4
→ 命中本 bug → 扩展加载失败 → 主链路中断。**此 bug 修复前，打包产物不可用。**

### 3.5 修复方向（待实施）

对齐 for-of 机制，为 classic for / for-in / while 的循环体补 per-iteration
封存：

1. 在 `compileFor`（无头 let 捕获分支）、`compileWhile`、`compileForIn` 编译体
   **前**记录 `iterationSlotStart := c.cur().tmpl.NumLocals`（体块入口槽位下界）。
2. 循环末尾（continue 目标处）发射 `OpCloseUpvalues(iterationSlotStart)`，
   封存本轮在体内创建的 upvalue（含体块级 const/let/function/class 捕获）。
3. 仅当体块**存在被嵌套函数捕获的块级声明**时发射（性能：避免每轮扫描
   openUpvalues；参照 `forLetCapturedNames` 增加 `bodyBlockCapturedNames`
   捕获分析）。
4. `for...in` 头变量 `const k`：需同样处理（在迭代作用域分配迭代槽 +
   每轮从键名写入 + 末尾关闭；或复用 for-of 的迭代槽路径）。
5. 回归测试：表驱动覆盖上述 bisect 全矩阵（同步闭包 + 异步闭包），
   并加 zod v4 级联形态用例（`for (const key in methods)` + getter 闭包）。

---

## 四、已知限制 / 低优先级问题

| # | 问题 | 说明 | 优先级 |
|---|---|---|---|
| 1 | 可选链不能作赋值目标未校验 | `o8?.arr?.[0]++` 被 aluka 接受，Node 22 报 SyntaxError（可选链结果非引用不能自增/赋值） | 低（语义宽松，暂不阻塞） |
| 2 | 动态 import 非常量 specifier | 4 个模块（jiti 等）构建期告警，产物运行时动态 import 失败 | 低（构建期已警告） |
| 3 | worker_threads 多入口 | `image-resize-worker` 经 `new URL(..., import.meta.url)` 派生 bun:// 虚拟 URL，aluka 编译产物内无此文件（bun 用多入口支持）。影响图片粘贴/缩放功能 | 中（功能降级，非主链路） |
| 4 | `-e` 模式无 `require` | REPL 求值上下文不提供 CJS require（需文件模式或 import） | 低 |

---

## 五、剩余工作清单

- [ ] **实现 §3.5 修复**（循环体 per-iteration 封存 + 捕获分析 + for-in 头变量）
- [ ] 回归测试：§3.5.5 全矩阵 + zod 形态用例；跑 `internal/engine/interpreter` 全套
- [ ] 专项回归：
  - [ ] `CGO_ENABLED=0 go test ./internal/engine/interpreter/jitdiff/ -count=1`
        （三 tier 零失配，编译器输出形态有变）
  - [ ] `CGO_ENABLED=0 go test -tags vmstackcheck ./internal/engine/interpreter/...`
        （MaxStack soundness 门）
  - [ ] `CGO_ENABLED=1 go test -race ./internal/engine/jit/... ./internal/engine/interpreter`
  - [ ] `CGO_ENABLED=0 go test ./...` 全量
- [ ] 重新打包 pi.exe 并验证：`--version` / `--help` / `--print --no-session --no-tools`
  （重点：zod 加载不再报错，扩展 pi-mcp-adapter 加载成功）
- [ ] 提交：
  - 已就绪：3 卡死修复 + parser `as` 修复 + FormatVersion 23 + 回归测试
  - 本 bug 修复后一并提交（或分两个提交：修复集 A（已就绪）/ 修复集 B（本 bug））
- [ ] （可选）`--optimize`（tree-shake + minify + bytecode-opt）打包对比冒烟
- [ ] （可选）§四限制项建跟踪 issue/文档条目
