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
                {{ channelStore.activeTab === 'messages' ? 'Messages' : (channelStore.activeTab === 'responses' ? 'Responses' : (channelStore.activeTab === 'gemini' ? 'Gemini' : (channelStore.activeTab === 'images' ? 'Images' : 'Chat'))) }} 流量统计
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
              @delete="deleteChannel"
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
      :channel-type="channelStore.activeTab"
      @save="saveChannel"
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
  </v-app>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, onBeforeUnmount, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTheme } from 'vuetify'
import { api, fetchHealth, ApiError, channelApiByType, type Channel } from './services/api'
import { versionService } from './services/version'
import { useAuthStore } from './stores/auth'
import { useChannelStore } from './stores/channel'
import { usePreferencesStore } from './stores/preferences'
import { useDialogStore } from './stores/dialog'
import { useSystemStore } from './stores/system'
import { useToast } from './composables/useToast'
import AddChannelModal from './components/AddChannelModal.vue'
import GlobalStatsChart from './components/GlobalStatsChart.vue'
import { useAppTheme } from './composables/useTheme'
import { useCountUp } from './composables/useCountUp'

// Vuetify主题
const theme = useTheme()

// 应用主题系统
const { init: initTheme } = useAppTheme()

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
const isEvalPage = computed(() => route.name === 'eval')
const isStandalonePage = computed(() => isConversationPage.value || isLogsPage.value || isBlockedLogsPage.value || isSkillsPage.value || isEvalPage.value || isOpenCodePage.value || isClaudeCodePage.value || isGptPage.value || isPiAgentPage.value || isDshPage.value || isSettingsPage.value)

