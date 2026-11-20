<template>
  <details class="tool-trace">
    <summary>工具关联 · {{ groups.length }} 个调用 ID</summary>
    <p>{{ links.notice }}</p>
    <p v-if="links.limited" class="text-warning">关联记录达到展示上限，当前列表并非完整执行历史。</p>
    <div v-for="group in groups" :key="group.id" class="tool-group">
      <div class="tool-heading"><strong>{{ group.name || '工具名称未记录' }}</strong><code>{{ group.id }}</code><span>{{ group.results.length ? '已观测到结果' : '未观测到结果' }}</span></div>
      <div v-if="group.outputs.length > 1" class="text-xs text-warning">同一 ID 有多条模型调用记录，需结合尝试与分支判断。</div>
      <div class="tool-references">
        <UiButton v-for="(observation, index) in group.observations" :key="index" size="small" variant="text" :color="observation.isError ? 'error' : undefined" :title="`${observation.path} · ${observation.requestId}`" @click="emit('locate', observation.requestId)">
          {{ observation.role === 'result' ? (observation.isError ? '错误结果' : '工具结果') : observation.source === 'model_output' ? '模型请求调用' : '历史调用引用' }} · #{{ observation.requestId.slice(0, 8) }}
        </UiButton>
      </div>
    </div>
  </details>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import { computed } from 'vue'
import type { ToolLinks, ToolObservation } from '@/types/logs'

const props = defineProps<{ links: ToolLinks }>()
const emit = defineEmits<{ locate: [requestId: string] }>()
const groups = computed(() => {
  const map = new Map<string, ToolObservation[]>()
  for (const observation of props.links.observations) {
    const entries = map.get(observation.callId) || []
    entries.push(observation)
    map.set(observation.callId, entries)
  }
  return Array.from(map, ([id, observations]) => ({
    id, observations, name: observations.find(observation => observation.name)?.name,
    results: observations.filter(observation => observation.role === 'result'),
    outputs: observations.filter(observation => observation.role === 'call' && observation.source === 'model_output')
  }))
})
</script>

<style scoped>
@reference "@/styles/index.css";

.tool-trace { @apply [margin:12px_0_20px] [border-bottom:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] pb-3; }
summary { @apply cursor-pointer [padding:8px_0] [font-size:13px]; }
p { @apply [margin:8px_0] [font-size:12px] [color:color-mix(in_srgb,_var(--foreground)_65%,_transparent)]; }
.tool-group { @apply [padding:12px_0]; }
.tool-group + .tool-group { @apply [border-top:1px_solid_color-mix(in_srgb,_var(--foreground)_8%,_transparent)]; }
.tool-heading { @apply flex gap-3 flex-wrap items-baseline [font-size:13px]; }
.tool-heading code { @apply [overflow-wrap:anywhere] [color:color-mix(in_srgb,_var(--foreground)_65%,_transparent)]; }
.tool-references { @apply flex gap-1.5 flex-wrap mt-2; }
</style>
