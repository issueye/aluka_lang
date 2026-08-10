# Aluka 打包热点分析与字节码优化设计

> 状态：Implementation draft
> 日期：2026-08-10

## 1. 目标

Aluka 的 `build --compile` 已经把 JS/TS 编译为 VM 字节码并序列化进 payload。本次扩展建立一条可观测、可优化、可约束的闭环：

```text
模块图 -> tree-shaking -> AST minify -> VM 字节码
       -> 字节码验证/优化 -> Pack -> 原生可执行文件
```

需要同时回答：

- 哪些模块和 JSON 资源占据 payload；
- tree-shaking、AST minify、字节码优化分别节省多少；
- 哪些 CJS、动态导入或大资源阻止进一步优化；
- CI 中 payload 是否超过预算。

运行时 `--profile`/`--monitor` 仍负责 CPU、堆和 IC 热点；本设计的 `--analyze` 只负责静态打包内容热点。

## 2. CLI

```bash
# 文本热点报告
aluka build --compile --analyze ./src/index.ts

# 安全优化预设：tree-shake + AST minify + basic bytecode optimize
aluka build --compile --optimize ./src/index.ts

# JSON 报告和 CI 预算
aluka build --compile --optimize --analyze=json \
  --analyze-out dist/analyze.json --max-payload=2MB ./src/index.ts

# 只分析，不写原生可执行文件
aluka build --compile --analyze-only --bytecode-opt ./src/index.ts
```

参数：

| 参数 | 作用 |
|------|------|
| `--analyze[=text|json]` | 输出热点、阶段收益和 findings；默认 text |
| `--analyze-out=<path>` | 将报告写入文件 |
| `--analyze-only` | 构建并分析 payload，但不写 outfile |
| `--analyze-top=<n>` | 文本报告展示前 N 个模块/资源，范围 1-100 |
| `--optimize` | 启用 tree-shake、minify、bytecode-opt |
| `--bytecode-opt` | 单独启用基础字节码优化 |
| `--max-payload=<size>` | payload 超限时退出码 2，跳过产物写入 |

未显式开启新选项时，现有构建行为和 payload 格式保持不变。JSON 直出时 stdout 只写 JSON，构建摘要写入 stderr；指定 `--analyze-out` 时保持原有 stdout 摘要。

## 3. 分析阶段与指标

分析器测量真实的 `bytecode.Serialize` 大小，而不是源码字符数：

```text
raw              graph.Build 输出
shaken           tree-shake 后
minified         AST minify 后
bytecodeOptimized 字节码优化后
payload          compile.Pack 输出
artifact         原生基座 + payload + footer
```

收益计算：

```text
treeShakeSaved = raw.moduleBytes - shaken.moduleBytes
minifySaved    = shaken.moduleBytes - minified.moduleBytes
bytecodeSaved  = minified.moduleBytes - bytecodeOptimized.moduleBytes
```

报告必须区分：

- `baseBytes`：原生 Aluka 基座；
- `payloadBytes`：模块、资源和 manifest；
- `artifactBytes`：最终文件总大小。

模块报告字段包括：虚拟路径、模块类型、源码字节数、raw/final 字节码字节数、优化收益、payload 占比、依赖数、被引用数、是否第三方模块。JSON 资源报告路径、字节数、占比和引用数。

## 4. 字节码优化

### 4.1 优化位置

字节码优化必须发生在最终模块编译完成之后、`compile.Pack` 之前。它直接处理 `bytecode.Module`，不重新解析源码，也不改变模块图。

实现位置：`internal/engine/bytecode/optimize.go`。

### 4.2 首期安全优化

首期只做可以局部证明等价的变换：

1. 删除 `NOP` 和无效 `JMP +0`；
2. 删除纯字面量/`DUP` 后紧跟 `POP` 的指令对；
3. 将 `LOAD_LOCAL + GET_PROP` 融合为已有的 `GET_PROP_LOCAL` 超级指令；
4. 将条件/无条件跳转直接穿透到目标 `JMP`；
5. 运行前后字节码结构验证。

