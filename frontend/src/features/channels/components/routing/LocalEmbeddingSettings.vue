<template>
  <div class="local-embedding">
    <p class="text-body-2">安装位置：<code class="install-path">{{ status?.installPath || catalog?.installPath || '正在读取安装位置' }}</code></p>
    <p class="text-body-2" role="status" aria-live="polite">{{ status?.platform }} · {{ phaseText }}</p>
    <v-alert v-if="actionError || statusError || status?.error" type="error" variant="tonal">{{ actionError || statusError || status?.error }}</v-alert>
    <div class="local-presets">
      <v-alert v-if="catalog?.error || catalogError" type="warning" variant="tonal">{{ catalog?.error || catalogError }}<span v-if="catalog?.stale">；下方保留上次成功清单，刷新成功后才能安装。</span></v-alert>
      <v-alert v-else-if="catalog && !catalog.presets.length" type="info" variant="tonal">推荐清单没有适合当前平台的模型，可使用已配置的 Embeddings 渠道。</v-alert>
      <v-select v-if="catalog?.presets.length" v-model="presetId" :items="catalog.presets" item-title="name" item-value="id" label="推荐向量模型" :disabled="busy" />
      <p v-if="selectedPreset" class="text-body-2 text-medium-emphasis">{{ selectedPreset.description }} · 约 {{ (selectedPreset.sizeBytes / 1024 / 1024).toFixed(0) }} MiB</p>
      <div class="d-flex flex-wrap ga-2">
        <v-btn v-if="catalog?.presets.length" color="primary" variant="flat" :disabled="busy || catalogLoading || status?.running || !presetId || !!catalog.error || !!catalogError" @click="run('preset')">下载并启动</v-btn>
        <v-btn variant="text" :loading="catalogLoading" :disabled="busy || catalogLoading" @click="loadPresets">刷新推荐清单</v-btn>
      </div>
    </div>
    <v-progress-linear v-if="busy" indeterminate color="primary" :aria-label="phaseText" />
    <p class="text-caption text-medium-emphasis">网关重启后需手动启动。助手使用 2 个 CPU 线程；状态就绪后可构建索引。</p>
    <div class="d-flex flex-wrap ga-2">
      <v-btn color="primary" :disabled="busy || !status?.installed || status.running" @click="run('start')">启动并预热</v-btn>
      <v-btn variant="outlined" :disabled="busy || !status?.running" @click="run('stop')">停止</v-btn>
      <v-btn variant="text" :disabled="!!action" @click="refresh">刷新状态</v-btn>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { intentApi, intentErrorText, type LocalPresets, type LocalStatus } from '@/features/channels/services/intent'

const emit = defineEmits<{ status: [value: LocalStatus]; busy: [value: boolean] }>()
const status = ref<LocalStatus>()
const catalog = ref<LocalPresets>()
const catalogError = ref('')
const catalogLoading = ref(false)
const presetId = ref('')
const statusError = ref('')
const actionError = ref('')
const action = ref('')
const busy = computed(() => !!action.value || !!status.value?.busy)
const selectedPreset = computed(() => catalog.value?.presets.find(preset => preset.id === presetId.value))
const phases: Record<string, string> = { idle: '尚未安装', downloading_runtime: '正在下载运行时', verifying_runtime: '正在校验运行时 SHA-256', extracting_runtime: '正在解压运行时', downloading_model: '正在下载向量模型', verifying_model: '正在校验模型 SHA-256', installed: '已安装，未运行', starting: '正在启动本地助手', warming: '正在预热向量模型', ready: '已就绪，可调用', stopped: '已停止', failed: '操作失败，请查看错误' }
const phaseText = computed(() => status.value?.ready ? '已就绪，可调用' : status.value?.busy ? phases[status.value.phase] || status.value.phase : status.value?.running ? '正在启动或预热' : status.value?.installed ? '已安装，未运行' : status.value ? phases[status.value.phase] || status.value.phase : '正在读取状态')
let timer: ReturnType<typeof setInterval> | undefined
let refreshing = false
let mounted = false
function updateStatus(value: LocalStatus) { status.value = value; emit('status', value); emit('busy', busy.value) }
async function refresh() {
  if (refreshing) return
  refreshing = true
  try { updateStatus(await intentApi.local()); statusError.value = '' }
  catch (cause) { statusError.value = intentErrorText(cause) }
  finally { refreshing = false }
}
async function loadPresets() {
  if (catalogLoading.value) return
  catalogLoading.value = true
  catalogError.value = ''
  try {
    catalog.value = await intentApi.presets()
    if (!catalog.value.presets.some(preset => preset.id === presetId.value)) presetId.value = catalog.value.presets[0]?.id ?? ''
  }
  catch (cause) { catalogError.value = intentErrorText(cause) }
  finally { catalogLoading.value = false }
}
async function run(next: 'preset' | 'start' | 'stop') {
  if (busy.value) return
  if (next === 'preset' && (catalogLoading.value || !selectedPreset.value || catalog.value?.error || catalogError.value)) return
  action.value = next
  actionError.value = ''
  emit('busy', true)
  try { updateStatus(next === 'preset' ? await intentApi.installPreset(presetId.value) : await intentApi.localAction(next)) }
  catch (cause) { actionError.value = intentErrorText(cause) }
  finally { action.value = ''; if (mounted) await refresh(); emit('busy', busy.value) }
}
onMounted(async () => {
  mounted = true
  await refresh()
  if (!mounted) return
  void loadPresets()
  timer = setInterval(() => { if (busy.value) void refresh() }, 1000)
})
onUnmounted(() => { mounted = false; clearInterval(timer) })
</script>

<style scoped>
.local-embedding { display: grid; gap: 16px; padding-top: 8px; }
.local-presets { display: grid; gap: 12px; }
.install-path { overflow-wrap: anywhere; }
</style>
