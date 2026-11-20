<template>
  <section class="disclosure-section panel-surface overflow-hidden">
    <button
      type="button"
      class="disclosure-section__toggle"
      :aria-expanded="expanded"
      :aria-controls="contentId"
      @click="expanded = !expanded"
    >
      <UiIcon v-if="icon" size="20" color="primary">{{ icon }}</UiIcon>
      <span class="disclosure-section__copy">
        <span class="disclosure-section__title">{{ title }}</span>
        <span v-if="description" class="disclosure-section__description">{{ description }}</span>
      </span>
      <span v-if="count !== undefined" class="disclosure-section__count">{{ count }}</span>
      <UiIcon size="20">{{ expanded ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</UiIcon>
    </button>
    <div :id="contentId" class="disclosure-section__panel" :class="{ 'is-expanded': expanded }" :inert="!expanded || undefined">
      <div class="disclosure-section__clip">
        <div class="disclosure-section__content">
          <slot v-if="!lazy || expanded"></slot>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
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
@reference "@/styles/index.css";

.disclosure-section + .disclosure-section { @apply mt-4; }
.disclosure-section__toggle { @apply flex items-center gap-3 w-full min-h-15 [padding:16px_20px] text-start text-foreground [transition:background_160ms_ease]; }
.disclosure-section__toggle:hover { @apply bg-muted/35; }
.disclosure-section__toggle[aria-expanded=true] { @apply bg-muted/25; }
.disclosure-section__copy { @apply flex flex-col [flex:1] min-w-0 gap-1; }
.disclosure-section__title { @apply [font-size:0.9375rem] [font-weight:650]; }
.disclosure-section__description { @apply text-muted-foreground [font-size:0.8125rem] [line-height:1.5]; }
.disclosure-section__count { @apply [padding:2px_8px] rounded-badge [font-size:0.75rem] tabular-nums [background:color-mix(in_srgb,_var(--primary)_8%,_transparent)] text-primary; }
/* 折叠用 grid 行过渡，只影响面板内部的布局，不触碰页面其它区域。 */
.disclosure-section__panel { @apply grid [grid-template-rows:0fr] [transition:grid-template-rows_180ms_var(--ease-out)]; }
.disclosure-section__panel.is-expanded { @apply [grid-template-rows:1fr]; }
.disclosure-section__clip { @apply min-h-0 overflow-hidden; }
.disclosure-section__content { @apply border-t border-border/70 [padding:16px_20px_20px]; }
@media (max-width: 600px) { .disclosure-section__toggle { @apply p-3.5; } .disclosure-section__content { @apply p-3.5; } }
</style>
