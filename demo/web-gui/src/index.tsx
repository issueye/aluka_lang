import './style.css';

export function renderApp() {
    const root = document.getElementById("root");
    if (!root) return;

    root.innerHTML = `
        <div class="container">
            <h1>Aluka GUI 桌面应用</h1>
            <p>本前端界面由 <code>aluka build --gui --web-entry src/index.tsx</code> 自动打包嵌入。</p>
            <button id="rpcBtn" class="btn">调用后端 RPC</button>
            <pre id="output" style="margin-top: 16px; background: #edf2f7; padding: 12px; border-radius: 4px;"></pre>
        </div>
    `;

    document.getElementById("rpcBtn")?.addEventListener("click", async () => {
        const out = document.getElementById("output");
        if (!out) return;
        try {
            // @ts-ignore
            if (window.Aluka && window.Aluka.rpc) {
                // @ts-ignore
                const info = await window.Aluka.rpc.call("getSystemInfo");
                out.textContent = JSON.stringify(info, null, 2);
            } else {
                out.textContent = "运行在独立浏览器模式或 RPC 待挂载";
            }
        } catch (e) {
            out.textContent = "RPC 调用异常: " + String(e);
        }
    });
}

// 页面加载完成后挂载
if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', renderApp);
    } else {
        renderApp();
    }
}
