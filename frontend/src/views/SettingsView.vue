<template>
  <div class="settings-page">
    <PageHeader title="设置" description="按分类管理全局配置；切换分类会保留当前页面内的未保存修改。" />

    <div class="settings-layout">
      <nav class="settings-nav" aria-label="设置分类">
        <v-list v-model:selected="selectedSections" nav mandatory density="compact" bg-color="transparent">
          <v-list-item v-for="section in settingsSections" :key="section.key" :value="section.key" :prepend-icon="section.icon" :title="section.label" color="primary" :aria-current="activeSection === section.key ? 'page' : undefined">
            <template #append><span v-if="!loading && dirtySections[section.key]" class="settings-dirty-dot" aria-label="有未保存的修改"></span></template>
          </v-list-item>
        </v-list>
      </nav>

      <div class="settings-content">
        <v-alert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-5">
          {{ loadError }}
        </v-alert>

        <v-skeleton-loader v-if="loading" type="list-item-two-line, list-item-two-line, list-item-two-line" />
        <v-btn v-else-if="loadError" variant="tonal" prepend-icon="mdi-refresh" @click="loadSettings">重新加载设置</v-btn>

        <section v-else-if="activeSection === 'network'" aria-labelledby="network-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="network-settings-title" class="text-h6 font-weight-bold">网络</h2>
            </div>
          </div>

          <v-form ref="formRef" @submit.prevent="saveNetworkSettings">
            <div class="setting-row">
              <div class="setting-copy">
                <div class="text-body-1 font-weight-medium">全局上游代理</div>
                <div class="text-body-2 text-medium-emphasis mt-1">用于网关访问上游服务；单个渠道可选择继承、直连或独立代理。</div>
              </div>
              <v-switch v-model="proxyEnabled" color="primary" inset hide-details aria-label="启用全局上游代理" />
            </div>

            <v-expand-transition>
              <div v-if="proxyEnabled" class="proxy-editor">
                <v-text-field
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
            </v-expand-transition>

            <v-alert v-if="notice" :type="notice.type" variant="tonal" density="compact" class="mt-5">
              {{ notice.message }}
            </v-alert>

            <div class="settings-actions">
              <v-btn type="submit" color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="!networkDirty">
                保存网络设置
              </v-btn>
            </div>
          </v-form>
        </section>

        <GatewaySettingsSection v-else-if="activeSection === 'gateway'" :editor="gatewayEditor" />

        <section v-else-if="activeSection === 'integration'" aria-labelledby="integration-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="integration-settings-title" class="text-h6 font-weight-bold">集成</h2>
            </div>
          </div>

          <v-form ref="integrationFormRef" @submit.prevent="saveIntegrationSettings">
            <div class="setting-row">
              <div class="setting-copy">
                <div class="text-body-1 font-weight-medium">CC Switch 路径</div>
                <div class="text-body-2 text-medium-emphasis mt-1">配置后可从渠道菜单一键导入 CC Switch；留空时仍可复制导入链接。</div>
              </div>
            </div>

            <div class="ccs-path-row">
              <v-text-field
                v-model="ccSwitchPath"
                label="CC Switch 可执行文件路径"
                placeholder="例如 D:\\Tools\\cc-switch\\cc-switch.exe"
                prepend-inner-icon="mdi-application-export"
                variant="outlined"
                density="comfortable"
                autocomplete="off"
                spellcheck="false"
                :rules="[ccSwitchPathRule]"
              />
              <v-btn
                color="primary"
                variant="tonal"
                prepend-icon="mdi-folder-open"
                :loading="pickingCcsPath"
                @click="pickCcsPath"
              >
                选择文件
              </v-btn>
            </div>

            <v-alert v-if="integrationNotice" :type="integrationNotice.type" variant="tonal" density="compact" class="mt-5">
              {{ integrationNotice.message }}
            </v-alert>

            <div class="settings-actions">
              <v-btn
                type="submit"
                color="primary"
                prepend-icon="mdi-content-save"
                :loading="saving"
                :disabled="!integrationDirty"
              >
                保存集成设置
              </v-btn>
            </div>
          </v-form>
        </section>

        <DisguiseSettingsSection v-else-if="activeSection === 'disguise'" :editor="disguiseEditor" />

        <AssistantSettingsSection v-else-if="activeSection === 'assistant'" :editor="assistantEditor" />

        <PromptSettingsSection v-else-if="activeSection === 'prompts'" :editor="promptsEditor" />

        <section v-else aria-labelledby="content-safety-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="content-safety-settings-title" class="text-h6 font-weight-bold">内容安全</h2>
            </div>
            <v-btn variant="text" prepend-icon="mdi-format-list-bulleted" to="/blocked-logs">拦截记录</v-btn>
          </div>

          <form @submit.prevent="saveContentSafetySettings">
            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">敏感数据处置</div>
                </div>
                <v-btn-toggle
                  v-model="contentSafety.sensitiveData.mode"
                  mandatory
                  divided
                  density="compact"
                  class="mode-toggle"
                >
                  <v-btn value="block" prepend-icon="mdi-shield-lock" selected-class="disposal-toggle__block">
                    阻断
                  </v-btn>
                  <v-btn value="mask" prepend-icon="mdi-eye-off" selected-class="disposal-toggle__mask">
                    可逆脱敏
                  </v-btn>
                </v-btn-toggle>
              </div>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">敏感词检测</div>
                </div>
                <v-switch
                  v-model="contentSafety.sensitiveWord.enabled"
                  color="primary"
                  inset
                  hide-details
                  aria-label="启用敏感词检测"
                />
              </div>
              <div class="option-grid" :class="{ 'options-disabled': !contentSafety.sensitiveWord.enabled }">
                <v-switch
                  v-for="item in sensitiveWordCategories"
                  :key="item.key"
                  v-model="contentSafety.sensitiveWord[item.key]"
                  :label="item.label"
                  :disabled="!contentSafety.sensitiveWord.enabled"
                  density="compact"
                  color="primary"
                  inset
                  hide-details
                />
              </div>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">个人信息保护</div>
                </div>
                <v-switch
                  v-model="contentSafety.sensitiveInfo.enabled"
                  color="primary"
                  inset
                  hide-details
                  aria-label="启用个人信息保护"
                />
              </div>
              <div class="option-grid" :class="{ 'options-disabled': !contentSafety.sensitiveInfo.enabled }">
                <v-checkbox
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
              <v-expand-transition>
                <div
                  v-if="
                    contentSafety.sensitiveInfo.enabled && contentSafety.sensitiveInfo.enabledRules.includes('ip_address')
                  "
                  class="mode-setting-row ip-scope-row"
                >
                  <span class="text-body-2">IP 掩码范围</span>
                  <v-btn-toggle
                    v-model="contentSafety.sensitiveInfo.ipMaskScope"
                    mandatory
                    divided
                    density="compact"
                    class="mode-toggle"
                  >
                    <v-btn value="public" selected-class="scope-toggle__selected">仅公网地址</v-btn>
                    <v-btn value="all" selected-class="scope-toggle__selected">全部地址</v-btn>
                  </v-btn-toggle>
                </div>
              </v-expand-transition>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">凭据保护</div>
                </div>
                <v-switch
                  v-model="contentSafety.credential.enabled"
                  color="error"
                  inset
                  hide-details
                  aria-label="启用凭据保护"
                />
              </div>
              <div class="option-grid" :class="{ 'options-disabled': !contentSafety.credential.enabled }">
                <v-checkbox
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
                  <div class="text-body-1 font-weight-medium">危险命令检测</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">检查 Bash、exec_command 等已知执行工具；普通回复和代码示例直接放行。</div>
                </div>
                <v-switch
                  v-model="contentSafety.dangerousCmd.enabled"
                  color="warning"
                  inset
                  hide-details
                  aria-label="启用危险命令检测"
                />
              </div>
              <div class="option-grid" :class="{ 'options-disabled': !contentSafety.dangerousCmd.enabled }">
                <v-checkbox
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
              <v-expand-transition>
                <div v-if="contentSafety.dangerousCmd.enabled" class="ai-gate-panel">
                  <div class="setting-row ai-gate-head-row">
                    <div class="setting-copy">
                      <div class="text-body-1 font-weight-medium">
                        <v-icon size="18" class="mr-1" color="warning">mdi-brain</v-icon>
                        AI 闸门
                      </div>
                      <div class="text-body-2 text-medium-emphasis mt-1">模型审批最多等待 10 秒。判定危险后进入确认窗口；审批失败时拦截该工具调用。</div>
                    </div>
                    <v-switch
                      v-model="contentSafety.dangerousCmd.aiGate.enabled"
                      color="warning"
                      inset
                      hide-details
                      aria-label="启用危险命令 AI 闸门"
                    />
                  </div>
                  <div v-if="contentSafety.dangerousCmd.aiGate.enabled" class="ai-gate-options">
                    <v-select
                      v-model="contentSafety.dangerousCmd.aiGate.protocol"
                      :items="gateProtocolItems"
                      label="审批协议"
                      variant="outlined"
                      density="compact"
                      class="ai-gate-select"
                      @update:model-value="resetGateTarget"
                    />
                    <v-select
                      v-model="contentSafety.dangerousCmd.aiGate.channelId"
                      :items="gateChannelItems"
                      item-title="title"
                      item-value="value"
                      label="判断渠道"
                      :loading="gateChannelsLoading"
                      :error-messages="gateChannelsError"
                      variant="outlined"
                      density="compact"
                      class="ai-gate-select"
                      @update:menu="open => { if (open) loadGateChannels() }"
                    />
                    <v-combobox
                      v-model="contentSafety.dangerousCmd.aiGate.model"
                      :items="gateModelItems"
                      label="审批模型"
                      placeholder="留空使用渠道默认模型"
                      hint="可选已有模型或输入模型 ID；显式选择优先于渠道默认模型，并应用渠道模型映射。"
                      persistent-hint
                      variant="outlined"
                      density="compact"
                      class="ai-gate-select"
                    />
                    <v-text-field
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
                  <div v-if="contentSafety.dangerousCmd.aiGate.enabled" class="text-body-2 text-medium-emphasis mt-2">
                    {{ gateProtocolHint }}
                  </div>
                </div>
              </v-expand-transition>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">白名单</div>
                  <!-- 命中工具名的 tool_result 与 tool_argument 跳过全部检测，并在拦截记录里留审计条目 -->
                </div>
                <v-switch
                  v-model="contentSafety.whitelist.enabled"
                  color="success"
                  inset
                  hide-details
                  aria-label="启用白名单放行"
                />
              </div>
              <v-combobox
                v-model="contentSafety.whitelist.toolNames"
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
                  <div class="text-body-1 font-weight-medium">
                    <v-icon size="18" class="mr-1" color="success">mdi-check-underline-circle</v-icon>
                    放行字面量
                  </div>
                  <!-- 与命中片段完全相等的字符串跳过全部检测维度（整段相等跳过整个字段，子串相等丢弃该命中），出厂含 127.0.0.1 等回环地址 -->
                </div>
                <v-switch
                  v-model="contentSafety.allowlist.enabled"
                  color="success"
                  inset
                  hide-details
                  aria-label="启用放行字面量"
                />
              </div>
              <v-combobox
                v-model="contentSafety.allowlist.entries"
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
                  <div class="text-body-1 font-weight-medium">
                    <v-icon size="18" class="mr-1" color="warning">mdi-regex</v-icon>
                    自定义检测规则
                  </div>
                  <!-- 自定义正则命中后走可逆脱敏（占位符发给模型，回答自动还原）；保护项目代号、内部人名等只有你自己知道的敏感信息。依赖"敏感数据处置 = 可逆脱敏" -->
                </div>
                <v-switch
                  v-model="contentSafety.customRules.enabled"
                  color="warning"
                  inset
                  hide-details
                  aria-label="启用自定义检测规则"
                />
              </div>
              <div v-if="contentSafety.customRules.enabled" class="custom-rules-editor">
                <div v-for="(rule, index) in contentSafety.customRules.rules" :key="index" class="custom-rule-row">
                  <v-text-field
                    v-model="rule.name"
                    label="规则名"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-name"
                  />
                  <v-text-field
                    v-model="rule.type"
                    label="类型前缀"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-type"
                  />
                  <v-text-field
                    v-model="rule.pattern"
                    label="正则表达式"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-pattern"
                  />
                  <v-btn
                    icon
                    size="small"
                    variant="text"
                    color="error"
                    aria-label="删除规则"
                    @click="contentSafety.customRules.rules.splice(index, 1)"
                  >
                    <v-icon size="18">mdi-delete</v-icon>
                  </v-btn>
                </div>
                <v-btn
                  variant="tonal"
                  size="small"
                  color="primary"
                  prepend-icon="mdi-plus"
                  :disabled="contentSafety.customRules.rules.length >= 50"
                  @click="addCustomRule"
                >
                  添加规则
                </v-btn>
              </div>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">
                    <v-icon size="18" class="mr-1" color="primary">mdi-bell-ring</v-icon>
                    危险命令桌面通知
                  </div>
                  <!-- 危险命令 AI 闸门弹窗时同步发送浏览器系统通知（仅本浏览器生效） -->
                </div>
                <v-switch
                  :model-value="desktopNotifyEnabled"
                  color="primary"
                  inset
                  hide-details
                  aria-label="启用危险命令桌面通知"
                  @update:model-value="toggleDesktopNotify"
                />
              </div>
              <v-alert
                v-if="desktopNotifyNotice"
                :type="desktopNotifyNotice.type"
                variant="tonal"
                density="compact"
                class="mx-4 mt-2"
              >
                {{ desktopNotifyNotice.message }}
              </v-alert>
            </div>

            <v-alert v-if="notice" :type="notice.type" variant="tonal" density="compact" class="mt-5">
              {{ notice.message }}
            </v-alert>

            <div class="settings-actions">
              <v-btn
                type="submit"
                color="primary"
                prepend-icon="mdi-content-save"
                :loading="saving"
                :disabled="!contentSafetyDirty"
              >
                保存内容安全设置
              </v-btn>
            </div>
          </form>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '@/components/common/PageHeader.vue'
