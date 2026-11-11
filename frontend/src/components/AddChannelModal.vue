<template>
  <v-dialog :model-value="show" max-width="800" persistent @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="dialog-card-shell">
      <v-card-title class="d-flex align-center ga-3 dialog-card-header">
        <v-avatar color="primary" variant="tonal" size="40" class="dialog-header-avatar">
          <v-icon size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</v-icon>
        </v-avatar>
        <div class="flex-grow-1">
          <div class="text-h6 font-weight-bold">
            {{ isEditing ? '编辑渠道' : '添加新渠道' }}
          </div>
          <div class="text-body-2 text-medium-emphasis dialog-card-subtitle">
            {{ entryLabel }} 入口 · {{ isQuickMode && !isEditing ? '粘贴地址和密钥，快速创建渠道' : '连接信息、模型映射与渠道策略' }}
          </div>
        </div>
        <div class="d-flex align-center ga-2 header-actions">
          <v-btn v-if="!isEditing && ['messages', 'responses', 'chat', 'gemini'].includes(channelType)" variant="tonal" size="small" :disabled="saving" :aria-expanded="editor.providerDrawerOpen.value" @click="editor.providerDrawerOpen.value = true">
            常见渠道商
          </v-btn>
          <!-- 模式切换按钮（仅在添加模式显示） -->
          <v-btn v-if="!isEditing" variant="outlined" size="small" class="mode-toggle-btn" :disabled="saving" @click="toggleMode">
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
          <!-- 两种模式共用同一个草稿与表单引用 -->
          <ChannelQuickForm v-if="!isEditing && isQuickMode" :editor="editor" />
          <ChannelDetailedForm v-else :editor="editor" />
        </fieldset>
      </v-card-text>

      <v-card-actions class="dialog-card-actions">
        <v-spacer />
        <v-btn variant="text" :disabled="saving" @click="handleCancel"> 取消 </v-btn>
        <v-btn
          v-if="!isEditing && isQuickMode"
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
          {{ isEditing ? '更新渠道' : '创建渠道' }}
        </v-btn>
      </v-card-actions>
      <ChannelProviderDrawer v-model="editor.providerDrawerOpen.value" :channel-type="channelType" @select="editor.applyProviderPreset" />
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
// 渠道弹窗入口：保持公开事件，编排逻辑与两种表单分别维护。
import type { Channel } from '../services/api'
import { computed } from 'vue'
import { findNavigation } from '@/router/navigation'
import type { ApiTab } from '../services/channel-api'
import { useChannelModal } from '@/composables/useChannelModal'
import ChannelQuickForm from './channel/ChannelQuickForm.vue'
import ChannelDetailedForm from './channel/ChannelDetailedForm.vue'
import ChannelProviderDrawer from './channel/ChannelProviderDrawer.vue'

interface Props {
  show: boolean
  saving?: boolean
  channel?: Channel | null
  channelType?: ApiTab
  initialPoolId?: string
}

const props = withDefaults(defineProps<Props>(), {
  channelType: 'messages',
  initialPoolId: 'default'
})

const emit = defineEmits<{
  'update:show': [value: boolean]
  save: [channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }]
}>()

const editor = useChannelModal(props, emit)
const entryLabel = computed(() => findNavigation(`/channels/${props.channelType}`)?.label)
const {
  isEditing, isQuickMode, toggleMode,
  isQuickFormValid, handleQuickSubmit, handleSubmit, handleCancel,
  // 保留入口组件原有的草稿引用，方便现有调用与特性测试观察同一份编辑状态。
  form, formRef, autoCompleteUrl, quickInput, parseQuickInput,
  detectedBaseUrl, detectedBaseUrls, detectedApiKeys, imagesExtraParamsError, imagesOutputError
} = editor
</script>

<style scoped src="../styles/channel-modal.css"></style>
