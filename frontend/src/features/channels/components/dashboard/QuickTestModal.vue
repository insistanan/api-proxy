<template>
  <AppDialog :model-value="isOpen" title="快捷测试" size="lg" @update:model-value="value => { if (!value) close() }">
    <template #header-actions>
      <span v-if="channel" class="max-w-36 truncate rounded-badge bg-muted px-2.5 py-1 text-xs text-muted-foreground sm:max-w-72" :title="channel.name">
        {{ channel.name }}
      </span>
    </template>

    <div class="space-y-5">
      <div v-if="supportsFingerprint" class="grid grid-cols-2 gap-1 rounded-control bg-muted/70 p-1" role="group" aria-label="测试方式">
        <button
          v-for="mode in testModes"
          :key="mode.value"
          type="button"
          class="flex min-h-10 items-center justify-center gap-2 rounded-control px-3 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50"
          :class="testMode === mode.value ? 'bg-card text-primary shadow-sm' : 'text-muted-foreground hover:text-foreground'"
          :aria-pressed="testMode === mode.value"
          :disabled="isSending || fingerprintBusy"
          @click="testMode = mode.value"
        >
          <UiIcon :icon="mode.icon" :size="17" />
          {{ mode.label }}
        </button>
      </div>

      <div class="grid gap-4" :class="supportsFingerprint ? 'sm:grid-cols-[minmax(0,1fr)_12rem]' : ''">
        <UiSelect
          v-model="selectedModel"
          searchable
          creatable
          :items="modelOptions"
          item-title="title"
          item-value="value"
          :return-object="false"
          label="模型"
          placeholder="选择或输入模型名称"
          prepend-inner-icon="mdi-robot-outline"
          :disabled="isSending || fingerprintBusy"
          :loading="isLoadingModels"
          clearable
        >
          <template #no-data>
            <div class="p-4 text-center text-sm text-muted-foreground">
              <template v-if="isLoadingModels">正在获取模型列表…</template>
              <template v-else>可直接输入模型名称</template>
            </div>
          </template>
        </UiSelect>
        <UiSelect
          v-if="supportsFingerprint"
          v-model="selectedThinking"
          :items="thinkingOptions"
          label="思考档位"
          prepend-inner-icon="mdi-brain"
          :disabled="isSending || fingerprintBusy"
        />
      </div>

      <div v-if="modelLoadError" class="flex items-start gap-2 rounded-control border border-warning/20 bg-warning/5 px-3 py-2 text-xs text-warning" role="alert">
        <UiIcon icon="mdi-alert-circle-outline" :size="16" class="mt-0.5 shrink-0" />
        <span class="min-w-0 flex-1 break-words leading-6">{{ modelLoadError }}</span>
        <UiButton size="x-small" variant="text" color="warning" :loading="isLoadingModels" :disabled="isSending || fingerprintBusy" @click="loadModels">重新获取</UiButton>
      </div>

      <ModelFingerprintPanel
        v-if="isOpen && testMode === 'fingerprint' && channel"
        :key="channel.id"
        :channel="channel"
        :model="selectedModel"
        :effort="selectedThinking"
        @busy="fingerprintBusy = $event"
        @target="restoreFingerprintTarget"
        @background="close"
      />

      <template v-else>
        <UiTextarea v-model="testMessage" label="测试消息" rows="3" :disabled="isSending" placeholder="输入测试消息…" />
        <div class="flex justify-end">
          <UiButton color="primary" prepend-icon="mdi-send" :disabled="!canSend" :loading="isSending" @click="sendTest">发送测试</UiButton>
        </div>
        <section v-if="responseText || responseError || isSending" class="overflow-hidden rounded-card border border-border" aria-label="响应结果">
          <header class="flex items-center justify-between gap-3 border-b border-border bg-muted/40 px-4 py-3">
            <span class="flex items-center gap-2 text-sm font-medium"><UiIcon icon="mdi-message-reply-text" :size="17" />响应结果</span>
            <span class="text-xs" :class="responseError ? 'text-destructive' : isSending ? 'text-primary' : 'text-success'" role="status">{{ responseError ? '请求失败' : isSending ? '正在接收…' : '已完成' }}</span>
          </header>
          <div class="max-h-80 overflow-y-auto p-4">
            <div v-if="responseError" class="flex items-start gap-2 rounded-control bg-destructive/5 p-3 text-sm text-destructive" role="alert">
              <UiIcon icon="mdi-alert-circle-outline" :size="18" class="mt-0.5 shrink-0" />
              <span class="min-w-0 whitespace-pre-wrap break-words">{{ responseError }}</span>
            </div>
            <div v-else-if="isSending && !responseText" class="flex items-center justify-center gap-3 py-6 text-sm text-muted-foreground">
              <UiSpinner indeterminate size="20" />等待模型回答…
            </div>
            <div v-else class="whitespace-pre-wrap break-words text-sm leading-7">{{ responseText }}</div>
          </div>
        </section>
      </template>
    </div>
  </AppDialog>
</template>
<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiSpinner from '@/shared/ui/feedback/Spinner.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { ref, computed, watch } from 'vue'
import type { Channel } from '@/api'
import { api, testChannelWithModel } from '@/api'
import type { ApiTab } from '@/features/channels/services/channel-api'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import ModelFingerprintPanel from '@/features/channels/components/dashboard/ModelFingerprintPanel.vue'
import type { FingerprintTask } from '@/types/model-fingerprint'

