<template>
  <v-app>
    <!-- 自动认证加载提示 - 只在真正进行自动认证时显示 -->
    <v-overlay
      :model-value="authStore.isAutoAuthenticating && !authStore.isInitialized"
      persistent
      class="align-center justify-center"
      scrim="black"
    >
      <v-card class="pa-6 text-center" max-width="400" rounded="lg">
        <v-progress-circular indeterminate :size="64" :width="6" color="primary" class="mb-4" />
        <div class="text-h6 mb-2">正在验证访问权限</div>
        <div class="text-body-2 text-medium-emphasis">使用保存的访问密钥进行身份验证...</div>
      </v-card>
    </v-overlay>

    <!-- 认证界面 -->
    <v-dialog v-model="showAuthDialog" persistent max-width="500">
      <v-card class="auth-card pa-4">
        <v-card-title class="auth-title">
          <span class="auth-title-icon"><v-icon size="22">mdi-key</v-icon></span>
          <span>API Proxy</span>
        </v-card-title>

        <v-card-text>
          <v-alert v-if="authStore.authError" type="error" variant="tonal" class="mb-4">
            {{ authStore.authError }}
          </v-alert>

          <v-form @submit.prevent="handleAuthSubmit">
            <v-text-field
              v-model="authStore.authKeyInput"
              label="访问密钥 (PROXY_ACCESS_KEY)"
              type="password"
              variant="outlined"
              prepend-inner-icon="mdi-key"
              :rules="[v => !!v || '请输入访问密钥']"
              required
              autofocus
              @keyup.enter="handleAuthSubmit"
            />

            <v-btn type="submit" color="primary" variant="flat" block size="large" class="mt-4" :loading="authStore.authLoading">
              验证并进入
            </v-btn>
          </v-form>
          <div class="auth-footnote">
            <v-icon size="15">mdi-shield-alert</v-icon>
            <span>密钥仅保存在当前浏览器</span>
          </div>
        </v-card-text>
      </v-card>
    </v-dialog>

    <!-- 应用栏 - 玻璃拟态效果 -->
    <v-app-bar elevation="0" :height="$vuetify.display.mobile ? 56 : 64" class="app-header">
      <template #prepend>
        <div class="app-logo">
          <v-icon :size="$vuetify.display.mobile ? 20 : 26" color="white"> mdi-rocket-launch </v-icon>
        </div>
      </template>

      <nav class="header-title" aria-label="主导航">
        <div v-for="group in navGroups" :key="group.key" class="nav-group" role="group" :aria-label="group.label">
          <router-link
            v-for="item in group.items"
            :key="item.key"
            :to="item.to"
            class="api-type-text"
            :class="{ active: topNavActive === item.key }"
          >
            {{ item.label }}
          </router-link>
        </div>
      </nav>

      <v-spacer/>

      <!-- 版本信息 -->
      <div class="version-anchor">
        <div
          v-if="systemStore.versionInfo.currentVersion"
          class="version-badge"
          :class="{
            'version-clickable': systemStore.versionInfo.status === 'update-available' || systemStore.versionInfo.status === 'latest',
            'version-checking': systemStore.versionInfo.status === 'checking',
            'version-latest': systemStore.versionInfo.status === 'latest',
            'version-update': systemStore.versionInfo.status === 'update-available'
          }"
          @click="handleVersionClick"
        >
          <v-icon
            v-if="systemStore.versionInfo.status === 'checking'"
            size="14"
            class="mr-1"
          >mdi-clock-outline</v-icon>
          <v-icon
            v-else-if="systemStore.versionInfo.status === 'latest'"
            size="14"
            class="mr-1"
            color="success"
          >mdi-check-circle</v-icon>
          <v-icon
            v-else-if="systemStore.versionInfo.status === 'update-available'"
            size="14"
            class="mr-1"
            color="warning"
          >mdi-alert</v-icon>
          <span class="version-text">{{ systemStore.versionInfo.currentVersion }}</span>
          <template v-if="systemStore.versionInfo.status === 'update-available' && systemStore.versionInfo.latestVersion">
            <span class="version-arrow mx-1">→</span>
            <span class="version-latest-text">{{ systemStore.versionInfo.latestVersion }}</span>
          </template>
        </div>

        <!-- 新版本提醒气泡：显示 5 秒自动收起，每小时重复提醒（见 watchUpdateBubble） -->
        <transition name="update-bubble">
          <div v-if="updateBubbleVisible && systemStore.versionInfo.latestVersion" class="update-bubble" role="status">
            <button class="update-bubble__close" aria-label="关闭提醒" @click="hideUpdateBubble">
              <v-icon size="14">mdi-close</v-icon>
            </button>
            <div class="update-bubble__title">
              <v-icon size="15" color="warning">mdi-arrow-up-bold-circle-outline</v-icon>
              发现新版本
            </div>
            <div class="update-bubble__versions">
              <span>{{ systemStore.versionInfo.currentVersion }}</span>
              <v-icon size="13" class="update-bubble__arrow">mdi-arrow-right</v-icon>
              <span class="update-bubble__latest">{{ systemStore.versionInfo.latestVersion }}</span>
            </div>
            <v-btn
              size="x-small"
              variant="flat"
              color="primary"
              class="update-bubble__btn"
              @click="handleVersionClick"
            >
              查看更新
            </v-btn>
          </div>
        </transition>
      </div>

      <!-- 设置 -->
      <v-tooltip text="设置" location="bottom">
        <template #activator="{ props }">
          <v-btn
            v-bind="props"
            icon
            variant="text"
            size="small"
            class="header-btn"
            :color="isSettingsPage ? 'primary' : undefined"
            aria-label="设置"
            @click="router.push('/settings')"
          >
            <v-icon size="20">mdi-cog</v-icon>
          </v-btn>
        </template>
      </v-tooltip>

      <!-- 暗色模式切换 -->
      <v-btn
        icon
        variant="text"
        size="small"
        class="header-btn"
        :aria-label="theme.global.current.value.dark ? '切换到浅色模式' : '切换到深色模式'"
        @click="toggleDarkMode"
      >
        <v-icon size="20">{{
          theme.global.current.value.dark ? 'mdi-weather-night' : 'mdi-white-balance-sunny'
        }}</v-icon>
      </v-btn>

      <!-- 注销按钮 -->
      <v-btn
        v-if="isAuthenticated"
        icon
        variant="text"
        size="small"
        class="header-btn"
        title="注销"
        @click="handleLogout"
      >
        <v-icon size="20">mdi-logout</v-icon>
      </v-btn>
    </v-app-bar>

    <!-- 主要内容 -->
    <v-main>
      <v-container fluid class="pa-4 pa-md-6">
        <template v-if="isAuthenticated && !isStandalonePage">
        <!-- 全局统计顶部可折叠卡片（根据当前 Tab 显示对应统计） -->
        <v-card class="mb-4 global-stats-panel">
          <div
            class="global-stats-header d-flex align-center justify-space-between px-4 py-2"
            style="cursor: pointer;"
            @click="preferencesStore.toggleGlobalStats()"
          >
            <div class="d-flex align-center">
              <v-icon size="20" class="mr-2">mdi-chart-areaspline</v-icon>
             <span class="text-subtitle-1 font-weight-bold">
                {{ activeProtocolLabel }} 流量统计
             </span>
            </div>
            <v-btn
              icon
              size="small"
              variant="text"
              :aria-label="preferencesStore.showGlobalStats ? '收起流量统计' : '展开流量统计'"
            >
              <v-icon>{{ preferencesStore.showGlobalStats ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </v-btn>
          </div>
          <v-expand-transition>
            <div v-if="preferencesStore.showGlobalStats">
              <v-divider />
              <GlobalStatsChart :api-type="channelStore.activeTab" />
            </div>
          </v-expand-transition>
        </v-card>

        <!-- 统计卡片 - 玻璃拟态风格 -->
        <v-row class="mb-6 stat-cards-row">
          <v-col cols="6" sm="4">
            <div class="stat-card stat-card-info">
              <div class="stat-card-icon">
                <v-icon size="28">mdi-server-network</v-icon>
              </div>
              <div class="stat-card-content">
                <div class="stat-card-value">{{ totalChannelsDisplay }}</div>
                <div class="stat-card-label">渠道</div>
              </div>
              <div class="stat-card-glow"></div>
            </div>
          </v-col>

          <v-col cols="6" sm="4">
            <div class="stat-card stat-card-success">
              <div class="stat-card-icon">
                <v-icon size="28">mdi-check-circle</v-icon>
              </div>
              <div class="stat-card-content">
                <div class="stat-card-value">
                  {{ activeChannelCountDisplay }}<span class="stat-card-total">/{{ channelStore.failoverChannelCount }}</span>
                </div>
                <div class="stat-card-label">活跃</div>
              </div>
              <div class="stat-card-glow"></div>
            </div>
          </v-col>

          <v-col cols="6" sm="4">
            <div
              class="stat-card"
              :class="systemStore.systemStatus === 'running' ? 'stat-card-emerald' : 'stat-card-error'"
            >
              <div class="stat-card-icon" :class="{ 'pulse-animation': systemStore.systemStatus === 'running' }">
                <v-icon size="28">{{ systemStore.systemStatus === 'running' ? 'mdi-heart-pulse' : 'mdi-alert-circle' }}</v-icon>
              </div>
              <div class="stat-card-content">
                <div class="stat-card-value">{{ systemStore.systemStatusText }}</div>
                <div class="stat-card-label">服务</div>
              </div>
              <div class="stat-card-glow"></div>
            </div>
          </v-col>
        </v-row>

        <!-- 操作按钮区域 - 现代化设计 -->
        <div class="action-bar mb-6">
          <div class="action-bar-left">
            <v-btn
              color="primary"
              size="large"
              prepend-icon="mdi-plus"
              class="action-btn action-btn-primary"
              @click="openAddChannelModal"
            >
              添加渠道
            </v-btn>

            <v-btn
              color="info"
              size="large"
              prepend-icon="mdi-speedometer"
              variant="tonal"
              :loading="channelStore.isPingingAll"
              class="action-btn"
              @click="pingAllChannels"
            >
              测试延迟
            </v-btn>

            <v-tooltip text="刷新" location="bottom">
              <template #activator="{ props }">
                <v-btn v-bind="props" icon="mdi-refresh" size="large" variant="text" class="action-btn action-icon-btn" aria-label="刷新" @click="refreshChannels" />
              </template>
            </v-tooltip>
          </div>

          <div class="action-bar-right">
            <!-- 客户端伪装切换按钮：仅 Messages / Responses 显示 -->
            <v-tooltip v-if="clientDisguiseProtocol" location="bottom" content-class="fuzzy-tooltip">
              <template #activator="{ props }">
                <v-btn
                  v-bind="props"
                  :variant="clientDisguiseEnabled ? 'flat' : 'outlined'"
                  size="large"
                  :loading="systemStore.clientDisguiseLoading"
                  :disabled="systemStore.clientDisguiseLoadError"
                  :color="clientDisguiseEnabled ? 'success' : undefined"
                  class="action-btn mode-control"
                  :class="{ 'is-on': clientDisguiseEnabled, 'is-off': !clientDisguiseEnabled, 'is-unavailable': systemStore.clientDisguiseLoadError }"
                  :aria-pressed="clientDisguiseEnabled"
                  @click="toggleClientDisguise"
                >
                  <v-icon start size="20">
                    {{ systemStore.clientDisguiseLoadError ? 'mdi-alert-circle-outline' : (clientDisguiseEnabled ? 'mdi-robot' : 'mdi-robot-outline') }}
                  </v-icon>
                  {{ clientDisguiseLabel }}
                  <span class="mode-state">{{ clientDisguiseEnabled ? '开' : '关' }}</span>
                </v-btn>
              </template>
              <span>{{ clientDisguiseTooltip }}</span>
            </v-tooltip>

            <!-- Fuzzy 模式切换按钮 -->
            <v-tooltip location="bottom" content-class="fuzzy-tooltip">
              <template #activator="{ props }">
                <v-btn
                  v-bind="props"
                  :variant="preferencesStore.fuzzyModeEnabled ? 'flat' : 'outlined'"
                  size="large"
                  :loading="systemStore.fuzzyModeLoading"
                  :disabled="systemStore.fuzzyModeLoadError"
                  :color="preferencesStore.fuzzyModeEnabled ? 'warning' : undefined"
                  class="action-btn mode-control"
                  :class="{ 'is-on': preferencesStore.fuzzyModeEnabled, 'is-off': !preferencesStore.fuzzyModeEnabled, 'is-unavailable': systemStore.fuzzyModeLoadError }"
                  :aria-pressed="preferencesStore.fuzzyModeEnabled"
                  @click="toggleFuzzyMode"
                >
                  <v-icon start size="20">
                    {{ systemStore.fuzzyModeLoadError ? 'mdi-alert-circle-outline' : (preferencesStore.fuzzyModeEnabled ? 'mdi-shield-refresh' : 'mdi-shield-off-outline') }}
                  </v-icon>
                  Fuzzy
                  <span class="mode-state">{{ preferencesStore.fuzzyModeEnabled ? '开' : '关' }}</span>
                </v-btn>
              </template>
              <span>{{ systemStore.fuzzyModeLoadError ? '加载失败，请刷新页面' : (preferencesStore.fuzzyModeEnabled ? 'Fuzzy 模式已启用：模糊处理错误，自动尝试所有渠道' : 'Fuzzy 模式已关闭：固定首个渠道，仅重试其 Key 与地址') }}</span>
            </v-tooltip>
          </div>
        </div>

        <!-- 渠道编排（高密度列表模式） -->
        <router-view v-slot="{ Component }">
          <transition name="page" mode="out-in" appear>
            <component
              :is="Component"
              :key="route.path"
              @edit="editChannel"
              @delete="handleDeleteChannel"
              @ping="pingChannel"
              @refresh="refreshChannels"
              @error="showErrorToast"
              @success="showSuccessToast"
            />
          </transition>
        </router-view>
        </template>
        <template v-else-if="isAuthenticated">
          <router-view v-slot="{ Component }">
            <transition name="page" mode="out-in" appear>
              <component :is="Component" :key="route.path" />
            </transition>
          </router-view>
        </template>
      </v-container>
    </v-main>

    <!-- 添加渠道模态框 -->
    <AddChannelModal
      v-model:show="dialogStore.showAddChannelModal"
      :channel="dialogStore.editingChannel"
      :channel-type="dialogStore.channelType"
      :saving="channelStore.isSavingChannel || channelStore.isDeletingChannel"
      @save="handleSaveChannel"
    />

    <!-- Toast通知 -->
    <v-snackbar
      v-for="toast in toasts"
      :key="toast.id"
      v-model="toast.show"
      :color="getToastColor(toast.type)"
      :timeout="3000"
      location="top right"
      variant="elevated"
    >
      <div class="d-flex align-center">
        <v-icon class="mr-3">{{ getToastIcon(toast.type) }}</v-icon>
        {{ toast.message }}
      </div>
    </v-snackbar>

    <!-- 危险命令 AI 闸门：全局待确认通知（轮询 + 声音 + 浏览器系统通知） -->
    <GatePendingNotification v-if="isAuthenticated" />

    <!-- AI 配置助手：全局浮窗 -->
    <AssistantWidget v-if="isAuthenticated && !isStandalonePage" />
  </v-app>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, onBeforeUnmount, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTheme } from 'vuetify'
import { api, fetchHealth, ApiError, channelApiByType, type Channel } from './services/api'
import { versionService } from './services/version'
import GatePendingNotification from './components/GatePendingNotification.vue'
import AssistantWidget from './components/AssistantWidget.vue'
import { useAuthStore } from './stores/auth'
import { useAuthFlow } from './composables/useAuthFlow'
import { useChannelActions } from './composables/useChannelActions'
import { useChannelStore } from './stores/channel'
import { usePreferencesStore } from './stores/preferences'
import { useDialogStore } from './stores/dialog'
import { useSystemStore } from './stores/system'
import { useToast } from './composables/useToast'
import AddChannelModal from './components/AddChannelModal.vue'
import { defineLazyChart } from './utils/lazyChart'
import { useCountUp } from './composables/useCountUp'

const GlobalStatsChart = defineLazyChart(() => import('./components/GlobalStatsChart.vue'))

// Vuetify主题
const theme = useTheme()

// 认证 Store
const authStore = useAuthStore()

// 渠道 Store
const channelStore = useChannelStore()
const route = useRoute()
const router = useRouter()

// 统计卡片数字滚动（数据刷新时平滑过渡，而非硬跳变）
const totalChannels = computed(() => channelStore.currentChannelsData.channels?.length || 0)
const { displayValue: totalChannelsDisplay } = useCountUp(totalChannels)

const activeChannels = computed(() => channelStore.activeChannelCount || 0)
const { displayValue: activeChannelCountDisplay } = useCountUp(activeChannels)

const isConversationPage = computed(() => route.name === 'conversations')
const isLogsPage = computed(() => route.name === 'request-logs')
const isBlockedLogsPage = computed(() => route.name === 'blocked-logs')
const isSkillsPage = computed(() => route.name === 'skills')
const isOpenCodePage = computed(() => route.name === 'opencode')
const isClaudeCodePage = computed(() => route.name === 'claude-code')
const isGptPage = computed(() => route.name === 'gpt')
const isPiAgentPage = computed(() => route.name === 'pi-agent')
const isDshPage = computed(() => route.name === 'dsh')
const isSettingsPage = computed(() => route.name === 'settings')
const isStandalonePage = computed(() => isConversationPage.value || isLogsPage.value || isBlockedLogsPage.value || isSkillsPage.value || isOpenCodePage.value || isClaudeCodePage.value || isGptPage.value || isPiAgentPage.value || isDshPage.value || isSettingsPage.value)

const activeProtocolLabel = computed(() => {
  const labels: Record<string, string> = {
    messages: 'Messages',
    responses: 'Responses',
    gemini: 'Gemini',
    chat: 'Chat',
    images: 'Images',
    embeddings: 'Embeddings',
    decisions: 'Decisions'
  }
  return labels[channelStore.activeTab] ?? 'Chat'
})

const navGroups = [
  {
    key: 'protocols',
    label: '协议',
    items: [
      { key: 'messages', label: 'Messages', to: '/channels/messages' },
      { key: 'responses', label: 'Responses', to: '/channels/responses' },
      { key: 'gemini', label: 'Gemini', to: '/channels/gemini' },
      { key: 'chat', label: 'Chat', to: '/channels/chat' },
      { key: 'images', label: 'Images', to: '/channels/images' },
      { key: 'embeddings', label: 'Embeddings', to: '/channels/embeddings' },
      { key: 'decisions', label: 'Decisions', to: '/channels/decisions' }
    ]
  },
  {
    key: 'workspace',
    label: '工作台',
    items: [
      { key: 'conversations', label: '对话', to: '/conversations' },
      { key: 'logs', label: '日志', to: '/logs' },
      { key: 'blocked-logs', label: '拦截', to: '/blocked-logs' },
      { key: 'skills', label: 'Skills', to: '/skills' }
    ]
  },
  {
    key: 'clients',
    label: '客户端',
    items: [
      { key: 'claude-code', label: 'Claude Code', to: '/claude-code' },
      { key: 'gpt', label: 'GPT', to: '/gpt' },
      { key: 'opencode', label: 'OpenCode', to: '/opencode' },
      { key: 'pi-agent', label: 'pi', to: '/pi-agent' },
      { key: 'dsh', label: 'DSH', to: '/dsh' }
    ]
  }
]

// 偏好设置 Store
const preferencesStore = usePreferencesStore()

// 对话框 Store
const dialogStore = useDialogStore()

// 系统状态 Store
const systemStore = useSystemStore()

// 对话框状态已迁移到 DialogStore

// 主题和偏好设置已迁移到 PreferencesStore

// 系统状态已迁移到 SystemStore

const topNavActive = computed(() => {
  if (isConversationPage.value) {
    return 'conversations'
  }
  if (isLogsPage.value) {
    return 'logs'
  }
  if (isBlockedLogsPage.value) {
    return 'blocked-logs'
  }
  if (isSkillsPage.value) {
    return 'skills'
  }
  if (isOpenCodePage.value) {
    return 'opencode'
  }
  if (isClaudeCodePage.value) {
    return 'claude-code'
  }
  if (isGptPage.value) {
    return 'gpt'
  }
  if (isPiAgentPage.value) {
    return 'pi-agent'
  }
  if (isDshPage.value) {
    return 'dsh'
  }
  if (isSettingsPage.value) {
    return 'settings'
  }
  const type = route.params.type
  return typeof type === 'string' ? type : 'messages'
})

const { toasts, getToastColor, getToastIcon, showToast, showErrorToast, showSuccessToast } = useToast()

// Fuzzy 模式管理
const loadFuzzyModeStatus = async () => {
  systemStore.setFuzzyModeLoadError(false)
  try {
    const { fuzzyModeEnabled: enabled } = await api.getFuzzyMode()
    preferencesStore.setFuzzyMode(enabled)
  } catch (e) {
    console.error('Failed to load fuzzy mode status:', e)
    systemStore.setFuzzyModeLoadError(true)
    // 加载失败时不使用默认值，保持 UI 显示未知状态
    showToast('加载 Fuzzy 模式状态失败，请刷新页面重试', 'warning')
  }
}

const toggleFuzzyMode = async () => {
  if (systemStore.fuzzyModeLoadError) {
    showToast('Fuzzy 模式状态未知，请先刷新页面', 'warning')
    return
  }
  systemStore.setFuzzyModeLoading(true)
  try {
    await api.setFuzzyMode(!preferencesStore.fuzzyModeEnabled)
    preferencesStore.toggleFuzzyMode()
    showToast(`Fuzzy 模式已${preferencesStore.fuzzyModeEnabled ? '启用' : '关闭'}`, 'success')
  } catch (e) {
    showToast(`切换 Fuzzy 模式失败: ${e instanceof Error ? e.message : '未知错误'}`, 'error')
  } finally {
    systemStore.setFuzzyModeLoading(false)
  }
}

const clientDisguiseProtocol = computed<'messages' | 'responses' | null>(() => {
  if (channelStore.activeTab === 'messages' || channelStore.activeTab === 'responses') {
    return channelStore.activeTab
  }
  return null
})

const clientDisguiseEnabled = computed(() => {
  return clientDisguiseProtocol.value === 'messages'
    ? preferencesStore.claudeCodeDisguiseEnabled
    : clientDisguiseProtocol.value === 'responses'
      ? preferencesStore.codexDisguiseEnabled
      : false
})

const clientDisguiseLabel = computed(() => clientDisguiseProtocol.value === 'responses' ? 'Codex 伪装' : 'Claude 伪装')

const clientDisguiseTooltip = computed(() => {
  if (systemStore.clientDisguiseLoadError) return '加载失败，请刷新页面'
  const clientName = clientDisguiseProtocol.value === 'responses' ? 'Codex CLI' : 'Claude Code'
  return clientDisguiseEnabled.value
    ? `${clientName} 伪装已启用：规范化客户端身份头，保留已有会话和协议能力字段`
    : `${clientName} 伪装已关闭：透明转发客户端身份头`
})

const loadClientDisguiseStatus = async () => {
  systemStore.setClientDisguiseLoadError(false)
  try {
    const status = await api.getClientDisguise()
    preferencesStore.setClientDisguise('messages', status.claudeCodeDisguiseEnabled)
    preferencesStore.setClientDisguise('responses', status.codexDisguiseEnabled)
  } catch (e) {
    console.error('Failed to load client disguise status:', e)
    systemStore.setClientDisguiseLoadError(true)
    showToast('加载客户端伪装状态失败，请刷新页面重试', 'warning')
  }
}

const toggleClientDisguise = async () => {
  const protocol = clientDisguiseProtocol.value
  if (!protocol) return
  if (systemStore.clientDisguiseLoadError) {
    showToast('客户端伪装状态未知，请先刷新页面', 'warning')
    return
  }

  const nextEnabled = !clientDisguiseEnabled.value
  systemStore.setClientDisguiseLoading(true)
  try {
    await api.setClientDisguise(protocol, nextEnabled)
    preferencesStore.setClientDisguise(protocol, nextEnabled)
    const clientName = protocol === 'responses' ? 'Codex' : 'Claude Code'
    showToast(`${clientName} 伪装已${nextEnabled ? '启用' : '关闭'}`, 'success')
  } catch (e) {
    showToast(`切换客户端伪装失败: ${e instanceof Error ? e.message : '未知错误'}`, 'error')
  } finally {
    systemStore.setClientDisguiseLoading(false)
  }
}

// 主题管理
const toggleDarkMode = () => {
  const newMode = preferencesStore.darkModePreference === 'dark' ? 'light' : 'dark'
  setDarkMode(newMode)
}

const setDarkMode = (themeName: 'light' | 'dark' | 'auto') => {
  preferencesStore.setDarkMode(themeName)
  const apply = (isDark: boolean) => {
    // 使用 Vuetify 3.9+ 推荐的 theme.change() API
    theme.change(isDark ? 'dark' : 'light')
  }

  if (themeName === 'auto') {
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
    apply(prefersDark)
  } else {
    apply(themeName === 'dark')
  }
  // PreferencesStore 已通过 pinia-plugin-persistedstate 自动持久化，无需手动写入 localStorage
}

// 认证流程收敛到 useAuthFlow composable（R5，方案 8.1）：
// 自动认证 / 手动提交 / 注销 / 认证失败处理离开页面壳，状态本体在 authStore。
const { isAuthenticated, showAuthDialog, autoAuthenticate, handleAuthSubmit, handleLogout, handleAuthError } =
  useAuthFlow(showToast)

// 渠道操作交给 useChannelActions 编排，App.vue 负责调用和展示反馈（R5，方案 8.1）。
// 配置已提交但后续处理失败时，由 composable 统一提示用户刷新（R3）。
const {
  refreshChannels: refreshChannelsAction,
  saveChannel,
  deleteChannel,
  pingChannel,
  pingAllChannels
} = useChannelActions(showToast)

const refreshChannels = () => refreshChannelsAction(handleAuthError)

const handleSaveChannel = (channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }) =>
  saveChannel(channel, options, handleAuthError)

