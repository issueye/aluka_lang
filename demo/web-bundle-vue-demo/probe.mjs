// @vue/compiler-sfc 兼容性探针（tests/conformance/vue-sfc 的执行体）。
// 用法：node|aluka probe.mjs [自定义.sfc 路径]
// 输出契约（供 run.sh 双跑比对，必须跨运行时确定）：
//   成功: "sfc-probe fnv=<hex> len=<n>" + "COMPILER_SFC_OK"
//   失败: "PROBE_FAIL <name>: <message>" + 栈首帧，退出码 1
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc';

const SAMPLE = `<template>
  <div class="hi">{{ count }} <button @click="inc">+</button></div>
</template>

<script setup>
import { ref } from 'vue'
const count = ref(0)
function inc() { count.value++ }
</script>
`;

function fnv1a(s) {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = (h * 0x01000193) >>> 0;
  }
  return h.toString(16).padStart(8, '0');
}

function fail(e) {
  const frame = (e && e.stack ? e.stack : '').split('\n').filter((l) => l.trim().startsWith('at '))[0] || '';
  console.log('PROBE_FAIL ' + (e && e.name ? e.name : 'Error') + ': ' + (e && e.message ? e.message : String(e)));
  if (frame) console.log(frame.trim());
  process.exit(1);
}

const src = process.argv[2]
  ? await import('node:fs').then((fs) => fs.readFileSync(process.argv[2], 'utf8'))
  : SAMPLE;

let descriptor;
try {
  const r = parse(src, { filename: 'probe.vue' });
  if (r.errors && r.errors.length) {
    fail(r.errors[0]);
  }
  descriptor = r.descriptor;
} catch (e) {
  fail(e);
}

let code = '';
let bindings;
try {
  const s = compileScript(descriptor, { id: '7ba2c40c', inlineTemplate: false });
  code += s.content;
  bindings = s.bindings;
} catch (e) {
  fail(e);
}
try {
  const t = compileTemplate({
    source: descriptor.template.content,
    filename: 'probe.vue',
    id: '7ba2c40c',
    compilerOptions: { bindingMetadata: bindings },
  });
  if (t.errors && t.errors.length) {
    fail(t.errors[0]);
  }
  code += '\n/* template */\n' + t.code;
} catch (e) {
  fail(e);
}

console.log('sfc-probe fnv=' + fnv1a(code) + ' len=' + code.length);
console.log('COMPILER_SFC_OK');
