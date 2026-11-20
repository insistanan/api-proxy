<template>
  <AppDialog :model-value="isOpen" title="快捷测试" size="lg" @update:model-value="value => { if (!value) close() }">
    <template #header-actions>
      <UiBadge v-if="channel" size="small" color="primary" variant="tonal">
        {{ channel.name }}
      </UiBadge>
    </template>

    <!-- 模型选择 -->
    <UiSelect
v-model="selectedModel" searchable
      creatable
      :items="modelOptions"
      item-title="title"
      item-value="value"
      :return-object="false"
      label="模型"
      variant="outlined"
      prepend-inner-icon="mdi-robot"
      density="comfortable"
      class="mb-4"
      :disabled="isSending"
      :loading="isLoadingModels"
      clearable
    >
      <template #no-data>
        <div class="text-center p-4 text-muted-foreground">
          <template v-if="isLoadingModels">正在从上游获取模型列表...</template>
          <template v-else-if="modelLoadError">暂时无法显示模型列表，仍可直接输入模型名称</template>
          <template v-else>未找到匹配模型，可直接输入模型名称</template>
        </div>
      </template>
    </UiSelect>

    <div v-if="modelLoadError" class="model-load-error mb-4" role="alert">
      <span>{{ modelLoadError }}</span>
      <UiButton
        size="small"
        variant="text"
        color="warning"
        :loading="isLoadingModels"
        :disabled="isSending"
        @click="loadModels"
      >
        重新获取
      </UiButton>
    </div>

    <UiSelect
      v-model="selectedThinking"
      :items="thinkingOptions"
      label="思考档位"
      variant="outlined"
      density="comfortable"
      class="mb-4"
      :disabled="isSending"
    />

    <!-- 测试输入 -->
    <UiTextarea
      v-model="testMessage"
      label="测试消息"
      variant="outlined"
      rows="3"
      :disabled="isSending"
      :loading="isSending"
      placeholder="输入测试消息..."
      class="mb-4"
    />

    <!-- 发送按钮 -->
    <div class="flex justify-end mb-4">
      <UiButton
        color="success"
        prepend-icon="mdi-send"
        :disabled="!canSend"
        :loading="isSending"
        @click="sendTest"
      >
        发送测试
      </UiButton>
    </div>

    <!-- 响应结果 -->
    <UiCard v-if="responseText || responseError || isSending" variant="outlined" class="response-area">
      <div class="ui-card-title px-4 pt-4 text-base font-semibold text-sm font-medium p-3 bg-card">
        <UiIcon size="small" class="mr-2">mdi-message-reply-text</UiIcon>
        响应结果
      </div>
      <hr class="border-0 border-t border-border" />
      <div class="p-4 p-4" style="max-height: 300px; overflow-y: auto;">
        <div v-if="responseError" class="error-message" role="alert">
          <UiIcon color="error" class="mr-2">mdi-alert-circle</UiIcon>
          <span class="text-destructive">{{ responseError }}</span>
        </div>
        <div v-else-if="isSending && !responseText" class="text-center text-muted-foreground py-4">
          <UiSpinner indeterminate size="32" />
          <div class="mt-2">正在发送请求...</div>
        </div>
        <div v-else class="response-text">{{ responseText }}</div>
      </div>
    </UiCard>
  </AppDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiSpinner from '@/shared/ui/feedback/Spinner.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { ref, computed, watch } from 'vue'
import type { Channel } from '@/api'
import { api, testChannelWithModel } from '@/api'
import type { ApiTab } from '@/features/channels/services/channel-api'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'

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
  { title: '使用协议默认值', value: '' }
])

const canSend = computed(() => {
  return !!(props.channel?.id && selectedModel.value?.trim() && testMessage.value.trim() && !isSending.value)
})

const close = () => {
  modelLoadRequestId++
  isOpen.value = false
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
watch([() => props.modelValue, () => props.channel?.id], ([newVal]) => {
  if (newVal && props.channel) {
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

<style scoped>
@reference "@/styles/index.css";

.response-area {
  @apply [border:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)];
}

.response-text {
  @apply whitespace-pre-wrap [word-break:break-word] [font-family:'Consolas',_'Monaco',_monospace] [font-size:14px] [line-height:1.6];
}

.error-message {
  @apply flex items-center text-destructive p-3 [background:color-mix(in_srgb,_var(--destructive)_10%,_transparent)] rounded-control;
}

.model-load-error {
  @apply flex flex-wrap items-center justify-between gap-3 [margin-top:-8px] [padding:8px_12px] text-warning [background:color-mix(in_srgb,_var(--warning)_8%,_transparent)] [border:1px_solid_color-mix(in_srgb,_var(--warning)_24%,_transparent)] rounded-badge [font-size:0.75rem] [line-height:1.5] [word-break:break-word];
}

.model-load-error span {
  @apply [flex:1_1_320px] min-w-0;
}

.model-load-error .ui-button {
  @apply [flex:0_0_auto] ml-auto;
}
</style>