const handleDeleteChannel = (channelId: number) => deleteChannel(channelId, handleAuthError)

const editChannel = (channel: Channel) => {
  dialogStore.openEditChannelModal(channel, channelStore.activeTab)
}

const openAddChannelModal = () => {
  dialogStore.openAddChannelModal(channelStore.activeTab)
}

// 版本检查
const checkVersion = async () => {
  if (systemStore.isCheckingVersion) return

  systemStore.setCheckingVersion(true)
  try {
    // 先获取当前版本
    const health = await fetchHealth()
    const currentVersion = health.version?.version || ''

    if (currentVersion) {
      versionService.setCurrentVersion(currentVersion)
      systemStore.setCurrentVersion(currentVersion)

      // 检查 GitHub 最新版本
      const result = await versionService.checkForUpdates()
      systemStore.setVersionInfo(result)
    } else {
      systemStore.setVersionInfo({
        ...systemStore.versionInfo,
        status: 'error',
      })
    }
  } catch (error) {
    console.warn('Version check failed:', error)
    systemStore.setVersionInfo({
      ...systemStore.versionInfo,
      status: 'error',
    })
  } finally {
    systemStore.setCheckingVersion(false)
  }
}

// 版本点击处理
const handleVersionClick = () => {
  if (
    (systemStore.versionInfo.status === 'update-available' || systemStore.versionInfo.status === 'latest') &&
    systemStore.versionInfo.releaseUrl
  ) {
    window.open(systemStore.versionInfo.releaseUrl, '_blank', 'noopener,noreferrer')
  }
}

