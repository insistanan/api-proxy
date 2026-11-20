<template>
  <section class="context-snapshot" aria-label="本次调用的上下文组成">
    <div class="context-heading">
      <strong>上下文组成</strong>
      <span>{{ completenessLabel }}</span>
      <span v-if="snapshot.completeness !== 'unavailable'">{{ snapshot.messageCount }} / {{ snapshot.originalMessages }} 条消息 · {{ estimatedLabel(snapshot.estimatedTokens) }}</span>
    </div>
    <p class="context-notice">{{ snapshot.notice }}</p>
    <p v-if="snapshot.inputChange" class="context-notice">与入口采集内容比较：{{ snapshot.inputChange.comparable ? `新增 ${snapshot.inputChange.added} 块，移除 ${snapshot.inputChange.removed} 块${snapshot.inputChange.orderChanged ? '，顺序变化' : ''}` : snapshot.inputChange.notice }}。协议转换也可能改变内容块结构。</p>
    <div v-if="snapshot.parts.length" class="composition" aria-label="各类内容的估算比例">
      <span v-for="part in snapshot.parts" :key="part.kind" :style="{ flex: Math.max(part.estimatedTokens, 1), backgroundColor: contextKind(part.kind).color }" :title="`${contextKind(part.kind).label}：${estimatedLabel(part.estimatedTokens)}`"></span>
    </div>
    <div class="context-legend">
      <span v-for="part in snapshot.parts" :key="part.kind"><i :style="{ backgroundColor: contextKind(part.kind).color }"></i>{{ contextKind(part.kind).label }} {{ estimatedLabel(part.estimatedTokens) }}</span>
    </div>
    <details v-if="snapshot.items?.length" @toggle="browseOpen = ($event.target as HTMLElement).hasAttribute('open')">
      <summary>按内容块浏览 · {{ snapshot.items.length }} 项{{ snapshot.itemsLimited ? '（仅列出前 256 项）' : '' }}</summary>
      <template v-if="browseOpen">
        <UiSelect v-model="selectedPath" :items="itemChoices" label="选择指令、工具定义或消息内容" density="compact" hide-details class="mb-3" />
        <JsonBodyPane :body="selectedBody" title="上下文内容块" :meta="selectedPath" wrap />
      </template>
    </details>
  </section>
</template>

<script setup lang="ts">
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, ref, watch } from 'vue'
import type { ContextSnapshot } from '@/types/logs'
import { contextKind, estimatedLabel } from '@/features/observability/utils/contextPresentation'
import JsonBodyPane from '@/features/observability/components/common/JsonBodyPane.vue'

const props = defineProps<{ snapshot: ContextSnapshot; body: string }>()
const browseOpen = ref(false)
const selectedPath = ref('')
const completenessLabel = computed(() => ({ captured: '已保存正文（媒体已省略）', summary: '尾部摘要', unavailable: '不可用' })[props.snapshot.completeness])
const itemChoices = computed(() => (props.snapshot.items || []).map(item => ({ title: `${contextKind(item.kind).label} · ${item.path} · ${estimatedLabel(item.estimatedTokens)}`, value: item.path })))
const selectedBody = computed(() => {
  if (!browseOpen.value || !selectedPath.value) return ''
  try {
    let value: unknown = JSON.parse(props.body)
    for (const key of selectedPath.value.split('.')) {
      if (!value || typeof value !== 'object' || !Object.prototype.hasOwnProperty.call(value, key)) return '对应内容未保存在此正文中'
      value = (value as Record<string, unknown>)[key]
    }
    return JSON.stringify(value, null, 2) ?? '对应内容为空'
  } catch { return '正文不是完整 JSON，无法定位内容块' }
})
watch(() => props.snapshot, () => { selectedPath.value = props.snapshot.items?.[0]?.path || '' }, { immediate: true })
</script>

<style scoped>
@reference "@/styles/index.css";

.context-snapshot { @apply [padding:14px_0] [border-bottom:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] mb-4; }
.context-heading { @apply flex items-baseline flex-wrap gap-3; }
.context-heading span, .context-notice { @apply [font-size:12px] [color:color-mix(in_srgb,_var(--foreground)_65%,_transparent)]; }
.context-notice { @apply [margin:8px_0_12px]; }
.composition { @apply flex h-2 overflow-hidden rounded-badge [gap:1px]; }
.context-legend { @apply flex flex-wrap [gap:8px_18px] [margin:12px_0] [font-size:12px]; }
.context-legend span { @apply flex gap-1.5 items-center; }
.context-legend i { @apply w-2 h-2 rounded-badge; }
summary { @apply [padding:8px_0] cursor-pointer [font-size:13px]; }
</style>
