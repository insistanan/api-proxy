<template>
  <section aria-labelledby="extensions-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="extensions-settings-title" class="text-h6 font-weight-bold">扩展与同步</h2>
        <div class="text-body-2 text-medium-emphasis mt-1">
          统一管理项目 Skills、启用清单 <code>skills-manifest.json</code> 与集中式 <code>mcp.json</code> 的存放路径及 Git 同步。
        </div>
      </div>
    </div>

    <v-form ref="extensionsFormRef" @submit.prevent="saveExtensionsSettings">
      <v-text-field
        v-model="skillsDir"
        label="项目 Skills / MCP 目录路径"
        placeholder="skills"
        hint="留空默认使用工作区下的 skills/；支持相对路径、绝对路径或 ~/ 路径。修改路径后会自动把原目录（含 .git、mcp.json 与翻译备份）安全迁移到新位置。"
        persistent-hint
        prepend-inner-icon="mdi-folder-open"
        variant="outlined"
        density="comfortable"
        autocomplete="off"
        spellcheck="false"
        class="mb-4"
      />

      <v-text-field
        v-model="skillsRepoUrl"
        label="Git 远程仓库地址（可选）"
        placeholder="git@github.com:yourname/agent-extensions.git 或 https://..."
        hint="用于多端同步项目 Skills、启用清单与 MCP 配置。支持 https://、ssh://、file:// 或 git@host:owner/repo.git。"
        persistent-hint
        prepend-inner-icon="mdi-link-variant"
        variant="outlined"
        density="comfortable"
        autocomplete="off"
        spellcheck="false"
        :rules="[skillsRepoUrlRule]"
      />

      <v-alert type="warning" variant="tonal" density="compact" class="mt-4">
        <strong>安全提示：</strong>该目录同时存放包含环境变量与请求头密钥的 <code>mcp.json</code>。如需配置 Git 远程仓库，请务必使用<strong>私有仓库</strong>。
      </v-alert>

      <v-alert v-if="extensionsNotice" :type="extensionsNotice.type" variant="tonal" density="compact" class="mt-4">
        {{ extensionsNotice.message }}
      </v-alert>

      <div class="settings-actions">
        <v-btn
          type="submit"
          color="primary"
          prepend-icon="mdi-content-save"
          :loading="saving"
          :disabled="!extensionsDirty"
        >
          保存扩展目录设置
        </v-btn>
      </div>
    </v-form>

    <v-divider class="my-6" />

    <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-3">
      <div>
        <div class="text-subtitle-1 font-weight-bold">Git 仓库同步</div>
        <div class="text-caption text-medium-emphasis">
          拉取或克隆后会自动将 <code>skills-manifest.json</code> 与 <code>mcp.json</code> 分发到各本地 Agent。
        </div>
      </div>
      <v-btn
        variant="text"
        size="small"
        prepend-icon="mdi-refresh"
        :loading="gitStatusLoading"
        :disabled="gitActionLoading !== null"
        @click="loadGitStatus"
      >
        刷新状态
      </v-btn>
    </div>

    <v-alert v-if="extensionsDirty" type="info" variant="tonal" density="compact" class="mb-4">
      当前目录或仓库地址有未保存的修改，请先点击「保存扩展目录设置」后再执行 Git 同步操作。
    </v-alert>

    <v-alert v-if="gitStatusError" type="error" variant="tonal" density="compact" class="mb-4">
      {{ gitStatusError }}
    </v-alert>

    <div v-if="gitStatus" class="ai-gate-panel mx-0">
      <div class="d-flex flex-wrap align-center ga-2 mb-2">
        <v-chip
          size="small"
          label
          :color="!gitStatus.gitAvailable ? 'error' : gitStatus.isGitRepo ? 'success' : 'warning'"
        >
          {{ !gitStatus.gitAvailable ? '未检测到 Git 命令' : gitStatus.isGitRepo ? '独立 Git 仓库' : '未初始化 Git' }}
        </v-chip>
        <v-chip v-if="gitStatus.isGitRepo && gitStatus.branch" size="small" label prepend-icon="mdi-source-branch">
          {{ gitStatus.branch }}
        </v-chip>
        <v-chip v-if="gitStatus.isGitRepo && gitStatus.dirty" size="small" color="warning" label>
          {{ gitStatus.changedFiles?.length || 0 }} 项未提交变更
        </v-chip>
        <v-chip v-if="gitStatus.isGitRepo && gitStatus.ahead > 0" size="small" color="info" label>
          领先 {{ gitStatus.ahead }} 提交
        </v-chip>
        <v-chip v-if="gitStatus.isGitRepo && gitStatus.behind > 0" size="small" color="info" label>
          落后 {{ gitStatus.behind }} 提交
        </v-chip>
      </div>

      <div class="text-body-2 text-medium-emphasis">
        当前生效路径：<code>{{ gitStatus.skillsDir }}</code>
      </div>
      <div v-if="gitStatus.remoteUrl" class="text-body-2 text-medium-emphasis mt-1">
        Remote (origin)：<code>{{ gitStatus.remoteUrl }}</code>
      </div>

      <div v-if="gitStatus.dirty && gitStatus.changedFiles?.length" class="text-caption text-medium-emphasis mt-2">
        变更预览：{{ gitStatus.changedFiles.slice(0, 6).join('、') }}
        <span v-if="gitStatus.changedFiles.length > 6">等共 {{ gitStatus.changedFiles.length }} 项</span>
      </div>

      <div class="d-flex flex-wrap align-center ga-2 mt-4">
        <v-btn
          v-if="!gitStatus.isGitRepo"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-source-branch"
          :loading="gitActionLoading === 'init'"
          :disabled="!gitStatus.gitAvailable || extensionsDirty || gitActionLoading !== null"
          @click="initGitRepo"
        >
          初始化仓库
        </v-btn>

        <v-btn
          v-if="!gitStatus.isGitRepo"
          variant="tonal"
          color="secondary"
          prepend-icon="mdi-cloud-download-outline"
          :loading="gitActionLoading === 'clone'"
          :disabled="!gitStatus.gitAvailable || extensionsDirty || !savedSkillsRepoUrl || gitActionLoading !== null"
          @click="cloneGitRepo"
        >
          从远程克隆
        </v-btn>

        <template v-if="gitStatus.isGitRepo">
          <v-btn
            variant="tonal"
            color="primary"
            prepend-icon="mdi-cloud-download-outline"
            :loading="gitActionLoading === 'pull'"
            :disabled="!gitStatus.gitAvailable || extensionsDirty || gitStatus.dirty || (!gitStatus.remoteUrl && !savedSkillsRepoUrl) || gitActionLoading !== null"
            @click="pullGitRepo"
          >
            拉取并同步到 Agent
          </v-btn>

          <v-btn
            color="primary"
            prepend-icon="mdi-cloud-upload-outline"
            :loading="gitActionLoading === 'push'"
            :disabled="!gitStatus.gitAvailable || extensionsDirty || (!gitStatus.remoteUrl && !savedSkillsRepoUrl) || gitActionLoading !== null"
            @click="pushGitRepo"
          >
            提交并推送
          </v-btn>
        </template>
      </div>

      <v-text-field
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

    <v-alert v-if="gitNotice" :type="gitNotice.type" variant="tonal" density="compact" class="mt-4">
      {{ gitNotice.message }}
    </v-alert>
  </section>
</template>

<script setup lang="ts">
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
