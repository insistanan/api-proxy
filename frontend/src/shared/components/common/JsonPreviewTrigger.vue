<template>
  <div class="json-preview-trigger" :class="{ 'json-preview-trigger--inline': inline }">
    <slot></slot>
    <button v-if="!disabled" type="button" class="json-preview-trigger__button" aria-label="查看 JSON" title="点击查看 JSON / 原文" @click.stop="open">
      <v-icon icon="mdi-code-json" size="16" />
      <span>{{ label }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { useJsonViewerDrawer } from '@/shared/composables/useJsonViewerDrawer'

const props = withDefaults(defineProps<{ data: unknown; title?: string; meta?: string; inline?: boolean; label?: string; disabled?: boolean }>(), { label: 'JSON' })
const drawer = useJsonViewerDrawer()
function open() {
  if (!props.disabled) drawer.openSource({ data: props.data, title: props.title, meta: props.meta })
}
</script>

<style scoped>
.json-preview-trigger { min-width: 0; }
.json-preview-trigger--inline { display: inline-block; }
.json-preview-trigger__button { display: inline-flex; gap: 4px; align-items: center; padding: 3px 7px; margin-block: 3px; border-radius: 4px; color: rgb(var(--v-theme-primary)); font-size: 12px; }
.json-preview-trigger__button:hover { background: rgba(var(--v-theme-primary), 0.1); }
.json-preview-trigger__button:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
</style>
