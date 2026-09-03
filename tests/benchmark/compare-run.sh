#!/usr/bin/env bash
# 跨引擎对比 harness（node vs aluka），逐用例交替执行。
#
# 三点方法学修正，都是实测踩到的坑：
#
#   1. 逐用例交替、每用例间冷却。本机持续满载约 5 秒后热降频到约 20% 速度，
#      整脚本顺序执行会让后跑的引擎与后面的用例被系统性惩罚（实测同一基准
#      连跑三遍，第三遍慢 4 倍）。
#   2. 每用例多轮取最小值。最小值是对"无干扰速度"最稳健的估计。
#   3. 用放大迭代量的脚本（perf-compare-scaled.js）。aluka 的时钟建在 Go
#      time.Since 上，本机 Windows 分辨率约 546µs，原 perf-compare.js 里
#      1-5ms 的用例读数不可用。
#
# 前置：/tmp/aluka_base.exe 与 /tmp/aluka_patched.exe（对比两个 aluka 版本）；
# 只想跑 node vs 当前构建时，把两者指向同一个二进制即可。
#
# 用法： ROUNDS=5 bash tests/benchmark/compare-run.sh
set -u
ROUNDS="${ROUNDS:-5}"
SCRIPT="${SCRIPT:-tests/benchmark/perf-compare-scaled.js}"
NODE_BIN="${NODE_BIN:-node}"
ALUKA_BASE="${ALUKA_BASE:-/tmp/aluka_base.exe}"
ALUKA_NEW="${ALUKA_NEW:-/tmp/aluka_patched.exe}"

CASES=$(REPS=1 "$NODE_BIN" "$SCRIPT" 2>/dev/null | sed 's/:.*//')
OUT=/tmp/cmp_results
rm -rf "$OUT" && mkdir -p "$OUT"

for r in $(seq 1 "$ROUNDS"); do
  for c in $CASES; do
    for spec in "node:$NODE_BIN" "base:$ALUKA_BASE" "patched:$ALUKA_NEW"; do
      label="${spec%%:*}"; bin="${spec#*:}"
      sleep 2
      ms=$(REPS=1 "$bin" "$SCRIPT" "$c" 2>/dev/null | sed 's/.*: //')
      [ -n "$ms" ] && echo "$ms" >> "$OUT/${label}__${c}"
    done
  done
  echo "round $r/$ROUNDS" >&2
done

python - "$OUT" <<'PY'
import os, sys, collections
d = sys.argv[1]
best, cases = collections.defaultdict(dict), []
for f in sorted(os.listdir(d)):
    label, name = f.split("__", 1)
    vals = [float(x) for x in open(os.path.join(d, f)) if x.strip()]
    if not vals:
        continue
    best[label][name] = min(vals)
    if name not in cases:
        cases.append(name)

print(f"{'case':<22}{'node':>9}{'base':>9}{'patched':>9}{'vs node':>10}{'patch/base':>12}")
print("-" * 71)
for c in cases:
    n, b, p = (best.get(k, {}).get(c) for k in ("node", "base", "patched"))
    if None in (n, b, p):
        continue
    print(f"{c:<22}{n:>9.0f}{b:>9.0f}{p:>9.0f}{p/n:>9.2f}x{p/b:>11.3f}x")
print("\n单位 ms，每格为多轮最小值；vs node <1 表示 aluka 更快")
PY
