<template>
  <div class="settings-page">
    <header class="settings-header">
      <div>
        <h1 class="text-h5 font-weight-bold">设置</h1>
        <p class="text-body-2 text-medium-emphasis mt-1 mb-0">管理服务级配置</p>
      </div>
    </header>

    <v-divider />

    <div class="settings-layout">
      <nav class="settings-nav" aria-label="设置分类">
        <v-list v-model:selected="selectedSections" nav mandatory density="compact" bg-color="transparent">
          <v-list-item value="network" color="primary" prepend-icon="mdi-web" title="网络" />
          <v-list-item value="content-safety" color="primary" prepend-icon="mdi-shield-alert" title="内容安全" />
          <v-list-item value="integration" color="primary" prepend-icon="mdi-swap-horizontal" title="集成" />
          <v-list-item value="assistant" color="primary" prepend-icon="mdi-robot" title="AI 助手" />
        </v-list>
      </nav>

      <main class="settings-content">
        <v-alert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-5">
          {{ loadError }}
        </v-alert>

        <v-skeleton-loader v-if="loading" type="list-item-two-line, list-item-two-line, list-item-two-line" />

        <section v-else-if="activeSection === 'network'" aria-labelledby="network-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="network-settings-title" class="text-h6 font-weight-bold">网络</h2>
              <p class="text-body-2 text-medium-emphasis mt-1 mb-0">配置服务访问上游渠道时使用的默认代理</p>
            </div>
          </div>

          <v-form ref="formRef" @submit.prevent="saveNetworkSettings">
            <div class="setting-row">
              <div class="setting-copy">
                <div class="text-body-1 font-weight-medium">全局上游代理</div>
                <div class="text-body-2 text-medium-emphasis mt-1">未单独指定代理策略的渠道使用此地址</div>
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
                  hint="支持 http、https、socks5 和 socks5h"
                  persistent-hint
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

        <section v-else-if="activeSection === 'integration'" aria-labelledby="integration-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="integration-settings-title" class="text-h6 font-weight-bold">集成</h2>
              <p class="text-body-2 text-medium-emphasis mt-1 mb-0">与外部工具的联动配置</p>
            </div>
          </div>

          <v-form ref="integrationFormRef" @submit.prevent="saveIntegrationSettings">
            <div class="setting-row">
              <div class="setting-copy">
                <div class="text-body-1 font-weight-medium">CC Switch 路径</div>
                <div class="text-body-2 text-medium-emphasis mt-1">
                  配置后，渠道菜单可将渠道一键导入 CC Switch（支持便携版）；
                  未配置时渠道菜单仅提供"复制 CCS 链接"
                </div>
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
                hint="便携版无需安装协议，配置路径后即可一键导入"
                persistent-hint
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

        <section v-else-if="activeSection === 'assistant'" aria-labelledby="assistant-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="assistant-settings-title" class="text-h6 font-weight-bold">AI 配置助手</h2>
              <p class="text-body-2 text-medium-emphasis mt-1 mb-0">配置助手对话使用的模型路由（渠道分组）</p>
            </div>
          </div>

          <v-form @submit.prevent="saveAssistantSettings">
            <div class="setting-row">
              <div class="setting-copy">
                <div class="text-body-1 font-weight-medium">模型分组</div>
                <div class="text-body-2 text-medium-emphasis mt-1">
                  助手以分组的匹配词作为模型 ID 走主链路编排（分组匹配 → 优先级 → 熔断/健康 → 兜底），
                  复用渠道编排能力，不单独维护默认模型
                </div>
              </div>
            </div>

            <v-select
              v-model="assistantModel"
              :items="assistantModelItems"
              item-title="title"
              item-value="value"
              label="模型分组"
              variant="outlined"
              density="comfortable"
              hint="留空时自动：按 Messages → Responses → Chat → Gemini 走各协议的兜底分组"
              persistent-hint
            />

            <v-select
              v-model="assistantChannel"
              :items="assistantChannelItems"
              item-title="title"
              item-value="value"
              label="指定渠道"
              variant="outlined"
              density="comfortable"
              class="mt-5"
              hint="留空时自动：按 Messages → Responses → Chat → Gemini 优先级编排；指定后固定使用该渠道，不回退不换渠道"
              persistent-hint
            />

            <v-alert
              v-if="assistantStatus"
              :type="assistantStatus.available ? 'info' : 'warning'"
              variant="tonal"
              density="compact"
              class="mt-5"
            >
              <template v-if="assistantStatus.available">
                当前生效：{{ assistantStatus.channelId ? '指定渠道' : assistantStatus.model ? `分组「${assistantStatus.model}」` : '自动（兜底分组）' }}
                <template v-if="assistantStatus.channel">
                  → {{ assistantStatus.channel.name }}（{{ assistantKindLabel(assistantStatus.channel.kind) }}）
                </template>
              </template>
              <template v-else>{{ assistantStatus.reason || '当前没有可用的模型渠道' }}</template>
            </v-alert>

            <v-alert v-if="assistantNotice" :type="assistantNotice.type" variant="tonal" density="compact" class="mt-3">
              {{ assistantNotice.message }}
            </v-alert>

            <div class="settings-actions">
              <v-btn
                type="submit"
                color="primary"
                prepend-icon="mdi-content-save"
                :loading="saving"
                :disabled="!assistantDirty"
              >
                保存助手设置
              </v-btn>
            </div>
          </v-form>
        </section>

        <section v-else aria-labelledby="content-safety-settings-title">
          <div class="section-heading">
            <div>
              <h2 id="content-safety-settings-title" class="text-h6 font-weight-bold">内容安全</h2>
              <p class="text-body-2 text-medium-emphasis mt-1 mb-0">管理请求检测、信息掩码和响应拦截策略</p>
            </div>
            <v-btn variant="text" prepend-icon="mdi-format-list-bulleted" to="/blocked-logs">拦截记录</v-btn>
          </div>

          <form @submit.prevent="saveContentSafetySettings">
            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">敏感数据处置</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">统一控制个人信息与凭据命中后的处理方式</div>
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
                  <div class="text-body-2 text-medium-emphasis mt-1">请求前检查用户输入</div>
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
                  <div class="text-body-2 text-medium-emphasis mt-1">手机号、身份证、邮箱和 IP 地址</div>
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
                  <div class="text-body-2 text-medium-emphasis mt-1">区分用户输入与工具读取结果</div>
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
                  <div class="text-body-2 text-medium-emphasis mt-1">响应代码块和工具参数</div>
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
                      <div class="text-body-2 text-medium-emphasis mt-1">
                        命中危险命令后由大模型渠道二次判断：判定危险时挂起请求等待确认（超时拦截），
                        判定安全时放行并保留否决窗口；判断失败一律拦截
                      </div>
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
                      v-model="contentSafety.dangerousCmd.aiGate.channelId"
                      :items="gateChannelItems"
                      item-title="title"
                      item-value="value"
                      label="判断渠道"
                      variant="outlined"
                      density="compact"
                      hint="留空时按 Messages → Responses → Chat → Gemini 自动挑选第一个可用渠道"
                      persistent-hint
                      class="ai-gate-select"
                    />
                    <v-text-field
                      v-model.number="contentSafety.dangerousCmd.aiGate.waitSeconds"
                      type="number"
                      min="3"
                      max="120"
                      label="等待秒数"
                      variant="outlined"
                      density="compact"
                      hint="判定危险时等待用户放行的窗口，超时即拦截（3-120 秒）"
                      persistent-hint
                      class="ai-gate-wait"
                    />
                  </div>
                </div>
              </v-expand-transition>
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">白名单</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">
                    命中工具名的 tool_result 与 tool_argument 跳过全部检测，并在拦截记录里留审计条目
                  </div>
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
                hint="回车添加，每个 chip 是一个工具名；匹配区分大小写"
                persistent-hint
              />
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">
                    <v-icon size="18" class="mr-1" color="success">mdi-check-underline-circle</v-icon>
                    放行字面量
                  </div>
                  <div class="text-body-2 text-medium-emphasis mt-1">
                    与命中片段完全相等的字符串跳过全部检测维度（整段相等跳过整个字段，子串相等丢弃该命中），出厂含 127.0.0.1 等回环地址
                  </div>
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
                hint="回车添加；精确匹配（区分大小写），最多 1000 条"
                persistent-hint
              />
            </div>

            <div class="safety-group">
              <div class="setting-row safety-master-row">
                <div class="setting-copy">
                  <div class="text-body-1 font-weight-medium">
                    <v-icon size="18" class="mr-1" color="warning">mdi-regex</v-icon>
                    自定义检测规则
                  </div>
                  <div class="text-body-2 text-medium-emphasis mt-1">
                    自定义正则命中后走可逆脱敏（占位符发给模型，回答自动还原）；保护项目代号、内部人名等只有你自己知道的敏感信息。依赖"敏感数据处置 = 可逆脱敏"
                  </div>
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
                    hint="大写字母/数字/下划线"
                  />
                  <v-text-field
                    v-model="rule.pattern"
                    label="正则表达式"
                    density="compact"
                    variant="outlined"
                    hide-details
                    class="custom-rule-pattern"
                    hint="如 project-falcon|internal-[a-z]+"
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
                  <div class="text-body-2 text-medium-emphasis mt-1">
                    危险命令 AI 闸门弹窗时同步发送浏览器系统通知（仅本浏览器生效）
                  </div>
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
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  api,
  channelApiByType,
  type AssistantStatusResponse,
  type ContentSafetySettings,
  type CredentialRule,
  type CustomRuleItem,
  type DangerousCommandRule,
  type SensitiveInfoRule
} from '@/services/api'

