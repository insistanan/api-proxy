<template>
  <div class="json-tree-node" role="treeitem" :aria-level="row.depth + 1" :aria-expanded="row.container ? row.expanded : undefined" :style="{ paddingInlineStart: `${Math.min(row.depth, 12) * 14 + 4}px` }">
    <button v-if="row.container" type="button" class="node-toggle" :aria-label="`${row.expanded ? '折叠' : '展开'} ${row.key}`" @click="emit('toggle', row.path)"><UiIcon :icon="row.expanded ? 'mdi-chevron-down' : 'mdi-chevron-right'" size="16" /></button>
    <span v-else class="node-toggle"></span>
    <span class="node-key" :title="row.path">{{ row.key }}</span><span class="node-colon">:</span>
    <span v-if="row.container" class="node-value">{{ row.type === 'array' ? '[…]' : '{…}' }}</span>
    <span v-else class="node-value" :class="`json-${row.type}`" :title="valueText">{{ valueText.length > 300 ? valueText.slice(0, 300) + '…' : valueText }}</span>
    <span class="node-badge">{{ row.type }}<template v-if="row.length !== undefined"> · {{ row.length }}</template></span>
    <button type="button" class="node-action" :aria-label="`复制节点 ${row.key}`" title="复制节点值" @click="emit('copy', row.value)"><UiIcon icon="mdi-content-copy" size="14" /></button>
    <button v-if="row.container" type="button" class="node-action" :aria-label="`单独查看 ${row.key}`" title="从此节点查看（可继续查看任意深度）" @click="emit('focus', row)"><UiIcon icon="mdi-open-in-new" size="14" /></button>
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { computed } from 'vue'
import type { JsonTreeRow } from '@/shared/utils/jsonViewer'
const props = defineProps<{ row: JsonTreeRow }>()
const emit = defineEmits<{ toggle: [path: string]; copy: [value: unknown]; focus: [row: JsonTreeRow] }>()
const valueText = computed(() => props.row.container ? '' : JSON.stringify(props.row.value))
</script>

<style scoped>
@reference "@/styles/index.css";

.json-tree-node { @apply flex items-baseline [gap:5px] [padding-block:5px] min-h-7.5 [border-bottom:1px_solid_color-mix(in_srgb,_var(--foreground)_4%,_transparent)] [font:12px/1.6_var(--font-code,_monospace)] [overflow-wrap:anywhere]; }
.json-tree-node:hover { @apply [background:color-mix(in_srgb,_var(--primary)_6%,_transparent)]; }
.node-toggle { @apply inline-flex w-4.5 [flex:0_0_18px] [align-self:flex-start]; }
.node-key { @apply text-primary [max-width:45%]; }
.node-value { @apply whitespace-pre-wrap min-w-0; }
.node-colon, .node-badge { @apply text-muted-foreground; }
.node-badge { @apply whitespace-nowrap [font:10px/1.6_sans-serif] [border:1px_solid_color-mix(in_srgb,_var(--foreground)_15%,_transparent)] rounded-badge [padding:0_4px]; }
.json-string { @apply text-success; }
.json-number { @apply text-info; }
.json-boolean { @apply text-warning; }
.json-null { @apply text-secondary; }
.node-action { @apply shrink-0 [padding:0_3px] text-muted-foreground; }
button:focus-visible { @apply [outline:2px_solid_var(--primary)]; }
</style>
