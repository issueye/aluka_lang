#!/usr/bin/env bash
# run.sh — 工作流 C1 微任务顺序语料双跑差分：aluka 与 node 逐行对比调度序列。
# 用法：
#   ALUKA=<aluka> NODE=<node> bash run.sh [case ...]     # 默认跑 cases/ 全部
# 输出：PASS/FAIL 逐用例 + 首个失败用例的序列差（unified diff）。
set -u

ALUKA="${ALUKA:-../../../../bin/aluka}"
NODE="${NODE:-node}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CASES_DIR="$SCRIPT_DIR/cases"

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
  echo "SKIP  microtask corpus (node not found; set NODE=path)"
  exit 0
fi

# 收集用例：显式参数或 cases/ 下全部入口（.cjs 文件与 tla-*/main.mjs）。
if [ $# -gt 0 ]; then
  targets=("$@")
else
  targets=()
  for f in "$CASES_DIR"/*.cjs; do
    [ -f "$f" ] && targets+=("$f")
  done
  for d in "$CASES_DIR"/tla-*/; do
    [ -f "$d/main.mjs" ] && targets+=("$d/main.mjs")
  done
fi

PASS=0; FAIL=0; FIRST_DIFF=""
for t in "${targets[@]}"; do
  name="$(basename "$(dirname "$t")")/$(basename "$t")"
  [ "$(basename "$t")" != "main.mjs" ] && name="$(basename "$t")"
  a_out="$(cd "$(dirname "$t")" && $ALUKA "$(basename "$t")" < /dev/null 2>/dev/null)"
  n_out="$(cd "$(dirname "$t")" && "$NODE" "$(basename "$t")" < /dev/null 2>/dev/null)"
  if [ "$a_out" = "$n_out" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL  $name"
    if [ -z "$FIRST_DIFF" ]; then
      FIRST_DIFF="$t"
    fi
  fi
done

echo ""
echo "microtask corpus: PASS=$PASS FAIL=$FAIL"
if [ -n "$FIRST_DIFF" ]; then
  echo "first failing case: $FIRST_DIFF"
  a_out="$(cd "$(dirname "$FIRST_DIFF")" && $ALUKA "$(basename "$FIRST_DIFF")" < /dev/null 2>/dev/null)"
  n_out="$(cd "$(dirname "$FIRST_DIFF")" && "$NODE" "$(basename "$FIRST_DIFF")" < /dev/null 2>/dev/null)"
  echo "--- aluka ---"; echo "$a_out"
  echo "--- node ---"; echo "$n_out"
fi
