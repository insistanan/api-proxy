<template>
  <div class="agent-config-page claude-code-page">
    <AgentConfigHeader title="Claude Code 配置" subtitle="管理当前服务运行用户的 Claude Code 代理和模型映射">
      <template #default>
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadSettings">刷新</v-btn>
        <v-btn color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="loading" @click="saveSettings">保存配置</v-btn>
      </template>
    </AgentConfigHeader>

    <v-alert v-if="error" type="error" variant="tonal" class="mb-5" closable @click:close="error = ''">{{ error }}</v-alert>
    <ToolInstallBanner tool="claude" class="mb-5" />
    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-5" />

    <template v-else>
      <AgentConfigLocation
        :label="settings?.exists ? '当前配置文件' : '首次保存将创建配置文件'"
        :path="settings?.path"
      >
        <v-chip v-if="settings?.jsonc" size="x-small" label>JSONC 将在保存后规范化为 JSON</v-chip>
      </AgentConfigLocation>

      <v-row>
        <v-col cols="12">
          <v-card elevation="0" class="settings-card agent-config-panel h-100">
            <div class="agent-config-panel-header">
              <div class="agent-config-panel-title">代理连接</div>
            </div>
            <v-divider />
            <v-card-text class="pa-5">
              <!-- 连接本代理（固定 messages 协议） -->
              <section class="agent-config-callout mb-4 channel-quick-pick">
                <div class="agent-config-callout__title">
                  <v-icon size="18">mdi-lightning-bolt</v-icon>
                  连接本代理
                </div>
                <div>
                  <v-btn color="primary" variant="tonal" prepend-icon="mdi-link-variant" :loading="connecting" @click="connectToProxy">一键连接本代理（{{ defaultBaseUrl || 'http://localhost:...' }}）</v-btn>
                  <div class="text-caption text-medium-emphasis mt-2">
                    Claude Code 固定走 messages 协议，点击后自动填充 Base URL 与访问密钥，模型可一键导入
                  </div>
                </div>
              </section>

              <v-divider class="my-4" />
              <v-text-field v-model.trim="baseUrl" label="Anthropic Base URL" variant="outlined" density="comfortable" placeholder="例如 http://127.0.0.1:8080" hint="留空则使用 Claude Code 默认 Anthropic 端点" persistent-hint />
              <v-radio-group v-model="credentialKind" inline class="mt-2 mb-1" hide-details>
                <v-radio label="认证令牌" value="authToken" />
                <v-radio label="API Key" value="apiKey" />
              </v-radio-group>
              <v-text-field
                v-model="credential"
                :label="credentialKind === 'authToken' ? 'ANTHROPIC_AUTH_TOKEN' : 'ANTHROPIC_API_KEY'"
                type="password"
                variant="outlined"
                density="comfortable"
                :placeholder="credentialPresent ? `当前：${credentialMasked}（留空则保留）` : '输入密钥'"
                @update:model-value="onCredentialInput"
              >
                <template #append-inner>
                  <v-btn v-if="credentialPresent" icon="mdi-delete" size="x-small" variant="text" title="清除已保存密钥" @click="clearCredential" />
                </template>
              </v-text-field>
            </v-card-text>
          </v-card>
        </v-col>
      </v-row>

      <v-card elevation="0" class="settings-card agent-config-panel mt-5">
        <div class="agent-config-panel-header">
          <div>
            <div class="agent-config-panel-title">默认模型标识</div>
            <div class="agent-config-panel-subtitle">为空时不会写入对应环境变量，Claude Code 将使用自身默认模型。每个模型族可独立勾选 1M 上下文。</div>
          </div>
          <div class="agent-config-panel-actions">
            <v-btn size="small" color="secondary" variant="tonal" prepend-icon="mdi-download" :loading="importing" @click="importModelDefaultsFromProxy">导入本代理模型</v-btn>
          </div>
        </div>
        <v-divider />
        <v-card-text class="pa-5">
          <v-row v-for="item in modelDefaults" :key="item.family" class="model-default-row">
            <v-col cols="12" sm="3" class="d-flex align-center pt-sm-5">
              <v-chip :color="familyColor(item.family)" label>{{ familyTitle(item.family) }}</v-chip>
            </v-col>
            <v-col cols="12" sm="5">
              <v-text-field v-model.trim="item.model" label="模型标识" variant="outlined" density="comfortable" :placeholder="`${familyTitle(item.family)} 默认模型`" />
            </v-col>
            <v-col cols="12" sm="3">
              <v-text-field v-model.trim="item.name" label="显示名称" variant="outlined" density="comfortable" placeholder="可选" />
            </v-col>
            <v-col cols="12" sm="1" class="d-flex align-center justify-center pt-sm-5">
              <v-tooltip text="启用 1M 上下文（写入模型标识追加 [1m] 后缀）">
                <template #activator="{ props }">
                  <v-checkbox v-model="item.supports1M" v-bind="props" label="1M" density="compact" hide-details color="primary" />
                </template>
              </v-tooltip>
            </v-col>
          </v-row>
        </v-card-text>
      </v-card>
    </template>

    <AgentPromptCard agent="claude-code" class="mt-5" />

    <v-snackbar v-model="notice.visible" :color="notice.type" location="top right" :timeout="3500">{{ notice.message }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AgentConfigHeader from '@/components/AgentConfigHeader.vue'
import AgentConfigLocation from '@/components/AgentConfigLocation.vue'
import AgentPromptCard from '@/components/AgentPromptCard.vue'
import ToolInstallBanner from '@/components/ToolInstallBanner.vue'
import { api, type ClaudeCodeModelDefault, type ClaudeCodeSettings } from '@/services/api'
import { buildClaudeCodeModelDefaults } from '@/composables/useChannelModelImport'
import { fetchProxyBaseUrl } from '@/composables/useChannelQuickPick'
import { filterProxyModelsByKind } from '@/composables/useChannelModelImport'
import { useAuthStore } from '@/stores/auth'

const loading = ref(false)
const saving = ref(false)
const error = ref('')
const settings = ref<ClaudeCodeSettings | null>(null)
const baseUrl = ref('')
const credentialKind = ref<'authToken' | 'apiKey'>('authToken')
const credential = ref('')
const credentialMasked = ref('')
const credentialPresent = ref(false)
const credentialAction = ref<'keep' | 'replace' | 'remove'>('keep')
const modelDefaults = ref<ClaudeCodeModelDefault[]>([])
const notice = ref({ visible: false, type: 'success', message: '' })

// 连接本代理
const connecting = ref(false)
const importing = ref(false)
const defaultBaseUrl = ref('')

const familyTitles: Record<string, string> = { fable: 'Fable', opus: 'Opus', sonnet: 'Sonnet', haiku: 'Haiku' }
const familyColors: Record<string, string> = { fable: 'primary', opus: 'warning', sonnet: 'success', haiku: 'info' }
const familyTitle = (family: string) => familyTitles[family] ?? family
const familyColor = (family: string) => familyColors[family] ?? 'default'

const loadSettings = async () => {
  loading.value = true
  error.value = ''
  try {
    const loaded = await api.getClaudeCodeSettings()
    settings.value = loaded
    baseUrl.value = loaded.baseUrl
    credentialKind.value = loaded.credentialKind
    credential.value = ''
    credentialMasked.value = loaded.credentialMasked
    credentialPresent.value = loaded.credentialPresent
    credentialAction.value = 'keep'
    modelDefaults.value = loaded.modelDefaults.map(item => ({ ...item }))
    // 预取本代理默认 Base URL 用于按钮展示
    fetchProxyBaseUrl().then(url => { defaultBaseUrl.value = url })
  } catch (loadError) {
    error.value = loadError instanceof Error ? loadError.message : '加载 Claude Code 配置失败'
  } finally {
    loading.value = false
  }
}

// 一键连接本代理：填 Base URL 与访问密钥
const connectToProxy = async () => {
  connecting.value = true
  try {
    baseUrl.value = await fetchProxyBaseUrl()
    const authStore = useAuthStore()
    if (authStore.apiKey) {
      credential.value = authStore.apiKey
      credentialAction.value = 'replace'
    }
    notice.value = { visible: true, type: 'success', message: `已连接本代理：${baseUrl.value}` }
  } catch (connectError) {
    notice.value = { visible: true, type: 'error', message: connectError instanceof Error ? connectError.message : '获取本代理地址失败' }
  } finally {
    connecting.value = false
  }
}

// 从本代理 messages 分组导入 modelDefaults（按 family 归类，导入即替换对应 family）
const importModelDefaultsFromProxy = async () => {
  importing.value = true
  try {
    const response = await api.getProxyModels()
    const modelIds = filterProxyModelsByKind(response.data, 'messages')
    if (modelIds.length === 0) {
      notice.value = { visible: true, type: 'error', message: '本代理 messages 协议分组下暂无模型，请先在渠道管理中配置分组' }
      return
    }
    // 导入时不自动追加 [1m]；1M 支持由用户在每行单独勾选，保留各 family 原有 supports1M 状态
    const imported = buildClaudeCodeModelDefaults(modelIds, false)
    if (imported.length === 0) {
      notice.value = { visible: true, type: 'error', message: '模型名不含 fable/opus/sonnet/haiku 关键词，无法按 family 归类导入' }
      return
    }
    // 保留后端返回的 family 顺序（fable/opus/sonnet/haiku），用导入结果覆盖对应 family
    const importedByFamily = new Map(imported.map(item => [item.family, item]))
    modelDefaults.value = modelDefaults.value.map(existing => {
      const matched = importedByFamily.get(existing.family)
      return matched ? { family: existing.family, model: matched.model, name: matched.name, supports1M: existing.supports1M } : existing
    })
    notice.value = { visible: true, type: 'success', message: `已导入本代理 ${imported.length} 个模型族（已替换对应 family，1M 勾选状态保留）` }
  } catch (importError) {
    notice.value = { visible: true, type: 'error', message: importError instanceof Error ? importError.message : '获取本代理模型列表失败' }
  } finally {
    importing.value = false
  }
}

const onCredentialInput = () => {
  if (credential.value) credentialAction.value = 'replace'
  else if (!credentialPresent.value) credentialAction.value = 'keep'
}

const clearCredential = () => {
  credential.value = ''
  credentialPresent.value = false
  credentialMasked.value = ''
  credentialAction.value = 'remove'
}

const saveSettings = async () => {
  saving.value = true
  try {
    const response = await api.saveClaudeCodeSettings({
      baseUrl: baseUrl.value,
      credentialKind: credentialKind.value,
      credentialAction: credentialAction.value,
      credential: credential.value,
      modelDefaults: modelDefaults.value
    })
    notice.value = { visible: true, type: 'success', message: `已保存到 ${response.path}` }
    await loadSettings()
  } catch (saveError) {
    notice.value = { visible: true, type: 'error', message: saveError instanceof Error ? saveError.message : '保存 Claude Code 配置失败' }
  } finally {
    saving.value = false
  }
}

onMounted(loadSettings)
</script>

<style scoped>
.model-default-row + .model-default-row { border-top: 1px solid rgba(var(--v-theme-on-surface), 0.1); }
</style>
