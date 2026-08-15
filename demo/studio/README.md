# Aluka Studio —— GUI 全功能演示

基于 **Aluka GUI 子系统**（纯 Go WebView2 + `aluka://app/` 内存虚拟协议）的桌面应用演示，
覆盖当前 GUI 层的全部能力。

## 演示的能力矩阵

| 能力 | 入口 | 说明 |
| :-- | :-- | :-- |
| 窗口生命周期 | `createWindow` | 尺寸约束（min 720×480）、DevTools 开关 |
| 窗口控制 | 标题栏按钮 | 最小化 / 最大化还原 / 关闭（原生，非 HTML 模拟） |
| RPC | `app.registerRPC` ↔ `window.aluka.rpc.call` | 前后端异步调用（系统信息 / Echo） |
| 原生对话框 | `window.aluka.dialog` | Win32 MessageBox / 文件选择器 |
| 系统托盘 | `createTray` | 图标 + 右键原生菜单（含 click 回调）+ 左键恢复窗口 |
| 全局快捷键 | `globalShortcut` | `Ctrl+Alt+K` 广播事件、`Ctrl+Alt+D` 打开 DevTools（系统级，失焦仍触发） |
| 主进程 → 前端事件 | `win.emit` | 心跳广播（3s）、托盘/快捷键消息 |
| 前端 → 主进程事件 | `window.aluka.events.emit` | `ui-ready` 上报 |
| 单文件打包 | `aluka build --gui` | 前端资源内嵌 + PE 图标注入 + 免黑框 |

## 开发运行

仓库根目录执行：

```bash
./bin/aluka run demo/studio/main.ts
```

- `setAssetDir("./demo/studio/web")` 把前端目录挂载到 `aluka://app/*`（改前端文件后重跑即生效）
- 窗口出现后可试：标题栏按钮、RPC 按钮、原生对话框、`Ctrl+Alt+K`、托盘菜单

## 打包为单文件桌面应用

```bash
./bin/aluka build --compile --gui \
  --web-dir demo/studio/web \
  --icon assets/icon.ico \
  --outfile dist/Studio.exe \
  demo/studio/main.ts
```

产物 `Studio.exe`（约 37MB）特性：

- **单文件自包含**：前端 HTML/CSS/JS 全部内嵌（`aluka://app/` 内存协议加载，零 TCP 端口）
- **应用图标**：Explorer / 窗口标题栏 / 任务栏 / 托盘均为 `assets/icon.ico`（PE 资源段级替换）
- **无控制台黑框**：产物启动即分离控制台
- 双击即可运行，无需安装 Aluka / Go / Node

## 文件结构

```
demo/studio/
  main.ts          主进程：窗口 / 托盘 / 快捷键 / RPC / 事件
  web/
    index.html     前端页面（暗色主题）
    app.css        样式
    app.js         前端逻辑（window.aluka 桥接调用）
```
