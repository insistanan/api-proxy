<template>
  <div class="agent-config-page dsh-page">
    <AgentConfigHeader title="DSH 配置">
      <template #default>
        <UiButton variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadSettings">刷新</UiButton>
        <UiButton color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="loading" @click="saveSettings">保存配置</UiButton>
      </template>
    </AgentConfigHeader>

    <UiAlert v-if="error" type="error" variant="tonal" class="mb-5" closable @click:close="error = ''">{{ error }}</UiAlert>
    <ToolInstallBanner tool="dsh" class="mb-5" />
    <UiProgress v-if="loading" indeterminate color="primary" class="mb-5" />

    <template v-else>
      <AgentConfigLocation
        :label="settings?.exists ? '当前配置文件' : '首次保存将创建配置文件'"
        :path="settings?.path"
      >
        <UiBadge v-if="settings?.writable" size="x-small" color="success" label>可写</UiBadge>
        <UiBadge v-else size="x-small" color="error" label>不可写</UiBadge>
      </AgentConfigLocation>

      <!-- 默认模型（置顶，方便快速选定启动模型） -->
      <UiCard elevation="0" class="settings-card agent-config-panel mb-5">
        <div class="agent-config-panel-header">
          <div>
            <div class="agent-config-panel-title">默认模型</div>
            <!-- 写入 settings.yaml 的 agent-default-model 字段；DSH 启动时自动选择该模型 -->
          </div>
        </div>
        <hr class="border-0 border-t border-border" />
        <div class="p-4 p-5">
          <div class="grid grid-cols-12 gap-4">
            <div class="min-w-0 col-span-12 sm:col-span-6"  >
              <UiSelect
                v-model="defaultModelProvider"
                label="默认提供商"
                variant="outlined"
                density="comfortable"
                :items="providerKeys"
                clearable
              />
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-6"  >
              <UiSelect
                v-model="defaultModelModel"
                label="默认模型"
                variant="outlined"
                density="comfortable"
                :items="defaultModelOptions"
                item-title="label"
                item-value="id"
                clearable
                :disabled="!defaultModelProvider"
              />
            </div>
          </div>
        </div>
      </UiCard>

      <div class="grid grid-cols-12 gap-4 agent-provider-layout">
        <!-- 提供商列表 -->
        <div class="min-w-0 col-span-12 lg:col-span-4"  >
          <UiCard class="provider-list-card" elevation="0">
            <div class="agent-config-panel-header">
              <div class="agent-config-panel-title">提供商</div>
              <UiButton icon="mdi-plus" size="small" variant="text" title="添加提供商" aria-label="添加提供商" @click="addProvider" />
            </div>
            <hr class="border-0 border-t border-border" />
            <UiList v-if="providerKeys.length" density="comfortable" nav class="py-2">
              <UiListItem
                v-for="key in providerKeys"
                :key="key"
                :active="selectedProviderKey === key"
                :title="providers[key]?.displayName || key"
                :subtitle="key"
                rounded="sm"
                @click="selectedProviderKey = key"
              >
                <template #prepend><UiIcon size="20">mdi-server-network</UiIcon></template>
                <template #append>
                  <UiBadge v-if="providers[key]?.api" size="x-small" :color="protocolColor(providers[key]!.api!)" label>{{ protocolLabel(providers[key]!.api!) }}</UiBadge>
                </template>
              </UiListItem>
            </UiList>
            <EmptyState v-else variant="inline" icon="mdi-server-network" title="尚未配置提供商">
              <UiButton size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addProvider">添加提供商</UiButton>
            </EmptyState>
          </UiCard>
        </div>

        <!-- 提供商编辑器 -->
        <div class="min-w-0 col-span-12 lg:col-span-8"  >
          <UiCard v-if="selectedProvider" elevation="0" class="provider-editor-card">
            <div class="agent-config-panel-header">
              <div>
                <div class="agent-config-panel-title">{{ selectedProvider.displayName || selectedProviderKey || '新提供商' }}</div>
                <!-- 写入 DSH settings.yaml 的 llm-pi-ai.providers 字段 -->
              </div>
              <div class="agent-config-panel-actions">
                <UiButton color="error" variant="text" size="small" prepend-icon="mdi-delete" @click="removeProvider(selectedProviderKey)">删除</UiButton>
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
                  <UiInput
                    v-model.trim="routeIdDraft"
                    label="路由标识 (route id)"
                    variant="outlined"
                    density="comfortable"
                    @blur="commitRouteId"
                    @keyup.enter="commitRouteId"
                  />
                </div>
                <div class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiInput v-model.trim="selectedProvider.displayName" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 Local Proxy" />
                </div>
                <div class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiSelect
                    v-model="selectedProvider.api"
                    label="API 协议"
                    variant="outlined"
                    density="comfortable"
                    :items="apiProtocols"
                    item-title="title"
                    item-value="value"
                  />
                </div>
                <div class="min-w-0 col-span-12 sm:col-span-6"  >
                  <UiInput v-model.trim="selectedProvider.baseURL" label="Base URL" variant="outlined" density="comfortable" placeholder="例如 http://127.0.0.1:9996" />
                </div>
                <div class="min-w-0 col-span-12" >
                  <UiInput
                    v-model.trim="selectedProvider.apiKeyEnv"
                    label="凭据引用名 (apiKeyEnv，可自动生成)"
                    variant="outlined"
                    density="comfortable"
                    placeholder="留空自动生成，例如 LOCAL_API_KEY"
                  />
                </div>
                <div class="min-w-0 col-span-12" >
                  <UiInput
                    v-model="selectedProvider.apiKey"
                    label="API Key"
                    type="password"
                    variant="outlined"
                    density="comfortable"
                    autocomplete="new-password"
                    :placeholder="selectedProvider.apiKeyPresent ? `当前：${selectedProvider.apiKeyMasked}（留空则保留）` : '填写 API Key；点击“连接本代理”会自动带入当前访问密钥'"
                    @update:model-value="onAPIKeyInput(selectedProvider)"
                  >
                    <template #append-inner>
                      <UiButton
                        v-if="selectedProvider.apiKeyPresent"
                        icon="mdi-delete"
                        size="x-small"
                        variant="text"
                        title="清除已保存密钥"
                        aria-label="清除已保存密钥"
                        @click.stop="clearAPIKey(selectedProvider)"
                      />
                    </template>
                  </UiInput>
                  <!-- 若系统存在同名环境变量，DSH 官方规则会优先使用环境值；通常无需手动设置环境变量。 -->
                </div>
              </div>

              <!-- 模型列表 -->
              <div class="flex items-center justify-between mt-7 mb-3">
                <div>
                  <div class="text-base font-medium font-bold">模型</div>
                </div>
                <div class="flex gap-2">
                  <UiButton size="small" color="secondary" variant="tonal" prepend-icon="mdi-download" :disabled="!quickPickType" @click="importProxyModelsToProvider">导入本代理模型</UiButton>
                  <UiButton size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addModel(selectedProvider)">添加模型</UiButton>
                </div>
              </div>

              <EmptyState v-if="!selectedProvider.models || selectedProvider.models.length === 0" variant="inline" icon="mdi-format-list-bulleted" title="添加至少一个模型后，它才会出现在 DSH 的模型选择器中。" />
              <UiAccordion v-else variant="accordion" class="model-panels">
                <UiAccordionItem v-for="(model, idx) in selectedProvider.models" :key="idx">
                  <template #title>
                    <div class="flex items-center gap-2 overflow-hidden">
                      <span class="font-medium truncate">{{ model.name || model.id || '新模型' }}</span>
                      <span v-if="model.id" class="text-xs text-muted-foreground truncate">{{ model.id }}</span>
                    </div>
                  </template>
                  <div class="px-4 pb-4">
                    <div class="grid grid-cols-12 gap-4">
                      <div class="min-w-0 col-span-12 sm:col-span-6"  >
                        <UiInput v-model.trim="model.id" label="模型 ID" variant="outlined" density="comfortable" placeholder="例如 opus、deepseek" />
                      </div>
                      <div class="min-w-0 col-span-12 sm:col-span-6"  >
                        <UiInput v-model.trim="model.name" label="显示名称" variant="outlined" density="comfortable" placeholder="例如 Claude Opus" />
                      </div>
                    </div>

                    <!-- 输入模态 -->
                    <div class="flex items-center gap-4 mt-3 mb-3">
                      <span class="text-sm font-medium font-bold">输入模态</span>
                      <UiCheckbox v-model="modelInputFlags[model.id || `__${idx}`].text" label="文本" density="compact" hide-details />
                      <UiCheckbox v-model="modelInputFlags[model.id || `__${idx}`].image" label="图片" density="compact" hide-details />
                    </div>

                    <!-- 推理等级 -->
                    <div class="reasoning-section mt-4">
                      <div class="flex items-center justify-between mb-2">
                        <div>
                          <div class="text-sm font-medium font-bold">推理等级 (Reasoning Efforts)</div>
                        </div>
                        <UiSwitch v-model="modelReasoningEnabled[model.id || `__${idx}`]" color="primary" density="compact" hide-details :label="modelReasoningEnabled[model.id || `__${idx}`] ? '支持' : '不支持'" />
                      </div>
                      <Transition name="ui-expand">
                        <div v-show="modelReasoningEnabled[model.id || `__${idx}`]" class="reasoning-levels pl-1">
                          <div v-for="level in reasoningLevels" :key="level.value" class="reasoning-row flex items-center gap-2 mb-2">
                            <UiCheckbox v-model="modelReasoningFlags[model.id || `__${idx}`][level.value]" density="compact" hide-details class="grow-0" />
                            <UiBadge :color="level.color" size="small" label>{{ level.title }}</UiBadge>
                            <span class="text-xs text-muted-foreground">{{ level.description }}</span>
                          </div>
                        </div>
                      </Transition>
                    </div>

                    <!-- 上下文与输出限制（对应 DSH settings.yaml 的 contextWindow / maxTokens 字段） -->
                    <div class="grid grid-cols-12 gap-4 mt-4">
                      <div class="min-w-0 col-span-12 sm:col-span-6"  >
                        <UiInput
                          v-model.number="model.contextWindow"
                          label="最大上下文 (contextWindow)"
                          variant="outlined"
                          density="comfortable"
                          type="number"
                          min="1"
                          placeholder="例如 131072"
                        />
                      </div>
                      <div class="min-w-0 col-span-12 sm:col-span-6"  >
                        <UiInput
                          v-model.number="model.maxTokens"
                          label="最大输出长度 (maxTokens)"
                          variant="outlined"
                          density="comfortable"
                          type="number"
                          min="1"
                          placeholder="例如 8192"
                        />
                      </div>
                    </div>

                    <div class="flex justify-end mt-4">
                      <UiButton size="small" variant="text" color="error" prepend-icon="mdi-delete" @click="removeModel(selectedProvider, idx)">删除模型</UiButton>
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
              <!-- 在这里配置 DSH 使用的协议、API Key 和模型。 -->
            </div>
          </UiCard>
        </div>
      </div>
    </template>

    <AgentPromptCard agent="dsh" class="mt-5" />
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
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, onMounted, ref, watch } from 'vue'
import AgentConfigHeader from '@/features/clients/components/AgentConfigHeader.vue'
import AgentConfigLocation from '@/features/clients/components/AgentConfigLocation.vue'
import AgentPromptCard from '@/features/clients/components/AgentPromptCard.vue'
import ToolInstallBanner from '@/features/clients/components/ToolInstallBanner.vue'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { api, type DSHSettings, type DSHProvider, type DSHModel, type SaveDSHSettings, type ApiTab } from '@/api'
import { useProxyProtocolPick, defaultQuickPickTypeOptions } from '@/features/clients/composables/useProxyProtocolPick'
import { filterProxyModelsByKind, importProxyModels, buildDSHModel } from '@/features/clients/utils/proxyModelImport'
import { useAuthStore } from '@/shared/stores/auth'
import { useToast } from '@/shared/composables/useToast'
import { useConfirm } from '@/shared/composables/useConfirm'

