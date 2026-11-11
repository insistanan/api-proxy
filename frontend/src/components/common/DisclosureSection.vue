<template>
  <section class="disclosure-section">
    <button
      type="button"
      class="disclosure-section__toggle"
      :aria-expanded="expanded"
      :aria-controls="contentId"
      @click="expanded = !expanded"
    >
      <v-icon v-if="icon" size="20" color="primary">{{ icon }}</v-icon>
      <span class="disclosure-section__copy">
        <span class="disclosure-section__title">{{ title }}</span>
        <span v-if="description" class="disclosure-section__description">{{ description }}</span>
      </span>
      <span v-if="count !== undefined" class="disclosure-section__count">{{ count }}</span>
      <v-icon size="20">{{ expanded ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
    </button>
    <div v-show="expanded" :id="contentId" class="disclosure-section__content">
      <slot v-if="!lazy || expanded"></slot>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useId } from 'vue'

withDefaults(defineProps<{
  title: string
  description?: string
  icon?: string
  count?: number
  lazy?: boolean
}>(), { lazy: false })

const expanded = defineModel<boolean>({ default: false })
const contentId = useId()
// 表单默认保留子组件，折叠不会丢失校验与草稿；只读大列表可按需挂载。
</script>

<style scoped>
.disclosure-section { border: 1px solid rgba(var(--v-theme-outline), 0.24); border-radius: var(--cut-md); background: rgb(var(--v-theme-surface)); overflow: hidden; }
.disclosure-section + .disclosure-section { margin-top: 16px; }
.disclosure-section__toggle { display: flex; align-items: center; gap: 12px; width: 100%; min-height: 60px; padding: 16px 20px; text-align: start; color: rgb(var(--v-theme-on-surface)); transition: background 160ms ease; }
.disclosure-section__toggle:hover { background: rgba(var(--v-theme-primary), 0.04); }
.disclosure-section__copy { display: flex; flex-direction: column; flex: 1; min-width: 0; gap: 4px; }
.disclosure-section__title { font-size: 0.9375rem; font-weight: 650; }
.disclosure-section__description { color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; line-height: 1.5; }
.disclosure-section__count { padding: 2px 8px; border-radius: 6px; font-size: 0.75rem; font-variant-numeric: tabular-nums; background: rgba(var(--v-theme-primary), 0.08); color: rgb(var(--v-theme-primary)); }
.disclosure-section__content { padding: 4px 20px 20px; }
@media (max-width: 600px) { .disclosure-section__toggle { padding: 14px; } .disclosure-section__content { padding: 4px 14px 16px; } }
</style>
