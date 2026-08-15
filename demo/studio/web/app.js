// Aluka Studio 前端逻辑 —— 全部能力经 window.aluka 桥接（自动注入）

const $ = (sel) => document.querySelector(sel);
const logEl = $("#log");

function log(event, text) {
  const row = document.createElement("div");
  const t = new Date().toLocaleTimeString();
  row.innerHTML = `<span class="t">${t}</span><span class="ev-${event}">[${event}]</span> ${text}`;
  logEl.appendChild(row);
  logEl.scrollTop = logEl.scrollHeight;
}

// ---------- 窗口控制（原生，非 HTML 模拟） ----------
$("#btn-min").onclick = () => window.aluka.window.minimize();
$("#btn-max").onclick = () => window.aluka.window.toggleMaximize();
$("#btn-close").onclick = () => window.aluka.window.close();

// ---------- RPC：调用主进程注册的方法 ----------
async function refreshSysInfo() {
  const info = await window.aluka.rpc.call("getSystemInfo");
  const kv = $("#sysinfo");
  kv.innerHTML = Object.entries(info)
    .map(([k, v]) => `<dt>${k}</dt><dd>${v}</dd>`)
    .join("");
  log("rpc", "rpc.call('getSystemInfo') 完成");
}
$("#btn-refresh").onclick = refreshSysInfo;

$("#btn-echo").onclick = async () => {
  const res = await window.aluka.rpc.call("echo", { hello: "aluka", n: 42 });
  log("rpc", `echo 回包: ${JSON.stringify(res)}`);
};

// ---------- 原生对话框 ----------
$("#btn-msgbox").onclick = async () => {
  const btn = await window.aluka.dialog.showMessageBox({
    title: "Aluka Studio",
    message: "这是原生 Win32 MessageBox，不是 HTML 模拟。",
  });
  log("dialog", `showMessageBox 返回按钮索引: ${btn}`);
};

$("#btn-openfile").onclick = async () => {
  const files = await window.aluka.dialog.showOpenDialog({ title: "选择一个文件" });
  log("dialog", `showOpenDialog: ${files && files.length ? files[0] : "(已取消)"}`);
};

// ---------- 双向事件 ----------
window.aluka.events.on("heartbeat", (d) => {
  $("#beat").textContent = `beats: ${d.beats}`;
});

window.aluka.events.on("tray-message", (d) => log("tray-message", d.text));
window.aluka.events.on("shortcut", (d) => log("shortcut", `${d.combo} 触发：${d.note}`));

// ---------- 前端 → 主进程事件 ----------
window.aluka.events.emit("ui-ready", { ua: navigator.userAgent.slice(0, 40) });

refreshSysInfo();
