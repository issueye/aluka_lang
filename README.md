# Aluka

> 用纯 Go 实现的、兼容 Bun（JavaScript 运行时）的运行时引擎。

[![CI](https://github.com/aluka-lang/aluka/actions/workflows/ci.yml/badge.svg)](https://github.com/aluka-lang/aluka/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/aluka-lang/aluka.svg)](https://pkg.go.dev/github.com/aluka-lang/aluka)
[![Go Report Card](https://goreportcard.com/badge/github.com/aluka-lang/aluka)](https://goreportcard.com/report/github.com/aluka-lang/aluka)

## 项目目标

Aluka 旨在用纯 Go 实现一个 JavaScript/TypeScript 运行时，**API 行为兼容 [Bun](https://bun.sh/)**：

- 直接运行 JS/TS 文件
- 兼容 Node.js 内置模块（fs / path / http / crypto / stream ...）
- 兼容 Web API（fetch / WebSocket / Streams ...）
- 兼容 Bun 特有 API（Bun.serve / Bun.file / Bun.$ ...）
- 单二进制分发，零运行时依赖

## 项目状态

🚧 **Phase 0 — 工程基座（已完成）**

- ✅ CLI 框架（`-e` / `run` / `--version` / `--help`）
- ✅ JS 引擎抽象层接口（`Engine` / `Context` / `Value`）
- ✅ 桩引擎（最小表达式求值器，验证端到端架构）
- ✅ `console` 全局对象（log / info / warn / error / assert / time / timeEnd）
- ✅ `process` 全局对象（argv / env / platform / cwd / hrtime ...）
- ✅ 单元测试（覆盖率 > 50%）
- ✅ GitHub Actions CI（lint + test + 跨平台 build）

详见 [开发计划文档](./docs/development-plan.md)。

后续阶段：

| Phase | 名称 | 状态 |
|-------|------|------|
| 0 | 工程基座 | ✅ |
| 1 | JS 引擎 + 模块 + TS 转译 | ⏳ |
| 2 | Node.js 核心模块 | ⏳ |
| 3 | Web API + P1 Node 模块 | ⏳ |
| 4 | Bun 特有 API | ⏳ |
| 5 | 包管理器 | ⏳ |
| 6 | 测试器 | ⏳ |
| 7 | 打包器 | ⏳ |

## 约束

- **纯 Go，禁用 CGO**（`CGO_ENABLED=0`）
- **核心组件自研**（JS 引擎 / 模块系统 / 事件循环 / TS 转译器）
- **暂不支持 JSX**
- 不引入第三方 JS 引擎

详见 [需求分析文档](./docs/requirements-analysis.md)。

## 快速开始

### 构建

```bash
# 本机构建
make build

# 或直接用 go
CGO_ENABLED=0 go build -o bin/aluka ./cmd/aluka
```

### 使用

```bash
# 执行内联代码
aluka -e "console.log(1+1)"
# => 2

# 执行文件
aluka run hello.js
aluka hello.js    # 简写

# 查看版本与帮助
aluka --version
aluka --help
```

### 示例

```bash
$ aluka -e "console.log(process.platform, process.arch)"
win32 x64

$ aluka -e "console.log('Hello, ' + 'Aluka!')"
Hello, Aluka!

$ aluka -e "console.log([1, 2, 3])"
[ 1, 2, 3 ]

$ aluka -e "console.log({ a: 1, b: 'hi' })"
{ a: 1, b: hi }
```

## 项目结构

```
aluka_lang/
├── cmd/
│   └── aluka/                 # CLI 入口
├── internal/
│   ├── engine/                 # JS 引擎（Phase 0 桩实现）
│   │   ├── engine.go           # Engine/Context/Value 接口
│   │   ├── value.go            # 值类型实现
│   │   └── stub.go             # 桩引擎（Phase 1 替换为自研 VM）
│   └── runtime/
│       └── globals/            # 全局对象
│           ├── console.go     # console 实现
│           └── process.go      # process 实现
├── docs/                       # 文档
│   ├── requirements-analysis.md
│   └── development-plan.md
├── .github/workflows/ci.yml    # CI 配置
├── Makefile
├── go.mod
└── go.sum
```

## 开发

```bash
# 运行测试
make test

# 覆盖率报告
make cover

# Lint（需要安装 golangci-lint）
make lint

# 跨平台构建
make release
```

## 设计原则

1. **纯 Go 实现** — 禁用 CGO，所有代码 `//go:build !cgo`
2. **核心自研** — JS 引擎、模块系统、事件循环、TS 转译器全部自研
3. **测试驱动** — 每个 ES 特性配 test262 子集回归
4. **渐进兼容** — 按 P0/P1/P2 优先级分阶段交付
5. **单二进制** — 静态编译，无运行时依赖

## License

MIT
