const t0 = Date.now();
const log = (...a) => console.log(`[t=${Date.now() - t0}ms]`, ...a);
try {
	const s = new Intl.Segmenter(undefined, { granularity: "grapheme" });
	const segs = [...s.segment("hello 👨‍👩‍👧‍👦 world")];
	log("grapheme segments:", segs.length, segs.map((x) => x.segment).join("|"));
	const w = new Intl.Segmenter(undefined, { granularity: "word" });
	const wsegs = [...w.segment("hello world")];
	log("word segments:", wsegs.length, wsegs.map((x) => x.segment).join("|"));
	log("OK");
} catch (e) {
	log("FAILED:", e?.message ?? String(e), e?.stack);
}
