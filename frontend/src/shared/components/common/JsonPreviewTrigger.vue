<template>
  <div class="json-preview-trigger" :class="{ 'json-preview-trigger--inline': inline }" @mouseenter="enter" @mouseleave="leave" @focusin="enter" @focusout="leave" @click="clickArea">
    <slot></slot>
    <button v-if="!disabled" type="button" class="json-preview-trigger__button" aria-label="固定查看 JSON" title="悬浮预览，点击固定 JSON 抽屉" @click.stop="pin">
      <v-icon icon="mdi-code-json" size="16" />
      <span>{{ label }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import { useJsonViewerDrawer } from '@/shared/composables/useJsonViewerDrawer'

const props = withDefaults(defineProps<{ data: unknown; title?: string; meta?: string; inline?: boolean; label?: string; disabled?: boolean }>(), { label: 'JSON' })
const drawer = useJsonViewerDrawer()
const owner = Symbol('json-trigger')
const getSource = () => ({ data: props.data, title: props.title, meta: props.meta })
const enter = () => { if (!props.disabled) drawer.enterTrigger(owner, getSource) }
const leave = () => drawer.leaveTrigger(owner)
const pin = () => { if (!props.disabled) drawer.pinSource(owner, getSource()) }
function clickArea(event: MouseEvent) {
  // 保持编辑框、链接和原有按钮的操作；点击正文或专用按钮均可固定抽屉。
  if (event.target instanceof Element && !event.target.closest('button, input, textarea, select, a, summary, [role="button"]')) pin()
}
onBeforeUnmount(() => drawer.releaseTrigger(owner))
</script>

<style scoped>
.json-preview-trigger { min-width: 0; }
.json-preview-trigger--inline { display: inline-block; }
.json-preview-trigger__button { display: inline-flex; gap: 4px; align-items: center; padding: 3px 7px; margin-block: 3px; border-radius: 4px; color: rgb(var(--v-theme-primary)); font-size: 12px; }
.json-preview-trigger__button:hover { background: rgba(var(--v-theme-primary), 0.1); }
.json-preview-trigger__button:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
</style>
