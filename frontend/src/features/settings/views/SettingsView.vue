<template>
  <div class="settings-page">
    <PageHeader title="设置" description="按分类管理全局配置；切换分类会保留当前页面内的未保存修改。" />

    <div class="settings-layout">
      <nav class="settings-nav" aria-label="设置分类">
        <UiList v-model:selected="selectedSections" nav mandatory density="compact" bg-color="transparent">
          <UiListItem v-for="section in settingsSections" :key="section.key" :value="section.key" :prepend-icon="section.icon" :title="section.label" :aria-current="activeSection === section.key ? 'page' : undefined">
            <template #append><span v-if="!loading && dirtySections[section.key]" class="settings-dirty-dot" aria-label="有未保存的修改"></span></template>
          </UiListItem>
        </UiList>
      </nav>

      <div class="settings-content">
        <section v-if="activeSection === 'appearance'" aria-labelledby="appearance-title">
          <h2 id="appearance-title" class="mb-5 text-lg font-semibold">外观</h2>
          <ThemePanel />
        </section>
        <template v-else>
        <UiAlert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-5">
          {{ loadError }}
        </UiAlert>

        <div v-if="loading" class="min-h-28 animate-pulse rounded-card bg-muted"  ></div>
        <UiButton v-else-if="loadError" variant="tonal" prepend-icon="mdi-refresh" @click="loadSettings">重新加载设置</UiButton>

        <section v-else-if="activeSection === 'network'" aria-labelledby="network-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="network-settings-title" class="text-lg font-bold">网络</h2>
            </div>
          </div>

          <UiForm ref="formRef" @submit.prevent="saveNetworkSettings">
            <div class="setting-row">
              <div class="setting-copy">
                <div class="text-base font-medium">全局上游代理</div>
                <div class="text-sm text-muted-foreground mt-1">用于网关访问上游服务；单个渠道可选择继承、直连或独立代理。</div>
              </div>
              <UiSwitch v-model="proxyEnabled" color="primary" inset hide-details aria-label="启用全局上游代理" />
            </div>

            <Transition name="ui-expand">
              <div v-if="proxyEnabled" class="proxy-editor">
                <UiInput
                  v-model="proxyUrl"
                  label="代理地址"
                  placeholder="http://127.0.0.1:7897"
                  prepend-inner-icon="mdi-web"
                  variant="outlined"
                  density="comfortable"
                  autocomplete="off"
                  spellcheck="false"
                  :rules="[proxyUrlRule]"
                />
              </div>
            </Transition>

            <UiAlert v-if="notice" :type="notice.type" variant="tonal" density="compact" class="mt-5">
              {{ notice.message }}
            </UiAlert>

            <div class="settings-actions">
              <UiButton type="submit" color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="!networkDirty">
                保存网络设置
              </UiButton>
            </div>
          </UiForm>
        </section>

        <GatewaySettingsSection v-else-if="activeSection === 'gateway'" :editor="gatewayEditor" />

        <DisguiseSettingsSection v-else-if="activeSection === 'disguise'" :editor="disguiseEditor" />

        <PromptSettingsSection v-else-if="activeSection === 'prompts'" :editor="promptsEditor" />

        <ExtensionsSettingsSection v-else-if="activeSection === 'extensions'" :editor="extensionsEditor" />

        <section v-else aria-labelledby="content-safety-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="content-safety-settings-title" class="text-lg font-bold">内容安全</h2>
            </div>
            <UiButton variant="text" prepend-icon="mdi-format-list-bulleted" to="/blocked-logs">拦截记录</UiButton>
          </div>

          <form @submit.prevent="(!contentSafety.dangerousCmd.enabled || !contentSafety.dangerousCmd.aiGate.enabled || gateSelectionValid) && saveContentSafetySettings()">
            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">敏感数据处置</div>
                  <p v-if="contentSafety.sensitiveData.mode === 'mask'" class="mt-1 text-xs text-muted-foreground">
                    模型接收占位符，客户端正文与工具参数按已授权映射还原。跨轮恢复需要稳定会话标识；签名内容、敏感数字或无法还原的工具参数可能被阻断。
                  </p>
                </div>
                <UiToggleGroup
                  v-model="contentSafety.sensitiveData.mode"
                  mandatory
                  divided
                  density="compact"
                  class="mode-toggle"
                >
                  <UiButton value="block" prepend-icon="mdi-shield-lock" selected-class="disposal-toggle__block">
                    阻断
                  </UiButton>
                  <UiButton value="mask" prepend-icon="mdi-eye-off" selected-class="disposal-toggle__mask">
                    可逆脱敏
                  </UiButton>
                </UiToggleGroup>
              </div>
            </div>

            <UiAlert v-if="legacyWordFilterEnabled" type="info" variant="tonal" class="mb-4">
              当前服务端仍启用了旧敏感词检测。保存本页后将关闭，保留个人信息、凭据和危险命令保护。
            </UiAlert>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">个人信息保护</div>
                </div>
                <UiSwitch
                  v-model="contentSafety.sensitiveInfo.enabled"
                  color="primary"
                  inset
                  hide-details
                  aria-label="启用个人信息保护"
                />
              </div>
              <div v-if="contentSafety.sensitiveInfo.enabled" class="option-grid">
                <UiCheckbox
                  v-for="item in sensitiveInfoRules"
                  :key="item.value"
                  v-model="contentSafety.sensitiveInfo.enabledRules"
                  :value="item.value"
                  :label="item.label"
                  :disabled="!contentSafety.sensitiveInfo.enabled"
                  density="compact"
                  color="primary"
                  hide-details
                />
              </div>
              <Transition name="ui-expand">
                <div
                  v-if="
                    contentSafety.sensitiveInfo.enabled && contentSafety.sensitiveInfo.enabledRules.includes('ip_address')
                  "
                  class="mode-setting-row ip-scope-row"
                >
                  <span class="text-sm">IP 掩码范围</span>
                  <UiToggleGroup
                    v-model="contentSafety.sensitiveInfo.ipMaskScope"
                    mandatory
                    divided
                    density="compact"
                    class="mode-toggle"
                  >
                    <UiButton value="public" selected-class="scope-toggle__selected">仅公网地址</UiButton>
                    <UiButton value="all" selected-class="scope-toggle__selected">全部地址</UiButton>
                  </UiToggleGroup>
                </div>
              </Transition>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">凭据保护</div>
                </div>
                <UiSwitch
                  v-model="contentSafety.credential.enabled"
                  color="error"
                  inset
                  hide-details
                  aria-label="启用凭据保护"
                />
              </div>
              <div v-if="contentSafety.credential.enabled" class="option-grid">
                <UiCheckbox
                  v-for="item in credentialRules"
                  :key="item.value"
                  v-model="contentSafety.credential.enabledRules"
                  :value="item.value"
                  :label="item.label"
                  :disabled="!contentSafety.credential.enabled"
                  density="compact"
                  color="error"
                  hide-details
                />
              </div>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">危险命令检测</div>
                  <div class="text-sm text-muted-foreground mt-1">在交付 Bash、exec_command 等已知执行工具前检查。拦截会结束当前响应，Agent 可能报错或重试；本地权限和沙箱仍由 Agent 管理。</div>
                </div>
                <UiSwitch
                  v-model="contentSafety.dangerousCmd.enabled"
                  color="warning"
                  inset
                  hide-details
                  aria-label="启用危险命令检测"
                />
              </div>
              <div v-if="contentSafety.dangerousCmd.enabled" class="option-grid">
                <UiCheckbox
                  v-for="item in dangerousCommandRules"
                  :key="item.value"
                  v-model="contentSafety.dangerousCmd.enabledRules"
                  :value="item.value"
                  :label="item.label"
                  :disabled="!contentSafety.dangerousCmd.enabled"
                  density="compact"
                  color="warning"
                  hide-details
                />
              </div>
              <Transition name="ui-expand">
                <div v-if="contentSafety.dangerousCmd.enabled" class="ai-gate-panel">
                  <div class="setting-row ai-gate-head-row">
                    <div class="setting-copy">
                      <div class="text-base font-medium">
                        <UiIcon size="18" class="mr-1" color="warning">mdi-brain</UiIcon>
                        AI 闸门
                      </div>
                      <div class="text-sm text-muted-foreground mt-1">关闭时，规则命中直接拦截。开启后增加一次模型审批：AI 判安全立即交付，事后否决不能撤回执行；判危险才等待人工确认，超时或审批失败则拦截。</div>
                    </div>
                    <UiSwitch
                      v-model="contentSafety.dangerousCmd.aiGate.enabled"
                      color="warning"
                      inset
                      hide-details
                      aria-label="启用危险命令 AI 闸门"
                    />
                  </div>
                  <div v-if="contentSafety.dangerousCmd.aiGate.enabled" class="ai-gate-options">
                    <UiSelect
                      v-model="contentSafety.dangerousCmd.aiGate.protocol"
                      :items="gateProtocolItems"
                      label="审批协议"
                      variant="outlined"
                      density="compact"
                      class="ai-gate-select"
                      @update:model-value="resetGateTarget"
                    />
                    <ChannelPicker v-model="contentSafety.dangerousCmd.aiGate.channelId" v-model:selected-model="contentSafety.dangerousCmd.aiGate.model" :options="eligibleGateChannels" :loading="gateChannelsLoading" :error="gateChannelsError" show-model allow-auto label="判断渠道" model-label="审批模型" class="ai-gate-channel-picker" @valid="gateSelectionValid = $event" />
                    <UiInput
                      v-model.number="contentSafety.dangerousCmd.aiGate.waitSeconds"
                      type="number"
                      min="3"
                      max="120"
                      label="人工确认窗口（秒）"
                      variant="outlined"
                      density="compact"
                      class="ai-gate-wait"
                    />
                  </div>
                  <SemanticSafetySettings v-if="contentSafety.dangerousCmd.semantic" v-model="contentSafety.dangerousCmd.semantic" :dangerous-enabled="contentSafety.dangerousCmd.enabled" :gate-enabled="contentSafety.dangerousCmd.aiGate.enabled" />
                  <div v-if="contentSafety.dangerousCmd.aiGate.enabled" class="text-sm text-muted-foreground mt-2">
                    {{ gateProtocolHint }}
                  </div>
                </div>
              </Transition>
            </div>

            <details class="safety-group" :open="advancedSafetyActive">
              <summary class="cursor-pointer text-base font-medium">高级规则与放行例外{{ advancedSafetyActive ? '（已有启用项）' : '' }}</summary>
              <p class="mt-2 text-sm text-muted-foreground">用于处理误报和自定义敏感数据。工具白名单跳过该工具输入、输出的上行检测；放行值会绕过对应上行检测，请仅添加确认可公开的内容。</p>
            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">白名单</div>
                  <!-- 命中工具名的 tool_result 与 tool_argument 跳过全部检测，并在拦截记录里留审计条目 -->
                </div>
                <UiSwitch
                  v-model="contentSafety.whitelist.enabled"
                  color="success"
                  inset
                  hide-details
                  aria-label="启用白名单放行"
                />
              </div>
              <UiSelect
