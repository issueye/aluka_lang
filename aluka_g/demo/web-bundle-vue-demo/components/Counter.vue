<template>
  <div class="counter">
    <button class="btn" @click="dec">-</button>
    <span class="count">{{ count }}</span>
    <button class="btn" @click="inc">+</button>
    <small class="doubled">x2 = {{ doubled }}</small>
  </div>
</template>

<script>
import { ref, computed } from 'vue';

export default {
  setup() {
    const count = ref(0);
    const doubled = computed(() => count.value * 2);
    function inc() {
      count.value = count.value + 1;
    }
    function dec() {
      count.value = count.value - 1;
    }
    return { count, doubled, inc, dec };
  },
};
</script>
