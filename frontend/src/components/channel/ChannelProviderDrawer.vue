<template>
  <Transition name="provider-drawer">
    <div v-if="modelValue" class="provider-layer">
      <div class="provider-scrim" aria-hidden="true" @click="close()"></div>
      <aside ref="panel" class="provider-panel" aria-label="常见渠道商" tabindex="-1">
        <header class="provider-heading">
          <div><h3>常见渠道商</h3><p>自动填入地址与上游协议</p></div>
          <v-btn icon="mdi-close" variant="text" size="small" aria-label="关闭渠道商列表" @click="close(true)" />
        </header>
        <div class="provider-search">
          <v-text-field v-model="query" placeholder="搜索渠道商" aria-label="搜索渠道商" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable />
        </div>
        <div class="provider-list">
          <button v-for="item in items" :key="item.preset.id" type="button" class="provider-item" :disabled="!item.endpoint" @click="select(item.preset)">
            <span class="provider-mark" aria-hidden="true">{{ item.preset.mark }}</span>
            <span class="provider-info">
              <span class="provider-name">{{ item.preset.name }} <small v-if="item.preset.region">{{ item.preset.region }}</small></span>
              <span class="provider-protocols">{{ item.preset.endpoints.map(endpoint => presetProtocolLabels[endpoint.protocol]).join(' · ') }}</span>
              <span v-if="item.endpoint" class="provider-url">{{ item.endpoint.url }}</span>
              <span v-else class="provider-protocols">当前入口暂不支持此上游</span>
            </span>
            <span v-if="item.endpoint" class="provider-route" :class="{ 'is-native': item.endpoint.protocol === channelType }">
              {{ item.endpoint.protocol === channelType ? '原生' : `转 ${presetProtocolLabels[item.endpoint.protocol]}` }}
            </span>
          </button>
          <p v-if="!items.length" class="provider-empty">没有匹配的渠道商</p>
        </div>
        <footer>选择后填写 Key 即可保存，已有密钥和分组会保留。</footer>
      </aside>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import { CHANNEL_PRESETS, presetProtocolLabels, resolveChannelPreset, type ChannelPreset } from '@/utils/channelPresets'

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
.provider-layer { position: absolute; inset: 0; z-index: 5; overflow: hidden; border-radius: inherit; }
.provider-scrim { position: absolute; inset: 0; background: rgba(0, 0, 0, 0.24); }
.provider-panel { position: absolute; inset: 0 0 0 auto; width: min(440px, 100%); display: flex; flex-direction: column; background: rgb(var(--v-theme-surface)); border-left: 1px solid rgba(var(--v-theme-outline), 0.25); box-shadow: -8px 0 28px rgba(0, 0, 0, 0.16); outline: none; }
.provider-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 22px 20px 16px; }
.provider-heading h3 { font-size: 1rem; }
.provider-heading p, footer { font-size: 0.75rem; line-height: 1.6; color: rgb(var(--v-theme-on-surface-variant)); }
.provider-heading p { margin-top: 4px; }
.provider-search { padding: 0 20px 16px; }
.provider-list { overflow-y: auto; flex: 1; min-height: 0; padding: 0 12px 12px; }
.provider-item { display: flex; align-items: flex-start; gap: 12px; width: 100%; padding: 16px 8px; border-bottom: 1px solid rgba(var(--v-theme-outline), 0.16); border-radius: 4px; text-align: left; transition: background 120ms; }
.provider-item:hover:not(:disabled), .provider-item:focus-visible { background: rgba(var(--v-theme-primary), 0.08); }
.provider-item:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: -2px; }
.provider-item:disabled { opacity: 0.45; cursor: not-allowed; }
.provider-mark { display: grid; place-items: center; width: 34px; height: 34px; flex-shrink: 0; border-radius: 10px; background: rgba(var(--v-theme-primary), 0.12); color: rgb(var(--v-theme-primary)); font-weight: 700; }
.provider-info { display: flex; flex-direction: column; min-width: 0; flex: 1; gap: 4px; }
.provider-name { font-weight: 600; font-size: 0.875rem; }
.provider-name small { font-weight: 400; font-size: 0.6875rem; color: rgb(var(--v-theme-on-surface-variant)); }
.provider-protocols, .provider-url { font-size: 0.6875rem; line-height: 1.5; color: rgb(var(--v-theme-on-surface-variant)); }
.provider-url { overflow-wrap: anywhere; }
.provider-route { flex-shrink: 0; padding: 3px 5px; border-radius: 4px; font-size: 0.625rem; background: rgba(var(--v-theme-on-surface), 0.06); }
.provider-route.is-native { color: rgb(var(--v-theme-success)); background: rgba(var(--v-theme-success), 0.1); }
.provider-empty { padding: 32px 12px; text-align: center; font-size: 0.875rem; }
footer { padding: 14px 20px; border-top: 1px solid rgba(var(--v-theme-outline), 0.2); }
.provider-drawer-enter-active, .provider-drawer-leave-active { transition: opacity 180ms ease; }
.provider-drawer-enter-active .provider-panel, .provider-drawer-leave-active .provider-panel { transition: transform 180ms ease; }
.provider-drawer-enter-from, .provider-drawer-leave-to { opacity: 0; }
.provider-drawer-enter-from .provider-panel, .provider-drawer-leave-to .provider-panel { transform: translateX(100%); }
@media (prefers-reduced-motion: reduce) {
  .provider-layer, .provider-layer .provider-panel, .provider-item { transition: none; }
}
</style>
