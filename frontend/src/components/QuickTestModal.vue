<template>
  <v-dialog v-model="isOpen" max-width="800" persistent>
    <v-card rounded="lg">
      <v-card-title class="d-flex align-center justify-space-between pa-4">
        <div class="d-flex align-center ga-2">
          <v-icon color="success">mdi-test-tube</v-icon>
          <span class="text-h6">快捷测试</span>
          <v-chip v-if="channel" size="small" color="primary" variant="tonal">
            {{ channel.name }}
          </v-chip>
        </div>
        <v-btn icon size="small" variant="text" aria-label="关闭快捷测试" @click="close">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-divider />

      <v-card-text class="pa-4">
        <!-- 模型选择 -->
        <v-combobox
          v-model="selectedModel"
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
            <div class="text-center pa-4 text-medium-emphasis">
              <template v-if="isLoadingModels">正在从上游获取模型列表...</template>
              <template v-else-if="modelLoadError">暂时无法显示模型列表，仍可直接输入模型名称</template>
              <template v-else>未找到匹配模型，可直接输入模型名称</template>
            </div>
          </template>
        </v-combobox>

        <div v-if="modelLoadError" class="model-load-error mb-4" role="alert">
          <span>{{ modelLoadError }}</span>
          <v-btn
            size="small"
            variant="text"
            color="warning"
            :loading="isLoadingModels"
            :disabled="isSending"
            @click="loadModels"
          >
            重新获取
          </v-btn>
        </div>

        <v-select
          v-model="selectedThinking"
          :items="thinkingOptions"
          label="思考档位"
          variant="outlined"
          density="comfortable"
          class="mb-4"
          :disabled="isSending"
        />

        <!-- 测试输入 -->
        <v-textarea
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
        <div class="d-flex justify-end mb-4">
          <v-btn
            color="success"
            prepend-icon="mdi-send"
            :disabled="!canSend"
            :loading="isSending"
            @click="sendTest"
          >
            发送测试
          </v-btn>
        </div>

        <!-- 响应结果 -->
        <v-card v-if="responseText || responseError || isSending" variant="outlined" class="response-area">
          <v-card-title class="text-subtitle-2 pa-3 bg-surface">
            <v-icon size="small" class="mr-2">mdi-message-reply-text</v-icon>
            响应结果
          </v-card-title>
          <v-divider />
          <v-card-text class="pa-4" style="max-height: 300px; overflow-y: auto;">
            <div v-if="responseError" class="error-message" role="alert">
              <v-icon color="error" class="mr-2">mdi-alert-circle</v-icon>
              <span class="text-error">{{ responseError }}</span>
            </div>
            <div v-else-if="isSending && !responseText" class="text-center text-medium-emphasis py-4">
              <v-progress-circular indeterminate size="32" />
              <div class="mt-2">正在发送请求...</div>
            </div>
            <div v-else class="response-text">{{ responseText }}</div>
          </v-card-text>
        </v-card>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { Channel } from '@/services/api'
import { api, testChannelWithModel } from '@/services/api'
import type { ApiTab } from '@/services/channel-api'

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
.response-area {
  border: 1px solid rgba(var(--v-border-color), 0.2);
}

.response-text {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 14px;
  line-height: 1.6;
}

.error-message {
  display: flex;
  align-items: center;
  color: rgb(var(--v-theme-error));
  padding: 12px;
  background: rgba(var(--v-theme-error), 0.1);
  border-radius: 8px;
}

.model-load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: -8px;
  padding: 8px 12px;
  color: rgb(var(--v-theme-warning));
  background: rgba(var(--v-theme-warning), 0.08);
  border: 1px solid rgba(var(--v-theme-warning), 0.24);
  border-radius: 6px;
  font-size: 0.75rem;
  line-height: 1.5;
  word-break: break-word;
}

.model-load-error span {
  flex: 1 1 320px;
  min-width: 0;
}

.model-load-error .v-btn {
  flex: 0 0 auto;
  margin-left: auto;
}
</style>
