<template>
  <div class="space-y-5">
    <div class="grid gap-4 sm:grid-cols-2">
      <UiSelect v-model="urlIndex" :items="urlOptions" label="采样地址" prepend-inner-icon="mdi-link-variant" :disabled="busy" />
      <UiSelect v-model="keyIndex" :items="keyOptions" label="采样密钥" prepend-inner-icon="mdi-key" :disabled="busy" />
    </div>

    <section class="rounded-card border border-border bg-muted/30 p-4" aria-label="检测状态">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 items-center gap-3" role="status" aria-live="polite">
          <span class="flex size-10 shrink-0 items-center justify-center rounded-control" :class="statusAppearance.surface">
            <UiIcon :icon="statusAppearance.icon" :size="21" :class="running || (!task && pending) ? 'animate-spin' : ''" />
          </span>
          <div class="min-w-0">
            <p class="text-sm font-semibold">{{ status }}</p>
            <template v-if="task">
              <p class="mt-1 text-xs text-muted-foreground">{{ analysis ? `${analysis.used_outputs}/3 条有效样本` : `已完成 ${completeCount}/3 条采样` }} · {{ formatDateTime(task.createdAt) }}</p>
              <p v-if="targetChanged" class="mt-1 text-xs text-muted-foreground [overflow-wrap:anywhere]">
                上次采样：{{ task.target.model }} · {{ task.target.effort || '默认思考档位' }} · 地址 {{ task.target.urlIndex + 1 }} / 密钥 {{ task.target.keyIndex + 1 }}
              </p>
            </template>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <UiButton v-if="error" size="small" variant="text" prepend-icon="mdi-refresh" :disabled="pending" @click="refresh()">刷新</UiButton>
          <template v-if="running">
            <UiButton size="small" variant="text" color="warning" :disabled="pending" @click="cancel">停止</UiButton>
            <UiButton size="small" variant="tonal" color="primary" prepend-icon="mdi-arrow-right" @click="emit('background')">后台运行</UiButton>
          </template>
          <template v-else>
            <UiButton v-if="retryableCount && !targetChanged" size="small" variant="tonal" :disabled="busy" @click="retry()">补测失败项（{{ retryableCount }}）</UiButton>
            <UiButton size="small" color="primary" :prepend-icon="task ? 'mdi-replay' : 'mdi-play-circle'" :disabled="!canStart" :loading="pending" @click="start">
              {{ task ? '重新检测' : '开始检测' }}
            </UiButton>
          </template>
        </div>
      </div>
      <div v-if="task" class="mt-4 grid grid-cols-3 gap-2" aria-label="三条样本进度">
        <div v-for="slot in sampleSlots" :key="slot.index" class="space-y-1.5">
          <div class="h-1 rounded-full" :class="sampleAppearance(slot.sample?.state).bar"></div>
          <span class="text-[11px] text-muted-foreground">{{ slot.index + 1 }} · {{ slot.sample ? sampleState[slot.sample.state] : running ? '等待采样' : '未采样' }}</span>
        </div>
      </div>
    </section>

    <div v-if="error" class="flex items-start gap-2.5 rounded-control border border-destructive/20 bg-destructive/5 p-3 text-sm text-destructive" role="alert">
      <UiIcon icon="mdi-alert-circle-outline" :size="18" class="mt-0.5 shrink-0" />
      <details v-if="error.length > 160" class="min-w-0 flex-1">
        <summary class="cursor-pointer break-words leading-6">{{ conciseError(error) }}</summary>
        <p class="mt-2 whitespace-pre-wrap break-words text-xs leading-6">{{ error }}</p>
      </details>
      <p v-else class="min-w-0 break-words leading-6">{{ error }}</p>
    </div>

    <section v-if="analysis" class="space-y-3" aria-label="候选排名">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="text-sm font-semibold">{{ decisionLabel }}</h3>
        <span class="text-xs text-muted-foreground">仅供参考</span>
      </div>
      <div v-if="analysis.results.length" class="overflow-hidden rounded-card border border-border">
        <table class="w-full table-fixed text-left text-sm">
          <colgroup><col class="w-9" /><col /><col class="w-16 sm:w-24" /><col class="w-24 sm:w-32" /></colgroup>
          <thead class="border-b border-border bg-muted/40 text-xs text-muted-foreground">
            <tr><th scope="col" class="py-3 pl-3 font-medium">#</th><th scope="col" class="px-2 py-3 font-medium">候选模型</th><th scope="col" class="px-2 py-3 text-right font-medium">分数</th><th scope="col" class="px-3 py-3 text-right font-medium">参考库置信度</th></tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="(candidate, index) in analysis.results.slice(0, 5)" :key="candidate.model" :class="index === 0 ? 'bg-primary/5' : ''">
              <td class="py-3 pl-3 text-xs tabular-nums" :class="index === 0 ? 'font-semibold text-primary' : 'text-muted-foreground'">{{ index + 1 }}</td>
              <th scope="row" class="break-words px-2 py-3 text-left font-medium" :class="index === 0 ? 'text-primary' : ''">{{ candidate.display_name }}</th>
              <td class="px-2 py-3 text-right font-mono text-xs tabular-nums">{{ candidate.score.toFixed(3) }}</td>
              <td class="px-3 py-3 text-right text-xs tabular-nums" :class="candidate.probability == null ? 'text-muted-foreground' : ''">{{ candidate.probability == null ? (analysis.used_outputs < 3 ? '样本不足' : '未校准') : `${(candidate.probability * 100).toFixed(1)}%` }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="analysisReason" class="text-xs leading-5 text-muted-foreground">{{ analysisReason }}</p>
    </section>

    <section v-if="samples.length" class="space-y-3" aria-label="采样明细">
      <h3 class="text-sm font-semibold">采样明细</h3>
      <ol class="space-y-2">
        <li v-for="sample in samples" :key="sample.index" class="overflow-hidden rounded-card border border-border">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-3.5 py-3">
            <span class="flex size-7 shrink-0 items-center justify-center rounded-control text-xs font-semibold tabular-nums" :class="sampleAppearance(sample.state).surface">{{ sample.index + 1 }}</span>
            <span class="text-sm font-medium" :class="sampleAppearance(sample.state).text">{{ sampleState[sample.state] }}</span>
            <span class="text-xs tabular-nums text-muted-foreground">{{ sample.count }}/{{ sample.expectedCount }} 个整数<span v-if="sample.durationMs"> · {{ (sample.durationMs / 1000).toFixed(1) }}s</span></span>
            <UiButton v-if="!targetChanged && ['failed', 'truncated', 'cancelled'].includes(sample.state)" size="x-small" variant="tonal" prepend-icon="mdi-replay" class="ml-auto" :disabled="busy" @click="retry(sample.index)">重试这一条</UiButton>
          </div>
          <div v-if="sample.error" class="mx-3.5 mb-3 rounded-control bg-destructive/5 px-3 py-2 text-xs leading-5 text-destructive">
            <details v-if="sample.error.length > 160">
              <summary class="cursor-pointer break-words">{{ conciseError(sample.error) }}</summary>
              <p class="mt-2 whitespace-pre-wrap break-words">{{ sample.error }}</p>
            </details>
            <p v-else class="break-words">{{ sample.error }}</p>
          </div>
          <details class="group border-t border-border/70">
            <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-3.5 py-2 text-xs text-muted-foreground transition-colors hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-ring">
              查看采样详情<UiIcon icon="mdi-chevron-down" :size="15" class="shrink-0 transition-transform group-open:rotate-180" />
            </summary>
            <div class="space-y-3 border-t border-border/70 bg-muted/20 p-3.5 text-xs">
              <p v-if="sample.responseModel" class="break-words text-muted-foreground">上游自报模型：{{ sample.responseModel }}</p>
              <div><p class="mb-1.5 font-medium text-muted-foreground">挑战内容</p><p class="whitespace-pre-wrap break-words leading-6">{{ sample.prompt }}</p></div>
              <div><p class="mb-1.5 font-medium text-muted-foreground">原始回答</p><pre class="max-h-44 overflow-auto whitespace-pre-wrap break-words rounded-control border border-border bg-card p-3 font-mono leading-6">{{ sample.text || '尚未收到回答' }}</pre></div>
            </div>
          </details>
        </li>
      </ol>
    </section>

    <details v-if="task" class="group border-t border-border pt-3 text-xs text-muted-foreground">
      <summary class="flex cursor-pointer list-none items-center justify-between gap-2 py-1 focus-visible:outline-2 focus-visible:outline-ring">本轮配置与检测版本<UiIcon icon="mdi-chevron-down" :size="15" class="transition-transform group-open:rotate-180" /></summary>
      <dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 rounded-control bg-muted/40 p-3 leading-5">
        <dt>采样模型</dt><dd class="break-all font-mono">{{ task.target.model }}</dd>
        <dt>思考档位</dt><dd>{{ task.target.effort || '模型默认值' }}</dd>
        <dt>采样目标</dt><dd>地址 {{ task.target.urlIndex + 1 }} · 密钥 {{ task.target.keyIndex + 1 }}</dd>
        <template v-if="task.dataBuilt"><dt>参考库</dt><dd>{{ formatDateTime(task.dataBuilt) }}</dd></template>
        <template v-if="task.sdkVersion"><dt>SDK</dt><dd>{{ task.sdkVersion }}</dd></template>
        <template v-if="task.dataSha256"><dt>数据摘要</dt><dd class="break-all font-mono">{{ task.dataSha256 }}</dd></template>
      </dl>
    </details>
  </div>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Channel } from '@/types'
