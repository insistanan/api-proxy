<template>
  <div class="agent-config-page piagent-page">
    <AgentConfigHeader title="pi 配置" subtitle="管理当前服务运行用户的 pi-agent 模型供应商">
      <template #default>
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadAll">刷新</v-btn>
        <v-btn color="primary" prepend-icon="mdi-content-save" :loading="savingProvider" :disabled="!selectedProvider" @click="saveSelectedProvider">保存</v-btn>
      </template>
    </AgentConfigHeader>

    <v-alert v-if="disabled" type="warning" variant="tonal" class="mb-4">
      pi-agent 配置目录不可用。请确认 <code>~/.pi/agent</code> 目录存在并可写。
    </v-alert>

    <v-alert v-if="loadError" type="error" variant="tonal" class="mb-4" closable @click:close="loadError = ''">
      {{ loadError }}
    </v-alert>

    <v-alert v-if="notice.visible" :type="notice.type" variant="tonal" density="compact" class="mb-4" closable @click:close="notice.visible = false">
      {{ notice.message }}
    </v-alert>

    <ToolInstallBanner tool="pi" class="mb-4" />

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-4" />

    <template v-if="!disabled && !loading">
      <AgentConfigLocation v-if="status" label="配置目录" :path="status.configDir">
        <v-chip v-if="!status.exists" size="x-small" color="warning" label>目录不存在</v-chip>
        <v-chip v-else-if="!status.writable" size="x-small" color="error" label>不可写</v-chip>
        <v-chip v-else size="x-small" color="success" label>可写</v-chip>
      </AgentConfigLocation>

      <!-- 默认模型设置（置顶，方便快速选定启动模型） -->
      <v-card elevation="0" class="agent-config-panel mb-5">
        <div class="agent-config-panel-header">
          <div>
            <div class="agent-config-panel-title">默认模型设置</div>
            <div class="agent-config-panel-subtitle">写入 pi-agent 的 settings.json，其余字段保持不变</div>
          </div>
          <v-btn color="primary" prepend-icon="mdi-content-save" :loading="savingSettings" @click="saveModelSettings">保存设置</v-btn>
        </div>
        <v-divider />
        <v-card-text class="pa-5">
          <v-row>
            <v-col cols="12" md="6">
              <v-select v-model="modelSettings.defaultProvider" label="默认供应商" variant="outlined" density="comfortable" :items="providers.map(p => ({ title: p.name || p.id, value: p.id }))" item-title="title" item-value="value" clearable />
            </v-col>
            <v-col cols="12" md="6">
              <v-text-field v-model="modelSettings.defaultModel" label="默认模型" variant="outlined" density="comfortable" placeholder="例如 anthropic/claude-opus-4-8" />
            </v-col>
            <v-col cols="12" md="6">
              <v-select v-model="modelSettings.defaultThinkingLevel" label="默认思考等级" variant="outlined" density="comfortable" :items="thinkingLevelsSettings" item-title="title" item-value="value" clearable />
            </v-col>
          </v-row>
        </v-card-text>
      </v-card>

      <!-- 供应商 + 模型（单页上段） -->
      <v-row>
        <v-col cols="12" lg="4">
          <v-card class="provider-list-card" elevation="0">
            <div class="agent-config-panel-header">
              <div class="agent-config-panel-title">供应商</div>
              <v-btn icon="mdi-plus" size="small" variant="text" title="添加供应商" aria-label="添加供应商" @click="addProvider" />
            </div>
            <v-divider />
            <v-list v-if="providers.length" density="comfortable" nav class="py-2">
              <v-list-item v-for="provider in providers" :key="provider.localId" :active="selectedProviderId === provider.localId" :title="provider.name || provider.id" :subtitle="provider.id" rounded="sm" @click="selectProvider(provider.localId)">
                <template #prepend><v-icon size="20">mdi-server-network</v-icon></template>
                <template #append>
                  <v-chip v-if="provider.apiKeyPresent" size="x-small" color="success" label>密钥</v-chip>
                  <v-chip v-else-if="provider.hasOAuth" size="x-small" color="info" label>OAuth</v-chip>
                </template>
              </v-list-item>
            </v-list>
            <div v-else class="empty-providers text-center text-medium-emphasis px-6 py-10">
              <v-icon size="32" class="mb-3">mdi-server-network</v-icon>
              <div class="text-body-2">尚未配置供应商</div>
              <v-btn class="mt-4" size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addProvider">添加供应商</v-btn>
            </div>
          </v-card>
        </v-col>

        <v-col cols="12" lg="8">
          <v-card v-if="selectedProvider" elevation="0" class="provider-editor-card">
            <div class="agent-config-panel-header">
              <div>
                <div class="agent-config-panel-title">{{ selectedProvider.name || '新供应商' }}</div>
                <div class="agent-config-panel-subtitle">写入 pi-agent 的 models.json providers 字段</div>
              </div>
              <div class="agent-config-panel-actions">
                <v-tooltip text="连通性测试"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-test-tube" size="small" variant="text" aria-label="测试供应商连通性" :disabled="!isExistingProvider || discovering" :loading="testing" @click="testProvider" /></template></v-tooltip>
                <v-tooltip text="发现 OpenAI 兼容模型"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-magnify" size="small" variant="text" aria-label="发现 OpenAI 兼容模型" :disabled="!isExistingProvider || testing || (Boolean(selectedProvider.api) && !['openai-completions', 'openai-responses'].includes(selectedProvider.api))" :loading="discovering" @click="discoverModels" /></template></v-tooltip>
                <v-btn color="error" variant="text" size="small" prepend-icon="mdi-delete" :disabled="!isExistingProvider" @click="removeProvider(selectedProvider.id)">删除</v-btn>
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
                  <v-text-field v-model="selectedProvider.id" label="供应商标识" variant="outlined" density="comfortable" :disabled="isExistingProvider" hint="例如 anthropic；模型引用将使用 provider/model" persistent-hint />
                </v-col>
                <v-col cols="12" sm="6">
                  <v-text-field v-model="selectedProvider.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 Anthropic" />
                </v-col>
                <v-col cols="12" sm="6">
                  <v-select v-model="selectedProvider.api" label="API 协议" variant="outlined" density="comfortable" :items="apiProtocols" item-title="title" item-value="value" clearable placeholder="不设置时使用默认协议" />
                </v-col>
                <v-col cols="12" sm="6">
                  <v-text-field v-model="selectedProvider.baseUrl" label="Base URL" variant="outlined" density="comfortable" placeholder="例如 https://api.anthropic.com" />
                </v-col>
                <v-col cols="12" sm="6">
                  <v-text-field v-model="selectedProvider.apiKey" label="API Key" type="password" variant="outlined" density="comfortable" :placeholder="selectedProvider.apiKeyPresent ? '当前：' + selectedProvider.apiKeyMasked + '（留空则保留）' : '填写 API Key'" />
                </v-col>
              </v-row>
              <v-expansion-panels variant="accordion" class="mt-2">
                <v-expansion-panel title="高级选项（请求头 / compat / 模型覆盖）">
                  <v-expansion-panel-text>
                    <v-row>
                      <v-col cols="12" md="4">
                        <v-textarea v-model="selectedProvider.headersText" label="请求头 JSON" variant="outlined" density="comfortable" rows="4" auto-grow />
                      </v-col>
                      <v-col cols="12" md="4">
                        <v-textarea v-model="selectedProvider.compatText" label="compat 兼容配置 JSON" variant="outlined" density="comfortable" rows="4" auto-grow />
                      </v-col>
                      <v-col cols="12" md="4">
                        <v-textarea v-model="selectedProvider.modelOverridesText" label="模型覆盖 JSON" variant="outlined" density="comfortable" rows="4" auto-grow />
                      </v-col>
                    </v-row>
                  </v-expansion-panel-text>
                </v-expansion-panel>
              </v-expansion-panels>

              <div class="d-flex align-center justify-space-between mt-6 mb-3">
                <div>
                  <div class="text-subtitle-1 font-weight-bold">模型</div>
                  <div class="text-caption text-medium-emphasis">模型 ID 必须与上游 API 接收的模型名一致</div>
                </div>
                <div class="d-flex ga-2">
                  <v-btn size="small" color="secondary" variant="tonal" prepend-icon="mdi-download" :disabled="!quickPickType" @click="importProxyModelsToProvider">导入本代理模型</v-btn>
                  <v-btn size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addModel">添加模型</v-btn>
                </div>
              </div>

              <div v-if="selectedProvider.models.length === 0" class="empty-models text-body-2 text-medium-emphasis py-6 text-center">添加至少一个模型后，它才会出现在 pi-agent 的模型列表中。</div>
              <v-expansion-panels v-else multiple variant="accordion" class="model-panels">
                <v-expansion-panel v-for="(model, index) in selectedProvider.models" :key="model.localId">
                  <template #title>
                    <div class="d-flex align-center ga-2 overflow-hidden">
                      <span class="font-weight-medium text-truncate">{{ model.name || model.id || '新模型' }}</span>
                      <span v-if="model.id" class="text-caption text-medium-emphasis text-truncate">{{ model.id }}</span>
                    </div>
                  </template>
                  <v-expansion-panel-text>
                    <!-- 主界面：精简字段 -->
                    <v-row>
                      <v-col cols="12" sm="6">
                        <v-text-field v-model="model.id" label="模型 ID" variant="outlined" density="comfortable" placeholder="例如 claude-opus-4-8" />
                      </v-col>
                      <v-col cols="12" sm="6">
                        <v-text-field v-model="model.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 Claude Opus 4.8" />
                      </v-col>
                      <v-col cols="12" sm="6">
                        <v-select v-model="model.reasoning" label="思考能力" variant="outlined" density="comfortable" :items="reasoningOptions" item-title="title" item-value="value" />
                      </v-col>
                      <v-col cols="12" sm="6" class="d-flex align-center">
                        <span class="text-subtitle-2 font-weight-bold mr-3">输入能力</span>
                        <v-checkbox v-model="model.inputTextEnabled" label="文本" density="compact" hide-details />
                        <v-checkbox v-model="model.inputImage" label="图片（识图）" density="compact" hide-details />
                      </v-col>
                    </v-row>

                    <!-- 思考等级映射：常驻展示（不放进折叠面板），仅思考能力启用时显示，与 writer 的 reasoning === true 分支一致 -->
                    <div v-if="model.reasoning === true" class="mt-3 mb-1">
                      <div class="text-body-2 font-weight-medium">思考等级</div>
                      <div class="text-caption text-medium-emphasis">勾选 = 模型支持该等级；右侧可填上游实际取值，留空即等级名；未勾选 = 不支持（pi 中隐藏）</div>
                    </div>
                    <v-row v-if="model.reasoning === true">
                      <v-col v-for="tl in thinkingLevelsList" :key="tl.key" cols="6" sm="4" md="3">
                        <div class="d-flex align-center ga-1">
                          <v-checkbox v-model="model.tlEnabled[tl.key]" :label="tl.label" density="compact" hide-details class="tl-checkbox" />
                          <v-text-field v-model="model.tlValue[tl.key]" :disabled="!model.tlEnabled[tl.key]" variant="outlined" density="compact" :placeholder="tl.key" hide-details style="max-width: 100px;" />
                        </div>
                      </v-col>
                    </v-row>

                    <!-- 高级：上下文窗口 / 费用 / JSON -->
                    <v-expansion-panels variant="accordion" class="mt-2">
                      <v-expansion-panel title="高级（上下文窗口 / 费用 / JSON）">
                        <v-expansion-panel-text>
                          <v-row>
                            <v-col cols="12" sm="6">
                              <v-text-field v-model.number="model.contextWindow" label="上下文窗口" type="number" min="0" variant="outlined" density="comfortable" />
                            </v-col>
                            <v-col cols="12" sm="6">
                              <v-text-field v-model.number="model.maxTokens" label="最大输出 Token" type="number" min="0" variant="outlined" density="comfortable" />
                            </v-col>
                          </v-row>

                          <v-row class="mt-2">
                            <v-col cols="6" sm="3">
                              <v-text-field v-model.number="model.costInput" label="费用-输入" type="number" min="0" step="0.01" variant="outlined" density="comfortable" />
                            </v-col>
                            <v-col cols="6" sm="3">
                              <v-text-field v-model.number="model.costOutput" label="费用-输出" type="number" min="0" step="0.01" variant="outlined" density="comfortable" />
                            </v-col>
                            <v-col cols="6" sm="3">
                              <v-text-field v-model.number="model.costCacheRead" label="费用-缓存读取" type="number" min="0" step="0.01" variant="outlined" density="comfortable" />
                            </v-col>
                            <v-col cols="6" sm="3">
                              <v-text-field v-model.number="model.costCacheWrite" label="费用-缓存写入" type="number" min="0" step="0.01" variant="outlined" density="comfortable" />
                            </v-col>
                          </v-row>

                          <v-row class="mt-2">
                            <v-col cols="12" md="6">
                              <v-textarea v-model="model.headersText" label="请求头 JSON" variant="outlined" density="comfortable" rows="3" auto-grow />
                            </v-col>
                            <v-col cols="12" md="6">
                              <v-textarea v-model="model.compatText" label="compat JSON" variant="outlined" density="comfortable" rows="3" auto-grow />
                            </v-col>
                          </v-row>
                        </v-expansion-panel-text>
                      </v-expansion-panel>
                    </v-expansion-panels>

                    <div class="d-flex justify-end mt-2">
                      <v-btn size="small" color="error" variant="text" prepend-icon="mdi-delete" @click="removeModel(index)">删除模型</v-btn>
                    </div>
                  </v-expansion-panel-text>
                </v-expansion-panel>
              </v-expansion-panels>
            </v-card-text>

            <!-- 底部保存条：sticky 常驻视口底部，长表单滚动到底也能直接保存 -->
            <div class="provider-save-bar">
              <v-btn color="primary" prepend-icon="mdi-content-save" :loading="savingProvider" @click="saveSelectedProvider">保存</v-btn>
            </div>
          </v-card>

          <v-card v-else elevation="0" class="empty-editor">
            <div class="empty-providers text-center text-medium-emphasis px-6 py-16">
              <v-icon size="40" class="mb-3">mdi-server-network</v-icon>
              <div class="text-body-1 mb-2">选择一个供应商或创建新的供应商</div>
              <div class="text-caption">支持 anthropic-messages / openai-completions / openai-responses / google-generative-ai 四种协议</div>
            </div>
          </v-card>
        </v-col>
      </v-row>
    </template>

    <AgentPromptCard agent="pi" class="mt-5" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AgentConfigHeader from '@/components/AgentConfigHeader.vue'