v-model="contentSafety.whitelist.toolNames" searchable
                creatable
                :disabled="!contentSafety.whitelist.enabled"
                label="放行工具名"
                placeholder="例如 tavily-search、tavily_search"
                multiple
                chips
                closable-chips
                variant="outlined"
                density="comfortable"
                hide-details="auto"
                class="mt-3"
              />
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">
                    <UiIcon size="18" class="mr-1" color="success">mdi-check-underline-circle</UiIcon>
                    放行字面量
                  </div>
                  <!-- 与命中片段完全相等的字符串跳过全部检测维度（整段相等跳过整个字段，子串相等丢弃该命中），出厂含 127.0.0.1 等回环地址 -->
                </div>
                <UiSwitch
                  v-model="contentSafety.allowlist.enabled"
                  color="success"
                  inset
                  hide-details
                  aria-label="启用放行字面量"
                />
              </div>
              <UiSelect
v-model="contentSafety.allowlist.entries" searchable
                creatable
                :disabled="!contentSafety.allowlist.enabled"
                label="放行字面量"
                placeholder="例如 10.0.0.5、sk-demo-12345"
                multiple
                chips
                closable-chips
                variant="outlined"
                density="comfortable"
                hide-details="auto"
                class="mt-3"
              />
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-base font-medium">
                    <UiIcon size="18" class="mr-1" color="warning">mdi-regex</UiIcon>
                    自定义检测规则
                  </div>
                  <!-- 自定义正则命中后走可逆脱敏（占位符发给模型，回答自动还原）；保护项目代号、内部人名等只有你自己知道的敏感信息。依赖"敏感数据处置 = 可逆脱敏" -->
                </div>
                <UiSwitch
                  v-model="contentSafety.customRules.enabled"
                  color="warning"
                  inset
                  hide-details
                  aria-label="启用自定义检测规则"
                />
              </div>
              <div v-if="contentSafety.customRules.enabled" class="custom-rules-editor">
                <div v-for="(rule, index) in contentSafety.customRules.rules" :key="index" class="custom-rule-row">
                  <UiInput
                    v-model="rule.name"
                    label="规则名"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-name"
                  />
                  <UiInput
                    v-model="rule.type"
                    label="类型前缀"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-type"
                  />
                  <UiInput
                    v-model="rule.pattern"
                    label="正则表达式"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-pattern"
                  />
                  <UiButton
                    icon
                    size="small"
                    variant="text"
                    color="error"
                    aria-label="删除规则"
                    @click="contentSafety.customRules.rules.splice(index, 1)"
                  >
                    <UiIcon size="18">mdi-delete</UiIcon>
                  </UiButton>
                </div>
                <UiButton
                  variant="tonal"
                  size="small"
                  color="primary"
                  prepend-icon="mdi-plus"
                  :disabled="contentSafety.customRules.rules.length >= 50"
                  @click="addCustomRule"
                >
                  添加规则
                </UiButton>
              </div>
            </div>

            </details>
            <p class="mt-3 text-sm text-muted-foreground">桌面提醒在右上角消息中心启用和测试。浏览器通知需要保持管理台打开。</p>

            <UiAlert v-if="notice" :type="notice.type" variant="tonal" density="compact" class="mt-5">
              {{ notice.message }}
            </UiAlert>

            <div class="settings-actions">
              <UiButton
                type="submit"
                color="primary"
                prepend-icon="mdi-content-save"
                :loading="saving"
                :disabled="!contentSafetyDirty || (contentSafety.dangerousCmd.enabled && contentSafety.dangerousCmd.aiGate.enabled && !gateSelectionValid)"
              >
                保存内容安全设置
              </UiButton>
            </div>
          </form>
        </section>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiToggleGroup from '@/shared/ui/button/ToggleGroup.vue'
