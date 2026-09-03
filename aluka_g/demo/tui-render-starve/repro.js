// 复现 TUI 渲染调度模式：process.nextTick + setTimeout 渲染，
// 期间在跑一个"首包延迟 3s"的流式 fetch。
// 如果 render 在第一个 chunk 到达前没有发生 → 事件循环被饿死，即用户描述的症状。

const MIN_RENDER_INTERVAL_MS = 16;

let renderRequested = false;
let renderTimer = null;
let lastRenderAt = 0;
let renderCount = 0;

function doRender(tag) {
	renderCount++;
	console.log(`[render #${renderCount}] ${tag} @ t=${(Date.now() - t0)}ms`);
}

function requestRender(tag) {
	if (renderRequested) return;
	renderRequested = true;
	process.nextTick(() => scheduleRender(tag));
}

function scheduleRender(tag) {
	if (renderTimer || !renderRequested) return;
	const elapsed = Date.now() - lastRenderAt;
	const delay = Math.max(0, MIN_RENDER_INTERVAL_MS - elapsed);
	renderTimer = setTimeout(() => {
		renderTimer = undefined;
		if (!renderRequested) return;
		renderRequested = false;
		lastRenderAt = Date.now();
		doRender(tag);
	}, delay);
}

const t0 = Date.now();

async function slowFetch() {
	console.log(`[fetch] start @ t=${Date.now() - t0}ms`);
	const res = await fetch("http://127.0.0.1:18321/stream");
	console.log(`[fetch] headers arrived @ t=${Date.now() - t0}ms`);
	const reader = res.body.getReader();
	let chunks = 0;
	while (true) {
		const { value, done } = await reader.read();
		if (done) {
			console.log(`[fetch] done @ t=${Date.now() - t0}ms`);
			break;
		}
		chunks++;
		console.log(`[chunk ${chunks}] @ t=${Date.now() - t0}ms`);
		requestRender(`chunk${chunks}`); // 模拟 message_update → requestRender
	}
}

// 模拟 agent_start → requestRender
requestRender("agent_start");
console.log(`[main] requestRender done @ t=${Date.now() - t0}ms`);

void slowFetch();

// 单独计时器，观察定时器是否在此期间仍能触发
setTimeout(() => {
	console.log(`[timer 100ms] fired @ t=${Date.now() - t0}ms (renderCount=${renderCount})`);
}, 100);

setTimeout(() => {
	console.log(`[timer 1000ms] fired @ t=${Date.now() - t0}ms (renderCount=${renderCount})`);
}, 1000);

setTimeout(() => {
	console.log(`[timer 2000ms] fired @ t=${Date.now() - t0}ms (renderCount=${renderCount})`);
}, 2000);
