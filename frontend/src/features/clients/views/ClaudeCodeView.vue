<template>
  <div class="agent-config-page claude-code-page">
    <UiAlert v-if="reloadRequired" type="warning" class="mb-4">配置已写入，请点击刷新核对；刷新成功后才能继续编辑或保存。</UiAlert>
    <DraftGuard :dirty="draftDirty || promptState.dirty" :busy="editorBusy || promptState.busy" />
    <AgentConfigHeader title="Claude Code 配置">
      <template #default>
        <UiButton variant="text" prepend-icon="mdi-refresh" :loading="loading" :disabled="editorBusy" @click="loadSettings()">刷新</UiButton>
        <UiButton color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="editorBusy || reloadRequired || !settings" @click="saveSettings">保存配置</UiButton>
      </template>
    </AgentConfigHeader>

    <UiAlert v-if="error" type="error" variant="tonal" class="mb-5" closable @click:close="error = ''">{{ error }}</UiAlert>
    <ToolInstallBanner tool="claude" class="mb-5" />
    <UiProgress v-if="loading" indeterminate color="primary" class="mb-5" />

    <template v-else>
      <AgentConfigLocation
        :label="settings?.exists ? '当前配置文件' : '首次保存将创建配置文件'"
        :path="settings?.path"
      >
        <UiBadge v-if="settings?.jsonc" size="x-small" label>JSONC 将在保存后规范化为 JSON</UiBadge>
      </AgentConfigLocation>

      <div class="grid grid-cols-12 gap-4">
        <div class="min-w-0 col-span-12" >
          <UiCard :inert="editorBusy || reloadRequired" elevation="0" class="settings-card agent-config-panel h-full">
            <div class="agent-config-panel-header">
              <div class="agent-config-panel-title">代理连接</div>
            </div>
            <hr class="border-0 border-t border-border" />
            <div class="p-4 p-5">
              <!-- 连接本代理（固定 messages 协议） -->
              <section class="agent-config-callout mb-4 channel-quick-pick">
                <div class="agent-config-callout__title">
                  <UiIcon size="18">mdi-lightning-bolt</UiIcon>
                  连接本代理
                </div>
                <div>
                  <UiButton color="primary" variant="tonal" prepend-icon="mdi-link-variant" :loading="connecting" @click="connectToProxy">一键连接本代理（{{ defaultBaseUrl || 'http://localhost:...' }}）</UiButton>
                  <!-- Claude Code 固定走 messages 协议，点击后自动填充 Base URL 与访问密钥，模型可一键导入 -->
                </div>
              </section>

              <hr class="border-0 border-t border-border my-4" />
              <UiInput v-model.trim="baseUrl" label="Anthropic Base URL" variant="outlined" density="comfortable" placeholder="例如 http://127.0.0.1:8080" />
              <UiRadioGroup v-model="credentialKind" inline class="mt-2 mb-1" hide-details>
                <UiRadio label="认证令牌" value="authToken" />
                <UiRadio label="API Key" value="apiKey" />
              </UiRadioGroup>
              <UiInput
                v-model="credential"
                :label="credentialKind === 'authToken' ? 'ANTHROPIC_AUTH_TOKEN' : 'ANTHROPIC_API_KEY'"
                type="password"
                variant="outlined"
                density="comfortable"
                :placeholder="credentialPresent ? `当前：${credentialMasked}（留空则保留）` : '输入密钥'"
                @update:model-value="onCredentialInput"
              >
                <template #append-inner>
                  <UiButton v-if="credentialPresent" icon="mdi-delete" size="x-small" variant="text" title="清除已保存密钥" @click="clearCredential" />
                </template>
              </UiInput>
            </div>
          </UiCard>
        </div>
      </div>

      <UiCard :inert="editorBusy || reloadRequired" elevation="0" class="settings-card agent-config-panel mt-5">
        <div class="agent-config-panel-header">
          <div>
            <div class="agent-config-panel-subtitle">为空时不会写入对应环境变量，Claude Code 将使用自身默认模型。每个模型族可独立勾选 1M 上下文。</div>
          </div>
          <div class="agent-config-panel-actions">
            <UiButton size="small" color="secondary" variant="tonal" prepend-icon="mdi-download" :loading="importing" @click="importModelDefaultsFromProxy">导入本代理模型</UiButton>
          </div>
        </div>
        <hr class="border-0 border-t border-border" />
        <div class="p-4 p-5">
          <div v-for="item in modelDefaults" :key="item.family" class="grid grid-cols-12 gap-4 model-default-row">
            <div   class="min-w-0 col-span-12 sm:col-span-3 flex items-center sm:pt-5">
              <UiBadge :color="familyColor(item.family)" label>{{ familyTitle(item.family) }}</UiBadge>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-5"  >
              <UiInput v-model.trim="item.model" label="模型标识" variant="outlined" density="comfortable" :placeholder="`${familyTitle(item.family)} 默认模型`" />
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-3"  >
              <UiInput v-model.trim="item.name" label="显示名称" variant="outlined" density="comfortable" placeholder="可选" />
            </div>
            <div   class="min-w-0 col-span-12 sm:col-span-1 flex items-center justify-center sm:pt-5">
              <UiTooltip text="启用 1M 上下文（写入模型标识追加 [1m] 后缀）">
                <template #activator="{ props }">
                  <UiCheckbox v-model="item.supports1M" v-bind="props" label="1M" density="compact" hide-details color="primary" />
                </template>
              </UiTooltip>
            </div>
          </div>
        </div>
      </UiCard>
    </template>

    <AgentPromptCard agent="claude-code" class="mt-5" @draft-state="promptState = $event" />
  </div>
