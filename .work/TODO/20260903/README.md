# 2026-09-03 · 每日 TODO

> 总 TODO 见 [../README.md](../README.md)；证据规则见其 §1（**不满足即不许勾选**）。

**里程碑**：M0（ISA 规范化 + 技术原型）　|　**轨道**：F（主）、文档/仓库基建
**上一日**：无（本日为 Rust 侧作用域确立后的第一天）

---

## 1. 今日目标

1. **计划层收口**：把「Rust 版取代 Go 版」的两个硬前提与 ISA 契约架构写进计划文档，
   使 M0-M7 与退役门禁可据以执行。
2. **仓库层收口**：Go 与 Rust 两实现分目录，且**所有会静默失效的配置都跟着改对**
   （CI / gitignore / Makefile / build.bat），验证方式是全套测试与 conformance 仍绿。
3. **M0 起步**：确立 F 轨的第一件事——ISA 事实反推的输入源与 golden 语料的收割路径
   必须先证明可行，再谈规范怎么写。

---

## 2. 待办清单

| # | 待办 | 状态 | 关联总 TODO 项 |
|---|------|------|---------------|
| 1 | 计划文档改为「取代 Go 版 + 两硬前提 + ISA 契约」，阶段改 8 段、退役门禁 8 项 | `[x]` | §2 M0 前置 |
| 2 | devplan 拆 A1/A2 双轨 + 新增 F 轨 + 里程碑改 M0-M7 + 验收扩到 16 项 | `[x]` | §2 M0 前置 |
| 3 | JVM 式架构 ADR 状态改「已采纳」，补新语法接入成本模型 | `[x]` | §2 M0 前置 |
| 4 | 校对文档数字与仓库实际是否一致（套件数 / 模块数 / 代码行数） | `[x]` | 证据规则自检 |
| 5 | Go / Rust 分目录为 `aluka_g/` `aluka_r/`，docs 共享 | `[x]` | §5 目录约定 |
| 6 | 分目录后修 CI / gitignore / Makefile / build.bat，并验证测试与 conformance | `[x]` | D3 |
| 7 | 建立 `.work/TODO` 体系（总 TODO + 每日模板 + 今日） | `[x]` | §5 目录约定 |
| 8 | 确认 ISA 反推输入源与 golden 语料收割路径可行 | `[x]` | F1、F5 |
| 9 | 写出 `aluka_r/docs/aluvm-isa-spec.md` 首版 | `[ ]` | F2 |
| 10 | 补 Rust 侧工程指南：`aluka_r/AGENTS.md` + 重写 `README.md` + `docs/README.md` | `[x]` | §5 目录约定 |
| 11 | 把每日 TODO 与证据流程写进 `aluka_r/AGENTS.md`，让约定离改动最近 | `[x]` | §1 证据规则 |
| 12 | 修 `AGENTS.md`/`README.md` 迁入 `aluka_g/` 后失效的 20 处 `./docs/` 链接 | `[x]` | — |
| 13 | 重构相关文档迁入 `aluka_r/docs/`，并修全部受影响引用 | `[x]` | §5 目录约定 |

第 9 条今日未动，理由见 §5——它依赖第 8 条查明的事实量，估工超出今日剩余时间，
拆到明日做才有意义。

---

## 3. 达成目标证据

本日待办落在 5 个提交：`99c53dc`（计划文档）→ `23d8866`（目录重构 + CI 适配）→
`9f97ea3`（Go 侧文档迁移 + 链接修复）→ `dfc1c91`（Rust 侧 AGENTS/README）→
`a09e25a`（本 TODO 体系）。

### 待办 1-3 · 计划文档与 ADR

**结论**：达成
**证据类型**：提交

```
99c53dc docs: Rust 重构定为 Go 版取代方案（两硬前提 + ISA 契约 + M0-M7）
```

内容要点：终局目标从「逐子系统替换」改为**Rust 版取代 Go 版**；两硬前提
（完全兼容 JS/TS 语法 → M2；字节码升格 ISA 契约 → M4）写成里程碑；架构显式拆
alukac 前端 / aluvm 后端 / ISA 契约，crate 按组件归属重排且跨组依赖只允许经
`aluka-bytecode`；阶段路线改 8 段（0-7，约 20 月）；新增 Go 版退役门禁 8 项。
JVM 式架构 ADR 由「提案（待决策）」改为「已采纳」。