type SettingsSection = 'network' | 'content-safety' | 'integration' | 'assistant'
type Notice = { type: 'success' | 'error'; message: string } | null
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

// AI 闸门判断渠道候选：四协议渠道（messages → responses → chat → gemini）。
// 拉取失败时静默降级——下拉只剩"自动"一项，主设置加载不受影响。
type GateChannelOption = { title: string; value: string }

const AUTO_GATE_CHANNEL: GateChannelOption = { title: '自动（四协议优先级）', value: '' }
const gateChannelItems = ref<GateChannelOption[]>([AUTO_GATE_CHANNEL])

const loadGateChannels = async () => {
  const tabs = ['messages', 'responses', 'chat', 'gemini'] as const
  const items: GateChannelOption[] = [AUTO_GATE_CHANNEL]
  const results = await Promise.allSettled(
    tabs.map(async tab => {
      const response = await channelApiByType(tab).getChannels()
      return response.channels || []
    })
  )
  const tabLabels: Record<(typeof tabs)[number], string> = {
    messages: 'Messages',
    responses: 'Responses',
    chat: 'Chat',
    gemini: 'Gemini'
  }
  tabs.forEach((tab, index) => {
    const result = results[index]
    if (result.status !== 'fulfilled') return
    for (const channel of result.value) {
      if (!channel.id) continue
      items.push({ title: `${tabLabels[tab]} · ${channel.name}`, value: channel.id })
    }
  })
  gateChannelItems.value = items
}

