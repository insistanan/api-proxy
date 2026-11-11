<template>
  <v-dialog :model-value="show" max-width="800" persistent @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="dialog-card-shell">
      <v-card-title class="d-flex align-center ga-3 pa-6" :class="headerClasses">
        <v-avatar :color="avatarColor" variant="flat" size="40">
          <v-icon :style="headerIconStyle" size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</v-icon>
        </v-avatar>
        <div class="flex-grow-1">
          <div class="text-h5 font-weight-bold">
            {{ isEditing ? '编辑渠道' : '添加新渠道' }}
          </div>
          <div class="text-body-2" :class="subtitleClasses">
            {{ isEditing ? '修改渠道配置信息' : isQuickMode ? '快速批量添加 API 密钥' : '配置API渠道信息和密钥' }}
          </div>
        </div>
        <div class="d-flex align-center ga-2 header-actions">
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
            :disabled="saving"
            @click="handleCancel"
          >
            <v-icon size="18">mdi-close</v-icon>
          </v-btn>
        </div>
      </v-card-title>

      <v-card-text class="pa-6 dialog-card-body">
        <fieldset :disabled="saving" :inert="saving" class="agent-config-editor">
          <!-- 两种模式共用同一个草稿与表单引用 -->
          <ChannelQuickForm v-if="!isEditing && isQuickMode" :editor="editor" />
          <ChannelDetailedForm v-else :editor="editor" />
        </fieldset>
      </v-card-text>

      <v-card-actions class="pa-6 dialog-card-actions">
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
          :disabled="!isFormValid || saving"
          :loading="saving"
          prepend-icon="mdi-check"
          @click="handleSubmit"
        >
          {{ isEditing ? '更新渠道' : '创建渠道' }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
// 渠道弹窗入口：保持公开事件，编排逻辑与两种表单分别维护。
import type { Channel } from '../services/api'
import type { ApiTab } from '../services/channel-api'
import { useChannelModal } from '@/composables/useChannelModal'
import ChannelQuickForm from './channel/ChannelQuickForm.vue'
import ChannelDetailedForm from './channel/ChannelDetailedForm.vue'

interface Props {
  show: boolean
  saving?: boolean
  channel?: Channel | null
  channelType?: ApiTab
}

const props = withDefaults(defineProps<Props>(), {
  channelType: 'messages'
})

const emit = defineEmits<{
  'update:show': [value: boolean]
  save: [channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }]
}>()

const editor = useChannelModal(props, emit)
const {
  isEditing, isQuickMode, toggleMode, headerClasses, avatarColor, headerIconStyle,
  subtitleClasses, isQuickFormValid, isFormValid, handleQuickSubmit, handleSubmit, handleCancel,
  // 保留入口组件原有的草稿引用，方便现有调用与特性测试观察同一份编辑状态。
  form, formRef, autoCompleteUrl, quickInput, parseQuickInput,
  detectedBaseUrl, detectedBaseUrls, detectedApiKeys, imagesExtraParamsError, imagesOutputError
} = editor
</script>

<style scoped src="../styles/channel-modal.css"></style>