import AgentConfigLocation from '@/components/AgentConfigLocation.vue'
import AgentPromptCard from '@/components/AgentPromptCard.vue'
import ToolInstallBanner from '@/components/ToolInstallBanner.vue'
import { api, type ApiError, type PiAgentProvider, type PiAgentStatus, type ApiTab } from '@/services/api'
import { useProxyProtocolPick, defaultQuickPickTypeOptions } from '@/composables/useChannelQuickPick'
import { importProxyModels, buildPiAgentModel } from '@/composables/useChannelModelImport'
import { useAuthStore } from '@/stores/auth'

const loading = ref(false)
const disabled = ref(false)
const loadError = ref('')
const notice = ref({ visible: false, type: 'success' as 'success' | 'error', message: '' })
const status = ref<PiAgentStatus | null>(null)

const apiProtocols = [
  { title: 'Anthropic Messages', value: 'anthropic-messages' },
  { title: 'OpenAI Completions', value: 'openai-completions' },
  { title: 'OpenAI Responses', value: 'openai-responses' },
  { title: 'Google Generative AI', value: 'google-generative-ai' }
]
const reasoningOptions = [
  { title: '未设置', value: null },
  { title: '启用思考', value: true },
  { title: '禁用思考', value: false }
]

const thinkingLevelsList = [
  { key: 'minimal', label: '最低' },
  { key: 'low', label: '低' },
  { key: 'medium', label: '中' },
  { key: 'high', label: '高' },
  { key: 'xhigh', label: '极高' },
  { key: 'max', label: '最高' }
]