import UiCheckbox from '@/shared/ui/check/Checkbox.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import ChannelPicker from '@/shared/components/domain/ChannelPicker.vue'
import {
  api,
  type ContentSafetySettings,
  type CredentialRule,
  type CustomRuleItem,
  type DangerousCommandRule,
  type SensitiveInfoRule
} from '@/api'

import GatewaySettingsSection from '@/features/settings/components/GatewaySettingsSection.vue'
import { useGatewaySettings } from '@/features/settings/composables/useGatewaySettings'
import DisguiseSettingsSection from '@/features/settings/components/DisguiseSettingsSection.vue'
import { useDisguiseSettings } from '@/features/settings/composables/useDisguiseSettings'
import PromptSettingsSection from '@/features/settings/components/PromptSettingsSection.vue'
import { usePromptSettings } from '@/features/settings/composables/usePromptSettings'
import ExtensionsSettingsSection from '@/features/settings/components/ExtensionsSettingsSection.vue'
import { useExtensionsSettings } from '@/features/settings/composables/useExtensionsSettings'
import { defaultContentSafety, normalizeContentSafety } from '@/features/settings/utils/contentSafetyForm'
import SemanticSafetySettings from '@/features/settings/components/SemanticSafetySettings.vue'
import ThemePanel from '@/shared/theme/components/ThemePanel.vue'
import type { SettingsSection, SettingsNotice as Notice } from '@/features/settings/composables/types'

