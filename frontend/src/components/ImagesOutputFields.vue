<template>
  <div>
    <v-select
      :model-value="outputFormat"
      :items="imagesOutputFormatOptions"
      label="输出格式"
      variant="outlined"
      density="comfortable"
      hint="固定格式会覆盖请求和自定义参数；跟随请求时不额外指定格式。"
      persistent-hint
      @update:model-value="$emit('update:outputFormat', $event)"
    />
    <v-row v-if="outputFormat === 'gif'" class="mt-1">
      <v-col cols="12" sm="6">
        <v-text-field
          :model-value="frameCount"
          label="GIF 帧数"
          type="number"
          min="2"
          max="16"
          step="1"
          :rules="[validateImagesFrameCount]"
          variant="outlined"
          density="comfortable"
          hint="默认 9 帧，可选 2–16 帧；清空后跟随请求或上游默认。"
          persistent-hint
          @update:model-value="$emit('update:frameCount', $event)"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-text-field
          :model-value="frameDurationMs"
          label="每帧时长（毫秒）"
          type="number"
          min="20"
          :max="serviceType === 'responses' ? 2000 : undefined"
          step="1"
          :rules="[value => validateImagesFrameDuration(value, serviceType)]"
          variant="outlined"
          density="comfortable"
          :hint="serviceType === 'responses' ? '默认 200 毫秒，可选 20–2000 毫秒；清空后继承。' : '默认 200 毫秒，至少 20 毫秒；清空后继承。'"
          persistent-hint
          @update:model-value="$emit('update:frameDurationMs', $event)"
        />
      </v-col>
    </v-row>
    <div v-if="outputFormat === 'gif' || outputFormat === 'svg'" class="text-caption text-medium-emphasis mt-2">
      {{ serviceType === 'responses' ? '本设置对 Responses 渠道同样生效，支持流式返回完整生成结果。' : '本格式自动使用非流式生成；客户端需要流式时仍返回 completed 事件。' }}
      {{ outputFormat === 'gif' ? 'GIF 不支持蒙版。' : 'SVG 仅支持单图输出。' }}
    </div>
  </div>
</template>

<script setup lang="ts">
// 快速添加和详细编辑共用输出表单，格式与 GIF 参数都写入独立渠道设置。
import type { ImagesOutputFormat } from '@/types/channel'
import { imagesOutputFormatOptions, validateImagesFrameCount, validateImagesFrameDuration } from '@/utils/imagesChannelProtocol'

defineProps<{
  outputFormat: ImagesOutputFormat
  frameCount: number | string | null
  frameDurationMs: number | string | null
  serviceType: string
}>()

defineEmits<{
  'update:outputFormat': [value: ImagesOutputFormat]
  'update:frameCount': [value: number | string | null]
  'update:frameDurationMs': [value: number | string | null]
}>()
</script>
