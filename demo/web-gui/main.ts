// demo/web-gui/main.ts
// Aluka GUI 桌面端主进程逻辑

console.log("[Aluka GUI] 正在启动主窗口...");

// 注册供前端 Web 调用的 RPC 服务
Aluka.gui.app.registerRPC("getSystemInfo", function(args) {
    return {
        platform: process.platform,
        arch: process.arch,
        version: "1.0.0",
        timestamp: Date.now()
    };
});

// 创建主窗口并加载打包好的虚拟前端页面
var win = Aluka.gui.createWindow({
    title: "Aluka GUI Studio",
    width: 960,
    height: 640,
    center: true
});

win.loadURL("aluka://app/index.html");