import type { FingerprintSample, FingerprintTask } from '@/types/model-fingerprint'
import { getModelFingerprint, startModelFingerprint, stopModelFingerprint, retryModelFingerprintSample, retryFailedModelFingerprint } from '@/features/channels/services/model-fingerprint'
import { formatDateTime } from '@/shared/utils/datetime'
import UiButton from '@/shared/ui/button/Button.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'

const props = defineProps<{ channel: Channel; model: string | null; effort: string }>()
const emit = defineEmits<{ busy: [value: boolean]; background: []; target: [target: FingerprintTask['target']] }>()
const task = ref<FingerprintTask | null>(null)
const running = computed(() => task.value?.status === 'loading' || task.value?.status === 'running')
const pending = ref(true)
const busy = computed(() => running.value || pending.value)
const requestError = ref('')
const error = computed(() => requestError.value || (['failed', 'interrupted'].includes(task.value?.status || '') ? task.value?.error : ''))
const samples = computed(() => task.value?.samples ?? [])
const analysis = computed(() => task.value?.analysis)
const completeCount = computed(() => samples.value.filter(sample => sample.state === 'complete').length)
const retryableCount = computed(() => samples.value.filter(sample => ['failed', 'truncated', 'cancelled'].includes(sample.state)).length)
const sampleSlots = computed(() => Array.from({ length: 3 }, (_, index) => ({ index, sample: samples.value.find(sample => sample.index === index) })))
const statusAppearance = computed(() => {
  if (running.value || (!task.value && pending.value)) return { icon: 'mdi-loading', surface: 'bg-primary/10 text-primary' }
  if (error.value) return { icon: 'mdi-alert-circle-outline', surface: 'bg-destructive/10 text-destructive' }
  if (task.value?.status === 'completed') return { icon: 'mdi-check-circle-outline', surface: 'bg-success/10 text-success' }
  return { icon: 'mdi-test-tube', surface: 'bg-muted text-muted-foreground' }
})

