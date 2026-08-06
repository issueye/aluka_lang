// M6-4 diff：node:perf_hooks —— performance User Timing / PerformanceObserver /
// constants / createHistogram / monitorEventLoopDelay。
const p = require('node:perf_hooks');
const results = {};

// surface
results.exports = Object.keys(p).sort();
results.constants = JSON.stringify(p.constants);

// now / timeOrigin（类型与相对量，避免绝对值差异）
const perf = p.performance;
results.nowType = typeof perf.now();
results.nowGe0 = perf.now() >= 0;
results.timeOriginType = typeof perf.timeOrigin;
results.timeOriginBig = perf.timeOrigin > 1e12;

// mark / getEntries / measure
perf.mark('a');
const m = perf.getEntriesByType('mark')[0];
results.markName = m.name;
results.markType = m.entryType;
results.markIsPerformanceMark = m instanceof p.PerformanceMark;
perf.measure('m1', 'a');
const me = perf.getEntriesByName('m1', 'measure')[0];
results.measureType = me.entryType;
results.measureDurType = typeof me.duration;
results.measureIsPerformanceMeasure = me instanceof p.PerformanceMeasure;
results.clearMarksWorks = (perf.clearMarks('a'), perf.getEntriesByName('a', 'mark').length === 0);

// PerformanceObserver：异步派发 + disconnect 丢弃已排队记录
const seen = [];
const obs = new p.PerformanceObserver((list) => { seen.push(list.getEntries()[0].name); });
obs.observe({ entryTypes: ['mark'] });
perf.mark('observe-me');
obs.disconnect();
perf.mark('after-disconnect');
const seen2 = [];
const obs2 = new p.PerformanceObserver((list) => { seen2.push(list.getEntries()[0].name); });
obs2.observe({ entryTypes: ['mark'] });
perf.mark('delivered');

// createHistogram（确定性：记录已知整数）
const h = p.createHistogram();
h.record(5); h.record(10); h.record(10);
results.histCount = h.count;
results.histMin = h.min;
results.histMax = h.max;
results.histP100 = h.percentile(100);
results.histMeanRounded = Math.round(h.mean * 1000) / 1000;
results.histHasExceeds = 'exceeds' in h;

// monitorEventLoopDelay（最小面：初始 count=0 + enable/disable 方法）
const md = p.monitorEventLoopDelay();
results.medCount = md.count;
results.medEnableType = typeof md.enable;
results.medDisableType = typeof md.disable;

// 其他方法面
results.timerifyType = typeof perf.timerify;
results.nodeTimingDurType = typeof perf.nodeTiming.duration;
results.eluKeys = Object.keys(perf.eventLoopUtilization()).sort();
results.supportedEntryTypes = JSON.stringify(p.PerformanceObserver.supportedEntryTypes);

setTimeout(() => {
  results.observedAfterDisconnect = seen.join(',');
  results.observedDelivered = seen2.join(',');
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 40);
