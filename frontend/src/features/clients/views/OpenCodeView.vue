<template>
  <div class="agent-config-page opencode-page">
    <DraftGuard :dirty="draftDirty || promptState.dirty" :busy="loading || editorBusy || promptState.busy" />
    <AgentConfigHeader title="OpenCode 配置">
      <template #default>
        <UiButton variant="text" prepend-icon="mdi-refresh" :loading="loading" :disabled="editorBusy" @click="loadConfig">刷新</UiButton>
        <UiButton color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="loading || editorBusy || !config" @click="saveConfig">保存配置</UiButton>
      </template>
    </AgentConfigHeader>

    <UiAlert v-if="loadError" type="error" variant="tonal" class="mb-5" closable @click:close="loadError = ''">
      {{ loadError }}
    </UiAlert>

    <ToolInstallBanner tool="opencode" class="mb-5" />

    <UiProgress v-if="loading" indeterminate color="primary" class="mb-5" />

    <fieldset v-else :disabled="editorBusy" :inert="editorBusy" class="agent-config-editor">
      <AgentConfigLocation
        :label="config?.exists ? '当前配置文件' : '首次保存将创建配置文件'"
        :path="config?.path"
      >
        <UiBadge v-if="config?.jsonc" size="x-small" label>JSONC 将在保存后规范化为 JSON</UiBadge>
      </AgentConfigLocation>

      <div class="grid grid-cols-12 gap-4 agent-provider-layout">
        <div class="min-w-0 col-span-12 lg:col-span-4"  >
          <UiCard class="provider-list-card" elevation="0">
            <div class="agent-config-panel-header">
              <div class="agent-config-panel-title">提供商</div>
              <UiButton icon="mdi-plus" size="small" variant="text" title="添加提供商" aria-label="添加提供商" @click="addProvider" />
            </div>
            <hr class="border-0 border-t border-border" />
            <UiList v-if="providers.length" density="comfortable" nav class="py-2">
              <UiListItem
                v-for="provider in providers"
                :key="provider.id"
                :active="selectedProviderId === provider.id"
                :title="provider.name || provider.id"
                :subtitle="provider.id"
                rounded="sm"
                @click="selectedProviderId = provider.id"
              >
                <template #prepend>
                  <UiIcon size="20">mdi-server-network</UiIcon>
                </template>
                <template #append>
                  <UiBadge size="x-small" :color="protocolColor(provider.protocol)" label>{{ protocolLabel(provider.protocol) }}</UiBadge>
                </template>
              </UiListItem>
            </UiList>
            <EmptyState v-else variant="inline" icon="mdi-server-network" title="尚未配置提供商">
              <UiButton size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addProvider">添加提供商</UiButton>
            </EmptyState>
          </UiCard>
        </div>

        <div class="min-w-0 col-span-12 lg:col-span-8"  >
          <UiCard v-if="selectedProvider" elevation="0" class="provider-editor-card">
            <div class="agent-config-panel-header">
              <div>
                <div class="agent-config-panel-title">{{ selectedProvider.name || '新提供商' }}</div>
                <!-- 配置会写入 OpenCode 标准 provider 字段 -->
              </div>
              <div class="agent-config-panel-actions">
                <UiButton color="error" variant="text" size="small" prepend-icon="mdi-delete" @click="removeProvider(selectedProvider.id)">删除</UiButton>
              </div>
            </div>
            <hr class="border-0 border-t border-border" />

            <div class="p-4 p-5">
              <!-- 连接本代理 -->
              <section class="agent-config-callout mb-5 channel-quick-pick">
                <div class="agent-config-callout__title">
                  <UiIcon size="18">mdi-lightning-bolt</UiIcon>
                  连接本代理
                </div>
                <div>
                  <UiSelect
                    v-model="quickPickType"
                    label="模型协议"
                    variant="outlined"
                    density="comfortable"
                    :items="quickPickTypeOptions"
                    item-title="title"
                    item-value="value"
                  />
                </div>
              </section>

              <div class="grid grid-cols-12 gap-4">
                <div class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiInput v-model.trim="selectedProvider.id" label="提供商标识" variant="outlined" density="comfortable" :disabled="providerIdInUse" @update:model-value="selectedProviderId = $event" />
                </div>
                <div class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiInput v-model.trim="selectedProvider.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 DeepSeek" />
                </div>
                <div class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiSelect v-model="selectedProvider.protocol" label="协议" variant="outlined" density="comfortable" :items="protocols" item-title="title" item-value="value" @update:model-value="applyProtocolNpm(selectedProvider)" />
                </div>
                <div v-if="selectedProvider.protocol === 'custom'" class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiInput v-model.trim="selectedProvider.npm" label="AI SDK 包" variant="outlined" density="comfortable" placeholder="例如 @ai-sdk/provider" />
                </div>
                <div :class="(selectedProvider.protocol === 'custom' ? 12 : 6) === 6 ? 'col-span-6' : 'col-span-12'" class="min-w-0" >
                  <UiInput v-model.trim="selectedProvider.baseUrl" label="Base URL" variant="outlined" density="comfortable" placeholder="例如 http://127.0.0.1:8080/v1" />
                </div>
                <div class="min-w-0 col-span-12" >
                  <UiInput
                    v-model="selectedProvider.apiKey"
                    label="API Key"
                    type="password"
                    variant="outlined"
                    density="comfortable"
                    :placeholder="selectedProvider.apiKeyPresent ? `当前：${selectedProvider.apiKeyMasked}（留空则保留）` : '可直接填写，也可使用 {env:VARIABLE}'"
                    @update:model-value="onAPIKeyInput(selectedProvider)"
                  >
                    <template #append-inner>
                      <UiButton v-if="selectedProvider.apiKeyPresent" icon="mdi-delete" size="x-small" variant="text" title="清除已保存密钥" @click="clearAPIKey(selectedProvider)" />
                    </template>
                  </UiInput>
                </div>
              </div>

              <UiAccordion variant="accordion" class="mt-2">
                <UiAccordionItem title="请求头与高级提供商选项">
                  <div class="px-4 pb-4">
                    <div class="grid grid-cols-12 gap-4">
                      <div class="min-w-0 col-span-12 md:col-span-6"  >
                        <JsonPreviewTrigger :data="selectedProvider.headersText" title="OpenCode 请求头"><UiTextarea v-model="selectedProvider.headersText" label="请求头 JSON" variant="outlined" density="comfortable" rows="5" auto-grow placeholder="{&#10;  &quot;X-Custom-Header&quot;: &quot;value&quot;&#10;}" /></JsonPreviewTrigger>
                      </div>
                      <div class="min-w-0 col-span-12 md:col-span-6"  >
                        <JsonPreviewTrigger :data="selectedProvider.optionsText" title="OpenCode 提供商选项"><UiTextarea v-model="selectedProvider.optionsText" label="高级 provider options JSON" variant="outlined" density="comfortable" rows="5" auto-grow placeholder="{&#10;  &quot;timeout&quot;: 600000&#10;}" /></JsonPreviewTrigger>
                      </div>
                    </div>
                  </div>
                </UiAccordionItem>
              </UiAccordion>

              <div class="flex items-center justify-between mt-7 mb-3">
                <div>
                  <div class="text-base font-medium font-bold">模型</div>
                </div>
                <div class="flex gap-2">
                  <UiButton size="small" color="secondary" variant="tonal" prepend-icon="mdi-download" :disabled="!quickPickType" @click="importProxyModelsToProvider">导入本代理模型</UiButton>
                  <UiButton size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addModel(selectedProvider)">添加模型</UiButton>
                </div>
              </div>

              <EmptyState v-if="selectedProvider.models.length === 0" variant="inline" icon="mdi-format-list-bulleted" title="添加至少一个模型后，它才会出现在 OpenCode 的模型列表。" />
              <UiAccordion v-else variant="accordion" class="model-panels">
                <UiAccordionItem v-for="model in selectedProvider.models" :key="model.localId">
                  <template #title>
                    <div class="flex items-center gap-2 overflow-hidden">
                      <span class="font-medium truncate">{{ model.name || model.key || '新模型' }}</span>
                      <span v-if="model.key" class="text-xs text-muted-foreground truncate">{{ model.key }}</span>
                    </div>
                  </template>
                  <div class="px-4 pb-4">
                    <div class="grid grid-cols-12 gap-4">
                      <div class="min-w-0 col-span-12 sm:col-span-6"  >
                        <UiInput v-model.trim="model.key" label="模型标识" variant="outlined" density="comfortable" placeholder="例如 deepseek-v4-pro" />
                      </div>
                      <div class="min-w-0 col-span-12 sm:col-span-6"  >
                        <UiInput v-model.trim="model.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 DeepSeek V4 Pro" />
                      </div>
                      <div class="min-w-0 col-span-12" >
                        <UiInput v-model.trim="model.apiModelId" label="上游模型 ID（可选）" variant="outlined" density="comfortable" placeholder="留空时使用模型标识；用于上游名称与 OpenCode 名称不同的情况" />
                      </div>
                      <div class="min-w-0 col-span-12 sm:col-span-4"  >
                        <UiInput v-model.number="model.contextLimit" label="上下文长度" type="number" min="0" variant="outlined" density="comfortable" />
                      </div>
                      <div class="min-w-0 col-span-12 sm:col-span-4"  >
                        <UiInput v-model.number="model.inputLimit" label="最大输入 Token" type="number" min="0" variant="outlined" density="comfortable" />
                      </div>
                      <div class="min-w-0 col-span-12 sm:col-span-4"  >
                        <UiInput v-model.number="model.outputLimit" label="最大输出 Token" type="number" min="0" variant="outlined" density="comfortable" />
                      </div>
                      <div  class="min-w-0 col-span-12 flex items-center">
                        <UiSwitch v-model="model.supportsImage" label="支持图片输入（识图）" color="primary" density="compact" hide-details />
                        <!-- 开启后写入 options.attachment 与 options.modalities；本项目有图片理解层兜底 -->
                      </div>
                    </div>

                    <!-- Variants 配置 -->
                    <div class="variants-section mt-4">
                      <div class="flex items-center justify-between mb-2">
                        <div>
                          <div class="text-sm font-medium font-bold">思考等级 Variants</div>
                          <!-- 开启后可在 OpenCode TUI 中按 Ctrl+T 切换思考等级 -->
                        </div>
                        <UiSwitch v-model="model.variantsEnabled" color="primary" density="compact" hide-details />
                      </div>
                      <Transition name="ui-expand">
                        <div v-show="model.variantsEnabled" class="variants-list pl-1">
                          <div v-for="(variant, vi) in model.variants" :key="vi" class="variant-row flex items-center gap-2 mb-2">
                            <UiCheckbox v-model="variant.enabled" density="compact" hide-details class="grow-0" />
                            <UiInput v-model.trim="variant.name" label="变体名称" variant="outlined" density="compact" hide-details style="max-width: 160px;" placeholder="high" />
                            <UiSelect v-model="variant.reasoningEffort" label="reasoningEffort" variant="outlined" density="compact" hide-details :items="reasoningEfforts" item-title="title" item-value="value" style="max-width: 200px;" />
                            <UiButton icon="mdi-delete" size="x-small" variant="text" @click="model.variants.splice(vi, 1)" />
                          </div>
                          <UiButton size="small" variant="tonal" prepend-icon="mdi-plus" @click="model.variants.push({ name: '', reasoningEffort: 'medium', enabled: true })">添加变体</UiButton>
                        </div>
                      </Transition>
                    </div>

                    <div  class="min-w-0 col-span-12 p-0 mt-4">
                      <JsonPreviewTrigger :data="model.optionsText" title="OpenCode 模型选项"><UiTextarea v-model="model.optionsText" label="模型高级 options JSON" variant="outlined" density="comfortable" rows="3" auto-grow placeholder='{ "key": "value" }' /></JsonPreviewTrigger>
                    </div>

                    <div class="flex justify-end mt-2">
                      <UiButton size="small" color="error" variant="text" prepend-icon="mdi-delete" @click="removeModel(selectedProvider, model.localId)">删除模型</UiButton>
                    </div>
                  </div>
                </UiAccordionItem>
              </UiAccordion>
            </div>
          </UiCard>

          <UiCard v-else elevation="0" class="empty-editor flex items-center justify-center text-center p-8">
            <div>
              <UiIcon size="44" color="primary" class="mb-3">mdi-cog</UiIcon>
              <div class="text-base font-medium font-bold">选择或添加一个提供商</div>
              <!-- 在这里配置 OpenCode 使用的协议、密钥和模型。 -->
            </div>
          </UiCard>
        </div>
      </div>

    </fieldset>

    <AgentPromptCard agent="opencode" class="mt-5" @draft-state="promptState = $event" />
  </div>
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
import DraftGuard from '@/shared/components/overlay/DraftGuard.vue'
import { useDraftSnapshot } from '@/shared/composables/useDraftSnapshot'
import { ApiError, apiErrorText } from '@/shared/services/http'
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiCheckbox from '@/shared/ui/check/Checkbox.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAccordion from '@/shared/ui/disclosure/Accordion.vue'
import UiAccordionItem from '@/shared/ui/disclosure/AccordionItem.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import JsonPreviewTrigger from '@/shared/components/data/JsonPreviewTrigger.vue'
import { computed, onMounted, ref } from 'vue'
import AgentConfigHeader from '@/features/clients/components/AgentConfigHeader.vue'
import AgentConfigLocation from '@/features/clients/components/AgentConfigLocation.vue'
import AgentPromptCard from '@/features/clients/components/AgentPromptCard.vue'
import ToolInstallBanner from '@/features/clients/components/ToolInstallBanner.vue'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { api, type OpenCodeConfig, type OpenCodeProtocol, type OpenCodeProvider, type OpenCodeVariant, type SaveOpenCodeProvider, type ApiTab } from '@/api'
import { useProxyProtocolPick, defaultQuickPickTypeOptions } from '@/features/clients/composables/useProxyProtocolPick'
import { filterProxyModelsByKind, importProxyModels, buildOpenCodeModel } from '@/features/clients/utils/proxyModelImport'
import { PROTOCOL_DEFAULTS } from '@/features/clients/utils/protocolDefaults'
import { useAuthStore } from '@/shared/stores/auth'
import { useToast } from '@/shared/composables/useToast'
import { parseJSONObject } from '@/shared/utils/parseJSONObject'
import { useConfirm } from '@/shared/composables/useConfirm'