**说明**：M4 的验收不是文档自评，而是**写一个玩具 DSL 前端、只读规范不碰后端代码、
在 aluvm 上跑通**——这条是为了让「新语法无缝接入」变成可执行证据。

### 待办 4 · 文档数字校对

**结论**：达成（发现并修正 4 处不符）
**证据类型**：命令

```bash
cd aluka_g
ALUKA=./bin/aluka.exe bash tests/conformance/build/run.sh     | tail -1
ALUKA=./bin/aluka.exe bash tests/conformance/webbuild/run.sh  | tail -1
ALUKA=./bin/aluka.exe bash tests/conformance/node/run.sh      | tail -2
ALUKA=./bin/aluka.exe bash tests/conformance/node22/run.sh    | tail -1
grep -rhoE 'RegisterBuiltin\("[a-z_/0-9]+"' internal/builtin | sort -u | wc -l
```

```
ℹ build conformance: 24 passed, 0 failed
webbuild conformance: PASS=13 FAIL=0 SKIP=0
Result: 11/11 passed (0 failed)
ℹ node22 conformance: 17 passed, 0 failed
58
```

修正内容：conformance 是 **9 套**而非「25 套」；`build` 实测 24/24（AGENTS.md 写
23/23）、`webbuild` 13/13（AGENTS.md 与 README 都写 11/11）；内置模块 **58** 个而非
59；Go 代码 **17.8 万行**（源码 12.6 万 + 测试 5.2 万）而非「47 万行」。

顺带发现 Rust 侧 `PLANNED_MODULES` 漏登记 `markdown` 与 `sys`，已补齐到 58，
与 Go 版 `RegisterBuiltin` 全集逐条一致（`comm` 比对后双向差集为空）。

**说明**：这条本不在计划内，是执行证据规则时被逼出来的——文档里的数字若不能被
命令重现，就不该出现在验收表里。

### 待办 5-6 · 目录重构与配置修复

**结论**：达成
**证据类型**：提交 + 命令

```
23d8866 refactor(repo): Go 与 Rust 两实现分目录（aluka_g / aluka_r），docs 共享
```

移动完整性（1571 个删除项与 1571 个新增未追踪项双向差集为空，git 识别 1568 个
100% rename + 2 个高相似度 rename）：

```bash
git diff --name-only --diff-filter=D | sed 's|^|aluka_g/|;s|^aluka_g/rust/|aluka_r/|' | sort > /tmp/del.txt
git status --porcelain -uall | grep '^??' | sed 's|^?? ||' | sort > /tmp/unt.txt
comm -23 /tmp/del.txt /tmp/unt.txt   # 删除但无对应新增
comm -13 /tmp/del.txt /tmp/unt.txt   # 新增但无对应删除
```

```
（两条 comm 均无输出）
```

功能未回归：

```bash
cd aluka_g && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test $(go list -f '{{.Dir}}/...' -m)
cd ../aluka_r && cargo test && cargo clippy --all-targets -- -D warnings && cargo fmt --all --check
```

```
go build / go test：全 workspace 模块无失败
cargo test：21 个测试目标，39 passed; 0 failed
clippy -D warnings、fmt --check：通过
```

**说明**：这次改动的风险不在移动本身，而在**移动后仍能"跑通"的配置其实已经指错
位置**。所以逐个查了会静默失效的点：CI 加 `defaults.run.working-directory`，但
`uses:` 步骤不吃 defaults——golangci-lint 必须单独加 `working-directory`，setup-go
必须加 `cache-dependency-path`，artifact 路径要加前缀；`.gitignore` 里
`/bin/`、`/dist/`、`demo/**/dist/`、`tests/compat/node22/results/` 这类根锚定规则
若不加 `aluka_g/` 前缀就会全部失效（`bench/results/` 首先中招）。

### 待办 7 · `.work/TODO` 体系

**结论**：达成
**证据类型**：产物

```
.work/TODO/README.md            总 TODO（作用域 / 证据规则 / M0 逐条待办 / 坑清单 / 遗留登记）
.work/TODO/TEMPLATE.md          每日模板（目标 / 清单 / 证据 / 偏差 / 阻塞 / 明日入口）
.work/TODO/20260903/README.md   本文件
```

**说明**：证据规则定了三类（命令 / 产物 / 提交）与四条硬规则，其中两条来自 Go 侧
的真实翻车：性能类必须写方法学（否则热降频会伪装成代码回归）、对拍类必须写清
oracle 是谁。

