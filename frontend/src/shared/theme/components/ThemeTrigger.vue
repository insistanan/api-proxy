<template>
  <div class="flex shrink-0 items-center gap-1">
    <Button
      :icon="theme.isDark ? 'mdi-weather-night' : 'mdi-white-balance-sunny'"
      variant="text"
      size="small"
      :aria-label="theme.isDark ? '切换到亮色' : '切换到暗色'"
      @click="theme.update({ mode: theme.isDark ? 'light' : 'dark' })"
    />
    <Button
      icon="mdi-eye-dropper"
      variant="text"
      size="small"
      aria-label="外观主题"
      title="外观主题"
      @click="open = true"
    />
    <AppDialog v-model="open" title="外观主题"><ThemePanel /></AppDialog>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useTheme } from '@/shared/theme'
import Button from '@/shared/ui/button/Button.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import ThemePanel from './ThemePanel.vue'
const theme = useTheme()
const open = ref(false)
</script>