const navGroups = [
  {
    key: 'protocols',
    label: '协议',
    items: [
      { key: 'messages', label: 'Messages', to: '/channels/messages' },
      { key: 'responses', label: 'Responses', to: '/channels/responses' },
      { key: 'gemini', label: 'Gemini', to: '/channels/gemini' },
      { key: 'chat', label: 'Chat', to: '/channels/chat' },
      { key: 'images', label: 'Images', to: '/channels/images' }
    ]
  },
  {
    key: 'workspace',
    label: '工作台',
    items: [
      { key: 'conversations', label: '对话', to: '/conversations' },
      { key: 'logs', label: '日志', to: '/logs' },
      { key: 'blocked-logs', label: '拦截', to: '/blocked-logs' },
      { key: 'skills', label: 'Skills', to: '/skills' },
      { key: 'eval', label: '评测', to: '/eval' }
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
  if (isEvalPage.value) {
    return 'eval'
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

// 主要功能函数 - 使用 ChannelStore
const refreshChannels = async () => {
  try {
    await channelStore.refreshChannels()
  } catch (error) {
    handleAuthError(error)
  }
}

const saveChannel = async (channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }) => {
  try {
    const result = await channelStore.saveChannel(channel, dialogStore.editingChannel?.index ?? null, options)
    showToast(result.message, 'success')
    if (result.quickAddMessage) {
      showToast(result.quickAddMessage, 'info')
    }
    dialogStore.closeAddChannelModal()
    await refreshChannels()
  } catch (error) {
    handleAuthError(error)
  }
}

const editChannel = (channel: Channel) => {
  dialogStore.openEditChannelModal(channel)
}

const deleteChannel = async (channelId: number) => {
  if (!confirm('确定要删除这个渠道吗？')) return

  try {
    const result = await channelStore.deleteChannel(channelId)
    showToast(result.message, 'success')
  } catch (error) {
    handleAuthError(error)
  }
}

const openAddChannelModal = () => {
  dialogStore.openAddChannelModal()
}

const pingChannel = async (channelId: number) => {
  try {
    await channelStore.pingChannel(channelId)
    // 不再使用 Toast，延迟结果直接显示在渠道列表中
  } catch (error) {
    showToast(`延迟测试失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
  }
}

const pingAllChannels = async () => {
  try {
    await channelStore.pingAllChannels()
    // 不再使用 Toast，延迟结果直接显示在渠道列表中
  } catch (error) {
    showToast(`批量延迟测试失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
  }
}

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

// 认证状态管理（使用 AuthStore）
const isAuthenticated = computed(() => authStore.isAuthenticated)
// 认证相关状态已迁移到 AuthStore

// 认证尝试限制
const MAX_AUTH_ATTEMPTS = 5

// 控制认证对话框显示
const showAuthDialog = computed({
  get: () => {
    // 只有在初始化完成后，且未认证，且不在自动认证中时，才显示对话框
    return authStore.isInitialized && !isAuthenticated.value && !authStore.isAutoAuthenticating
  },
  set: () => {} // 防止外部修改，认证状态只能通过内部逻辑控制
})

// 自动验证保存的密钥
const autoAuthenticate = async () => {
  // 检查 AuthStore 中是否有保存的密钥
  if (!authStore.apiKey) {
    // 没有保存的密钥，显示登录对话框
    authStore.setAuthError('请输入访问密钥以继续')
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
    return false
  }

  // 有保存的密钥，尝试自动认证
  try {
    // 尝试调用API验证密钥是否有效
    await channelApiByType('messages').getChannels()

    // 密钥有效，认证成功
    authStore.setAuthError('')
    return true
  } catch (error) {
    // 仅在明确 401 时视为密钥无效；其他错误（网络/5xx）不应清除密钥
    if (error instanceof ApiError && error.status === 401) {
      console.warn('自动认证失败: 认证失败(401)')
      authStore.clearAuth()
      authStore.setAuthError('保存的访问密钥已失效，请重新输入')
      return false
    }

    console.warn('自动认证暂时失败:', error)
    showToast(`无法验证访问密钥: ${error instanceof Error ? error.message : '未知错误'}`, 'warning')
    // 非 401：保留密钥，继续尝试连接后端（后续刷新会更新系统状态）
    return true
  } finally {
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
  }
}

// 手动设置密钥（用于重新认证）
const setAuthKey = (key: string) => {
  authStore.setApiKey(key)
  authStore.setAuthError('')
}

// 处理认证提交
const handleAuthSubmit = async () => {
  if (!authStore.authKeyInput.trim()) {
    authStore.setAuthError('请输入访问密钥')
    return
  }

  // 检查是否被锁定
  if (authStore.isAuthLocked) {
    const remainingSeconds = Math.ceil((authStore.authLockoutTime! - Date.now()) / 1000)
    authStore.setAuthError(`认证尝试次数过多，请在 ${remainingSeconds} 秒后重试`)
    return
  }

  authStore.setAuthLoading(true)
  authStore.setAuthError('')

  try {
    // 设置密钥
    setAuthKey(authStore.authKeyInput.trim())

    // 测试API调用以验证密钥
    await channelApiByType('messages').getChannels()

    // 认证成功，重置计数器
    authStore.resetAuthAttempts()
    authStore.setAuthLockout(null)

    // 如果成功，加载数据
    await refreshChannels()

    authStore.setAuthKeyInput('')

    // 记录认证成功(前端日志)
    if (import.meta.env.DEV) {
      console.info('✅ 认证成功 - 时间:', new Date().toISOString())
    }
  } catch (error) {
    // 仅在明确 401 时计入认证失败；网络/5xx 不计入失败次数，也不清除已保存密钥
    if (error instanceof ApiError && error.status === 401) {
      authStore.incrementAuthAttempts()

      // 记录认证失败(前端日志)
      console.warn('🔒 认证失败 - 尝试次数:', authStore.authAttempts, '时间:', new Date().toISOString())

      // 如果尝试次数过多，锁定5分钟
      if (authStore.authAttempts >= MAX_AUTH_ATTEMPTS) {
        authStore.setAuthLockout(new Date(Date.now() + 5 * 60 * 1000))
        authStore.setAuthError('认证尝试次数过多，请在5分钟后重试')
      } else {
        authStore.setAuthError(`访问密钥验证失败 (剩余尝试次数: ${MAX_AUTH_ATTEMPTS - authStore.authAttempts})`)
      }

      authStore.clearAuth()
      return
    }

    showToast(`无法验证访问密钥: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
  } finally {
    authStore.setAuthLoading(false)
  }
}

// 处理注销
const handleLogout = () => {
  authStore.clearAuth()
  channelStore.clearChannels()
  authStore.setAuthError('请输入访问密钥以继续')
  showToast('已安全注销', 'info')
}

// 处理认证失败
const handleAuthError = (error: any) => {
  if (error.message && error.message.includes('认证失败')) {
    authStore.setAuthError('访问密钥无效或已过期，请重新输入')
  } else {
    showToast(`操作失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
  }
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
  // 初始化玻璃拟态主题
  initTheme()

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

<style scoped>
/* ============================================================
   API Proxy — 清晰、克制的控制台视觉系统
   辨识度来自网格、排版和局部色轨
   ============================================================ */

/* ----- 应用栏 ----- */
.app-header {
  background: rgba(var(--v-theme-surface), 0.94) !important;
  backdrop-filter: blur(14px) saturate(1.2) !important;
  -webkit-backdrop-filter: blur(14px) saturate(1.2) !important;
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.62) !important;
  box-shadow: 0 1px 0 rgba(var(--v-theme-primary), 0.16) !important;
  padding: 0 20px !important;
  transition: background 0.2s ease, border-color 0.2s ease;
}

.v-theme--dark .app-header {
  background: rgba(var(--v-theme-surface), 0.9) !important;
  border-bottom-color: rgba(var(--v-theme-outline), 0.85) !important;
  box-shadow: 0 1px 0 rgba(var(--v-theme-primary), 0.2) !important;
}

.app-header :deep(.v-toolbar__prepend) {
  margin-inline-end: 4px !important;
}

.app-header .v-toolbar-title {
  overflow: hidden !important;
  min-width: 0 !important;
  flex: 1 !important;
}

.app-header :deep(.v-toolbar__content) {
  overflow: visible !important;
}

.auth-card { overflow: hidden; }
.auth-title {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  margin-bottom: 14px;
  font-size: 1.25rem;
  font-weight: 750;
}
.auth-title-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  color: rgb(var(--v-theme-on-primary));
  background: rgb(var(--v-theme-primary));
  border-radius: 9px 3px 9px 3px;
  box-shadow: 0 5px 14px rgba(var(--v-theme-primary), 0.28);
}
.auth-footnote {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin-top: 16px;
  color: rgba(var(--v-theme-on-surface), 0.48);
  font-size: 0.75rem;
}

.app-logo {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, rgb(var(--v-theme-primary)), rgb(var(--v-theme-primary-darken-1)));
  border: 1px solid rgba(var(--v-theme-primary-darken-1), 0.8);
  /* 不对称切角 — 品牌形状的第一处露出 */
  border-radius: 11px 3px 11px 3px;
  box-shadow: 0 3px 10px rgba(var(--v-theme-primary), 0.3), inset 0 1px 0 rgba(255, 255, 255, 0.28);
  margin-right: 10px;
  flex-shrink: 0;
  transition: transform 0.2s var(--ease-spring), box-shadow 0.2s ease;
}
.app-logo:hover { transform: translateY(-2px) rotate(-6deg); box-shadow: 0 6px 16px rgba(var(--v-theme-primary), 0.38), inset 0 1px 0 rgba(255, 255, 255, 0.28); }
.v-theme--dark .app-logo { border-color: rgba(var(--v-theme-primary-lighten-1), 0.55); box-shadow: 0 3px 12px rgba(var(--v-theme-primary), 0.2), inset 0 1px 0 rgba(255, 255, 255, 0.16); }

/* 标题容器 */
.header-title {
  display: flex;
  align-items: center;
  flex: 1 1 auto;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
  gap: 8px;
}
.header-title::-webkit-scrollbar { display: none; }
.nav-group {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 3px;
  flex: 0 0 auto;
  border: 1px solid rgba(var(--v-theme-outline), 0.38);
  border-radius: 10px 3px 10px 3px;
  background: rgba(var(--v-theme-surface-variant), 0.38);
}

.api-type-text {
  cursor: pointer;
  opacity: 0.6;
  transition: color 0.16s ease, background 0.16s ease, transform 0.16s var(--ease-out);
  padding: 5px 10px;
  text-decoration: none;
  color: inherit;
  border: 1px solid transparent;
  border-radius: 7px 2px 7px 2px;
  font-size: 0.85rem;
  font-weight: 600;
}

a.api-type-text { display: inline-block; }
.api-type-text:hover { opacity: 0.95; background: rgba(var(--v-theme-primary), 0.1); transform: translateY(-1px); }
.api-type-text.active {
  opacity: 1;
  font-weight: 800;
  color: rgb(var(--v-theme-on-primary));
  background: rgb(var(--v-theme-primary));
  border-color: transparent;
  box-shadow: 0 2px 8px rgba(var(--v-theme-primary), 0.28);
}
.v-theme--dark .nav-group {
  background: rgba(255, 255, 255, 0.035);
  border-color: rgba(var(--v-theme-outline), 0.62);
}
.v-theme--dark .api-type-text:hover {
  background: rgba(var(--v-theme-primary), 0.1);
}

.header-btn {
  margin-left: 2px;
  transition: background 0.15s ease, border-color 0.15s ease !important;
  border: 1px solid transparent;
  border-radius: 7px 2px 7px 2px !important;
}
.header-btn:hover { background: rgba(var(--v-theme-primary), 0.1) !important; border-color: rgba(var(--v-theme-primary), 0.45); }

/* ----- 版本信息 ----- */
.version-badge {
  display: flex;
  align-items: center;
  padding: 2px 10px;
  margin-right: 6px;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  font-size: 11px;
  border-radius: 6px 2px 6px 2px;
  background: rgba(var(--v-theme-surface-variant), 0.7);
  border: 1px solid rgba(var(--v-theme-outline), 0.6);
  box-shadow: none;
  transition: all 0.25s ease;
  cursor: default;
}
.version-badge.version-clickable { cursor: pointer; }
.version-badge.version-clickable:hover {
  background: rgba(var(--v-theme-primary), 0.1);
  border-color: rgba(var(--v-theme-primary), 0.55);
  transform: translateY(-1px);
  box-shadow: 0 2px 7px rgba(var(--v-theme-primary), 0.14);
}
.version-badge.version-latest { border-color: rgba(var(--v-theme-success), 0.6); background: rgba(var(--v-theme-success), 0.1); }
.version-badge.version-update { border-color: rgba(var(--v-theme-warning), 0.6); background: rgba(var(--v-theme-warning), 0.1); }
.version-text { color: rgb(var(--v-theme-on-surface)); }
.version-arrow { color: rgb(var(--v-theme-warning)); font-weight: 700; }
.version-latest-text { color: rgb(var(--v-theme-warning)); font-weight: 700; }

/* ----- 新版本提醒气泡 ----- */
.version-anchor { position: relative; margin-right: 6px; }
.version-anchor .version-badge { margin-right: 0; }

.update-bubble {
  position: absolute;
  top: calc(100% + 10px);
  right: 0;
  z-index: 1200;
  width: 220px;
  padding: 12px 14px;
  border-radius: 8px;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-warning), 0.55);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.18);
}
/* 指向版本徽章的小箭头（边框色与气泡边框衔接） */
.update-bubble::before {
  content: '';
  position: absolute;
  top: -5px;
  right: 16px;
  width: 9px;
  height: 9px;
  background: rgb(var(--v-theme-surface));
  border-left: 1px solid rgba(var(--v-theme-warning), 0.55);
  border-top: 1px solid rgba(var(--v-theme-warning), 0.55);
  transform: rotate(45deg);
}
.update-bubble__close {
  position: absolute;
  top: 4px;
  right: 4px;
  padding: 4px;
  border-radius: 4px;
  color: rgb(var(--v-theme-on-surface), 0.6);
}
.update-bubble__close:hover { background: rgba(var(--v-theme-on-surface), 0.08); }
.update-bubble__title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 700;
  margin-bottom: 6px;
}
.update-bubble__versions {
  display: flex;
  align-items: center;
  gap: 4px;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  font-size: 12px;
  color: rgb(var(--v-theme-on-surface));
  margin-bottom: 10px;
}
.update-bubble__latest { color: rgb(var(--v-theme-warning)); font-weight: 700; }
.update-bubble__btn { text-transform: none; }

