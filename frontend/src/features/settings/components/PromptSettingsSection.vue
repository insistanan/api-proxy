<template>
  <section aria-labelledby="prompts-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="prompts-settings-title" class="text-lg font-bold">通用提示词</h2>
        <!-- 集中维护带版本号的全局提示词，可一键写入各智能体客户端的提示词文件 -->
      </div>
      <UiBadge v-if="promptActiveVersion" size="small" color="success" label>
        <UiIcon start size="12">mdi-check-circle</UiIcon>
        生效版本：{{ promptActiveVersion }}
      </UiBadge>
    </div>

    <UiAlert v-if="promptNotice" :type="promptNotice.type" variant="tonal" density="compact" class="mb-5">
      {{ promptNotice.message }}
    </UiAlert>

    <div class="prompt-version-bar flex flex-wrap items-center gap-3 mb-5">
      <UiSelect
        v-model="selectedPromptEntry"
        :items="promptEntries"
        :item-title="promptEntryTitle"
        return-object
        label="版本"
        variant="outlined"
        density="comfortable"
        hide-details
        max-width="280px"
        class="prompt-version-select"
      />
      <UiButton size="small" variant="tonal" color="primary" prepend-icon="mdi-plus" @click="addPromptVersion">
        新建版本
      </UiButton>
      <UiButton
        size="small"
        variant="tonal"
        color="success"
        prepend-icon="mdi-check-circle"
        :disabled="!selectedPromptEntry || promptActiveVersion === selectedPromptEntry.version"
        @click="setActivePromptVersion"
      >
        设为生效版本
      </UiButton>
      <UiButton
        size="small"
        variant="text"
        color="error"
        prepend-icon="mdi-delete"
        :disabled="!selectedPromptEntry"
        @click="askRemovePromptVersion"
      >
        删除版本
      </UiButton>
    </div>

    <template v-if="selectedPromptEntry">
      <div class="grid grid-cols-12 gap-4">
        <div class="min-w-0 col-span-12 sm:col-span-4"  >
          <UiInput
            v-model.trim="selectedPromptEntry.version"
            label="版本号"
            variant="outlined"
            density="comfortable"
            placeholder="例如 v1、2026-09"
            hide-details
          />
        </div>
        <div class="min-w-0 col-span-12 sm:col-span-8"  >
          <UiInput
            v-model.trim="selectedPromptEntry.note"
            label="备注（可选）"
            variant="outlined"
            density="comfortable"
            placeholder="该版本的说明"
            hide-details
          />
        </div>
      </div>
      <UiTextarea
        v-model="selectedPromptEntry.content"
        label="提示词内容"
        variant="outlined"
        density="comfortable"
        rows="14"
        auto-grow
        max-rows="28"
        class="mt-4 prompt-content-editor"
        spellcheck="false"
      />
      <div class="text-xs text-muted-foreground mt-1">
        更新时间：{{ formatPromptTime(selectedPromptEntry.updatedAt) }}（保存后刷新）
      </div>
    </template>

    <EmptyState
      v-else
      variant="panel"
      icon="mdi-file-cog-outline"
      title="尚未创建任何版本"
      description="点击“新建版本”开始维护通用提示词，保存后会同步给各客户端。"
    />

    <div class="settings-actions">
      <UiButton
        color="primary"
        prepend-icon="mdi-content-save"
        :loading="saving"
        :disabled="!promptsDirty"
        @click="savePromptSettings"
      >
        保存通用提示词
      </UiButton>
    </div>
  </section>
  <ConfirmDialog
    :model-value="pendingConfirm !== null"
    :message="pendingConfirm?.message ?? ''"
    :title="pendingConfirm?.title"
    :confirm-text="pendingConfirm?.confirmText"
    :cancel-text="pendingConfirm?.cancelText"
    :confirm-color="pendingConfirm?.confirmColor"
    @update:model-value="cancelConfirm"
    @confirm="acceptConfirm"
  />
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import type { usePromptSettings } from '@/features/settings/composables/usePromptSettings'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { useConfirm } from '@/shared/composables/useConfirm'

const props = defineProps<{ editor: ReturnType<typeof usePromptSettings> }>()
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()
const {
  promptEntries,
  promptActiveVersion,
  selectedPromptEntry,
  promptNotice,
  promptEntryTitle,
  promptsDirty,
  formatPromptTime,
  addPromptVersion,
  setActivePromptVersion,
  savePromptSettings,
  saving
} = props.editor

// 删除版本属于不可恢复操作，确认弹窗由组件统一渲染，composable 只负责状态变更。
const askRemovePromptVersion = () => {
  const entry = selectedPromptEntry.value
  if (!entry) return
  askConfirm({
    message: `删除版本「${entry.version || '（未命名）'}」？删除后不可恢复。`,
    confirmText: '删除',
    action: () => props.editor.removePromptVersion()
  })
}
</script>

<style scoped src="@/features/settings/styles/settings.css"></style>
