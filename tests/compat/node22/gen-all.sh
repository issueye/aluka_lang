#!/usr/bin/env bash
# gen-all.sh — M0 一键重建：manifest → 探针双跑 → 覆盖报告与缺口清单。
# 用法：bash gen-all.sh
# 输出：
#   tests/compat/node22/manifest/{modules,globals,errors,cli,entry-names}.json
#   tests/compat/node22/results/probe-*.json + diff-*.txt + summary.txt
#   docs/node22-api-coverage.md
#   tests/compat/node22/gaps.md
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "== [1/3] 生成 manifest（官方 all.json → modules/globals/errors/cli） =="
node tools/gen-manifest.mjs

echo "== [2/3] 探针双跑（node vs aluka） =="
bash run-probe.sh

echo "== [3/3] 生成覆盖报告与缺口清单 =="
node tools/gen-coverage.mjs

echo ""
echo "✅ 完成。查看：docs/node22-api-coverage.md 与 tests/compat/node22/gaps.md"