const thinkingLevelsSettings = [
  { title: '关闭', value: 'off' },
  { title: '最低', value: 'minimal' },
  { title: '低', value: 'low' },
  { title: '中', value: 'medium' },
  { title: '高', value: 'high' },
  { title: '极高', value: 'xhigh' },
  { title: '最高', value: 'max' }
]

// 渠道类型 -> pi-agent 协议映射
const protocolForChannelType: Record<ApiTab, string> = {
  messages: 'anthropic-messages',
  responses: 'openai-responses',
  gemini: 'google-generative-ai',
  chat: 'openai-completions',
  images: 'openai-completions'
}

// ---- 编辑模型接口 ----
interface EditableModel {
  localId: string
  id: string
  name: string
  api: string
  baseUrl: string
  reasoning: boolean | null
  contextWindow: number | null
  maxTokens: number | null
  costInput: number | null
  costOutput: number | null
  costCacheRead: number | null
  costCacheWrite: number | null
  tlEnabled: Record<string, boolean>
  tlValue: Record<string, string>
  inputTextEnabled: boolean
  inputImage: boolean
  headersText: string
  compatText: string
}

interface EditableProvider {
  localId: string
  persisted: boolean
  id: string
  name: string
  api: string
  baseUrl: string
  apiKey: string
  authHeader: boolean | null
  headersText: string
  compatText: string
  modelOverridesText: string
  models: EditableModel[]
  hasOAuth: boolean
  apiKeyMasked: string
  apiKeyPresent: boolean
}