import {
  api,
  type ContentSafetySettings,
  type CredentialRule,
  type CustomRuleItem,
  type DangerousCommandRule,
  type SensitiveInfoRule
} from '@/services/api'

import GatewaySettingsSection from '@/components/settings/GatewaySettingsSection.vue'
import { useGatewaySettings } from '@/composables/settings/useGatewaySettings'
import DisguiseSettingsSection from '@/components/settings/DisguiseSettingsSection.vue'
import { useDisguiseSettings } from '@/composables/settings/useDisguiseSettings'
import PromptSettingsSection from '@/components/settings/PromptSettingsSection.vue'
import { usePromptSettings } from '@/composables/settings/usePromptSettings'
import AssistantSettingsSection from '@/components/settings/AssistantSettingsSection.vue'
import { useAssistantSettings } from '@/composables/settings/useAssistantSettings'
import { defaultContentSafety, normalizeContentSafety } from '@/utils/contentSafetyForm'
import type { SettingsSection, SettingsNotice as Notice } from '@/composables/settings/types'

type SensitiveWordToggle =
  | 'pornographyEnabled'
  | 'gamblingEnabled'
  | 'drugsEnabled'
  | 'violenceTerrorEnabled'
  | 'politicalEnabled'
  | 'illegalCrimeEnabled'