const defaultContentSafety = (): ContentSafetySettings => ({
  sensitiveData: {
    mode: 'block'
  },
  sensitiveWord: {
    enabled: false,
    pornographyEnabled: false,
    gamblingEnabled: false,
    drugsEnabled: false,
    violenceTerrorEnabled: false,
    politicalEnabled: false,
    illegalCrimeEnabled: false,
    customWords: []
  },
  sensitiveInfo: {
    enabled: false,
    mode: 'mask',
    enabledRules: [],
    ipMaskScope: 'all'
  },
  credential: {
    enabled: false,
    userInputMode: 'block',
    toolResultMode: 'block',
    toolArgumentMode: 'block',
    enabledRules: []
  },
  dangerousCmd: {
    enabled: false,
    enabledRules: [],
    aiGate: {
      enabled: false,
      channelId: '',
      waitSeconds: 10
    }
  },
  whitelist: {
    enabled: false,
    toolNames: []
  },
  allowlist: {
    enabled: true,
    entries: ['127.0.0.1', 'localhost', '0.0.0.0', '::1']
  },
  customRules: {
    enabled: false,
    rules: []
  }
})

const normalizeContentSafety = (
  settings: ContentSafetySettings | null | undefined
): ContentSafetySettings => {
  const defaults = defaultContentSafety()
  if (!settings) return defaults

  const sensitiveWord = settings.sensitiveWord
  const sensitiveInfo = settings.sensitiveInfo
  const credential = settings.credential
  const dangerousCmd = settings.dangerousCmd

  const rawInfoRules = Array.isArray(sensitiveInfo?.enabledRules)
    ? (sensitiveInfo.enabledRules as string[])
    : []
  const legacyAPIKey = rawInfoRules.includes('api_key')
  const normalizedInfoRules = rawInfoRules.filter(rule => rule !== 'api_key') as SensitiveInfoRule[]

  return {
    sensitiveData: {
      mode: settings.sensitiveData?.mode === 'mask' ? 'mask' : 'block'
    },
    sensitiveWord: {
      ...defaults.sensitiveWord,
      ...(sensitiveWord || {}),
      customWords: Array.isArray(sensitiveWord?.customWords) ? [...sensitiveWord.customWords] : []
    },
    sensitiveInfo: {
      ...defaults.sensitiveInfo,
      ...(sensitiveInfo || {}),
      enabled: legacyAPIKey && normalizedInfoRules.length === 0 ? false : Boolean(sensitiveInfo?.enabled),
      enabledRules: normalizedInfoRules,
      ipMaskScope:
        sensitiveInfo?.ipMaskScope === 'all' || sensitiveInfo?.ipMaskScope === 'public'
          ? sensitiveInfo.ipMaskScope
          : defaults.sensitiveInfo.ipMaskScope
    },
    credential: {
      ...defaults.credential,
      ...(credential || {}),
      enabled: credential?.enabled ?? (legacyAPIKey && Boolean(sensitiveInfo?.enabled)),
      userInputMode: credential?.userInputMode ?? (legacyAPIKey ? 'mask' : defaults.credential.userInputMode),
      toolResultMode: credential?.toolResultMode ?? defaults.credential.toolResultMode,
      toolArgumentMode: credential?.toolArgumentMode ?? defaults.credential.toolArgumentMode,
      enabledRules: Array.isArray(credential?.enabledRules)
        ? [...credential.enabledRules]
        : legacyAPIKey
          ? ['api_key']
          : []
    },
    dangerousCmd: {
      ...defaults.dangerousCmd,
      ...(dangerousCmd || {}),
      enabledRules: Array.isArray(dangerousCmd?.enabledRules)
        ? [...dangerousCmd.enabledRules]
        : dangerousCmd
          ? []
          : [...defaults.dangerousCmd.enabledRules],
      aiGate: {
        ...defaults.dangerousCmd.aiGate,
        ...(dangerousCmd?.aiGate || {}),
        channelId: typeof dangerousCmd?.aiGate?.channelId === 'string' ? dangerousCmd.aiGate.channelId : '',
        waitSeconds:
          typeof dangerousCmd?.aiGate?.waitSeconds === 'number' && dangerousCmd.aiGate.waitSeconds > 0
            ? dangerousCmd.aiGate.waitSeconds
            : defaults.dangerousCmd.aiGate.waitSeconds
      }
    },
    whitelist: {
      ...defaults.whitelist,
      ...(settings.whitelist || {}),
      toolNames: Array.isArray(settings.whitelist?.toolNames) ? [...settings.whitelist.toolNames] : []
    },
    allowlist: {
      ...defaults.allowlist,
      ...(settings.allowlist || {}),
      enabled: settings.allowlist?.enabled ?? defaults.allowlist.enabled,
      entries: Array.isArray(settings.allowlist?.entries)
        ? settings.allowlist.entries.filter(entry => typeof entry === 'string' && entry !== '')
        : [...defaults.allowlist.entries]
    },
    customRules: {
      ...defaults.customRules,
      ...(settings.customRules || {}),
      rules: Array.isArray(settings.customRules?.rules)
        ? settings.customRules.rules
            .filter(
              (rule): rule is CustomRuleItem =>
                rule && typeof rule.name === 'string' && typeof rule.type === 'string' && typeof rule.pattern === 'string'
            )
            .map(rule => ({ name: rule.name, type: rule.type, pattern: rule.pattern }))
        : []
    }
  }
}

