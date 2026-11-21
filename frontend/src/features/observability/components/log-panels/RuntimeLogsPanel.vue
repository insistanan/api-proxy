<template>
  <section class="runtime-logs" aria-label="进程运行日志">
    <div class="runtime-toolbar">
      <UiInput v-model="search" label="搜索运行日志标签或正文" maxlength="200" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable />
      <UiButton variant="tonal" :loading="loading" @click="load(false)">刷新到最新</UiButton>
    </div>
    <UiAlert v-if="error" type="error" variant="tonal" density="compact">{{ error }}</UiAlert>
    <div class="runtime-lines" :aria-busy="loading">
      <EmptyState v-if="!entries.length" variant="inline" :icon="loading ? 'mdi-progress-clock' : 'mdi-magnify-close'" :title="loading ? '正在读取日志…' : '暂无匹配的运行日志'" :description="loading ? undefined : '清空搜索词或换一个标签再试。'" />
      <div v-for="entry in entries" :key="entry.id" class="runtime-line">
        <span class="runtime-time">{{ formatDateTime(entry.timestamp) }}</span>
        <span class="runtime-tag">{{ entry.tag || '未分类' }}</span>
        <pre>{{ entry.message }}</pre>
      </div>
    </div>
    <UiButton v-if="nextCursor" variant="text" :loading="loading" @click="load(true)">读取更早日志</UiButton>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { api } from '@/api'
import type { RuntimeLogEntry } from '@/types/logs'
import { formatDateTime } from '@/shared/utils/datetime'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'

const search = ref('')
const entries = shallowRef<RuntimeLogEntry[]>([])
const nextCursor = ref('')
const loading = ref(false)
const error = ref('')
let revision = 0
async function load(more = false) {
  const current = ++revision
  loading.value = true
  error.value = ''
  try {
    const page = await api.getRuntimeLogs({ q: (search.value || '').trim(), before: more ? nextCursor.value : undefined })
    if (current !== revision) return
    const merged = new Map((more ? entries.value : []).map(entry => [entry.id, entry]))
    page.logs.forEach(entry => merged.set(entry.id, entry))
    const ordered = Array.from(merged.values()).sort((a, b) => b.unixMilli - a.unixMilli || b.id - a.id)
    entries.value = more ? ordered.slice(-200) : ordered.slice(0, 200)
    nextCursor.value = page.nextCursor || ''
  } catch (failure) { if (current === revision) error.value = failure instanceof Error ? failure.message : '读取运行日志失败' }
  finally { if (current === revision) loading.value = false }
}
watch(search, (_value, _previous, onCleanup) => {
  revision++
  entries.value = []
  nextCursor.value = ''
  error.value = ''
  loading.value = true
  const timer = setTimeout(() => { void load() }, 300)
  onCleanup(() => clearTimeout(timer))
})
onMounted(() => { void load() })
onBeforeUnmount(() => { revision++ })
</script>

<style scoped>
@reference "@/styles/index.css";

.runtime-logs { @apply flex [flex:1] flex-col gap-3 min-h-0 overflow-hidden; }
.runtime-toolbar { @apply flex items-center gap-3 flex-wrap; }
.runtime-lines { @apply [flex:1] min-h-0 overflow-auto [border-top:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)]; }
.runtime-line { @apply grid [grid-template-columns:165px_170px_minmax(0,_1fr)] gap-3 [padding:10px_4px] [border-bottom:1px_solid_color-mix(in_srgb,_var(--foreground)_8%,_transparent)] [font-size:12px]; }
.runtime-time { @apply [color:color-mix(in_srgb,_var(--foreground)_60%,_transparent)]; }
.runtime-tag { @apply [overflow-wrap:anywhere] text-primary; }
pre { @apply whitespace-pre-wrap [overflow-wrap:anywhere] [font:inherit] [font-family:monospace]; }
@media (max-width: 700px) { .runtime-line { @apply [grid-template-columns:1fr] gap-1; } }
</style>
