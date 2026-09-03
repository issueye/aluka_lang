// 更接近 pi 真实流程的复现：
//  - 顶层 fire-and-forget main()
//  - main 内 while(true) 循环：await getUserInput() → await session.prompt()
//  - session.prompt 先同步 emit agent_start/message_start(user)（→ requestRender），再启动慢 fetch
//  - fetch 首包延迟 3s，期间渲染（nextTick+setTimeout）应发生
//  - 用定时器模拟"用户按下回车"（真实场景由 stdin data 事件驱动）

const t0 = Date.now();
const log = (...a) => console.log(`[t=${String(Date.now() - t0).padStart(5)}]`, ...a);

// ---------- TUI render 系统（与 pi-tui TuiBase 相同） ----------
const MIN_RENDER_INTERVAL_MS = 16;
let renderRequested = false;
let renderTimer = null;
let lastRenderAt = 0;
let renderCount = 0;

function doRender() {
	renderCount++;
	log(`RENDER #${renderCount} (t=${Date.now() - t0})`);
}
function requestRender() {
	if (renderRequested) return;
	renderRequested = true;
	process.nextTick(() => scheduleRender());
}
function scheduleRender() {
	if (renderTimer || !renderRequested) return;
	const elapsed = Date.now() - lastRenderAt;
	const delay = Math.max(0, MIN_RENDER_INTERVAL_MS - elapsed);
	renderTimer = setTimeout(() => {
		renderTimer = undefined;
		if (!renderRequested) return;
		renderRequested = false;
		lastRenderAt = Date.now();
		doRender();
	}, delay);
}

// ---------- agent session 模拟 ----------
class FakeAgentSession {
	constructor() {
		this.listeners = [];
		this.isStreaming = false;
	}
	subscribe(fn) {
		this.listeners.push(fn);
		return () => {};
	}
	_emit(event) {
		// 事件总线：同步触发订阅者（与 pi event-bus 相同）
		for (const l of this.listeners) l(event);
	}
	async prompt(text) {
		// ---- 同步 emit agent_start / message_start(user)（首个 await 之前）----
		log("prompt: emitting agent_start");
		this._emit({ type: "agent_start" });
		log("prompt: emitting message_start(user)");
		this._emit({ type: "message_start", message: { role: "user", content: [{ type: "text", text }] } });

		this.isStreaming = true;

		// ---- 异步部分：慢 SSE fetch ----
		const res = await fetch("http://127.0.0.1:18321/stream");
		log("prompt: fetch headers arrived");
		const reader = res.body.getReader();
		const dec = new TextDecoder();
		let buf = "";
		while (true) {
			const { value, done } = await reader.read();
			if (done) break;
			buf += dec.decode(value, { stream: true });
			log("prompt: stream chunk -> emitting message_update");
			this._emit({ type: "message_update" });
		}
		this.isStreaming = false;
		log("prompt: stream done");
	}
}

async function main() {
	const session = new FakeAgentSession();
	session.subscribe((event) => {
		// handleEvent 简化：非 assistant message_update 立即 requestRender
		log(`handleEvent(${event.type}) -> requestRender`);
		requestRender();
	});

	// while(true) 交互循环（与 interactive-mode.run 相同）
	while (true) {
		const userInput = await new Promise((resolve) => {
			// 模拟 stdin data 事件：2ms 后"用户回车"
			setTimeout(() => {
				log("stdin: Enter pressed");
				resolve("hello");
			}, 2);
		});
		log(`main: got input "${userInput}", calling session.prompt`);
		await session.prompt(userInput);
		process.exit(0);
	}
}

// fire-and-forget（与 cli.ts main(process.argv.slice(2)) 相同）
void main();

// 观察定时器
setTimeout(() => log(`checkpoint 500ms: renderCount=${renderCount}`), 500);
