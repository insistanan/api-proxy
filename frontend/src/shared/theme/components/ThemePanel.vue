<template>
  <section class="space-y-6" aria-label="外观设置">
    <UiAlert v-if="theme.error" type="error">
      {{ theme.error }}
    </UiAlert>
    <div>
      <h3 class="mb-3 text-sm font-semibold">
        一键主题 <span v-if="theme.preset === 'custom'" class="text-muted-foreground">· 自定义</span>
      </h3>
      <div class="grid grid-cols-2 gap-3">
        <button
          v-for="preset in THEME_PRESETS"
          :key="preset.id"
          type="button"
          class="group overflow-hidden rounded-card border bg-popover text-left transition-[border-color,box-shadow] hover:border-primary"
          :class="theme.preset === preset.id ? 'border-primary ring-2 ring-ring/20' : 'border-border'"
          :aria-pressed="theme.preset === preset.id"
          @click="theme.applyPreset(preset.id)"
        >
          <div
            :data-palette="preset.settings.palette"
            :data-theme-mode="theme.isDark ? 'dark' : 'light'"
            :data-radius="preset.settings.radius"
            :data-surface="preset.settings.surface"
            :data-decoration="preset.settings.decoration"
            :data-opacity="preset.settings.opacity"
            class="theme-preview relative isolate h-28 overflow-hidden bg-background p-3 text-foreground sm:h-32"
          >
            <ArabesquePattern v-if="preset.settings.decoration === 'arabesque'" class="pointer-events-none absolute inset-0 -z-10 size-full" />
            <span
              v-else-if="preset.settings.decoration !== 'none'"
              class="pointer-events-none absolute inset-0 -z-10 bg-primary/15"
              :class="preset.settings.decoration === 'waves' ? 'translate-y-14 -rotate-12 rounded-[50%]' : preset.settings.decoration === 'confetti' ? 'translate-x-14 -translate-y-14 rotate-12 rounded-full bg-accent/30' : 'blur-lg'"
            ></span>
            <div class="panel-surface flex h-full flex-col gap-2 p-2.5">
              <span class="flex items-center gap-1.5 border-b border-border/60 pb-2"><span class="size-2.5 rounded-full bg-primary"></span><span class="h-1.5 w-12 rounded-full bg-foreground/25"></span><span class="ml-auto h-3.5 w-7 rounded-badge bg-primary"></span></span>
              <span class="flex min-h-0 flex-1 gap-2" aria-hidden="true"><span class="w-7 rounded-control bg-muted"></span><span class="flex min-w-0 flex-1 flex-col gap-1.5"><span class="h-2 w-3/4 rounded-full bg-primary/25"></span><span class="theme-preview-meter h-1.5 w-full overflow-hidden rounded-full bg-muted"><span class="block h-full w-2/3 bg-success"></span></span><span class="theme-preview-activity flex min-h-2 flex-1 items-end gap-1"><span v-for="(height, index) in [40, 70, 50, 100, 65, 85, 55]" :key="index" class="w-1 bg-primary/45" :style="{ height: `${height}%` }"></span></span></span></span>
            </div>
          </div>
          <span class="flex items-center justify-between gap-2 px-3 pt-2.5 text-sm font-medium" :class="preset.description ? 'pb-1' : 'pb-2.5'">{{ preset.name }}<UiIcon v-if="theme.preset === preset.id" icon="mdi-check-circle" :size="16" class="text-primary" /></span>
          <span v-if="preset.description" class="block px-3 pb-2.5 text-[11px] leading-5 text-muted-foreground">{{ preset.description }}</span>
        </button>
      </div>
    </div>
    <div class="grid gap-4 sm:grid-cols-2">
      <label v-for="(options, key) in THEME_OPTIONS" :key="key" class="space-y-1 text-sm">
        <span class="block font-medium">{{ labels[key] }}</span>
        <select
          :value="theme.settings[key]"
          :disabled="key === 'opacity' && theme.settings.surface !== 'glass'"
          :data-disabled="(key === 'opacity' && theme.settings.surface !== 'glass') || undefined"
          class="field-surface h-10 w-full border-input px-3 disabled:opacity-50"
          @change="change(key, ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="(name, value) in options" :key="value" :value="value">{{ name }}</option>
        </select>
      </label>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4">
      <h3 class="text-sm font-semibold">界面音效</h3>
      <UiSwitch v-model="preferences.headerSoundEnabled" label="播放交互音效" />
    </div>
    <p class="text-xs leading-6 text-muted-foreground">
      主题即时应用并保存在当前浏览器。透明度选项调整玻璃卡片；鎏金文书使用纸张和金属质感。明暗模式可独立跟随系统。
    </p>
    <button
      type="button"
      class="rounded-control border border-border px-3 py-2 text-sm hover:bg-muted"
      @click="theme.reset()"
    >
      恢复默认外观
    </button>
  </section>
</template>
<script setup lang="ts">
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import { THEME_OPTIONS, THEME_PRESETS, useTheme, type ThemeSettings } from '@/shared/theme'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import ArabesquePattern from './ArabesquePattern.vue'
import { usePreferencesStore } from '@/shared/stores/preferences'
const theme = useTheme()
const preferences = usePreferencesStore()
const labels = {
  mode: '明暗模式',
  palette: '主题色',
  surface: '卡片质感',
  radius: '圆角',
  decoration: '背景装饰',
  opacity: '玻璃不透明度'
}
function change(key: keyof typeof THEME_OPTIONS, value: string) {
  theme.update({ [key]: key === 'opacity' ? Number(value) : value } as Partial<ThemeSettings>)
}
</script>

<style scoped>
.theme-preview[data-palette='pop'] .theme-preview-meter > span { background-image: repeating-linear-gradient(-55deg, transparent 0 4px, #fff7 4px 6px); }
.theme-preview[data-palette='pop'] .theme-preview-activity > span { border-radius: 3px 3px 0 0; background: var(--primary); }
.theme-preview[data-palette='breeze'] .theme-preview-activity > span,
.theme-preview[data-palette='ocean'] .theme-preview-activity > span { border-radius: 3px; }
.theme-preview[data-radius='square'] .theme-preview-activity > span { border-radius: 0; }
.theme-preview .panel-surface { background-image: none; border-color: var(--border); }
.theme-preview[data-surface='antique'] .panel-surface { background-image: var(--antique-card-background); border-color: var(--gold); background-size: 12px 12px, 12px 12px, 12px 12px, 12px 12px, 160px 160px, auto; }
</style>
