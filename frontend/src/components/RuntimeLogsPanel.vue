<template>
  <section class="runtime-logs" aria-label="进程运行日志">
    <div class="runtime-toolbar">
      <v-text-field v-model="search" label="搜索运行日志标签或正文" maxlength="200" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable />
      <v-btn variant="tonal" :loading="loading" @click="load(false)">刷新到最新</v-btn>
    </div>
    <p class="text-caption text-medium-emphasis">进程运行日志按时间倒序展示；当前最多保留 200 行，翻页时自动回收较新的行。</p>
    <v-alert v-if="error" type="error" variant="tonal" density="compact">{{ error }}</v-alert>
    <div class="runtime-lines" :aria-busy="loading">
      <div v-if="!entries.length" class="pa-4 text-medium-emphasis">{{ loading ? '正在读取日志…' : '暂无匹配的运行日志' }}</div>
      <div v-for="entry in entries" :key="entry.id" class="runtime-line">
        <span class="runtime-time">{{ new Date(entry.timestamp).toLocaleString() }}</span>
        <span class="runtime-tag">{{ entry.tag || '未分类' }}</span>
        <pre>{{ entry.message }}</pre>
      </div>
    </div>
    <v-btn v-if="nextCursor" variant="text" :loading="loading" @click="load(true)">读取更早日志</v-btn>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { api } from '@/services/api'
import type { RuntimeLogEntry } from '@/types/logs'

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
.runtime-logs { display: flex; flex: 1; flex-direction: column; gap: 12px; min-height: 0; overflow: hidden; }
.runtime-toolbar { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.runtime-lines { flex: 1; min-height: 0; overflow: auto; border-top: 1px solid rgba(var(--v-theme-on-surface), 0.12); }
.runtime-line { display: grid; grid-template-columns: 165px 170px minmax(0, 1fr); gap: 12px; padding: 10px 4px; border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.08); font-size: 12px; }
.runtime-time { color: rgba(var(--v-theme-on-surface), 0.6); }
.runtime-tag { overflow-wrap: anywhere; color: rgb(var(--v-theme-primary)); }
pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; font-family: monospace; }
@media (max-width: 700px) { .runtime-line { grid-template-columns: 1fr; gap: 4px; } }
</style>