const { showToast } = useToast()
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()

interface EditableVariant {
  name: string
  reasoningEffort: string
  enabled: boolean
}

interface EditableModel {
  localId: string
  key: string
  apiModelId: string
  name: string
  contextLimit: number
  inputLimit: number
  outputLimit: number
  variantsEnabled: boolean
  variants: EditableVariant[]
  supportsImage: boolean
  optionsText: string
}

interface EditableProvider {
  id: string
  name: string
  protocol: OpenCodeProtocol
  npm: string
  baseUrl: string
  apiKeyMasked: string
  apiKeyPresent: boolean
  apiKey: string
  apiKeyAction: 'keep' | 'replace' | 'remove'
  headersText: string
  optionsText: string
  models: EditableModel[]
}

const protocols: Array<{ title: string; value: OpenCodeProtocol }> = [
  { title: 'Chat Completions', value: 'chat' },
  { title: 'Responses', value: 'responses' },
  { title: 'Messages', value: 'messages' },
  { title: 'Gemini', value: 'gemini' },
  { title: '自定义 SDK', value: 'custom' }
]

const reasoningEfforts = [
  { title: '不启用推理', value: 'none' },
  { title: '低', value: 'low' },
  { title: '中', value: 'medium' },
  { title: '高', value: 'high' },
  { title: '超高', value: 'xhigh' },
  { title: '最高', value: 'max' }
]