### 待办 8 · ISA 反推输入源与 golden 收割路径

**结论**：达成
**证据类型**：命令

反推输入源已定位并核对数量：

```bash
cd aluka_g
sed -n '25,197p' internal/engine/bytecode/opcodes.go | grep -cE "^\tOp[A-Za-z0-9]+"
grep -n "FormatVersion = " internal/engine/bytecode/serialize.go
wc -l ../docs/bytecode-spec.md
```

```
106
77:const FormatVersion = 30 // 30: lexer 模板字面量行终止符规范化（CRLF/CR→LF）…
111 ../docs/bytecode-spec.md
```

golden 语料收割路径已实证可行（跑一次 Go 二进制即落盘一个序列化模块）：

```bash
mkdir -p /tmp/bcprobe && printf 'const a = 1 + 2;\nconsole.log(a);\n' > /tmp/bcprobe/t.js
./aluka_g/bin/aluka.exe run /tmp/bcprobe/t.js
find /tmp/bcprobe -name "*.bc" | head -1 | xargs head -c 12 | xxd
```

```
3
00000000: 414c 554b 4142 4331 1e00 0000            ALUKABC1....
```

`ALUKABC1` 是 8 字节 magic，紧随其后的 `1e 00 00 00` 是小端 u32 = **30**，与源码
`FormatVersion = 30` 吻合——**证明「不改 Go 源码、只跑二进制即可收割字节码」这条
路成立**，这是本作用域下 golden 语料可行性的关键前提。

**说明**：这条证据同时暴露了一个必须写进语料方案的约束：`.bc` 落在
`node_modules/.aluka/cache/<2 位分片>/<sha256>.bc`，缓存键包含**绝对路径与 mtime**，
所以二进制本身不可跨机复现。语料必须记录「如何重新生成」而非只存产物，否则换台
机器就对不上。

### 待办 10-11 · Rust 侧工程指南 + TODO 流程落地

**结论**：达成
**证据类型**：产物

```
aluka_r/AGENTS.md        约束 / 命令 / crate 布局 / 代码风格 / 测试约定 / 开发流程
aluka_r/README.md        重写：定位、两前提、架构、工程约定、现状与下一步
aluka_r/docs/README.md   说明该目录只放 Rust 专属文档（跨实现的进 ../docs/）
```

**说明**：`AGENTS.md` 与 `README.md` 早前被移进了 `aluka_g/`，导致仓库根和
`aluka_r/` 两侧都没有入口。本次给 Rust 侧补齐独立工程指南，并把 `.work/TODO`
的每日流程（复制模板 → 写可判定目标 → 半天粒度待办 → 逐条补证据 → 回写总 TODO）
写进去，让约定落在离改动最近的地方，而不是只存在于 `.work` 里。

### 待办 12 · 修复迁移后失效的链接

**结论**：达成
**证据类型**：命令

```bash
# 批量改写并验证
python - <<'PY'   # 把 (./docs/ 改为 (../docs/
...
PY
for l in $(grep -oE '\]\([^)h#][^)]*\)' AGENTS.md README.md docs/README.md \
        | sed 's/^](//;s/)$//' | tr -d '\r' | sort -u); do
  [ -e "$l" ] || echo "MISS $l"
done
```

```
aluka_g/AGENTS.md fixed 17 links
aluka_g/README.md fixed 3 links
all ../docs links resolve
link check done   （三份新文档零 MISS）
```

**说明**：注意 grep 输出带 `\r`（CRLF 仓库），不 `tr -d '\r'` 会把所有链接误报成
MISS——这条也是坑，验证脚本本身要先过一遍。

### 待办 13 · 重构文档迁入 `aluka_r/docs/`

**结论**：达成
**证据类型**：命令 + 提交

迁移集合（`git mv` 保历史，状态列显示为 `R` 而非 `D`+`A`）：

```
docs/rust-reimplementation-plan.md     -> aluka_r/docs/rust-reimplementation-plan.md
docs/rust-reimplementation-devplan.md  -> aluka_r/docs/rust-reimplementation-devplan.md
```

```bash
git status --short | grep '^R'
```

```
RM docs/rust-reimplementation-devplan.md -> aluka_r/docs/rust-reimplementation-devplan.md
RM docs/rust-reimplementation-plan.md    -> aluka_r/docs/rust-reimplementation-plan.md
```