// ----- 新版本提醒气泡 -----
// 交互约定（用户确认）：检测到有更新时气泡显示 5 秒自动收起；若用户始终未升级，
// 每隔 1 小时（版本检查缓存的周期）再次弹出。手动点 × 或"查看更新"只结束当次，
// 不影响下一小时的重复提醒——提醒的止点始终是用户完成升级（或上游版本变化）。
const UPDATE_BUBBLE_VISIBLE_MS = 5000
const UPDATE_BUBBLE_REMIND_INTERVAL_MS = 60 * 60 * 1000

const updateBubbleVisible = ref(false)
let updateBubbleHideTimer: ReturnType<typeof setTimeout> | null = null
// 上次自动弹出的时间戳，用于把重复提醒限制为每小时一次
let updateBubbleLastShownAt = 0

const hideUpdateBubble = () => {
  updateBubbleVisible.value = false
  if (updateBubbleHideTimer !== null) {
    clearTimeout(updateBubbleHideTimer)
    updateBubbleHideTimer = null
  }
}

// 状态变化或周期检查后触发：满足"有更新 + 距上次弹出 ≥1 小时"才弹。
const maybeShowUpdateBubble = () => {
  if (systemStore.versionInfo.status !== 'update-available') return
  if (Date.now() - updateBubbleLastShownAt < UPDATE_BUBBLE_REMIND_INTERVAL_MS) return
  updateBubbleLastShownAt = Date.now()
  updateBubbleVisible.value = true
  if (updateBubbleHideTimer !== null) clearTimeout(updateBubbleHideTimer)
  updateBubbleHideTimer = setTimeout(hideUpdateBubble, UPDATE_BUBBLE_VISIBLE_MS)
}

