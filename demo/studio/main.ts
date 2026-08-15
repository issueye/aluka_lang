// Aluka Studio —— GUI 子系统全功能演示（主进程）
//
// 开发运行（仓库根目录）：
//   ./bin/aluka run demo/studio/main.ts
// 打包为单文件桌面应用：
//   ./bin/aluka build --compile --gui --web-dir demo/studio/web \
//     --icon assets/icon.ico --outfile dist/Studio.exe demo/studio/main.ts

import { app, createWindow, createTray, setAssetDir, globalShortcut } from "aluka:gui";

// 开发模式：前端资产目录映射到 aluka://app/* 虚拟协议。
// （打包模式由 --web-dir 内嵌，此调用无害；相对路径以启动 cwd 为准）
setAssetDir("./demo/studio/web");

// 1. 主窗口：无边框 + 尺寸约束 + DevTools
//    无边框拖拽：标题栏声明 --aluka-draggable: drag（见 app.css，
//    亦兼容 -webkit-app-region / data-aluka-drag 写法），
//    边缘 6px 自动进入缩放热区，拖拽区双击最大化
const win = createWindow({
  title: "Aluka Studio",
  width: 960,
  height: 640,
  minWidth: 720,
  minHeight: 480,
  frame: false,
  devTools: true,
  url: "aluka://app/index.html",
});

// 2. RPC：前端经 window.aluka.rpc.call("name", params) 调用
app.registerRPC("getSystemInfo", () => ({
  engine: "Aluka",
  version: "0.1.0",
  pid: typeof process !== "undefined" && process.pid ? process.pid : -1,
  platform: typeof process !== "undefined" && process.platform ? process.platform : "unknown",
  uptimeSec:
    typeof process !== "undefined" && typeof process.uptime === "function"
      ? Math.round(process.uptime())
      : 0,
}));

app.registerRPC("echo", (payload) => ({
  youSent: payload,
  at: Date.now(),
}));

// 3. 系统托盘 + 原生菜单（click 回调运行在主进程）
const tray = createTray({
  tooltip: "Aluka Studio 正在运行",
  menu: [
    { label: "显示主窗口", click: () => win.show() },
    {
      label: "向前端打招呼",
      click: () => win.emit("tray-message", { from: "tray", text: "来自托盘菜单的问候 👋" }),
    },
    { type: "separator" },
    { label: "退出应用", click: () => app.quit() },
  ],
});
tray.on("click", () => win.show());

// 4. 全局快捷键：系统级生效（应用不在前台也能触发）
//    Ctrl+Alt+K → 通知前端；Ctrl+Alt+D → 打开 DevTools
globalShortcut.register("Ctrl+Alt+K", () => {
  win.emit("shortcut", { combo: "Ctrl+Alt+K", note: "全局快捷键触发" });
});
globalShortcut.register("Ctrl+Alt+D", () => win.openDevTools());

// 5. 主进程 → 前端 定向事件：心跳广播
let beats = 0;
setInterval(() => {
  beats += 1;
  win.emit("heartbeat", { beats, ts: Date.now() });
}, 3000);

// 6. 前端 → 主进程 事件（window.aluka.events.emit）
win.on("ui-ready", (data) => {
  console.log("[studio] 前端就绪:", JSON.stringify(data));
});

app.on("ready", () => console.log("[studio] Aluka Studio ready 🚀"));

// 启动 OS 消息循环（阻塞直到窗口全部关闭 / app.quit）
app.run();
