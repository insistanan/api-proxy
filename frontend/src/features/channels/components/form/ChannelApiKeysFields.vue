<template>
<UiCard variant="outlined" rounded="lg" :color="form.apiKeys.length === 0 ? 'error' : undefined">
  <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center justify-between flex-wrap gap-2 p-4 pb-2">
    <div class="flex items-center flex-wrap gap-2">
      <UiIcon :color="form.apiKeys.length > 0 ? 'primary' : 'error'">mdi-key</UiIcon>
      <span class="text-base font-bold">API 密钥 *</span>
      <UiBadge v-if="form.apiKeys.length" size="x-small" variant="tonal">{{ form.apiKeys.length }} 个</UiBadge>
      <UiBadge v-if="form.apiKeys.length === 0" size="x-small" color="error" variant="tonal">
        至少需要一个密钥
      </UiBadge>
    </div>
    <span class="text-xs text-muted-foreground">支持多个密钥</span>
  </div>

  <div class="p-4 pt-2">
    <div v-if="copyNotice" class="text-xs text-muted-foreground mb-3" role="status">{{ copyNotice }}</div>
    <!-- 现有密钥列表 -->
    <div v-if="form.apiKeys.length" class="mb-4">
      <UiList density="compact" class="bg-transparent">
        <UiListItem
          v-for="(key, index) in form.apiKeys"
          :key="index"
          class="mb-2"
          rounded="lg"
          variant="tonal"
          :color="duplicateKeyIndex === index ? 'error' : 'surface-variant'"
          :class="{ 'animate-pulse': duplicateKeyIndex === index }"
        >
          <template #prepend>
            <UiIcon size="small" :color="duplicateKeyIndex === index ? 'error' : 'primary'">
              {{ duplicateKeyIndex === index ? 'mdi-alert' : 'mdi-key' }}
            </UiIcon>
          </template>

          <span class="block">
            <div class="flex items-center justify-between flex-wrap gap-2">
              <code class="text-xs">{{ maskApiKey(key) }}</code>
              <div class="flex items-center gap-1">
                <!-- Models 状态标签 -->
                <UiBadge
                  v-if="keyModelsStatus.get(key)?.loading"
                  size="x-small"
                  color="info"
                  variant="tonal"
                >
                  <UiIcon start size="12">mdi-loading</UiIcon>
                  检测中...
                </UiBadge>
                <UiBadge
                  v-else-if="keyModelsStatus.get(key)?.success"
                  size="x-small"
                  color="success"
                  variant="tonal"
                >
                  models {{ keyModelsStatus.get(key)?.statusCode }} ({{ keyModelsStatus.get(key)?.modelCount }} 个)
                </UiBadge>
                <UiTooltip
                  v-else-if="keyModelsStatus.get(key)?.error"
                  :text="keyModelsStatus.get(key)?.error"
                  location="top"
                  max-width="300"
                >
                  <template #activator="{ props: tooltipProps }">
                    <UiBadge
                      v-bind="tooltipProps"
                      size="x-small"
                      color="error"
                      variant="tonal"
                    >
                      models {{ keyModelsStatus.get(key)?.statusCode || 'ERR' }}
                    </UiBadge>
                  </template>
                </UiTooltip>
                <!-- 重复密钥标签 -->
                <UiBadge v-if="duplicateKeyIndex === index" size="x-small" color="error" variant="text">
                  重复密钥
                </UiBadge>
              </div>
            </div>
          </span>

          <template #append>
            <div class="flex items-center gap-1">
              <!-- 置顶/置底：仅首尾密钥显示 -->
              <UiTooltip
                v-if="index === form.apiKeys.length - 1 && form.apiKeys.length > 1"
                text="置顶"
                location="top"
                :open-delay="150"
                content-class="key-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <UiButton
                    v-bind="tooltipProps"
                    size="small"
                    color="warning"
                    icon
                    variant="text"
                    rounded="md"
                    aria-label="将密钥移到首位"
                    @click="moveApiKeyToTop(index)"
                  >
                    <UiIcon size="small">mdi-arrow-up-bold</UiIcon>
                  </UiButton>
                </template>
              </UiTooltip>
              <UiTooltip
                v-if="index === 0 && form.apiKeys.length > 1"
                text="置底"
                location="top"
                :open-delay="150"
                content-class="key-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <UiButton
                    v-bind="tooltipProps"
                    size="small"
                    color="warning"
                    icon
                    variant="text"
                    rounded="md"
                    aria-label="将密钥移到末位"
                    @click="moveApiKeyToBottom(index)"
                  >
                    <UiIcon size="small">mdi-arrow-down-bold</UiIcon>
                  </UiButton>
                </template>
              </UiTooltip>
              <UiTooltip
                :text="copiedKeyIndex === index ? '已复制!' : '复制密钥'"
                location="top"
                :open-delay="150"
                content-class="key-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <UiButton
                    v-bind="tooltipProps"
                    size="small"
                    :color="copiedKeyIndex === index ? 'success' : 'primary'"
                    icon
                    variant="text"
                    aria-label="复制密钥"
                    @click="copyApiKey(key, index)"
                  >
                    <UiIcon size="small">{{
                      copiedKeyIndex === index ? 'mdi-check' : 'mdi-content-copy'
                    }}</UiIcon>
                  </UiButton>
                </template>
              </UiTooltip>
              <UiTooltip text="删除密钥" location="top" :open-delay="150" content-class="key-tooltip">
                <template #activator="{ props: tooltipProps }">
                  <UiButton
                    v-bind="tooltipProps"
                    size="small"
                    color="error"
                    icon
                    variant="text"
                    aria-label="删除密钥"
                    @click="removeApiKey(index)"
                  >
                    <UiIcon size="small" color="error">mdi-close</UiIcon>
                  </UiButton>
                </template>
              </UiTooltip>
            </div>
          </template>
        </UiListItem>
      </UiList>
    </div>

    <!-- 添加新密钥 -->
    <div class="flex items-start gap-3">
      <UiInput
        v-model="newApiKey"
        label="添加新的API密钥"
        placeholder="输入完整的API密钥"
        prepend-inner-icon="mdi-plus"
        variant="outlined"
        density="comfortable"
        type="password"
        :error="!!apiKeyError"
        :error-messages="apiKeyError"
        class="grow"
        @keydown.enter.prevent="addApiKey"
        @input="handleApiKeyInput"
      />
      <UiButton
        color="primary"
        variant="elevated"
        size="large"
        height="40"
        :disabled="!newApiKey.trim()"
        class="mt-1"
        @click="addApiKey"
      >
        添加
      </UiButton>
    </div>
  </div>
</UiCard>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
// 密钥列表与输入共用父级编辑草稿，切换模式或取消时不写入已保存配置。
import type { useChannelModal } from '@/features/channels/composables/useChannelModal'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
const {
  form, copyNotice, duplicateKeyIndex, keyModelsStatus, maskApiKey,
  moveApiKeyToTop, moveApiKeyToBottom, copiedKeyIndex, copyApiKey,
  removeApiKey, newApiKey, apiKeyError, addApiKey, handleApiKeyInput
} = props.editor
</script>

<style scoped src="@/features/channels/styles/channel-modal.css"></style>