// 版本检查结果落地（包括缓存命中）即尝试弹一次；另起 1 小时定时器覆盖
// "页面长开、缓存 30 分钟后重新检查仍提示更新"的重复提醒。
watch(() => systemStore.versionInfo.status, (status) => {
  if (status === 'update-available') maybeShowUpdateBubble()
})

let updateBubbleRemindTimer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  maybeShowUpdateBubble()
  updateBubbleRemindTimer = setInterval(maybeShowUpdateBubble, UPDATE_BUBBLE_REMIND_INTERVAL_MS)
})
onBeforeUnmount(() => {
  if (updateBubbleRemindTimer !== null) clearInterval(updateBubbleRemindTimer)
  if (updateBubbleHideTimer !== null) clearTimeout(updateBubbleHideTimer)
})

// 初始化
onMounted(async () => {
  // 加载保存的暗色模式偏好（从 PreferencesStore 读取，已自动从 localStorage 恢复）
  setDarkMode(preferencesStore.darkModePreference)

  // 监听系统主题变化
  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  const handlePref = () => {
    if (preferencesStore.darkModePreference === 'auto') setDarkMode('auto')
  }
  mediaQuery.addEventListener('change', handlePref)

  // 版本检查（独立于认证，静默执行）
  checkVersion()

  // 检查 AuthStore 中是否有保存的密钥
  if (authStore.apiKey) {
    // 有保存的密钥，开始自动认证
    authStore.setAutoAuthenticating(true)
    authStore.setInitialized(false)
  } else {
    // 没有保存的密钥，直接显示登录对话框
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
  }

  // 尝试自动认证
  const authenticated = await autoAuthenticate()

  if (authenticated) {
    // 加载渠道数据
    await refreshChannels()
    // 加载 Fuzzy 模式状态
    await loadFuzzyModeStatus()
    // 加载 Messages / Responses 客户端伪装状态
    await loadClientDisguiseStatus()
    // 启动自动刷新
    startAutoRefresh()
    // 初始化完成后根据最新刷新结果设置系统状态
    systemStore.setSystemStatus(channelStore.lastRefreshSuccess ? 'running' : 'error')
  }
})

