<template>
  <div class="relative isolate min-h-dvh" :class="isAuthenticated && navigationBelow ? '[--app-header-height:104px]' : '[--app-header-height:56px]'">
    <ThemeBackground />
    <a class="fixed top-2 left-4 z-50 -translate-y-[160%] rounded-control bg-primary px-4 py-3 text-primary-foreground focus:translate-y-0" href="#main-content">跳转到主要内容</a>

    <!-- 自动认证加载提示：只在使用已保存密钥验证时显示 -->
    <UiOverlay
      :model-value="authStore.isAutoAuthenticating && !authStore.isInitialized"
      persistent
      class="items-center justify-center"
      scrim="black"
    >
      <UiCard class="p-6 text-center" max-width="400" rounded="lg">
        <UiSpinner indeterminate :size="64" :width="6" color="primary" class="mb-4" />
        <div class="text-lg mb-2">正在验证访问权限</div>
        <div class="text-sm text-muted-foreground">使用保存的访问密钥进行身份验证...</div>
      </UiCard>
    </UiOverlay>

    <UiDialog v-model="showAuthDialog" persistent max-width="440" title="验证访问权限">
      <UiCard class="px-6 pt-7 pb-5">
        <div class="mb-5 flex items-center gap-3">
          <span class="grid size-9 shrink-0 place-items-center rounded-control bg-primary/10 text-primary"><UiIcon size="22">mdi-api</UiIcon></span>
          <div>
            <h1 class="text-lg font-semibold tracking-tight">API Proxy</h1>
            <p class="mt-1 text-[13px] text-muted-foreground">输入部署时设置的访问密钥进入管理控制台</p>
          </div>
        </div>

        <div class="p-0">
          <UiAlert v-if="authStore.authError" type="error" variant="tonal" class="mb-4">
            {{ authStore.authError }}
          </UiAlert>

          <UiForm @submit.prevent="submitAuth">
            <UiInput
              v-model="authStore.authKeyInput"
              label="访问密钥 (PROXY_ACCESS_KEY)"
              type="password"
              prepend-inner-icon="mdi-key"
              :rules="[v => !!v || '请输入访问密钥']"
              required
              autofocus
            />

            <UiButton type="submit" color="primary" variant="flat" block size="large" class="mt-4" :loading="authStore.authLoading">
              验证并进入
            </UiButton>
          </UiForm>
          <div class="mt-4 flex items-center justify-center gap-1.5 text-[13px] text-muted-foreground">
            <UiIcon size="15">mdi-shield-alert</UiIcon>
            <span>密钥仅保存在当前浏览器</span>
          </div>
        </div>
      </UiCard>
    </UiDialog>

    <!-- 顶栏平铺全部功能入口；宽度不足时导航下移到第二行，仍保持一次点击可达。 -->
    <AppHeader :extended="isAuthenticated && navigationBelow">
      <router-link to="/channels/messages" class="mr-2 flex shrink-0 items-center gap-2.5 font-semibold tracking-tight" aria-label="API Proxy 首页">
        <span class="grid size-8 place-items-center rounded-control bg-primary text-primary-foreground"><UiIcon size="20">mdi-api</UiIcon></span>
        <span class="hidden min-[601px]:inline">API Proxy</span>
      </router-link>

      <AppNavigation v-if="isAuthenticated && !navigationBelow" />
      <span v-else class="flex-1" ></span>

      <VersionBadge />

      <UiTooltip text="设置" location="bottom">
        <template #activator="{ props }">
          <UiButton
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
      </UiTooltip>

      <ThemeTrigger />

      <UiButton v-if="isAuthenticated" icon="mdi-logout" variant="text" size="small" title="注销" aria-label="注销" @click="handleLogout" />

      <template v-if="isAuthenticated && navigationBelow" #extension>
        <AppNavigation />
      </template>
    </AppHeader>

    <main id="main-content" class="relative min-w-0" tabindex="-1">
      <div class="mx-auto w-full max-w-[1680px] p-4 pb-20 md:p-6 md:pb-20">
        <!-- 认证后直接挂载目标页面，让懒加载与首屏请求立即开始。 -->
        <router-view v-if="isAuthenticated" v-slot="{ Component }">
          <div :key="route.path" class="motion-safe:animate-app-page">
            <component :is="Component" />
          </div>
        </router-view>
      </div>
    </main>

    <UiSnackbar
      v-for="(toast, index) in toasts"
      :key="toast.id"
      v-model="toast.show"
      :color="toast.type"
      :position="index"
      location="top right"
      variant="elevated"
    >
      <div class="flex items-center">
        <UiIcon class="mr-3">{{ toastIcons[toast.type] }}</UiIcon>
        {{ toast.message }}
      </div>
    </UiSnackbar>

    <!-- 危险命令 AI 闸门：全局待确认通知（轮询 + 声音 + 浏览器系统通知） -->
    <GatePendingNotification v-if="isAuthenticated" />
    <JsonViewerDrawer v-if="isAuthenticated" />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiSnackbar from '@/shared/ui/feedback/Snackbar.vue'
import UiSpinner from '@/shared/ui/feedback/Spinner.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiDialog from '@/shared/ui/overlay/Dialog.vue'
import UiOverlay from '@/shared/ui/overlay/Overlay.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
// 应用壳：认证、顶栏导航、主题与全局反馈。业务页面的内容与数据刷新由各自的视图负责。
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import AppHeader from '@/app/layout/AppHeader.vue'
import AppNavigation from '@/app/layout/AppNavigation.vue'
import VersionBadge from '@/app/layout/VersionBadge.vue'
import ThemeTrigger from '@/shared/theme/components/ThemeTrigger.vue'
import ThemeBackground from '@/shared/theme/components/ThemeBackground.vue'
import JsonViewerDrawer from '@/shared/components/data/JsonViewerDrawer.vue'
import GatePendingNotification from '@/features/observability/components/GatePendingNotification.vue'
import { useAuthFlow } from '@/app/useAuthFlow'
import { toastIcons, useToast } from '@/shared/composables/useToast'
import { useAuthStore } from '@/shared/stores/auth'

const route = useRoute()
const authStore = useAuthStore()
const { toasts, showToast } = useToast()

// 全部入口排成一行约需 1400px，不足时把导航放到顶栏第二行并允许横向滚动。
const navigationQuery = window.matchMedia('(width < 1440px)')
const navigationBelow = ref(navigationQuery.matches)
const updateNavigation = () => { navigationBelow.value = navigationQuery.matches }

// 认证流程由 useAuthFlow 编排（R5，方案 8.1），状态本体在 authStore。
const { isAuthenticated, showAuthDialog, autoAuthenticate, handleAuthSubmit, handleLogout } = useAuthFlow(showToast)

const submitAuth = () => {
  if (!authStore.authLoading) void handleAuthSubmit()
}

onMounted(() => {
  navigationQuery.addEventListener('change', updateNavigation)

  // 有保存的密钥时先显示验证遮罩，避免登录框闪现；没有则直接显示登录框。
  const hasSavedKey = !!authStore.apiKey
  authStore.setAutoAuthenticating(hasSavedKey)
  authStore.setInitialized(!hasSavedKey)
  void autoAuthenticate()
})

onUnmounted(() => navigationQuery.removeEventListener('change', updateNavigation))
</script>