// 思考等级从低到高的标准排序，用于加载后端配置时保证 variants 显示顺序一致
const reasoningEffortOrder = ['none', 'low', 'medium', 'high', 'xhigh', 'max']

const defaultVariantNames = ['none', 'low', 'medium', 'high', 'xhigh', 'max']

function makeDefaultVariants(): EditableVariant[] {
  return defaultVariantNames.map(name => ({ name, reasoningEffort: name, enabled: true }))
}

const { dirty: draftDirty, markSaved } = useDraftSnapshot(() => providers.value)
const promptState = ref({ dirty: false, busy: false })
const loading = ref(false)
const saving = ref(false)
const preparingProvider = ref(false)
const editorBusy = computed(() => saving.value || preparingProvider.value)
const loadError = ref('')
const config = ref<OpenCodeConfig | null>(null)
const providers = ref<EditableProvider[]>([])
const selectedProviderId = ref('')

// 连接本代理（协议选择）
const quickPickTypeOptions = defaultQuickPickTypeOptions

// 渠道协议 -> OpenCode 协议映射
const protocolForChannelType: Record<ApiTab, OpenCodeProtocol> = {
  messages: 'messages',
  responses: 'responses',
  gemini: 'gemini',
  chat: 'chat',
  images: 'chat',
  embeddings: 'chat',
  decisions: 'chat'
}

