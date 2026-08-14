// Standard JavaScript ESM Module (.js)
export const JS_MODULE_TAG = "esm-javascript";

export function transformData(items, mapper) {
  return items.map((item, index) => {
    const mapped = mapper(item, index);
    return {
      id: `item_${index + 1}`,
      raw: item,
      processed: mapped,
      timestamp: Date.now(),
    };
  });
}

export function aggregate(items) {
  const sum = items.reduce((acc, curr) => acc + (typeof curr === "number" ? curr : 0), 0);
  const count = items.length;
  const avg = count > 0 ? sum / count : 0;
  return { sum, count, avg };
}

export default class DataFormatter {
  constructor(prefix = "DATA") {
    this.prefix = prefix;
  }

  format(val) {
    return `[${this.prefix}] ${JSON.stringify(val)}`;
  }

  getModuleInfo() {
    return {
      tag: JS_MODULE_TAG,
      url: import.meta.url,
      main: import.meta.main,
    };
  }
}