const props = defineProps<{
  modelValue: boolean
  channel: Channel | null
  apiType: ApiTab
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

const isOpen = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value)
})

const modelOptions = ref<Array<{ title: string; value: string }>>([])
const testModes = [
  { value: 'message', label: '消息测试', icon: 'mdi-chat-outline' },
  { value: 'fingerprint', label: '模型指纹', icon: 'mdi-chart-box-outline' }
] as const
const testMode = ref<'message' | 'fingerprint'>('message')
const fingerprintBusy = ref(false)
const supportsFingerprint = computed(() => !props.channel?.builtinProvider && ['messages', 'responses', 'chat', 'gemini'].includes(props.apiType))
const selectedModel = ref<string | null>(null)
const isLoadingModels = ref(false)
const modelLoadError = ref('')
const selectedThinking = ref('')
const testMessage = ref('请用一句完整的话说明你当前能完成什么任务。')
const isSending = ref(false)
const responseText = ref('')
const responseError = ref<string | null>(null)
let modelLoadRequestId = 0

const thinkingOptions = computed(() => [
  { title: '使用模型默认值', value: '' },
  { title: '关闭思考（none）', value: 'none' },
  { title: 'low', value: 'low' },
  { title: 'high', value: 'high' },
  { title: 'xhigh', value: 'xhigh' },
  { title: 'max', value: 'max' }
])

const canSend = computed(() => {
  return !!(props.channel?.id && selectedModel.value?.trim() && testMessage.value.trim() && !isSending.value)
})

const close = () => {
  modelLoadRequestId++
  isOpen.value = false
}

function restoreFingerprintTarget(target: FingerprintTask['target']) {
  selectedModel.value = target.model
  selectedThinking.value = target.effort
}

const loadModels = async () => {
  const channel = props.channel
  const requestId = ++modelLoadRequestId
  if (!channel) return

  const baseUrl = channel.baseUrl?.trim() || channel.baseUrls?.find(url => url.trim())?.trim() || ''
  if (!baseUrl) {
    modelLoadError.value = '渠道未配置有效的上游地址，无法获取模型列表。'
    return
  }

  isLoadingModels.value = true
  modelLoadError.value = ''

  try {
    const response = await api.discoverUpstreamModels({
      baseUrl,
      baseUrls: channel.baseUrls,
      apiKey: channel.apiKeys?.[0] || '',
      serviceType: channel.serviceType,
      insecureSkipVerify: channel.insecureSkipVerify,
      proxyMode: channel.proxyMode,
      proxyUrl: channel.proxyUrl
    })
    if (requestId !== modelLoadRequestId) return

    const defaultModel = channel.defaultModel?.trim() || ''
    const discoveredModels = response.data
      .map(item => item.id?.trim())
      .filter((model): model is string => !!model)
    const models = Array.from(new Set([defaultModel, ...discoveredModels].filter(Boolean)))

    modelOptions.value = models.map(model => ({ title: model, value: model }))
    if (!selectedModel.value && models.length > 0) {
      selectedModel.value = models[0]
    }
    if (discoveredModels.length === 0) {
      modelLoadError.value = '上游未返回可用模型，请直接输入模型名称。'
    }
  } catch (error) {
    if (requestId !== modelLoadRequestId) return
    const message = error instanceof Error ? error.message : '未知错误'
    modelLoadError.value = `获取模型列表失败：${message}。仍可直接输入模型名称。`
  } finally {
    if (requestId === modelLoadRequestId) {
      isLoadingModels.value = false
    }
  }
}

const sendTest = async () => {
  if (!canSend.value || !props.channel) return

  isSending.value = true
  responseText.value = ''
  responseError.value = null

  try {
    await testChannelWithModel(
      props.apiType,
      props.channel.id!,
      selectedModel.value || undefined,
      testMessage.value.trim(),
      (chunk: string) => {
        responseText.value += chunk
      },
      {
        purpose: 'quick_test',
        channelIndex: props.channel.index,
        thinking: selectedThinking.value
      }
    )
  } catch (error: any) {
    console.error('测试失败:', error)
    responseError.value = error.message || '测试失败'
  } finally {
    isSending.value = false
  }
}

// 监听弹窗打开或渠道切换，自动加载对应渠道的模型。
watch([() => props.modelValue, () => props.channel?.id], ([newVal, channelId], [, previousChannelId]) => {
  if (newVal && props.channel) {
    if (channelId !== previousChannelId || !supportsFingerprint.value) testMode.value = 'message'
    fingerprintBusy.value = false
    // 重置状态
    modelLoadRequestId++
    responseText.value = ''
    responseError.value = null
    testMessage.value = '请用一句完整的话说明你当前能完成什么任务。'
    selectedModel.value = props.channel.defaultModel || null
    modelOptions.value = props.channel.defaultModel
      ? [{ title: props.channel.defaultModel, value: props.channel.defaultModel }]
      : []
    selectedThinking.value = ''
    modelLoadError.value = ''
    void loadModels()
  } else {
    modelLoadRequestId++
    isLoadingModels.value = false
  }
})
</script>