function sampleAppearance(state?: FingerprintSample['state']) {
  switch (state) {
    case 'complete': return { surface: 'bg-success/10 text-success', text: 'text-success', bar: 'bg-success' }
    case 'failed': return { surface: 'bg-destructive/10 text-destructive', text: 'text-destructive', bar: 'bg-destructive' }
    case 'truncated':
    case 'cancelled': return { surface: 'bg-warning/10 text-warning', text: 'text-warning', bar: 'bg-warning' }
    case 'waiting': return { surface: 'bg-primary/10 text-primary', text: 'text-primary', bar: 'bg-primary motion-safe:animate-pulse' }
    default: return { surface: 'bg-muted text-muted-foreground', text: 'text-muted-foreground', bar: 'bg-border' }
  }
}

function conciseError(message: string) {
  return `${message.replace(/\s+/g, ' ').slice(0, 140)}…`
}
const status = computed(() => {
  if (!task.value) return pending.value ? '正在读取检测记录…' : '等待开始'
  switch (task.value.status) {
    case 'loading': return '正在准备检测…'
    case 'running': return `采样进度 ${samples.value.filter(sample => sample.state !== 'waiting').length}/3`
    case 'completed': return '检测完成'
    case 'cancelled': return '检测已停止'
    case 'interrupted': return '检测已中断'
    case 'failed': return '检测失败'
    default: throw new Error(`未知的指纹任务状态：${task.value.status}`)
  }
})
const urlIndex = ref(0)
const keyIndex = ref(0)
let controller: AbortController | null = null
let pollTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

