<template>
  <main class="bench" data-probe="aluka-vue3-ok">
    <p class="eid">ALUKA · VUE {{ version }} · SERIAL 3.5.13</p>
    <h1>双通道校验台</h1>
    <p class="lede">
      同一份 <code>.vue</code>：<code>aluka run</code> 走官方 runtime SSR，
      <code>aluka build --target=web</code> 读 <code>aluka.config.js</code> 走 official compiler-sfc。
    </p>
    <section class="dial">
      <button type="button" class="knob" @click="dec" aria-label="decrease">−</button>
      <div class="readout">
        <span class="count">{{ count }}</span>
        <small>×2 {{ doubled }}</small>
      </div>
      <button type="button" class="knob" @click="inc" aria-label="increase">+</button>
    </section>
    <p v-if="count === 0" class="hint">计数器尚未扳动</p>
    <p v-else class="hint live">响应式已触发</p>
  </main>
</template>

<script setup>
import { computed, ref, version } from 'vue';

const count = ref(0);
const doubled = computed(() => count.value * 2);

function inc() {
  count.value += 1;
}
function dec() {
  count.value -= 1;
}
</script>

<style scoped>
.bench {
  position: relative;
  padding: 2.25rem 2rem 2rem;
  border: 1px solid var(--line);
  background:
    linear-gradient(180deg, rgba(232, 214, 176, 0.08), transparent 42%),
    var(--panel);
  box-shadow: 0 24px 60px rgba(8, 6, 3, 0.45);
}

.eid {
  margin: 0 0 0.85rem;
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.22em;
  color: var(--brass);
}

h1 {
  margin: 0 0 0.6rem;
  font-family: var(--display);
  font-size: clamp(2.1rem, 4vw, 3.1rem);
  font-weight: 600;
  line-height: 0.95;
  color: var(--paper);
}

.lede {
  max-width: 38rem;
  margin: 0 0 1.75rem;
  color: var(--muted);
  line-height: 1.55;
}

code {
  font-family: var(--mono);
  font-size: 0.86em;
  color: var(--phosphor);
}

.dial {
  display: flex;
  align-items: stretch;
  gap: 1rem;
  margin-bottom: 1.1rem;
}

.knob {
  width: 3.4rem;
  border: 1px solid var(--brass);
  background: transparent;
  color: var(--paper);
  font-family: var(--display);
  font-size: 1.8rem;
  cursor: pointer;
}

.knob:hover {
  background: rgba(201, 154, 74, 0.16);
}

.readout {
  flex: 1;
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  padding: 0.7rem 1rem 0.55rem;
  border: 1px solid rgba(122, 214, 150, 0.35);
  background: #07140d;
  color: var(--phosphor);
  font-family: var(--mono);
}

.count {
  font-size: 2.4rem;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.08em;
}

.readout small {
  opacity: 0.72;
  letter-spacing: 0.12em;
}

.hint {
  margin: 0;
  font-family: var(--mono);
  font-size: 0.78rem;
  letter-spacing: 0.08em;
  color: var(--muted);
}

.hint.live {
  color: var(--phosphor);
}
</style>