const providers = ref<EditableProvider[]>([])
const selectedProviderId = ref('')
const providersRevision = ref('')
const savingProvider = ref(false)
const testing = ref(false)
const discovering = ref(false)

const selectedProvider = computed(() => providers.value.find(p => p.localId === selectedProviderId.value) ?? null)
const isExistingProvider = computed(() => Boolean(selectedProvider.value?.persisted))
const localID = () => `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`

// 安全字符串：任何 undefined/null 都转为 ''
const s = (v: unknown): string => (v == null ? '' : String(v))

// 安全 JSON 化
const toJSON = (v: unknown): string => {
  if (!v || typeof v !== 'object') return ''
  const keys = Object.keys(v as Record<string, unknown>)
  return keys.length ? JSON.stringify(v, null, 2) : ''
}

// 安全 JSON 解析
const parseJSON = (text: string, field: string): Record<string, unknown> => {
  if (!text.trim()) return {}
  let parsed: unknown
  try { parsed = JSON.parse(text) } catch { throw new Error(`${field} 必须是有效的 JSON 对象`) }
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error(`${field} 必须是 JSON 对象`)
  return parsed as Record<string, unknown>
}

// 连接本代理（协议选择）
const quickPickTypeOptions = defaultQuickPickTypeOptions

const applyProtocol = async (type: ApiTab, defaultBaseUrl: Promise<string>) => {
  const provider = selectedProvider.value
  if (!provider) return
  // 自动切换协议
  provider.api = protocolForChannelType[type]
  // Base URL 默认填本代理地址，可手动改；OpenAI 协议 SDK 不会自动补 /v1，需要显式带上
  const proxyBaseUrl = (await defaultBaseUrl).replace(/\/$/, '')
  provider.baseUrl = provider.api.startsWith('openai-') ? `${proxyBaseUrl}/v1` : proxyBaseUrl
  // 密钥填本代理访问 key（Web 鉴权与代理鉴权共用同一 key）
  const authStore = useAuthStore()
  if (authStore.apiKey) {
    provider.apiKey = authStore.apiKey
  }
}