**未迁**的部分是有意为之，判据写进 `aluka_r/docs/README.md`：ADR 与「Go 侧实验
证伪的历史结论」（`docs/adr/{jvm-style,object-arena,stage2-nanbox-slots}…`）、
`bytecode-spec.md`、`performance-report-*` 的主语都是 Go 版或跨实现决策，留共享目录。

受影响引用逐处改：反向链接 `aluka_g/{AGENTS,README}.md`、`.work/TODO/README.md`、
`docs/adr/jvm-style-bytecode-architecture.md`；Rust 侧 `aluka_r/{AGENTS,README}.md`、
`Cargo.toml`、`aluka-core/{lib,gc}.rs`、`aluka-builtins/lib.rs`；迁走两份的互引。

全仓校验（87 个相对链接）：

```bash
python /tmp/linkcheck.py
```

```
checked 87 relative links across *.md
ALL RESOLVE
```

**这一条的价值在于校验器抓到了我自己引入的错误**：批量替换时按 `docs/adr/` 的
目录深度算相对路径，套到了 `aluka_g/` 上，产出 `../../aluka_r/docs/…`（多跳一级）。
纯靠人眼读 diff 极易漏。修后重跑才 `ALL RESOLVE`。

校验顺带查出文档计数在迁移后失真，改为实测值：

```bash
ls docs/*.md | wc -l; ls docs/adr/*.md | wc -l; ls aluka_r/docs/*.md | wc -l
```

```
50
10
3
```

plan 原写「docs 39 份」（迁前就已过期）→ 改为 `docs/` 50 份顶层 + `docs/adr/`
10 份 ADR，并注明重构专属文档在 `aluka_r/docs/`。

顺带暴露 4 处**更早就断掉**的历史链接（上次 `aluka_g` 搬迁漏网，非本次引入），
一并修掉：

```
docs/node22-api-coverage.md    -> ./node22/../tests/compat/node22/gaps.md
docs/web-plugin-hook-fixes-plan.md -> ../internal/project
docs/performance-and-functionality-evaluation.md -> file:///E:/code/issueye/…（2 处，
  指向别人机器的绝对路径，本仓库从未有效）
```

Rust 门禁未因注释改动回归：

```
cargo fmt --all --check  -> OK
cargo test               -> 39 passed; 0 failed
cargo clippy -D warnings -> 0 error
```

**遗留说明**：校验另有 2 个"未解析"是**故意的未来路径**——`aluka_r/docs/aluvm-isa-spec.md`
与 `aluka_r/tests/golden/` 是 F2/F5 的产出目标，现在还不该存在。这类引用要能
与真正的笔误区分，所以在 plan 头部补了路径约定段（裸 `internal/…`、`tests/…`
相对 `aluka_g/`；带 `docs/`、`aluka_r/` 前缀相对仓库根），devplan 引用同一条约定。

### 附带：验证「穷尽 match = 编译期保证」这条主张

AGENTS.md 里写了「新增指令漏登记栈效果会编译失败」，这是约束不是偏好，所以实测：
临时给 `Op` 加一个 `ProbeUnregistered` 变体、不写 match 分支。

```
error[E0004]: missing match arm: `ProbeUnregistered` not covered
21 |     ProbeUnregistered,
   |     ----------------- not covered
error: could not compile `aluka-bytecode` (lib) due to 1 previous error
```

探针已回滚（`git diff --stat crates/aluka-bytecode/src/op.rs` 为空），随后
`cargo fmt --check` 通过、`cargo test` **39 passed; 0 failed**。

对比：Go 侧 `opMeta` 是稀疏 `[256]*OpMeta`，漏登记**能编译通过**。这条差异值得
保留在文档里——它是"不要为了少写 match 分支退回稀疏表"的实据。

---

## 4. 偏差与决策