/* 气泡浮现/收起过渡 */
.update-bubble-enter-active { transition: opacity 0.25s ease, transform 0.25s ease; }
.update-bubble-leave-active { transition: opacity 0.2s ease, transform 0.2s ease; }
.update-bubble-enter-from,
.update-bubble-leave-to { opacity: 0; transform: translateY(-6px); }

/* 暗色模式版本徽章 */
.v-theme--dark .version-badge {
  background: rgba(255, 255, 255, 0.05);
  border-color: rgba(var(--v-theme-outline), 0.75);
}
.v-theme--dark .version-badge.version-latest {
  border-color: rgba(var(--v-theme-success), 0.5);
  background: rgba(var(--v-theme-success), 0.1);
}
.v-theme--dark .version-badge.version-update {
  border-color: rgba(var(--v-theme-warning), 0.5);
  background: rgba(var(--v-theme-warning), 0.1);
}

/* ----- 统计卡片：用细色轨区分类型，避免装饰压过数据 ----- */
.stat-cards-row { margin-bottom: 20px !important; }

/* 统计卡与操作栏入场浮现（阶梯 delay，仅首屏播放一次） */
.stat-cards-row .v-col:nth-child(1) .stat-card { animation: float-in 0.5s ease-out both; animation-delay: 60ms; }
.stat-cards-row .v-col:nth-child(2) .stat-card { animation: float-in 0.5s ease-out both; animation-delay: 120ms; }
.stat-cards-row .v-col:nth-child(3) .stat-card { animation: float-in 0.5s ease-out both; animation-delay: 180ms; }
.action-bar { animation: float-in 0.5s ease-out both; animation-delay: 240ms; }

