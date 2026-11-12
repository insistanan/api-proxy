<template>
  <section class="context-trend" aria-label="全会话上下文趋势">
    <div class="trend-heading"><strong>上下文趋势</strong><span v-if="overview">全会话 {{ overview.calls }} 次调用 · {{ overview.knownCalls }} 次有可比较快照</span></div>
    <v-alert v-if="error" type="error" variant="tonal" density="compact">{{ error }} <v-btn size="small" variant="text" @click="load">重试</v-btn></v-alert>
    <p v-else-if="!overview">{{ loading ? '正在读取会话摘要…' : '暂无趋势数据' }}</p>
    <template v-if="overview">
      <p>{{ overview.bucketSize > 1 ? `每组最多 ${overview.bucketSize} 次调用，显示组内上下文估算峰值` : '按调用顺序展示已保存输入的上下文估算' }}。尾部摘要、缺失数据与服务端历史引用不参与比较。</p>
      <div v-if="overview.knownCalls" class="trend-chart" :aria-busy="loading">
        <button v-for="(point, index) in overview.points" :key="index" type="button" class="trend-column" :disabled="!point.requestId" :aria-label="pointLabel(point)" :title="pointLabel(point)" @click="point.requestId && emit('locate', point.requestId)">
          <span v-if="point.peak" class="trend-stack" :style="{ height: `${Math.max(3, point.peak.estimatedTokens / peak * 100)}%` }">
            <i v-for="part in point.peak.parts" :key="part.kind" :style="{ flex: Math.max(part.estimatedTokens, 1), backgroundColor: contextKind(part.kind).color }"></i>
          </span>
          <span v-else class="trend-unknown"></span>
        </button>
      </div>
      <p v-else>历史日志尚未记录可比较的上下文摘要；新调用将在结束后出现在这里。</p>
      <div class="trend-scale"><span>会话开始</span><span>点击柱形定位调用 · 最高 {{ estimatedLabel(peak) }}</span><span>最近调用</span></div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { api } from '@/services/api'
import type { ContextOverview, ContextTrendPoint } from '@/types/logs'
import { contextKind, estimatedLabel } from '@/utils/contextPresentation'

const props = defineProps<{ groupKey: string; version: string }>()
const emit = defineEmits<{ locate: [requestId: string] }>()
const overview = shallowRef<ContextOverview | null>(null)
const loading = ref(false)
const error = ref('')
let revision = 0
let loadedKey = ''
const peak = computed(() => Math.max(1, ...(overview.value?.points.map(point => point.peak?.estimatedTokens || 0) || [])))
const pointLabel = (point: ContextTrendPoint) => `${new Date(point.firstAt).toLocaleString()}～${new Date(point.lastAt).toLocaleString()} · ${point.calls} 次调用 · ${point.knownCalls} 次有快照 · ${point.peak ? estimatedLabel(point.peak.estimatedTokens) : '上下文未知'}`
async function load() {
  const version = ++revision
  if (loadedKey !== props.groupKey) { overview.value = null; loadedKey = props.groupKey }
  loading.value = true
  error.value = ''
  try {
    const result = await api.getLogContextOverview(props.groupKey)
    if (version === revision) overview.value = result
  } catch (failure) { if (version === revision) error.value = failure instanceof Error ? failure.message : '读取上下文趋势失败' }
  finally { if (version === revision) loading.value = false }
}
watch(() => [props.groupKey, props.version], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { revision++ })
</script>

<style scoped>
.context-trend { margin-bottom: 20px; border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12); padding-bottom: 16px; }
.trend-heading { display: flex; align-items: baseline; flex-wrap: wrap; gap: 12px; }
.trend-heading span, p, .trend-scale { font-size: 12px; color: rgba(var(--v-theme-on-surface), 0.65); }
p { margin: 8px 0 12px; }
.trend-chart { display: flex; gap: 2px; height: 92px; align-items: stretch; }
.trend-column { display: flex; flex: 1 1 0; min-width: 1px; align-items: flex-end; background: transparent; border: 0; cursor: pointer; }
.trend-column:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.trend-column:disabled { cursor: default; }
.trend-column:hover .trend-stack { filter: brightness(1.25); }
.trend-stack { display: flex; flex-direction: column-reverse; width: 100%; overflow: hidden; border-radius: 2px 2px 0 0; }
.trend-stack i { min-height: 1px; }
.trend-unknown { width: 100%; height: 3px; background: rgba(var(--v-theme-on-surface), 0.2); }
.trend-scale { display: flex; justify-content: space-between; gap: 12px; margin-top: 8px; flex-wrap: wrap; }
</style>
