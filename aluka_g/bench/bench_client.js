// 压测客户端：对给定 URL 按并发度压测 DURATION 秒，输出 RPS 与延迟分位。
// 用法: node bench_client.js <baseUrl> <path> <concurrency> <duration> [method] [body]
const http = require('http');

const base = process.argv[2];
const path = process.argv[3] || '/';
const concurrency = Number(process.argv[4] || 100);
const duration = Number(process.argv[5] || 3);
const method = process.argv[6] || 'GET';
const body = process.argv[7] ? process.argv[7] : null;

const url = new URL(path, base);
let completed = 0;
let errors = 0;
const latencies = [];
const start = Date.now();
const deadline = start + duration * 1000;

function request() {
	const t0 = process.hrtime.bigint();
	const req = http.request(url, { method, headers: body ? { 'Content-Type': 'application/json' } : {} }, (res, err) => {
		if (err || !res) { errors++; if (Date.now() < deadline) request(); else maybeDone(); return; }
		res.resume();
		res.on('end', () => {
			completed++;
			latencies.push(Number(process.hrtime.bigint() - t0) / 1e6);
			if (Date.now() < deadline) request(); else maybeDone();
		});
	});
	req.on('error', () => { errors++; if (Date.now() < deadline) request(); else maybeDone(); });
	if (body) req.write(body);
	req.end();
}

let finished = false;
function maybeDone() {
	if (finished || Date.now() < deadline) return;
	if (completed + errors < concurrency) return; // 仍有在途请求
	finished = true;
	const sorted = latencies.sort((a, b) => a - b);
	const p = (q) => (sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))] : 0);
	// 吞吐按压测窗口（duration）内的完成数计。
	console.log(JSON.stringify({
		concurrency, duration, completed, errors,
		rps: Math.round(completed / duration),
		p50: p(0.5).toFixed(2), p95: p(0.95).toFixed(2), p99: p(0.99).toFixed(2),
		max: (sorted.length ? sorted[sorted.length - 1] : 0).toFixed(2),
	}));
	process.exit(0);
}

// 兜底：duration + 60s 强制输出（服务器异常慢时防挂起）。
setTimeout(() => { if (!finished) maybeDone(); }, (duration + 60) * 1000);

for (let i = 0; i < concurrency; i++) request();
