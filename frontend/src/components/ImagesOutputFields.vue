<template>
  <div>
    <v-select
      :model-value="outputFormat"
      :items="imagesOutputFormatOptions"
      label="输出格式"
      variant="outlined"
      density="comfortable"
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
          @update:model-value="$emit('update:frameDurationMs', $event)"
        />
      </v-col>
    </v-row>
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