const urls = computed(() => props.channel.baseUrls?.length ? props.channel.baseUrls : props.channel.baseUrl ? [props.channel.baseUrl] : [])
const urlOptions = computed(() => urls.value.map((url, value) => {
  let title = `地址 ${value + 1}`
  try {
    const parsed = new URL(url.replace(/#$/, ''))
    title += ` · ${parsed.origin}${parsed.pathname}`
  } catch {
    title += ' · 地址格式无效'
  }
  return { title, value }
}))
const keyOptions = computed(() => props.channel.apiKeys.map((_, value) => ({ title: `密钥 ${value + 1}`, value })))
const canStart = computed(() => !!props.channel.id && !!props.model?.trim() && urls.value.length > 0 && keyOptions.value.length > 0)
const targetChanged = computed(() => {
  const target = task.value?.target
  return !!target && (
    target.model !== props.model?.trim() || target.effort !== props.effort ||
    target.urlIndex !== urlIndex.value || target.keyIndex !== keyIndex.value
  )
})
const sampleState: Record<FingerprintSample['state'], string> = {
  waiting: '正在采样', complete: '已完成', truncated: '被截断', failed: '失败', cancelled: '已停止'
}
const decisionLabel = computed(() => analysis.value ? {
  scored: '候选排名', partial: '部分样本排名', unscorable: '样本不足，无法评分', not_model: '数字序列不符合参考样本特征'
}[analysis.value.decision] : '')
const analysisReason = computed(() => {
  if (!analysis.value) return ''
  switch (analysis.value.evidence.method) {
    case 'partial-sample-ranking': return '需要 3 条有效样本才能计算置信度。'
    case 'complete-three-answers': return '有效样本不足，暂时无法评分。'
    case 'uncalibrated-ranking': return '参考库的置信度校准与评分器不匹配，当前仅提供排名。'
    case 'reference-calibrated-ranking': return ''
    case 'sequence-shape': return '数字序列与参考样本差异较大，无法给出排名。'
    default: return analysis.value.evidence.reason
  }
})

function stopViewing() {
  clearTimeout(pollTimer)
  controller?.abort()
}

function applyTask(value: FingerprintTask | null, restoreTarget = false) {
  task.value = value
  if (value && running.value && restoreTarget) {
    urlIndex.value = value.target.urlIndex
    keyIndex.value = value.target.keyIndex
    emit('target', value.target)
  }
  if (running.value && !disposed) pollTimer = setTimeout(() => { void refresh(false) }, 2000)
}

async function refresh(restoreTarget = true) {
  if (!props.channel.id) return
  stopViewing()
  const active = new AbortController()
  controller = active
  pending.value = true
  requestError.value = ''
  try {
    const value = await getModelFingerprint(props.channel.id, active.signal)
    if (!active.signal.aborted) applyTask(value, restoreTarget)
  } catch (cause) {
    if (!active.signal.aborted) requestError.value = cause instanceof Error ? cause.message : '读取检测任务失败'
  } finally {
    if (controller === active) pending.value = false
  }
}

async function start() {
  if (!canStart.value || busy.value || !props.channel.id) return
  stopViewing()
  const active = new AbortController()
  controller = active
  pending.value = true
  requestError.value = ''
  try {
    const value = await startModelFingerprint(props.channel.id, {
      model: props.model!.trim(), urlIndex: urlIndex.value, keyIndex: keyIndex.value, effort: props.effort
    }, active.signal)
    if (!active.signal.aborted) applyTask(value)
  } catch (cause) {
    if (!active.signal.aborted) {
      requestError.value = cause instanceof Error ? cause.message : '开始检测失败'
    }
  } finally {
    if (controller === active) pending.value = false
  }
}

async function cancel() {
  if (!props.channel.id || !task.value || pending.value) return
  stopViewing()
  const active = new AbortController()
  controller = active
  pending.value = true
  requestError.value = ''
  try {
    const value = await stopModelFingerprint(props.channel.id, task.value.id, active.signal)
    if (!active.signal.aborted) applyTask(value)
  } catch (cause) {
    if (!active.signal.aborted) requestError.value = cause instanceof Error ? cause.message : '停止检测失败'
  } finally {
    if (controller === active) pending.value = false
  }
}

async function retry(index?: number) {
  if (!props.channel.id || !task.value || busy.value || targetChanged.value) return
  stopViewing()
  const active = new AbortController()
  controller = active
  pending.value = true
  requestError.value = ''
  try {
    const value = index === undefined
      ? await retryFailedModelFingerprint(props.channel.id, task.value.id, active.signal)
      : await retryModelFingerprintSample(props.channel.id, task.value.id, index, active.signal)
    if (!active.signal.aborted) applyTask(value, true)
  } catch (cause) {
    if (!active.signal.aborted) requestError.value = cause instanceof Error ? cause.message : '重试样本失败'
  } finally {
    if (controller === active) pending.value = false
  }
}

watch(busy, value => { if (!disposed) emit('busy', value) }, { immediate: true })
onMounted(() => { void refresh() })
onBeforeUnmount(() => {
  disposed = true
  stopViewing()
  emit('busy', false)
})
</script>
