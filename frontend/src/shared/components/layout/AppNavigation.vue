<template>
  <nav ref="navElement" class="app-navigation" aria-label="主导航">
    <button v-if="overflowing" type="button" class="nav-scroll" :disabled="atStart" aria-label="查看前面的导航入口" @click="scrollNavigation(-1)">
      <v-icon size="20">mdi-chevron-left</v-icon>
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
      <v-icon size="20">mdi-chevron-right</v-icon>
    </button>
  </nav>
</template>

<script setup lang="ts">
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
.app-navigation { display: flex; align-items: center; flex: 1 1 auto; min-width: 0; gap: 4px; }
.nav-viewport { flex: 1; min-width: 0; overflow-x: auto; overscroll-behavior-x: contain; scrollbar-width: none; }
.nav-viewport::-webkit-scrollbar { display: none; }
.nav-track { display: flex; align-items: center; width: max-content; padding: 5px; }
.nav-group { display: flex; align-items: center; gap: 2px; flex-shrink: 0; }
.nav-group + .nav-group { margin-left: 10px; padding-left: 10px; border-left: 1px solid rgba(var(--v-theme-outline), 0.24); }
.nav-link { display: flex; align-items: center; min-height: 34px; padding: 6px 9px; border-radius: 7px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; font-weight: 600; line-height: 1.5; white-space: nowrap; text-decoration: none; transition: color 160ms ease, background 160ms ease; }
.nav-link:hover { color: rgb(var(--v-theme-on-surface)); background: rgba(var(--v-theme-on-surface), 0.05); }
.nav-link.is-active { color: rgb(var(--v-theme-primary)); background: rgba(var(--v-theme-primary), 0.12); box-shadow: inset 0 0 0 1px rgba(var(--v-theme-primary), 0.2); }
.nav-scroll { display: grid; place-items: center; flex-shrink: 0; width: 32px; height: 36px; border-radius: 7px; color: rgb(var(--v-theme-on-surface)); background: rgba(var(--v-theme-on-surface), 0.05); }
.nav-scroll:hover:not(:disabled) { color: rgb(var(--v-theme-primary)); background: rgba(var(--v-theme-primary), 0.1); }
.nav-scroll:disabled { opacity: 0.3; cursor: default; }
.nav-scroll:focus-visible { outline-offset: -3px; }
@media (pointer: coarse) { .nav-link { min-height: 44px; padding-inline: 12px; } .nav-scroll { width: 44px; height: 44px; } .nav-track { padding-block: 2px; } }
</style>