const { selectedType: quickPickType, defaultBaseUrl } = useProxyProtocolPick(applyProtocol)

// 后端模型 → 编辑模型
const toEditableModel = (m: Record<string, unknown>): EditableModel => {
  const tlMap = (m.thinkingLevelMap as Record<string, string | null>) ?? {}
  const tlEnabled: Record<string, boolean> = {}
  const tlValue: Record<string, string> = {}
  for (const { key } of thinkingLevelsList) {
    // pi 语义：缺省 = 默认支持；null = 不支持（隐藏）；字符串 = 上游实际取值
    const val = tlMap[key]
    tlEnabled[key] = val !== null
    tlValue[key] = typeof val === 'string' ? val : ''
  }
  const cost = m.cost as Record<string, number> | undefined
  const input = (m.input as string[]) ?? []
  return {
    localId: localID(),
    id: s(m.id),
    name: s(m.name),
    api: s(m.api),
    baseUrl: s(m.baseUrl),
    reasoning: (m.reasoning as boolean | null) ?? null,
    contextWindow: (m.contextWindow as number | null) ?? null,
    maxTokens: (m.maxTokens as number | null) ?? null,
    costInput: cost?.input ?? null,
    costOutput: cost?.output ?? null,
    costCacheRead: cost?.cacheRead ?? null,
    costCacheWrite: cost?.cacheWrite ?? null,
    tlEnabled, tlValue,
    inputTextEnabled: input.includes('text'),
    inputImage: input.includes('image'),
    headersText: toJSON(m.headers),
    compatText: toJSON(m.compat)
  }
}

// 后端 provider → 编辑 provider
const toEditableProvider = (p: PiAgentProvider): EditableProvider => {
  const models = ((p.models as unknown as Record<string, unknown>[]) ?? []).map(toEditableModel)
  return {
    localId: localID(),
    persisted: true,
    id: s(p.id),
    name: s(p.name),
    api: s(p.api),
    baseUrl: s(p.baseUrl),
    apiKey: '',
    authHeader: (p.authHeader as boolean | null) ?? null,
    headersText: toJSON(p.headers),
    compatText: toJSON(p.compat),
    modelOverridesText: toJSON(p.modelOverrides),
    models,
    hasOAuth: (p.hasOAuth as boolean) ?? false,
    apiKeyMasked: s(p.apiKeyMasked),
    apiKeyPresent: (p.apiKeyPresent as boolean) ?? false
  }
}

