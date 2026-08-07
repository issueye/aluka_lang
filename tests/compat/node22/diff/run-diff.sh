#!/usr/bin/env bash
# run-diff.sh — M0/M1 差分用例运行器：同一用例在 aluka 与 node22 双跑，逐行对比。
# 用例约定：输出规范化为 `result: <值>`（或 `FAIL: <错误>`）单行，或单个 JSON 行。
# 用法：
#   ALUKA=<aluka> NODE=<node> bash run-diff.sh [用例名]
set -u

ALUKA="${ALUKA:-go run ../../../../cmd/aluka}"
NODE="${NODE:-node}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

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
  echo "SKIP  node22 diff (node not found; set NODE=path)"
  exit 0
fi

# DNS 协议可用性探测：node 的 dns.resolve 走 c-ares 直连 DNS 服务器（UDP-53）。
# 沙箱/CI 屏蔽 UDP-53 时失败（而 aluka 的 Go 系统解析器仍可成功，属已知差异）。
# 探测失败则导出 DIFF_NO_DNS=1，两侧用例对 live 解析断言归一化为 'skipped'。
if ! "$NODE" -e "require('node:dns').resolve('localhost', function(e){ process.exit(e ? 1 : 0) })" >/dev/null 2>&1; then
  export DIFF_NO_DNS=1
fi

PASS=0
FAIL=0

# norm_output 归一化：剔除废弃警告与版本差异，按行排序后比较。
norm_output() {
  echo "$1" \
    | grep -v 'DeprecationWarning' \
    | grep -v -- '--trace-deprecation' \
    | grep -v '^(node:' \
    | sed 's/\r$//'
}

run_case() { # run_case <用例文件>
  local case_file="$1"
  local name
  name="$(basename "$case_file")"
  local dir
  dir="$(dirname "$case_file")"
  local a_out n_out
  # stdin 重定向 /dev/null：避免交互式 readline/网络输入造成挂起。
  a_out="$(cd "$dir" && $ALUKA "$name" < /dev/null 2>&1)"
  n_out="$(cd "$dir" && "$NODE" "$name" < /dev/null 2>&1)"
  a_out="$(norm_output "$a_out")"
  n_out="$(norm_output "$n_out")"
  if [ "$a_out" = "$n_out" ]; then
    PASS=$((PASS + 1))
    echo "PASS  $name"
  else
    FAIL=$((FAIL + 1))
    echo "FAIL  $name"
    echo "       aluka: $(echo "$a_out" | head -1)"
    echo "       node22: $(echo "$n_out" | head -1)"
  fi
}

if [ $# -gt 0 ]; then
  run_case "$SCRIPT_DIR/$1"
else
  for case_file in "$SCRIPT_DIR"/*.cjs; do
    [ -f "$case_file" ] && run_case "$case_file"
  done
fi

echo ""
echo "ℹ node22 diff: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
