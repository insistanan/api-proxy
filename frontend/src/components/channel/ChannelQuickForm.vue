<template>
<div>
  <p class="quick-form-guide">粘贴上游请求地址和 API 密钥，每行一项；下方会显示识别结果。</p>
  <v-textarea
    v-model="quickInput"
    label="输入内容"
    placeholder="每行输入一个 API Key、Base URL 或渠道名称&#10;&#10;示例:&#10;name: 备用 OpenAI 渠道&#10;sk-xxx-your-api-key&#10;https://api.example.com/v1"
    variant="outlined"
    rows="6"
    no-resize
    autofocus
    class="quick-input-textarea"
    @input="parseQuickInput"
  />

  <p class="quick-form-note">需要模型映射或独立代理时，请切换到详细配置并在那里保存。</p>

  <div class="quick-channel-fields">
    <v-text-field
      v-model="quickChannelName"
      label="渠道名称 (可选)"
      placeholder="不填则从输入内容识别或自动生成"
      prepend-inner-icon="mdi-tag"
      variant="outlined"
      density="comfortable"
    />

    <v-select
      v-model="form.poolId"
      label="渠道分组"
      :items="poolOptions"
      :loading="poolsLoading"
      :error-messages="poolLoadError"
      variant="outlined"
      density="comfortable"
    />

    <v-select
      v-if="supportsClientIdentity"
      v-model="form.clientIdentityMode"
      label="客户端身份"
      :items="clientIdentityModeItems"
      :hint="clientIdentityHint"
      variant="outlined"
      density="comfortable"
    />
  </div>

  <!-- 检测状态提示 -->
  <ImagesChannelProtocolFields
    v-if="channelType === 'images'"
    v-model:service-type="form.serviceType"
    v-model:responses-model="form.responsesModel"
    :model-options="targetModelOptions"
    :models-loading="fetchingModels"
    :models-error="fetchModelsError"
    class="mt-4"
    @fetch-models="handleFetchModels('')"
  />
  <ImagesOutputFields
    v-if="channelType === 'images'"
    v-model:output-format="form.imagesOutputFormat"
    v-model:frame-count="form.imagesFrameCount"
    v-model:frame-duration-ms="form.imagesFrameDurationMs"
    :service-type="form.serviceType"
    class="mt-4"
  />
  <v-card variant="outlined" class="mt-4 detection-status-card" rounded="lg">
    <v-card-text class="pa-4">
      <div class="d-flex flex-column ga-3">
        <!-- Base URL 检测 -->
        <div class="d-flex align-start ga-3">
          <v-icon :color="detectedBaseUrls.length > 0 ? 'success' : hasQuickInput ? 'error' : undefined" size="20" class="mt-1">
            {{ detectedBaseUrls.length > 0 ? 'mdi-check-circle' : hasQuickInput ? 'mdi-alert-circle' : 'mdi-web' }}
          </v-icon>
          <div class="flex-grow-1">
            <div class="d-flex align-center justify-space-between flex-wrap ga-2">
              <span class="text-body-2 font-weight-medium">{{ form.useFullUrl ? '完整请求地址' : 'Base URL' }}</span>
              <v-switch
                v-model="autoCompleteUrl"
                label="自动补全请求地址"
                color="primary"
                density="compact"
                inset
                hide-details
                class="flex-grow-0"
              />
            </div>
            <div v-if="detectedBaseUrls.length === 0" class="text-caption" :class="hasQuickInput ? 'text-error' : 'text-medium-emphasis'">
              请输入一个有效的 URL (https://...)
            </div>
            <div v-else class="d-flex flex-column ga-2 mt-1">
              <div v-for="url in detectedBaseUrls" :key="url" class="base-url-item">
                <div class="text-caption text-success">{{ url }}</div>
              </div>
            </div>
          </div>
          <v-chip v-if="detectedBaseUrls.length > 0" size="x-small" color="success" variant="tonal">
            {{ detectedBaseUrls.length }} 个
          </v-chip>
        </div>

        <!-- API Keys 检测 -->
        <div class="d-flex align-center ga-3">
          <v-icon :color="detectedApiKeys.length > 0 ? 'success' : hasQuickInput ? 'error' : undefined" size="20">
            {{ detectedApiKeys.length > 0 ? 'mdi-check-circle' : hasQuickInput ? 'mdi-alert-circle' : 'mdi-key' }}
          </v-icon>
          <div class="flex-grow-1">
            <div class="text-body-2 font-weight-medium">API 密钥</div>
            <div class="text-caption" :class="detectedApiKeys.length > 0 ? 'text-success' : hasQuickInput ? 'text-error' : 'text-medium-emphasis'">
              {{
                detectedApiKeys.length > 0
                  ? `已检测到 ${detectedApiKeys.length} 个密钥`
                  : '请至少输入一个 API Key'
              }}
            </div>
          </div>
          <v-chip v-if="detectedApiKeys.length > 0" size="x-small" color="success" variant="tonal">
            {{ detectedApiKeys.length }} 个
          </v-chip>
        </div>

        <!-- 渠道名称预览 -->
        <div class="d-flex align-center ga-3">
          <v-icon color="primary" size="20">mdi-tag</v-icon>
          <div class="flex-grow-1">
            <div class="text-body-2 font-weight-medium">渠道名称</div>
            <div class="text-caption text-primary font-weight-medium">
              {{ generatedChannelName }}
            </div>
            <div v-if="detectedChannelName && !quickChannelName.trim()" class="text-caption text-medium-emphasis">
              已从输入内容识别：{{ detectedChannelName }}
            </div>
          </div>
          <v-chip size="x-small" color="primary" variant="tonal">
            {{ quickChannelName.trim() || detectedChannelName ? '自定义' : '自动生成' }}
          </v-chip>
        </div>

        <!-- 渠道类型提示 -->
        <div class="d-flex align-center ga-3">
          <v-icon color="info" size="20">mdi-information</v-icon>
          <div class="flex-grow-1">
            <div class="text-body-2 font-weight-medium">入口与上游</div>
            <div class="text-caption text-medium-emphasis">
              {{ channelTypeLabel }} 入口 → {{ upstreamLabel }} 上游
            </div>
          </div>
        </div>
      </div>
    </v-card-text>
  </v-card>
</div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { useChannelModal } from '@/composables/useChannelModal'
import { findNavigation } from '@/router/navigation'
import ImagesChannelProtocolFields from '../ImagesChannelProtocolFields.vue'
import ImagesOutputFields from '../ImagesOutputFields.vue'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
const {
  parseQuickInput,
  quickServiceType,
  supportsClientIdentity,
  clientIdentityModeItems,
  clientIdentityHint,
  serviceTypeOptions,
  autoCompleteUrl,
  quickInput,
  quickChannelName,
  detectedBaseUrls,
  detectedApiKeys,
  detectedChannelName,
  generatedChannelName,
  form,
  poolOptions,
  poolsLoading,
  poolLoadError,
  targetModelOptions,
  fetchingModels,
  fetchModelsError,
  handleFetchModels,
  imagesProtocolLabel,
  channelType
} = props.editor

const hasQuickInput = computed(() => quickInput.value.trim().length > 0)
const channelTypeLabel = computed(() => findNavigation(`/channels/${channelType.value}`)?.label)
const upstreamLabel = computed(() => channelType.value === 'images'
  ? imagesProtocolLabel(quickServiceType.value)
  : serviceTypeOptions.value.find(option => option.value === quickServiceType.value)?.title || quickServiceType.value)
</script>

<style scoped src="../../styles/channel-modal.css"></style>
