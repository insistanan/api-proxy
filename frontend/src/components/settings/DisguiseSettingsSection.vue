<template>
  <section aria-labelledby="disguise-settings-title">
    <div class="section-heading">
      <div>
        <h2 id="disguise-settings-title" class="text-h6 font-weight-bold">客户端伪装</h2>
      </div>
    </div>

    <!-- 全局启用开关：与 Messages / Responses 渠道页顶部按钮读写同一份服务端配置 -->
    <div class="setting-row">
      <div class="setting-copy">
        <div class="text-body-1 font-weight-medium">全局启用</div>
        <div class="text-body-2 text-medium-emphasis">
          与渠道页顶部快捷开关同步。仅对渠道中设置为「继承全局设置」、且实际发往 Claude / Codex 原生接口的请求生效。
        </div>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          :variant="claudeCodeDisguiseEnabled ? 'flat' : 'outlined'"
          :color="claudeCodeDisguiseEnabled ? 'success' : undefined"
          :prepend-icon="claudeCodeDisguiseEnabled ? 'mdi-robot' : 'mdi-robot-outline'"
          :loading="disguiseToggleLoading === 'messages'"
          :disabled="!disguiseInfo || disguiseToggleLoading !== null"
          @click="setClientDisguiseEnabled('messages', !claudeCodeDisguiseEnabled)"
        >
          Claude 伪装
          <span class="ml-2">{{ claudeCodeDisguiseEnabled ? '开' : '关' }}</span>
        </v-btn>
        <v-btn
          :variant="codexDisguiseEnabled ? 'flat' : 'outlined'"
          :color="codexDisguiseEnabled ? 'success' : undefined"
          :prepend-icon="codexDisguiseEnabled ? 'mdi-robot' : 'mdi-robot-outline'"
          :loading="disguiseToggleLoading === 'responses'"
          :disabled="!disguiseInfo || disguiseToggleLoading !== null"
          @click="setClientDisguiseEnabled('responses', !codexDisguiseEnabled)"
        >
          Codex 伪装
          <span class="ml-2">{{ codexDisguiseEnabled ? '开' : '关' }}</span>
        </v-btn>
      </div>
    </div>

    <v-divider class="my-4" />

    <v-form ref="disguiseFormRef" @submit.prevent="saveDisguiseSettings">
      <div class="setting-row">
        <div class="setting-copy">
          <div class="text-body-1 font-weight-medium">自动跟随官方最新版本</div>
          <div class="text-body-2 text-medium-emphasis">开启后，若未指定自定义版本，代理将定时从 npm 获取官方发布的最新版本自动伪装</div>
        </div>
        <v-switch v-model="disguiseAutoUpdate" color="primary" inset hide-details aria-label="自动跟随官方最新版本" />
      </div>

      <v-divider class="my-4" />

      <div class="disguise-section">
        <div class="text-subtitle-1 font-weight-bold mb-2">Claude Code 伪装</div>
        <div class="text-body-2 text-medium-emphasis mb-4">
          当前生效版本：<code>{{ disguiseInfo?.effectiveClaudeVersion ?? '尚未读取' }}</code>
          <span v-if="disguiseInfo?.latestClaudeVersion" class="ml-3">
            (npm 最新: <code>{{ disguiseInfo.latestClaudeVersion }}</code>)
          </span>
        </div>
        <div class="d-flex align-center gap-3">
          <v-text-field
            v-model="disguiseClaudeVersion"
            label="Claude Code 伪装版本号"
            :placeholder="disguiseAutoUpdate ? '留空则自动跟随最新版本' : '留空则使用内置默认版本'"
            prepend-inner-icon="mdi-tag"
            variant="outlined"
            density="comfortable"
            autocomplete="off"
            spellcheck="false"
            :rules="[disguiseVersionRule]"
          />
          <v-btn
            v-if="disguiseInfo?.latestClaudeVersion"
            variant="tonal"
            color="primary"
            @click="disguiseClaudeVersion = disguiseInfo.latestClaudeVersion"
          >
            填入最新版本
          </v-btn>
        </div>
      </div>

      <v-divider class="my-4" />

      <div class="disguise-section">
        <div class="text-subtitle-1 font-weight-bold mb-2">Codex / GPT 伪装</div>
        <div class="text-body-2 text-medium-emphasis mb-4">
          当前生效版本：<code>{{ disguiseInfo?.effectiveCodexVersion ?? '尚未读取' }}</code>
          <span v-if="disguiseInfo?.latestCodexVersion" class="ml-3">
            (npm 最新: <code>{{ disguiseInfo.latestCodexVersion }}</code>)
          </span>
        </div>
        <div class="d-flex align-center gap-3">
          <v-text-field
            v-model="disguiseCodexVersion"
            label="Codex 伪装版本号"
            :placeholder="disguiseAutoUpdate ? '留空则自动跟随最新版本' : '留空则使用内置默认版本'"
            prepend-inner-icon="mdi-tag"
            variant="outlined"
            density="comfortable"
            autocomplete="off"
            spellcheck="false"
            :rules="[disguiseVersionRule]"
          />
          <v-btn
            v-if="disguiseInfo?.latestCodexVersion"
            variant="tonal"
            color="primary"
            @click="disguiseCodexVersion = disguiseInfo.latestCodexVersion"
          >
            填入最新版本
          </v-btn>
        </div>
      </div>

      <div class="mt-4">
        <v-btn variant="tonal" :loading="disguiseChecking || disguiseInfo?.sync.syncing" @click="refreshDisguiseVersions">
          检查最新版本
        </v-btn>
        <div class="text-body-2 text-medium-emphasis mt-2">
          Claude Code 上次查询成功：{{ formatSyncTime(disguiseInfo?.sync.claudeLastSuccessAt) }}
          <br />
          Codex 上次查询成功：{{ formatSyncTime(disguiseInfo?.sync.codexLastSuccessAt) }}
        </div>
      </div>
      <v-alert v-if="disguiseStatusError" type="error" variant="tonal" density="compact" class="mt-4">
        {{ disguiseStatusError }}
      </v-alert>
      <v-alert v-if="disguiseInfo?.sync.claudeError || disguiseInfo?.sync.codexError" type="warning" variant="tonal" density="compact" class="mt-4">
        <div v-if="disguiseInfo?.sync.claudeError">Claude Code：{{ disguiseInfo.sync.claudeError }}</div>
        <div v-if="disguiseInfo?.sync.codexError">Codex：{{ disguiseInfo.sync.codexError }}</div>
        查询失败时保留上次成功结果。
      </v-alert>

      <v-alert v-if="disguiseNotice" :type="disguiseNotice.type" variant="tonal" density="compact" class="mt-5">
        {{ disguiseNotice.message }}
      </v-alert>

      <div class="settings-actions">
        <v-btn
          type="submit"
          color="primary"
          prepend-icon="mdi-content-save"
          :loading="saving"
          :disabled="!disguiseDirty"
        >
          保存伪装设置
        </v-btn>
      </div>
    </v-form>
  </section>
</template>

<script setup lang="ts">
import type { useDisguiseSettings } from '@/composables/settings/useDisguiseSettings'

const props = defineProps<{ editor: ReturnType<typeof useDisguiseSettings> }>()
const {
  disguiseFormRef,
  disguiseClaudeVersion,
  disguiseCodexVersion,
  disguiseAutoUpdate,
  claudeCodeDisguiseEnabled,
  codexDisguiseEnabled,
  disguiseToggleLoading,
  setClientDisguiseEnabled,
  disguiseNotice,
  disguiseInfo,
  disguiseStatusError,
  disguiseChecking,
  disguiseVersionRule,
  formatSyncTime,
  disguiseDirty,
  refreshDisguiseVersions,
  saveDisguiseSettings,
  saving
} = props.editor
</script>

<style scoped src="../../styles/settings.css"></style>
