<template>
  <div class="local-embedding">
    <p class="text-body-2">安装位置：<code class="install-path">{{ status?.installPath || catalog?.installPath || '正在读取安装位置' }}</code></p>
    <p class="text-body-2" role="status" aria-live="polite">{{ status?.platform }} · {{ phaseText }}</p>
    <v-alert v-if="actionError || statusError || status?.error" type="error" variant="tonal">{{ actionError || statusError || status?.error }}</v-alert>
    <template v-if="!manualOnly">
      <v-alert v-if="catalog?.error || catalogError" type="warning" variant="tonal">{{ catalog?.error || catalogError }}<span v-if="catalog?.stale">；下方保留上次成功清单，刷新成功后才能安装。</span></v-alert>
      <v-alert v-else-if="catalog && !catalog.presets.length" type="info" variant="tonal">发布清单暂未提供预设，可使用手动安装</v-alert>
      <v-select v-if="catalog?.presets.length" v-model="presetId" :items="catalog.presets" item-title="name" item-value="id" label="推荐向量模型" :disabled="busy" />
      <p v-if="selectedPreset" class="text-body-2 text-medium-emphasis">{{ selectedPreset.description }} · 约 {{ (selectedPreset.sizeBytes / 1024 / 1024).toFixed(0) }} MiB</p>
      <v-btn v-if="catalog?.presets.length" color="primary" variant="flat" :disabled="busy || status?.running || !presetId || !!catalog.error || !!catalogError" @click="run('preset')">下载并启动</v-btn>
    </template>
    <v-progress-linear v-if="busy" indeterminate color="primary" :aria-label="phaseText" />
    <v-expansion-panels v-model="manualPanel" variant="accordion">
      <v-expansion-panel v-if="!manualOnly" value="catalog">
        <v-expansion-panel-title>下载源设置（高级）</v-expansion-panel-title>
        <v-expansion-panel-text>
          <v-text-field v-model="presetsUrl" label="发布清单地址（HTTPS）" density="compact" :disabled="busy || savingUrl" :error-messages="urlError" />
          <div class="d-flex flex-wrap ga-2">
            <v-btn variant="tonal" size="small" :loading="savingUrl" :disabled="busy || savingUrl" @click="savePresetsUrl">保存地址</v-btn>
            <v-btn variant="text" size="small" :disabled="busy || savingUrl || presetsUrl === defaultPresetsUrl" @click="resetPresetsUrl">恢复默认</v-btn>
            <v-btn variant="text" size="small" :loading="catalogLoading" :disabled="busy || savingUrl" @click="loadPresets">刷新发布清单</v-btn>
          </div>
          <p v-if="defaultPresetsUrl" class="text-caption text-medium-emphasis install-path">默认清单：{{ defaultPresetsUrl }}</p>
        </v-expansion-panel-text>
      </v-expansion-panel>
      <v-expansion-panel value="manual">
        <v-expansion-panel-title v-if="!manualOnly">手动安装（高级）</v-expansion-panel-title>
        <v-expansion-panel-text>
    <div class="local-install-fields">
    <h4 class="text-subtitle-2">运行时资源</h4>
    <v-text-field v-model="manifest.runtimeUrl" label="CPU 运行时 ZIP 下载地址（HTTPS）" :disabled="busy" />
    <v-text-field v-model="manifest.runtimeSha256" label="运行时 SHA-256" :disabled="busy" />
    <v-text-field v-model="manifest.executable" label="ZIP 内 llama-server 相对路径" placeholder="llama-server.exe" :disabled="busy" />
    <h4 class="text-subtitle-2">向量模型资源</h4>
    <v-text-field v-model="manifest.modelUrl" label="小型 GGUF 向量模型下载地址（HTTPS）" :disabled="busy" />
    <v-text-field v-model="manifest.modelSha256" label="模型 SHA-256" :disabled="busy" />
    <v-text-field v-model="manifest.modelName" label="模型名称（与语义配置一致）" :disabled="busy" />
    <v-select v-model="manifest.pooling" :items="['cls', 'mean', 'last']" label="模型池化方式" :disabled="busy" />
    <p class="text-caption text-medium-emphasis">请使用文件直链和完整校验值，而不是网页地址。模型名称是本地调用标识，需与数据集构建配置一致。</p>
    <div><v-btn variant="tonal" :loading="action === 'install'" :disabled="busy || status?.running || !manifestComplete" @click="run('install')">下载并校验</v-btn></div>
    </div>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>
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
import { intentApi, intentErrorText, type LocalManifest, type LocalPresets, type LocalStatus } from '@/features/channels/services/intent'