.stat-card {
  /* 每张卡的语义色，驱动色轨 / 图标 / 数值，避免三处各写一遍 */
  --sc: var(--v-theme-primary);
  position: relative;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px 22px 20px 26px;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-outline), 0.62);
  /* 与主卡片反向的切角 — 同一形状语言，不同节奏 */
  border-radius: var(--cut-sm) var(--cut-lg) var(--cut-sm) var(--cut-lg);
  box-shadow: var(--shadow-1);
  transition: transform 0.2s var(--ease-out), box-shadow 0.2s ease, border-color 0.2s ease;
  overflow: hidden;
  min-height: 106px;
}

.stat-card-info { --sc: var(--v-theme-primary); }
.stat-card-success,
.stat-card-emerald { --sc: var(--v-theme-success); }
.stat-card-error { --sc: var(--v-theme-error); }

/* 左侧刻度色轨 — 从顶部横条改到侧边，配合右上大切角形成不对称构图 */
.stat-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: var(--rail-strong);
  background:
    repeating-linear-gradient(180deg,
      rgba(255, 255, 255, 0.5) 0 1px,
      transparent 1px 9px),
    linear-gradient(180deg,
      rgb(var(--sc)) 0%,
      rgba(var(--sc), 0.45) 50%,
      rgb(var(--sc)) 100%);
  background-size: 100% 100%, 100% 200%;
  box-shadow: inset -1px 0 0 rgba(0, 0, 0, 0.14);
  pointer-events: none;
  z-index: 2;
}

