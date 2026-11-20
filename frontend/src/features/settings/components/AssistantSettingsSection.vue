<template>
  <section aria-labelledby="assistant-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="assistant-settings-title" class="text-lg font-bold">AI 配置助手</h2>
      </div>
    </div>

    <UiForm @submit.prevent="channelSelectionValid && saveAssistantSettings()">
      <div class="setting-row">
        <div class="setting-copy">
          <div class="text-base font-medium">模型分组</div>
          <div class="text-sm text-muted-foreground mt-1">使用分组匹配词选择模型，复用渠道的优先级和健康检查。</div>
        </div>
      </div>

      <UiSelect
        v-model="assistantModel"
        :items="assistantModelItems"
        item-title="title"
        item-value="value"
        label="模型分组"
        :loading="assistantModelsLoading"
        :error-messages="assistantModelsError"
        variant="outlined"
        density="comfortable"
      />

      <ChannelPicker v-model="assistantChannel" :options="assistantStatus?.channels ?? []" :error="assistantStatusError" allow-auto label="指定渠道" class="mt-5" @valid="channelSelectionValid = $event" />
      <p class="text-xs text-muted-foreground">自动选择按协议优先级调度；指定后固定使用该渠道。</p>

      <UiAlert
        v-if="assistantStatus && !assistantStatusError"
        :type="assistantStatus.available ? 'info' : 'warning'"
        variant="tonal"
        density="compact"
        class="mt-5"
      >
        <template v-if="assistantStatus.available">
          当前生效：{{ assistantStatus.channelId ? '指定渠道' : assistantStatus.model ? `分组「${assistantStatus.model}」` : '自动（兜底分组）' }}
          <template v-if="assistantStatus.channel">
            → {{ assistantStatus.channel.name }}（{{ assistantKindLabel(assistantStatus.channel.kind) }}）
          </template>
        </template>
        <template v-else>{{ assistantStatus.reason || '当前没有可用的模型渠道' }}</template>
      </UiAlert>

      <UiAlert v-if="assistantNotice" :type="assistantNotice.type" variant="tonal" density="compact" class="mt-3">
        {{ assistantNotice.message }}
      </UiAlert>

      <div class="settings-actions">
        <UiButton
          type="submit"
          color="primary"
          prepend-icon="mdi-content-save"
          :loading="saving"
          :disabled="!assistantDirty || !channelSelectionValid"
        >
          保存助手设置
        </UiButton>
      </div>
    </UiForm>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { ref } from 'vue'
import type { useAssistantSettings } from '@/features/settings/composables/useAssistantSettings'
import ChannelPicker from '@/shared/components/domain/ChannelPicker.vue'

const props = defineProps<{ editor: ReturnType<typeof useAssistantSettings> }>()
const channelSelectionValid = ref(true)
const {
  assistantStatus,
  assistantStatusError,
  assistantModelsError,
  assistantModelsLoading,
  assistantKindLabel,
  assistantModel,
  assistantChannel,
  assistantNotice,
  assistantDirty,
  assistantModelItems,
  saveAssistantSettings,
  saving
} = props.editor
</script>

<style scoped src="@/features/settings/styles/settings.css"></style>