const sensitiveInfoRules: Array<{ value: SensitiveInfoRule; label: string }> = [
  { value: 'phone', label: '手机号' },
  { value: 'id_card', label: '身份证号' },
  { value: 'email', label: '邮箱' },
  { value: 'ip_address', label: 'IP 地址' },
  { value: 'bank_card', label: '银行卡号' }
]

const credentialRules: Array<{ value: CredentialRule; label: string }> = [
  { value: 'api_key', label: '已知 API Key / Token' },
  { value: 'named_secret', label: '密码与命名密钥' },
  { value: 'private_key', label: 'PEM 私钥' },
  { value: 'connection_string', label: '数据库连接串' },
  { value: 'high_entropy', label: '高熵环境变量值' }
]

const dangerousCommandRules: Array<{ value: DangerousCommandRule; label: string }> = [
  { value: 'destructive', label: '破坏性命令' },
  { value: 'download_execute', label: '远程下载执行' },
  { value: 'reverse_shell', label: '反弹 Shell' },
  { value: 'privilege_escalation', label: '权限提升' },
  { value: 'environment_tampering', label: '环境变量篡改' }
]

// 审批候选按命令审核使用的协议筛选。
const gateProtocolItems = [
  { title: '大模型对话 API', value: 'chat' },
  { title: 'Decisions API（原生决策格式）', value: 'decisions' }
]
const gateChannels = ref<Awaited<ReturnType<typeof api.getGateChannels>>['channels']>([])
const gateChannelsLoading = ref(false)
const gateChannelsError = ref('')
const loadGateChannels = async () => {
  if (gateChannelsLoading.value) return
  gateChannelsLoading.value = true
  gateChannelsError.value = ''
  try {
    gateChannels.value = (await api.getGateChannels()).channels
  } catch (error) {
    gateChannelsError.value = error instanceof Error ? error.message : '读取审批渠道失败'
  } finally {
    gateChannelsLoading.value = false
  }
}
const isDecisionGate = computed(() => ['decisions', 'systemone'].includes(contentSafety.value.dangerousCmd.aiGate.protocol ?? 'chat'))
const eligibleGateChannels = computed(() => gateChannels.value.filter(channel => isDecisionGate.value ? channel.kind === 'decisions' : channel.kind !== 'decisions'))
const gateSelectionValid = ref(true)
const gateProtocolHint = computed(() => {
  switch (contentSafety.value.dangerousCmd.aiGate.protocol) {
    case 'systemone':
    case 'decisions': return '使用 Decisions 渠道调用 /v1/decisions，复用 state 和具名 questions 的原生决策格式；渠道启用完整 URL 时按配置地址发送。'
    default: return '使用 Messages、Responses、Chat 或 Gemini 渠道；只将命中本地规则的执行工具命令送审。'
  }
})