/* 色轨上滑过的高光 — 装置感扫描，缓慢循环 */
.stat-card::after {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  width: var(--rail-strong);
  height: 34%;
  background: linear-gradient(180deg, transparent, rgba(255, 255, 255, 0.75), transparent);
  pointer-events: none;
  z-index: 3;
  animation: rail-sweep 4.6s ease-in-out infinite;
}

/* 色轨纵向扫光 */
@keyframes rail-sweep {
  0%   { transform: translateY(-120%); opacity: 0; }
  15%  { opacity: 1; }
  85%  { opacity: 1; }
  100% { transform: translateY(420%); opacity: 0; }
}

.stat-card:hover {
  border-color: rgba(var(--sc), 0.7);
  box-shadow: var(--shadow-2);
  transform: translateY(-3px);
}

/* 统计卡底部微光 — 悬停时泛起的呼吸光晕 */
.stat-card-glow {
  position: absolute;
  right: -24px;
  bottom: -36px;
  width: 160px;
  height: 160px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(var(--sc), 0.22), transparent 70%);
  filter: blur(10px);
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.4s ease, transform 0.4s ease;
}
.stat-card:hover .stat-card-glow {
  opacity: 1;
  transform: scale(1.15);
}

.v-theme--dark .stat-card {
  border-color: rgba(var(--v-theme-outline), 0.85);
  box-shadow: var(--shadow-1), var(--inset-hi);
}

.v-theme--dark .stat-card:hover {
  border-color: rgba(var(--sc), 0.8);
  box-shadow: var(--shadow-2), var(--inset-hi);
}

.stat-card-icon {
  width: 50px;
  height: 50px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  /* 与卡片反向切角保持同一族形状 */
  border-radius: var(--cut-xs) var(--cut-md) var(--cut-xs) var(--cut-md);
  background: rgb(var(--sc));
  box-shadow: 0 3px 12px rgba(var(--sc), 0.3), inset 0 1px 0 rgba(255, 255, 255, 0.3);
  position: relative;
  isolation: isolate;
  transition: transform 0.22s var(--ease-spring), box-shadow 0.22s ease;
}
.stat-card:hover .stat-card-icon {
  transform: scale(1.08) rotate(-4deg);
  box-shadow: 0 5px 18px rgba(var(--sc), 0.42), inset 0 1px 0 rgba(255, 255, 255, 0.3);
}

.stat-card-content { flex: 1; min-width: 0; }

.stat-card-value {
  font-size: 2rem;
  font-weight: 700;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  font-variant-numeric: tabular-nums;
  line-height: 1.15;
  letter-spacing: -0.03em;
  color: rgb(var(--sc));
}

.stat-card-total { font-size: 0.9rem; font-weight: 500; opacity: 0.4; }

.stat-card-label {
  font-size: 0.72rem;
  font-weight: 800;
  margin-top: 4px;
  opacity: 0.7;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: rgb(var(--v-theme-on-surface));
}

.stat-card-icon .v-icon { color: rgb(var(--v-theme-surface)) !important; }
.v-theme--dark .stat-card-icon .v-icon { color: rgb(var(--v-theme-background)) !important; }

/* ----- 操作按钮区域 ----- */
.action-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 20px 16px 24px;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-outline), 0.62);
  border-radius: var(--cut-sm) var(--cut-lg) var(--cut-sm) var(--cut-lg);
  box-shadow: var(--shadow-1);
  position: relative;
  overflow: hidden;
}