// 编辑模型 → 后端模型对象
const toModelObj = (m: EditableModel): Record<string, unknown> => {
  const obj: Record<string, unknown> = {}
  obj.id = m.id
  if (m.name) obj.name = m.name
  if (m.api) obj.api = m.api
  if (m.baseUrl) obj.baseUrl = m.baseUrl
  if (m.reasoning !== null) obj.reasoning = m.reasoning
  if (m.contextWindow !== null) obj.contextWindow = m.contextWindow
  if (m.maxTokens !== null) obj.maxTokens = m.maxTokens
  const cost = { input: m.costInput, output: m.costOutput, cacheRead: m.costCacheRead, cacheWrite: m.costCacheWrite }
  if (Object.values(cost).some(v => v !== null && v !== 0)) obj.cost = cost
  // 仅 reasoning=true 时写映射：勾选 = 支持（留空取等级名），未勾选 = null（pi 中隐藏该等级）
  if (m.reasoning === true) {
    const tlMap: Record<string, string | null> = {}
    for (const { key } of thinkingLevelsList) {
      tlMap[key] = m.tlEnabled[key] ? (m.tlValue[key].trim() || key) : null
    }
    obj.thinkingLevelMap = tlMap
  }
  const input: string[] = []
  if (m.inputTextEnabled) input.push('text')
  if (m.inputImage) input.push('image')
  if (input.length) obj.input = input
  const h = parseJSON(m.headersText, 'headers'); if (Object.keys(h).length) obj.headers = h
  const c = parseJSON(m.compatText, 'compat'); if (Object.keys(c).length) obj.compat = c
  return obj
}

// 编辑 provider → 后端 provider 对象
const toProviderObj = (p: EditableProvider): Record<string, unknown> => {
  const obj: Record<string, unknown> = {}
  if (p.name) obj.name = p.name
  if (p.api) obj.api = p.api
  if (p.baseUrl) obj.baseUrl = p.baseUrl
  if (p.apiKey) obj.apiKey = p.apiKey
  if (p.authHeader !== null) obj.authHeader = p.authHeader
  const h = parseJSON(p.headersText, 'headers'); if (Object.keys(h).length) obj.headers = h
  const c = parseJSON(p.compatText, 'compat'); if (Object.keys(c).length) obj.compat = c
  const o = parseJSON(p.modelOverridesText, 'modelOverrides'); if (Object.keys(o).length) obj.modelOverrides = o
  const blankModel = p.models.find(m => !m.id.trim())
  if (blankModel) throw new Error('模型 ID 不能为空；请填写或删除空模型')
  const models = p.models.map(toModelObj)
  if (models.length) obj.models = models
  return obj
}

const selectProvider = (id: string) => { selectedProviderId.value = id }

const addProvider = () => {
  const draft: EditableProvider = {
    localId: localID(), persisted: false, id: '', name: '', api: '', baseUrl: '', apiKey: '',
    authHeader: null, headersText: '', compatText: '', modelOverridesText: '',
    models: [], hasOAuth: false, apiKeyMasked: '', apiKeyPresent: false
  }
  providers.value.push(draft)
  selectedProviderId.value = draft.localId
}

const makeDefaultTL = () => {
  const tlEnabled: Record<string, boolean> = {}
  const tlValue: Record<string, string> = {}
  for (const { key } of thinkingLevelsList) { tlEnabled[key] = false; tlValue[key] = '' }
  return { tlEnabled, tlValue }
}

const addModel = () => {
  if (!selectedProvider.value) return
  selectedProvider.value.models.push({
    localId: localID(), id: '', name: '', api: '', baseUrl: '',
    reasoning: null, contextWindow: null, maxTokens: null,
    costInput: null, costOutput: null, costCacheRead: null, costCacheWrite: null,
    ...makeDefaultTL(), inputTextEnabled: true, inputImage: false, headersText: '', compatText: ''
  })
}

