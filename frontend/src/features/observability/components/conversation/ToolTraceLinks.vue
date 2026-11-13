<template>
  <details class="tool-trace">
    <summary>工具关联 · {{ groups.length }} 个调用 ID</summary>
    <p>{{ links.notice }}</p>
    <p v-if="links.limited" class="text-warning">关联记录达到展示上限，当前列表并非完整执行历史。</p>
    <div v-for="group in groups" :key="group.id" class="tool-group">
      <div class="tool-heading"><strong>{{ group.name || '工具名称未记录' }}</strong><code>{{ group.id }}</code><span>{{ group.results.length ? '已观测到结果' : '未观测到结果' }}</span></div>
      <div v-if="group.outputs.length > 1" class="text-caption text-warning">同一 ID 有多条模型调用记录，需结合尝试与分支判断。</div>
      <div class="tool-references">
        <v-btn v-for="(observation, index) in group.observations" :key="index" size="small" variant="text" :color="observation.isError ? 'error' : undefined" :title="`${observation.path} · ${observation.requestId}`" @click="emit('locate', observation.requestId)">
          {{ observation.role === 'result' ? (observation.isError ? '错误结果' : '工具结果') : observation.source === 'model_output' ? '模型请求调用' : '历史调用引用' }} · #{{ observation.requestId.slice(0, 8) }}
        </v-btn>
      </div>
    </div>
  </details>
</template>

<script setup lang="ts">
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
.tool-trace { margin: 12px 0 20px; border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12); padding-bottom: 12px; }
summary { cursor: pointer; padding: 8px 0; font-size: 13px; }
p { margin: 8px 0; font-size: 12px; color: rgba(var(--v-theme-on-surface), 0.65); }
.tool-group { padding: 12px 0; }
.tool-group + .tool-group { border-top: 1px solid rgba(var(--v-theme-on-surface), 0.08); }
.tool-heading { display: flex; gap: 12px; flex-wrap: wrap; align-items: baseline; font-size: 13px; }
.tool-heading code { overflow-wrap: anywhere; color: rgba(var(--v-theme-on-surface), 0.65); }
.tool-references { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 8px; }
</style>