/* 左侧刻度色轨 — 与统计卡同一装置语言 */
.action-bar::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: var(--rail);
  background:
    repeating-linear-gradient(180deg,
      rgba(255, 255, 255, 0.5) 0 1px,
      transparent 1px 9px),
    linear-gradient(180deg, rgb(var(--v-theme-primary)), rgba(var(--v-theme-primary), 0.4));
  box-shadow: inset -1px 0 0 rgba(0, 0, 0, 0.14);
  transition: opacity 0.3s ease;
}

.v-theme--dark .action-bar {
  border-color: rgba(var(--v-theme-outline), 0.85);
  box-shadow: var(--shadow-1), var(--inset-hi);
}

.action-bar-left, .action-bar-right { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }

.action-btn {
  font-weight: 700;
  transition: transform 0.15s var(--ease-out), box-shadow 0.15s ease;
}

.action-icon-btn { min-width: 48px !important; }
.mode-control {
  min-width: 148px;
  justify-content: space-between;
  gap: 10px;
  border-width: 1px !important;
}
.mode-control.is-on { box-shadow: 0 3px 12px rgba(0, 0, 0, 0.16) !important; }
.mode-control.is-off {
  color: rgba(var(--v-theme-on-surface), 0.65) !important;
  border-color: rgba(var(--v-theme-outline), 0.72) !important;
}
.mode-control.is-unavailable { opacity: 0.46; cursor: not-allowed !important; }
.mode-state {
  min-width: 20px;
  padding: 2px 5px;
  border-radius: 4px 1px 4px 1px;
  font-size: 10px;
  line-height: 1.2;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  opacity: 0.8;
}
.mode-control.is-on .mode-state { background: rgba(255, 255, 255, 0.18); }
.mode-control.is-off .mode-state { background: rgba(var(--v-theme-on-surface), 0.08); }

.action-btn-primary {
  background: rgb(var(--v-theme-primary)) !important;
  color: rgb(var(--v-theme-on-primary)) !important;
  border: 1px solid rgba(var(--v-theme-primary-darken-1), 0.9) !important;
  box-shadow: 0 3px 9px rgba(var(--v-theme-primary), 0.26) !important;
}
.action-btn-primary:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(var(--v-theme-primary), 0.34) !important;
}

/* ----- 全局统计面板 ----- */
.global-stats-panel {
  background: rgb(var(--v-theme-surface)) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.62) !important;
  border-radius: var(--cut-lg) var(--cut-sm) var(--cut-lg) var(--cut-sm) !important;
  overflow: hidden;
  box-shadow: var(--shadow-1) !important;
}

.v-theme--dark .global-stats-panel {
  border-color: rgba(var(--v-theme-outline), 0.85) !important;
  box-shadow: var(--shadow-1), var(--inset-hi) !important;
}

.global-stats-header { min-height: 58px; position: relative; transition: background 0.15s ease; }
.global-stats-header::before { display: none; }
.global-stats-header > div:first-child { margin-left: 0; }
.global-stats-header:hover { background: rgba(var(--v-theme-primary), 0.06); }

/* ----- 渠道过渡动画 ----- */
.channel-list-enter-active, .channel-list-leave-active { transition: all 0.25s ease; }
.channel-list-enter-from { opacity: 0; transform: translateY(10px); }
.channel-list-leave-to { opacity: 0; transform: translateY(-8px); }
.channel-list-move { transition: transform 0.2s ease; }

/* 心跳动画 — 呼吸缩放 + 微光，营造"系统活着"的感觉 */
.pulse-animation { animation: pulse-glow 2.2s ease-in-out infinite; }
@keyframes pulse-glow {
  0%, 100% { opacity: 1; transform: scale(1); }
  50%      { opacity: 0.85; transform: scale(0.94); }
}

/* 运行状态图标 — 同心扩散，心跳般的持续呼吸 */
.stat-card-emerald .stat-card-icon::after {
  content: '';
  position: absolute;
  inset: -2px;
  border-radius: inherit;
  border: 2px solid rgba(var(--v-theme-success), 0.6);
  opacity: 0;
  animation: pulse-ring 2.4s ease-out infinite;
  pointer-events: none;
}

/* 响应式 */
@media (min-width: 768px) { .app-header { padding: 0 28px !important; } }
@media (min-width: 1024px) { .app-header { padding: 0 32px !important; } }

