<template>
  <v-card elevation="0" class="settings-card agent-config-panel agent-prompt-card">
    <div class="agent-config-panel-header">
      <div>
        <div class="agent-config-panel-title d-flex align-center ga-2">
          <v-icon size="18">mdi-text</v-icon>
          全局提示词 · {{ info?.displayName || agent }}
          <v-chip v-if="statusChip" :color="statusChip.color" size="x-small" label>
            <v-icon start size="12">{{ statusChip.icon }}</v-icon>
            {{ statusChip.text }}
          </v-chip>
        </div>
        <div class="agent-config-panel-subtitle">
          {{ subtitle }}
        </div>
      </div>
      <div class="agent-config-panel-actions d-flex ga-1">
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn>
        <v-btn
          v-if="!editing"
          variant="text"
          prepend-icon="mdi-pencil"
          :disabled="!info || !info.writable"
          @click="startEdit"
        >
          编辑
        </v-btn>
      </div>
    </div>
    <v-divider />
    <v-card-text class="pa-5">
      <v-alert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-4">{{ loadError }}</v-alert>

      <div class="prompt-file-path mb-4">
        <code>{{ info?.path || '路径不可用' }}</code>
        <span v-if="info" class="text-caption text-medium-emphasis ml-2">
          {{ info.exists ? `${formatSize(info.sizeBytes)} · 更新于 ${formatTime(info.updatedAt)}` : '尚未创建，保存后将创建该文件' }}
        </span>
      </div>

      <template v-if="editing">
        <v-textarea
          v-model="draft"
          variant="outlined"
          density="comfortable"
          auto-grow
          rows="10"
          max-rows="24"
          hide-details
          class="prompt-editor mb-3"
          spellcheck="false"
        />
        <div v-if="saveError" class="text-caption text-error mb-3">
          {{ saveError }}
          <div v-if="saveDetail" class="text-medium-emphasis">{{ saveDetail }}</div>
        </div>
        <div class="d-flex ga-2">
          <v-btn color="primary" prepend-icon="mdi-content-save" :loading="saving" @click="save">保存提示词</v-btn>
          <v-btn variant="text" :disabled="saving" @click="cancelEdit">取消</v-btn>
        </div>
      </template>

      <template v-else>
        <div v-if="info?.readError" class="text-caption text-error mb-3">{{ info.readError }}</div>
        <pre v-else class="prompt-preview">{{ info?.content || '（提示词文件为空或尚未创建）' }}</pre>
      </template>

      <template v-if="!editing">
        <v-divider class="my-5" />

        <!-- 一键配置提示词：把设置页维护的通用提示词版本写入该文件 -->
        <div class="d-flex align-center ga-2 mb-2">
          <v-icon size="18">mdi-lightning-bolt</v-icon>
          <span class="text-subtitle-2 font-weight-bold">一键配置提示词</span>
        </div>
        <div v-if="promptEntries.length === 0" class="text-body-2 text-medium-emphasis">
          尚未维护通用提示词版本，请先前往 <router-link to="/settings">设置 · 通用提示词</router-link> 创建。
        </div>
        <template v-else>
          <div class="d-flex flex-wrap align-center ga-3">
            <v-select
              v-model="selectedVersion"
              :items="promptVersionItems"
              item-title="title"
              item-value="value"
              label="通用提示词版本"
              variant="outlined"
              density="comfortable"
              max-width="320px"
              hide-details
              class="prompt-version-select"
            />
            <v-btn
              color="primary"
              variant="tonal"
              prepend-icon="mdi-download"
              :loading="applying"
              :disabled="!selectedVersion || !!info?.readError"
              @click="applyPromptVersion"
            >
              写入该版本
            </v-btn>
          </div>
          <div class="text-caption text-medium-emphasis mt-2">
            写入会整体替换当前提示词文件（原文件自动保存 .bak 备份）{{ activeVersionHint }}
          </div>
          <div v-if="applyError" class="text-caption text-error mt-2">
            {{ applyError }}
            <div v-if="applyDetail" class="text-medium-emphasis">{{ applyDetail }}</div>
          </div>
        </template>
      </template>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
// 智能体客户端配置页"全局提示词"卡片：
// 读取并展示各智能体（Claude Code / Codex / OpenCode / pi / DSH）的全局提示词文件
// （如 Claude Code 的 ~/.claude/CLAUDE.md、其余各家约定的 AGENTS.md），
// 支持就地编辑保存（写入失败如只读 / 权限不足时显式展示错误详情），
// 以及把设置页维护的通用提示词版本一键写入该文件。
// 状态自管（同 ToolInstallBanner 的先例），不进 store——数据只服务于本卡片。
import { computed, onMounted, ref } from 'vue'
import { api, ApiError, type AgentPromptAgent, type AgentPromptFile, type PromptEntry } from '@/services/api'

const props = defineProps<{
  /** 智能体标识，须在后端白名单内：claude-code / codex / opencode / pi / dsh */
  agent: AgentPromptAgent
}>()

const loading = ref(false)
const saving = ref(false)
const info = ref<AgentPromptFile | null>(null)
const loadError = ref('')
const saveError = ref('')
const saveDetail = ref('')

const editing = ref(false)
const draft = ref('')

