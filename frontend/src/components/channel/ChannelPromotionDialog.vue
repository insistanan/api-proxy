<template>
<v-dialog v-model="showPromotionDialog" max-width="420" :persistent="isSavingPromotion">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon color="info" class="mr-2">mdi-rocket-launch</v-icon>
          设置促销期
          <span v-if="promotionChannel" class="ml-2 text-subtitle-1 text-medium-emphasis"> — {{ promotionChannel.name }}</span>
        </v-card-title>
        <v-divider />
        <v-card-text class="pt-4">
          <!-- 促销渠道在所属分组内临时优先，仍受健康检查与熔断限制。可同时设置时间和成功请求次数，任一限制达到即结束；两项均为 0 时清除促销。 -->
          <v-alert v-if="promotionError" type="error" variant="tonal" density="compact" class="mb-4">{{ promotionError }}</v-alert>
          <v-text-field
            v-model.number="promotionDuration"
            :disabled="isSavingPromotion"
            label="促销时长（分钟）"
            type="number"
            :min="0"
            density="compact"
            class="mb-3"
          />
          <v-text-field
            v-model.number="promotionCount"
            :disabled="isSavingPromotion"
            label="促销请求次数"
            type="number"
            :min="0"
            density="compact"
          />
        </v-card-text>
        <v-divider />
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="isSavingPromotion" @click="closePromotionDialog">取消</v-btn>
          <v-btn color="info" variant="flat" :loading="isSavingPromotion" :disabled="isSavingPromotion" @click="confirmPromotion">确认设置</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
</template>

<script setup lang="ts">
// 促销表单只绑定外部编辑器，避免复制草稿和提交状态。
import type { useChannelPromotion } from '@/composables/useChannelPromotion'

const props = defineProps<{ editor: ReturnType<typeof useChannelPromotion> }>()
const {
  showPromotionDialog, promotionChannel, isSavingPromotion, promotionError,
  promotionDuration, promotionCount, closePromotionDialog, confirmPromotion
} = props.editor
</script>
