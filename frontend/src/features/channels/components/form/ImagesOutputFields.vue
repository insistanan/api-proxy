<template>
  <div>
    <UiSelect
      :model-value="outputFormat"
      :items="imagesOutputFormatOptions"
      label="输出格式"
      variant="outlined"
      density="comfortable"
      @update:model-value="$emit('update:outputFormat', $event)"
    />
    <div v-if="outputFormat === 'gif'" class="grid grid-cols-12 gap-4 mt-1">
      <div class="min-w-0 col-span-12 sm:col-span-6"  >
        <UiInput
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
      </div>
      <div class="min-w-0 col-span-12 sm:col-span-6"  >
        <UiInput
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
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
// 快速添加和详细编辑共用输出表单，格式与 GIF 参数都写入独立渠道设置。
import type { ImagesOutputFormat } from '@/types/channel'
import { imagesOutputFormatOptions, validateImagesFrameCount, validateImagesFrameDuration } from '@/features/channels/utils/imagesChannelProtocol'

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