const { showToast } = useToast()
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()

// ---- 常量 ----

const apiProtocols = [
  { title: 'Anthropic Messages', value: 'anthropic-messages' },
  { title: 'OpenAI Completions', value: 'openai-completions' },
  { title: 'OpenAI Responses', value: 'openai-responses' },
  { title: 'Gemini Generate Content', value: 'gemini-generate-content' }
]

const quickPickTypeOptions = defaultQuickPickTypeOptions

// 渠道协议 -> DSH 协议映射
const protocolForChannelType: Record<ApiTab, string> = {
  messages: 'anthropic-messages',
  responses: 'openai-responses',
  gemini: 'gemini-generate-content',
  chat: 'openai-completions',
  images: 'openai-completions',
  embeddings: 'openai-completions',
  decisions: 'openai-completions'
}

const reasoningLevels = [
  { value: 'off', title: 'Off', color: 'grey', description: '关闭思考' },
  { value: 'medium', title: 'Medium', color: 'info', description: '中等推理强度' },
  { value: 'high', title: 'High', color: 'success', description: '高推理强度' },
  { value: 'max', title: 'Max', color: 'warning', description: '最大推理强度' }
]

// ---- 状态 ----

const loading = ref(false)
const saving = ref(false)
const error = ref('')
const settings = ref<DSHSettings | null>(null)
const providers = ref<Record<string, DSHProvider>>({})
const providerKeys = ref<string[]>([])
const selectedProviderKey = ref('')
const defaultModelProvider = ref('')
const defaultModelModel = ref('')

