# 🖥️ Aluka GUI 现代桌面应用示例

本 Demo 演示了如何基于 **Aluka GUI（参考 Wails v3 架构）** 构建一个极致轻量、启动飞快、单语言全栈开发的高颜值桌面应用。

---

## 🌟 核心特性展示

1. **前后端统一 TypeScript 语言**：无需切换 Go/Rust 编写主进程，直接使用标准 TS/JS；
2. **零网络端口自定义协议（`aluka://app/`）**：纯内存流式安全加载前端资产，无需本地开 HTTP 端口；
3. **现代无边框玻璃拟态 UI**：支持 Windows 11 Mica 云母特效、无边框拖拽、双向事件派发；
4. **单二进制编译分发**：支持通过 `aluka build --compile` 将前端资源与主进程字节码直接打入独立 `.exe`。

---

## 🚀 运行方式

```bash
# 执行主进程脚本
aluka run demo/gui-demo/main.ts
```