</template>

<script setup lang="ts">
import DraftGuard from '@/shared/components/overlay/DraftGuard.vue'
import { useDraftSnapshot } from '@/shared/composables/useDraftSnapshot'
import { ApiError, apiErrorText } from '@/shared/services/http'
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiCheckbox from '@/shared/ui/check/Checkbox.vue'
import UiRadio from '@/shared/ui/check/Radio.vue'
import UiRadioGroup from '@/shared/ui/check/RadioGroup.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import { computed, onMounted, ref } from 'vue'
import AgentConfigHeader from '@/features/clients/components/AgentConfigHeader.vue'
import AgentConfigLocation from '@/features/clients/components/AgentConfigLocation.vue'
import AgentPromptCard from '@/features/clients/components/AgentPromptCard.vue'
import ToolInstallBanner from '@/features/clients/components/ToolInstallBanner.vue'
import { api, type ClaudeCodeModelDefault, type ClaudeCodeSettings } from '@/api'
import { buildClaudeCodeModelDefaults } from '@/features/clients/utils/proxyModelImport'
import { fetchProxyBaseUrl } from '@/features/clients/composables/useProxyProtocolPick'
import { filterProxyModelsByKind } from '@/features/clients/utils/proxyModelImport'
import { useAuthStore } from '@/shared/stores/auth'
import { useToast } from '@/shared/composables/useToast'

const { showToast } = useToast()

const { dirty: draftDirty, markSaved } = useDraftSnapshot(() => [baseUrl.value, credentialKind.value, credential.value, credentialAction.value, modelDefaults.value])
const loading = ref(false)
const saving = ref(false)
const reloadRequired = ref(false)
const editorBusy = computed(() => loading.value || saving.value || connecting.value || importing.value)
const promptState = ref({ dirty: false, busy: false })
const error = ref('')
const settings = ref<ClaudeCodeSettings | null>(null)
const baseUrl = ref('')
const credentialKind = ref<'authToken' | 'apiKey'>('authToken')
const credential = ref('')
const credentialMasked = ref('')
const credentialPresent = ref(false)
const credentialAction = ref<'keep' | 'replace' | 'remove'>('keep')
const credentialRemoved = ref(false)
const modelDefaults = ref<ClaudeCodeModelDefault[]>([])

// 连接本代理
const connecting = ref(false)
const importing = ref(false)
const defaultBaseUrl = ref('')

const familyTitles: Record<string, string> = { fable: 'Fable', opus: 'Opus', sonnet: 'Sonnet', haiku: 'Haiku' }
const familyColors: Record<string, string> = { fable: 'primary', opus: 'warning', sonnet: 'success', haiku: 'info' }
const familyTitle = (family: string) => familyTitles[family] ?? family
const familyColor = (family: string) => familyColors[family] ?? 'default'