const applyProtocol = async (type: ApiTab, defaultBaseUrl: Promise<string>) => {
  const provider = selectedProvider.value
  if (!provider || editorBusy.value || loading.value) return
  preparingProvider.value = true
  try {
    const baseUrl = await defaultBaseUrl
    // 自动切换协议与 SDK 包
    provider.protocol = protocolForChannelType[type]
    applyProtocolNpm(provider)
    // AI SDK 在 baseURL 后直接追加资源路径，需在这里补齐对应协议的版本前缀。
    const root = baseUrl.replace(/\/+$/, '').replace(/\/v1(?:beta)?$/, '')
    provider.baseUrl = `${root}/${type === 'gemini' ? 'v1beta' : 'v1'}`
    // 密钥填本代理访问 key（Web 鉴权与代理鉴权共用同一 key）
    const authStore = useAuthStore()
    if (authStore.apiKey) {
      provider.apiKey = authStore.apiKey
      provider.apiKeyAction = 'replace'
    }
  } catch (error) {
    showToast(error instanceof Error ? error.message : '连接本代理失败', 'error')
  } finally {
    preparingProvider.value = false
  }
}

const { selectedType: quickPickType, defaultBaseUrl } = useProxyProtocolPick(applyProtocol)

const selectedProvider = computed(() => providers.value.find(provider => provider.id === selectedProviderId.value) ?? null)
const providerIdInUse = computed(() => Boolean(selectedProvider.value && config.value?.providers.some(provider => provider.id === selectedProvider.value?.id)))
const localID = () => `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`

