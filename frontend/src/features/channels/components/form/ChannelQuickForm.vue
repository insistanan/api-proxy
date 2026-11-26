<template>
<div>
  <UiTextarea
    v-model="quickInput"
    label="输入内容"
    placeholder="每行输入一个 API Key、Base URL 或渠道名称&#10;&#10;示例:&#10;name: 备用 OpenAI 渠道&#10;sk-xxx-your-api-key&#10;https://api.example.com/v1"
    variant="outlined"
    rows="6"
    no-resize
    autofocus
    class="quick-input-textarea"
    @update:model-value="parseQuickInput"
  />

  <p class="quick-form-note">
    {{
      draftPreset === 'summary'
        ? '已加入跨协议共享文本摘要池并退出常规对话调度；粘贴地址和密钥后即可下拉拉取摘要模型。'
        : draftPreset === 'vision'
          ? '已加入跨协议共享图片理解池并退出常规对话调度；需要独立代理或更多设置可切到详细配置。'
          : '需要模型映射或独立代理时，请切换到详细配置并在那里保存。'
    }}
  </p>

  <div class="quick-channel-fields">
    <UiInput
      v-model="quickChannelName"
      label="渠道名称 (可选)"
      placeholder="不填则从输入内容识别或自动生成"
      prepend-inner-icon="mdi-tag"
      variant="outlined"
      density="comfortable"
    />

    <UiSelect
      v-if="draftPreset"
      v-model="form.serviceType"
      label="上游服务类型"
      :items="serviceTypeOptions"
      prepend-inner-icon="mdi-cog"
      variant="outlined"
      density="comfortable"
    />

    <UiSelect
      v-else
      v-model="form.poolId"
      label="渠道分组"
      :items="poolOptions"
      :loading="poolsLoading"
      :error-messages="poolLoadError"
      variant="outlined"
      density="comfortable"
    />

    <UiSelect
      v-if="!draftPreset && supportsClientIdentity"
      v-model="form.clientIdentityMode"
      label="客户端身份"
      :items="clientIdentityModeItems"
      :hint="clientIdentityHint"
      variant="outlined"
      density="comfortable"
    />
  </div>

  <!-- 文本摘要池快速模式：粘贴连接信息后可直接拉取或填写摘要模型 -->
  <div v-if="draftPreset === 'summary'" class="mt-4">
    <UiSelect
      v-model="form.compaction.summaryModel"
      searchable
      creatable
      label="摘要模型 *"
      placeholder="粘贴地址和密钥后点击拉取，或直接输入模型 ID"
      :items="targetModelOptions"
      item-title="title"
      item-value="value"
      :return-object="false"
      :loading="fetchingModels"
      variant="outlined"
      density="comfortable"
      required
      @focus="handleFetchModels('')"
      @update:menu="(open: boolean) => { if (open) handleFetchModels('') }"
    />
    <div v-if="fetchModelsError" class="text-destructive text-xs mt-1" role="alert">{{ fetchModelsError }}</div>
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
  <UiCard variant="outlined" class="mt-4 detection-status-card" rounded="lg">
    <div class="p-4 p-4">
      <div class="flex flex-col gap-3">
        <!-- Base URL 检测 -->
        <div class="flex items-start gap-3">
          <UiIcon :color="detectedBaseUrls.length > 0 ? 'success' : hasQuickInput ? 'error' : undefined" size="20" class="mt-1">
            {{ detectedBaseUrls.length > 0 ? 'mdi-check-circle' : hasQuickInput ? 'mdi-alert-circle' : 'mdi-web' }}
          </UiIcon>
          <div class="grow">
            <div class="flex items-center justify-between flex-wrap gap-2">
              <span class="text-sm font-medium">{{ form.useFullUrl ? '完整请求地址' : 'Base URL' }}</span>
              <UiSwitch
                v-model="autoCompleteUrl"
                label="自动补全请求地址"
                color="primary"
                density="compact"
                inset
                hide-details
                class="grow-0"
              />
            </div>
            <div v-if="detectedBaseUrls.length === 0" class="text-xs" :class="hasQuickInput ? 'text-destructive' : 'text-muted-foreground'">
              请输入一个有效的 URL (https://...)
            </div>
            <div v-else class="flex flex-col gap-2 mt-1">
              <div v-for="url in detectedBaseUrls" :key="url" class="base-url-item">
                <div class="text-xs text-success">{{ url }}</div>
              </div>
              <div v-if="endpointPreviews.length" class="flex flex-col gap-1">
                <div class="text-xs text-muted-foreground">最终请求地址</div>
                <div v-for="url in endpointPreviews" :key="url" class="text-xs text-muted-foreground break-all">{{ url }}</div>
              </div>
            </div>
          </div>
          <UiBadge v-if="detectedBaseUrls.length > 0" size="x-small" color="success" variant="tonal">
            {{ detectedBaseUrls.length }} 个
          </UiBadge>
        </div>

        <!-- API Keys 检测 -->
        <div class="flex items-center gap-3">
          <UiIcon :color="detectedApiKeys.length > 0 ? 'success' : hasQuickInput ? 'error' : undefined" size="20">
            {{ detectedApiKeys.length > 0 ? 'mdi-check-circle' : hasQuickInput ? 'mdi-alert-circle' : 'mdi-key' }}
          </UiIcon>
          <div class="grow">
            <div class="text-sm font-medium">API 密钥</div>
            <div class="text-xs" :class="detectedApiKeys.length > 0 ? 'text-success' : hasQuickInput ? 'text-destructive' : 'text-muted-foreground'">
              {{
                detectedApiKeys.length > 0
                  ? `已检测到 ${detectedApiKeys.length} 个密钥`
                  : '请至少输入一个 API Key'
              }}
            </div>
          </div>
          <UiBadge v-if="detectedApiKeys.length > 0" size="x-small" color="success" variant="tonal">
            {{ detectedApiKeys.length }} 个
          </UiBadge>
        </div>

        <!-- 渠道名称预览 -->
        <div class="flex items-center gap-3">
          <UiIcon color="primary" size="20">mdi-tag</UiIcon>
          <div class="grow">
            <div class="text-sm font-medium">渠道名称</div>
            <div class="text-xs text-primary font-medium">
              {{ generatedChannelName }}
            </div>
            <div v-if="detectedChannelName && !quickChannelName.trim()" class="text-xs text-muted-foreground">
              已从输入内容识别：{{ detectedChannelName }}
            </div>
          </div>
          <UiBadge size="x-small" color="primary" variant="tonal">
            {{ quickChannelName.trim() || detectedChannelName ? '自定义' : '自动生成' }}
          </UiBadge>
        </div>

        <!-- 渠道类型提示 -->
        <div class="flex items-center gap-3">
          <UiIcon color="info" size="20">mdi-information</UiIcon>
          <div class="grow">
            <div class="text-sm font-medium">入口与上游</div>
            <div class="text-xs text-muted-foreground">
              {{ channelTypeLabel }} 入口 → {{ upstreamLabel }} 上游
            </div>
          </div>
        </div>
      </div>
    </div>
  </UiCard>
</div>
</template>

<script setup lang="ts">
import UiCard from '@/shared/ui/card/Card.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed } from 'vue'
import type { useChannelModal } from '@/features/channels/composables/useChannelModal'
import { findNavigation } from '@/router/navigation'
import ImagesChannelProtocolFields from '@/features/channels/components/form/ImagesChannelProtocolFields.vue'
import ImagesOutputFields from '@/features/channels/components/form/ImagesOutputFields.vue'
import { buildEndpointPreviews } from '@/features/channels/utils/upstreamEndpointPreview'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
const {
  draftPreset,
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
// 快速模式同样预览网关补全后的最终地址，帮助核对 BaseURL 是否多写或少写版本号。
const endpointPreviews = computed(() => buildEndpointPreviews({
  baseUrls: detectedBaseUrls.value,
  channelType: channelType.value,
  serviceType: quickServiceType.value,
  model: form.defaultModel,
  useFullUrl: form.useFullUrl
}))
</script>

<style scoped src="@/features/channels/styles/channel-modal.css"></style>