暂不做可能受闭包、`eval`、异常或代理对象影响的激进 Store 删除和常量传播。

### 4.3 PC 重定位约束

Aluka 指令固定为 4 字节，跳转操作数是带符号 24 位相对偏移。删除或融合指令后必须同步重定位：

- 所有相对跳转，包括 `OpOptionalJump` 和 `OpForInNext`；
- `TryTable.StartPC/CatchPC/FinallyPC`；
- `LineStarts` 源码行映射。

优化器对每个旧 PC 建立 `oldPC -> newPC` 映射，并在写回前验证：

- 代码长度是指令宽度整数倍；
- opcode 有效；
- 跳转目标对齐且位于代码范围内；
- try 表和行表 PC 有效。

任何校验失败都使构建失败，不生成可能损坏的产物。

### 4.4 未来优化

待真实 benchmark 证明收益后再考虑：

- 基于 CFG 的不可达块删除；
- 局部变量活跃性与死 Store 删除；
- 常量池跨函数去重；
- 更多超级指令；
- profile-guided bytecode specialization。

## 5. Findings 规则

首期使用确定性阈值，不生成模糊评分：

| ID | 条件 | 建议 |
|----|------|------|
| `DYNAMIC_IMPORT_UNRESOLVED` | 动态导入无法静态折叠 | 使用字面量或显式 specifier map |
| `TREE_SHAKE_DISABLED` | 关闭 tree-shaking | 启用 `--tree-shake` |
| `MINIFY_DISABLED` | 大模块未启用 minify | 使用 `--minify`/`--optimize` |
| `BYTECODE_OPT_DISABLED` | 大模块未启用字节码优化 | 使用 `--bytecode-opt`/`--optimize` |
| `CJS_OPTIMIZATION_LIMITED` | 大型 CJS 占 payload 10% 以上 | 优先 ESM 或更窄的 subpath 入口 |
| `MODULE_HOTSPOT` | 单模块 >= 64KB 且占 payload >= 20% | 拆分模块，检查大常量和宽依赖 |
| `ASSET_HOTSPOT` | 单资源 >= 64KB 且占 payload >= 20% | 压缩、外置或按需加载 |
| `THIRD_PARTY_DOMINANT` | node_modules 占 payload >= 60% | 检查重型依赖和子路径导入 |
| `PAYLOAD_BUDGET_EXCEEDED` | 超过 `--max-payload` | 显示预算和 payload 实际值 |

## 6. JSON 契约

```json
{
  "schemaVersion": 1,
  "reports": [{
    "entry": "src/index.ts",
    "options": {
      "treeShake": true,
      "minify": true,
      "bytecodeOptimize": true
    },
    "sizes": {
      "baseBytes": 34638336,
      "payloadBytes": 1929379,
      "artifactBytes": 36532435
    },
    "stages": {
      "raw": {"moduleCount": 42, "moduleBytes": 2579496},
      "shaken": {"moduleCount": 35, "moduleBytes": 2190288},
      "minified": {"moduleCount": 35, "moduleBytes": 1811456},
      "bytecodeOptimized": {"moduleCount": 35, "moduleBytes": 1769021}
    },
    "bytecode": {
      "instructionsBefore": 12000,
      "instructionsAfter": 11400,
      "removedInstructions": 430,
      "fusedInstructions": 170,
      "threadedJumps": 20
    },
    "modules": [],
    "removedModules": [],
    "assets": [],
    "findings": []
  }]
}
```

JSON 不包含构建机绝对路径、源码内容、字符串常量或时间戳，便于 CI 稳定 diff。

## 7. 测试与验收

- 字节码优化器：指令删除、融合、跳转穿透、try 表/行表重定位、非法 PC 拒绝；
- 分析器：阶段字节数、模块/资源排序、依赖 fan-in/fan-out、finding 边界、JSON 稳定性；
- CLI：text/json、`--analyze-only`、`--optimize` 等价性、预算超限退出码 2；
- 产物：优化前后行为一致，序列化往返通过；
- 回归：`go test ./...` 和现有 build conformance 全部通过。
