<template>
  <section aria-labelledby="assistant-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="assistant-settings-title" class="text-h6 font-weight-bold">AI 配置助手</h2>
      </div>
    </div>

    <v-form @submit.prevent="saveAssistantSettings">
      <div class="setting-row">
        <div class="setting-copy">
          <div class="text-body-1 font-weight-medium">模型分组</div>
          <!-- 助手以分组的匹配词作为模型 ID 走主链路编排（分组匹配 → 优先级 → 熔断/健康 → 兜底）， 复用渠道编排能力，不单独维护默认模型 -->
        </div>
      </div>

      <v-select
        v-model="assistantModel"
        :items="assistantModelItems"
        item-title="title"
        item-value="value"
        label="模型分组"
        variant="outlined"
        density="comfortable"
      />

      <v-select
        v-model="assistantChannel"
        :items="assistantChannelItems"
        item-title="title"
        item-value="value"
        label="指定渠道"
        variant="outlined"
        density="comfortable"
        class="mt-5"
      >
        <template #selection="{ item }">
          <span class="v-select__selection-text">
            {{
              assistantChannelItems.some(option => option.value === item.value)
                ? item.title
                : '指定渠道不可用，请重新选择'
            }}
          </span>
        </template>
      </v-select>

      <v-alert
        v-if="assistantStatus"
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
      </v-alert>

      <v-alert v-if="assistantNotice" :type="assistantNotice.type" variant="tonal" density="compact" class="mt-3">
        {{ assistantNotice.message }}
      </v-alert>

      <div class="settings-actions">
        <v-btn
          type="submit"
          color="primary"
          prepend-icon="mdi-content-save"
          :loading="saving"
          :disabled="!assistantDirty"
        >
          保存助手设置
        </v-btn>
      </div>
    </v-form>
  </section>
</template>

<script setup lang="ts">
import type { useAssistantSettings } from '@/composables/settings/useAssistantSettings'

const props = defineProps<{ editor: ReturnType<typeof useAssistantSettings> }>()
const {
  assistantStatus,
  assistantKindLabel,
  assistantModel,
  assistantChannel,
  assistantNotice,
  assistantDirty,
  assistantChannelItems,
  assistantModelItems,
  saveAssistantSettings,
  saving
} = props.editor
</script>

<style scoped src="../../styles/settings.css"></style>
