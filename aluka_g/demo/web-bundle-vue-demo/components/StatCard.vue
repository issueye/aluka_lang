<template>
  <section class="stat-card">
    <h2 class="stat-title">动态 chunk</h2>
    <pre>{{ text }}</pre>
  </section>
</template>

<script>
// 纯展示组件：真实 vue 需显式声明 props，模板 _ctx.text 才有值。
export default {
  props: ['text'],
};
</script>
