<template>
  <div class="agent-config-page opencode-page">
    <AgentConfigHeader title="OpenCode 配置" subtitle="管理当前服务运行用户的 OpenCode 模型提供商和默认模型">
      <template #default>
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadConfig">刷新</v-btn>
        <v-btn color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="loading" @click="saveConfig">保存配置</v-btn>
      </template>
    </AgentConfigHeader>

    <v-alert v-if="loadError" type="error" variant="tonal" class="mb-5" closable @click:close="loadError = ''">
      {{ loadError }}
    </v-alert>

    <ToolInstallBanner tool="opencode" class="mb-5" />

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-5" />

    <template v-else>
      <AgentConfigLocation
        :label="config?.exists ? '当前配置文件' : '首次保存将创建配置文件'"
        :path="config?.path"
      >
        <v-chip v-if="config?.jsonc" size="x-small" label>JSONC 将在保存后规范化为 JSON</v-chip>
      </AgentConfigLocation>

      <v-row>
        <v-col cols="12" lg="4">
          <v-card class="provider-list-card" elevation="0">
            <div class="agent-config-panel-header">
              <div class="agent-config-panel-title">提供商</div>
              <v-btn icon="mdi-plus" size="small" variant="text" title="添加提供商" aria-label="添加提供商" @click="addProvider" />
            </div>
            <v-divider />
            <v-list v-if="providers.length" density="comfortable" nav class="py-2">
              <v-list-item
                v-for="provider in providers"
                :key="provider.id"
                :active="selectedProviderId === provider.id"
                :title="provider.name || provider.id"
                :subtitle="provider.id"
                rounded="sm"
                @click="selectedProviderId = provider.id"
              >
                <template #prepend>
                  <v-icon size="20">mdi-server-network</v-icon>
                </template>
                <template #append>
                  <v-chip size="x-small" :color="protocolColor(provider.protocol)" label>{{ protocolLabel(provider.protocol) }}</v-chip>
                </template>
              </v-list-item>
            </v-list>
            <div v-else class="empty-providers text-center text-medium-emphasis px-6 py-10">
              <v-icon size="32" class="mb-3">mdi-server-network</v-icon>
              <div class="text-body-2">尚未配置提供商</div>
              <v-btn class="mt-4" size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addProvider">添加提供商</v-btn>
            </div>
          </v-card>
        </v-col>

        <v-col cols="12" lg="8">
          <v-card v-if="selectedProvider" elevation="0" class="provider-editor-card">
            <div class="agent-config-panel-header">
              <div>
                <div class="agent-config-panel-title">{{ selectedProvider.name || '新提供商' }}</div>
                <div class="agent-config-panel-subtitle">配置会写入 OpenCode 标准 provider 字段</div>
              </div>
              <div class="agent-config-panel-actions">
                <v-btn color="error" variant="text" size="small" prepend-icon="mdi-delete" @click="removeProvider(selectedProvider.id)">删除</v-btn>
              </div>
            </div>
            <v-divider />

            <v-card-text class="pa-5">
              <!-- 连接本代理 -->
              <section class="agent-config-callout mb-5 channel-quick-pick">
                <div class="agent-config-callout__title">
                  <v-icon size="18">mdi-lightning-bolt</v-icon>
                  连接本代理
                </div>
                <div>
                  <v-select
                    v-model="quickPickType"
                    label="模型协议"
                    variant="outlined"
                    density="comfortable"
                    :items="quickPickTypeOptions"
                    item-title="title"
                    item-value="value"
                  />
                  <div class="text-caption text-medium-emphasis mt-1">
                    选择协议后将自动连接本代理（{{ defaultBaseUrl }}）：协议、Base URL 与密钥自动填充，模型可一键导入
                  </div>
                </div>
              </section>

              <v-row>
                <v-col cols="12" sm="6">
                  <v-text-field v-model.trim="selectedProvider.id" label="提供商标识" variant="outlined" density="comfortable" :disabled="providerIdInUse" hint="例如 deepseek；模型引用将使用 deepseek/模型标识" persistent-hint @update:model-value="selectedProviderId = $event" />
                </v-col>
                <v-col cols="12" sm="6">
                  <v-text-field v-model.trim="selectedProvider.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 DeepSeek" />
                </v-col>
                <v-col cols="12" sm="6">
                  <v-select v-model="selectedProvider.protocol" label="协议" variant="outlined" density="comfortable" :items="protocols" item-title="title" item-value="value" @update:model-value="applyProtocolNpm(selectedProvider)" />
                </v-col>
                <v-col v-if="selectedProvider.protocol === 'custom'" cols="12" sm="6">
                  <v-text-field v-model.trim="selectedProvider.npm" label="AI SDK 包" variant="outlined" density="comfortable" placeholder="例如 @ai-sdk/provider" />
                </v-col>
                <v-col :cols="selectedProvider.protocol === 'custom' ? 12 : 6">
                  <v-text-field v-model.trim="selectedProvider.baseUrl" label="Base URL" variant="outlined" density="comfortable" placeholder="例如 http://127.0.0.1:8080/v1" hint="填入 API 版本根路径，不要填具体 endpoint" persistent-hint />
                </v-col>
                <v-col cols="12">
                  <v-text-field
                    v-model="selectedProvider.apiKey"
                    label="API Key"
                    type="password"
                    variant="outlined"
                    density="comfortable"
                    :placeholder="selectedProvider.apiKeyPresent ? `当前：${selectedProvider.apiKeyMasked}（留空则保留）` : '可直接填写，也可使用 {env:VARIABLE}'"
                    @update:model-value="onAPIKeyInput(selectedProvider)"
                  >
                    <template #append-inner>
                      <v-btn v-if="selectedProvider.apiKeyPresent" icon="mdi-delete" size="x-small" variant="text" title="清除已保存密钥" @click="clearAPIKey(selectedProvider)" />
                    </template>
                  </v-text-field>
                </v-col>
              </v-row>

              <v-expansion-panels variant="accordion" class="mt-2">
                <v-expansion-panel title="请求头与高级提供商选项">
                  <v-expansion-panel-text>
                    <v-row>
                      <v-col cols="12" md="6">
                        <v-textarea v-model="selectedProvider.headersText" label="请求头 JSON" variant="outlined" density="comfortable" rows="5" auto-grow placeholder="{&#10;  &quot;X-Custom-Header&quot;: &quot;value&quot;&#10;}" />
                      </v-col>
                      <v-col cols="12" md="6">
                        <v-textarea v-model="selectedProvider.optionsText" label="高级 provider options JSON" variant="outlined" density="comfortable" rows="5" auto-grow placeholder="{&#10;  &quot;timeout&quot;: 600000&#10;}" />
                      </v-col>
                    </v-row>
                  </v-expansion-panel-text>
                </v-expansion-panel>
              </v-expansion-panels>

              <div class="d-flex align-center justify-space-between mt-7 mb-3">
                <div>
                  <div class="text-subtitle-1 font-weight-bold">模型</div>
                  <div class="text-caption text-medium-emphasis">模型标识必须与上游 API 接收的模型名一致</div>
                </div>
                <div class="d-flex ga-2">
                  <v-btn size="small" color="secondary" variant="tonal" prepend-icon="mdi-download" :disabled="!quickPickType" @click="importProxyModelsToProvider">导入本代理模型</v-btn>
                  <v-btn size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addModel(selectedProvider)">添加模型</v-btn>
                </div>
              </div>

              <div v-if="selectedProvider.models.length === 0" class="empty-models text-body-2 text-medium-emphasis py-6 text-center">添加至少一个模型后，它才会出现在 OpenCode 的模型列表。</div>
              <v-expansion-panels v-else variant="accordion" class="model-panels">
                <v-expansion-panel v-for="model in selectedProvider.models" :key="model.localId">
                  <template #title>
                    <div class="d-flex align-center ga-2 overflow-hidden">
                      <span class="font-weight-medium text-truncate">{{ model.name || model.key || '新模型' }}</span>
                      <span v-if="model.key" class="text-caption text-medium-emphasis text-truncate">{{ model.key }}</span>
                    </div>
                  </template>
                  <v-expansion-panel-text>
                    <v-row>
                      <v-col cols="12" sm="6">
                        <v-text-field v-model.trim="model.key" label="模型标识" variant="outlined" density="comfortable" placeholder="例如 deepseek-v4-pro" />
                      </v-col>
                      <v-col cols="12" sm="6">
                        <v-text-field v-model.trim="model.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 DeepSeek V4 Pro" />
                      </v-col>
                      <v-col cols="12">
                        <v-text-field v-model.trim="model.apiModelId" label="上游模型 ID（可选）" variant="outlined" density="comfortable" placeholder="留空时使用模型标识；用于上游名称与 OpenCode 名称不同的情况" />
                      </v-col>
                      <v-col cols="12" sm="4">
                        <v-text-field v-model.number="model.contextLimit" label="上下文长度" type="number" min="0" variant="outlined" density="comfortable" />
                      </v-col>
                      <v-col cols="12" sm="4">
                        <v-text-field v-model.number="model.inputLimit" label="最大输入 Token" type="number" min="0" variant="outlined" density="comfortable" />
                      </v-col>
                      <v-col cols="12" sm="4">
                        <v-text-field v-model.number="model.outputLimit" label="最大输出 Token" type="number" min="0" variant="outlined" density="comfortable" />
                      </v-col>
                      <v-col cols="12" class="d-flex align-center">
                        <v-switch v-model="model.supportsImage" label="支持图片输入（识图）" color="primary" density="compact" hide-details />
                        <span class="text-caption text-medium-emphasis ml-3">开启后写入 options.attachment 与 options.modalities；本项目有图片理解层兜底</span>
                      </v-col>
                    </v-row>

                    <!-- Variants 配置 -->
                    <div class="variants-section mt-4">
                      <div class="d-flex align-center justify-space-between mb-2">
                        <div>
                          <div class="text-subtitle-2 font-weight-bold">思考等级 Variants</div>
                          <div class="text-caption text-medium-emphasis">开启后可在 OpenCode TUI 中按 Ctrl+T 切换思考等级</div>
                        </div>
                        <v-switch v-model="model.variantsEnabled" color="primary" density="compact" hide-details />
                      </div>
                      <v-expand-transition>
                        <div v-show="model.variantsEnabled" class="variants-list pl-1">
                          <div v-for="(variant, vi) in model.variants" :key="vi" class="variant-row d-flex align-center ga-2 mb-2">
                            <v-checkbox v-model="variant.enabled" density="compact" hide-details class="flex-grow-0" />
                            <v-text-field v-model.trim="variant.name" label="变体名称" variant="outlined" density="compact" hide-details style="max-width: 160px;" placeholder="high" />
                            <v-select v-model="variant.reasoningEffort" label="reasoningEffort" variant="outlined" density="compact" hide-details :items="reasoningEfforts" item-title="title" item-value="value" style="max-width: 200px;" />
                            <v-btn icon="mdi-delete" size="x-small" variant="text" @click="model.variants.splice(vi, 1)" />
                          </div>
                          <v-btn size="small" variant="tonal" prepend-icon="mdi-plus" @click="model.variants.push({ name: '', reasoningEffort: 'medium', enabled: true })">添加变体</v-btn>
                        </div>
                      </v-expand-transition>
                    </div>

                    <v-col cols="12" class="pa-0 mt-4">
                      <v-textarea v-model="model.optionsText" label="模型高级 options JSON" variant="outlined" density="comfortable" rows="3" auto-grow placeholder='{ "key": "value" }' />
                    </v-col>
                  </v-expansion-panel-text>
                </v-expansion-panel>
              </v-expansion-panels>
            </v-card-text>
          </v-card>

          <v-card v-else elevation="0" class="empty-editor d-flex align-center justify-center text-center pa-8">
            <div>
              <v-icon size="44" color="primary" class="mb-3">mdi-cog</v-icon>
              <div class="text-subtitle-1 font-weight-bold">选择或添加一个提供商</div>
              <div class="text-body-2 text-medium-emphasis mt-1">在这里配置 OpenCode 使用的协议、密钥和模型。</div>
            </div>
          </v-card>
        </v-col>
      </v-row>

    </template>

    <v-snackbar v-model="notice.visible" :color="notice.type" location="top right" :timeout="3500">{{ notice.message }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AgentConfigHeader from '@/components/AgentConfigHeader.vue'
import AgentConfigLocation from '@/components/AgentConfigLocation.vue'
import ToolInstallBanner from '@/components/ToolInstallBanner.vue'
import { api, type OpenCodeConfig, type OpenCodeProtocol, type OpenCodeProvider, type OpenCodeVariant, type SaveOpenCodeProvider, type ApiTab } from '@/services/api'
import { useProxyProtocolPick, defaultQuickPickTypeOptions } from '@/composables/useChannelQuickPick'
import { filterProxyModelsByKind, importProxyModels, buildOpenCodeModel } from '@/composables/useChannelModelImport'
import { PROTOCOL_DEFAULTS } from '@/composables/channelDefaults'
import { useAuthStore } from '@/stores/auth'

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

const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const config = ref<OpenCodeConfig | null>(null)
const providers = ref<EditableProvider[]>([])
const selectedProviderId = ref('')
const notice = ref({ visible: false, type: 'success', message: '' })

// 连接本代理（协议选择）
const quickPickTypeOptions = defaultQuickPickTypeOptions

// 渠道协议 -> OpenCode 协议映射
const protocolForChannelType: Record<ApiTab, OpenCodeProtocol> = {
  messages: 'messages',
  responses: 'responses',
  gemini: 'gemini',
  chat: 'chat',
  images: 'chat'
}

const applyProtocol = async (type: ApiTab, defaultBaseUrl: Promise<string>) => {
  const provider = selectedProvider.value
  if (!provider) return
  // 自动切换协议与 SDK 包
  provider.protocol = protocolForChannelType[type]
  applyProtocolNpm(provider)
  // Base URL 默认填本代理地址，可手动改
  provider.baseUrl = await defaultBaseUrl
  // 密钥填本代理访问 key（Web 鉴权与代理鉴权共用同一 key）
  const authStore = useAuthStore()
  if (authStore.apiKey) {
    provider.apiKey = authStore.apiKey
    provider.apiKeyAction = 'replace'
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
  loading.value = true
  loadError.value = ''
  try {
    const loaded = await api.getOpenCodeConfig()
    config.value = loaded
    providers.value = loaded.providers.map(toEditableProvider)
    selectedProviderId.value = providers.value[0]?.id ?? ''
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '加载 OpenCode 配置失败'
  } finally {
    loading.value = false
  }
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
  if (!provider || !window.confirm(`删除提供商“${provider.name || provider.id}”及其模型？`)) return
  providers.value = providers.value.filter(item => item !== provider)
  selectedProviderId.value = providers.value[0]?.id ?? ''
}

// 新增模型默认值：用 channelDefaults 的 chat 协议默认值（未选渠道时的合理兜底）
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
  if (!provider || !quickPickType.value) return
  try {
    const response = await api.getProxyModels()
    const modelIds = filterProxyModelsByKind(response.data, quickPickType.value)
    if (modelIds.length === 0) {
      notice.value = { visible: true, type: 'error', message: `本代理 ${quickPickType.value} 协议分组下暂无模型，请先在渠道管理中配置分组` }
      return
    }
    const count = importProxyModels(quickPickType.value, modelIds, buildOpenCodeModel, (models) => {
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
    notice.value = { visible: true, type: 'success', message: `已导入本代理 ${quickPickType.value} 分组的 ${count} 个模型（已替换原有模型）` }
  } catch (importError) {
    notice.value = { visible: true, type: 'error', message: importError instanceof Error ? importError.message : '获取本代理模型列表失败' }
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

const parseJSONObject = (text: string, field: string): Record<string, unknown> => {
  if (!text.trim()) return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error(`${field} 必须是有效的 JSON 对象`)
  }
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error(`${field} 必须是 JSON 对象`)
  return parsed as Record<string, unknown>
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
  saving.value = true
  try {
    const payload = { providers: providers.value.map(prepareProvider) }
    const response = await api.saveOpenCodeConfig(payload)
    notice.value = { visible: true, type: 'success', message: `已保存到 ${response.path}` }
    await loadConfig()
  } catch (error) {
    notice.value = { visible: true, type: 'error', message: error instanceof Error ? error.message : '保存 OpenCode 配置失败' }
  } finally {
    saving.value = false
  }
}

onMounted(loadConfig)
</script>

<style scoped>
.variants-section { border: 1px solid rgba(var(--v-theme-on-surface), 0.12); border-radius: 8px; padding: 12px 16px; }
.variant-row { flex-wrap: nowrap; }
</style>
