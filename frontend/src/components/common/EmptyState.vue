<template>
  <div class="empty-state" :class="`empty-state--${variant}`">
    <v-icon v-if="icon" class="empty-state__icon" :size="variant === 'inline' ? 20 : 30">{{ icon }}</v-icon>
    <p class="empty-state__title">{{ title }}</p>
    <p v-if="description" class="empty-state__desc">{{ description }}</p>
    <div v-if="$slots.default" class="empty-state__action"><slot></slot></div>
  </div>
</template>

<script setup lang="ts">
// 统一空态：图标 + 主文案 + 辅助说明 + 可选行动，替代各页各自拼的灰字加留白。
withDefaults(defineProps<{
  title: string
  description?: string
  icon?: string
  variant?: 'panel' | 'inline' | 'table'
}>(), {
  icon: 'mdi-tray',
  variant: 'panel'
})
</script>

<style scoped>
.empty-state { display: flex; flex-direction: column; align-items: center; text-align: center; color: rgb(var(--v-theme-on-surface-variant)); }
.empty-state--panel { padding: 52px 24px; }
.empty-state--inline { padding: 24px 16px; gap: 2px; }
.empty-state--table { padding: 40px 24px; }
.empty-state__icon { margin-bottom: 12px; color: rgba(var(--v-theme-outline), 0.75); }
.empty-state--inline .empty-state__icon { margin-bottom: 6px; }
.empty-state__title { margin: 0; color: rgb(var(--v-theme-on-surface)); font-size: 0.9375rem; font-weight: 600; line-height: 1.5; }
.empty-state--inline .empty-state__title { font-size: 0.875rem; font-weight: 500; }
.empty-state__desc { max-width: 42ch; margin: 6px 0 0; font-size: 0.8125rem; line-height: 1.65; }
.empty-state__action { margin-top: 16px; display: flex; align-items: center; gap: 8px; }
</style>
