<template>
  <div
    ref="actionsRoot"
    class="quick-actions"
    :data-quick-appearance="quickAppearance"
    :class="{ 'above-sticky-actions': route.name === 'settings' || route.name === 'pi-agent' }"
    @pointerenter="hoverOpen"
    @pointerleave="scheduleClose"
    @focusout="handleFocusOut"
    @keydown.esc.stop.prevent="closeAndFocus"
  >
    <button
      ref="trigger"
      type="button"
      class="quick-actions-trigger"
      aria-label="快捷操作"
      :aria-expanded="expanded"
      :aria-controls="expanded ? actionsId : undefined"
      @click="toggleActions"
      @focus="expandActions"
      @keydown.up.prevent="focusFirstAction"
      @keydown.down.prevent="focusFirstAction"
    >
      <UiIcon :icon="quickActionIcons[quickAppearance]" :size="24" class="quick-actions-mark" />
      <UiIcon icon="mdi-close" :size="20" class="quick-actions-close" />
    </button>
    <Transition name="quick-actions-reveal">
      <div v-if="expanded" :id="actionsId" class="quick-actions-list" role="group" aria-label="快捷操作">
        <button type="button" class="quick-action" style="--action-delay: 60ms" @click="openSearch">
          <span>搜索渠道</span><span class="quick-action-icon"><UiIcon icon="mdi-magnify" :size="20" /></span>
        </button>
        <button type="button" class="quick-action" style="--action-delay: 30ms" @click="openChannelEditor">
          <span>添加渠道</span><span class="quick-action-icon"><UiIcon icon="mdi-plus" :size="20" /></span>
        </button>
        <button v-if="canScrollToTop" type="button" class="quick-action" style="--action-delay: 0ms" @click="scrollToTop">
          <span>返回顶部</span><span class="quick-action-icon"><UiIcon icon="mdi-arrow-collapse-up" :size="20" /></span>
        </button>
      </div>
    </Transition>
  </div>

  <ChannelEditorDialog v-if="dialogStore.showAddChannelModal" />
  <ChannelSearchDialog v-if="searchOpen" v-model="searchOpen" />
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { useRoute } from 'vue-router'
import { API_TABS } from '@/features/channels'
import { useDialogStore } from '@/features/channels/stores/dialog'
import { useTheme, type ThemeSettings } from '@/shared/theme'
import UiIcon from '@/shared/ui/icon/Icon.vue'

const ChannelEditorDialog = defineAsyncComponent(() => import('@/features/channels/components/form/ChannelEditorDialog.vue'))
const ChannelSearchDialog = defineAsyncComponent(() => import('@/features/channels/components/dashboard/ChannelSearchDialog.vue'))
const route = useRoute()
const dialogStore = useDialogStore()
const theme = useTheme()
const quickActionIcons: Record<ThemeSettings['palette'] | 'glass', string> = {
  pop: 'mdi-shape',
  breeze: 'mdi-weather-windy',
  ink: 'mdi-compass-outline',
  ocean: 'mdi-waves',
  forest: 'mdi-leaf',
  sunset: 'mdi-weather-sunset',
  glass: 'mdi-layers-triple-outline',
  rose: 'mdi-view-grid-outline',
  sultan: 'mdi-seal-variant'
}
const quickAppearance = computed(() => {
  const { palette, surface } = theme.settings
  return palette === 'ink' && surface === 'glass' ? 'glass' : palette
})
const canScrollToTop = ref(false)
const expanded = ref(false)
const searchOpen = ref(false)
const actionsRoot = ref<HTMLElement>()
const trigger = ref<HTMLElement>()
const actionsId = useId()
let closeTimer: ReturnType<typeof setTimeout> | undefined
let pinned = false

function expandActions() {
  clearTimeout(closeTimer)
  expanded.value = true
}

function hoverOpen(event: PointerEvent) {
  if (event.pointerType === 'mouse') expandActions()
}

function scheduleClose() {
  clearTimeout(closeTimer)
  if (pinned || actionsRoot.value?.contains(document.activeElement)) return
  closeTimer = setTimeout(() => { expanded.value = false }, 180)
}

function handleFocusOut(event: { relatedTarget: unknown }) {
  if (event.relatedTarget instanceof Element && actionsRoot.value?.contains(event.relatedTarget)) return
  clearTimeout(closeTimer)
  if (!pinned) closeTimer = setTimeout(collapseActions, 180)
}

function collapseActions() {
  clearTimeout(closeTimer)
  pinned = false
  expanded.value = false
}

function toggleActions() {
  if (pinned) collapseActions()
  else { pinned = true; expandActions() }
}

function closeAndFocus() {
  trigger.value?.focus()
  collapseActions()
}

async function focusFirstAction() {
  expandActions()
  await nextTick()
  actionsRoot.value?.querySelector<HTMLElement>('.quick-action')?.focus()
}

function handleOutsidePointer(event: PointerEvent) {
  if (event.target instanceof Element && !actionsRoot.value?.contains(event.target)) collapseActions()
}

function openSearch() {
  collapseActions()
  trigger.value?.blur()
  searchOpen.value = true
}

function openChannelEditor() {
  collapseActions()
  trigger.value?.blur()
  const type = API_TABS.find(tab => tab === route.params.type) ?? 'messages'
  dialogStore.openAddChannelModal(type, 'default', true)
}

function updateScrollPosition() {
  canScrollToTop.value = window.scrollY > 24
}

function scrollToTop() {
  collapseActions()
  trigger.value?.blur()
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  window.scrollTo({ top: 0, behavior: reduceMotion ? 'instant' : 'smooth' })
}

