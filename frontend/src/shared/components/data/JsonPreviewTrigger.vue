<template>
  <div class="json-preview-trigger" :class="{ 'json-preview-trigger--inline': inline }">
    <slot></slot>
    <button v-if="!disabled" type="button" class="json-preview-trigger__button" aria-label="查看 JSON" title="点击查看 JSON / 原文" @click.stop="open">
      <UiIcon icon="mdi-code-json" size="16" />
      <span>{{ label }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { useJsonViewerDrawer } from '@/shared/composables/useJsonViewerDrawer'

const props = withDefaults(defineProps<{ data: unknown; title?: string; meta?: string; inline?: boolean; label?: string; disabled?: boolean }>(), { label: 'JSON' })
const drawer = useJsonViewerDrawer()
function open() {
  if (!props.disabled) drawer.openSource({ data: props.data, title: props.title, meta: props.meta })
}
</script>

<style scoped>
@reference "@/styles/index.css";

.json-preview-trigger { @apply min-w-0; }
.json-preview-trigger--inline { @apply inline-block; }
.json-preview-trigger__button { @apply inline-flex gap-1 items-center [padding:3px_7px] [margin-block:3px] rounded-badge text-primary [font-size:12px]; }
.json-preview-trigger__button:hover { @apply [background:color-mix(in_srgb,_var(--primary)_10%,_transparent)]; }
.json-preview-trigger__button:focus-visible { @apply [outline:2px_solid_var(--primary)] [outline-offset:2px]; }
</style>
