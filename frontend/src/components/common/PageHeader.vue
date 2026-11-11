<template>
  <header class="page-header">
    <div class="page-header__copy">
      <h1>{{ title }}</h1>
      <p v-if="description">{{ description }}</p>
    </div>
    <div v-if="$slots.default" class="page-header__actions"><slot></slot></div>
  </header>
</template>

<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<style scoped>
.page-header { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin-bottom: 24px; min-width: 0; }
.page-header__copy { min-width: 0; }
h1 { margin: 0; color: rgb(var(--v-theme-on-background)); font-size: 1.5rem; line-height: 1.35; font-weight: 700; letter-spacing: -0.025em; }
p { margin: 6px 0 0; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.875rem; line-height: 1.65; }
.page-header__actions { display: flex; align-items: center; flex-wrap: wrap; justify-content: flex-end; gap: 8px; max-width: 100%; }
@media (max-width: 700px) { .page-header { align-items: flex-start; flex-direction: column; gap: 16px; margin-bottom: 20px; } .page-header__actions { justify-content: flex-start; width: 100%; } h1 { font-size: 1.3rem; } }
</style>
