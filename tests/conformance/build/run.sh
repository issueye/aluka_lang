#!/usr/bin/env bash
# build --compile conformance（M1：单入口产物可执行，docs/build-compile-plan.md）。
#
# 验证：
#   1) CJS 入口（无 import/export .ts）→ 产物直接运行输出正确
#   2) ESM 入口（含 export）→ 产物直接运行输出正确
#   3) 普通 aluka（无 payload）行为不变
#   4) 尾部非 footer 垃圾 → 回退普通模式
#
# 用法：
#   ALUKA=<aluka 可执行路径> bash run.sh
set -u

ALUKA="${ALUKA:-go run ../../../cmd/aluka}"
# 相对路径的 ALUKA 需转绝对路径（子 shell 中 cd 到临时目录执行产物）。
case "$ALUKA" in
  /* | [A-Za-z]:/*) ;;
  *)
    if [ -f "$ALUKA" ] || [ -d "$ALUKA" ]; then
      ALUKA="$(cd "$(dirname "$ALUKA")" && pwd)/$(basename "$ALUKA")"
    fi
    ;;
esac

PASS=0
FAIL=0

check() { # check <名称> <期望> <实际>
  local name="$1" want="$2" got="$3"
  if [ "$got" = "$want" ]; then
    PASS=$((PASS + 1))
    echo "PASS  $name"
  else
    FAIL=$((FAIL + 1))
    echo "FAIL  $name"
    echo "       want: $want"
    echo "       got:  $got"
  fi
}

DIR="$(mktemp -d)"
trap 'rm -rf "$DIR"' EXIT

# 1) CJS 入口：无 import/export 的 .ts（console.log 直接输出）。
cat > "$DIR/hello.ts" <<'EOF'
console.log('hello from compiled aluka');
EOF
if ! $ALUKA build --compile --outfile "$DIR/hello.exe" "$DIR/hello.ts" >"$DIR/build1.log" 2>&1; then
  FAIL=$((FAIL + 1))
  echo "FAIL  build cjs entry"
  sed 's/^/       /' "$DIR/build1.log" | head -5
else
  check "compile cjs entry runs" "hello from compiled aluka" "$("$DIR/hello.exe" 2>&1)"
fi

# 2) ESM 入口：含 export 的 .ts。
cat > "$DIR/esm.ts" <<'EOF'
export const x = 42;
console.log('esm x = ' + x);
EOF
if ! $ALUKA build --compile --outfile "$DIR/esm.exe" "$DIR/esm.ts" >"$DIR/build2.log" 2>&1; then
  FAIL=$((FAIL + 1))
  echo "FAIL  build esm entry"
  sed 's/^/       /' "$DIR/build2.log" | head -5
else
  check "compile esm entry runs" "esm x = 42" "$("$DIR/esm.exe" 2>&1)"
fi

# 3) 普通 aluka（无 payload）行为不变。
check "normal mode unaffected" "2" "$($ALUKA -e 'console.log(1+1)' 2>&1)"

# 4) 尾部追加非 footer 垃圾 → 回退普通模式（不误判为产物）。
cp "$ALUKA" "$DIR/junk.exe"
printf 'JUNKJUNKJUNKJUNK' >> "$DIR/junk.exe"
check "corrupt footer falls back" "2" "$("$DIR/junk.exe" -e 'console.log(1+1)' 2>&1)"

echo ""
echo "ℹ build conformance: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
