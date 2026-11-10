<template>
  <section aria-labelledby="prompts-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="prompts-settings-title" class="text-h6 font-weight-bold">通用提示词</h2>
        <!-- 集中维护带版本号的全局提示词，可一键写入各智能体客户端的提示词文件 -->
      </div>
      <v-chip v-if="promptActiveVersion" size="small" color="success" label>
        <v-icon start size="12">mdi-check-circle</v-icon>
        生效版本：{{ promptActiveVersion }}
      </v-chip>
    </div>

    <v-alert v-if="promptNotice" :type="promptNotice.type" variant="tonal" density="compact" class="mb-5">
      {{ promptNotice.message }}
    </v-alert>

    <div class="prompt-version-bar d-flex flex-wrap align-center ga-3 mb-5">
      <v-select
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
      <v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-plus" @click="addPromptVersion">
        新建版本
      </v-btn>
      <v-btn
        size="small"
        variant="tonal"
        color="success"
        prepend-icon="mdi-check-circle"
        :disabled="!selectedPromptEntry || promptActiveVersion === selectedPromptEntry.version"
        @click="setActivePromptVersion"
      >
        设为生效版本
      </v-btn>
      <v-btn
        size="small"
        variant="text"
        color="error"
        prepend-icon="mdi-delete"
        :disabled="!selectedPromptEntry"
        @click="removePromptVersion"
      >
        删除版本
      </v-btn>
    </div>

    <template v-if="selectedPromptEntry">
      <v-row>
        <v-col cols="12" sm="4">
          <v-text-field
            v-model.trim="selectedPromptEntry.version"
            label="版本号"
            variant="outlined"
            density="comfortable"
            placeholder="例如 v1、2026-09"
            hide-details
          />
        </v-col>
        <v-col cols="12" sm="8">
          <v-text-field
            v-model.trim="selectedPromptEntry.note"
            label="备注（可选）"
            variant="outlined"
            density="comfortable"
            placeholder="该版本的说明"
            hide-details
          />
        </v-col>
      </v-row>
      <v-textarea
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
      <div class="text-caption text-medium-emphasis mt-1">
        更新时间：{{ formatPromptTime(selectedPromptEntry.updatedAt) }}（保存后刷新）
      </div>
    </template>

    <div v-else class="text-body-2 text-medium-emphasis py-8 text-center">
      尚未创建任何版本，点击"新建版本"开始维护通用提示词。
    </div>

    <div class="settings-actions">
      <v-btn
        color="primary"
        prepend-icon="mdi-content-save"
        :loading="saving"
        :disabled="!promptsDirty"
        @click="savePromptSettings"
      >
        保存通用提示词
      </v-btn>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { usePromptSettings } from '@/composables/settings/usePromptSettings'

const props = defineProps<{ editor: ReturnType<typeof usePromptSettings> }>()
const {
  promptEntries,
  promptActiveVersion,
  selectedPromptEntry,
  promptNotice,
  promptEntryTitle,
  promptsDirty,
  formatPromptTime,
  addPromptVersion,
  removePromptVersion,
  setActivePromptVersion,
  savePromptSettings,
  saving
} = props.editor
</script>

<style scoped src="../../styles/settings.css"></style>