| 类型 | 内容 | 影响 |
|------|------|------|
| **计划偏差** | devplan M0 原写「**Go 版** verifier 强化到通过即安全」。本日确立 Rust-only 作用域后，此项改为**只在 Rust 侧实现**，且从第一天就是完整强度 | 已记入总 TODO §0 对照表；devplan 正文暂不改（等 M0 收口一次性同步，避免文档反复） |
| **计划偏差** | devplan M0 原写「把 `docs/bytecode-spec.md` 提升为规范」。改为**新写** `aluka_r/docs/aluvm-isa-spec.md` | 原文件是 Go 实现说明且被 AGENTS.md 引用，就地改写会让一份文档同时承担「Go 实现说明」与「跨实现契约」两种身份 |
| **技术决策** | 目录命名取 `aluka_g` / `aluka_r`，docs 留根 | 计划、ADR、性能报告本质是跨实现的；若跟着 Go 侧走，Rust 侧引用要跨目录回指，且退役时还要再搬一次 |
| **技术决策** | TODO/证据流程不只写在 `.work`，同时写进 `aluka_r/AGENTS.md` | 约定若只活在 `.work`，下次会话读工程指南时看不到它，等于没有。判据是**离改动最近的那份文档**必须包含它 |
| **技术决策** | 文档里「穷尽 match 是编译期保证」这类主张必须实测，不靠推断 | 已用探针验证 `E0004`（见 §3 附带条）。文档写「会编译失败」而实际不失败，比没写更糟 |
| **技术决策** | 只迁 plan/devplan，ADR 与「Go 侧证伪实验结论」留在共享 `docs/adr/` | 判据是**主语是谁**：`jvm-style` 约束 Go 版（退役门禁）、`object-arena`/`nanbox` 的记录对象是 Go 实现。搬进 `aluka_r` 会让 Go 侧读者丢历史，且 ADR 流程本身在那边。判据写进 `aluka_r/docs/README.md` |
| **意外发现** | 文档 4 处数字与仓库实际不符（见待办 4） | 已修 |
| **意外发现** | Rust `PLANNED_MODULES` 漏 `markdown` / `sys` 两模块 | 已补；`sys` 是 `util` 的废弃别名（DEP0140）需同一对象身份，已写进注释 |
| **意外发现** | Go 侧 5 项缺陷（加载不校验、残留调试打印、`Decode` 截断容忍、栈效果双实现、conformance 改写入库文件） | 作用域外**不修**，登记总 TODO §6；其中 3 项直接转成 Rust verifier 的必测项 |
| **意外发现** | 4 处历史断链（上次 `aluka_g` 搬迁漏网 + 2 个指向他人机器的 `file:///`），非本次引入 | 一并修复；教训：改路径后必须跑**全仓**校验而非只查改动文件 |

---

## 5. 未达成与阻塞

**待办 9（`aluka_r/docs/aluvm-isa-spec.md` 首版）未动。**

- **卡在哪**：不是技术阻塞，是估工。今日查明 Go 文档的缺口远比预想大——111 行的
  `bytecode-spec.md` 对 106 条指令只举例约 30 条，且**完全没有** opcode 数值、异常
  语义、强制类型转换语义、完整文件布局、verifier 契约。逐指令规范是硬工作量。
- **已排除**：曾考虑「先写框架、指令表留 TODO 占位」。放弃——占位表会让 F2 看着
  像完成了，而 F2 的验收恰恰是「第三方读规范即可写前端」，有洞的表满足不了这条，
  反而会让后续 A1/A2 并行建立在假地基上。
- **下一步试什么**：先做 F1（把 106 条 opcode 的数值/操作数/栈效果导成机器可读表），
  用表驱动生成规范骨架，再逐条补异常与副作用语义。顺序上 F1 必须在 F2 之前。

---

## 6. 明日入口

**做 F1：导出 ISA 事实表。**

- 输入：`aluka_g/internal/engine/bytecode/meta.go`（`opMeta` 稀疏表，9 个字段/条：
  `Name`/`Operand`/`Pops`/`Pushes`/`PurePush`/`IsJump`/`IsTerminal`/`StackCond`/`VarStack`）
  与 `opcodes.go`（`iota` 块，`OpNop = 0` … `OpEnd = 105`，共 106 条）
- 输出：`.work/evidence/20260904/isa-facts.tsv`，106 行 + 表头
- 判定：行数 = 106；与 `meta.go` 逐条 diff 为空；11 种 `OperandKind` 全部出现
- 注意三件事：
  1. `opMeta` 是稀疏 `[256]*OpMeta`，**漏登记能编译通过**，只有 `meta_test.go`
     的遍历测试拦得住 → Rust 侧要用穷尽 `match` 换成编译期保证
  2. 指令操作数是**大端 uint24**，而序列化字段全是**小端**，导表时不要混
  3. `OpForInNext`(84) 与 `OpEnd`(105) 在 VM 里**没有 dispatch case**——前者是
     遗留指令（`meta.go` 有注释说明），后者是哨兵。规范里必须标明状态，不能当漏洞修