const stringifyJSON = (value: Record<string, unknown>) => Object.keys(value).length ? JSON.stringify(value, null, 2) : ''

// 从后端 options 推断识图开关状态（attachment 为 false 或 modalities.input 不含 image 视为关闭）
const detectSupportsImage = (options: Record<string, unknown>): boolean => {
  if (options.attachment === false) return false
  const modalities = options.modalities as { input?: string[] } | undefined
  if (modalities?.input && Array.isArray(modalities.input)) {
    return modalities.input.includes('image')
  }
  // 后端 applyOpenCodeImageDefaults 默认给 true + [text,image]，未显式关闭时视为开
  return true
}

const toEditableProvider = (provider: OpenCodeProvider): EditableProvider => ({
  ...provider,
  apiKey: '',
  apiKeyAction: 'keep',
  headersText: stringifyJSON(provider.headers),
  optionsText: stringifyJSON(provider.options),
  models: provider.models.map(model => {
    const options = { ...model.options }
    delete options.reasoningEffort
    const supportsImage = detectSupportsImage(options)
    // 识图字段从 options 中剥离，由专门开关管理
    delete options.attachment
    delete options.modalities
    // From model.variants build editable variants; sort by standard effort order (low to high)
    const rawVariants = model.variants ?? {}
    const variantNames = Object.keys(rawVariants).sort((a, b) => {
      const indexA = reasoningEffortOrder.indexOf(a)
      const indexB = reasoningEffortOrder.indexOf(b)
      // Known levels sort by predefined order; unknown levels sort last, stable
      if (indexA === -1 && indexB === -1) return a.localeCompare(b)
      if (indexA === -1) return 1
      if (indexB === -1) return -1
      return indexA - indexB
    })
    let variants: EditableVariant[]
    let variantsEnabled: boolean
    if (variantNames.length > 0) {
      variantsEnabled = true
      variants = variantNames.map(name => ({
        name,
        reasoningEffort: rawVariants[name]?.reasoningEffort ?? 'high',
        enabled: true
      }))
    } else {
      // Migrate from legacy model.options.reasoningEffort
      const oldEffort = typeof model.options.reasoningEffort === 'string' ? model.options.reasoningEffort : ''
      if (oldEffort) {
        variantsEnabled = true
        variants = makeDefaultVariants().map(v => ({ ...v, enabled: v.name === oldEffort }))
      } else {
        variantsEnabled = false
        variants = makeDefaultVariants()
      }
    }
    return { ...model, localId: localID(), variantsEnabled, variants, supportsImage, optionsText: stringifyJSON(options) }
  })
})

