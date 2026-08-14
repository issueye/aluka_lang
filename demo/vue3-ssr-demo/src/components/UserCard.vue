<template>
  <div class="max-w-xl mx-auto p-6 bg-slate-900 border border-sky-500 rounded-2xl shadow-2xl text-slate-100">
    <div class="flex items-center justify-between border-b border-slate-800 pb-4 mb-4">
      <div>
        <h2 class="text-2xl font-bold text-sky-400">{{ user.name }}</h2>
        <p class="text-xs text-purple-400 font-mono mt-1">{{ user.role }}</p>
      </div>
      <span class="px-3 py-1 bg-emerald-900 border border-emerald-500 text-emerald-300 text-xs font-mono rounded-full">Vue 3 SFC</span>
    </div>
    <p class="text-sm text-slate-300 leading-relaxed mb-4">{{ user.bio }}</p>
    <div class="flex items-center justify-between text-xs text-slate-400 pt-3 border-t border-slate-800">
      <span>Styled with Tailwind CSS</span>
      <span class="font-mono text-sky-400">100% Pure Go VM</span>
    </div>
  </div>
</template>

<script setup>
import { reactive } from '../reactivity.js';

export const user = reactive({
  name: 'Aluka Gopher',
  role: 'Next-Gen JavaScript/TypeScript Runtime',
  bio: 'A high-performance JS/TS runtime built from scratch in 100% Pure Go without CGO, featuring full Vue 3 reactivity & Tailwind CSS JIT compilation.'
});
</script>

<style scoped>
/* Scoped styles can be mixed with Tailwind utilities */
</style>
