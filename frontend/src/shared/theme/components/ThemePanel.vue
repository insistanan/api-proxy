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
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <button
          v-for="preset in THEME_PRESETS"
          :key="preset.id"
          type="button"
          class="overflow-hidden rounded-card border bg-card text-left transition hover:border-primary"
          :class="theme.preset === preset.id ? 'border-primary ring-2 ring-ring/20' : 'border-border'"
          :aria-pressed="theme.preset === preset.id"
          @click="theme.applyPreset(preset.id)"
        >
          <div
            :data-palette="preset.settings.palette"
            :data-radius="preset.settings.radius"
            :data-surface="preset.settings.surface"
            :data-decoration="preset.settings.decoration"
            :data-opacity="preset.settings.opacity"
            class="relative isolate flex h-16 items-center gap-2 overflow-hidden bg-muted px-4"
          >
            <span
              v-if="preset.settings.decoration !== 'none'"
              class="pointer-events-none absolute inset-0 -z-10 bg-primary/15"
              :class="preset.settings.decoration === 'waves' ? 'translate-y-7 -rotate-12 rounded-[50%]' : 'blur-lg'"
            ></span>
            <span class="panel-surface h-8 w-12"></span><span class="size-6 rounded-control bg-primary"></span>
          </div>
          <span class="block px-3 py-2 text-sm font-medium">{{ preset.name }}</span>
        </button>
      </div>
    </div>
    <div class="grid gap-4 sm:grid-cols-2">
      <label v-for="(options, key) in THEME_OPTIONS" :key="key" class="space-y-1 text-sm">
        <span class="block font-medium">{{ labels[key] }}</span>
        <select
          :value="theme.settings[key]"
          :disabled="key === 'opacity' && theme.settings.surface !== 'glass'"
          class="h-10 w-full rounded-control border border-input bg-card px-3 disabled:opacity-50"
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
