<template>
  <UiDialog :model-value="show" max-width="900" :persistent="saving" @update:model-value="$emit('update:show', $event)">
    <UiCard rounded="lg" class="dialog-card-shell">
      <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center gap-3 dialog-card-header">
        <div    class="inline-flex size-10 shrink-0 items-center justify-center rounded-control bg-primary/12 text-primary dialog-header-avatar">
          <UiIcon size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</UiIcon>
        </div>
        <div class="grow">
          <div class="text-lg font-bold">
            {{ isEditing ? '编辑渠道' : isCopying ? '复制渠道' : draftPreset === 'summary' ? '添加文本摘要渠道' : draftPreset === 'vision' ? '添加图片理解渠道' : '添加新渠道' }}
          </div>
          <div class="text-sm text-muted-foreground dialog-card-subtitle">
            {{
              draftPreset === 'summary'
                ? '跨协议共享文本摘要池 · 粘贴连接信息并选择摘要模型'
                : draftPreset === 'vision'
                  ? '跨协议共享图片理解池 · 粘贴地址和密钥快速创建'
                  : `${entryLabel} 入口 · ${isQuickMode && !isEditing && !isCopying ? '粘贴地址和密钥，快速创建渠道' : '连接信息、模型映射与渠道策略'}`
            }}
          </div>
        </div>
        <div class="flex items-center gap-2 header-actions">
          <UiButton v-if="!isEditing && !isCopying && isPresetProtocol(channelType)" variant="tonal" size="small" :disabled="saving" :aria-expanded="editor.providerDrawerOpen.value" @click="editor.providerDrawerOpen.value = true">
            常见渠道商
          </UiButton>
          <!-- 模式切换按钮（仅在添加模式显示） -->
          <UiButton v-if="!isEditing && !isCopying" variant="outlined" size="small" class="mode-toggle-btn" :disabled="saving" @click="toggleMode">
            <UiIcon start size="16">{{ isQuickMode ? 'mdi-form-textbox' : 'mdi-lightning-bolt' }}</UiIcon>
            {{ isQuickMode ? '详细配置' : '快速添加' }}
          </UiButton>
          <UiButton
            icon
            size="small"
            variant="text"
            class="header-action-btn"
            title="关闭"
            aria-label="关闭渠道配置"
            :disabled="saving"
            @click="handleCancel"
          >
            <UiIcon size="18">mdi-close</UiIcon>
          </UiButton>
        </div>
      </div>

      <div class="p-4 dialog-card-body">
        <fieldset :disabled="saving" :inert="saving" class="agent-config-editor">
          <template v-if="canSelectProtocol">
            <p v-if="isCopying" class="text-sm text-muted-foreground mb-4">从 {{ copySourceLabel }} 的“{{ copySource?.name }}”创建新渠道，可修改连接信息、分组与模型配置。</p>
            <UiSelect
              :model-value="channelType"
              label="目标协议"
              :items="protocolOptions"
              variant="outlined"
              density="comfortable"
              class="mb-4"
              @update:model-value="selectTargetProtocol"
            />
            <UiAlert v-if="isCopying && channelType !== (copySourceType ?? channelType)" type="info" variant="tonal" class="mb-4">
              请确认供应商支持所选上游服务类型和模型。跨协议复制已重新初始化专属策略，并移除转发到原协议其他渠道的模型映射。
            </UiAlert>
          </template>
          <!-- 两种模式共用同一个草稿与表单引用 -->
          <ChannelQuickForm v-if="!isEditing && !isCopying && isQuickMode" :editor="editor" />
          <ChannelDetailedForm v-else :editor="editor" />
        </fieldset>
      </div>

      <div class="flex flex-wrap items-center gap-2 px-4 py-3 dialog-card-actions">
        <span class="flex-1" ></span>
        <UiButton variant="text" :disabled="saving" @click="handleCancel"> 取消 </UiButton>
        <UiButton
          v-if="!isEditing && !isCopying && isQuickMode"
          color="primary"
          variant="elevated"
          :disabled="!isQuickFormValid || saving"
          :loading="saving"
          prepend-icon="mdi-check"
          @click="handleQuickSubmit"
        >
          创建渠道
        </UiButton>
        <UiButton
          v-else
          color="primary"
          variant="elevated"
          :disabled="saving"
          :loading="saving"
          prepend-icon="mdi-check"
          @click="handleSubmit"
        >
          {{ isEditing ? '更新渠道' : isCopying ? '确认复制' : '创建渠道' }}
        </UiButton>
      </div>
      <ChannelProviderDrawer v-model="editor.providerDrawerOpen.value" :channel-type="channelType" @select="editor.applyProviderPreset" />
    </UiCard>
  </UiDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiDialog from '@/shared/ui/overlay/Dialog.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
// 渠道弹窗入口：保持公开事件，编排逻辑与两种表单分别维护。
import type { Channel } from '@/api'
import { computed } from 'vue'
import { findNavigation } from '@/router/navigation'
import { API_TABS, API_TAB_LABELS, type ApiTab } from '@/features/channels/services/channel-api'
import { isPresetProtocol } from '@/features/channels/utils/channelPresets'
import { useChannelModal } from '@/features/channels/composables/useChannelModal'
import type { ChannelDraftPreset } from '@/features/channels/utils/channelDraftPreset'
import ChannelQuickForm from '@/features/channels/components/form/ChannelQuickForm.vue'
import ChannelDetailedForm from '@/features/channels/components/form/ChannelDetailedForm.vue'
import ChannelProviderDrawer from '@/features/channels/components/form/ChannelProviderDrawer.vue'

interface Props {
  show: boolean
  saving?: boolean
  channel?: Channel | null
  copySource?: Channel | null
  copySourceType?: ApiTab
  channelType?: ApiTab
  initialPoolId?: string
  allowProtocolChange?: boolean
  draftPreset?: ChannelDraftPreset | null
}

const props = withDefaults(defineProps<Props>(), {
  channelType: 'messages',
  initialPoolId: 'default'
})

const emit = defineEmits<{
  'update:show': [value: boolean]
  'update:channelType': [value: ApiTab]
  save: [channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }]
}>()

const editor = useChannelModal(props, emit)
const entryLabel = computed(() => findNavigation(`/channels/${props.channelType}`)?.label)
const isCopying = computed(() => !!props.copySource)
const canSelectProtocol = computed(() => !props.channel && (isCopying.value || props.allowProtocolChange))
const copySourceLabel = computed(() => API_TAB_LABELS[props.copySourceType ?? props.channelType])
const protocolOptions = API_TABS.map(value => ({ title: API_TAB_LABELS[value], value }))
const selectTargetProtocol = (value: ApiTab) => {
  if (canSelectProtocol.value && !props.saving && API_TABS.includes(value)) emit('update:channelType', value)
}
const {
  isEditing, isQuickMode, toggleMode,
  isQuickFormValid, handleQuickSubmit, handleSubmit, handleCancel,
  // 保留入口组件原有的草稿引用，方便现有调用与特性测试观察同一份编辑状态。
  form, formRef, autoCompleteUrl, quickInput, parseQuickInput,
  detectedBaseUrl, detectedBaseUrls, detectedApiKeys, imagesExtraParamsError, imagesOutputError
} = editor
</script>

<style scoped src="@/features/channels/styles/channel-modal.css"></style>
