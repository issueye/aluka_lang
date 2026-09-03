# Aluka Web-GUI 闭环构建示例

展示 `aluka build --gui --web-entry` 一条命令从源码到单文件桌面应用程序的打包闭环。

## 目录结构

- `main.ts`：桌面应用主进程逻辑（创建窗口、注册后端 RPC、系统托盘响应）
- `src/index.tsx`：前端 Web 界面（支持 TS/TSX 组件与 CSS 样式）
- `src/style.css`：样式表

## 构建与运行

无需先运行任何外部构建工具（如 Vite / Webpack），直接执行：

```bash
# 单命令打包为单文件桌面可执行应用
aluka build --compile --gui --web-entry src/index.tsx --outfile myapp.exe main.ts

# 运行桌面应用
./myapp.exe
```

构建器将自动：
1. 解析前端 `src/index.tsx` 依赖图并完成 AST 压缩、Tree-shaking 与 CSS 合并；
2. 自动生成前端静态资产映射并内嵌至产物虚拟协议 `aluka://app/`；
3. 将桌面端主进程 `main.ts` 编译为字节码并合成单文件 PE 可执行产物（自动开启 Windows GUI 子系统免黑框）。
