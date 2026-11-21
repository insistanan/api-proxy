<template>
  <nav ref="navElement" class="app-navigation" aria-label="主导航">
    <button v-if="overflowing" type="button" class="nav-scroll" data-header-hit :disabled="atStart" aria-label="查看前面的导航入口" @click="scrollNavigation(-1)">
      <UiIcon size="20">mdi-chevron-left</UiIcon>
    </button>
    <div ref="viewport" class="nav-viewport" @scroll.passive="updateScrollPosition">
      <div ref="track" class="nav-track">
        <div v-for="group in navigationGroups" :key="group.key" class="nav-group" :data-nav-group="group.key" role="group" :aria-label="group.label">
          <router-link v-for="item in group.items" :key="item.to" :to="item.to" class="nav-link" data-header-hit active-class="is-active">
            <span class="header-hit-label">{{ item.label }}</span>
          </router-link>
        </div>
      </div>
    </div>
    <button v-if="overflowing" type="button" class="nav-scroll" data-header-hit :disabled="atEnd" aria-label="查看后面的导航入口" @click="scrollNavigation(1)">
      <UiIcon size="20">mdi-chevron-right</UiIcon>
    </button>
  </nav>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { navigationGroups } from '@/router/navigation'

const route = useRoute()
const navElement = ref<HTMLElement>()
const viewport = ref<HTMLElement>()
const track = ref<HTMLElement>()
const overflowing = ref(false)
const atStart = ref(true)
const atEnd = ref(true)
let resizeObserver: ResizeObserver | undefined

function updateScrollPosition() {
  const element = viewport.value
  if (!element) return
  atStart.value = element.scrollLeft <= 1
  atEnd.value = element.scrollLeft + element.clientWidth >= element.scrollWidth - 1
}

function scrollNavigation(direction: number) {
  const element = viewport.value
  if (element) element.scrollBy({ left: direction * element.clientWidth * 0.8, behavior: 'instant' })
}

// 只滚动导航容器，切换页面或前进后退时不牵动正文的滚动位置。
async function revealActiveLink() {
  await nextTick()
  const element = viewport.value
  const active = track.value?.querySelector<HTMLElement>('.is-active')
  if (!element || !active) return
  const bounds = element.getBoundingClientRect()
  const link = active.getBoundingClientRect()
  const offset = link.left < bounds.left ? link.left - bounds.left : Math.max(0, link.right - bounds.right)
  element.scrollBy({ left: offset, behavior: 'instant' })
  updateScrollPosition()
}

watch(() => route.path, revealActiveLink)
onMounted(() => {
  resizeObserver = new ResizeObserver(() => {
    if (!navElement.value || !track.value) return
    // 按整个导航的宽度判断，避免箭头自身占位导致溢出状态反复切换。
    overflowing.value = track.value.scrollWidth > navElement.value.clientWidth + 1
    void revealActiveLink()
  })
  for (const element of [navElement.value, viewport.value, track.value]) {
    if (element) resizeObserver.observe(element)
  }
})
onUnmounted(() => resizeObserver?.disconnect())
</script>

<style scoped>
@reference "@/styles/index.css";

.app-navigation { @apply flex items-center [flex:1_1_auto] min-w-0 gap-1; }
.nav-viewport { @apply [flex:1] min-w-0 overflow-x-auto [overscroll-behavior-x:contain] [scrollbar-width:none]; }
.nav-viewport::-webkit-scrollbar { @apply hidden; }
.nav-track { @apply flex items-center gap-2 [width:max-content] [padding:6px_4px]; }
.nav-group {
  --nav-tone: var(--primary);
  --nav-tone-foreground: var(--primary-foreground);
  @apply relative flex shrink-0 items-center gap-0.5 rounded-card border p-1;
  border-color: color-mix(in srgb, var(--nav-tone) 16%, transparent);
  background: color-mix(in srgb, var(--nav-tone) 5%, var(--surface));
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--surface-bright) 45%, transparent);
}
.nav-group[data-nav-group='activity'] { --nav-tone: var(--info); --nav-tone-foreground: var(--info-foreground); }
.nav-group[data-nav-group='clients'] { --nav-tone: var(--warning); --nav-tone-foreground: var(--warning-foreground); }
.nav-group[data-nav-group='extensions'] { --nav-tone: var(--success); --nav-tone-foreground: var(--success-foreground); }
.nav-link { @apply relative isolate flex min-h-8.5 items-center overflow-hidden rounded-control [padding:5px_9px] text-[13px] font-medium leading-6 whitespace-nowrap no-underline; color: var(--muted-foreground); }
.nav-link::before { content: ''; position: absolute; inset: 0; z-index: -1; pointer-events: none; opacity: 0; background: linear-gradient(110deg, transparent 15%, color-mix(in srgb, var(--surface-bright) 25%, transparent) 50%, transparent 85%); transform: translateX(-100%); }
.nav-link:hover, .nav-link:focus-visible { color: var(--foreground); background: color-mix(in srgb, var(--nav-tone) 12%, transparent); }
.nav-link.is-active { color: var(--nav-tone-foreground); background: var(--nav-tone); font-weight: 750; box-shadow: inset 0 1px 0 #fff3, 0 2px 6px color-mix(in srgb, var(--nav-tone) 25%, transparent); }
.nav-link.is-active::after { content: ''; position: absolute; bottom: 3px; left: calc(50% - 7px); width: 14px; height: 2px; border-radius: 2px; background: currentColor; opacity: .7; }
.header-hit-label { display: inline-block; }
[data-palette='pop'] .nav-group { border-width: 1.5px; border-color: color-mix(in srgb, var(--nav-tone) 28%, transparent); box-shadow: 2px 2px 0 color-mix(in srgb, var(--accent) 25%, transparent); }
[data-palette='pop'] .nav-link.is-active { box-shadow: 2px 2px 0 var(--accent); }
[data-palette='breeze'] .nav-group { background: color-mix(in srgb, var(--nav-tone) 3%, var(--surface)); }
[data-radius='square'] .nav-group { border-bottom-width: 2px; }
@media (prefers-reduced-motion: no-preference) and (hover: hover) {
  .nav-link[data-pointer-active='true']::before { opacity: .8; animation: nav-glint 600ms var(--ease-out) 1; }
  .nav-link[data-hover-speed='swift']::before { animation-duration: 300ms; }
  .nav-link[data-hover-speed='slow']::before { animation-duration: 850ms; }
}
@keyframes nav-glint { to { transform: translateX(100%); } }
.nav-scroll { @apply grid [place-items:center] shrink-0 w-8 h-9 rounded-control text-foreground [background:color-mix(in_srgb,_var(--foreground)_5%,_transparent)]; }
.nav-scroll:hover:not(:disabled) { @apply text-primary [background:color-mix(in_srgb,_var(--primary)_10%,_transparent)]; }
.nav-scroll:disabled { @apply opacity-30 cursor-default; }
.nav-scroll:focus-visible { @apply [outline-offset:-3px]; }
@media (pointer: coarse) { .nav-link { @apply min-h-11 px-3; } .nav-scroll { @apply w-11 h-11; } .nav-track { @apply py-0.5; } }
</style>
