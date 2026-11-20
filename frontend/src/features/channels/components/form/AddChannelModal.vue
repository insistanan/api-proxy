<template>
  <v-dialog :model-value="show" max-width="900" :persistent="saving" @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="dialog-card-shell">
      <v-card-title class="d-flex align-center ga-3 dialog-card-header">
        <v-avatar color="primary" variant="tonal" size="40" class="dialog-header-avatar">
          <v-icon size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</v-icon>
        </v-avatar>
        <div class="flex-grow-1">
          <div class="text-h6 font-weight-bold">
            {{ isEditing ? '编辑渠道' : isCopying ? '复制渠道' : '添加新渠道' }}
          </div>
          <div class="text-body-2 text-medium-emphasis dialog-card-subtitle">
            {{ entryLabel }} 入口 · {{ isQuickMode && !isEditing && !isCopying ? '粘贴地址和密钥，快速创建渠道' : '连接信息、模型映射与渠道策略' }}
          </div>
        </div>
        <div class="d-flex align-center ga-2 header-actions">
          <v-btn v-if="!isEditing && !isCopying && isPresetProtocol(channelType)" variant="tonal" size="small" :disabled="saving" :aria-expanded="editor.providerDrawerOpen.value" @click="editor.providerDrawerOpen.value = true">
            常见渠道商
          </v-btn>
          <!-- 模式切换按钮（仅在添加模式显示） -->
          <v-btn v-if="!isEditing && !isCopying" variant="outlined" size="small" class="mode-toggle-btn" :disabled="saving" @click="toggleMode">
            <v-icon start size="16">{{ isQuickMode ? 'mdi-form-textbox' : 'mdi-lightning-bolt' }}</v-icon>
            {{ isQuickMode ? '详细配置' : '快速添加' }}
          </v-btn>
          <v-btn
            icon
            size="small"
            variant="text"
            class="header-action-btn"
            title="关闭"
            aria-label="关闭渠道配置"
            :disabled="saving"
            @click="handleCancel"
          >
            <v-icon size="18">mdi-close</v-icon>
          </v-btn>
        </div>
      </v-card-title>

      <v-card-text class="dialog-card-body">
        <fieldset :disabled="saving" :inert="saving" class="agent-config-editor">
          <template v-if="isCopying">
            <p class="text-body-2 text-medium-emphasis mb-4">从 {{ copySourceLabel }} 的“{{ copySource?.name }}”创建新渠道，可修改连接信息、分组与模型配置。</p>
            <v-select
              :model-value="channelType"
              label="目标协议"
              :items="protocolOptions"
              variant="outlined"
              density="comfortable"
              class="mb-4"
              @update:model-value="selectTargetProtocol"
            />
            <v-alert v-if="channelType !== (copySourceType ?? channelType)" type="info" variant="tonal" class="mb-4">
              请确认供应商支持所选上游服务类型和模型。跨协议复制已重新初始化专属策略，并移除转发到原协议其他渠道的模型映射。
            </v-alert>
          </template>
          <!-- 两种模式共用同一个草稿与表单引用 -->
          <ChannelQuickForm v-if="!isEditing && !isCopying && isQuickMode" :editor="editor" />
          <ChannelDetailedForm v-else :editor="editor" />
        </fieldset>
      </v-card-text>

      <v-card-actions class="dialog-card-actions">
        <v-spacer />
        <v-btn variant="text" :disabled="saving" @click="handleCancel"> 取消 </v-btn>
        <v-btn
          v-if="!isEditing && !isCopying && isQuickMode"
          color="primary"
          variant="elevated"
          :disabled="!isQuickFormValid || saving"
          :loading="saving"
          prepend-icon="mdi-check"
          @click="handleQuickSubmit"
        >
          创建渠道
        </v-btn>
        <v-btn
          v-else
          color="primary"
          variant="elevated"
          :disabled="saving"
          :loading="saving"
          prepend-icon="mdi-check"
          @click="handleSubmit"
        >
          {{ isEditing ? '更新渠道' : isCopying ? '确认复制' : '创建渠道' }}
        </v-btn>
      </v-card-actions>
      <ChannelProviderDrawer v-model="editor.providerDrawerOpen.value" :channel-type="channelType" @select="editor.applyProviderPreset" />
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
// 渠道弹窗入口：保持公开事件，编排逻辑与两种表单分别维护。
import type { Channel } from '@/api'
import { computed } from 'vue'
import { findNavigation } from '@/router/navigation'
import { API_TABS, API_TAB_LABELS, type ApiTab } from '@/features/channels/services/channel-api'
import { isPresetProtocol } from '@/features/channels/utils/channelPresets'
import { useChannelModal } from '@/features/channels/composables/useChannelModal'
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
const copySourceLabel = computed(() => API_TAB_LABELS[props.copySourceType ?? props.channelType])
const protocolOptions = API_TABS.map(value => ({ title: API_TAB_LABELS[value], value }))
const selectTargetProtocol = (value: ApiTab) => {
  if (!props.saving && API_TABS.includes(value)) emit('update:channelType', value)
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
