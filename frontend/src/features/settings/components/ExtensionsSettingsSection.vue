<template>
  <section aria-labelledby="extensions-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="extensions-settings-title" class="text-lg font-bold">技能与 MCP</h2>
        <div class="text-sm text-muted-foreground mt-1">
          统一管理 Skills 与 MCP 的存放路径。Git 推送仅包含 Skill 资源和启用清单；MCP 配置、备注及翻译备份保留在本机。
        </div>
      </div>
    </div>

    <UiForm ref="extensionsFormRef" @submit.prevent="saveExtensionsSettings">
      <UiInput
        v-model="skillsDir"
        label="项目 Skills / MCP 目录路径"
        placeholder="D:\data\agent-skills"
        hint="填写绝对路径，留空使用工作区下的 skills/；修改后原目录会自动迁移到新位置。"
        persistent-hint
        prepend-inner-icon="mdi-folder-open"
        variant="outlined"
        density="comfortable"
        autocomplete="off"
        spellcheck="false"
        class="mb-4"
      />

      <UiInput
        v-model="skillsRepoUrl"
        label="Git 远程仓库地址（可选）"
        placeholder="git@github.com:yourname/agent-extensions.git 或 https://..."
        hint="用于多端同步项目 Skills 与启用清单。支持 https://、ssh://、file:// 或 git@host:owner/repo.git。"
        persistent-hint
        prepend-inner-icon="mdi-link-variant"
        variant="outlined"
        density="comfortable"
        autocomplete="off"
        spellcheck="false"
        :rules="[skillsRepoUrlRule]"
      />

      <UiAlert type="warning" variant="tonal" density="compact" class="mt-4">
        <strong>安全提示：</strong>该目录同时存放包含环境变量与请求头密钥的 <code>mcp.json</code>。如需配置 Git 远程仓库，请务必使用<strong>私有仓库</strong>。
      </UiAlert>

      <UiAlert v-if="extensionsNotice" :type="extensionsNotice.type" variant="tonal" density="compact" class="mt-4">
        {{ extensionsNotice.message }}
      </UiAlert>

      <div class="settings-actions">
        <UiButton
          type="submit"
          color="primary"
          prepend-icon="mdi-content-save"
          :loading="saving"
          :disabled="!extensionsDirty"
        >
          保存扩展目录设置
        </UiButton>
      </div>
    </UiForm>

    <hr class="border-0 border-t border-border my-6" />

    <div class="flex items-center justify-between flex-wrap gap-2 mb-3">
      <div>
        <div class="text-base font-medium font-bold">Git 仓库同步</div>
        <div class="text-xs text-muted-foreground">
          拉取或克隆后会自动将 <code>skills-manifest.json</code> 与 <code>mcp.json</code> 分发到各本地 Agent。
        </div>
      </div>
      <UiButton
        variant="text"
        size="small"
        prepend-icon="mdi-refresh"
        :loading="gitStatusLoading"
        :disabled="gitActionLoading !== null"
        @click="loadGitStatus"
      >
        刷新状态
      </UiButton>
    </div>

    <UiAlert v-if="extensionsDirty" type="info" variant="tonal" density="compact" class="mb-4">
      当前目录或仓库地址有未保存的修改，请先点击「保存扩展目录设置」后再执行 Git 同步操作。
    </UiAlert>

    <UiAlert v-if="gitStatusError" type="error" variant="tonal" density="compact" class="mb-4">
      {{ gitStatusError }}
    </UiAlert>

    <div v-if="gitStatus" class="ai-gate-panel mx-0">
      <div class="flex flex-wrap items-center gap-2 mb-2">
        <UiBadge
          size="small"
          label
          :color="!gitStatus.gitAvailable ? 'error' : gitStatus.isGitRepo ? 'success' : 'warning'"
        >
          {{ !gitStatus.gitAvailable ? '未检测到 Git 命令' : gitStatus.isGitRepo ? '独立 Git 仓库' : '未初始化 Git' }}
        </UiBadge>
        <UiBadge v-if="gitStatus.isGitRepo && gitStatus.branch" size="small" label prepend-icon="mdi-source-branch">
          {{ gitStatus.branch }}
        </UiBadge>
        <UiBadge v-if="gitStatus.isGitRepo && gitStatus.dirty" size="small" color="warning" label>
          {{ gitStatus.changedFiles?.length || 0 }} 项未提交变更
        </UiBadge>
        <UiBadge v-if="gitStatus.isGitRepo && gitStatus.ahead > 0" size="small" color="info" label>
          领先 {{ gitStatus.ahead }} 提交
        </UiBadge>
        <UiBadge v-if="gitStatus.isGitRepo && gitStatus.behind > 0" size="small" color="info" label>
          落后 {{ gitStatus.behind }} 提交
        </UiBadge>
      </div>

      <div class="text-sm text-muted-foreground">
        当前生效路径：<code>{{ gitStatus.skillsDir }}</code>
      </div>
      <div v-if="gitStatus.remoteUrl" class="text-sm text-muted-foreground mt-1">
        远程仓库（origin）：<code>{{ gitStatus.remoteUrl }}</code>
      </div>

      <div v-if="gitStatus.dirty && gitStatus.changedFiles?.length" class="text-xs text-muted-foreground mt-2">
        变更预览：{{ gitStatus.changedFiles.slice(0, 6).join('、') }}
        <span v-if="gitStatus.changedFiles.length > 6">等共 {{ gitStatus.changedFiles.length }} 项</span>
      </div>
      <UiAlert v-if="gitStatus.exportError" type="error" class="mt-2">{{ gitStatus.exportError }}</UiAlert>
      <UiAlert v-if="gitStatus.migration && gitStatus.migration.stage !== 'complete'" type="warning" class="mt-2">
        目录迁移阶段：{{ gitStatus.migration.stage }}。完整目标：{{ gitStatus.migration.target }}；原目录：{{ gitStatus.migration.source }}。记录：{{ gitStatus.migration.recordPath }}
      </UiAlert>
      <details v-if="!gitStatus.exportError && gitStatus.exportRevision" class="mt-2 text-xs">
        <summary>推送文件清单（{{ gitStatus.exportFiles?.length || 0 }} 项）</summary>
        <p>仅导出 Skill 资源及启用清单，MCP 配置、本机状态和翻译备份保留在本地。</p>
        <ul><li v-for="path in gitStatus.exportFiles" :key="path">{{ path }}</li></ul>
      </details>

      <div class="flex flex-wrap items-center gap-2 mt-4">
        <UiButton
          v-if="!gitStatus.isGitRepo"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-source-branch"
          :loading="gitActionLoading === 'init'"
          :disabled="!gitStatus.gitAvailable || extensionsDirty || gitActionLoading !== null"
          @click="initGitRepo"
        >
          初始化仓库
        </UiButton>

        <UiButton
          v-if="!gitStatus.isGitRepo"
          variant="tonal"
          color="secondary"
          prepend-icon="mdi-cloud-download-outline"
          :loading="gitActionLoading === 'clone'"
          :disabled="!gitStatus.gitAvailable || extensionsDirty || !savedSkillsRepoUrl || gitActionLoading !== null"
          @click="cloneGitRepo"
        >
          从远程克隆
        </UiButton>

        <template v-if="gitStatus.isGitRepo">
          <UiButton
            variant="tonal"
            color="primary"
            prepend-icon="mdi-cloud-download-outline"
            :loading="gitActionLoading === 'pull'"
            :disabled="!gitStatus.gitAvailable || extensionsDirty || gitStatus.dirty || (!gitStatus.remoteUrl && !savedSkillsRepoUrl) || gitActionLoading !== null"
            @click="pullGitRepo"
          >
            拉取并同步到 Agent
          </UiButton>

          <UiButton
            color="primary"
            prepend-icon="mdi-cloud-upload-outline"
            :loading="gitActionLoading === 'push'"
            :disabled="!gitStatus.gitAvailable || !gitStatus.exportRevision || !!gitStatus.exportError || extensionsDirty || (!gitStatus.remoteUrl && !savedSkillsRepoUrl) || gitActionLoading !== null"
            @click="pushGitRepo"
          >
            提交并推送
          </UiButton>
        </template>
      </div>

      <UiInput
        v-if="gitStatus.isGitRepo"
        v-model="commitMessage"
        label="提交说明（可选）"
        placeholder="chore: sync skills and mcp config"
        variant="outlined"
        density="compact"
        hide-details
        class="mt-3"
      />
    </div>

    <UiAlert v-if="gitNotice" :type="gitNotice.type" variant="tonal" density="compact" class="mt-4">
      {{ gitNotice.message }}
    </UiAlert>
    <p class="mt-4 text-xs leading-6 text-muted-foreground">
      远程领先时先拉取。本地有修改时先本地提交；历史分叉时在仓库目录执行 <code>git fetch origin</code> 和 <code>git merge '@{upstream}'</code>。
      有冲突就编辑冲突文件、移除冲突标记并检查格式，再 <code>git add &lt;文件&gt;</code>、<code>git commit</code> 后推送。
      界面只支持快进拉取与普通推送；推送前会重新获取远程状态，远程领先时本次不会自动提交本地修改。
    </p>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import type { useExtensionsSettings } from '@/features/settings/composables/useExtensionsSettings'

const props = defineProps<{ editor: ReturnType<typeof useExtensionsSettings> }>()
const {
  extensionsFormRef,
  skillsDir,
  skillsRepoUrl,
  savedSkillsRepoUrl,
  extensionsDirty,
  extensionsNotice,
  skillsRepoUrlRule,
  saveExtensionsSettings,
  gitStatus,
  gitStatusLoading,
  gitStatusError,
  gitActionLoading,
  gitNotice,
  commitMessage,
  loadGitStatus,
  initGitRepo,
  cloneGitRepo,
  pullGitRepo,
  pushGitRepo,
  saving
} = props.editor
</script>

<style scoped src="@/features/settings/styles/settings.css"></style>
