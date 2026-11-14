<template>
  <div class="json-tree-node" role="treeitem" :aria-level="row.depth + 1" :aria-expanded="row.container ? row.expanded : undefined" :style="{ paddingInlineStart: `${Math.min(row.depth, 12) * 14 + 4}px` }">
    <button v-if="row.container" type="button" class="node-toggle" :aria-label="`${row.expanded ? '折叠' : '展开'} ${row.key}`" @click="emit('toggle', row.path)"><v-icon :icon="row.expanded ? 'mdi-chevron-down' : 'mdi-chevron-right'" size="16" /></button>
    <span v-else class="node-toggle"></span>
    <span class="node-key" :title="row.path">{{ row.key }}</span><span class="node-colon">:</span>
    <span v-if="row.container" class="node-value">{{ row.type === 'array' ? '[…]' : '{…}' }}</span>
    <span v-else class="node-value" :class="`json-${row.type}`" :title="valueText">{{ valueText.length > 300 ? valueText.slice(0, 300) + '…' : valueText }}</span>
    <span class="node-badge">{{ row.type }}<template v-if="row.length !== undefined"> · {{ row.length }}</template></span>
    <button type="button" class="node-action" :aria-label="`复制节点 ${row.key}`" title="复制节点值" @click="emit('copy', row.value)"><v-icon icon="mdi-content-copy" size="14" /></button>
    <button v-if="row.container" type="button" class="node-action" :aria-label="`单独查看 ${row.key}`" title="从此节点查看（可继续查看任意深度）" @click="emit('focus', row)"><v-icon icon="mdi-open-in-new" size="14" /></button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { JsonTreeRow } from '@/shared/utils/jsonViewer'
const props = defineProps<{ row: JsonTreeRow }>()
const emit = defineEmits<{ toggle: [path: string]; copy: [value: unknown]; focus: [row: JsonTreeRow] }>()
const valueText = computed(() => props.row.container ? '' : JSON.stringify(props.row.value))
</script>

<style scoped>
.json-tree-node { display: flex; align-items: baseline; gap: 5px; padding-block: 5px; min-height: 30px; border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.04); font: 12px/1.6 var(--font-mono, monospace); overflow-wrap: anywhere; }
.json-tree-node:hover { background: rgba(var(--v-theme-primary), 0.06); }
.node-toggle { display: inline-flex; width: 18px; flex: 0 0 18px; align-self: flex-start; }
.node-key { color: rgb(var(--v-theme-primary)); max-width: 45%; }
.node-value { white-space: pre-wrap; min-width: 0; }
.node-colon, .node-badge { color: rgb(var(--v-theme-on-surface-variant)); }
.node-badge { white-space: nowrap; font: 10px/1.6 sans-serif; border: 1px solid rgba(var(--v-theme-on-surface), 0.15); border-radius: 4px; padding: 0 4px; }
.json-string { color: rgb(var(--v-theme-success)); }
.json-number { color: rgb(var(--v-theme-info)); }
.json-boolean { color: rgb(var(--v-theme-warning)); }
.json-null { color: rgb(var(--v-theme-secondary)); }
.node-action { flex-shrink: 0; padding: 0 3px; color: rgb(var(--v-theme-on-surface-variant)); }
button:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); }
</style>
