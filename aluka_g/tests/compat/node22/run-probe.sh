#!/usr/bin/env bash
# run-probe.sh — M0 探针双跑：node 与 aluka 分别运行四类探针，输出归一化 JSON 到 results/。
# 用法：
#   ALUKA=<aluka 可执行路径> bash run-probe.sh
# 输出：
#   results/probe-node-<probe>.json     —— node 基准
#   results/probe-aluka-<probe>.json    —— aluka 实测
#   results/summary.txt                 —— 每探针差异摘要
set -u

ALUKA="${ALUKA:-go run ../../../cmd/aluka}"
NODE="${NODE:-node}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULT_DIR="$SCRIPT_DIR/results"
PROBES=(modules globals classes events protos hooks)

mkdir -p "$RESULT_DIR"

# 相对路径的 ALUKA 转绝对路径。
case "$ALUKA" in
  /* | [A-Za-z]:/*) ;;
  *)
    if [ -f "$ALUKA" ] || [ -d "$ALUKA" ]; then
      ALUKA="$(cd "$(dirname "$ALUKA")" && pwd)/$(basename "$ALUKA")"
    fi
    ;;
esac

if ! command -v "$NODE" >/dev/null 2>&1; then
  echo "SKIP  node22 probe (node not found; set NODE=path)"
  exit 0
fi

summary="$RESULT_DIR/summary.txt"
: > "$summary"

for probe in "${PROBES[@]}"; do
  node_out="$RESULT_DIR/probe-node-$probe.json"
  aluka_out="$RESULT_DIR/probe-aluka-$probe.json"
  "$NODE" "$SCRIPT_DIR/probe/$probe.cjs" > "$node_out" 2> "$RESULT_DIR/probe-node-$probe.stderr"
  (cd "$SCRIPT_DIR" && $ALUKA "$SCRIPT_DIR/probe/$probe.cjs") > "$aluka_out" 2> "$RESULT_DIR/probe-aluka-$probe.stderr"
  # 归一化对比：仅比较 JSON 内容（不含生成噪音），逐探针输出差异数。
  node -e '
    const fs = require("fs");
    let a, b;
    try { a = JSON.parse(fs.readFileSync(process.argv[1], "utf8")); }
    catch (e) { console.log("ERROR: node 输出不可解析: " + e.message); process.exit(1); }
    try { b = JSON.parse(fs.readFileSync(process.argv[2], "utf8")); }
    catch (e) { console.log("ERROR: aluka 输出不可解析: " + e.message); process.exit(1); }
    // 递归差异统计：值不同的叶子路径。
    const diffs = [];
    function walk(x, y, p) {
      if (typeof x !== typeof y) { diffs.push(p + ": type " + typeof x + " vs " + typeof y); return; }
      if (x === null || y === null) { if (x !== y) diffs.push(p); return; }
      if (typeof x !== "object") { if (String(x) !== String(y)) diffs.push(p + ": " + JSON.stringify(x) + " vs " + JSON.stringify(y)); return; }
      if (Array.isArray(x) && Array.isArray(y)) {
        if (x.length !== y.length) diffs.push(p + ": len " + x.length + " vs " + y.length);
        const n = Math.min(x.length, y.length);
        for (let i = 0; i < n; i++) walk(x[i], y[i], p + "[" + i + "]");
        return;
      }
      const keys = new Set([...Object.keys(x), ...Object.keys(y)]);
      for (const k of keys) {
        if (!(k in x)) { diffs.push(p + "." + k + ": missing in node"); continue; }
        if (!(k in y)) { diffs.push(p + "." + k + ": missing in aluka"); continue; }
        walk(x[k], y[k], p + "." + k);
      }
    }
    walk(a, b, "");
    fs.writeFileSync(process.argv[3], diffs.slice(0, 200).join("\n") + (diffs.length > 200 ? "\n... truncated" : ""), "utf8");
    console.log(diffs.length);
  ' "$node_out" "$aluka_out" "$RESULT_DIR/diff-$probe.txt" > "$RESULT_DIR/diffcount-$probe.txt" 2>&1
  count=$(cat "$RESULT_DIR/diffcount-$probe.txt")
  echo "$probe: $count diff(s)" >> "$summary"
  echo "  probe $probe: $count diff(s) (see results/diff-$probe.txt)"
done

echo ""
echo "ℹ node22 probes: summary written to results/summary.txt"