// 启动自动刷新定时器
const startAutoRefresh = () => {
  channelStore.startAutoRefresh()
}

// 停止自动刷新定时器
const stopAutoRefresh = () => {
  channelStore.stopAutoRefresh()
}

// 监听 Tab 切换，刷新对应数据
watch(() => channelStore.activeTab, async () => {
  if (isAuthenticated.value) {
    try {
      await channelStore.refreshChannels()
    } catch (error) {
      console.error('切换 Tab 刷新失败:', error)
    }
  }
})

// 监听认证状态变化
watch(isAuthenticated, newValue => {
  if (newValue) {
    startAutoRefresh()
  } else {
    stopAutoRefresh()
  }
})

// 监听自动刷新状态，更新 systemStatus
watch(() => channelStore.lastRefreshSuccess, (success) => {
  if (isAuthenticated.value) {
    systemStore.setSystemStatus(success ? 'running' : 'error')
  }
})

// 在组件卸载时清除定时器
onUnmounted(() => {
  channelStore.stopAutoRefresh()
})
</script>

<style scoped src="./styles/app-shell.css"></style>

<!-- ============================================================
     全局样式 — Instrument Deck 设计系统
     柔和的雾底 / 深空底，模块靠深边框与切角"焊"在上面
     ============================================================ -->
<style src="./styles/app-global.css"></style>