const formRef = ref()
const settingsSections: Array<{ key: SettingsSection; label: string; icon: string }> = [
  { key: 'appearance', label: '外观', icon: 'mdi-eye-dropper' },
  { key: 'network', label: '网络连接', icon: 'mdi-web' },
  { key: 'gateway', label: '请求与日志', icon: 'mdi-text' },
  { key: 'content-safety', label: '内容安全', icon: 'mdi-shield-alert' },
  { key: 'disguise', label: '客户端伪装', icon: 'mdi-incognito' },
  { key: 'prompts', label: '通用提示词', icon: 'mdi-text' },
  { key: 'extensions', label: '技能与 MCP', icon: 'mdi-puzzle-outline' }
]
const route = useRoute()
const router = useRouter()
const activeSection = computed<SettingsSection>(() => settingsSections.find(section => section.key === route.query.section)?.key ?? 'network')
const selectedSections = computed({
  get: () => [activeSection.value],
  set: (sections: SettingsSection[]) => {
    const section = sections[0]
    if (section) void router.push({ query: { ...route.query, section } })
  }
})
const loading = ref(true)
const saving = ref(false)
const loadError = ref('')

// 各分区共用保存标记，草稿在独立编辑器中持有；切换分类不丢失未保存内容。
const gatewayEditor = useGatewaySettings(saving)
const { gateway, savedGateway } = gatewayEditor
const disguiseEditor = useDisguiseSettings(saving, activeSection)
const { disguiseClaudeVersion, disguiseCodexVersion, disguiseAutoUpdate, savedDisguiseClaudeVersion, savedDisguiseCodexVersion, savedDisguiseAutoUpdate, loadDisguiseStatus } = disguiseEditor
const promptsEditor = usePromptSettings(saving)
const { promptEntries, promptActiveVersion, selectedPromptEntry, savedPromptsSnapshot, savedPromptActiveVersion } = promptsEditor
const extensionsEditor = useExtensionsSettings(saving, activeSection)
const { skillsDir, skillsRepoUrl, savedSkillsDir, savedSkillsRepoUrl, loadGitStatus } = extensionsEditor