// 一键导入本代理模型：导入本代理暴露的全部分组，不按协议过滤（分组名与协议无关，任一协议都可能配置同名分组）；导入即替换
const importProxyModelsToProvider = async () => {
  const provider = selectedProvider.value
  if (!provider || !quickPickType.value) return
  try {
    const response = await api.getProxyModels()
    const modelIds = response.data.map(entry => entry.id.trim()).filter(Boolean)
    if (modelIds.length === 0) {
      notice.value = { visible: true, type: 'error', message: '本代理暂无可导入的模型，请先在渠道管理中配置分组' }
      return
    }
    const count = importProxyModels(quickPickType.value, modelIds, buildPiAgentModel, (models) => {
      provider.models = models.map(m => ({
        localId: localID(),
        id: m.id,
        name: m.name,
        api: '',
        baseUrl: '',
        reasoning: m.reasoning,
        contextWindow: m.contextWindow,
        maxTokens: m.maxTokens,
        costInput: null,
        costOutput: null,
        costCacheRead: null,
        costCacheWrite: null,
        ...makeDefaultTL(),
        // 思考等级映射默认支持 medium/high/xhigh/max（取值即等级名，同 DSH 默认）
        tlEnabled: { minimal: false, low: false, medium: true, high: true, xhigh: true, max: true },
        tlValue: { minimal: '', low: '', medium: '', high: '', xhigh: '', max: '' },
        inputTextEnabled: m.input.includes('text'),
        inputImage: m.input.includes('image'),
        headersText: '',
        compatText: ''
      }))
    })
    notice.value = { visible: true, type: 'success', message: `已导入本代理全部分组的 ${count} 个模型（已替换原有模型）` }
  } catch (importError) {
    handleApiError(importError, '获取本代理模型列表失败')
  }
}

const removeModel = (index: number) => {
  if (!selectedProvider.value) return
  selectedProvider.value.models.splice(index, 1)
}

const saveSelectedProvider = async () => {
  const provider = selectedProvider.value
  if (!provider) return
  savingProvider.value = true
  try {
    if (!provider.id.trim()) throw new Error('供应商标识不能为空')
    const providerObj = toProviderObj(provider)
    const existing = providers.value.find(p => p.id === provider.id && p.localId !== provider.localId)
    if (existing) throw new Error(`供应商 ${provider.id} 已存在`)
    if (provider.persisted) {
      const result = await api.updatePiAgentProvider(provider.id, { revision: providersRevision.value, provider: providerObj })
      providersRevision.value = result.revision
      notice.value = { visible: true, type: 'success', message: `已更新供应商 ${provider.id}` }
    } else {
      const result = await api.createPiAgentProvider({ id: provider.id.trim(), revision: providersRevision.value, provider: providerObj })
      providersRevision.value = result.revision
      notice.value = { visible: true, type: 'success', message: `已创建供应商 ${provider.id}` }
    }
    await loadProviders()
    selectedProviderId.value = providers.value.find(p => p.id === provider.id)?.localId ?? ''
  } catch (error) {
    handleApiError(error, '保存供应商失败')
  } finally {
    savingProvider.value = false
  }
}

const removeProvider = async (id: string) => {
  const provider = providers.value.find(p => p.id === id)
  if (!provider || !window.confirm(`删除供应商“${provider.name || provider.id}”及其模型？`)) return
  try {
    const result = await api.deletePiAgentProvider(id, providersRevision.value)
    providersRevision.value = result.revision
    notice.value = { visible: true, type: 'success', message: `已删除供应商 ${id}` }
    await loadProviders()
    selectedProviderId.value = providers.value[0]?.localId ?? ''
  } catch (error) {
    handleApiError(error, '删除供应商失败')
  }
}

const testProvider = async () => {
  const provider = selectedProvider.value
  if (!provider) return
  testing.value = true
  try {
    if (!provider.baseUrl.trim()) throw new Error('请先填写 Base URL')
    const result = await api.testPiAgentProvider(provider.id, { baseUrl: provider.baseUrl.trim(), apiKey: provider.apiKey || undefined, api: provider.api || undefined })
    if (result.success) {
      notice.value = { visible: true, type: 'success', message: `连通性测试通过（${result.latencyMs}ms，HTTP ${result.statusCode}）` }
    } else {
      notice.value = { visible: true, type: 'error', message: result.error || '连通性测试失败' }
    }
  } catch (error) {
    handleApiError(error, '连通性测试失败')
  } finally {
    testing.value = false
  }
}