const sensitiveWordCategories: Array<{ key: SensitiveWordToggle; label: string }> = [
  { key: 'pornographyEnabled', label: '色情' },
  { key: 'gamblingEnabled', label: '赌博' },
  { key: 'drugsEnabled', label: '毒品' },
  { key: 'violenceTerrorEnabled', label: '暴力与恐怖' },
  { key: 'politicalEnabled', label: '政治敏感' },
  { key: 'illegalCrimeEnabled', label: '违法犯罪' }
]

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

// 审批使用独立渠道候选，不读取 AI 助手的模型和渠道筛选结果。
const gateProtocolItems = [
  { title: '大模型对话 API', value: 'chat' },
  { title: 'Decisions API（OpenAI 格式）', value: 'decisions' },
  { title: 'System One API（Jev 原生格式）', value: 'systemone' }
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
const gateChannelItems = computed(() => {
  const items = [
    { title: isDecisionGate.value ? '自动选择 Decisions 渠道' : '自动选择对话渠道', value: '' },
    ...eligibleGateChannels.value.map(channel => ({
      title: `${channel.kind} · ${channel.name}`,
      value: channel.id
    }))
  ]
  const selected = contentSafety.value.dangerousCmd.aiGate.channelId
  if (selected && !items.some(item => item.value === selected)) {
    items.push({ title: `已选渠道当前不可用（${selected}）`, value: selected })
  }
  return items
})
const gateModelItems = computed(() => {
  const selected = contentSafety.value.dangerousCmd.aiGate.channelId
  return [...new Set(eligibleGateChannels.value
    .filter(channel => !selected || channel.id === selected)
    .flatMap(channel => channel.models))]
})
const gateProtocolHint = computed(() => {
  switch (contentSafety.value.dangerousCmd.aiGate.protocol) {
    case 'decisions': return '使用 Decisions 渠道调用 /v1/decisions，发送 input 和问题数组。Jev 经 LiteLLM 等兼容网关接入时也可选此格式。'
    case 'systemone': return '使用 Decisions 渠道调用 /v1/systemone，发送 state 和具名问题。适用于 Jev 原生 API；渠道启用完整 URL 时按配置地址发送。'
    default: return '使用 Messages、Responses、Chat 或 Gemini 渠道；只将命中本地规则的执行工具命令送审。'
  }
})

const formRef = ref()
const settingsSections: Array<{ key: SettingsSection; label: string; icon: string }> = [
  { key: 'network', label: '网络连接', icon: 'mdi-web' },
  { key: 'gateway', label: '请求与日志', icon: 'mdi-text' },
  { key: 'content-safety', label: '内容安全', icon: 'mdi-shield-alert' },
  { key: 'integration', label: '外部集成', icon: 'mdi-swap-horizontal' },
  { key: 'disguise', label: '客户端伪装', icon: 'mdi-incognito' },
  { key: 'assistant', label: 'AI 助手', icon: 'mdi-robot' },
  { key: 'prompts', label: '通用提示词', icon: 'mdi-text' }
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
const assistantEditor = useAssistantSettings(saving)
const { assistantModel, savedAssistantModel, assistantChannel, savedAssistantChannel, loadAssistantModelOptions, loadAssistantStatus } = assistantEditor

const proxyEnabled = ref(false)
const proxyUrl = ref('')
const savedProxyUrl = ref('')
const savedProxyEnabled = ref(false)
const contentSafety = ref<ContentSafetySettings>(defaultContentSafety())
const savedContentSafety = ref('')
const notice = ref<Notice>(null)

const resetGateTarget = () => {
  contentSafety.value.dangerousCmd.aiGate.channelId = ''
  contentSafety.value.dangerousCmd.aiGate.model = ''
}

// 集成设置（CC Switch）
const integrationFormRef = ref()
const ccSwitchPath = ref('')
const savedCcSwitchPath = ref('')
const pickingCcsPath = ref(false)
const integrationNotice = ref<Notice>(null)

// 危险命令检测总开关关闭时联动关闭 AI 闸门：闸门依赖检测，检测关闭后
// 闸门面板隐藏但开关状态会残留为开启，导致保存被依赖校验拦下且无处可关。
watch(
  () => contentSafety.value.dangerousCmd.enabled,
  enabled => {
    if (!enabled) contentSafety.value.dangerousCmd.aiGate.enabled = false
  }
)

const integrationDirty = computed(() => ccSwitchPath.value.trim() !== savedCcSwitchPath.value)

const ccSwitchPathRule = (value: string) => {
  const raw = value?.trim()
  if (!raw) return true // 允许为空：未配置时渠道菜单退化为复制链接
  if (!/\.exe$/i.test(raw)) return '路径必须指向 .exe 可执行文件'
  return true
}

const normalizedProxyUrl = computed(() => (proxyEnabled.value ? proxyUrl.value.trim() : ''))
const networkDirty = computed(() =>
  proxyEnabled.value !== savedProxyEnabled.value || normalizedProxyUrl.value !== savedProxyUrl.value
)
const contentSafetyDirty = computed(() => JSON.stringify(contentSafety.value) !== savedContentSafety.value)
const dirtySections = computed<Record<SettingsSection, boolean>>(() => ({
  network: networkDirty.value,
  gateway: gatewayEditor.gatewayDirty.value,
  'content-safety': contentSafetyDirty.value,
  integration: integrationDirty.value,
  disguise: disguiseEditor.disguiseDirty.value,
  assistant: assistantEditor.assistantDirty.value,
  prompts: promptsEditor.promptsDirty.value
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
    const savedPath = settings.integration?.ccSwitchPath ?? ''
    savedCcSwitchPath.value = savedPath
    ccSwitchPath.value = savedPath
    const savedModel = settings.assistant?.model ?? ''
    savedAssistantModel.value = savedModel
    assistantModel.value = savedModel
    const savedChannel = settings.assistant?.channelId ?? ''
    savedAssistantChannel.value = savedChannel
    assistantChannel.value = savedChannel
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

const saveIntegrationSettings = async () => {
  const { valid } = await integrationFormRef.value.validate()
  if (!valid) return

  integrationNotice.value = null
  saving.value = true
  try {
    const settings = await api.updateSettings({
      integration: { ccSwitchPath: ccSwitchPath.value.trim() }
    })
    const savedPath = settings.integration?.ccSwitchPath ?? ''
    savedCcSwitchPath.value = savedPath
    ccSwitchPath.value = savedPath
    integrationNotice.value = { type: 'success', message: '集成设置已保存' }
  } catch (error) {
    integrationNotice.value = { type: 'error', message: error instanceof Error ? error.message : '保存集成设置失败' }
  } finally {
    saving.value = false
  }
}

const pickCcsPath = async () => {
  pickingCcsPath.value = true
  integrationNotice.value = null
  try {
    const { path } = await api.pickCcsPath()
    if (path) {
      // 后端选择时已顺手保存，这里同步本地状态
      savedCcSwitchPath.value = path
      ccSwitchPath.value = path
      integrationNotice.value = { type: 'success', message: '路径已保存' }
    }
  } catch (error) {
    integrationNotice.value = { type: 'error', message: error instanceof Error ? error.message : '打开文件选择框失败，请手动填写路径' }
  } finally {
    pickingCcsPath.value = false
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
  const hasSensitiveWordRule = sensitiveWordCategories.some(item => settingsToSave.sensitiveWord[item.key]) ||
    settingsToSave.sensitiveWord.customWords.length > 0
  if (settingsToSave.sensitiveWord.enabled && !hasSensitiveWordRule) {
    notice.value = { type: 'error', message: '敏感词检测已启用，但未选择任何分类或自定义词' }
    return
  }
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
  } catch (error) {
    notice.value = { type: 'error', message: error instanceof Error ? error.message : '保存内容安全设置失败' }
  } finally {
    saving.value = false
  }
}

// 危险命令桌面通知（纯浏览器偏好，存 localStorage；开关打开时向浏览器申请授权）
const DESKTOP_NOTIFY_STORAGE_KEY = 'gate-desktop-notify'
const desktopNotifyEnabled = ref(false)
const desktopNotifyNotice = ref<Notice>(null)

const initDesktopNotify = () => {
  desktopNotifyEnabled.value = localStorage.getItem(DESKTOP_NOTIFY_STORAGE_KEY) === 'enabled'
}

const toggleDesktopNotify = async (value: boolean | null) => {
  desktopNotifyNotice.value = null
  if (!value) {
    localStorage.setItem(DESKTOP_NOTIFY_STORAGE_KEY, 'disabled')
    desktopNotifyEnabled.value = false
    return
  }
  if (typeof Notification === 'undefined') {
    desktopNotifyNotice.value = { type: 'error', message: '当前浏览器不支持系统通知' }
    return
  }
  if (Notification.permission === 'denied') {
    desktopNotifyNotice.value = { type: 'error', message: '浏览器已拒绝通知授权，请在浏览器设置中重新允许' }
    return
  }
  if (Notification.permission !== 'granted') {
    const permission = await Notification.requestPermission()
    if (permission !== 'granted') {
      desktopNotifyNotice.value = { type: 'error', message: '未获得通知授权，桌面通知未启用' }
      return
    }
  }
  localStorage.setItem(DESKTOP_NOTIFY_STORAGE_KEY, 'enabled')
  desktopNotifyEnabled.value = true
  desktopNotifyNotice.value = { type: 'success', message: '桌面通知已启用（仅本浏览器）' }
}

onMounted(() => {
  loadSettings()
  loadGateChannels()
  loadAssistantModelOptions()
  loadAssistantStatus()
  initDesktopNotify()
})
</script>

<style scoped src="../styles/settings.css"></style>
