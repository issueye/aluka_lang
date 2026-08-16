# vue-sfc 差分 gate——gap 台账

驱动式修复循环的审计记录（`docs/vue-compiler-sfc-dev-plan.md` §4）。
每条 gap：现象 / 最小复现 / 规范依据 / 修复 commit，闭环后标记 ✅。

| # | 状态 | 现象 | 根因 | 修复 |
|---|------|------|------|------|
| G1 | ✅ 已闭环 | 探针死于 `TypeError: Object prototype may only be an Object or null`（postcss `_inheritsLoose` → `Object.create(superClass.prototype)`） | `Object.defineProperty` 部分描述符丢失 `value`（描述符只给 `writable` 时把现值重置为 undefined），且 `writable/enumerable/configurable` 完全不执行。7 行复现：`function N(){}; N.prototype.w=1; Object.defineProperty(N,'prototype',{writable:false}); typeof N.prototype` → node `object` / 旧 aluka `undefined` | `engine.DefineOwnProperty`（规范子集：部分描述符合并/标志执行/校验）+ `objectValue.attrs` 惰性标志字典 + IC `SetCached` 不可写守卫 + `ObjectUnwrapper`（Closure/NativeMethod/vmClosure 解包） |

## 修复 G1 期间发现的次生缺口（均已闭环）

- **函数对象访问器丢失**：`Object.defineProperty(fn, 'g', {get(){...}})` 后 `fn.g === undefined`。
  根因：`Closure` 等包装类型靠委托实现 `engine.Object`，engine 侧类型 switch 认不出，
  静默走退化路径。修复：`engine.ObjectUnwrapper` 可选接口 + `unwrapObjectValue`，
  `DefineOwnProperty/AttrsOf/AllOwnKeys/GetOwnSlot` 统一解包。
- **SameValue 对数值不可靠**：`numberValue` 是包着 `*numberBox` 的值类型（接口存值形态），
  指针断言失败后落到结构体 `==`（比较不同 slab box 指针）→ 等值重定义误判为不等。
  修复：`sameValue` 按数值比较（含 NaN 相等）。

## 已知边界（未修，按需驱动）

- 严格模式下 `writable:false` 赋值应抛 TypeError——当前统一 sloppy 静默
  （VM 未建模严格性，接入后区分）。
- `Object.freeze/seal/preventExtensions` 仍为 stub——非可扩展目标拒绝
  新属性定义的校验待 extensibility 建模后补。
- SameValue 的 `+0/-0` 区分未实现（`+0 === -0` 视为相等）。
- JIT Native 层属性直写不查 `attrs`（tier0 静默忽略 vs JIT 写入理论上可分歧）；
  jitdiff 三 tier 当前零失配，真实语料命中后再处理。

## M4（official bundler 后端）实现要点

- `internal/bundler/vue/official.go`：构建 VM 上经 `module.Loader.RequireModule`
  加载 `vue/compiler-sfc`（SetNoCache 保持 webbuild 缓存约束；`builtin.RegisterAll`
  补 path/util 等内置；裸 VM 补 `process.env.NODE_ENV=production` 与 no-op console）。
- 驱动器挂接采用 Vite 同款模式：`export default` → `const __sfc__` +
  `__sfc__.render = render`——遗漏挂接时选项式组件无 render，SSR 渲染为 `<!---->`
  空占位（已由集成测试锁定）。
- require 解析基准传入口**文件**（resolver 从父文件目录向上爬 node_modules，
  传目录会差一层）。

## M3（正则加固）闭环记录

- `tools/extract-regex-corpus.mjs` 从 compiler-sfc 依赖闭包提取 182 个去重模式；
  174 个模式可同时由 RE2 翻译层与自研回溯引擎编译，×30 个领域/通用输入
  共 5220 次逐捕获组索引对拍零差异（8 个模式因任一侧不支持明确 skip）。
- 对拍驱动修复三类真实语义 bug（Node 22 为裁判）：
  1. 懒量词位于捕获组内时，回退多吃字符后未更新捕获终点（`(.*?)$`）；
  2. 字符类转义未区分单 rune 与集合转义，作为区间端点时以 `lo=0`
     误构造超宽区间（`[ -,\.\/:-@\[-\^`{-~]`）；
  3. `[^]` 语义二次取反：旧实现给 negated 空类塞全范围 part 后再整体取反，
     变成永不匹配；现以 negated + 空 parts 自然表示任意字符。
- 回溯引擎增加一次 exec 全局共享的 `btMaxSteps=2^22` 步数预算，覆盖候选
  起始位置和 lookaround 子状态；超限保守返回不匹配。测试经低预算
  `execWithLimit(...,32)` 明确断言 `aborted=true`，并验证正常匹配不被误杀。