const formRef = ref()
const selectedSections = ref<SettingsSection[]>(['network'])
const activeSection = computed<SettingsSection>(() => selectedSections.value[0] || 'network')
const loading = ref(true)
const saving = ref(false)
const loadError = ref('')
const proxyEnabled = ref(false)
const proxyUrl = ref('')
const savedProxyUrl = ref('')
const savedProxyEnabled = ref(false)
const contentSafety = ref<ContentSafetySettings>(defaultContentSafety())
const savedContentSafety = ref('')
const notice = ref<Notice>(null)

// 集成设置（CC Switch）
const integrationFormRef = ref()
const ccSwitchPath = ref('')
const savedCcSwitchPath = ref('')
const pickingCcsPath = ref(false)
const integrationNotice = ref<Notice>(null)

// AI 配置助手设置（模型分组路由 + 显式渠道）
const assistantModel = ref('')
const savedAssistantModel = ref('')
const assistantChannel = ref('')
const savedAssistantChannel = ref('')
const assistantNotice = ref<Notice>(null)
const assistantStatus = ref<AssistantStatusResponse | null>(null)

const assistantDirty = computed(
  () =>
    assistantModel.value.trim() !== savedAssistantModel.value ||
    assistantChannel.value !== savedAssistantChannel.value
)