const proxyEnabled = ref(false)
const proxyUrl = ref('')
const savedProxyUrl = ref('')
const savedProxyEnabled = ref(false)
const contentSafety = ref<ContentSafetySettings>(defaultContentSafety())
const savedContentSafety = ref('')
const notice = ref<Notice>(null)
const legacyWordFilterEnabled = ref(false)
const advancedSafetyActive = computed(() => contentSafety.value.whitelist.enabled || contentSafety.value.customRules.enabled ||
  (contentSafety.value.allowlist.enabled && contentSafety.value.allowlist.entries.some(entry => !['127.0.0.1', 'localhost', '0.0.0.0', '::1'].includes(entry))))

const resetGateTarget = () => {
  contentSafety.value.dangerousCmd.aiGate.channelId = ''
  contentSafety.value.dangerousCmd.aiGate.model = ''
}

// 危险命令检测总开关关闭时联动关闭 AI 闸门：闸门依赖检测，检测关闭后
// 闸门面板隐藏但开关状态会残留为开启，导致保存被依赖校验拦下且无处可关。
watch(
  () => contentSafety.value.dangerousCmd.enabled,
  enabled => {
    if (!enabled) {
      contentSafety.value.dangerousCmd.aiGate.enabled = false
      if (contentSafety.value.dangerousCmd.semantic) contentSafety.value.dangerousCmd.semantic.enabled = false
    }
  }
)

const normalizedProxyUrl = computed(() => (proxyEnabled.value ? proxyUrl.value.trim() : ''))
const networkDirty = computed(() =>
  proxyEnabled.value !== savedProxyEnabled.value || normalizedProxyUrl.value !== savedProxyUrl.value
)
const contentSafetyDirty = computed(() => JSON.stringify(contentSafety.value) !== savedContentSafety.value)
const dirtySections = computed<Record<SettingsSection, boolean>>(() => ({
  appearance: false,
  network: networkDirty.value,
  gateway: gatewayEditor.gatewayDirty.value,
  'content-safety': contentSafetyDirty.value,
  disguise: disguiseEditor.disguiseDirty.value,
  prompts: promptsEditor.promptsDirty.value,
  extensions: extensionsEditor.extensionsDirty.value
}))