// 模型输入模态的临时状态（按 model id 索引）
const modelInputFlags = ref<Record<string, { text: boolean; image: boolean }>>({})
// 模型推理等级启用状态
const modelReasoningEnabled = ref<Record<string, boolean>>({})
// 模型各推理等级的勾选状态
const modelReasoningFlags = ref<Record<string, Record<string, boolean>>>({})
// 路由标识编辑草稿
const routeIdDraft = ref('')

// ---- 计算属性 ----

const selectedProvider = computed(() => {
  if (!selectedProviderKey.value) return null
  return providers.value[selectedProviderKey.value] ?? null
})

const defaultModelOptions = computed(() => {
  if (!defaultModelProvider.value) return []
  const provider = providers.value[defaultModelProvider.value]
  if (!provider?.models) return []
  return provider.models.map(m => ({ id: m.id, label: m.name || m.id }))
})

// ---- 协议选择（连接本代理） ----

// DSH 的 apiKeyEnv 是凭据引用名。用户只填 API Key 时，为它生成一个稳定、
// 符合 DSH 引用格式的名称，避免把环境变量配置暴露成必填步骤。
const generatedAPIKeyReference = (routeId: string) => {
  let normalized = routeId.trim().toUpperCase().replace(/[^A-Z0-9_]/g, '_')
  if (!normalized) normalized = 'PROVIDER'
  if (/^[0-9]/.test(normalized)) normalized = `_${normalized}`
  return `${normalized}_API_KEY`
}

