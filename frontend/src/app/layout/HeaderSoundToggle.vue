<template>
  <UiButton
    :icon="preferences.headerSoundEnabled ? 'mdi-volume-high' : 'mdi-volume-off'"
    variant="text"
    size="small"
    class="header-tool header-sound"
    data-header-hit
    data-feedback-silent
    :aria-pressed="preferences.headerSoundEnabled"
    :aria-label="preferences.headerSoundEnabled ? '关闭界面音效' : '开启界面音效'"
    :title="preferences.headerSoundEnabled ? '界面音效已开启（首次点击后可播放）' : '界面音效已关闭'"
    @click="preferences.headerSoundEnabled = !preferences.headerSoundEnabled"
  />
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import { usePreferencesStore } from '@/shared/stores/preferences'

const preferences = usePreferencesStore()
</script>