// 渠道选择器候选：自动 + status 返回的四协议渠道（m → r → c → g 顺序）。
type AssistantChannelOption = { title: string; value: string }

const assistantChannelItems = computed<Array<AssistantChannelOption>>(() => [
  { title: '自动（Messages → Responses → Chat → Gemini）', value: '' },
  ...(assistantStatus.value?.channels || []).map(channel => ({
    title: `${assistantKindLabel(channel.kind)} · ${channel.name}`,
    value: channel.id
  }))
])

// 危险命令检测总开关关闭时联动关闭 AI 闸门：闸门依赖检测，检测关闭后
// 闸门面板隐藏但开关状态会残留为开启，导致保存被依赖校验拦下且无处可关。
watch(
  () => contentSafety.value.dangerousCmd.enabled,
  enabled => {
    if (!enabled) contentSafety.value.dangerousCmd.aiGate.enabled = false
  }
)

// 助手模型分组候选：四协议分组（messages → responses → chat → gemini），值取分组的
// 匹配词（即请求的模型 ID）。跨协议重复的匹配词路由等价，按协议优先级去重。
// 拉取失败时静默降级——下拉只剩"自动"一项，主设置加载不受影响。
type AssistantModelOption = { title: string; value: string }

const AUTO_ASSISTANT_MODEL: AssistantModelOption = { title: '自动（兜底分组）', value: '' }
const assistantModelItems = ref<AssistantModelOption[]>([AUTO_ASSISTANT_MODEL])

const assistantKindLabels: Record<string, string> = {
  messages: 'Messages',
  responses: 'Responses',
  chat: 'Chat',
  gemini: 'Gemini'
}

const assistantKindLabel = (kind: string) => assistantKindLabels[kind] ?? kind

const loadAssistantModelOptions = async () => {
  const tabs = ['messages', 'responses', 'chat', 'gemini'] as const
  const results = await Promise.allSettled(tabs.map(tab => api.getChannelPools(tab)))
  const items: AssistantModelOption[] = [AUTO_ASSISTANT_MODEL]
  const seen = new Set<string>([''])
  tabs.forEach((tab, index) => {
    const result = results[index]
    if (result.status !== 'fulfilled') return
    for (const pool of result.value.pools || []) {
      const matcher = pool.modelMatcher?.trim()
      if (!matcher || seen.has(matcher)) continue
      seen.add(matcher)
      items.push({ title: `${assistantKindLabels[tab]} · ${pool.name}（匹配: ${matcher}）`, value: matcher })
    }
  })
  assistantModelItems.value = items
}

const loadAssistantStatus = async () => {
  try {
    assistantStatus.value = await api.getAssistantStatus()
  } catch {
    assistantStatus.value = null
  }
}