const ensureAPIKeyReference = (provider: DSHProvider, routeId = selectedProviderKey.value) => {
  if (!provider.apiKeyEnv?.trim()) {
    provider.apiKeyEnv = generatedAPIKeyReference(routeId)
  }
}

const applyProtocol = async (type: ApiTab, defaultBaseUrl: Promise<string>) => {
  const provider = selectedProvider.value
  if (!provider) return
  // 自动设置协议
  provider.api = protocolForChannelType[type]
  // Base URL 默认填本代理地址，可手动改
  provider.baseURL = await defaultBaseUrl
  // 生成 apiKeyEnv 引用名；引用名不是密钥值，实际值由下方凭据字段保存。
  if (!provider.apiKeyEnv?.trim()) {
    provider.apiKeyEnv = generatedAPIKeyReference(selectedProviderKey.value)
  }
  // 与 OpenCode / Codex / pi-agent 一致：连接本代理时复用当前管理页面访问密钥。
  const authStore = useAuthStore()
  if (authStore.apiKey) {
    provider.apiKey = authStore.apiKey
    provider.apiKeyPresent = true
    provider.apiKeyAction = 'replace'
  }
}

const { selectedType: quickPickType, defaultBaseUrl } = useProxyProtocolPick(applyProtocol)

// ---- 侦听器 ----

