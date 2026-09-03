# Aluka Web Bundle Demo

自包含示例：演示 `aluka build --target=web` 的核心能力，无任何 npm 依赖。

## 演示内容

- **TSX 组件**：`components/Card.tsx` 使用 JSX 语法，构建期自动 lower
- **极简 React shim**：`react.ts` 提供 `createElement` / `renderToString`（~30 行，无需安装 React）
- **多模块静态依赖**：入口 → Card / format / react，tree-shake 后拼接为单文件 ESM
- **动态 import 拆包**：`lib/heavy-stats.ts` 经 `import()` 拆为独立 chunk，点击按钮才加载
- **CSS**：`styles.css` 经 HTML `<link>` 引用随构建拷贝（可选 minify）
- **HTML 入口**：`index.html` 的 `<script src="./main.tsx">` 自动改写为产物路径

## 快速开始

在仓库根目录执行：

```bash
# 构建（产物到 demo/web-bundle-demo/dist/）
go run ./cmd/aluka build --target=web --outdir demo/web-bundle-demo/dist demo/web-bundle-demo/index.html

# 浏览器打开
demo/web-bundle-demo/dist/index.html
```

产物结构：

```
dist/
  index.html        # script 引用已改写为 main.js
  main.js           # 单文件 ESM bundle（静态闭包）
  chunk-xxxxxxxx.js # 动态 import 拆出的 heavy-stats chunk
  styles.css        # 样式
```

页面加载即渲染 Card；点击「加载动态 chunk」按钮触发 `import('./lib/heavy-stats.ts')`，
浏览器才请求对应 chunk 文件。

## 开发模式

```bash
# watch：源文件变更后全量重建（自动清理陈旧 chunk）
go run ./cmd/aluka build --target=web --watch --outdir demo/web-bundle-demo/dist demo/web-bundle-demo/index.html

# dev server：静态服务 + SPA fallback + SSE 热重载端点
go run ./cmd/aluka dev --port 3000 --outdir demo/web-bundle-demo/dist demo/web-bundle-demo/index.html
```

dev 模式端点：

- `GET /__aluka/health` —— 最近一次构建是否成功（JSON）
- `GET /__aluka/reload` —— SSE 流，重建成功后广播 `event: reload`

## 多格式输出

```bash
# CJS（module.exports 暴露入口导出）
go run ./cmd/aluka build --target=web --format=cjs --outfile dist/main.cjs demo/web-bundle-demo/main.tsx

# UMD（CommonJS / AMD / global 三分支；--global-name 控制全局名）
go run ./cmd/aluka build --target=web --format=umd --global-name=WebBundleDemo --outfile dist/main.umd.js demo/web-bundle-demo/main.tsx
```

## Node 中验证产物

```bash
node --input-type=module -e "import('url').then(async ({pathToFileURL}) => {
  const m = await import(pathToFileURL('dist/main.js'));
  console.log(m.render());        // Card 的 HTML 字符串
  console.log(await m.loadStats()); // 触发动态 chunk 并返回统计文本
})"
```

> 入口含 `typeof document` 守卫，Node 下导入不会触碰 DOM。
