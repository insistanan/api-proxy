<template>
<v-card variant="outlined" rounded="lg" :color="form.apiKeys.length === 0 ? 'error' : undefined">
  <v-card-title class="d-flex align-center justify-space-between flex-wrap ga-2 pa-4 pb-2">
    <div class="d-flex align-center flex-wrap ga-2">
      <v-icon :color="form.apiKeys.length > 0 ? 'primary' : 'error'">mdi-key</v-icon>
      <span class="text-body-1 font-weight-bold">API 密钥 *</span>
      <v-chip v-if="form.apiKeys.length" size="x-small" variant="tonal">{{ form.apiKeys.length }} 个</v-chip>
      <v-chip v-if="form.apiKeys.length === 0" size="x-small" color="error" variant="tonal">
        至少需要一个密钥
      </v-chip>
    </div>
    <span class="text-caption text-medium-emphasis">支持多个密钥</span>
  </v-card-title>

  <v-card-text class="pt-2">
    <div v-if="copyNotice" class="text-caption text-medium-emphasis mb-3" role="status">{{ copyNotice }}</div>
    <!-- 现有密钥列表 -->
    <div v-if="form.apiKeys.length" class="mb-4">
      <v-list density="compact" class="bg-transparent">
        <v-list-item
          v-for="(key, index) in form.apiKeys"
          :key="index"
          class="mb-2"
          rounded="lg"
          variant="tonal"
          :color="duplicateKeyIndex === index ? 'error' : 'surface-variant'"
          :class="{ 'animate-pulse': duplicateKeyIndex === index }"
        >
          <template #prepend>
            <v-icon size="small" :color="duplicateKeyIndex === index ? 'error' : 'primary'">
              {{ duplicateKeyIndex === index ? 'mdi-alert' : 'mdi-key' }}
            </v-icon>
          </template>

          <v-list-item-title>
            <div class="d-flex align-center justify-space-between flex-wrap ga-2">
              <code class="text-caption">{{ maskApiKey(key) }}</code>
              <div class="d-flex align-center ga-1">
                <!-- Models 状态标签 -->
                <v-chip
                  v-if="keyModelsStatus.get(key)?.loading"
                  size="x-small"
                  color="info"
                  variant="tonal"
                >
                  <v-icon start size="12">mdi-loading</v-icon>
                  检测中...
                </v-chip>
                <v-chip
                  v-else-if="keyModelsStatus.get(key)?.success"
                  size="x-small"
                  color="success"
                  variant="tonal"
                >
                  models {{ keyModelsStatus.get(key)?.statusCode }} ({{ keyModelsStatus.get(key)?.modelCount }} 个)
                </v-chip>
                <v-tooltip
                  v-else-if="keyModelsStatus.get(key)?.error"
                  :text="keyModelsStatus.get(key)?.error"
                  location="top"
                  max-width="300"
                >
                  <template #activator="{ props: tooltipProps }">
                    <v-chip
                      v-bind="tooltipProps"
                      size="x-small"
                      color="error"
                      variant="tonal"
                    >
                      models {{ keyModelsStatus.get(key)?.statusCode || 'ERR' }}
                    </v-chip>
                  </template>
                </v-tooltip>
                <!-- 重复密钥标签 -->
                <v-chip v-if="duplicateKeyIndex === index" size="x-small" color="error" variant="text">
                  重复密钥
                </v-chip>
              </div>
            </div>
          </v-list-item-title>

          <template #append>
            <div class="d-flex align-center ga-1">
              <!-- 置顶/置底：仅首尾密钥显示 -->
              <v-tooltip
                v-if="index === form.apiKeys.length - 1 && form.apiKeys.length > 1"
                text="置顶"
                location="top"
                :open-delay="150"
                content-class="key-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-btn
                    v-bind="tooltipProps"
                    size="small"
                    color="warning"
                    icon
                    variant="text"
                    rounded="md"
                    aria-label="将密钥移到首位"
                    @click="moveApiKeyToTop(index)"
                  >
                    <v-icon size="small">mdi-arrow-up-bold</v-icon>
                  </v-btn>
                </template>
              </v-tooltip>
              <v-tooltip
                v-if="index === 0 && form.apiKeys.length > 1"
                text="置底"
                location="top"
                :open-delay="150"
                content-class="key-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-btn
                    v-bind="tooltipProps"
                    size="small"
                    color="warning"
                    icon
                    variant="text"
                    rounded="md"
                    aria-label="将密钥移到末位"
                    @click="moveApiKeyToBottom(index)"
                  >
                    <v-icon size="small">mdi-arrow-down-bold</v-icon>
                  </v-btn>
                </template>
              </v-tooltip>
              <v-tooltip
                :text="copiedKeyIndex === index ? '已复制!' : '复制密钥'"
                location="top"
                :open-delay="150"
                content-class="key-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-btn
                    v-bind="tooltipProps"
                    size="small"
                    :color="copiedKeyIndex === index ? 'success' : 'primary'"
                    icon
                    variant="text"
                    aria-label="复制密钥"
                    @click="copyApiKey(key, index)"
                  >
                    <v-icon size="small">{{
                      copiedKeyIndex === index ? 'mdi-check' : 'mdi-content-copy'
                    }}</v-icon>
                  </v-btn>
                </template>
              </v-tooltip>
              <v-tooltip text="删除密钥" location="top" :open-delay="150" content-class="key-tooltip">
                <template #activator="{ props: tooltipProps }">
                  <v-btn
                    v-bind="tooltipProps"
                    size="small"
                    color="error"
                    icon
                    variant="text"
                    aria-label="删除密钥"
                    @click="removeApiKey(index)"
                  >
                    <v-icon size="small" color="error">mdi-close</v-icon>
                  </v-btn>
                </template>
              </v-tooltip>
            </div>
          </template>
        </v-list-item>
      </v-list>
    </div>

    <!-- 添加新密钥 -->
    <div class="d-flex align-start ga-3">
      <v-text-field
        v-model="newApiKey"
        label="添加新的API密钥"
        placeholder="输入完整的API密钥"
        prepend-inner-icon="mdi-plus"
        variant="outlined"
        density="comfortable"
        type="password"
        :error="!!apiKeyError"
        :error-messages="apiKeyError"
        class="flex-grow-1"
        @keydown.enter.prevent="addApiKey"
        @input="handleApiKeyInput"
      />
      <v-btn
        color="primary"
        variant="elevated"
        size="large"
        height="40"
        :disabled="!newApiKey.trim()"
        class="mt-1"
        @click="addApiKey"
      >
        添加
      </v-btn>
    </div>
  </v-card-text>
</v-card>
</template>

<script setup lang="ts">
// 密钥列表与输入共用父级编辑草稿，切换模式或取消时不写入已保存配置。
import type { useChannelModal } from '@/composables/useChannelModal'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
const {
  form, copyNotice, duplicateKeyIndex, keyModelsStatus, maskApiKey,
  moveApiKeyToTop, moveApiKeyToBottom, copiedKeyIndex, copyApiKey,
  removeApiKey, newApiKey, apiKeyError, addApiKey, handleApiKeyInput
} = props.editor
</script>

<style scoped src="../../styles/channel-modal.css"></style>
