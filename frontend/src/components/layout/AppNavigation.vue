<template>
  <v-navigation-drawer v-model="open" :width="224" mobile-breakpoint="md" class="app-navigation">
    <router-link to="/channels/messages" class="navigation-brand">
      <span class="navigation-brand__icon"><v-icon size="22">mdi-api</v-icon></span>
      <span>API Proxy<small>模型网关 · 管理面板</small></span>
    </router-link>
    <nav aria-label="主导航">
      <v-list v-model:opened="openedGroups" nav bg-color="transparent" class="navigation-list">
        <v-list-group v-for="group in navigationGroups" :key="group.key" :value="group.key">
          <template #activator="{ props }">
            <v-list-item v-bind="props" :prepend-icon="group.icon" :title="group.label" />
          </template>
          <v-list-item
            v-for="item in group.items"
            :key="item.to"
            :to="item.to"
            :title="item.label"
            :active="route.path === item.to"
            :aria-current="route.path === item.to ? 'page' : undefined"
            color="primary"
          />
        </v-list-group>
        <v-divider class="my-3" />
        <v-list-item to="/settings" prepend-icon="mdi-cog" title="设置" color="primary" :aria-current="route.path === '/settings' ? 'page' : undefined" />
      </v-list>
    </nav>
    <template #append><div class="navigation-footnote">统一接入 · 集中管理</div></template>
  </v-navigation-drawer>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { findNavigation, navigationGroups } from '@/router/navigation'

const open = defineModel<boolean | null>({ default: null })
const route = useRoute()
const openedGroups = ref<string[]>([])

// 深链接和浏览器前进后退也展开当前分组，用户手动展开的其他分组保持原状。
watch(() => route.path, path => {
  const key = findNavigation(path)?.groupKey
  if (key && key !== 'settings' && !openedGroups.value.includes(key)) openedGroups.value.push(key)
}, { immediate: true })
</script>

<style scoped>
.app-navigation { border-right: 1px solid rgba(var(--v-theme-outline), 0.2); }
.navigation-brand { display: flex; align-items: center; gap: 12px; min-height: 88px; padding: 20px; color: inherit; text-decoration: none; font-size: 1.125rem; font-weight: 750; }
.navigation-brand__icon { display: grid; place-items: center; width: 36px; height: 36px; flex-shrink: 0; color: rgb(var(--v-theme-on-primary)); background: rgb(var(--v-theme-primary)); border-radius: 10px; }
.navigation-brand small { display: block; margin-top: 3px; font-size: 0.75rem; font-weight: 400; color: rgb(var(--v-theme-on-surface-variant)); }
.navigation-list { padding: 0 12px 16px; }
.navigation-list :deep(.v-list-item) { min-height: 42px; margin-bottom: 4px; border-radius: 8px; }
.navigation-list :deep(.v-list-item-title) { font-size: 0.875rem; }
.navigation-list :deep(.v-list-item__prepend > .v-icon) { font-size: 20px; margin-inline-end: 12px; }
.navigation-list :deep(.v-list-group__items .v-list-item) { padding-inline-start: 44px !important; min-height: 38px; }
.navigation-footnote { padding: 16px 20px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.75rem; border-top: 1px solid rgba(var(--v-theme-outline), 0.16); }
@media (pointer: coarse) { .navigation-list :deep(.v-list-item) { min-height: 44px; } }
</style>