const saveAssistantSettings = async () => {
  assistantNotice.value = null
  saving.value = true
  try {
    // model 与 channelId 必须同时提交，未提交字段会被设置接口按零值覆盖。
    const settings = await api.updateSettings({
      assistant: { model: assistantModel.value.trim(), channelId: assistantChannel.value }
    })
    const saved = settings.assistant?.model ?? ''
    savedAssistantModel.value = saved
    assistantModel.value = saved
    const savedChannel = settings.assistant?.channelId ?? ''
    savedAssistantChannel.value = savedChannel
    assistantChannel.value = savedChannel
    assistantNotice.value = { type: 'success', message: '助手设置已保存' }
    await loadAssistantStatus()
  } catch (error) {
    assistantNotice.value = { type: 'error', message: error instanceof Error ? error.message : '保存助手设置失败' }
  } finally {
    saving.value = false
  }
}

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

<style scoped>
.settings-page {
  max-width: 1120px;
  margin: 0 auto;
}

.settings-header {
  display: flex;
  align-items: center;
  min-height: 84px;
  padding: 8px 4px 20px;
}

.settings-layout {
  display: grid;
  grid-template-columns: minmax(180px, 220px) minmax(0, 1fr);
  gap: 40px;
  padding-top: 24px;
}

.ccs-path-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.ccs-path-row .v-btn {
  margin-top: 4px;
  flex-shrink: 0;
}

.settings-nav {
  border-right: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding-right: 20px;
}

.settings-content {
  max-width: 760px;
  min-width: 0;
  padding: 4px 0 48px;
}

.section-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 28px;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 32px;
  min-height: 72px;
  padding: 16px 0;
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.setting-copy {
  min-width: 0;
}

.proxy-editor {
  padding-top: 24px;
}

.safety-group + .safety-group {
  margin-top: 28px;
}

.safety-master-row {
  border-bottom: 0;
}

.mode-setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

/* 二选一切换器：选中项用实色背景 + 反色文字，亮/暗主题都一眼可辨。
   颜色走主题 token 而非写死，主题切换时自动适配。 */
.mode-toggle :deep(.disposal-toggle__block) {
  background: rgb(var(--v-theme-error));
  color: rgb(var(--v-theme-on-error));
  opacity: 1;
}

.mode-toggle :deep(.disposal-toggle__mask) {
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
  opacity: 1;
}

.mode-toggle :deep(.scope-toggle__selected) {
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
  opacity: 1;
}

.ip-scope-row {
  margin: 10px 16px 12px;
}

/* 危险命令 AI 闸门子面板：与规则勾选区分层（边框 + 浅底），亮暗主题都用主题 token */
.ai-gate-panel {
  margin: 10px 16px 12px;
  padding: 12px 16px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
  background: rgba(var(--v-theme-surface-variant), 0.24);
}

.ai-gate-head-row {
  border-bottom: 0;
}

.ai-gate-options {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.ai-gate-select {
  flex: 1 1 260px;
  max-width: 380px;
}

.ai-gate-wait {
  flex: 0 1 160px;
}

/* 自定义规则编辑器：每行 名称 | 类型前缀 | 正则 | 删除 */
.custom-rules-editor {
  padding: 10px 16px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.custom-rule-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.custom-rule-name {
  flex: 0 1 140px;
}

.custom-rule-type {
  flex: 0 1 140px;
}

.custom-rule-pattern {
  flex: 1 1 240px;
}

.option-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 2px 24px;
  min-height: 104px;
  padding: 10px 16px 12px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
  background: rgba(var(--v-theme-surface-variant), 0.24);
  transition: opacity 180ms ease;
}

.options-disabled {
  opacity: 0.58;
}

.settings-actions {
  display: flex;
  justify-content: flex-end;
  padding-top: 28px;
}

@media (prefers-reduced-motion: reduce) {
  .option-grid {
    transition: none;
  }
}

@media (max-width: 700px) {
  .settings-layout {
    grid-template-columns: 1fr;
    gap: 20px;
  }

  .settings-nav {
    border-right: 0;
    border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
    padding-right: 0;
    padding-bottom: 12px;
  }

  .settings-nav :deep(.v-list) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .setting-row {
    align-items: flex-start;
    gap: 20px;
  }

  .mode-setting-row {
    align-items: flex-start;
    flex-direction: column;
  }

  .section-heading {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (max-width: 480px) {
  .option-grid {
    grid-template-columns: 1fr;
  }
}
</style>