const loadConfig = async () => {
  if (loading.value || editorBusy.value) return
  loading.value = true
  loadError.value = ''
  try {
    const loaded = await api.getOpenCodeConfig()
    applyLoadedConfig(loaded)
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '加载 OpenCode 配置失败'
  } finally {
    loading.value = false
  }
}

const applyLoadedConfig = (loaded: OpenCodeConfig) => {
  const selectedId = selectedProviderId.value
  config.value = loaded
  providers.value = loaded.providers.map(toEditableProvider)
  selectedProviderId.value = providers.value.some(provider => provider.id === selectedId)
    ? selectedId : providers.value[0]?.id ?? ''
  markSaved()
}

const addProvider = () => {
  let index = providers.value.length + 1
  let id = `provider-${index}`
  while (providers.value.some(provider => provider.id === id)) {
    index += 1
    id = `provider-${index}`
  }
  providers.value.push({
    id,
    name: '',
    protocol: 'chat',
    npm: '@ai-sdk/openai-compatible',
    baseUrl: '',
    apiKeyMasked: '',
    apiKeyPresent: false,
    apiKey: '',
    apiKeyAction: 'keep',
    headersText: '',
    optionsText: '',
    models: []
  })
  selectedProviderId.value = id
}

const removeProvider = (id: string) => {
  const provider = providers.value.find(item => item.id === id)
  if (!provider) return
  askConfirm({
    message: `删除提供商“${provider.name || provider.id}”及其模型？`,
    confirmText: '删除',
    action: () => {
      providers.value = providers.value.filter(item => item !== provider)
      selectedProviderId.value = providers.value[0]?.id ?? ''
    }
  })
}

// 新增模型默认值：用 protocolDefaults 的 chat 协议默认值（未选渠道时的合理兜底）
const addModel = (provider: EditableProvider) => {
  const defaults = PROTOCOL_DEFAULTS.chat
  provider.models.push({
    localId: localID(),
    key: '',
    apiModelId: '',
    name: '',
    contextLimit: defaults.contextLimit,
    inputLimit: defaults.inputLimit,
    outputLimit: defaults.outputLimit,
    variantsEnabled: false,
    variants: makeDefaultVariants(),
    supportsImage: true,
    optionsText: ''
  })
}

// 一键导入本代理模型（按所选协议分组，导入即替换）
const importProxyModelsToProvider = async () => {
  const provider = selectedProvider.value
  const type = quickPickType.value
  if (!provider || !type || editorBusy.value || loading.value) return
  preparingProvider.value = true
  try {
    const response = await api.getProxyModels(type)
    const modelIds = filterProxyModelsByKind(response.data, type)
    if (modelIds.length === 0) {
      showToast(`本代理 ${type} 协议分组下暂无模型，请先在渠道管理中配置分组`, 'error')
      return
    }
    const count = importProxyModels(type, modelIds, buildOpenCodeModel, (models) => {
      provider.models = models.map(m => ({
        localId: localID(),
        key: m.key,
        apiModelId: m.apiModelId,
        name: m.name,
        contextLimit: m.contextLimit,
        inputLimit: m.inputLimit,
        outputLimit: m.outputLimit,
        variantsEnabled: true,
        variants: Object.entries(m.variants).map(([name, v]) => ({ name, reasoningEffort: v.reasoningEffort, enabled: true })),
        supportsImage: m.supportsImage,
        optionsText: ''
      }))
    })
    showToast(`已导入本代理 ${type} 分组的 ${count} 个模型（已替换原有模型）`, 'success')
  } catch (importError) {
    showToast(importError instanceof Error ? importError.message : '获取本代理模型列表失败', 'error')
  } finally {
    preparingProvider.value = false
  }
}

const removeModel = (provider: EditableProvider, localId: string) => {
  provider.models = provider.models.filter(model => model.localId !== localId)
}