const proxyUrlRule = (value: string) => {
  const raw = value?.trim()
  if (!raw) return '请输入代理地址'
  try {
    const parsed = new URL(raw)
    if (!['http:', 'https:', 'socks5:', 'socks5h:'].includes(parsed.protocol)) {
      return '仅支持 http、https、socks5 和 socks5h 协议'
    }
    if (!parsed.hostname) return '代理地址必须包含主机'
    if ((parsed.pathname && parsed.pathname !== '/') || parsed.search || parsed.hash) {
      return '代理地址不能包含路径、查询参数或片段'
    }
    return true
  } catch {
    return '请输入有效的代理地址'
  }
}

const loadSettings = async () => {
  loading.value = true
  loadError.value = ''
  try {
    const settings = await api.getSettings()
    if (!settings.gateway) throw new Error('服务端未返回请求与日志设置，请确认后端版本')
    gateway.value = settings.gateway
    savedGateway.value = JSON.stringify(settings.gateway)
    const current = settings.network?.upstreamProxyUrl?.trim() || ''
    const enabled = settings.network?.upstreamProxyEnabled ?? false
    savedProxyUrl.value = current
    savedProxyEnabled.value = enabled
    proxyUrl.value = current
    proxyEnabled.value = enabled
    contentSafety.value = normalizeContentSafety(settings.contentSafety)
    savedContentSafety.value = JSON.stringify(contentSafety.value)
    legacyWordFilterEnabled.value = contentSafety.value.sensitiveWord.enabled
    contentSafety.value.sensitiveWord.enabled = false
    // 伪装设置
    if (!settings.disguise) throw new Error('服务端未返回伪装设置，请确认后端版本')
    const savedDisguise = settings.disguise
    savedDisguiseClaudeVersion.value = savedDisguise.claudeCodeVersion
    savedDisguiseCodexVersion.value = savedDisguise.codexVersion
    savedDisguiseAutoUpdate.value = savedDisguise.autoUpdateFromRegistry
    disguiseClaudeVersion.value = savedDisguiseClaudeVersion.value
    disguiseCodexVersion.value = savedDisguiseCodexVersion.value
    disguiseAutoUpdate.value = savedDisguiseAutoUpdate.value
    await loadDisguiseStatus()
    // 通用提示词版本库（兼容旧配置：prompts 缺失时按空版本库处理）
    promptEntries.value = settings.prompts?.entries ?? []
    promptActiveVersion.value = settings.prompts?.activeVersion ?? ''
    savedPromptActiveVersion.value = promptActiveVersion.value
    selectedPromptEntry.value =
      promptEntries.value.find(entry => entry.version === promptActiveVersion.value) ?? promptEntries.value[0] ?? null
    savedPromptsSnapshot.value = JSON.stringify(promptEntries.value)
    // 扩展与同步设置
    savedSkillsDir.value = settings.extensions?.skillsDir ?? ''
    savedSkillsRepoUrl.value = settings.extensions?.skillsRepoUrl ?? ''
    skillsDir.value = savedSkillsDir.value
    skillsRepoUrl.value = savedSkillsRepoUrl.value
    if (activeSection.value === 'extensions') {
      await loadGitStatus()
    }
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '加载设置失败'
  } finally {
    loading.value = false
  }
}

const saveNetworkSettings = async () => {
  if (proxyEnabled.value) {
    const { valid } = await formRef.value.validate()
    if (!valid) return
  }

  saving.value = true
  notice.value = null
  try {
    const settings = await api.updateSettings({
      network: {
        // 提交规范化值：开关关闭时同步清空地址，避免后端残留旧代理地址强制生效
        upstreamProxyUrl: normalizedProxyUrl.value,
        upstreamProxyEnabled: proxyEnabled.value
      }
    })
    const saved = settings.network?.upstreamProxyUrl?.trim() || ''
    const enabled = settings.network?.upstreamProxyEnabled ?? false
    savedProxyUrl.value = saved
    savedProxyEnabled.value = enabled
    proxyUrl.value = saved
    proxyEnabled.value = enabled
    notice.value = { type: 'success', message: '网络设置已保存' }
  } catch (error) {
    notice.value = { type: 'error', message: error instanceof Error ? error.message : '保存网络设置失败' }
  } finally {
    saving.value = false
  }
}

