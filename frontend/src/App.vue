<template>
  <v-app>
    <a class="skip-link" href="#main-content">跳转到主要内容</a>

    <!-- 自动认证加载提示：只在使用已保存密钥验证时显示 -->
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

    <v-dialog v-model="showAuthDialog" persistent max-width="500">
      <v-card class="pa-4">
        <v-card-title class="auth-title">
          <span class="auth-title-icon"><v-icon size="22">mdi-key</v-icon></span>
          <span>API Proxy</span>
        </v-card-title>

        <v-card-text>
          <v-alert v-if="authStore.authError" type="error" variant="tonal" class="mb-4">
            {{ authStore.authError }}
          </v-alert>

          <v-form @submit.prevent="submitAuth">
            <v-text-field
              v-model="authStore.authKeyInput"
              label="访问密钥 (PROXY_ACCESS_KEY)"
              type="password"
              prepend-inner-icon="mdi-key"
              :rules="[v => !!v || '请输入访问密钥']"
              required
              autofocus
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

    <!-- 顶栏平铺全部功能入口；宽度不足时导航下移到第二行，仍保持一次点击可达。 -->
    <v-app-bar elevation="0" :height="56" :extended="isAuthenticated && navigationBelow" :extension-height="48" class="app-header">
      <router-link to="/channels/messages" class="app-brand" aria-label="API Proxy 首页">
        <span class="app-brand__icon"><v-icon size="20">mdi-api</v-icon></span>
        <span class="app-brand__name">API Proxy</span>
      </router-link>

      <AppNavigation v-if="isAuthenticated && !navigationBelow" />
      <v-spacer v-else />

      <VersionBadge />

      <v-tooltip text="设置" location="bottom">
        <template #activator="{ props }">
          <v-btn
            v-bind="props"
            icon="mdi-cog"
            variant="text"
            size="small"
            to="/settings"
            :active="false"
            :color="route.name === 'settings' ? 'primary' : undefined"
            aria-label="设置"
          />
        </template>
      </v-tooltip>

      <v-btn
        :icon="isDark ? 'mdi-weather-night' : 'mdi-white-balance-sunny'"
        variant="text"
        size="small"
        :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'"
        @click="applyTheme(isDark ? 'light' : 'dark')"
      />

      <v-btn v-if="isAuthenticated" icon="mdi-logout" variant="text" size="small" title="注销" aria-label="注销" @click="handleLogout" />

      <template v-if="isAuthenticated && navigationBelow" #extension>
        <AppNavigation />
      </template>
    </v-app-bar>

    <v-main id="main-content" tabindex="-1">
      <v-container fluid class="app-content pa-4 pa-md-6">
        <!-- 认证后直接挂载目标页面，让懒加载与首屏请求立即开始。 -->
        <router-view v-if="isAuthenticated" v-slot="{ Component }">
          <component :is="Component" :key="route.path" />
        </router-view>
      </v-container>
    </v-main>

    <v-snackbar
      v-for="toast in toasts"
      :key="toast.id"
      v-model="toast.show"
      :color="toast.type"
      :timeout="TOAST_DURATION_MS"
      location="top right"
      variant="elevated"
    >
      <div class="d-flex align-center">
        <v-icon class="mr-3">{{ toastIcons[toast.type] }}</v-icon>
        {{ toast.message }}
      </div>
    </v-snackbar>

    <!-- 危险命令 AI 闸门：全局待确认通知（轮询 + 声音 + 浏览器系统通知） -->
    <GatePendingNotification v-if="isAuthenticated" />
  </v-app>
</template>

<script setup lang="ts">
// 应用壳：认证、顶栏导航、主题与全局反馈。业务页面的内容与数据刷新由各自的视图负责。
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useDisplay, useTheme } from 'vuetify'
import AppNavigation from './components/layout/AppNavigation.vue'
import VersionBadge from './components/layout/VersionBadge.vue'
import GatePendingNotification from './components/GatePendingNotification.vue'
import { useAuthFlow } from './composables/useAuthFlow'
import { toastIcons, useToast } from './composables/useToast'
import { useAuthStore } from './stores/auth'
import { usePreferencesStore, type ThemePreference } from './stores/preferences'

const route = useRoute()
const theme = useTheme()
const display = useDisplay()
const authStore = useAuthStore()
const preferencesStore = usePreferencesStore()
const { toasts, showToast, TOAST_DURATION_MS } = useToast()

// 全部入口排成一行约需 1400px，不足时把导航放到顶栏第二行并允许横向滚动。
const navigationBelow = computed(() => display.width.value < 1440)

const isDark = computed(() => theme.global.current.value.dark)
const systemThemeQuery = window.matchMedia('(prefers-color-scheme: dark)')

const applyTheme = (preference: ThemePreference) => {
  preferencesStore.setDarkMode(preference)
  const dark = preference === 'auto' ? systemThemeQuery.matches : preference === 'dark'
  theme.change(dark ? 'dark' : 'light')
}
const followSystemTheme = () => {
  if (preferencesStore.darkModePreference === 'auto') applyTheme('auto')
}

// 认证流程由 useAuthFlow 编排（R5，方案 8.1），状态本体在 authStore。
const { isAuthenticated, showAuthDialog, autoAuthenticate, handleAuthSubmit, handleLogout } = useAuthFlow(showToast)

const submitAuth = () => {
  if (!authStore.authLoading) void handleAuthSubmit()
}

onMounted(() => {
  applyTheme(preferencesStore.darkModePreference)
  systemThemeQuery.addEventListener('change', followSystemTheme)

  // 有保存的密钥时先显示验证遮罩，避免登录框闪现；没有则直接显示登录框。
  const hasSavedKey = !!authStore.apiKey
  authStore.setAutoAuthenticating(hasSavedKey)
  authStore.setInitialized(!hasSavedKey)
  void autoAuthenticate()
})

onUnmounted(() => systemThemeQuery.removeEventListener('change', followSystemTheme))
</script>

<style scoped src="./styles/app-shell.css"></style>
<!-- 全站基础样式：页面与浮层统一使用主题色、间距和圆角令牌。 -->
<style src="./styles/app-global.css"></style>
