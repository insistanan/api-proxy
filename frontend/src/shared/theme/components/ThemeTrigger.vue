<template>
  <div class="shrink-0">
    <Button
      variant="text"
      size="small"
      class="header-tool theme-trigger"
      :class="{ 'is-current': open }"
      data-header-hit
      :aria-expanded="open"
      :aria-label="`外观主题：${themeName}，${modeName}`"
      :title="`${themeName} · ${modeName}`"
      prepend-icon="mdi-eye-dropper"
      @click="open = true"
    >
      <span class="theme-trigger-name">{{ themeName }}</span>
      <UiIcon class="theme-trigger-mode" size="16" :icon="theme.isDark ? 'mdi-weather-night' : 'mdi-white-balance-sunny'" />
    </Button>
    <AppDialog v-model="open" title="外观主题"><ThemePanel /></AppDialog>
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { THEME_OPTIONS, THEME_PRESETS, useTheme } from '@/shared/theme'
import Button from '@/shared/ui/button/Button.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import ThemePanel from './ThemePanel.vue'
const theme = useTheme()
const open = ref(false)
const themeName = computed(() => THEME_PRESETS.find(preset => preset.id === theme.preset)?.name ?? THEME_OPTIONS.palette[theme.settings.palette])
const modeName = computed(() => theme.settings.mode === 'system' ? `跟随系统（当前${theme.isDark ? '暗色' : '亮色'}）` : theme.isDark ? '暗色' : '亮色')
</script>

<style scoped>
.theme-trigger-name { white-space: nowrap; }
.theme-trigger-mode { margin-left: 1px; opacity: .78; }
</style>