const addCustomRule = () => {
  contentSafety.value.customRules.rules.push({ name: '', type: '', pattern: '' })
}

const validateCustomRulesForSave = (rules: CustomRuleItem[]): string | null => {
  const seenNames = new Set<string>()
  const seenTypes = new Set<string>()
  for (const rule of rules) {
    if (!rule.name.trim()) return '自定义规则名不能为空'
    if (rule.name.length > 64) return `自定义规则名 ${rule.name} 超过 64 个字符`
    if (seenNames.has(rule.name)) return `自定义规则名 ${rule.name} 重复`
    seenNames.add(rule.name)
    if (!/^[A-Z0-9_]{1,16}$/.test(rule.type)) return `自定义规则 ${rule.name} 的类型前缀只能包含大写字母、数字和下划线（1-16 位）`
    if (seenTypes.has(rule.type)) return `自定义规则类型前缀 ${rule.type} 重复`
    seenTypes.add(rule.type)
    if (!rule.pattern.trim()) return `自定义规则 ${rule.name} 缺少正则表达式`
  }
  return null
}

const saveContentSafetySettings = async () => {
  notice.value = null
  const settingsToSave = normalizeContentSafety(contentSafety.value)
  settingsToSave.dangerousCmd.aiGate.model = settingsToSave.dangerousCmd.aiGate.model?.trim() ?? ''
  settingsToSave.sensitiveWord.enabled = false
  for (const [enabled, rules, label] of [
    [settingsToSave.sensitiveInfo.enabled, settingsToSave.sensitiveInfo.enabledRules, '个人信息保护'],
    [settingsToSave.credential.enabled, settingsToSave.credential.enabledRules, '凭据保护'],
    [settingsToSave.dangerousCmd.enabled, settingsToSave.dangerousCmd.enabledRules, '危险命令检测']
  ] as const) {
    if (enabled && rules.length === 0) {
      notice.value = { type: 'error', message: `${label}已启用，但未选择任何规则` }
      return
    }
  }
  if (settingsToSave.dangerousCmd.aiGate.enabled && !settingsToSave.dangerousCmd.enabled) {
    notice.value = { type: 'error', message: '危险命令 AI 闸门依赖危险命令检测：请先启用危险命令检测' }
    return
  }
  if (settingsToSave.customRules.enabled && settingsToSave.sensitiveData.mode !== 'mask') {
    notice.value = { type: 'error', message: '自定义检测规则依赖可逆脱敏：请将敏感数据处置切换为可逆脱敏' }
    return
  }
  if (settingsToSave.customRules.enabled && settingsToSave.customRules.rules.length === 0) {
    notice.value = { type: 'error', message: '自定义检测规则已启用，但没有配置任何规则' }
    return
  }
  const customRuleError = validateCustomRulesForSave(settingsToSave.customRules.rules)
  if (customRuleError) {
    notice.value = { type: 'error', message: customRuleError }
    return
  }
  const waitSeconds = settingsToSave.dangerousCmd.aiGate.waitSeconds
  if (settingsToSave.dangerousCmd.aiGate.enabled && (waitSeconds < 3 || waitSeconds > 120 || !Number.isInteger(waitSeconds))) {
    notice.value = { type: 'error', message: '危险命令 AI 闸门等待秒数必须是 3 到 120 之间的整数' }
    return
  }

  saving.value = true
  try {
    const settings = await api.updateSettings({ contentSafety: settingsToSave })
    contentSafety.value = normalizeContentSafety(settings.contentSafety)
    savedContentSafety.value = JSON.stringify(contentSafety.value)
    notice.value = { type: 'success', message: '内容安全设置已保存' }
    legacyWordFilterEnabled.value = false
  } catch (error) {
    notice.value = { type: 'error', message: error instanceof Error ? error.message : '保存内容安全设置失败' }
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadSettings()
  loadGateChannels()
})
</script>

<style scoped src="@/features/settings/styles/settings.css"></style>
