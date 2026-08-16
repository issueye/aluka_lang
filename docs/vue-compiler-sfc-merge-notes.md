# Vue compiler-sfc 分支合并注意事项

本文记录 `feat/vue-compiler-sfc-compat` 合入 `main` 前后的门禁、已知边界和维护事项。
实现设计与开发记录分别见：

- [vue-compiler-sfc-compat-plan.md](./vue-compiler-sfc-compat-plan.md)
- [vue-compiler-sfc-dev-plan.md](./vue-compiler-sfc-dev-plan.md)

## 合并结论

当前分支可以合入 `main`。`main` 是该分支的直接祖先，没有额外主线提交，
因此可使用 fast-forward 合并。远端 CI 全绿是最终合并门禁。

## 已完成验证

本地已完成以下验证：

```bash
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go build -o bin/aluka ./cmd/aluka
ALUKA=./bin/aluka bash tests/conformance/vue-sfc/run.sh
ALUKA=./bin/aluka bash tests/conformance/webbuild/run.sh
ALUKA=./bin/aluka bash tests/conformance/build/run.sh
git diff --check
```

结果：

- 全量 Go 测试通过，包含 JIT 与 jitdiff；
- Vue SFC conformance：`PASS=1 FAIL=0 SKIP=0`；
- Webbuild conformance：`PASS=11 FAIL=0 SKIP=0`；
- Build conformance：`PASS=23 FAIL=0`；
- `git diff --check` 无空白错误。

本机未安装 `golangci-lint`，因此 lint 必须由远端 CI 验证。CI lint 未通过时
不得合并。

## 仓库体积

该分支相对 `main` 的当前规模约为：

```text
617 files changed, 359446 insertions(+), 930 deletions(-)
```

主要增量来自 `demo/web-bundle-vue-demo/node_modules/` 中提交的 Vue 3.5.13、
`@vue/compiler-sfc` 及其完整传递依赖 fixture。提交 fixture 的目的是：

- CI 和离线环境不依赖 npm registry；
- compiler-sfc 探针、正则语料和官方 SFC 后端可以稳定复现；
- Vue 与编译器版本严格锁定，不受上游发布影响。

代价是仓库 clone、索引和代码审查体积明显增加。后续升级 Vue/compiler-sfc 时，
应使用锁文件重建完整 fixture，并同时重新生成正则 corpus、Node oracle、性能数据
和产物体积基线。不要只替换单个包目录。

## SFC 功能边界

官方后端当前支持：

- 普通 `<script>`；
- `<script setup>`；
- TypeScript；
- 普通脚本 named exports；
- 官方 template 编译、指令、模板 hoist 与事件缓存。

以下结构在 graph 输入或资产管线接入前明确报错：

- `<script src>`；
- `<template src>`；
- `<style>`；
- custom block。

这些是显式能力边界，不允许静默忽略内容，也不允许自动回退到 subset 后端。
支持外部 block 时必须同步接入依赖图、watch 输入、错误位置映射和资产输出。

## 正则与字符串边界

正则实现已按 JavaScript 可见语义使用 UTF-16 索引：

- legacy 模式按 UTF-16 code unit 匹配；
- `u` 模式按 code point 匹配；
- `index`、capture span、`lastIndex`、replace/split 回调偏移使用 UTF-16 单位；
- 回溯预算耗尽返回显式错误，不再伪装成无匹配。

当前 engine.Value 字符串最终仍由 Go UTF-8 字符串承载。孤立 surrogate 的索引和
匹配边界正确，但物化或显示时可能变成 U+FFFD。若需要完整保存孤立 surrogate，
必须单独设计 UTF-16/WTF-8 字符串表示，不能只在 RegExp 层修补。

## 安全边界

`--vue-compiler=official` 会在构建期执行项目 `node_modules` 中的
compiler-sfc 及其传递依赖，权限与 `aluka run` 相同。只应对可信依赖启用。

默认后端仍是纯 Go subset。official 失败时直接返回构建错误，不会静默回退，
避免两个后端的不同输出语义掩盖问题。

## 推荐合并流程

```bash
git switch main
git merge --ff-only feat/vue-compiler-sfc-compat
```

合并前确认：

1. 远端 CI 的测试、lint 和跨平台构建全部通过；
2. 分支工作树干净；
3. `main` 仍是分支祖先；如果主线已前进，先在功能分支上 rebase 或合并主线并重跑门禁；
4. 不使用 squash 删除阶段性提交，除非维护者明确决定压缩历史。当前提交序列记录了引擎、bundler、正则和收尾修复的独立阶段，便于定位回归。

## 合并后观察项

合并后重点观察：

- 三平台 CI 中 resolver exports 条件是否一致；
- JIT 属性写入是否绕过冻结、密封或数组 length 约束；
- compiler-sfc 升级后 regex corpus checked/skipped 数量是否变化；
- official 冷启动和热 Transform 性能是否偏离现有基线；
- vendored fixture 是否显著影响 clone、checkout 或 CI cache 时间。