// 一键配置提示词：把设置页维护的通用提示词版本写入该文件
// activeVersion 是设置页标记的生效版本号（展示提示用）
const promptEntries = ref<PromptEntry[]>([])
const activeVersion = ref('')
const selectedVersion = ref('')
const applying = ref(false)
const applyError = ref('')
const applyDetail = ref('')

const statusChip = computed(() => {
  if (!info.value) return null
  if (info.value.readError) return { color: 'error', icon: 'mdi-alert-circle-outline', text: '读取失败' }
  if (!info.value.writable) return { color: 'error', icon: 'mdi-alert-circle-outline', text: '不可写' }
  if (!info.value.exists) return { color: 'warning', icon: 'mdi-plus', text: '未创建' }
  return { color: 'success', icon: 'mdi-check-circle', text: '已生效' }
})

const subtitle = computed(() => {
  if (loading.value) return '正在读取…'
  if (!info.value) return '该智能体的全局提示词文件'
  if (!info.value.writable) return '文件不可写（可能只读或无写入权限），只能查看'
  return '查看与编辑该智能体的全局提示词文件'
})

const promptVersionItems = computed(() =>
  promptEntries.value.map(entry => ({
    title: entry.version + (entry.note ? ` · ${entry.note}` : ''),
    value: entry.version
  }))
)

// 生效版本提示：展示设置页标记的生效版本，帮助发现误选旧版本
const activeVersionHint = computed(() => {
  const active = promptEntries.value.find(entry => entry.version === activeVersion.value)
  return active ? `；当前生效版本为「${active.version}」` : ''
})

const formatSize = (bytes: number): string => {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

const formatTime = (millis: number): string => {
  if (!millis) return '未知'
  return new Date(millis).toLocaleString('zh-CN', { hour12: false })
}

/** 从 ApiError 提取后端 detail 字段（保存失败的具体原因，如权限不足） */
const extractDetail = (error: unknown): string => {
  if (error instanceof ApiError) {
    const details = error.details as { detail?: unknown } | undefined
    if (details && typeof details.detail === 'string') return details.detail
  }
  return ''
}

const load = async () => {
  loading.value = true
  loadError.value = ''
  try {
    info.value = await api.getAgentPrompt(props.agent)
    if (editing.value && !info.value.writable) {
      // 文件在编辑期间变为不可写：退出编辑避免白写
      editing.value = false
    }
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '读取全局提示词失败'
  } finally {
    loading.value = false
  }
}

// 通用提示词版本库懒加载：与提示词文件并行拉取，失败不阻断卡片（一键写入区就地提示）
const loadPromptVersions = async () => {
  try {
    const settings = await api.getSettings()
    const prompts = settings.prompts
    promptEntries.value = prompts?.entries ?? []
    // 默认选中当前生效版本；未指定生效版本时退化为第一个版本
    activeVersion.value = prompts?.activeVersion || ''
    selectedVersion.value = activeVersion.value || promptEntries.value[0]?.version || ''
  } catch {
    // 版本库加载失败不阻断卡片主功能；一键写入区展示"尚未维护"提示
    promptEntries.value = []
    activeVersion.value = ''
  }
}

const startEdit = () => {
  // 未加载完成或不可写时不进入编辑，避免把未知内容当作空草稿保存覆盖原文件
  if (!info.value || !info.value.writable) return
  draft.value = info.value.content
  saveError.value = ''
  saveDetail.value = ''
  editing.value = true
}

const cancelEdit = () => {
  editing.value = false
  saveError.value = ''
  saveDetail.value = ''
}

const save = async () => {
  saving.value = true
  saveError.value = ''
  saveDetail.value = ''
  try {
    await api.saveAgentPrompt(props.agent, draft.value)
    editing.value = false
    await load()
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : '保存全局提示词失败'
    saveDetail.value = extractDetail(error)
  } finally {
    saving.value = false
  }
}

const applyPromptVersion = async () => {
  const entry = promptEntries.value.find(item => item.version === selectedVersion.value)
  if (!entry) return
  if (info.value?.exists && info.value.content) {
    const confirmed = window.confirm(
      `将用通用提示词「${entry.version}」整体替换当前提示词文件，原内容会自动保存 .bak 备份。继续？`
    )
    if (!confirmed) return
  }
  applying.value = true
  applyError.value = ''
  applyDetail.value = ''
  try {
    await api.saveAgentPrompt(props.agent, entry.content)
    await load()
  } catch (error) {
    applyError.value = error instanceof Error ? error.message : '写入通用提示词失败'
    applyDetail.value = extractDetail(error)
  } finally {
    applying.value = false
  }
}

onMounted(() => {
  void load()
  void loadPromptVersions()
})
</script>

<style scoped>
.prompt-file-path {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
}

.prompt-file-path code {
  overflow-wrap: anywhere;
  font-size: 0.85rem;
}

.prompt-preview {
  max-height: 360px;
  overflow: auto;
  padding: 12px 14px;
  background: rgba(var(--v-theme-surface-variant), 0.45);
  border-radius: 6px;
  font-size: 0.85rem;
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.prompt-editor :deep(textarea) {
  font-family: inherit;
  line-height: 1.6;
}

.prompt-version-select {
  min-width: 240px;
}
</style>
