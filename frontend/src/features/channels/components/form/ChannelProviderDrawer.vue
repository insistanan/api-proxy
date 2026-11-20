<template>
  <Transition name="provider-drawer">
    <div v-if="modelValue" class="provider-layer">
      <div class="provider-scrim" aria-hidden="true" @click="close()"></div>
      <aside ref="panel" class="provider-panel" aria-label="常见渠道商" tabindex="-1">
        <header class="provider-heading">
          <div><h3>常见渠道商</h3><p>自动填入地址与上游协议</p></div>
          <UiButton icon="mdi-close" variant="text" size="small" aria-label="关闭渠道商列表" @click="close(true)" />
        </header>
        <div class="provider-search">
          <UiInput v-model="query" placeholder="搜索渠道商" aria-label="搜索渠道商" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable />
        </div>
        <div class="provider-list">
          <button v-for="item in items" :key="item.preset.id" type="button" class="provider-item" :disabled="!item.endpoint" @click="select(item.preset)">
            <span class="provider-mark" aria-hidden="true">{{ item.preset.mark }}</span>
            <span class="provider-info">
              <span class="provider-name">{{ item.preset.name }}</span>
              <span class="provider-protocols">{{ item.preset.endpoints.map(endpoint => presetProtocolLabels[endpoint.protocol]).join(' · ') }}</span>
              <span v-if="item.endpoint" class="provider-url">{{ item.endpoint.url }}</span>
              <span v-else class="provider-protocols">当前入口暂不支持此上游</span>
            </span>
            <span v-if="item.endpoint" class="provider-route" :class="{ 'is-native': item.endpoint.protocol === channelType }">
              {{ item.endpoint.protocol === channelType ? '原生' : `转 ${presetProtocolLabels[item.endpoint.protocol]}` }}
            </span>
          </button>
          <EmptyState v-if="!items.length" variant="inline" icon="mdi-magnify" title="没有匹配的渠道商" description="换个关键词，或清空搜索查看全部渠道商。" />
        </div>
        <footer>选择后填写 Key 即可保存，已有密钥和分组会保留。</footer>
      </aside>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import { computed, nextTick, ref, watch } from 'vue'
import type { ApiTab } from '@/features/channels/services/channel-api'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { CHANNEL_PRESETS, presetProtocolLabels, resolveChannelPreset, type ChannelPreset } from '@/features/channels/utils/channelPresets'

const props = defineProps<{ modelValue: boolean; channelType: ApiTab }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; select: [preset: ChannelPreset] }>()
const panel = ref<HTMLElement>()
const query = ref<string | null>('')
let returnFocus: HTMLElement | null = null
const items = computed(() => {
  const search = (query.value ?? '').trim().toLowerCase()
  return CHANNEL_PRESETS.filter(preset => `${preset.name} ${preset.id}`.toLowerCase().includes(search))
    .map(preset => ({ preset, endpoint: resolveChannelPreset(preset, props.channelType) }))
})

function close(restoreFocus = false) {
  emit('update:modelValue', false)
  if (restoreFocus && returnFocus?.isConnected) returnFocus.focus()
}
function select(preset: ChannelPreset) {
  emit('select', preset)
  close(true)
}

watch(() => props.modelValue, async (open, _previous, onCleanup) => {
  if (!open) return
  let cancelled = false
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  query.value = ''
  // 面板获得焦点后才监听移出，避免打开按钮自身的焦点让抽屉立即关闭。
  const focusOutside = (event: Event) => {
    if (event.target instanceof window.Node && !panel.value?.contains(event.target)) close()
  }
  const escape = (event: KeyboardEvent) => {
    if (event.key !== 'Escape') return
    event.preventDefault()
    event.stopImmediatePropagation()
    close(true)
  }
  const blur = () => close()
  onCleanup(() => {
    cancelled = true
    document.removeEventListener('focusin', focusOutside)
    document.removeEventListener('keydown', escape, true)
    window.removeEventListener('blur', blur)
  })
  await nextTick()
  if (cancelled) return
  panel.value?.querySelector('input')?.focus()
  document.addEventListener('focusin', focusOutside)
  document.addEventListener('keydown', escape, true)
  window.addEventListener('blur', blur)
})
</script>

<style scoped>
@reference "@/styles/index.css";

.provider-layer { @apply absolute [inset:0] [z-index:5] overflow-hidden [border-radius:inherit]; }
.provider-scrim { @apply absolute [inset:0] [background:rgba(0,_0,_0,_0.24)]; }
.provider-panel { @apply absolute [inset:0_0_0_auto] [width:min(440px,_100%)] flex flex-col [background:var(--surface)] [border-left:1px_solid_color-mix(in_srgb,_var(--outline)_25%,_transparent)] shadow-popover [outline:none]; }
.provider-heading { @apply flex items-center justify-between gap-3 [padding:22px_20px_16px]; }
.provider-heading h3 { @apply [font-size:1rem]; }
.provider-heading p, footer { @apply [font-size:0.75rem] [line-height:1.6] text-muted-foreground; }
.provider-heading p { @apply mt-1; }
.provider-search { @apply [padding:0_20px_16px]; }
.provider-list { @apply overflow-y-auto [flex:1] min-h-0 [padding:0_12px_12px]; }
.provider-item { @apply flex items-start gap-3 w-full [padding:16px_8px] [border-bottom:1px_solid_color-mix(in_srgb,_var(--outline)_16%,_transparent)] rounded-badge text-left [transition:background_120ms]; }
.provider-item:hover:not(:disabled), .provider-item:focus-visible { @apply [background:color-mix(in_srgb,_var(--primary)_8%,_transparent)]; }
.provider-item:focus-visible { @apply [outline:2px_solid_var(--primary)] [outline-offset:-2px]; }
.provider-item:disabled { @apply opacity-45 cursor-not-allowed; }
.provider-mark { @apply grid [place-items:center] w-8.5 h-8.5 shrink-0 rounded-control [background:color-mix(in_srgb,_var(--primary)_12%,_transparent)] text-primary font-bold; }
.provider-info { @apply flex flex-col min-w-0 [flex:1] gap-1; }
.provider-name { @apply font-semibold [font-size:0.875rem]; }
.provider-protocols, .provider-url { @apply [font-size:0.6875rem] [line-height:1.5] text-muted-foreground; }
.provider-url { @apply [overflow-wrap:anywhere]; }
.provider-route { @apply shrink-0 [padding:3px_5px] rounded-badge [font-size:0.625rem] [background:color-mix(in_srgb,_var(--foreground)_6%,_transparent)]; }
.provider-route.is-native { @apply text-success [background:color-mix(in_srgb,_var(--success)_10%,_transparent)]; }
footer { @apply [padding:14px_20px] [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)]; }
.provider-drawer-enter-active, .provider-drawer-leave-active { @apply [transition:opacity_180ms_ease]; }
.provider-drawer-enter-active .provider-panel, .provider-drawer-leave-active .provider-panel { @apply [transition:transform_180ms_ease]; }
.provider-drawer-enter-from, .provider-drawer-leave-to { @apply opacity-0; }
.provider-drawer-enter-from .provider-panel, .provider-drawer-leave-to .provider-panel { @apply [transform:translateX(100%)]; }
@media (prefers-reduced-motion: reduce) {
  .provider-layer, .provider-layer .provider-panel, .provider-item { @apply [transition:none]; }
}
</style>