// 当切换选中的 provider 时，同步模型的 UI 状态和路由标识草稿
watch(selectedProviderKey, () => {
  routeIdDraft.value = selectedProviderKey.value
  syncModelFlags()
})

// ---- 方法 ----

// 一键导入本代理模型（按所选协议分组，导入即替换）
const importProxyModelsToProvider = async () => {
  const provider = selectedProvider.value
  const type = quickPickType.value
  if (!provider || !type) return
  try {
    const response = await api.getProxyModels(type)
    const modelIds = filterProxyModelsByKind(response.data, type)
    if (modelIds.length === 0) {
      showToast(`本代理 ${type} 协议分组下暂无模型，请先在渠道管理中配置分组`, 'error')
      return
    }
    const count = importProxyModels(type, modelIds, buildDSHModel, (models) => {
      provider.models = models as DSHModel[]
    })
    syncModelFlags()
    showToast(`已导入本代理 ${type} 分组的 ${count} 个模型（已替换原有模型）`, 'success')
  } catch (importError) {
    showToast(importError instanceof Error ? importError.message : '获取本代理模型列表失败', 'error')
  }
}

const syncModelFlags = () => {
  const provider = selectedProvider.value
  if (!provider?.models) return
  for (const [idx, model] of provider.models.entries()) {
    const key = model.id || `__${idx}`
    // 输入模态
    if (!modelInputFlags.value[key]) {
      modelInputFlags.value[key] = { text: false, image: false }
    }
    modelInputFlags.value[key].text = model.input?.includes('text') ?? false
    modelInputFlags.value[key].image = model.input?.includes('image') ?? false
    // 推理等级
    const efforts = model.reasoningEfforts
    modelReasoningEnabled.value[key] = efforts !== false && efforts !== undefined
    if (!modelReasoningFlags.value[key]) {
      modelReasoningFlags.value[key] = {}
    }
    const flags: Record<string, boolean> = {}
    if (efforts && typeof efforts === 'object') {
      for (const level of reasoningLevels) {
        flags[level.value] = level.value in efforts
      }
    } else {
      // 默认勾选全部
      for (const level of reasoningLevels) {
        flags[level.value] = true
      }
    }
    modelReasoningFlags.value[key] = flags
  }
}

// 从后端响应里安全取出 provider 集合与有序 key 列表。
// 后端在某些异常路径下（如历史构建的 nil slice）会返回 null，
// 直接 [...null] 会抛 "undefined is not iterable"，这里统一归一为 []。
const normalizeDSHSettings = (loaded: Partial<DSHSettings>) => {
  const providerMap = loaded.providers ?? {}
  const providerKeys = Array.isArray(loaded.providerKeys)
    ? loaded.providerKeys.filter(k => typeof k === 'string' && k in providerMap)
    : []
  const credentials = loaded.credentials ?? {}
  return { providerMap, providerKeys, credentials }
}

const loadSettings = async () => {
  loading.value = true
  error.value = ''
  try {
    const loaded = await api.getDSHSettings()
    settings.value = loaded
    const { providerMap, providerKeys: orderedKeys, credentials } = normalizeDSHSettings(loaded)
    providers.value = Object.fromEntries(Object.entries(providerMap).map(([key, provider]) => {
      const credential = credentials[key]
      return [key, {
        ...provider,
        apiKey: '',
        apiKeyMasked: credential?.apiKeyMasked ?? '',
        apiKeyPresent: credential?.apiKeyPresent ?? false,
        apiKeyAction: 'keep'
      }]
    }))
    // 后端给的顺序缺失时从 map key 补齐，保持"文件顺序优先、字母序兜底"的约定。
    providerKeys.value = orderedKeys.length
      ? orderedKeys
      : Object.keys(providerMap).sort()
    defaultModelProvider.value = loaded.defaultModel?.provider ?? ''
    defaultModelModel.value = loaded.defaultModel?.model ?? ''
    selectedProviderKey.value = providerKeys.value[0] ?? ''
    routeIdDraft.value = selectedProviderKey.value
    syncModelFlags()
  } catch (loadError) {
    error.value = loadError instanceof Error ? loadError.message : '加载 DSH 配置失败'
  } finally {
    loading.value = false
  }
}

