// M6-7 diff：node:trace_events —— createTracing / getEnabledCategories / Tracing 面。
const te = require('node:trace_events');
const results = {};

results.exports = Object.keys(te).sort();
results.getEnabledCategoriesType = typeof te.getEnabledCategories;
results.getEnabledCategoriesInitial = te.getEnabledCategories();

// Tracing 对象：enabled/categories/enable/disable
{
  const t = te.createTracing({ categories: ['node.perf', 'node.http'] });
  results.categoriesStr = t.categories;
  results.enabledInitial = t.enabled;
  results.enableFn = typeof t.enable;
  results.disableFn = typeof t.disable;
  results.tracingSurface = ['enable','disable','enabled','categories']
    .map((k) => k + ':' + typeof t[k]).join(',');
  t.enable();
  results.enabledAfterEnable = t.enabled;
  t.disable();
  results.enabledAfterDisable = t.enabled;
}

// categories 传非数组会抛 ERR_INVALID_ARG_TYPE（Node 语义）
{
  let threw = null;
  try { te.createTracing({ categories: 'single.cat' }); } catch (e) { threw = e && e.code; }
  results.nonArrayCategoriesThrows = threw;
}

// categories 为空数组会抛 ERR_TRACE_EVENTS_CATEGORY_REQUIRED（Node 语义）
{
  let emptyThrew = null;
  try { te.createTracing({ categories: [] }); } catch (e) { emptyThrew = e && e.code; }
  results.emptyCategoriesThrows = emptyThrew;
}

setTimeout(() => {
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 20);
