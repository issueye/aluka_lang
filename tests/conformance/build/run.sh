#!/usr/bin/env bash
# build --compile conformance（docs/build-compile-plan.md）。
#
# M1：单入口产物可执行 + 普通模式回退。
# M2：多文件 + node_modules 静态依赖 + 循环依赖 + 动态 import + 未嵌入报错。
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

# 5) M2 多文件 + node_modules 静态依赖（ESM 导入 + CJS require 混合）。
mkdir -p "$DIR/multi/node_modules/smallpkg"
cat > "$DIR/multi/main.ts" <<'EOF'
import { greet } from './util.ts';
import { double } from './num.cjs';
const { magic } = require('smallpkg');
console.log(greet('aluka'), double(21), 'magic=' + magic());
EOF
cat > "$DIR/multi/util.ts" <<'EOF'
export function greet(n) { return 'hi ' + n; }
EOF
cat > "$DIR/multi/num.cjs" <<'EOF'
module.exports = { double: (x) => x * 2 };
EOF
cat > "$DIR/multi/node_modules/smallpkg/package.json" <<'EOF'
{ "name": "smallpkg", "version": "1.0.0", "main": "./index.js" }
EOF
cat > "$DIR/multi/node_modules/smallpkg/index.js" <<'EOF'
module.exports = { magic: () => 7 };
EOF
if ! $ALUKA build --compile --outfile "$DIR/multi.exe" "$DIR/multi/main.ts" >"$DIR/build5.log" 2>&1; then
  FAIL=$((FAIL + 1))
  echo "FAIL  build multi-file"
  sed 's/^/       /' "$DIR/build5.log" | head -5
else
  check "multi-file + node_modules" "hi aluka 42 magic=7" "$("$DIR/multi.exe" 2>&1)"
fi

# 6) M2 循环依赖（a↔b）不栈溢出。
mkdir -p "$DIR/cycle"
cat > "$DIR/cycle/a.ts" <<'EOF'
import { b } from './b.ts';
export const a = 'A' + b;
console.log('a = ' + a);
EOF
cat > "$DIR/cycle/b.ts" <<'EOF'
import { a } from './a.ts';
export const b = 'B';
EOF
if ! $ALUKA build --compile --outfile "$DIR/cycle.exe" "$DIR/cycle/a.ts" >"$DIR/build6.log" 2>&1; then
  FAIL=$((FAIL + 1))
  echo "FAIL  build circular deps"
  sed 's/^/       /' "$DIR/build6.log" | head -5
else
  check "circular deps" "a = AB" "$("$DIR/cycle.exe" 2>&1)"
fi

# 7) M2 动态 import 字面量（产物内模块）。
cat > "$DIR/dyn.ts" <<'EOF'
import('./lazy.ts').then(m => console.log('dynamic: ' + m.msg));
EOF
cat > "$DIR/lazy.ts" <<'EOF'
export const msg = 'lazy loaded';
EOF
if ! $ALUKA build --compile --outfile "$DIR/dyn.exe" "$DIR/dyn.ts" >"$DIR/build7.log" 2>&1; then
  FAIL=$((FAIL + 1))
  echo "FAIL  build dynamic import"
  sed 's/^/       /' "$DIR/build7.log" | head -5
else
  check "dynamic import" "dynamic: lazy loaded" "$("$DIR/dyn.exe" 2>&1)"
fi

# 8) M2 未嵌入模块：动态 import 变量形式（构建期无法静态收集）在运行期
#    报清晰错误；静态 require 不存在的文件在构建期即失败（正确把关）。
cat > "$DIR/external.ts" <<'EOF'
const name = './not-embedded.ts';
import(name).then(() => console.log('unexpected'), (e) => console.log('ERR: ' + e.message));
EOF
if ! $ALUKA build --compile --outfile "$DIR/external.exe" "$DIR/external.ts" >"$DIR/build8.log" 2>&1; then
  FAIL=$((FAIL + 1))
  echo "FAIL  build external-ref entry"
  sed 's/^/       /' "$DIR/build8.log" | head -5
else
  out="$("$DIR/external.exe" 2>&1)"
  case "$out" in
    *"cannot load external module"*) echo "PASS  unembedded module errors clearly"; PASS=$((PASS + 1)) ;;
    *) echo "FAIL  unembedded module errors clearly"; echo "       got: $out"; FAIL=$((FAIL + 1)) ;;
  esac
fi

echo ""
echo "ℹ build conformance: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