const addProvider = () => {
  let index = providerKeys.value.length + 1
  let key = `provider-${index}`
  while (key in providers.value) {
    index += 1
    key = `provider-${index}`
  }
  providers.value[key] = {
    api: 'anthropic-messages',
    baseURL: '',
    apiKeyEnv: '',
    apiKey: '',
    apiKeyMasked: '',
    apiKeyPresent: false,
    apiKeyAction: 'keep',
    models: []
  }
  providerKeys.value.push(key)
  selectedProviderKey.value = key
}

const removeProvider = (key: string) => {
  if (!key) return
  const provider = providers.value[key]
  const name = provider?.displayName || key
  askConfirm({
    message: `删除提供商“${name}”及其所有模型？`,
    confirmText: '删除',
    action: () => {
      delete providers.value[key]
      providerKeys.value = providerKeys.value.filter(k => k !== key)
      if (defaultModelProvider.value === key) {
        defaultModelProvider.value = ''
        defaultModelModel.value = ''
      }
      selectedProviderKey.value = providerKeys.value[0] ?? ''
    }
  })
}

const addModel = (provider: DSHProvider) => {
  if (!provider.models) provider.models = []
  provider.models.push({ id: '', name: '', input: ['text'] })
  syncModelFlags()
}

const commitRouteId = () => {
  const newKey = routeIdDraft.value.trim()
  if (!newKey || newKey === selectedProviderKey.value) {
    routeIdDraft.value = selectedProviderKey.value
    return
  }
  if (newKey in providers.value) {
    showToast(`路由标识 "${newKey}" 已存在`, 'error')
    routeIdDraft.value = selectedProviderKey.value
    return
  }
  const oldKey = selectedProviderKey.value
  const provider = providers.value[oldKey]
  delete providers.value[oldKey]
  providers.value[newKey] = provider
  const idx = providerKeys.value.indexOf(oldKey)
  if (idx >= 0) providerKeys.value[idx] = newKey
  selectedProviderKey.value = newKey
  if (defaultModelProvider.value === oldKey) {
    defaultModelProvider.value = newKey
  }
}

const removeModel = (provider: DSHProvider, idx: number) => {
  if (!provider.models) return
  provider.models.splice(idx, 1)
  syncModelFlags()
}

// API Key 是写入型字段：GET 只带脱敏状态，输入框本身始终保持空白。
const onAPIKeyInput = (provider: DSHProvider) => {
  if (provider.apiKey?.trim()) {
    ensureAPIKeyReference(provider)
    provider.apiKeyAction = 'replace'
  } else if (provider.apiKeyAction !== 'remove') {
    provider.apiKeyAction = 'keep'
  }
}

const clearAPIKey = (provider: DSHProvider) => {
  provider.apiKey = ''
  provider.apiKeyMasked = ''
  provider.apiKeyPresent = false
  provider.apiKeyAction = 'remove'
}

// 将最大上下文/最大输出长度的输入值归一化：空值、非正数或非法数值统一转为 undefined，
// 回传后端时省略该字段，不写入 settings.yaml。
const normalizeModelNumber = (value: unknown): number | undefined => {
  if (typeof value !== 'number' || !Number.isFinite(value)) return undefined
  if (value <= 0) return undefined
  return Math.trunc(value)
}

