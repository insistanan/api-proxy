<template>
  <div class="local-embedding">
    <p class="text-sm">安装位置：<code class="install-path">{{ status?.installPath || catalog?.installPath || '正在读取安装位置' }}</code></p>
    <p class="text-sm" role="status" aria-live="polite">{{ status?.platform }} · {{ phaseText }}</p>
    <UiAlert v-if="actionError || statusError || status?.error" type="error" variant="tonal">{{ actionError || statusError || status?.error }}</UiAlert>
    <p v-if="status?.notice" class="text-xs text-muted-foreground">{{ status.notice }}</p>
    <div class="local-presets">
      <UiAlert v-if="catalog?.error || catalogError" type="warning" variant="tonal">{{ catalog?.error || catalogError }}<span v-if="catalog?.stale">；下方保留上次成功清单，刷新成功后才能安装。</span></UiAlert>
      <UiAlert v-else-if="catalog && !catalog.presets.length" type="info" variant="tonal">推荐清单没有适合当前平台的模型，可使用已配置的 Embeddings 渠道。</UiAlert>
      <UiSelect v-if="catalog?.presets.length" v-model="presetId" :items="catalog.presets" item-title="name" item-value="id" label="推荐向量模型" :disabled="busy" />
      <p v-if="selectedPreset" class="text-sm text-muted-foreground">{{ selectedPreset.description }} · 下载总量不超过约 {{ (selectedPreset.sizeBytes / 1024 / 1024).toFixed(0) }} MiB</p>
      <div class="flex flex-wrap gap-2">
        <UiButton v-if="catalog?.presets.length" color="primary" variant="flat" :disabled="busy || catalogLoading || status?.running || !presetId || !!catalog.error || !!catalogError" @click="run('preset')">下载并启动</UiButton>
        <UiButton variant="text" :loading="catalogLoading" :disabled="busy || catalogLoading" @click="loadPresets">刷新推荐清单</UiButton>
      </div>
    </div>
    <UiProgress v-if="busy" :indeterminate="!downloadTotal" :model-value="downloadPercent" color="primary" :aria-label="phaseText" />
    <p v-if="busy && downloadTotal" class="text-xs">{{ ((status?.downloadedBytes ?? 0) / 1024 / 1024).toFixed(1) }} / {{ (downloadTotal / 1024 / 1024).toFixed(1) }} MiB</p>
    <p class="text-xs text-muted-foreground">已启用配置依赖本地模型时，网关重启会自动启动并预热。下载和准备在后台执行，关闭页面后可继续查看。</p>
    <div class="flex flex-wrap gap-2">
      <UiButton color="primary" :disabled="busy || !status?.installed || status.running" @click="run('start')">启动并预热</UiButton>
      <UiButton variant="outlined" :disabled="busy || !status?.running" @click="run('stop')">停止</UiButton>
      <UiButton variant="text" :disabled="!!action" @click="refresh">刷新状态</UiButton>
      <UiButton v-if="status?.busy" variant="text" @click="cancel">取消后台准备</UiButton>
    </div>
    <details class="grid gap-3">
      <summary class="cursor-pointer text-sm">网络受限：离线导入与自定义下载源</summary>
      <div class="grid gap-3 pt-3">
        <p class="text-xs text-muted-foreground">默认资源随每次应用 Release 发布；下载失败会尝试固定版本的上游源。也可在能联网的设备下载后导入，系统会核对 SHA-256，随后继续一键准备。</p>
        <template v-if="selectedPreset">
          <div class="flex gap-3 text-sm">
            <a v-if="selectedPreset.runtimeUrl" :href="selectedPreset.runtimeUrl" target="_blank" rel="noopener noreferrer" class="text-primary">下载当前网关运行时</a>
            <a v-if="selectedPreset.modelUrl" :href="selectedPreset.modelUrl" target="_blank" rel="noopener noreferrer" class="text-primary">下载向量模型</a>
          </div>
          <label class="grid gap-1 text-sm">导入运行时（ZIP / tar.gz）<input type="file" accept=".zip,.gz,.tgz" :disabled="busy || status?.running" @change="importFile($event, 'runtime')" /></label>
          <label class="grid gap-1 text-sm">导入向量模型（GGUF）<input type="file" accept=".gguf" :disabled="busy || status?.running" @change="importFile($event, 'model')" /></label>
        </template>
        <UiInput v-model="presetsUrl" label="自定义 HTTPS 预设清单地址（留空使用内置清单）" :disabled="busy" />
        <p class="text-xs text-muted-foreground">自定义清单可配置镜像源和校验值，仅填写可信来源。应用版本变化不影响内置清单。</p>
        <UiButton variant="outlined" :disabled="busy" @click="savePresetsUrl">保存清单地址</UiButton>
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { intentApi, intentErrorText, type LocalPresets, type LocalStatus } from '@/features/channels/services/intent'