const applyProtocolNpm = (provider: EditableProvider) => {
  const npm: Record<Exclude<OpenCodeProtocol, 'custom'>, string> = {
    chat: '@ai-sdk/openai-compatible',
    responses: '@ai-sdk/openai',
    messages: '@ai-sdk/anthropic',
    gemini: '@ai-sdk/google'
  }
  if (provider.protocol !== 'custom') provider.npm = npm[provider.protocol]
}

const protocolLabel = (protocol: OpenCodeProtocol) => protocols.find(item => item.value === protocol)?.title ?? '自定义 SDK'
const protocolColor = (protocol: OpenCodeProtocol) => ({ chat: 'info', responses: 'primary', messages: 'success', gemini: 'deep-purple', custom: 'warning' })[protocol]

const onAPIKeyInput = (provider: EditableProvider) => {
  if (provider.apiKey) provider.apiKeyAction = 'replace'
  else if (!provider.apiKeyPresent) provider.apiKeyAction = 'keep'
}

const clearAPIKey = (provider: EditableProvider) => {
  provider.apiKey = ''
  provider.apiKeyAction = 'remove'
  provider.apiKeyPresent = false
  provider.apiKeyMasked = ''
}

const prepareProvider = (provider: EditableProvider): SaveOpenCodeProvider => {
  const headers = parseJSONObject(provider.headersText, `提供商 ${provider.id} 的请求头`)
  if (Object.values(headers).some(value => typeof value !== 'string')) throw new Error(`提供商 ${provider.id} 的请求头值必须是字符串`)
  return {
    id: provider.id.trim(),
    name: provider.name.trim(),
    protocol: provider.protocol,
    npm: provider.npm.trim(),
    baseUrl: provider.baseUrl.trim(),
    apiKeyAction: provider.apiKeyAction,
    apiKey: provider.apiKey,
    headers: headers as Record<string, string>,
    options: parseJSONObject(provider.optionsText, `提供商 ${provider.id} 的高级选项`),
    models: provider.models.map(model => {
      const options = parseJSONObject(model.optionsText, `模型 ${model.key || '新模型'} 的高级选项`)
      // 识图开关写回 options（与后端 applyOpenCodeImageDefaults 字段对齐）
      if (model.supportsImage) {
        options.attachment = true
        options.modalities = { input: ['text', 'image'], output: ['text'] }
      } else {
        options.attachment = false
      }
      // variants
      const variants: Record<string, OpenCodeVariant> = {}
      if (model.variantsEnabled) {
        const enabledVariants = model.variants.filter(v => v.enabled && v.name.trim())
        if (enabledVariants.length === 0) throw new Error(`模型 ${model.key || '新模型'} 开启了 Variants 但未启用任何变体`)
        for (const v of enabledVariants) {
          variants[v.name.trim()] = { reasoningEffort: v.reasoningEffort }
        }
      }
      return {
        key: model.key.trim(),
        apiModelId: model.apiModelId.trim(),
        name: model.name.trim(),
        contextLimit: Number(model.contextLimit) || 0,
        inputLimit: Number(model.inputLimit) || 0,
        outputLimit: Number(model.outputLimit) || 0,
        options,
        variants: model.variantsEnabled ? variants : {}
      }
    })
  }
}

const saveConfig = async () => {
  if (editorBusy.value || loading.value || !config.value) return
  saving.value = true
  try {
    const payload = { providers: providers.value.map(prepareProvider), revision: config.value.revision }
    const response = await api.saveOpenCodeConfig(payload)
    config.value.revision = response.revision
    markSaved()
    showToast(`已保存到 ${response.path}`, 'success')
    try {
      applyLoadedConfig(await api.getOpenCodeConfig())
    } catch (error) {
      showToast(`配置已保存，但刷新失败: ${error instanceof Error ? error.message : '未知错误'}。请刷新查看实际状态，请勿重复提交`, 'warning')
    }
  } catch (error) {
    const committed = error instanceof ApiError && error.committed
    showToast(apiErrorText(error) + (committed ? '' : '。当前编辑已保留'), committed ? 'warning' : 'error')
  } finally {
    saving.value = false
  }
}

onMounted(loadConfig)
</script>

<style scoped>
@reference "@/styles/index.css";

.variants-section { @apply [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] rounded-control [padding:12px_16px]; }
.variant-row { @apply flex-nowrap; }
</style>