@media (max-width: 600px) {
  .v-main .v-container { padding-left: 10px !important; padding-right: 10px !important; }
  .app-header { padding: 0 12px !important; }
  .app-logo { width: 28px; height: 28px; margin-right: 6px; }
  .header-title { gap: 5px; }
  .nav-group { gap: 0; padding: 2px; }
  .api-type-text { padding: 4px 7px; font-size: 0.72rem; }
  .stat-card { padding: 16px 14px; gap: 12px; min-height: 84px; box-shadow: 0 2px 7px rgba(24, 28, 38, 0.05); }
  .stat-card-icon { width: 38px; height: 38px; }
  .stat-card-icon .v-icon { font-size: 18px !important; }
  .stat-card-value { font-size: 1.3rem; }
  .stat-card-label { font-size: 0.68rem; }
  .stat-cards-row { margin-bottom: 14px !important; }
  .stat-cards-row .v-col { padding: 4px !important; }
  .stat-cards-row .v-col:last-child { flex: 0 0 100%; max-width: 100%; }
  .action-bar { flex-direction: column; gap: 10px; padding: 14px; }
  .action-bar-left { width: 100%; display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .action-bar-left .action-btn { width: 100%; justify-content: center; }
  .action-bar-left .action-btn:nth-child(3) { grid-column: 1 / -1; }
  .action-bar-right { width: 100%; display: grid; grid-template-columns: auto 1fr; gap: 8px; }
  .mode-control { min-width: 0; width: 100%; }
}

@media (prefers-reduced-motion: reduce) {
  .app-logo,
  .api-type-text,
  .stat-card,
  .stat-card::after,
  .stat-card-icon,
  .stat-card-glow,
  .action-btn,
  .pulse-animation,
  .stat-card-emerald .stat-card-icon::after,
  .channel-list-enter-active,
  .channel-list-leave-active,
  .channel-list-move { transition: none !important; animation: none !important; }
}
</style>

<!-- ============================================================
     全局样式 — Instrument Deck 设计系统
     柔和的雾底 / 深空底，模块靠深边框与切角"焊"在上面
     ============================================================ -->
<style>
/* 全局背景 — 工程图纸网格 + 顶部主色微光；网格比色块更耐看，也更不像模板 */
.v-application {
  background:
    radial-gradient(1100px 460px at 50% -170px, rgba(var(--v-theme-primary), 0.07), transparent 62%),
    linear-gradient(rgba(var(--v-theme-on-background), 0.022) 1px, transparent 1px),
    linear-gradient(90deg, rgba(var(--v-theme-on-background), 0.022) 1px, transparent 1px),
    rgb(var(--v-theme-background)) !important;
  background-size: auto, 26px 26px, 26px 26px, auto;
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif !important;
  min-height: 100vh;
}

/* 暗色模式 — 顶部主色光晕 + 右下角铜色微光，两点光源拉开纵深 */
.v-application.v-theme--dark {
  background:
    radial-gradient(1200px 520px at 50% -190px, rgba(var(--v-theme-primary), 0.13), transparent 62%),
    radial-gradient(900px 460px at 100% 100%, rgba(var(--v-theme-accent), 0.05), transparent 62%),
    linear-gradient(rgba(255, 255, 255, 0.022) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.022) 1px, transparent 1px),
    rgb(var(--v-theme-background)) !important;
  background-size: auto, auto, 26px 26px, 26px 26px, auto;
}

.v-main {
  background: transparent !important;
  position: relative;
  z-index: 1;
}

/* 主卡片 — 对角不对称切角是全站签名；边框深、影子浅，模块边界硬但不压抑 */
.v-card {
  border-radius: var(--cut-lg) var(--cut-sm) var(--cut-lg) var(--cut-sm) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.62) !important;
  box-shadow: var(--shadow-1) !important;
}

/* 暗色卡片 — 提亮一档 + 内高光勾边，从近黑底上"浮"起来 */
.v-application.v-theme--dark .v-card {
  background: rgb(var(--v-theme-surface)) !important;
  border-color: rgba(var(--v-theme-outline), 0.85) !important;
  box-shadow: var(--shadow-1), var(--inset-hi) !important;
}

/* 代码元素 — Fira Code 等宽字体 */
code, pre, .text-mono {
  font-family: 'Fira Code', 'JetBrains Mono', 'Consolas', 'Courier New', monospace;
  font-size: 0.85em;
  line-height: 1.5;
}

code {
  background: rgba(var(--v-theme-primary), 0.07);
  border: 1px solid rgba(var(--v-theme-primary), 0.18);
  border-radius: 4px 1px 4px 1px;
  padding: 1px 5px;
  color: rgb(var(--v-theme-on-surface-variant));
}

