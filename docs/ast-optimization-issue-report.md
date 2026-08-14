# AST 处理优化：进展、回归问题与剩余任务

> 目标：参考 tsgo（typescript-go）的 AST 处理架构，优化 aluka 的 AST 处理。
> 本文档记录已完成工作、真实打包回归暴露的崩溃问题、排查进展与剩余任务。
> 日期：2026-08-13

---

## 一、已完成（3 个里程碑，均已提交）

### M1 — 统一遍历基础设施（`internal/engine/ast/walk.go`，提交 `29f4016`）

- `ForEachChild(node, fn)`：全部 ~50 种节点类型子节点枚举的**单一事实来源**；
  用 reflect 剔除 typed-nil 接口陷阱（nil 具体指针赋给 Node 接口后 `!= nil` 失效）。
- `Walk(node, visit)`：通用先序深度优先遍历（visit 返回 false 跳过子树）。
- `ForEachRef(node, fn)`：引用位置遍历（跳过声明名/非计算属性键/模式绑定名/
  类方法名；命中计算属性键与真实引用；赋值语境解构目标按引用处理）。
- 测试：ForEachChild 全节点类型子节点精确计数契约；Walk 跳过子树；
  parser 真实源码全类型覆盖；ForEachRef 语义矩阵。

### M2 — 用统一遍历替换反射/手写遍历（提交 `7772a33`）

- `HasTopLevelAwait`（原 ast.go 约 200 行手写 switch）→ 基于 ForEachChild 的
  递归实现，保留"不深入函数体/类体"语义；对齐 ES 语义（for-init/for-in-of
  Left/模式默认值/计算属性键的 await 可识别；`for await` 标记位显式处理）。
- `ForEachRef` 收敛为 `RewriteRefs`（slot 级原地改写）的只读包装，单一权威
  引用位置遍历；`esm.go rewriteImportedIdentifiers` 改用 `RewriteRefs`，
  删除反射 + 字段名白名单启发式，修复计算属性 `obj[imported]` 漏改。
- `astutil.CollectRefs` → ForEachRef（修正误收集：成员属性名/嵌套函数形参/
  类方法名/对象模式键不再计入）。
- `graph.go collectDeps`（反射）→ ast.Walk。
- `compiler.go astBodyReferencesName/forLetCapturedNames/collectLoopBodyBlockNames`
  （反射 walkValue）→ ForEachChild 自递归，删除 `walkValue`。
- `clone.go DeepCopy` 保留（构建管线非热路径，留白后续生成式 clone）。

### M3 — 去重与清理（提交 `422e839`）

- 新增 `ast.PatternNames`/`ast.DeclNames`，统一 compiler/esm/shake 三处重复
  实现；顺带修复 shake 版 `declNames` 不支持解构导出 `export const {a,b}=x`。
- 删除死代码 `ast.PosFromToken` 与 ast 包不再使用的 lexer 导入。

### 已验证（M3 之前）

- `CGO_ENABLED=0 go test ./...`：28 包全过。
- jitdiff 三 tier 零失配。
- ESM live-binding + TLA + 计算属性引用端到端正确。

---

## 二、回归问题（真实打包暴露）

用 `aluka build --compile` 打包 coding-agent `src/cli.ts`：

```bash
aluka build --compile --outfile pi.exe src/cli.ts
```

- 基线（`3a512b7`，改动前）：产物可运行（`--version` 输出 `0.0.0`）。
- **新代码（M2 起）：产物启动即崩溃**：

```
module: error in "cli.ts": { name: Error, message: module: error in "config.ts":
{ name: TypeError, message: undefined is not a function, stack: TypeError:
undefined is not a function
    at config.ts:481
    at cli.ts:8 }, ... }
```

- `--no-tree-shake` 构建可运行 → 定位到 **tree-shake 管线**（CollectRefs 语义
  修正影响 shake 输出）。
- 构建产物信息：1758 模块，tree-shaken 11 模块；基线/新代码一致。

---

## 三、排查进展（已排除 vs 待查）

### 已排除（对拍测试证明无差异）

| 假设 | 验证方式 | 结论 |
|---|---|---|
| HasTopLevelAwait 改变模块 TLA 分类 | 新旧实现全模块对拍（graph 内全部 SourceUnit） | 零差异 |
| CollectRefs 改变 shake **保留模块集合** | 复制 Shake 主流程注入旧反射 refs，对比 Kept | 零差异 |
| CollectRefs 改变 **import specifier 剪枝** | 全模块对比 old>0 && new==0 的 binding | 零差异 |
| 模块集合/清单差异 | 提取两产物 payload manifest，diff 模块路径 | 1758 模块完全一致 |
| config.ts 的 ModuleKind/ModuleType 分类 | 提取 EntryInfo | 均为 esm/typescript/esm |

### 已确认的差异（关键线索）

- **config.ts 编译后字节码长度不同**：基线 `len=18148`，新代码 `len=16761`
  （差 1387 字节）。模块集合一致但字节码不同 → 差异来自**模块内 AST 剪枝**
  或 **compiler 阶段**。
