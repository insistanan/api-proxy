<template>
  <div
    ref="actionsRoot"
    class="quick-actions"
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
      <UiIcon :icon="expanded ? 'mdi-close' : 'mdi-dots-horizontal'" :size="24" />
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
import { defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { useRoute } from 'vue-router'
import { API_TABS } from '@/features/channels'
import { useDialogStore } from '@/features/channels/stores/dialog'
import UiIcon from '@/shared/ui/icon/Icon.vue'

const ChannelEditorDialog = defineAsyncComponent(() => import('@/features/channels/components/form/ChannelEditorDialog.vue'))
const ChannelSearchDialog = defineAsyncComponent(() => import('@/features/channels/components/dashboard/ChannelSearchDialog.vue'))
const route = useRoute()
const dialogStore = useDialogStore()
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
.quick-actions-trigger { @apply order-last flex size-12 items-center justify-center rounded-control border border-primary/20 bg-primary text-primary-foreground shadow-popover transition-[transform,box-shadow] duration-200 hover:shadow-dialog; }
.quick-actions-trigger[aria-expanded='true'] { transform: rotate(90deg); }
.quick-actions-list { @apply grid justify-items-end gap-2 pb-3; transform-origin: bottom right; }
.quick-action { @apply flex min-h-11 items-center gap-3 rounded-control border border-border bg-popover py-1.5 pr-1.5 pl-4 text-sm font-medium text-popover-foreground shadow-popover transition-colors hover:border-primary/40 hover:text-primary; }
.quick-action-icon { @apply flex size-8 shrink-0 items-center justify-center rounded-control bg-muted/60 text-primary; }
.quick-actions-reveal-enter-active { transition: opacity 240ms ease, transform 320ms var(--ease-out); }
.quick-actions-reveal-leave-active { transition: opacity 140ms ease, transform 180ms ease; }
.quick-actions-reveal-enter-from, .quick-actions-reveal-leave-to { opacity: 0; transform: translateY(12px) scale(.96); }
.quick-actions-reveal-enter-active .quick-action { animation: quick-action-in 300ms var(--ease-out) var(--action-delay) both; }
@keyframes quick-action-in { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) {
  .quick-actions-trigger[aria-expanded='true'] { transform: none; }
  .quick-actions-reveal-enter-active .quick-action { animation: none; }
}
</style>