const discoverModels = async () => {
  const provider = selectedProvider.value
  if (!provider) return
  discovering.value = true
  try {
    if (!provider.baseUrl.trim()) throw new Error('请先填写 Base URL')
    const result = await api.discoverPiAgentModels(provider.id, { baseUrl: provider.baseUrl.trim(), apiKey: provider.apiKey || undefined, api: provider.api || undefined })
    if (result.success && result.models?.length) {
      const existing = new Set(provider.models.map(m => m.id).filter(Boolean))
      for (const modelID of result.models) {
        if (existing.has(modelID)) continue
        provider.models.push({
          localId: localID(), id: modelID, name: '', api: '', baseUrl: '',
          reasoning: null, contextWindow: null, maxTokens: null,
          costInput: null, costOutput: null, costCacheRead: null, costCacheWrite: null,
          ...makeDefaultTL(), inputTextEnabled: true, inputImage: false, headersText: '', compatText: ''
        })
        existing.add(modelID)
      }
      notice.value = { visible: true, type: 'success', message: `发现 ${result.models.length} 个模型候选，已加入列表（未保存）` }
    } else {
      notice.value = { visible: true, type: 'error', message: result.error || '未发现任何模型' }
    }
  } catch (error) {
    handleApiError(error, '模型发现失败')
  } finally {
    discovering.value = false
  }
}

// ---- 默认模型设置 ----
const modelSettings = ref({ defaultProvider: '', defaultModel: '', defaultThinkingLevel: '', enabledModels: [] as string[] })
const settingsRevision = ref('')
const savingSettings = ref(false)

const saveModelSettings = async () => {
  savingSettings.value = true
  try {
    const result = await api.updatePiAgentModelSettings({
      revision: settingsRevision.value,
      settings: {
        defaultProvider: modelSettings.value.defaultProvider || undefined,
        defaultModel: modelSettings.value.defaultModel || undefined,
        defaultThinkingLevel: modelSettings.value.defaultThinkingLevel || undefined,
        enabledModels: modelSettings.value.enabledModels
      }
    })
    settingsRevision.value = result.revision
    notice.value = { visible: true, type: 'success', message: '默认模型设置已保存' }
  } catch (error) {
    handleApiError(error, '保存默认模型设置失败')
  } finally {
    savingSettings.value = false
  }
}

// ---- 通用 ----
const handleApiError = (error: unknown, fallback: string) => {
  const apiError = error as ApiError
  if (apiError?.status === 403) {
    disabled.value = true
    notice.value = { visible: true, type: 'error', message: apiError.message || 'pi-agent 配置管理未启用' }
    return
  }
  if (apiError?.status === 409 || apiError?.status === 428) {
    notice.value = { visible: true, type: 'error', message: apiError.status === 428 ? '保存前需要先读取最新配置，已刷新，请重试' : '配置文件已被外部修改，已刷新，请重试' }
    loadAll(true)
    return
  }
  notice.value = { visible: true, type: 'error', message: apiError?.message || fallback }
}

const loadStatus = async (): Promise<boolean> => {
  try {
    status.value = await api.getPiAgentStatus()
    disabled.value = false
    return true
  } catch (error) { handleApiError(error, '加载 pi-agent 状态失败') }
  return false
}

const loadProviders = async () => {
  try {
    const result = await api.getPiAgentProviders()
    providersRevision.value = result.revision
    const prevSelectionId = selectedProvider.value?.id ?? ''
    providers.value = result.providers.map(toEditableProvider)
    const target = prevSelectionId ? providers.value.find(p => p.id === prevSelectionId) : undefined
    selectedProviderId.value = target?.localId ?? providers.value[0]?.localId ?? ''
  } catch (error) { handleApiError(error, '加载供应商失败') }
}

const loadModelSettings = async () => {
  try {
    const result = await api.getPiAgentModelSettings()
    settingsRevision.value = result.revision
    modelSettings.value = {
      defaultProvider: result.settings.defaultProvider ?? '',
      defaultModel: result.settings.defaultModel ?? '',
      defaultThinkingLevel: result.settings.defaultThinkingLevel ?? '',
      enabledModels: result.settings.enabledModels ?? []
    }
  } catch (error) { handleApiError(error, '加载默认模型设置失败') }
}

const loadAll = async (silent = false) => {
  if (!silent) loading.value = true
  loadError.value = ''
  try {
    if (!await loadStatus() || disabled.value) return
    await Promise.all([loadProviders(), loadModelSettings()])
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '加载 pi-agent 配置失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => loadAll())
</script>

<style scoped>
.mono-text { font-family: 'JetBrains Mono', 'Cascadia Code', monospace; }

/* v-card 默认 overflow:hidden 会让 sticky 失效；clip 不建立滚动容器，sticky 才能相对视口生效，圆角裁切效果不变 */
.provider-editor-card {
  overflow: clip;
}

.provider-save-bar {
  position: sticky;
  bottom: 0;
  z-index: 5;
  display: flex;
  justify-content: flex-end;
  padding: 12px 20px;
  background: rgb(var(--v-theme-surface));
  border-top: 1px solid rgba(var(--v-theme-outline), 0.4);
}
</style>