const props = defineProps<{ manualOnly?: boolean }>()
const emit = defineEmits<{ status: [value: LocalStatus]; busy: [value: boolean] }>()
const status = ref<LocalStatus>()
const manualPanel = ref<string | undefined>(props.manualOnly ? 'manual' : undefined)
const catalog = ref<LocalPresets>()
const catalogError = ref('')
const catalogLoading = ref(false)
const presetId = ref('')
const presetsUrl = ref('')
const defaultPresetsUrl = ref('')
const urlError = ref('')
const savingUrl = ref(false)
const statusError = ref('')
const actionError = ref('')
const action = ref('')
const busy = computed(() => !!action.value || !!status.value?.busy)
const selectedPreset = computed(() => catalog.value?.presets.find(preset => preset.id === presetId.value))
const phases: Record<string, string> = { idle: '尚未安装', downloading_runtime: '正在下载运行时', verifying_runtime: '正在校验运行时 SHA-256', extracting_runtime: '正在解压运行时', downloading_model: '正在下载向量模型', verifying_model: '正在校验模型 SHA-256', installed: '已安装，未运行', starting: '正在启动本地助手', warming: '正在预热向量模型', ready: '已就绪，可调用', stopped: '已停止', failed: '操作失败，请查看错误' }
const phaseText = computed(() => status.value?.ready ? '已就绪，可调用' : status.value?.busy ? phases[status.value.phase] || status.value.phase : status.value?.running ? '正在启动或预热' : status.value?.installed ? '已安装，未运行' : status.value ? phases[status.value.phase] || status.value.phase : '正在读取状态')
const manifest = ref<LocalManifest>({ runtimeUrl: '', runtimeSha256: '', executable: '', modelUrl: '', modelSha256: '', modelName: '', pooling: 'cls' })
const manifestComplete = computed(() => Object.values(manifest.value).every(value => value.trim() !== ''))
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
    presetsUrl.value = catalog.value.presetsUrl
    defaultPresetsUrl.value = catalog.value.defaultPresetsUrl
    if (!catalog.value.presets.some(preset => preset.id === presetId.value)) presetId.value = catalog.value.presets[0]?.id ?? ''
  }
  catch (cause) { catalogError.value = intentErrorText(cause) }
  finally { catalogLoading.value = false }
}
async function savePresetsUrl() {
  if (savingUrl.value) return
  savingUrl.value = true
  urlError.value = ''
  try {
    catalog.value = await intentApi.setPresetsUrl(presetsUrl.value)
    presetsUrl.value = catalog.value.presetsUrl
    defaultPresetsUrl.value = catalog.value.defaultPresetsUrl
    catalogError.value = ''
  } catch (cause) { urlError.value = intentErrorText(cause) }
  finally { savingUrl.value = false }
}
async function resetPresetsUrl() {
  presetsUrl.value = defaultPresetsUrl.value
  await savePresetsUrl()
}
async function run(next: 'install' | 'preset' | 'start' | 'stop') {
  if (busy.value) return
  if (next === 'preset' && (!selectedPreset.value || catalog.value?.error || catalogError.value)) return
  action.value = next
  actionError.value = ''
  emit('busy', true)
  try { updateStatus(next === 'preset' ? await intentApi.installPreset(presetId.value) : next === 'install' ? await intentApi.install(manifest.value) : await intentApi.localAction(next)) }
  catch (cause) { actionError.value = intentErrorText(cause) }
  finally { action.value = ''; if (mounted) await refresh(); emit('busy', busy.value) }
}
onMounted(async () => {
  mounted = true
  await refresh()
  if (status.value?.installed) manifest.value = { ...status.value.manifest }
  if (!mounted) return
  if (!props.manualOnly) void loadPresets()
  timer = setInterval(() => { if (busy.value) void refresh() }, 1000)
})
onUnmounted(() => { mounted = false; clearInterval(timer) })
</script>

<style scoped>
.local-embedding { display: grid; gap: 16px; padding-top: 8px; }
.local-install-fields { display: grid; gap: 18px; padding-top: 8px; }
.install-path { overflow-wrap: anywhere; }
</style>
