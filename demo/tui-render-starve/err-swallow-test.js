const t0 = Date.now();
const log = (...a) => console.log(`[t=${Date.now() - t0}ms]`, ...a);
process.on("uncaughtException", (e) => log("uncaughtException:", e.message));
process.on("unhandledRejection", (e) => log("unhandledRejection:", e?.message ?? String(e)));

setTimeout(() => {
	log("render-ish callback starting");
	// 模拟 doRender 内部抛错
	throw new Error("BOOM: simulated doRender failure");
}, 16);

setTimeout(() => {
	log("still alive after the error! timer continues.");
	process.exit(0);
}, 100);