onMounted(() => {
  updateScrollPosition()
  window.addEventListener('scroll', updateScrollPosition, { passive: true })
  document.addEventListener('pointerdown', handleOutsidePointer)
})

onUnmounted(() => {
  window.removeEventListener('scroll', updateScrollPosition)
  document.removeEventListener('pointerdown', handleOutsidePointer)
  clearTimeout(closeTimer)
  dialogStore.resetDialogState()
})

watch(() => route.fullPath, collapseActions)
</script>

<style scoped>
@reference "@/styles/index.css";

.quick-actions {
  @apply fixed z-30 flex flex-col items-end;
  right: max(1rem, env(safe-area-inset-right));
  bottom: calc(max(1rem, env(safe-area-inset-bottom)) + var(--quick-actions-offset, 0px));
}

.above-sticky-actions { --quick-actions-offset: 4.5rem; }
.quick-actions-trigger {
  @apply order-last grid size-12 place-items-center rounded-control border text-primary;
  background: var(--quick-fill, color-mix(in srgb, var(--primary) 8%, var(--popover)));
  border-color: color-mix(in srgb, var(--primary) 28%, var(--border));
  box-shadow: var(--quick-shadow, var(--shadow-card));
  transition: background 180ms ease, border-color 180ms ease, transform 220ms var(--ease-out);
}
.quick-actions-trigger:hover, .quick-actions-trigger[aria-expanded='true'] {
  border-color: color-mix(in srgb, var(--primary) 55%, var(--border));
}
.quick-actions-trigger:hover { transform: translateY(-2px); }
.quick-actions-trigger:active { transform: translateY(0); }
[data-quick-appearance='pop'] .quick-actions-trigger {
  --quick-fill: color-mix(in srgb, var(--primary) 16%, var(--popover));
  --quick-shadow: 2px 2px 0 var(--accent);
}
[data-quick-appearance='breeze'] .quick-actions-trigger {
  --quick-fill: color-mix(in srgb, var(--primary) 4%, var(--popover));
}
[data-quick-appearance='ocean'] .quick-actions-trigger {
  --quick-fill: linear-gradient(180deg, var(--popover) 20%, color-mix(in srgb, var(--primary) 18%, var(--popover)));
}
[data-quick-appearance='forest'] .quick-actions-trigger {
  --quick-fill: radial-gradient(circle at 30% 24%, color-mix(in srgb, var(--primary) 15%, var(--popover)), var(--popover) 85%);
}
[data-quick-appearance='sunset'] .quick-actions-trigger {
  --quick-fill: linear-gradient(150deg, color-mix(in srgb, var(--primary) 20%, var(--popover)), var(--popover) 75%);
}
[data-quick-appearance='glass'] .quick-actions-trigger {
  --quick-fill: var(--menu-surface);
  backdrop-filter: blur(min(var(--surface-blur), 10px));
}
[data-quick-appearance='rose'] .quick-actions-trigger {
  border-width: var(--surface-border-width);
}
[data-quick-appearance='sultan'] .quick-actions-trigger {
  --quick-fill: #642d38;
  --quick-shadow: inset 0 0 0 3px #47202a, inset 0 0 0 4px #b48f5670, 0 4px 12px #160e0d65;
  border-color: #b48f56;
  border-radius: 12px;
  corner-shape: bevel;
  color: #f2d7a7;
}
[data-quick-appearance='sultan'] .quick-actions-trigger:is(:hover, [aria-expanded='true']) {
  --quick-fill: #783744;
  border-color: #e6c58f;
}
[data-quick-appearance='sultan'] .quick-action {
  border-radius: 7px;
  corner-shape: bevel;
  border-color: var(--border);
  background-color: var(--surface);
}
[data-quick-appearance='sultan'] .quick-action-icon {
  border-radius: 5px;
  corner-shape: bevel;
  background-color: #642d38;
  color: #f2d7a7;
}
.quick-actions-mark, .quick-actions-close {
  grid-area: 1 / 1;
  transition: opacity 160ms ease, transform 220ms var(--ease-out);
}
.quick-actions-close { opacity: 0; transform: rotate(-30deg) scale(.8); }
.quick-actions-trigger[aria-expanded='true'] .quick-actions-mark { opacity: 0; transform: rotate(20deg) scale(.8); }
.quick-actions-trigger[aria-expanded='true'] .quick-actions-close { opacity: 1; transform: none; }
.quick-actions-list { @apply grid justify-items-end gap-2 pb-3; transform-origin: bottom right; }
.quick-action { @apply flex min-h-11 items-center gap-3 rounded-control border border-border bg-popover py-1.5 pr-1.5 pl-4 text-sm font-medium text-popover-foreground shadow-popover transition-colors hover:border-primary/40 hover:text-primary; }
.quick-action-icon { @apply flex size-8 shrink-0 items-center justify-center rounded-control bg-muted/60 text-primary; }
.quick-actions-reveal-enter-active { transition: opacity 240ms ease, transform 320ms var(--ease-out); }
.quick-actions-reveal-leave-active { transition: opacity 140ms ease, transform 180ms ease; }
.quick-actions-reveal-enter-from, .quick-actions-reveal-leave-to { opacity: 0; transform: translateY(12px) scale(.96); }
.quick-actions-reveal-enter-active .quick-action { animation: quick-action-in 300ms var(--ease-out) var(--action-delay) both; }
@keyframes quick-action-in { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) {
  .quick-actions-trigger, .quick-actions-trigger .quick-actions-mark, .quick-actions-trigger .quick-actions-close { transform: none !important; }
  .quick-actions-reveal-enter-active .quick-action { animation: none; }
}
</style>