.v-application.v-theme--dark code {
  background: rgba(var(--v-theme-primary), 0.1);
  border-color: rgba(var(--v-theme-primary), 0.24);
  color: rgb(var(--v-theme-primary-lighten-1));
}

/* 无边框卡片变体 */
.v-card.v-card--flat {
  border: none !important;
  box-shadow: none !important;
}

/* 按钮 — 小号切角，与卡片大切角形成层级对比 */
.v-btn:not(.v-btn--icon) {
  border-radius: 9px 3px 9px 3px !important;
  font-weight: 700 !important;
  text-transform: none !important;
  letter-spacing: 0.01em;
}

.v-btn--icon { border-radius: 8px 3px 8px 3px !important; }

/* 描边按钮 — 边框加实，弱化"幽灵按钮"的模板感 */
.v-btn--variant-outlined { border-width: 1px !important; }

/* Chip — 切角 + 实边框 */
.v-chip {
  border-radius: 5px 1px 5px 1px !important;
  font-weight: 650;
}

/* 对话框 */
.v-dialog .v-card {
  border-radius: var(--cut-lg) var(--cut-sm) var(--cut-lg) var(--cut-sm) !important;
  box-shadow: 0 28px 60px rgba(0, 0, 0, 0.2) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.6) !important;
}

.v-application.v-theme--dark .v-dialog .v-card {
  box-shadow: 0 28px 70px rgba(0, 0, 0, 0.6), 0 0 44px rgba(var(--v-theme-primary), 0.06) !important;
  border-color: rgba(var(--v-theme-outline), 0.9) !important;
}

/* 菜单 */
.v-menu > .v-overlay__content > .v-list {
  border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.55) !important;
  box-shadow: var(--shadow-2) !important;
  padding: 6px !important;
}

.v-application.v-theme--dark .v-menu > .v-overlay__content > .v-list {
  background: rgb(var(--v-theme-surface)) !important;
  border-color: rgba(var(--v-theme-outline), 0.9) !important;
}

.v-menu > .v-overlay__content > .v-list .v-list-item {
  border-radius: 5px 1px 5px 1px !important;
  margin-bottom: 1px;
}

/* Snackbar */
.v-snackbar__wrapper {
  border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs) !important;
  box-shadow: var(--shadow-2) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.5) !important;
}

/* 输入框 */
.v-text-field .v-field,
.v-select .v-field {
  border-radius: 8px 2px 8px 2px !important;
}

/* 工具提示 */
.v-tooltip > .v-overlay__content {
  border-radius: 6px 1px 6px 1px !important;
  font-size: 0.8rem;
  padding: 6px 12px;
  border: 1px solid rgba(var(--v-theme-outline), 0.4);
}

/* 表格 */
.v-table {
  border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.4) !important;
}

/* 展开面板 */
.v-expansion-panel {
  border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.45) !important;
}

.v-expansion-panel-title {
  border-radius: inherit !important;
}

/* 分割线 */
.v-divider {
  border-color: rgba(var(--v-theme-outline), 0.4) !important;
}

/* 进度条 */
.v-progress-circular { stroke-linecap: round; }

/* 按钮组 */
.v-btn-toggle .v-btn:first-child { border-radius: 8px 0 8px 0 !important; }
.v-btn-toggle .v-btn:last-child { border-radius: 0 8px 0 8px !important; }

/* 警报 */
.v-alert { border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs) !important; }

/* 选中态高亮 */
.v-list-item--active {
  background: rgba(var(--v-theme-primary), 0.1) !important;
}

/* 输入框 outline 圆角 */
.v-field--variant-outlined .v-field__outline__start,
.v-field--variant-outlined .v-field__outline__end,
.v-field--variant-outlined .v-field__outline__notch::before,
.v-field--variant-outlined .v-field__outline__notch::after {
  border-radius: 6px !important;
}

/* 通用浮层 tooltip */
.fuzzy-tooltip {
  background: rgb(var(--v-theme-surface)) !important;
  color: rgb(var(--v-theme-on-surface)) !important;
  border: 1px solid rgba(var(--v-theme-outline), 0.6) !important;
  border-radius: 8px 2px 8px 2px !important;
  box-shadow: var(--shadow-2) !important;
  padding: 8px 14px !important;
  font-size: 0.8rem !important;
}

:where(a, button, [role='button'], .v-btn, .v-list-item):focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary)) !important;
  outline-offset: 3px;
}

:where(a, button, [role='button'], .v-btn, .v-list-item) {
  touch-action: manipulation;
}

/* 文本选中 — 主色薄底，保留原文字色，不做高饱和反白 */
::selection {
  background: rgba(var(--v-theme-primary), 0.22);
  color: inherit;
}
</style>
