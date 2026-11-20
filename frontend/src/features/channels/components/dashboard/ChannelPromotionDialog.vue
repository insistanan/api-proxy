<template>
  <AppDialog
    v-model="showPromotionDialog"
    :title="promotionChannel ? '设置促销期 — ' + promotionChannel.name : '设置促销期'"
    size="sm"
    :busy="isSavingPromotion"
  >
    <!-- 促销渠道在所属分组内临时优先，仍受健康检查与熔断限制。可同时设置时间和成功请求次数，任一限制达到即结束；两项均为 0 时清除促销。 -->
    <UiAlert v-if="promotionError" type="error" variant="tonal" density="compact" class="mb-4">{{ promotionError }}</UiAlert>
    <UiInput
      v-model.number="promotionDuration"
      :disabled="isSavingPromotion"
      label="促销时长（分钟）"
      type="number"
      :min="0"
      density="compact"
      class="mb-3"
    />
    <UiInput
      v-model.number="promotionCount"
      :disabled="isSavingPromotion"
      label="促销请求次数"
      type="number"
      :min="0"
      density="compact"
    />
    <template #actions>
      <UiButton variant="text" :disabled="isSavingPromotion" @click="closePromotionDialog">取消</UiButton>
      <UiButton color="info" variant="flat" :loading="isSavingPromotion" :disabled="isSavingPromotion" @click="confirmPromotion">确认设置</UiButton>
    </template>
  </AppDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
// 促销表单只绑定外部编辑器，避免复制草稿和提交状态。
import type { useChannelPromotion } from '@/features/channels/composables/useChannelPromotion'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'

const props = defineProps<{ editor: ReturnType<typeof useChannelPromotion> }>()
const {
  showPromotionDialog, promotionChannel, isSavingPromotion, promotionError,
  promotionDuration, promotionCount, closePromotionDialog, confirmPromotion
} = props.editor
</script>