const emit = defineEmits<{ status: [value: LocalStatus]; busy: [value: boolean] }>()
const status = ref<LocalStatus>()
const catalog = ref<LocalPresets>()
const catalogError = ref('')
const catalogLoading = ref(false)
const presetId = ref('')
const presetsUrl = ref('')
const statusError = ref('')
const actionError = ref('')
const action = ref('')
const busy = computed(() => !!action.value || !!status.value?.busy)
const selectedPreset = computed(() => catalog.value?.presets.find(preset => preset.id === presetId.value))
const phases: Record<string, string> = { idle: '尚未安装', queued: '准备任务已排队', preparing_model: '正在准备模型', preparing_samples: '正在准备样本', indexing: '正在构建意图索引', downloading_runtime: '正在下载运行时', verifying_runtime: '正在校验运行时 SHA-256', extracting_runtime: '正在解压运行时', downloading_model: '正在下载向量模型', verifying_model: '正在校验模型 SHA-256', installed: '已安装，未运行', starting: '正在启动本地助手', warming: '正在预热向量模型', ready: '已就绪，可调用', stopped: '已停止', failed: '操作失败，请查看错误', cancelled: '准备已取消，可重试' }
const phaseText = computed(() => status.value?.busy ? phases[status.value.phase] || status.value.phase : status.value?.error ? '操作失败，请查看错误' : status.value?.ready ? '已就绪，可调用' : status.value?.running ? '正在启动或预热' : status.value?.installed ? '已安装，未运行' : status.value ? phases[status.value.phase] || status.value.phase : '正在读取状态')
const downloadTotal = computed(() => status.value?.phase.startsWith('downloading_') ? status.value.totalBytes ?? 0 : 0)
const downloadPercent = computed(() => downloadTotal.value ? 100 * (status.value?.downloadedBytes ?? 0) / downloadTotal.value : 0)
async function cancel() {
  try { await intentApi.cancelPreparation(); await refresh() }
  catch (cause) { actionError.value = intentErrorText(cause) }
}
let timer: ReturnType<typeof setInterval> | undefined
phases.importing = '正在导入并校验离线资源'
phases.imported = '资源已导入，可继续准备'
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
    presetsUrl.value = catalog.value.presetsUrl
    if (!catalog.value.presets.some(preset => preset.id === presetId.value)) presetId.value = catalog.value.presets[0]?.id ?? ''
  }
  catch (cause) { catalogError.value = intentErrorText(cause) }
  finally { catalogLoading.value = false }
}
async function savePresetsUrl() {
  if (busy.value) return
  action.value = 'source'
  actionError.value = ''
  emit('busy', true)
  try {
    catalog.value = await intentApi.setPresetsUrl(presetsUrl.value.trim())
    catalogError.value = ''
    presetId.value = catalog.value.presets[0]?.id ?? ''
  } catch (cause) { actionError.value = intentErrorText(cause) }
  finally { action.value = ''; await refresh(); emit('busy', busy.value) }
}
async function importFile(event: Event, resource: 'runtime' | 'model') {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file || !presetId.value || busy.value) return
  const limit = resource === 'runtime' ? 128 * 1024 * 1024 : 1024 * 1024 * 1024
  if (file.size > limit) { actionError.value = '文件超过允许大小，请确认选择了正确资源'; input.value = ''; return }
  action.value = 'import'
  actionError.value = ''
  emit('busy', true)
  try { updateStatus(await intentApi.importResource(presetId.value, resource, file)) }
  catch (cause) { actionError.value = intentErrorText(cause) }
  finally { input.value = ''; action.value = ''; await refresh(); emit('busy', busy.value) }
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
@reference "@/styles/index.css";

.local-embedding { @apply grid gap-4 pt-2; }
.local-presets { @apply grid gap-3; }
.install-path { @apply [overflow-wrap:anywhere]; }
</style>