const loadSettings = async (afterSave = false) => {
  if (loading.value || (!afterSave && editorBusy.value)) return
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
    credentialRemoved.value = false
    modelDefaults.value = loaded.modelDefaults.map(item => ({ ...item }))
    markSaved()
    reloadRequired.value = false
    // 预取本代理默认 Base URL 用于按钮展示
    fetchProxyBaseUrl().then(url => { defaultBaseUrl.value = url }).catch(error => showToast(apiErrorText(error), 'warning'))
  } catch (loadError) {
    if (afterSave) reloadRequired.value = true
    error.value = loadError instanceof Error ? loadError.message : '加载 Claude Code 配置失败'
  } finally {
    loading.value = false
  }
}

// 一键连接本代理：填 Base URL 与访问密钥
const connectToProxy = async () => {
  if (editorBusy.value || reloadRequired.value) return
  connecting.value = true
  try {
    baseUrl.value = await fetchProxyBaseUrl()
    const authStore = useAuthStore()
    if (authStore.apiKey) {
      credential.value = authStore.apiKey
      credentialRemoved.value = false
      credentialAction.value = 'replace'
    }
    showToast(`已连接本代理：${baseUrl.value}`, 'success')
  } catch (connectError) {
    showToast(connectError instanceof Error ? connectError.message : '获取本代理地址失败', 'error')
  } finally {
    connecting.value = false
  }
}

// 从本代理 messages 分组导入 modelDefaults（按 family 归类，导入即替换对应 family）
const importModelDefaultsFromProxy = async () => {
  if (editorBusy.value || reloadRequired.value) return
  importing.value = true
  try {
    const response = await api.getProxyModels('messages')
    const modelIds = filterProxyModelsByKind(response.data, 'messages')
    if (modelIds.length === 0) {
      showToast('本代理 messages 协议分组下暂无模型，请先在渠道管理中配置分组', 'error')
      return
    }
    // 导入时不自动追加 [1m]；1M 支持由用户在每行单独勾选，保留各 family 原有 supports1M 状态
    const imported = buildClaudeCodeModelDefaults(modelIds, false)
    if (imported.length === 0) {
      showToast('模型名不含 fable/opus/sonnet/haiku 关键词，无法按 family 归类导入', 'error')
      return
    }
    // 保留后端返回的 family 顺序（fable/opus/sonnet/haiku），用导入结果覆盖对应 family
    const importedByFamily = new Map(imported.map(item => [item.family, item]))
    modelDefaults.value = modelDefaults.value.map(existing => {
      const matched = importedByFamily.get(existing.family)
      return matched ? { family: existing.family, model: matched.model, name: matched.name, supports1M: existing.supports1M } : existing
    })
    showToast(`已导入本代理 ${imported.length} 个模型族（已替换对应 family，1M 勾选状态保留）`, 'success')
  } catch (importError) {
    showToast(importError instanceof Error ? importError.message : '获取本代理模型列表失败', 'error')
  } finally {
    importing.value = false
  }
}

const onCredentialInput = () => {
  if (credential.value) { credentialRemoved.value = false; credentialAction.value = 'replace' }
  else credentialAction.value = credentialRemoved.value ? 'remove' : 'keep'
}

const clearCredential = () => {
  credentialRemoved.value = true
  credential.value = ''
  credentialPresent.value = false
  credentialMasked.value = ''
  credentialAction.value = 'remove'
}

const saveSettings = async () => {
  if (editorBusy.value || reloadRequired.value || !settings.value) return
  saving.value = true
  try {
    const response = await api.saveClaudeCodeSettings({
      revision: settings.value?.revision ?? '',
      baseUrl: baseUrl.value,
      credentialKind: credentialKind.value,
      credentialAction: credentialAction.value,
      credential: credential.value,
      modelDefaults: modelDefaults.value
    })
    showToast(`已保存到 ${response.path}`, 'success')
    markSaved()
    await loadSettings(true)
  } catch (saveError) {
    if (saveError instanceof ApiError && saveError.committed) { reloadRequired.value = true; markSaved() }
    showToast(apiErrorText(saveError), saveError instanceof ApiError && saveError.committed ? 'warning' : 'error')
  } finally {
    saving.value = false
  }
}

onMounted(() => loadSettings())
</script>

<style scoped>
@reference "@/styles/index.css";

.model-default-row + .model-default-row { @apply [border-top:1px_solid_color-mix(in_srgb,_var(--foreground)_10%,_transparent)]; }
</style>
