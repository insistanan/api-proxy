<template>
  <div>
    <v-select
      :model-value="serviceType"
      label="上游 API 协议 *"
      :items="imagesProtocolOptions"
      :rules="[validateProtocol]"
      :error-messages="serviceType && !isImagesServiceType(serviceType) ? '该图片协议不受支持，请重新选择' : ''"
      prepend-inner-icon="mdi-cog"
      variant="outlined"
      density="comfortable"
      :hint="serviceType === 'responses' ? '内部调用 /v1/responses 生图，对外仍返回 Images API 图片结果。' : '调用上游 /v1/images/generations 或 /v1/images/edits。'"
      persistent-hint
      @update:model-value="$emit('update:serviceType', $event)"
    />
    <v-combobox
      v-if="serviceType === 'responses'"
      :model-value="responsesModel"
      label="Responses 主模型（上游对话模型）*"
      placeholder="点选输入框查询上游模型，例如：gpt-6.1-sol"
      :items="modelOptions"
      item-title="title"
      item-value="value"
      :loading="modelsLoading"
      :error-messages="modelsError"
      hint="这是 /v1/responses 的顶层模型；图片生成工具使用下方固定模型映射。"
      persistent-hint
      :rules="[validateResponsesModel]"
      variant="outlined"
      density="comfortable"
      class="mt-4"
      @focus="$emit('fetch-models')"
      @update:model-value="$emit('update:responsesModel', $event)"
    />
  </div>
</template>

<script setup lang="ts">
// 快速添加与详细编辑共用协议表单，避免默认值、必填校验和提示文案漂移。
import type { ChannelFormState } from '@/composables/useChannelEditor'
import { imagesProtocolOptions, isImagesServiceType } from '@/utils/imagesChannelProtocol'

defineProps<{
  serviceType: ChannelFormState['serviceType']
  responsesModel: string
  modelOptions: Array<{ title: string; value: string }>
  modelsLoading: boolean
  modelsError: string
}>()
defineEmits<{
  'update:serviceType': [value: ChannelFormState['serviceType']]
  'update:responsesModel': [value: string]
  'fetch-models': []
}>()

const validateProtocol = (value: unknown) => (
  typeof value === 'string' && isImagesServiceType(value)
) || '请选择 Images API 或 Responses API'

const validateResponsesModel = (value: unknown) => (
  typeof value === 'string' && !!value.trim()
) || '请填写支持图片生成工具的 Responses 主模型'
</script>