- config.ts 的 ESM lower 输出经单测验证**正确**：
  `pkg = JSON.parse(__imp_0.readFileSync(getPackageJsonPath(),'utf-8'))`
  （`__imp_0 = __importReq('fs')`），且 `__filename = __imp_3.fileURLToPath(...)`
  （url）、`__dirname = __imp_2.dirname(...)`（path）均正确。
- **按提交二分**：M1（walk.go 基础设施）产物可运行；**M2 起崩溃**。

### 待查（剩余嫌疑）

1. **模块内剪枝差异**（shake 的 pruneModule ExportDecl 分支）：
   `refs := astutil.CollectRefs(stmt)` 用于"部分使用的导出声明"的
   keepDecl 判定（`refs[name] > 0` 决定是否保留声明）。新 CollectRefs 对
   成员属性名/类方法名不再计数，若某导出声明的声明名仅通过这类位置被
   "引用"，旧实现保留、新实现删除 —— 需对拍**剪枝后的 AST**（对 config.ts
   及各模块，用 `TestShakePrunedConfigDiff` 思路：`ast.DeepCopy` 后分别跑
   新旧 shake，比较 `TransformESMToCJS` 后结构哈希）。
2. **compiler 的 NFE/per-iteration walker 差异**（`astBodyReferencesName`/
   `forLetCapturedNames`/`collectLoopBodyBlockNames`）：ForEachChild 自递归
   与旧反射 walkValue 在复杂代码上的结果差异 → 字节码 slot 分配不同 →
   运行时行为变化。需在 compiler 包对拍这些 walker 在 config.ts AST 上的
   输出。
3. **esm live-binding 改写（RewriteRefs）与 shaken AST 的交互**：rewrite 在
   no-tree-shake 与 tree-shake 构建中都会执行，但 shaken AST（剪掉部分导入/
   导出后）使 lazyBindings 集合不同 —— 需确认改写是否在剪枝后漏改/错改
   某引用。

---

## 四、剩余任务清单

### 1. 定位并修复回归（P0）

- [ ] 完成 `TestShakePrunedConfigDiff`（对拍新旧 shake 后 config.ts 剪枝结果），
      确认模块内剪枝是否差异。
- [ ] 在 compiler 包对拍三个 walker（`astBodyReferencesName`/
      `forLetCapturedNames`/`collectLoopBodyBlockNames`）在 coding-agent 全模块
      AST 上的新旧输出差异。
- [ ] 对拍 `RewriteRefs` 与旧反射改写在新/旧 shaken AST 上的替换结果。
- [ ] 修复定位到的差异（倾向：CollectRefs/ForEachRef 在某参考位置的漏计，
      或 RewriteRefs 在 shaken AST 上的漏改），补回归测试。
- [ ] 重新打包 coding-agent 三档产物验证：`--version`/`--help`/offline 非交互
      路径；`CGO_ENABLED=0 go test ./...` + jitdiff 三 tier 零失配。

### 2. 后续优化（当前回归修复后的独立里程碑，按计划留白）

- [ ] **统一 `*Node` + `nodeData` 接口**（tsgo 核心形态；全部消费方改造，
      规模大，独立立项）。
- [ ] **arena 分配 / NodeFactory**（约 278 处 `&ast.X{}` 分配点改造；收益需
      基准支撑）。
- [ ] **TextRange 字节偏移位置**（联动 lexer/parser/compiler/解释器；每节点
      省内存，独立里程碑）。
- [ ] **Parent 指针**（compiler 已有作用域栈，暂无消费方）。
- [ ] **作用域感知的 CollectRefs**（当前保留保守语义：嵌套函数体内引用计入；
      后续引入绑定解析以精确剪枝）。
- [ ] **生成式 clone**（替代 `clone.go` 反射 DeepCopy；与统一 Node 形态配合）。

### 3. 清理临时调试产物

- [ ] 删除对拍/调试测试文件：`internal/bundler/astutil/zz_diff_test.go`、
      `internal/bundler/graph/zz_debug_lower_test.go`、`zz_prune_diff_test.go`、
      `zz_tla_diff_test.go`、`internal/bundler/shake/zz_keptset_diff_test.go`、
      `zz_prune_compare_test.go`、`cmd/dumpmod_tmp/`。
- [ ] 保留有价值的对拍逻辑为正式回归测试（如"旧反射 CollectRefs"仅存于
      git 历史，测试内拷贝应注明来源提交）。

---

## 五、验证命令速查

```bash
# 全量测试
CGO_ENABLED=0 go test ./...

# jitdiff 三 tier
CGO_ENABLED=0 go test ./internal/engine/interpreter/jitdiff/ -count=1

# 复现回归（需 coding-agent 源码在 E:\codes\github\pi\packages\coding-agent）
./bin/aluka build --compile --outfile /tmp/pi_aluka_check/pi.exe E:/codes/github/pi/packages/coding-agent/src/cli.ts
/tmp/pi_aluka_check/pi.exe --version   # 预期崩溃：config.ts:481 undefined is not a function
# 对照：--no-tree-shake 构建可运行（输出 0.0.0）

# 按提交二分（git worktree）
git worktree add /tmp/aluka_baseline 3a512b7   # 基线，可运行
git worktree add /tmp/aluka_m1 29f4016         # M1，可运行
git worktree add /tmp/aluka_m2 7772a33         # M2，崩溃
```
