# Aluka React 18 SSR + Tailwind CSS JIT 示例项目

本项目演示了在 **Aluka 纯 Go JavaScript/TypeScript 运行时** 上原生解析、编译与服务端渲染（SSR）**React 18 JSX 组件**，并结合 **Tailwind CSS JIT 即时编译器** 实现现代化 Web 界面的全套能力。

---

## 🌟 核心特性展示

1. **源码级 JSX / TSX 原生支持**：
   - 无需 Babel、Webpack、Vite 或 esbuild 预编译，Aluka 引擎直接读取 `.jsx` / `.tsx` 文件并即时 Lowering 执行；
   - 完整支持自定义函数组件、Fragment `<>...</>`、属性展开 `{...props}` 与动态子表达式；
2. **React 18 核心与 ReactDOMServer**：
   - 纯 JS 实现的 `React.createElement`、`Fragment` 与 `ReactDOMServer.renderToString`；
   - 递归 HTML 序列化，支持自动 `className` 映射、内联 style 对象转换与 HTML 特殊字符转义；
3. **Tailwind CSS JIT 即时编译**：
   - 自动扫描所有 React 组件中声明的 Tailwind 类名，按需即时生成紧凑现代的暗色系 CSS；
4. **Node.js HTTP 服务端渲染**：
   - 基于纯 Go 内置的 `node:http` 模块对外提供极速响应的 SSR 端点。

---

## 🚀 目录结构

```
demo/react-ssr-demo/
├── src/
│   ├── react.js             # React 核心实现 (createElement / Fragment / Context / Hooks)
│   ├── server.js            # ReactDOMServer (renderToString 高性能 HTML 渲染器)
│   ├── tailwind.js          # Tailwind CSS JIT 即时编译器
│   └── components/
│       ├── Header.jsx       # 顶部导航栏组件
│       ├── MetricCard.jsx   # 核心指标卡片组件
│       ├── FeatureList.jsx  # 特性列表组件 (Fragment / map 遍历)
│       └── App.jsx          # 根组件 (暗色仪表盘布局)
├── app.js                   # HTTP SSR 服务入口
├── test.js                  # 全链路自动化测试脚本
└── README.md                # 使用说明
```

---

## 🏃 快速开始

### 1. 运行自动化全链路测试
```bash
./aluka.exe demo/react-ssr-demo/test.js
```

### 2. 启动 React SSR HTTP 服务
```bash
./aluka.exe demo/react-ssr-demo/app.js
```

### 3. 访问服务端路由
- **`http://localhost:3001/`**：查看完整的 React 18 SSR 服务端渲染页面（内嵌即时编译的 Tailwind CSS）；
- **`http://localhost:3001/api/render`**：获取 SSR 渲染的元数据与状态；
- **`http://localhost:3001/style.css`**：获取由 Tailwind JIT 实时提取生成的 CSS 样式表。
