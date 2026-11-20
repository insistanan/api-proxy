<template>
  <nav ref="navElement" class="app-navigation" aria-label="主导航">
    <button v-if="overflowing" type="button" class="nav-scroll" :disabled="atStart" aria-label="查看前面的导航入口" @click="scrollNavigation(-1)">
      <UiIcon size="20">mdi-chevron-left</UiIcon>
    </button>
    <div ref="viewport" class="nav-viewport" @scroll.passive="updateScrollPosition">
      <div ref="track" class="nav-track">
        <div v-for="group in navigationGroups" :key="group.key" class="nav-group" role="group" :aria-label="group.label">
          <router-link v-for="item in group.items" :key="item.to" :to="item.to" class="nav-link" active-class="is-active">
            {{ item.label }}
          </router-link>
        </div>
      </div>
    </div>
    <button v-if="overflowing" type="button" class="nav-scroll" :disabled="atEnd" aria-label="查看后面的导航入口" @click="scrollNavigation(1)">
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
.nav-track { @apply flex items-center [width:max-content] [padding:5px]; }
.nav-group { @apply flex items-center gap-0.5 shrink-0; }
.nav-group + .nav-group { @apply ml-2 pl-2 border-l border-border/70; }
.nav-link { @apply flex items-center min-h-8.5 [padding:6px_9px] rounded-control text-muted-foreground text-[13px] font-medium leading-6 whitespace-nowrap no-underline transition-colors; }
.nav-link:hover { @apply text-foreground [background:color-mix(in_srgb,_var(--foreground)_5%,_transparent)]; }
.nav-link.is-active { @apply text-primary font-semibold bg-primary/10; }
.nav-scroll { @apply grid [place-items:center] shrink-0 w-8 h-9 rounded-control text-foreground [background:color-mix(in_srgb,_var(--foreground)_5%,_transparent)]; }
.nav-scroll:hover:not(:disabled) { @apply text-primary [background:color-mix(in_srgb,_var(--primary)_10%,_transparent)]; }
.nav-scroll:disabled { @apply opacity-30 cursor-default; }
.nav-scroll:focus-visible { @apply [outline-offset:-3px]; }
@media (pointer: coarse) { .nav-link { @apply min-h-11 px-3; } .nav-scroll { @apply w-11 h-11; } .nav-track { @apply py-0.5; } }
</style>
