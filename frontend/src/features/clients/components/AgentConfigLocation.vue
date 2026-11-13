<template>
  <div class="agent-config-location">
    <v-icon size="17" class="agent-config-location__icon">mdi-file-cog-outline</v-icon>
    <span class="agent-config-location__label">{{ label }}</span>
    <code>{{ path || '路径不可用' }}</code>
    <slot></slot>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  label: string
  path?: string
}>()
</script>

<style scoped>
.agent-config-location { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; padding: 10px 14px; border: 1px solid var(--border-soft); border-radius: var(--cut-md); background: rgba(var(--v-theme-surface-variant), 0.5); }
.agent-config-location__icon { color: rgb(var(--v-theme-primary)); }
.agent-config-location__label { color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; white-space: nowrap; }
.agent-config-location code { padding: 2px 6px; border-radius: 4px; color: rgb(var(--v-theme-on-surface)); font-family: var(--font-mono); font-size: 0.75rem; background: rgba(var(--v-theme-primary), 0.07); word-break: break-all; }
</style>
