<template>
  <nav ref="navElement" class="app-navigation" aria-label="主导航">
    <div v-for="group in navigationGroups" :key="group.key" class="nav-group" role="group" :aria-label="group.label">
      <router-link v-for="item in group.items" :key="item.to" :to="item.to" class="nav-link" active-class="is-active">
        {{ item.label }}
      </router-link>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { navigationGroups } from '@/router/navigation'

const route = useRoute()
const navElement = ref<HTMLElement>()

// 窄屏下导航横向滚动；深链接或前进后退后把当前入口滚入可视范围。
watch(() => route.path, async () => {
  await nextTick()
  navElement.value?.querySelector('.is-active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
}, { immediate: true, flush: 'post' })
</script>

<style scoped>
.app-navigation { display: flex; align-items: center; gap: 6px; flex: 1 1 auto; min-width: 0; overflow-x: auto; scrollbar-width: none; }
.app-navigation::-webkit-scrollbar { display: none; }
.nav-group { display: flex; align-items: center; gap: 2px; flex: 0 0 auto; padding: 3px; border-radius: 10px; background: rgba(var(--v-theme-on-surface), 0.05); }
.nav-link { padding: 5px 7px; border-radius: 7px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; font-weight: 550; line-height: 1.5; white-space: nowrap; text-decoration: none; transition: color 160ms ease, background 160ms ease; }
.nav-link:hover { color: rgb(var(--v-theme-on-surface)); background: rgba(var(--v-theme-on-surface), 0.07); }
.nav-link.is-active { color: rgb(var(--v-theme-on-primary)); background: rgb(var(--v-theme-primary)); font-weight: 650; }
@media (pointer: coarse) { .nav-link { padding: 9px 12px; } }
</style>
