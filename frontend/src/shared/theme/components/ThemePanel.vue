<template>
  <section class="space-y-6" aria-label="外观设置">
    <div
      v-if="theme.error"
      role="alert"
      class="rounded-control border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
    >
      {{ theme.error }}
    </div>
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
            <span
              v-if="preset.settings.decoration !== 'none'"
              class="pointer-events-none absolute inset-0 -z-10 bg-primary/15"
              :class="preset.settings.decoration === 'waves' ? 'translate-y-14 -rotate-12 rounded-[50%]' : preset.settings.decoration === 'confetti' ? 'translate-x-14 -translate-y-14 rotate-12 rounded-full bg-accent/30' : 'blur-lg'"
            ></span>
            <div class="panel-surface flex h-full flex-col gap-2 p-2.5">
              <span class="flex items-center gap-1.5 border-b border-border/60 pb-2"><span class="size-2.5 rounded-full bg-primary"></span><span class="h-1.5 w-12 rounded-full bg-foreground/25"></span><span class="ml-auto h-3.5 w-7 rounded-badge bg-primary"></span></span>
              <span class="flex min-h-0 flex-1 gap-2"><span class="w-7 rounded-control bg-muted"></span><span class="flex flex-1 flex-col gap-1.5"><span class="h-2 w-3/4 rounded-full bg-primary/25"></span><span class="h-1.5 w-full rounded-full bg-muted"></span><span class="h-1.5 w-2/3 rounded-full bg-muted"></span></span></span>
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
    <p class="text-xs leading-6 text-muted-foreground">
      主题即时应用并保存在当前浏览器。透明度只影响玻璃表面，菜单和弹窗保持清晰；明暗模式可独立跟随系统。
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
import { THEME_OPTIONS, THEME_PRESETS, useTheme, type ThemeSettings } from '@/shared/theme'
import UiIcon from '@/shared/ui/icon/Icon.vue'
const theme = useTheme()
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
