<template>
  <div class="empty-state" :class="`empty-state--${variant}`">
    <UiIcon v-if="icon" class="empty-state__icon" :size="variant === 'inline' ? 20 : 30">{{ icon }}</UiIcon>
    <p class="empty-state__title">{{ title }}</p>
    <p v-if="description" class="empty-state__desc">{{ description }}</p>
    <div v-if="$slots.default" class="empty-state__action"><slot></slot></div>
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
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
@reference "@/styles/index.css";

.empty-state { @apply flex flex-col items-center text-center text-muted-foreground; }
.empty-state--panel { @apply [padding:52px_24px]; }
.empty-state--inline { @apply [padding:24px_16px] gap-0.5; }
.empty-state--table { @apply [padding:40px_24px]; }
.empty-state__icon { @apply mb-3 [color:color-mix(in_srgb,_var(--outline)_75%,_transparent)]; }
.empty-state--inline .empty-state__icon { @apply mb-1.5; }
.empty-state__title { @apply m-0 text-foreground [font-size:0.9375rem] font-semibold [line-height:1.5]; }
.empty-state--inline .empty-state__title { @apply [font-size:0.875rem] font-medium; }
.empty-state__desc { @apply [max-width:42ch] [margin:6px_0_0] [font-size:0.8125rem] [line-height:1.65]; }
.empty-state__action { @apply mt-4 flex items-center gap-2; }
</style>