// 从 UI 状态收集模型配置
const collectModelConfig = (model: DSHModel, idx: number): DSHModel => {
  const key = model.id || `__${idx}`
  const inputFlags = modelInputFlags.value[key] ?? { text: true, image: false }
  const input: string[] = []
  if (inputFlags.text) input.push('text')
  if (inputFlags.image) input.push('image')

  let reasoningEfforts: DSHModel['reasoningEfforts'] = undefined
  if (modelReasoningEnabled.value[key]) {
    const flags = modelReasoningFlags.value[key] ?? {}
    const efforts: Record<string, string | null> = {}
    for (const level of reasoningLevels) {
      if (flags[level.value]) {
        efforts[level.value] = level.value === 'off' ? null : level.value
      }
    }
    reasoningEfforts = Object.keys(efforts).length > 0 ? efforts : undefined
  } else {
    reasoningEfforts = false
  }

  return {
    id: model.id,
    name: model.name || undefined,
    input: input.length > 0 ? input : undefined,
    reasoningEfforts,
    contextWindow: normalizeModelNumber(model.contextWindow),
    maxTokens: normalizeModelNumber(model.maxTokens)
  }
}

const saveSettings = async () => {
  saving.value = true
  try {
    // 收集所有 provider 的模型配置。按 providerKeys 顺序构建，保证写出的
    // settings.yaml 中 provider 排列与列表一致（后端也按此顺序落盘）。
    const providersPayload: Record<string, DSHProvider> = {}
    const orderedKeys: string[] = []
    for (const key of providerKeys.value) {
      if (typeof key !== 'string' || !key || key in providersPayload) continue
      const provider = providers.value[key]
      if (!provider) continue
      if (provider.apiKeyAction === 'replace' && provider.apiKey?.trim()) {
        ensureAPIKeyReference(provider, key)
      }
      providersPayload[key] = {
        apiKeyEnv: provider.apiKeyEnv || undefined,
        api: provider.api || undefined,
        baseURL: provider.baseURL || undefined,
        displayName: provider.displayName || undefined,
        models: (provider.models ?? []).map((m, i) => collectModelConfig(m, i))
      }
      orderedKeys.push(key)
    }

    const credentialUpdates: NonNullable<SaveDSHSettings['credentialUpdates']> = {}
    for (const key of orderedKeys) {
      const provider = providers.value[key]
      if (!provider || !provider.apiKeyAction || provider.apiKeyAction === 'keep') continue
      credentialUpdates[key] = {
        action: provider.apiKeyAction,
        apiKey: provider.apiKeyAction === 'replace' ? provider.apiKey : undefined
      }
    }

    const defaultModel = (defaultModelProvider.value && defaultModelModel.value)
      ? { provider: defaultModelProvider.value, model: defaultModelModel.value }
      : null
    const hasCredentialUpdates = Object.keys(credentialUpdates).length > 0

    const response = await api.saveDSHSettings({
      providers: providersPayload,
      providerKeys: orderedKeys,
      defaultModel,
      credentialUpdates: hasCredentialUpdates ? credentialUpdates : undefined
    })
    showToast(
      hasCredentialUpdates
        ? `配置已保存；凭据已更新到 ${response.credentialsPath}`
        : `已保存到 ${response.path}`,
      'success',
    )
    await loadSettings()
  } catch (saveError) {
    showToast(saveError instanceof Error ? saveError.message : '保存 DSH 配置失败', 'error')
    // 原子写入已过提交点时，先重新读取两份文件的实际状态，避免页面继续展示旧快照。
    if (typeof saveError === 'object' && saveError !== null && 'committed' in saveError && saveError.committed === true) {
      await loadSettings()
    }
  } finally {
    saving.value = false
  }
}

const protocolLabel = (protocol: string) => apiProtocols.find(p => p.value === protocol)?.title ?? protocol
const protocolColor = (protocol: string) => {
  const colors: Record<string, string> = {
    'anthropic-messages': 'success',
    'openai-completions': 'info',
    'openai-responses': 'primary',
    'gemini-generate-content': 'deep-purple'
  }
  return colors[protocol] ?? 'default'
}

onMounted(loadSettings)
</script>

<style scoped>
@reference "@/styles/index.css";

.reasoning-section { @apply [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] rounded-control [padding:12px_16px]; }
.reasoning-row { @apply flex-nowrap; }
</style>
