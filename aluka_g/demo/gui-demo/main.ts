// Aluka GUI 主进程入口脚本
// 运行命令: aluka run demo/gui-demo/main.ts

import { app, createWindow, setAssetDir } from "aluka:gui";

// 1. 设置本地开发资产目录（映射到 aluka://app/* 虚拟协议）
setAssetDir("./demo/gui-demo");

// 2. 监听应用就绪事件
app.on("ready", () => {
  console.log("🚀 Aluka GUI Application Ready!");

  // 3. 创建现代无边框桌面窗口
  const win = createWindow({
    title: "Aluka Desktop Studio",
    width: 1080,
    height: 720,
    center: true,
    url: "aluka://app/index.html",
  });

  // 4. 监听窗口生命周期事件
  win.on("show", () => {
    console.log("Window displayed to user");
  });

  win.on("close", () => {
    console.log("Window closed, preparing to exit");
  });
});

// 5. 启动 GUI 原生事件循环
app.run();
